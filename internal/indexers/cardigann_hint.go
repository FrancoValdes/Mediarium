package indexers

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// cookiePageHint says what the site showed instead of a signed-in page, so a
// member can tell a browser check from a sign-in form from an error page. It
// never includes the cookie or the query part of the address.
func cookiePageHint(resp *cgResponse, doc *html.Node) string {
	body := ""
	if resp != nil {
		body = strings.ToLower(string(resp.body))
	}
	where := "/"
	if resp != nil && resp.url != nil && resp.url.Path != "" {
		where = resp.url.Path
	}
	switch {
	case strings.Contains(body, "just a moment") || strings.Contains(body, "challenge-platform") || strings.Contains(body, "cf-chl"):
		return ". The site showed a browser check (Cloudflare): set up FlareSolverr, or copy a cookie that includes cf_clearance."
	case pageHas(doc, `input[type="password"]`):
		return ". The site showed its sign-in page at " + where + ", so it did not recognise the cookie: check that it holds the uid and pass values, and that the User-Agent is the one from the same browser."
	}
	title := strings.Join(strings.Fields(pageText(doc, "title")), " ")
	if len(title) > 80 {
		title = title[:80] + "..."
	}
	if title == "" {
		return ". The site answered at " + where + " without the signed-in link."
	}
	return ". The site answered at " + where + " with the page " + strconv.Quote(title) + "."
}

func pageHas(doc *html.Node, selector string) bool {
	sel, err := CompileSelector(selector)
	if err != nil {
		return false
	}
	return sel.First(doc) != nil
}

func pageText(doc *html.Node, selector string) string {
	sel, err := CompileSelector(selector)
	if err != nil {
		return ""
	}
	if n := sel.First(doc); n != nil {
		return strings.TrimSpace(textContent(n))
	}
	return ""
}
