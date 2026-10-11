-- Releases that automation found but is holding back on purpose, because a
-- delay profile says to wait for a better protocol (Settings > Languages and
-- automation). A row is a reminder to search that title again when the wait is
-- over; the release itself is found again then. A row stays for a week after
-- its wait ended: it is also the memory of when a release without a posting
-- date was first seen.
CREATE TABLE IF NOT EXISTS delayed_releases (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT NOT NULL,                -- movie or episode
    movie_id      INTEGER NOT NULL DEFAULT 0,
    series_id     INTEGER NOT NULL DEFAULT 0,
    season        INTEGER NOT NULL DEFAULT 0,
    episode       INTEGER NOT NULL DEFAULT 0,   -- 0 = a whole season
    release_title TEXT NOT NULL,
    protocol      TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,                -- when automation first saw this release
    due_at        TEXT NOT NULL,                -- when the wait ends
    searched      INTEGER NOT NULL DEFAULT 0,   -- 1 once the search that follows the wait has run
    UNIQUE (kind, movie_id, series_id, season, episode, release_title)
);
CREATE INDEX IF NOT EXISTS delayed_releases_due ON delayed_releases (due_at);
