package metadata

import (
	"context"
	"fmt"
	"sort"
)

// CollectionRef is the collection a film belongs to, as GET /movie/{id}
// names it (belongs_to_collection).
type CollectionRef struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

// CollectionPart is one film of a collection, as GET /collection/{id} lists it.
// TMDB's documentation names the title field "name" while the answers carry
// "title" like every other movie list; both are read.
type CollectionPart struct {
	TMDBID      int     `json:"id"`
	Title       string  `json:"title"`
	Name        string  `json:"name"`
	ReleaseDate string  `json:"release_date"`
	PosterPath  string  `json:"poster_path"`
	Overview    string  `json:"overview"`
	VoteAverage float64 `json:"vote_average"`
	MediaType   string  `json:"media_type"`
}

// DisplayTitle is the film's title.
func (p CollectionPart) DisplayTitle() string {
	if p.Title != "" {
		return p.Title
	}
	return p.Name
}

// Year is the release year, or 0.
func (p CollectionPart) Year() int { return yearOfDate(p.ReleaseDate) }

// Collection is GET /collection/{id}: a named series of films.
type Collection struct {
	ID           int              `json:"id"`
	Name         string           `json:"name"`
	Overview     string           `json:"overview"`
	PosterPath   string           `json:"poster_path"`
	BackdropPath string           `json:"backdrop_path"`
	Parts        []CollectionPart `json:"parts"`
}

// GetCollection fetches a collection and its films, oldest release first
// (films without a date last). The answer is kept for a few minutes and must
// not be modified.
func (c *Client) GetCollection(ctx context.Context, id int) (*Collection, error) {
	key := fmt.Sprintf("collection:%d", id)
	if v, ok := c.pages.get(key); ok {
		return v.(*Collection), nil
	}
	var col Collection
	if err := c.get(ctx, fmt.Sprintf("/collection/%d", id), nil, &col); err != nil {
		return nil, err
	}
	if col.ID == 0 {
		col.ID = id
	}
	parts := col.Parts[:0]
	for _, p := range col.Parts {
		if p.TMDBID > 0 && (p.MediaType == "" || p.MediaType == "movie") {
			parts = append(parts, p)
		}
	}
	col.Parts = parts
	sort.SliceStable(col.Parts, func(i, j int) bool {
		a, b := col.Parts[i].ReleaseDate, col.Parts[j].ReleaseDate
		if (a == "") != (b == "") {
			return b == ""
		}
		return a < b
	})
	c.pages.put(key, &col)
	return &col, nil
}
