-- Import lists: lists on Trakt, TMDB, IMDb, MDBList and Letterboxd that are
-- read on a schedule, adding what is on them to the library.
-- config is the source's own settings (JSON); secret is the person's API key
-- for the source, encrypted with the server key. tags is a JSON list.
CREATE TABLE IF NOT EXISTS import_lists (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL,
    source         TEXT    NOT NULL,
    media          TEXT    NOT NULL DEFAULT 'movie',  -- movie, tv or both
    config         TEXT    NOT NULL DEFAULT '{}',
    secret         TEXT    NOT NULL DEFAULT '',
    enabled        INTEGER NOT NULL DEFAULT 1,
    interval_hours INTEGER NOT NULL DEFAULT 24,
    profile_id     INTEGER NOT NULL DEFAULT 0,        -- 0 = the default quality profile
    movie_root     TEXT    NOT NULL DEFAULT '',
    tv_root        TEXT    NOT NULL DEFAULT '',
    monitored      INTEGER NOT NULL DEFAULT 1,
    search_now     INTEGER NOT NULL DEFAULT 1,
    tags           TEXT    NOT NULL DEFAULT '[]',
    min_rating     REAL    NOT NULL DEFAULT 0,
    min_year       INTEGER NOT NULL DEFAULT 0,
    max_per_run    INTEGER NOT NULL DEFAULT 25,
    start_fresh    INTEGER NOT NULL DEFAULT 0,        -- leave what is on the list at the first read alone
    created_by     INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    last_run_at    TEXT    NOT NULL DEFAULT '',
    last_status    TEXT    NOT NULL DEFAULT '',       -- ok or error
    last_message   TEXT    NOT NULL DEFAULT '',
    last_found     INTEGER NOT NULL DEFAULT 0,
    last_added     INTEGER NOT NULL DEFAULT 0,
    last_skipped   INTEGER NOT NULL DEFAULT 0,
    total_added    INTEGER NOT NULL DEFAULT 0
);

-- What a list has handled, so a title is dealt with once. ref names the entry
-- as the list does ("tmdb:movie:603", "imdb:tt0133093" or "title:alien|1979").
-- outcome: added, in-library, existing (on the list when it was first read and
-- left alone), excluded (on the Not interested list), filtered (below the
-- minimum rating or year), no-match (not found on TMDB) or failed.
CREATE TABLE IF NOT EXISTS import_list_items (
    list_id INTEGER NOT NULL REFERENCES import_lists(id) ON DELETE CASCADE,
    ref     TEXT    NOT NULL,
    kind    TEXT    NOT NULL DEFAULT '',
    tmdb_id INTEGER NOT NULL DEFAULT 0,
    title   TEXT    NOT NULL DEFAULT '',
    year    INTEGER NOT NULL DEFAULT 0,
    rating  REAL    NOT NULL DEFAULT 0,
    outcome TEXT    NOT NULL,
    seen_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (list_id, ref)
);
