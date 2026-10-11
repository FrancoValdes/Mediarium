-- How a show's episodes are numbered in release names, when that differs from
-- the numbering Mediarium keeps: TheXEM's scene numbering and the AniDB entries
-- of an anime (Fribb's anime-lists), with the AniList / AniDB / MyAnimeList ids
-- and the other names the show is known by. One row per show, read from the
-- services when the show is added or refreshed, or when "Refresh mapping" is
-- pressed. A show's copies use the main show's row.
CREATE TABLE IF NOT EXISTS series_numbering (
    series_id   INTEGER PRIMARY KEY REFERENCES series(id) ON DELETE CASCADE,
    anidb_id    INTEGER NOT NULL DEFAULT 0,  -- the show's first AniDB entry
    anilist_id  INTEGER NOT NULL DEFAULT 0,
    mal_id      INTEGER NOT NULL DEFAULT 0,
    numbering   TEXT    NOT NULL DEFAULT '', -- JSON: scene rows and AniDB entries
    aliases     TEXT    NOT NULL DEFAULT '', -- JSON list of other names, for matching release names
    xem_state   TEXT    NOT NULL DEFAULT '', -- mapped, none, offline
    lists_state TEXT    NOT NULL DEFAULT '', -- found, none, offline
    note        TEXT    NOT NULL DEFAULT '', -- the last problem, in plain words
    checked_at  TEXT    NOT NULL DEFAULT '', -- when the services were last asked (RFC 3339)
    titles_at   TEXT    NOT NULL DEFAULT ''  -- when the names were last read from AniList
);
