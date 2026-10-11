// Package importlists reads the lists Mediarium can add titles from by itself:
// the file formats (CSV exports from IMDb and Letterboxd), MDBList's API, and
// the rules that decide whether a title on a list is wanted (minimum rating
// and year). Nothing here touches the library: internal/api turns what a list
// holds into titles to add.
//
// Trakt and TMDB lists are read by internal/trakt and internal/metadata.
package importlists

// UserAgent is what Mediarium calls itself when it asks a service for a list.
// The app sets it to include its version.
var UserAgent = "Mediarium (+https://mediarium.app)"

// Item is one title on a list, as the list names it. Kind is "movie" or "tv";
// it is empty when the file doesn't say (a CSV without a type column). Either
// TMDBID or IMDBID is usually set; with neither, only the title and year are
// known and the title has to be searched for.
type Item struct {
	Kind   string
	TMDBID int
	IMDBID string
	Title  string
	Year   int
	// Rating is TMDB's vote average when the list came from TMDB and said so,
	// else 0 (not known; it is looked up when a minimum rating needs it).
	Rating float64
}
