-- mediarium:rebuild
-- Second copies: a movie or show can be kept again in another quality (a 4K
-- copy next to the 1080p one), each copy a row of its own pointing at the
-- main one with copy_of. The movie database id is then unique among the
-- main titles only, and a copy once per quality profile, so both tables are
-- rebuilt without the UNIQUE on tmdb_id (SQLite can't drop it in place).
-- Nothing else changes: every column and row is kept as it is.

CREATE TABLE movies_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type    TEXT NOT NULL DEFAULT 'movie',
    tmdb_id       INTEGER NOT NULL,
    title         TEXT NOT NULL,
    year          INTEGER,
    overview      TEXT,
    poster_path   TEXT,
    status        TEXT NOT NULL DEFAULT 'missing',
    quality       TEXT,
    file_path     TEXT,
    monitored     INTEGER NOT NULL DEFAULT 1,
    added_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    release_date  TEXT,
    profile_id    INTEGER REFERENCES quality_profiles(id) ON DELETE SET NULL,
    source_pref   TEXT NOT NULL DEFAULT '',
    added_by      INTEGER,
    genres        TEXT,
    no_upgrade    INTEGER NOT NULL DEFAULT 0,
    details_state TEXT NOT NULL DEFAULT '',
    details_note  TEXT NOT NULL DEFAULT '',
    root_path     TEXT NOT NULL DEFAULT '',
    copy_of       INTEGER REFERENCES movies(id) ON DELETE CASCADE
);
INSERT INTO movies_new (id, media_type, tmdb_id, title, year, overview, poster_path, status, quality, file_path, monitored, added_at, updated_at,
                        release_date, profile_id, source_pref, added_by, genres, no_upgrade, details_state, details_note, root_path)
    SELECT id, media_type, tmdb_id, title, year, overview, poster_path, status, quality, file_path, monitored, added_at, updated_at,
           release_date, profile_id, source_pref, added_by, genres, no_upgrade, details_state, details_note, root_path
    FROM movies;
-- Keep the id counter where it was, so a removed movie's id isn't handed out again.
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'movies'), 0)) WHERE name = 'movies_new';
DROP TABLE movies;
ALTER TABLE movies_new RENAME TO movies;
CREATE INDEX idx_movies_status ON movies(status);
CREATE UNIQUE INDEX idx_movies_tmdb_main ON movies(tmdb_id) WHERE copy_of IS NULL;
CREATE UNIQUE INDEX idx_movies_copy_profile ON movies(copy_of, profile_id) WHERE copy_of IS NOT NULL;

CREATE TABLE series_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    tmdb_id        INTEGER NOT NULL,
    title          TEXT NOT NULL,
    year           INTEGER,
    overview       TEXT,
    poster_path    TEXT,
    first_air_date TEXT,
    monitored      INTEGER NOT NULL DEFAULT 1,
    added_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    profile_id     INTEGER REFERENCES quality_profiles(id) ON DELETE SET NULL,
    source_pref    TEXT NOT NULL DEFAULT '',
    added_by       INTEGER,
    genres         TEXT,
    no_upgrade     INTEGER NOT NULL DEFAULT 0,
    details_state  TEXT NOT NULL DEFAULT '',
    details_note   TEXT NOT NULL DEFAULT '',
    root_path      TEXT NOT NULL DEFAULT '',
    series_type    TEXT NOT NULL DEFAULT 'standard',
    copy_of        INTEGER REFERENCES series(id) ON DELETE CASCADE
);
INSERT INTO series_new (id, tmdb_id, title, year, overview, poster_path, first_air_date, monitored, added_at, updated_at,
                        profile_id, source_pref, added_by, genres, no_upgrade, details_state, details_note, root_path, series_type)
    SELECT id, tmdb_id, title, year, overview, poster_path, first_air_date, monitored, added_at, updated_at,
           profile_id, source_pref, added_by, genres, no_upgrade, details_state, details_note, root_path, series_type
    FROM series;
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'series'), 0)) WHERE name = 'series_new';
DROP TABLE series;
ALTER TABLE series_new RENAME TO series;
CREATE UNIQUE INDEX idx_series_tmdb_main ON series(tmdb_id) WHERE copy_of IS NULL;
CREATE UNIQUE INDEX idx_series_copy_profile ON series(copy_of, profile_id) WHERE copy_of IS NOT NULL;
