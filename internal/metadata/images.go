package metadata

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Image is one entry of TMDB's images lists.
type Image struct {
	FilePath    string  `json:"file_path"`
	Language    string  `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
}

// Images is GET /movie/{id}/images or /tv/{id}/images.
type Images struct {
	Posters   []Image `json:"posters"`
	Backdrops []Image `json:"backdrops"`
	Logos     []Image `json:"logos"`
}

// GetImages fetches a film's or show's images (kind is "movie" or "tv"),
// including those with no language and those in English, next to the ones in
// the language the client reads titles in. The answer is kept for a few
// minutes and must not be modified.
func (c *Client) GetImages(ctx context.Context, kind string, tmdbID int) (*Images, error) {
	if kind != "movie" && kind != "tv" {
		return nil, fmt.Errorf("images: unknown kind %q", kind)
	}
	key := fmt.Sprintf("images:%s:%d:%s", kind, tmdbID, c.lang())
	if v, ok := c.pages.get(key); ok {
		return v.(*Images), nil
	}
	lang := baseLanguage(c.lang())
	include := lang + ",en,null"
	if lang == "en" {
		include = "en,null"
	}
	var img Images
	q := url.Values{"include_image_language": {include}, "language": {lang}}
	if err := c.get(ctx, fmt.Sprintf("/%s/%d/images", kind, tmdbID), q, &img); err != nil {
		return nil, err
	}
	c.pages.put(key, &img)
	return &img, nil
}

// baseLanguage is the two-letter part of a language tag ("de-DE" gives "de").
func baseLanguage(tag string) string {
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		tag = tag[:i]
	}
	if tag == "" {
		return "en"
	}
	return strings.ToLower(tag)
}

// BestLogo picks the logo to use as clearlogo.png: the best rated PNG in the
// preferred language, else English, else one with no language, else any. SVG
// logos are left out (a media server wants a bitmap). "" when there is none.
func BestLogo(logos []Image, lang string) string {
	lang = baseLanguage(lang)
	rank := func(l Image) int {
		switch strings.ToLower(l.Language) {
		case lang:
			return 0
		case "en":
			return 1
		case "":
			return 2
		default:
			return 3
		}
	}
	best, bestRank := -1, 99
	for i, l := range logos {
		if !strings.HasSuffix(strings.ToLower(l.FilePath), ".png") {
			continue
		}
		r := rank(l)
		switch {
		case best < 0 || r < bestRank:
			best, bestRank = i, r
		case r == bestRank && (l.VoteAverage > logos[best].VoteAverage || (l.VoteAverage == logos[best].VoteAverage && l.VoteCount > logos[best].VoteCount)):
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return logos[best].FilePath
}

// ImageHost is where TMDB serves pictures from.
const ImageHost = "https://image.tmdb.org"

// OriginalImageURL is the full-size picture for a TMDB file path
// ("/abc123.jpg"), or "" for a path that does not look like one.
func OriginalImageURL(path string) string {
	if !ValidImagePath(path) {
		return ""
	}
	return ImageHost + "/t/p/original" + path
}

// ValidImagePath reports whether path is a plain TMDB image path: a slash, a
// file name of letters, digits, dashes and underscores, and a picture extension.
func ValidImagePath(path string) bool {
	if len(path) < 6 || len(path) > 100 || path[0] != '/' {
		return false
	}
	dot := strings.LastIndexByte(path, '.')
	if dot < 2 {
		return false
	}
	switch strings.ToLower(path[dot+1:]) {
	case "jpg", "jpeg", "png":
	default:
		return false
	}
	for _, r := range path[1:dot] {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}
