package indexers

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"reflect"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
)

// Cardigann definitions embed a small Go-template-like language
// ({{ .Keywords }}, {{ if .Config.x }}...{{ else }}...{{ end }}, {{ range }},
// {{ join }}, {{ re_replace }}...). Go's text/template runs nearly all of it;
// rewriteTemplate papers over the differences:
//
//   - string literals in Cardigann are raw ("\d" means backslash-d), so every
//     "..." inside an action becomes an equivalent Go literal;
//   - names Go can't lex (.Config.2facode, .Config.sort-by) become index calls;
//   - stray unbalanced parentheses, which Cardigann's own engine ignores, are
//     dropped;
//   - every printing action is piped through a modifier, so values placed in
//     a URL path can be URL-encoded (Cardigann encodes variables there).

// tmplVars is the variable set a template sees.
type tmplVars struct {
	Config      map[string]string
	Query       map[string]string
	Result      map[string]string
	Keywords    string
	Categories  []string
	DownloadURI map[string]any
	now         time.Time
}

func newTmplVars() *tmplVars {
	return &tmplVars{
		Config: map[string]string{},
		Query:  map[string]string{},
		Result: map[string]string{},
	}
}

func (v *tmplVars) data() map[string]any {
	now := v.now
	if now.IsZero() {
		now = time.Now()
	}
	d := map[string]any{
		"Config":     v.Config,
		"Query":      v.Query,
		"Result":     v.Result,
		"Keywords":   v.Keywords,
		"Categories": v.Categories,
		"Today":      map[string]string{"Year": strconv.Itoa(now.Year())},
		"True":       checkboxTrue,
		"False":      "",
	}
	if v.DownloadURI != nil {
		d["DownloadUri"] = v.DownloadURI
	} else {
		d["DownloadUri"] = map[string]any{"Query": map[string]string{}}
	}
	return d
}

// checkboxTrue is what a ticked checkbox setting reads as, and what .True
// is; an unticked one is "", like .False, so {{ if .Config.x }} and
// {{ eq .Config.x .False }} both behave as definitions expect.
const checkboxTrue = ".True"

var (
	tmplCacheMu sync.Mutex
	tmplCache   = map[string]*template.Template{}
)

var tmplFuncs = template.FuncMap{
	"join":       tmplJoin,
	"re_replace": tmplReReplace,
	"__mod":      func(s any) any { return s }, // replaced per execution
	"__range":    func(v any) any { return v }, // replaced per execution
}

// applyTemplate renders a Cardigann template string. encode, if set, is
// applied to every printed value (used for URL paths).
func applyTemplate(src string, vars *tmplVars, encode func(string) string) (string, error) {
	if !strings.Contains(src, "{{") {
		return src, nil
	}
	rewritten, err := rewriteTemplate(src)
	if err != nil {
		return "", fmt.Errorf("template %q: %w", src, err)
	}
	t, err := compileTemplate(rewritten)
	if err != nil {
		return "", fmt.Errorf("template %q: %w", src, err)
	}
	run := &tmplRun{deadline: time.Now().Add(maxTemplateTime), loopsLeft: maxTemplateLoops}
	mod := func(s any) (any, error) { return s, run.check() }
	if encode != nil {
		mod = func(s any) (any, error) { return encode(fmt.Sprint(s)), run.check() }
	}
	t, err = t.Clone()
	if err != nil {
		return "", fmt.Errorf("template %q: %w", src, err)
	}
	t.Funcs(run.funcs(mod))
	w := &limitedWriter{max: maxTemplateOutput}
	if err := t.Execute(w, vars.data()); err != nil {
		return "", fmt.Errorf("template %q: %w", src, err)
	}
	return strings.ReplaceAll(w.sb.String(), "<no value>", ""), nil
}

// A template can build text in variables, where the output limit never sees
// it, and loop over whatever a variable holds. tmplRun keeps one execution
// within bounds: every loop is counted, every function that builds text is
// size-checked, and the whole run has a deadline.
type tmplRun struct {
	deadline  time.Time
	loopsLeft int
}

const (
	// maxTemplateTime is how long one template may run. Real ones take
	// microseconds.
	maxTemplateTime = time.Second
	// maxRangeItems is the longest list one {{ range }} may loop over.
	maxRangeItems = 10000
	// maxTemplateLoops is how many loop turns one template may take in all.
	maxTemplateLoops = 100000
)

var (
	errTemplateTooSlow  = errors.New("the template takes too long")
	errTemplateTooLoopy = errors.New("the template loops too many times")
)

func (r *tmplRun) check() error {
	if time.Now().After(r.deadline) {
		return errTemplateTooSlow
	}
	return nil
}

// funcs are the functions one execution sees: the built-in ones that build
// text are replaced with size-checked versions.
func (r *tmplRun) funcs(mod func(any) (any, error)) template.FuncMap {
	return template.FuncMap{
		"__mod":   mod,
		"__range": r.rangeOver,
		"printf":  r.printf,
		"print": func(args ...any) (string, error) {
			if err := r.fits(len(args), args); err != nil {
				return "", err
			}
			return fmt.Sprint(args...), nil
		},
		"println": func(args ...any) (string, error) {
			if err := r.fits(len(args)+1, args); err != nil {
				return "", err
			}
			return fmt.Sprintln(args...), nil
		},
		// escaping makes text up to six times longer
		"html":     r.escaper(template.HTMLEscaper),
		"js":       r.escaper(template.JSEscaper),
		"urlquery": r.escaper(template.URLQueryEscaper),
		"join": func(list any, sep string) (string, error) {
			if err := r.check(); err != nil {
				return "", err
			}
			out := tmplJoin(list, sep)
			if len(out) > maxTemplateOutput {
				return "", errTemplateTooLong
			}
			return out, nil
		},
		"re_replace": func(s any, pattern, replacement string) (string, error) {
			if err := r.check(); err != nil {
				return "", err
			}
			return tmplReReplace(s, pattern, replacement)
		},
	}
}

// rangeOver is what every {{ range }} loops over (see rewriteAction): a list
// or map, or a small number, counted against the run's loop allowance.
func (r *tmplRun) rangeOver(v any) (any, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(v)
	var n int64
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		n = int64(rv.Len())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n = rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if rv.Uint() > maxRangeItems {
			n = maxRangeItems + 1
		} else {
			n = int64(rv.Uint())
		}
	default:
		return nil, fmt.Errorf("{{ range }} can only loop over a list, not a %s", rv.Kind())
	}
	if n > maxRangeItems {
		return nil, fmt.Errorf("{{ range }} can loop over at most %d items", maxRangeItems)
	}
	if n > 0 {
		r.loopsLeft -= int(n)
	}
	if r.loopsLeft < 0 {
		return nil, errTemplateTooLoopy
	}
	return v, nil
}

// printf is fmt.Sprintf that refuses to build more than maxTemplateOutput:
// widths and precisions are read from the format before anything is built.
func (r *tmplRun) printf(format string, args ...any) (string, error) {
	if err := r.check(); err != nil {
		return "", err
	}
	verbs, widths, ok := formatCost(format)
	if !ok {
		return "", errTemplateTooLong
	}
	longest := 0
	for _, a := range args {
		longest = max(longest, approxLen(a))
	}
	if int64(len(format))+int64(verbs)*int64(longest)+widths > maxTemplateOutput {
		return "", errTemplateTooLong
	}
	return fmt.Sprintf(format, args...), nil
}

// fits checks that printing args (with extra separators) stays within the
// output limit.
func (r *tmplRun) fits(extra int, args []any) error {
	if err := r.check(); err != nil {
		return err
	}
	total := int64(extra)
	for _, a := range args {
		total += int64(approxLen(a))
	}
	if total > maxTemplateOutput {
		return errTemplateTooLong
	}
	return nil
}

func (r *tmplRun) escaper(esc func(...any) string) func(...any) (string, error) {
	return func(args ...any) (string, error) {
		if err := r.fits(len(args), args); err != nil {
			return "", err
		}
		total := 0
		for _, a := range args {
			total += approxLen(a)
		}
		if total*6 > maxTemplateOutput {
			return "", errTemplateTooLong
		}
		return esc(args...), nil
	}
}

// approxLen is about how long a value is once printed. Strings are the only
// values a template can make long; the rest come from the definition.
func approxLen(a any) int {
	switch v := a.(type) {
	case string:
		return len(v)
	case nil, bool, int, int64, float64:
		return 32
	}
	return len(fmt.Sprint(a))
}

// formatCost reads a printf format: how many verbs it has and the sum of the
// widths and precisions it asks for. ok is false for a width taken from an
// argument (*) or one too large to be real.
func formatCost(format string) (verbs int, widths int64, ok bool) {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		verbs++
		for i++; i < len(format); i++ {
			c := format[i]
			if c == '*' {
				return 0, 0, false
			}
			if c >= '0' && c <= '9' {
				j := i
				for j < len(format) && format[j] >= '0' && format[j] <= '9' {
					j++
				}
				if j-i > 7 {
					return 0, 0, false
				}
				n, _ := strconv.Atoi(format[i:j])
				widths += int64(n)
				i = j - 1
				continue
			}
			if !strings.ContainsRune("+-# .[]", rune(c)) {
				break // the verb itself
			}
		}
	}
	return verbs, widths, true
}

// checkTemplate reports whether src can be compiled, without running it.
func checkTemplate(src string) error {
	if !strings.Contains(src, "{{") {
		return nil
	}
	rewritten, err := rewriteTemplate(src)
	if err != nil {
		return err
	}
	_, err = compileTemplate(rewritten)
	return err
}

func compileTemplate(src string) (*template.Template, error) {
	tmplCacheMu.Lock()
	t, ok := tmplCache[src]
	tmplCacheMu.Unlock()
	if ok {
		return t, nil
	}
	t, err := template.New("cardigann").Funcs(tmplFuncs).Option("missingkey=zero").Parse(src)
	if err != nil {
		return nil, err
	}
	tmplCacheMu.Lock()
	if len(tmplCache) > 4096 {
		tmplCache = map[string]*template.Template{}
	}
	tmplCache[src] = t
	tmplCacheMu.Unlock()
	return t, nil
}

var controlWords = map[string]bool{
	"if": true, "else": true, "end": true, "range": true, "with": true, "define": true,
	"template": true, "block": true, "break": true, "continue": true,
}

var badFieldRe = regexp.MustCompile(`\.(Config|Result|Query)\.([A-Za-z0-9_\-]+)`)
var goIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// rewriteTemplate converts Cardigann template syntax into Go template syntax.
func rewriteTemplate(src string) (string, error) {
	var out strings.Builder
	rest := src
	var blocks []string // kinds of the blocks that are open: "if", "with", "range"
	for {
		i := strings.Index(rest, "{{")
		if i < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:i])
		rest = rest[i+2:]
		// find the closing }} outside string literals
		end := -1
		inStr := false
		for j := 0; j < len(rest)-1; j++ {
			if rest[j] == '"' {
				inStr = !inStr
				continue
			}
			if !inStr && rest[j] == '}' && rest[j+1] == '}' {
				end = j
				break
			}
		}
		if end < 0 {
			return "", fmt.Errorf("unterminated {{ action")
		}
		action := rest[:end]
		rest = rest[end+2:]
		text, keyword, err := rewriteAction(action)
		if err != nil {
			return "", err
		}
		switch keyword {
		case "if", "with", "range":
			blocks = append(blocks, keyword)
			if n := countKind(blocks, "range"); n > maxRangeNesting {
				return "", fmt.Errorf("loops are nested more than %d deep", maxRangeNesting)
			}
		case "end":
			if len(blocks) > 0 {
				blocks = blocks[:len(blocks)-1]
			}
		}
		out.WriteString("{{")
		out.WriteString(text)
		out.WriteString("}}")
	}
}

// maxRangeNesting is how many loops a template may nest. Definitions loop over
// the categories being searched and nothing else; the limit keeps a crafted
// template from running for ever.
const maxRangeNesting = 2

// maxTemplateOutput is the most text one template may produce.
const maxTemplateOutput = 1 << 20

func countKind(list []string, kind string) int {
	n := 0
	for _, k := range list {
		if k == kind {
			n++
		}
	}
	return n
}

// limitedWriter stops a template that produces too much text.
type limitedWriter struct {
	sb  strings.Builder
	max int
}

var errTemplateTooLong = errors.New("the template produces too much text")

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.sb.Len()+len(p) > w.max {
		return 0, errTemplateTooLong
	}
	return w.sb.Write(p)
}

// rangeOverNumber reports whether the code of a range action holds a number or
// character literal: Go templates loop that many times ("range 1000000000").
func rangeOverNumber(code string) bool {
	inStr := false
	prev := byte(' ')
	for i := 0; i < len(code); i++ {
		c := code[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			prev = c
			continue
		}
		switch {
		case c == '"':
			inStr = true
		case c == '\'' || c == '`':
			return true
		case c >= '0' && c <= '9':
			isName := prev == '.' || prev == '$' || prev == '_' || (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9')
			if !isName {
				return true
			}
		}
		prev = c
	}
	return false
}

func rewriteAction(action string) (text, keyword string, err error) {
	leftTrim, rightTrim := "", ""
	body := action
	if strings.HasPrefix(body, "-") && len(body) > 1 && (body[1] == ' ' || body[1] == '\t' || body[1] == '\n') {
		leftTrim = "- "
		body = body[2:]
	}
	if strings.HasSuffix(body, "-") && len(body) > 1 {
		if c := body[len(body)-2]; c == ' ' || c == '\t' || c == '\n' {
			rightTrim = " -"
			body = body[:len(body)-2]
		}
	}

	// Split into string literals and code, rewriting only code.
	var sb strings.Builder
	depth := 0
	for len(body) > 0 {
		q := strings.IndexByte(body, '"')
		code := body
		if q >= 0 {
			code = body[:q]
		}
		code = badFieldRe.ReplaceAllStringFunc(code, func(m string) string {
			parts := badFieldRe.FindStringSubmatch(m)
			if goIdentRe.MatchString(parts[2]) {
				return m
			}
			return fmt.Sprintf("(index .%s %s)", parts[1], strconv.Quote(parts[2]))
		})
		// drop unbalanced closing parentheses
		for _, r := range code {
			switch r {
			case '(':
				depth++
			case ')':
				if depth == 0 {
					continue
				}
				depth--
			}
			sb.WriteRune(r)
		}
		if q < 0 {
			break
		}
		body = body[q+1:]
		e := strings.IndexByte(body, '"')
		if e < 0 { // unterminated literal: keep as is
			sb.WriteString(strconv.Quote(body))
			body = ""
			break
		}
		sb.WriteString(strconv.Quote(body[:e]))
		body = body[e+1:]
	}
	code := sb.String() + strings.Repeat(")", depth)

	trimmed := strings.TrimSpace(code)
	first := trimmed
	// the first word ends at anything that is not part of a name, so
	// "range.Categories" is the word "range" followed by a field
	if k := strings.IndexFunc(first, func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}); k >= 0 {
		first = first[:k]
	}
	switch first {
	case "define", "template", "block":
		return "", "", fmt.Errorf("{{ %s }} is not supported", first)
	case "range":
		rest := strings.TrimPrefix(trimmed, "range")
		if rangeOverNumber(rest) {
			return "", "", fmt.Errorf("{{ range }} can only loop over a list, not a number")
		}
		// a variable can still hold a number or a huge list: every loop goes
		// through __range, which checks what it is given
		decl := rangeDeclRe.FindString(rest)
		if pipe := strings.TrimSpace(rest[len(decl):]); pipe != "" {
			code = " range " + strings.TrimSpace(decl) + " __range (" + pipe + ") "
		}
	}
	if controlWords[first] {
		keyword = first
	}
	isOutput := trimmed != "" && !controlWords[first] && !strings.HasPrefix(trimmed, "/*") && !declRe.MatchString(trimmed)
	if isOutput {
		code = " " + trimmed + " | __mod "
	}
	return leftTrim + code + rightTrim, keyword, nil
}

var declRe = regexp.MustCompile(`^\$[A-Za-z0-9_]*\s*(:=|=)`)

// rangeDeclRe is the "$i, $e :=" a range action may start with.
var rangeDeclRe = regexp.MustCompile(`^\s*\$[A-Za-z0-9_]*\s*(,\s*\$[A-Za-z0-9_]*\s*)?(:=|=)`)

func tmplJoin(list any, sep string) string {
	switch l := list.(type) {
	case []string:
		return strings.Join(l, sep)
	case []any:
		parts := make([]string, len(l))
		for i, x := range l {
			parts[i] = fmt.Sprint(x)
		}
		return strings.Join(parts, sep)
	case string:
		return l
	case nil:
		return ""
	}
	return fmt.Sprint(list)
}

func tmplReReplace(s any, pattern, replacement string) (string, error) {
	re, err := compileDotNetRegex(pattern)
	if err != nil {
		if errors.Is(err, errRegexTooBig) {
			return "", err
		}
		warnSkippedRegex(err)
		return fmt.Sprint(s), nil
	}
	return regexReplaceMax(re, fmt.Sprint(s), replacement, maxTemplateOutput)
}

var (
	skippedRegexMu sync.Mutex
	skippedRegex   = map[string]bool{}
)

// warnSkippedRegex logs (once per pattern) that a text replacement was
// skipped because Go's regular expressions can't express it; the value is
// left unchanged rather than failing the whole result.
func warnSkippedRegex(err error) {
	skippedRegexMu.Lock()
	defer skippedRegexMu.Unlock()
	if skippedRegex[err.Error()] || len(skippedRegex) > 256 {
		return
	}
	skippedRegex[err.Error()] = true
	log.Printf("cardigann: skipping a text replacement: %v", err)
}

// unicodeBlocks maps .NET named blocks to the closest Go script.
var unicodeBlocks = map[string]string{
	"IsCyrillic": "Cyrillic", "IsCyrillicSupplement": "Cyrillic", "IsGreek": "Greek",
	"IsGreekandCoptic": "Greek", "IsArabic": "Arabic", "IsHebrew": "Hebrew", "IsThai": "Thai",
	"IsCJKUnifiedIdeographs": "Han", "IsHiragana": "Hiragana", "IsKatakana": "Katakana",
	"IsHangulSyllables": "Hangul", "IsArmenian": "Armenian", "IsGeorgian": "Georgian",
	"IsDevanagari": "Devanagari",
}

// translateDotNetRegex rewrites the .NET-only escapes definitions use into
// RE2 syntax: a \u followed by four hex digits, and \p{IsBlock} classes.
func translateDotNetRegex(p string) string {
	if !strings.Contains(p, `\`) {
		return p
	}
	var sb strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c != '\\' || i+1 >= len(p) {
			sb.WriteByte(c)
			continue
		}
		n := p[i+1]
		switch {
		case n >= 0x80:
			// .NET lets any character be escaped; RE2 only punctuation.
			continue
		case n == 'u' && i+5 < len(p) && isHex(p[i+2:i+6]):
			sb.WriteString(`\x{` + p[i+2:i+6] + `}`)
			i += 5
		case (n == 'p' || n == 'P') && i+2 < len(p) && p[i+2] == '{':
			if end := strings.IndexByte(p[i:], '}'); end > 0 {
				if script, ok := unicodeBlocks[p[i+3:i+end]]; ok {
					sb.WriteString(`\` + string(n) + `{` + script + `}`)
					i += end
					continue
				}
			}
			sb.WriteString(p[i : i+2])
			i++
		default:
			sb.WriteString(p[i : i+2])
			i++
		}
	}
	return sb.String()
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// --- regular expressions ---

// compiledRegex is a cached compile: the expression and its cost per byte of
// text, or why it can't be used.
type compiledRegex struct {
	re     *regexp.Regexp
	weight int64
	err    error
}

var (
	regexCacheMu sync.Mutex
	regexCache   = map[string]compiledRegex{}
)

const (
	// maxRegexSteps is the largest compiled pattern accepted. Real definitions
	// use patterns of a few hundred steps.
	maxRegexSteps = 10000
	// maxRegexLength is the longest pattern compiled at all. Real ones are a
	// few hundred characters.
	maxRegexLength = 4 << 10
)

// errRegexTooBig marks a pattern refused for its size, not its dialect: one
// that would cost too much to run rather than one Go can't express.
var errRegexTooBig = errors.New("the regular expression is too complex")

// compileDotNetRegex compiles a regular expression written for .NET (the
// dialect Cardigann definitions target) with Go's RE2 engine. Constructs RE2
// lacks (look-around, back-references, atomic groups) fail with a clear
// error.
func compileDotNetRegex(pattern string) (*regexp.Regexp, error) {
	c := compileDotNetRegexCost(pattern)
	return c.re, c.err
}

// compileDotNetRegexCost is compileDotNetRegex with the pattern's cost per
// byte of text it runs over. Failures are cached too, so a bad pattern used on
// every row is only looked at once.
func compileDotNetRegexCost(pattern string) compiledRegex {
	regexCacheMu.Lock()
	c, ok := regexCache[pattern]
	regexCacheMu.Unlock()
	if ok {
		return c
	}
	c = buildDotNetRegex(pattern)
	regexCacheMu.Lock()
	if len(regexCache) > 4096 {
		regexCache = map[string]compiledRegex{}
	}
	regexCache[pattern] = c
	regexCacheMu.Unlock()
	return c
}

func buildDotNetRegex(pattern string) compiledRegex {
	if len(pattern) > maxRegexLength {
		return compiledRegex{err: fmt.Errorf("regular expression %q is too long: %w", shortenText(pattern, 60), errRegexTooBig)}
	}
	for _, bad := range []struct{ tok, what string }{
		{"(?=", "look-ahead"}, {"(?!", "negative look-ahead"}, {"(?<=", "look-behind"},
		{"(?<!", "negative look-behind"}, {"(?>", "atomic group"},
	} {
		if strings.Contains(pattern, bad.tok) {
			return compiledRegex{err: fmt.Errorf("regular expression %q uses a %s, which is not supported", pattern, bad.what)}
		}
	}
	translated := translateDotNetRegex(pattern)
	// Matching takes time in proportion to the pattern's size times the text's:
	// a pattern of a million steps run over a page of 16 MB never finishes. The
	// size is measured before the real compile, which is the costly part.
	steps := 0
	if parsed, perr := syntax.Parse(translated, syntax.Perl); perr == nil {
		if prog, cerr := syntax.Compile(parsed.Simplify()); cerr == nil {
			steps = len(prog.Inst)
		}
	}
	if steps > maxRegexSteps {
		return compiledRegex{err: fmt.Errorf("regular expression %q: %w", shortenText(pattern, 60), errRegexTooBig)}
	}
	re, err := regexp.Compile(translated)
	if err != nil {
		return compiledRegex{err: fmt.Errorf("regular expression %q is not supported: %w", pattern, err)}
	}
	return compiledRegex{re: re, weight: 1 + int64(steps)/16}
}

var dotNetGroupRef = regexp.MustCompile(`\$(\d+)`)

// dotNetReplacement turns .NET's $1 into Go's ${1} so "$1a" keeps meaning
// group 1 followed by "a".
func dotNetReplacement(s string) string {
	return dotNetGroupRef.ReplaceAllString(s, "$${$1}")
}

// urlPathEscape encodes a value placed into a URL path the way Cardigann
// does (form encoding: spaces become '+').
func urlPathEscape(s string) string { return url.QueryEscape(s) }
