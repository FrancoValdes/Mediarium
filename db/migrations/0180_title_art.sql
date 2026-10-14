-- A wide still (backdrop) and the title logo of a movie or a show, for the
-- calendar's Backdrops view. They come from TMDB's images list, which is a
-- separate call per title, so they are fetched in the background and kept
-- here. checked_at is when TMDB was last asked, so a title without a logo
-- isn't asked again every few minutes.
CREATE TABLE IF NOT EXISTS title_art (
    kind       TEXT    NOT NULL,           -- 'movie' or 'tv'
    tmdb_id    INTEGER NOT NULL,
    backdrop   TEXT    NOT NULL DEFAULT '', -- a TMDB image path like /abc.jpg
    logo       TEXT    NOT NULL DEFAULT '',
    checked_at TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (kind, tmdb_id)
);
