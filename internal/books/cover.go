package books

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// maxCoverBytes bounds one downloaded cover; a medium or large Open Library
// cover is well under a megabyte.
const maxCoverBytes = 4 << 20

// openLibraryCovers is where Open Library serves covers from. Each answer there
// is a redirect to an archive.org server, which is why covers are slow to
// appear when the browser asks for them one by one.
const openLibraryCovers = "https://covers.openlibrary.org"

// LocalCoverURL is the address of a book cover on this server. Mediarium fetches
// it once from Open Library, keeps a copy, and serves that copy afterwards, so
// the browser never waits on Open Library or talks to it directly. size is "S",
// "M" or "L".
func LocalCoverURL(coverID int, size string) string {
	if coverID <= 0 {
		return ""
	}
	return fmt.Sprintf("/api/book-covers/%d-%s.jpg", coverID, size)
}

// FetchCover downloads one cover by its Open Library cover id. It returns
// ErrNotFound when Open Library has no such cover.
func (c *Client) FetchCover(ctx context.Context, coverID int, size string) ([]byte, error) {
	base := openLibraryCovers
	if c.Base != openLibraryBase { // a test points the client at its own server
		base = c.Base
	}
	// default=false makes Open Library answer 404 for a missing cover instead
	// of a one pixel picture.
	u := fmt.Sprintf("%s/b/id/%d-%s.jpg?default=false", base, coverID, size)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("cover: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cover: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("cover: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cover: read image: %w", err)
	}
	if len(data) > maxCoverBytes {
		return nil, fmt.Errorf("cover: image is larger than %d bytes", maxCoverBytes)
	}
	// Trust the bytes, not the header: only a real JPEG or PNG is kept.
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png":
		return data, nil
	}
	return nil, ErrNotFound
}
