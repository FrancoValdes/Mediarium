package notify

import (
	"fmt"
	"time"
)

// Digest is what the weekly summary says. Zero counts are left out of the table.
type Digest struct {
	Since           time.Time
	Movies          int // movie downloads that finished in the period
	Episodes        int // downloads of shows
	Albums          int
	Books           int // ebooks and audiobooks
	Failed          int
	Subtitles       int
	MissingMovies   int // wanted now
	MissingEpisodes int
	Waiting         int // downloads in the line now
}

// ComposeDigest builds the weekly summary.
func ComposeDigest(d Digest, links Links, at time.Time) Event {
	arrived := d.Movies + d.Episodes + d.Albums + d.Books
	ev := Event{Type: EventDigest, Timestamp: at, Title: "Mediarium this week", LinkURL: links.to("/")}
	switch {
	case arrived == 0 && d.Failed == 0:
		ev.Lead = "Nothing new came in this week."
	case arrived == 0:
		ev.Lead = fmt.Sprintf("Nothing new came in this week, and %s failed.", count(d.Failed, "download", "downloads"))
	case d.Failed == 0:
		ev.Lead = fmt.Sprintf("%s came in this week.", count(arrived, "download", "downloads"))
	default:
		ev.Lead = fmt.Sprintf("%s came in this week, and %s failed.", count(arrived, "download", "downloads"), count(d.Failed, "download", "downloads"))
	}
	var rows []Detail
	add := func(label string, n int) {
		if n > 0 {
			rows = append(rows, Detail{label, fmt.Sprint(n)})
		}
	}
	add("Movies", d.Movies)
	add("Shows", d.Episodes)
	add("Albums", d.Albums)
	add("Books", d.Books)
	add("Failed", d.Failed)
	add("Subtitles", d.Subtitles)
	add("Movies still missing", d.MissingMovies)
	add("Episodes still missing", d.MissingEpisodes)
	add("Waiting in the line", d.Waiting)
	ev.Details = rows
	ev.Message = plainBody(ev)
	return ev
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
