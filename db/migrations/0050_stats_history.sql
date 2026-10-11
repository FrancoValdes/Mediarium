-- One snapshot of the library a day, for the Statistics page's "Over time"
-- charts: titles, what's downloaded, the library's size, and the day's
-- downloads. Kept for good; a row is a few dozen bytes.
CREATE TABLE stats_daily (
    day            TEXT PRIMARY KEY, -- YYYY-MM-DD (UTC)
    movies         INTEGER NOT NULL DEFAULT 0,
    movies_have    INTEGER NOT NULL DEFAULT 0,
    shows          INTEGER NOT NULL DEFAULT 0,
    episodes_have  INTEGER NOT NULL DEFAULT 0,
    albums_have    INTEGER NOT NULL DEFAULT 0,
    library_bytes  INTEGER NOT NULL DEFAULT 0,
    completed      INTEGER NOT NULL DEFAULT 0,
    failed         INTEGER NOT NULL DEFAULT 0,
    download_bytes INTEGER NOT NULL DEFAULT 0
);
