-- Plex watchlists: an account connects its Plex account, and titles it puts on
-- its Plex watchlist are added (or asked for) by themselves.
-- token is the Plex account token, encrypted with the server key.
CREATE TABLE plex_watchlists (
    user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token        TEXT    NOT NULL,
    plex_name    TEXT    NOT NULL DEFAULT '',
    auto_add     INTEGER NOT NULL DEFAULT 1,
    connected_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    checked_at   TEXT    NOT NULL DEFAULT '',
    last_error   TEXT    NOT NULL DEFAULT ''
);

-- Every title seen on a watchlist, so each is handled once. outcome says what
-- happened: existing (was already on the list when it was connected), added,
-- requested, in-library, no-match (Plex knows no TMDB id) or failed.
CREATE TABLE plex_watchlist_items (
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plex_key TEXT    NOT NULL,
    kind     TEXT    NOT NULL,
    title    TEXT    NOT NULL DEFAULT '',
    year     INTEGER NOT NULL DEFAULT 0,
    tmdb_id  INTEGER NOT NULL DEFAULT 0,
    outcome  TEXT    NOT NULL DEFAULT '',
    seen_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (user_id, plex_key)
);
