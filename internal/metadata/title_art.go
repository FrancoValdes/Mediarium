package metadata

import (
	"context"
	"sort"
)

// Artwork is the picture TMDB has for a title in the calendar's Backdrops
// view: a wide still without text on it, and the title's logo. Both are image
// paths like "/abc.jpg", empty when TMDB has none.
type Artwork struct {
	Backdrop string
	Logo     string
}

// TitleArtwork picks the backdrop and the logo of a movie (kind "movie") or a
// show (kind "tv") from TMDB's images list.
func (c *Client) TitleArtwork(ctx context.Context, kind string, tmdbID int) (Artwork, error) {
	imgs, err := c.GetImages(ctx, kind, tmdbID)
	if err != nil {
		return Artwork{}, err
	}
	return Artwork{Backdrop: bestWideImage(imgs.Backdrops), Logo: BestLogo(imgs.Logos, c.lang())}, nil
}

// bestWideImage prefers a still with no text on it, then a big one, then the
// best rated.
func bestWideImage(list []Image) string {
	sorted := append([]Image(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if (a.Language == "") != (b.Language == "") {
			return a.Language == ""
		}
		if (a.Width >= 1280) != (b.Width >= 1280) {
			return a.Width >= 1280
		}
		return a.VoteAverage > b.VoteAverage
	})
	if len(sorted) == 0 {
		return ""
	}
	return sorted[0].FilePath
}

// WideURL builds the URL of a backdrop at the size the calendar uses.
func WideURL(path string) string {
	if !ValidImagePath(path) {
		return ""
	}
	return ImageHost + "/t/p/w780" + path
}

// LogoURL builds the URL of a title logo.
func LogoURL(path string) string {
	if !ValidImagePath(path) {
		return ""
	}
	return ImageHost + "/t/p/w300" + path
}
