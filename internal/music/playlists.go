package music

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Limits that keep a list usable and a request bounded.
const (
	MaxPlaylistNameLen = 100
	MaxPlaylistTracks  = 5000
)

// ErrPlaylistNotFound is returned for a playlist that doesn't exist, or that
// the caller may not see.
var ErrPlaylistNotFound = errors.New("playlist not found")

// ErrPlaylistFull is returned when adding would pass MaxPlaylistTracks.
var ErrPlaylistFull = errors.New("playlist is full")

// ErrBadOrder is returned when a reorder doesn't name every entry once.
var ErrBadOrder = errors.New("the order must list every entry once")

// Playlist is one list of tracks. UserID is the account that owns it.
type Playlist struct {
	ID        int64
	UserID    int64
	Name      string
	Shared    bool
	CreatedAt string
	UpdatedAt string
	// Filled by ListPlaylists.
	TrackCount int
	DurationMs int
	AlbumIDs   []int64 // the first few albums on the list, for a cover mosaic
}

// PlaylistEntry is one place on a playlist. EntryID is its own id (a track can
// be on a list more than once).
type PlaylistEntry struct {
	EntryID  int64
	Position int
	Track    Track
	Title    string // the track's title (same as Track.Title)
	Album    string
	ArtistID int64
	Artist   string
}

// CleanPlaylistName trims a name and checks it. ok is false for an empty one.
func CleanPlaylistName(name string) (string, bool) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", false
	}
	r := []rune(name)
	if len(r) > MaxPlaylistNameLen {
		name = strings.TrimSpace(string(r[:MaxPlaylistNameLen]))
	}
	return name, true
}

// CreatePlaylist makes an empty list for an account.
func (r *Repo) CreatePlaylist(userID int64, name string, shared bool) (Playlist, error) {
	name, ok := CleanPlaylistName(name)
	if !ok {
		return Playlist{}, errors.New("a playlist needs a name")
	}
	res, err := r.db.Exec(`INSERT INTO music_playlists (user_id, name, shared) VALUES (?, ?, ?)`, userID, name, boolInt(shared))
	if err != nil {
		return Playlist{}, fmt.Errorf("create playlist: %w", err)
	}
	id, _ := res.LastInsertId()
	return r.GetPlaylist(id, userID)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const playlistColumns = `p.id, p.user_id, p.name, p.shared, p.created_at, p.updated_at,
	(SELECT COUNT(*) FROM music_playlist_tracks e WHERE e.playlist_id = p.id),
	(SELECT COALESCE(SUM(t.length_ms), 0) FROM music_playlist_tracks e JOIN tracks t ON t.id = e.track_id WHERE e.playlist_id = p.id)`

func scanPlaylist(row interface{ Scan(...any) error }) (Playlist, error) {
	var p Playlist
	var shared int
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &shared, &p.CreatedAt, &p.UpdatedAt, &p.TrackCount, &p.DurationMs)
	p.Shared = shared != 0
	return p, err
}

// GetPlaylist returns a list the account owns or one that is shared with
// everyone. Anything else is ErrPlaylistNotFound, so a private list of someone
// else can't be told apart from one that doesn't exist.
func (r *Repo) GetPlaylist(id, userID int64) (Playlist, error) {
	p, err := scanPlaylist(r.db.QueryRow(`SELECT `+playlistColumns+` FROM music_playlists p WHERE p.id = ? AND (p.user_id = ? OR p.shared = 1)`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrPlaylistNotFound
	}
	if err != nil {
		return Playlist{}, fmt.Errorf("get playlist %d: %w", id, err)
	}
	return p, nil
}

// ListPlaylists returns the account's own lists and the ones shared with
// everyone, its own first, newest change first within each group.
func (r *Repo) ListPlaylists(userID int64) ([]Playlist, error) {
	rows, err := r.db.Query(`SELECT `+playlistColumns+` FROM music_playlists p WHERE p.user_id = ? OR p.shared = 1
		ORDER BY (p.user_id = ?) DESC, p.updated_at DESC, p.id DESC`, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("list playlists: %w", err)
	}
	var out []Playlist
	for rows.Next() {
		p, err := scanPlaylist(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("list playlists: %w", err)
		}
		out = append(out, p)
	}
	rows.Close()
	for i := range out {
		out[i].AlbumIDs = r.playlistAlbums(out[i].ID, 4)
	}
	return out, rows.Err()
}

// playlistAlbums is the first n different albums on a list, in list order.
func (r *Repo) playlistAlbums(id int64, n int) []int64 {
	rows, err := r.db.Query(`SELECT t.album_id FROM music_playlist_tracks e JOIN tracks t ON t.id = e.track_id
		WHERE e.playlist_id = ? ORDER BY e.position, e.id`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	seen := map[int64]bool{}
	var out []int64
	for rows.Next() && len(out) < n {
		var a int64
		if rows.Scan(&a) == nil && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// UpdatePlaylist renames a list and/or changes who can see it. Only the owner
// may; anyone else gets ErrPlaylistNotFound.
func (r *Repo) UpdatePlaylist(id, userID int64, name *string, shared *bool) error {
	if name != nil {
		clean, ok := CleanPlaylistName(*name)
		if !ok {
			return errors.New("a playlist needs a name")
		}
		name = &clean
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("update playlist: %w", err)
	}
	defer tx.Rollback()
	if err := ownedBy(tx, id, userID); err != nil {
		return err
	}
	if name != nil {
		if _, err := tx.Exec(`UPDATE music_playlists SET name = ? WHERE id = ?`, *name, id); err != nil {
			return fmt.Errorf("rename playlist: %w", err)
		}
	}
	if shared != nil {
		if _, err := tx.Exec(`UPDATE music_playlists SET shared = ? WHERE id = ?`, boolInt(*shared), id); err != nil {
			return fmt.Errorf("share playlist: %w", err)
		}
	}
	if err := touch(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePlaylist removes a list the account owns.
func (r *Repo) DeletePlaylist(id, userID int64) error {
	res, err := r.db.Exec(`DELETE FROM music_playlists WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete playlist: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPlaylistNotFound
	}
	return nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func ownedBy(q execer, id, userID int64) error {
	var owner int64
	err := q.QueryRow(`SELECT user_id FROM music_playlists WHERE id = ?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != userID) {
		return ErrPlaylistNotFound
	}
	if err != nil {
		return fmt.Errorf("read playlist %d: %w", id, err)
	}
	return nil
}

func touch(q execer, id int64) error {
	if _, err := q.Exec(`UPDATE music_playlists SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, id); err != nil {
		return fmt.Errorf("update playlist %d: %w", id, err)
	}
	return nil
}

// PlaylistEntries returns a list's tracks in order, with the names the player
// shows. It doesn't check who may see the list: GetPlaylist does.
func (r *Repo) PlaylistEntries(id int64) ([]PlaylistEntry, error) {
	rows, err := r.db.Query(`SELECT e.id, e.position, t.id, t.album_id, t.disc, t.position, t.title, t.length_ms, t.file_path,
			al.title, ar.id, ar.name
		FROM music_playlist_tracks e
		JOIN tracks t ON t.id = e.track_id
		JOIN albums al ON al.id = t.album_id
		JOIN artists ar ON ar.id = al.artist_id
		WHERE e.playlist_id = ? ORDER BY e.position, e.id`, id)
	if err != nil {
		return nil, fmt.Errorf("read playlist %d: %w", id, err)
	}
	defer rows.Close()
	var out []PlaylistEntry
	for rows.Next() {
		var e PlaylistEntry
		if err := rows.Scan(&e.EntryID, &e.Position, &e.Track.ID, &e.Track.AlbumID, &e.Track.Disc, &e.Track.Position, &e.Track.Title,
			&e.Track.LengthMs, &e.Track.FilePath, &e.Album, &e.ArtistID, &e.Artist); err != nil {
			return nil, fmt.Errorf("read playlist %d: %w", id, err)
		}
		e.Title = e.Track.Title
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddToPlaylist appends tracks to a list the account owns, in the order given.
// Tracks that no longer exist are skipped. It returns how many were added.
func (r *Repo) AddToPlaylist(id, userID int64, trackIDs []int64) (int, error) {
	if len(trackIDs) == 0 {
		return 0, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("add to playlist: %w", err)
	}
	defer tx.Rollback()
	if err := ownedBy(tx, id, userID); err != nil {
		return 0, err
	}
	var count, last int
	if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(MAX(position), 0) FROM music_playlist_tracks WHERE playlist_id = ?`, id).Scan(&count, &last); err != nil {
		return 0, fmt.Errorf("add to playlist: %w", err)
	}
	if count+len(trackIDs) > MaxPlaylistTracks {
		return 0, ErrPlaylistFull
	}
	added := 0
	for _, tid := range trackIDs {
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM tracks WHERE id = ?`, tid).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return 0, fmt.Errorf("add to playlist: %w", err)
		}
		last++
		if _, err := tx.Exec(`INSERT INTO music_playlist_tracks (playlist_id, track_id, position) VALUES (?, ?, ?)`, id, tid, last); err != nil {
			return 0, fmt.Errorf("add to playlist: %w", err)
		}
		added++
	}
	if added > 0 {
		if err := touch(tx, id); err != nil {
			return 0, err
		}
	}
	return added, tx.Commit()
}

// RemoveFromPlaylist removes one entry from a list the account owns.
func (r *Repo) RemoveFromPlaylist(id, userID, entryID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("remove from playlist: %w", err)
	}
	defer tx.Rollback()
	if err := ownedBy(tx, id, userID); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM music_playlist_tracks WHERE id = ? AND playlist_id = ?`, entryID, id)
	if err != nil {
		return fmt.Errorf("remove from playlist: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPlaylistNotFound
	}
	if err := touch(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ReorderPlaylist puts the entries of a list the account owns in the order
// given, which must name every entry exactly once.
func (r *Repo) ReorderPlaylist(id, userID int64, entryIDs []int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("reorder playlist: %w", err)
	}
	defer tx.Rollback()
	if err := ownedBy(tx, id, userID); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id FROM music_playlist_tracks WHERE playlist_id = ?`, id)
	if err != nil {
		return fmt.Errorf("reorder playlist: %w", err)
	}
	have := map[int64]bool{}
	for rows.Next() {
		var e int64
		if err := rows.Scan(&e); err != nil {
			rows.Close()
			return fmt.Errorf("reorder playlist: %w", err)
		}
		have[e] = true
	}
	rows.Close()
	if len(entryIDs) != len(have) {
		return ErrBadOrder
	}
	seen := map[int64]bool{}
	for _, e := range entryIDs {
		if !have[e] || seen[e] {
			return ErrBadOrder
		}
		seen[e] = true
	}
	for i, e := range entryIDs {
		if _, err := tx.Exec(`UPDATE music_playlist_tracks SET position = ? WHERE id = ?`, i+1, e); err != nil {
			return fmt.Errorf("reorder playlist: %w", err)
		}
	}
	if err := touch(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// PlayableTrackIDs is the ids of an album's tracks that have a file, in
// disc and track order.
func (r *Repo) PlayableTrackIDs(albumID int64) ([]int64, error) {
	rows, err := r.db.Query(`SELECT id FROM tracks WHERE album_id = ? AND file_path <> '' ORDER BY disc, position`, albumID)
	if err != nil {
		return nil, fmt.Errorf("list tracks of album %d: %w", albumID, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ArtistPlayableTracks is every track with a file of an artist, newest album
// first, each album in track order.
func (r *Repo) ArtistPlayableTracks(artistID int64) ([]Track, error) {
	rows, err := r.db.Query(`SELECT t.id, t.album_id, t.disc, t.position, t.title, t.length_ms, t.file_path
		FROM tracks t JOIN albums al ON al.id = t.album_id
		WHERE al.artist_id = ? AND t.file_path <> ''
		ORDER BY al.release_date = '', al.release_date DESC, al.id, t.disc, t.position`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list tracks of artist %d: %w", artistID, err)
	}
	defer rows.Close()
	var out []Track
	for rows.Next() {
		var t Track
		if err := rows.Scan(&t.ID, &t.AlbumID, &t.Disc, &t.Position, &t.Title, &t.LengthMs, &t.FilePath); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
