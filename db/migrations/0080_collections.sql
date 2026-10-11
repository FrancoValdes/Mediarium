-- TMDB collections: a series of films (a trilogy, a franchise). A movie in the
-- library remembers which collection it belongs to; the collection itself is
-- kept here, with the choices for "also add new films in this collection".
CREATE TABLE IF NOT EXISTS collections (
    tmdb_id           INTEGER PRIMARY KEY,
    name              TEXT    NOT NULL DEFAULT '',
    overview          TEXT    NOT NULL DEFAULT '',
    poster_path       TEXT    NOT NULL DEFAULT '',
    backdrop_path     TEXT    NOT NULL DEFAULT '',
    -- 1 = add new films of this collection by itself (off by default)
    follow            INTEGER NOT NULL DEFAULT 0,
    -- what those films are added with: NULL = the default quality profile,
    -- '' = the main movies folder
    follow_profile_id INTEGER,
    follow_root_path  TEXT    NOT NULL DEFAULT '',
    follow_monitored  INTEGER NOT NULL DEFAULT 1,
    -- the films the collection had when "also add new films" was last settled
    -- (a JSON list of TMDB ids): only films that are not in it are new
    known_parts       TEXT    NOT NULL DEFAULT '',
    -- when the collection was last looked at for new films (RFC 3339)
    checked_at        TEXT    NOT NULL DEFAULT ''
);

-- 0 = not part of a collection (or not looked up yet: see collection_checked).
ALTER TABLE movies ADD COLUMN collection_id INTEGER NOT NULL DEFAULT 0;
-- 1 once TMDB has been asked which collection the movie belongs to.
ALTER TABLE movies ADD COLUMN collection_checked INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_movies_collection ON movies (collection_id) WHERE collection_id > 0;
