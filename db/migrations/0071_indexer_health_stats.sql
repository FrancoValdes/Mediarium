-- How each indexer has been answering: failures in a row and the pause that
-- follows (so a restart does not ask a struggling indexer again at once), and
-- a count per day of searches, failures, releases found and grabs for the
-- Statistics page.
CREATE TABLE IF NOT EXISTS indexer_health (
    indexer_id      INTEGER PRIMARY KEY REFERENCES indexers(id) ON DELETE CASCADE,
    failures        INTEGER NOT NULL DEFAULT 0,
    paused_until    TEXT NOT NULL DEFAULT '',
    reason          TEXT NOT NULL DEFAULT '',
    last_failure_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS indexer_stats (
    indexer_id INTEGER NOT NULL REFERENCES indexers(id) ON DELETE CASCADE,
    day        TEXT NOT NULL,                  -- YYYY-MM-DD (UTC)
    queries    INTEGER NOT NULL DEFAULT 0,
    failures   INTEGER NOT NULL DEFAULT 0,
    results    INTEGER NOT NULL DEFAULT 0,
    grabs      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (indexer_id, day)
);
