-- Lyrics found online for a track, kept so each track is asked about once.
-- found = 0 records that nothing was there (asked again after a month).
-- Lyrics inside the file or in a .lrc file next to it are read from the disk
-- each time and never stored here.
CREATE TABLE IF NOT EXISTS music_lyrics (
    track_id     INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    source       TEXT    NOT NULL DEFAULT 'lrclib',
    found        INTEGER NOT NULL DEFAULT 0,
    instrumental INTEGER NOT NULL DEFAULT 0,
    synced       TEXT    NOT NULL DEFAULT '',
    plain        TEXT    NOT NULL DEFAULT '',
    fetched_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
