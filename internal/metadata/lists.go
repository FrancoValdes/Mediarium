package metadata

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// What the scheduled import lists need from TMDB: a public list by its
// number, the top rated charts, a filtered browse, and the lookup from an
// IMDb id to a TMDB one.

// ListEntry is one title on a TMDB list.
type ListEntry struct {
	Kind   string // "movie" or "tv"
	TMDBID int
	Title  string
	Year   int
	Rating float64 // TMDB's vote average, 0 when it has none
}

// maxListPages stops a runaway list: TMDB serves 20 entries a page.
const maxListPages = 25

// ListEntries fetches the titles on a public TMDB list (the number in its
// address, like themoviedb.org/list/8504994).
func (c *Client) ListEntries(ctx context.Context, listID int) ([]ListEntry, error) {
	if listID <= 0 {
		return nil, fmt.Errorf("tmdb list: %d is not a list number", listID)
	}
	var out []ListEntry
	for page := 1; page <= maxListPages; page++ {
		var resp struct {
			ItemCount  int `json:"item_count"`
			TotalPages int `json:"total_pages"`
			Items      []struct {
				ID           int     `json:"id"`
				MediaType    string  `json:"media_type"`
				Title        string  `json:"title"`
				Name         string  `json:"name"`
				ReleaseDate  string  `json:"release_date"`
				FirstAirDate string  `json:"first_air_date"`
				VoteAverage  float64 `json:"vote_average"`
			} `json:"items"`
		}
		path := "/list/" + strconv.Itoa(listID)
		if err := c.get(ctx, path, url.Values{"page": {strconv.Itoa(page)}}, &resp); err != nil {
			return nil, err
		}
		for _, it := range resp.Items {
			if it.ID <= 0 {
				continue
			}
			e := ListEntry{TMDBID: it.ID, Rating: it.VoteAverage}
			switch it.MediaType {
			case "tv":
				e.Kind, e.Title, e.Year = "tv", it.Name, yearOfDate(it.FirstAirDate)
			case "movie", "": // a v3 list holds only movies, and older answers leave the type out
				e.Kind, e.Title, e.Year = "movie", it.Title, yearOfDate(it.ReleaseDate)
			default:
				continue
			}
			out = append(out, e)
		}
		if len(resp.Items) == 0 || len(out) >= resp.ItemCount || (resp.TotalPages > 0 && page >= resp.TotalPages) {
			break
		}
	}
	return out, nil
}

// TopRatedMovies and TopRatedTV are one page of TMDB's top rated charts.
func (c *Client) TopRatedMovies(ctx context.Context, page int) (*MoviePage, error) {
	return c.moviePage(ctx, "/movie/top_rated", url.Values{"page": {strconv.Itoa(ClampPage(page))}})
}

func (c *Client) TopRatedTV(ctx context.Context, page int) (*ShowPage, error) {
	return c.showPage(ctx, "/tv/top_rated", url.Values{"page": {strconv.Itoa(ClampPage(page))}})
}

// BrowseFilter narrows a browse of TMDB's catalogue. Zero values mean "any".
type BrowseFilter struct {
	Sort     string // SortPopular, SortRating or SortNewest
	Genre    int
	Language string // original language, a two-letter code like "ko"
	Page     int
}

// params builds the discover query. A "best rated" browse only counts titles
// with enough votes, like the Discover page does.
func (f BrowseFilter) params(dateField string) url.Values {
	q := url.Values{"include_adult": {"false"}, "page": {strconv.Itoa(ClampPage(f.Page))}}
	switch f.Sort {
	case SortRating:
		q.Set("sort_by", "vote_average.desc")
		q.Set("vote_count.gte", strconv.Itoa(minVotesForRating))
	case SortNewest:
		q.Set("sort_by", dateField+".desc")
	default:
		q.Set("sort_by", "popularity.desc")
	}
	if f.Genre > 0 {
		q.Set("with_genres", strconv.Itoa(f.Genre))
	}
	if l := strings.ToLower(strings.TrimSpace(f.Language)); l != "" {
		q.Set("with_original_language", l)
	}
	return q
}

// BrowseMovies and BrowseTV page through TMDB's catalogue with a BrowseFilter.
func (c *Client) BrowseMovies(ctx context.Context, f BrowseFilter) (*MoviePage, error) {
	q := f.params("primary_release_date")
	if f.Sort == SortNewest {
		q.Set("primary_release_date.lte", c.today()) // not the ones that are still to come
	}
	return c.moviePage(ctx, "/discover/movie", q)
}

func (c *Client) BrowseTV(ctx context.Context, f BrowseFilter) (*ShowPage, error) {
	q := f.params("first_air_date")
	if f.Sort == SortNewest {
		q.Set("first_air_date.lte", c.today())
	}
	return c.showPage(ctx, "/discover/tv", q)
}

// FindByIMDB looks an IMDb id (like tt0133093) up on TMDB. It gives the
// movie's and the show's TMDB id; whichever TMDB doesn't know is 0.
func (c *Client) FindByIMDB(ctx context.Context, imdbID string) (movieID, showID int, err error) {
	imdbID = strings.TrimSpace(imdbID)
	if !strings.HasPrefix(imdbID, "tt") || strings.ContainsAny(imdbID, "/?#& ") {
		return 0, 0, fmt.Errorf("find by imdb id: %q is not an IMDb id", imdbID)
	}
	var resp struct {
		MovieResults []struct {
			ID int `json:"id"`
		} `json:"movie_results"`
		TVResults []struct {
			ID int `json:"id"`
		} `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+imdbID, url.Values{"external_source": {"imdb_id"}}, &resp); err != nil {
		return 0, 0, err
	}
	if len(resp.MovieResults) > 0 {
		movieID = resp.MovieResults[0].ID
	}
	if len(resp.TVResults) > 0 {
		showID = resp.TVResults[0].ID
	}
	return movieID, showID, nil
}
