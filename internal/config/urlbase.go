package config

import (
	"fmt"
	"strings"
)

// reservedFirstSegments are the first parts of addresses Mediarium itself
// answers (the API, the Radarr/Sonarr/Lidarr-style APIs, built files and the
// pages of the web app). A URL base starting with one of them could not be
// told apart from the real thing.
var reservedFirstSegments = map[string]bool{
	"api": true, "radarr": true, "sonarr": true, "lidarr": true, "metrics": true, "robots.txt": true,
	"assets": true, "fonts": true, "demo": true, "share": true, "bookshelf": true, "music-player": true,
	"search": true, "stats": true, "import": true, "discover": true, "library": true, "music": true,
	"book": true, "books": true, "title": true, "series": true, "show": true, "wanted": true,
	"calendar": true, "queue": true, "settings": true, "profile": true, "about": true,
}

// NormalizeURLBase turns the URL_BASE a person typed into the form the app
// uses: a leading slash, no trailing slash, and "" for the root. "mediarium",
// "/mediarium" and "/mediarium/" all give "/mediarium". It returns an error
// (a sentence for the person) for a value that cannot be a path prefix.
func NormalizeURLBase(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" || strings.Trim(v, "/") == "" {
		return "", nil
	}
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("URL_BASE is %q, but %s. Use a path like /mediarium, or leave it empty to serve Mediarium at the root", raw, why)
	}
	if len(v) > 100 {
		return bad("it is too long")
	}
	v = "/" + strings.Trim(v, "/")
	if strings.Contains(v, "//") {
		return bad("it has an empty part between two slashes")
	}
	segments := strings.Split(v[1:], "/")
	for _, seg := range segments {
		if seg == "." || seg == ".." {
			return bad(`it has "." or ".." in it`)
		}
		for _, r := range seg {
			ok := r == '-' || r == '_' || r == '.' || r == '~' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
			if !ok {
				return bad("it can only have letters, numbers, dashes, underscores and dots")
			}
		}
	}
	if reservedFirstSegments[strings.ToLower(segments[0])] {
		return bad(fmt.Sprintf("%q is a name Mediarium already uses for its own pages", segments[0]))
	}
	return v, nil
}
