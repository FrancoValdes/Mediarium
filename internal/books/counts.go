package books

import "fmt"

// FormatCounts is how the books wanted in one format stand.
type FormatCounts struct {
	Books      int // books that want this format
	Downloaded int
	Missing    int
}

// Counts totals the books wanted as an ebook and as an audiobook, for the
// dashboard, without reading every book into memory.
func (r *Repo) Counts() (ebooks, audiobooks FormatCounts, err error) {
	err = r.db.QueryRow(`SELECT
			COALESCE(SUM(CASE WHEN want_ebook <> 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN want_ebook <> 0 AND ebook_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN want_ebook <> 0 AND ebook_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN want_audiobook <> 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN want_audiobook <> 0 AND audiobook_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN want_audiobook <> 0 AND audiobook_status = ? THEN 1 ELSE 0 END), 0)
		FROM books`, StatusDownloaded, StatusMissing, StatusDownloaded, StatusMissing).
		Scan(&ebooks.Books, &ebooks.Downloaded, &ebooks.Missing, &audiobooks.Books, &audiobooks.Downloaded, &audiobooks.Missing)
	if err != nil {
		return FormatCounts{}, FormatCounts{}, fmt.Errorf("count books: %w", err)
	}
	return ebooks, audiobooks, nil
}

// ListReleasing lists the books wanted in some format whose release date
// ("YYYY-MM-DD") falls from from to to, inclusive, newest added first.
func (r *Repo) ListReleasing(from, to string) ([]Book, error) {
	rows, err := r.db.Query(`SELECT `+columns+` FROM books
		WHERE length(release_date) = 10 AND release_date >= ? AND release_date <= ? AND (want_ebook <> 0 OR want_audiobook <> 0)
		ORDER BY added_at DESC, id DESC`, from, to)
	if err != nil {
		return nil, fmt.Errorf("list releasing books: %w", err)
	}
	defer rows.Close()
	out := []Book{}
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan book: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
