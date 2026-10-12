// Package configfile keeps a Mediarium's settings in one YAML file, for people
// who want them in git or in a deploy script. It does not touch the database:
// it reads and writes through the same API the Settings pages use, so every
// value is checked exactly as if it had been typed in the page.
//
// The file has one block per settings page that holds a single set of values:
//
//	settings:        # Settings > General, Library, Downloading, Info, ...
//	downloadClient:  # Settings > Downloading > Torrents > Use your own torrent client
//	debrid:          # Settings > Downloading > Torrents > Debrid service
//	proxy:           # Settings > Downloading > Proxy
//	systemOptions:   # Settings > System > Updates
//
// Anything not in the file is left as it is. Secrets are written as
// ${NAME}, which is replaced with the environment variable NAME when the file
// is applied, so they never have to be in the file.
package configfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Section is one block of the file and the API address that holds it.
type Section struct {
	Name string
	Path string
}

// Sections are the blocks a file may have, in the order they are applied.
var Sections = []Section{
	{"settings", "/api/settings"},
	{"downloadClient", "/api/download-client"},
	{"debrid", "/api/debrid"},
	{"proxy", "/api/proxy"},
	{"systemOptions", "/api/system/options"},
}

func sectionNames() string {
	names := make([]string, len(Sections))
	for i, s := range Sections {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

// Client talks to a running Mediarium as an administrator.
type Client struct {
	BaseURL string // for example http://127.0.0.1:8264
	Key     string // an administrator's API key (Profile page); empty when HTTP already carries a session
	HTTP    *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) do(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return nil, fmt.Errorf("bad Mediarium address %q: %w", c.BaseURL, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Key != "" {
		req.Header.Set("X-API-Key", c.Key)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach Mediarium at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		msg := ""
		if json.Unmarshal(data, &e) == nil {
			msg = e.Error
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			if msg == "" {
				msg = "Mediarium refused the request"
			}
			return nil, fmt.Errorf("%s (use an administrator's API key, made on the Profile page)", strings.TrimRight(msg, ". "))
		}
		if msg != "" {
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("Mediarium answered HTTP %d", resp.StatusCode)
	}
	var out map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("Mediarium's answer for %s wasn't what was expected", path)
		}
	}
	return out, nil
}

// derived are values a page shows but that are worked out, not chosen: they
// are left out of an export (and ignored by Mediarium if a file has them).
var derived = regexp.MustCompile(`^(has[A-Z].*|.*BuiltIn|.*UsingOwnKey|canCheck|legalAcknowledgedAt|onboardingDone)$`)

// Export reads every block from the running Mediarium and writes the file.
func Export(ctx context.Context, c *Client) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Mediarium settings. Apply with: mediarium config apply <this file>\n")
	buf.WriteString("# Keep only the lines you want to control: anything not here is left alone.\n")
	buf.WriteString("# Passwords and keys are never exported. Write them as ${NAME} to read them from the environment.\n\n")
	for _, s := range Sections {
		cur, err := c.do(ctx, http.MethodGet, s.Path, nil)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", s.Name, err)
		}
		for k := range cur {
			if derived.MatchString(k) {
				delete(cur, k)
			}
		}
		var node yaml.Node
		if err := node.Encode(map[string]any{s.Name: cur}); err != nil {
			return nil, err
		}
		out, err := yaml.Marshal(&node)
		if err != nil {
			return nil, err
		}
		buf.Write(out)
		buf.WriteString("\n")
	}
	return buf.Bytes(), nil
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expand replaces ${NAME} and ${NAME:-default} in every text value.
func expand(v any, env func(string) (string, bool), where string) (any, error) {
	switch t := v.(type) {
	case string:
		var missing string
		out := envRef.ReplaceAllStringFunc(t, func(m string) string {
			parts := envRef.FindStringSubmatch(m)
			if val, ok := env(parts[1]); ok && val != "" {
				return val
			}
			if strings.Contains(m, ":-") {
				return parts[2]
			}
			missing = parts[1]
			return ""
		})
		if missing != "" {
			return nil, fmt.Errorf("%s uses ${%s}, but %s isn't set", where, missing, missing)
		}
		return out, nil
	case map[string]any:
		for k, x := range t {
			nx, err := expand(x, env, where+"."+k)
			if err != nil {
				return nil, err
			}
			t[k] = nx
		}
		return t, nil
	case []any:
		for i, x := range t {
			nx, err := expand(x, env, fmt.Sprintf("%s[%d]", where, i))
			if err != nil {
				return nil, err
			}
			t[i] = nx
		}
		return t, nil
	}
	return v, nil
}

// File is a parsed settings file: block name -> its values.
type File map[string]map[string]any

// Parse reads a file. env looks up ${NAME}; pass os.LookupEnv.
func Parse(data []byte, env func(string) (string, bool)) (File, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("the file isn't valid YAML: %w", err)
	}
	known := map[string]bool{}
	for _, s := range Sections {
		known[s.Name] = true
	}
	out := File{}
	for name, v := range raw {
		if !known[name] {
			return nil, fmt.Errorf("%q isn't a block Mediarium knows. The blocks are: %s", name, sectionNames())
		}
		if v == nil {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q must be a list of name: value lines", name)
		}
		ex, err := expand(m, env, name)
		if err != nil {
			return nil, err
		}
		out[name] = ex.(map[string]any)
	}
	return out, nil
}

// ReadFile reads and parses a file from disk, taking ${NAME} from the environment.
func ReadFile(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, os.LookupEnv)
}

// Change is one value that differs.
type Change struct {
	Section string
	Key     string
	Have    string // "" for a value Mediarium doesn't show back (a password, a key)
	Want    string
	Secret  bool // not shown back by Mediarium, so it is always sent
}

func (c Change) String() string {
	if c.Secret {
		return fmt.Sprintf("%s.%s: (secret) will be set", c.Section, c.Key)
	}
	return fmt.Sprintf("%s.%s: %s -> %s", c.Section, c.Key, c.Have, c.Want)
}

// same compares a value from the file with one from Mediarium, ignoring that
// YAML has ints where JSON has floats.
func same(a, b any) bool {
	na, nb := normal(a), normal(b)
	return reflect.DeepEqual(na, nb)
}

func normal(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// Result is what happened to one block.
type Result struct {
	Section string
	Changes []Change
	Err     error
	Applied bool
}

// Plan compares the file with the running Mediarium without changing anything.
func Plan(ctx context.Context, c *Client, f File) ([]Result, error) {
	return run(ctx, c, f, false)
}

// Apply makes the running Mediarium match the file. Blocks are applied one by
// one; one that Mediarium refuses is reported and the rest still go through.
func Apply(ctx context.Context, c *Client, f File) ([]Result, error) {
	return run(ctx, c, f, true)
}

// writeOnly are the values a page accepts but never shows back, per block:
// passwords, keys and the like. A name that is neither shown nor in this list
// is a typo, and the file is refused.
var writeOnly = map[string]map[string]bool{
	"settings": {
		"tmdbApiKey": true, "openSubtitlesApiKey": true, "subdlApiKey": true,
		"openSubtitlesUsername": true, "openSubtitlesPassword": true, "traktClientId": true,
	},
	"downloadClient": {"password": true},
	"debrid":         {"apiKey": true},
	"proxy":          {"password": true},
}

func run(ctx context.Context, c *Client, f File, apply bool) ([]Result, error) {
	var results []Result
	var bodies = map[string]map[string]any{}
	var unknown []string
	for _, s := range Sections {
		want, ok := f[s.Name]
		if !ok || len(want) == 0 {
			continue
		}
		res := Result{Section: s.Name}
		cur, err := c.do(ctx, http.MethodGet, s.Path, nil)
		if err != nil {
			res.Err = fmt.Errorf("read %s: %w", s.Name, err)
			results = append(results, res)
			continue
		}
		keys := make([]string, 0, len(want))
		for k := range want {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		body := map[string]any{}
		for k, v := range cur {
			body[k] = v
		}
		for _, k := range keys {
			have, shown := cur[k]
			if !shown {
				if !writeOnly[s.Name][k] {
					unknown = append(unknown, s.Name+"."+k)
					continue
				}
				res.Changes = append(res.Changes, Change{Section: s.Name, Key: k, Secret: true})
				body[k] = want[k]
				continue
			}
			if !same(have, want[k]) {
				res.Changes = append(res.Changes, Change{Section: s.Name, Key: k, Have: show(have), Want: show(want[k])})
				body[k] = want[k]
			}
		}
		bodies[s.Name] = body
		results = append(results, res)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("these aren't settings Mediarium has (check the spelling; nothing was changed): %s", strings.Join(unknown, ", "))
	}
	if apply {
		for i := range results {
			r := &results[i]
			if r.Err != nil || len(r.Changes) == 0 {
				continue
			}
			var path string
			for _, s := range Sections {
				if s.Name == r.Section {
					path = s.Path
				}
			}
			if _, err := c.do(ctx, http.MethodPut, path, bodies[r.Section]); err != nil {
				r.Err = fmt.Errorf("%s: %w", r.Section, err)
			} else {
				r.Applied = true
			}
		}
	}
	return results, nil
}
