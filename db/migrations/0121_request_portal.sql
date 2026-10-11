-- The request portal: a request quota per account (-1 = the default one set
-- under Settings > Accounts, 0 = no limit, N = that many), whether an account
-- wants to hear when its requests are answered, 4K requests, the moment a
-- request's title turned up in the library, devices that get push messages
-- for an account, and problem reports on titles.
ALTER TABLE users ADD COLUMN request_quota INTEGER NOT NULL DEFAULT -1;
ALTER TABLE users ADD COLUMN notify_requests INTEGER NOT NULL DEFAULT 1;

ALTER TABLE requests ADD COLUMN four_k INTEGER NOT NULL DEFAULT 0;
-- available_at: empty until the title is in the library and the person has been
-- told; "skipped" for requests answered before this existed (nobody is told
-- about those afterwards).
ALTER TABLE requests ADD COLUMN available_at TEXT NOT NULL DEFAULT '';
UPDATE requests SET available_at = 'skipped' WHERE status <> 'pending';

-- A browser or phone an account allowed notifications on. subscription is the
-- push subscription (JSON), encrypted with the server key.
CREATE TABLE IF NOT EXISTS user_push_devices (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL DEFAULT '',
    subscription TEXT    NOT NULL,
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX IF NOT EXISTS user_push_devices_user ON user_push_devices (user_id);

-- "Report a problem with this title".
CREATE TABLE IF NOT EXISTS issues (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT    NOT NULL,                 -- movie or tv
    item_id     INTEGER NOT NULL DEFAULT 0,       -- the movie or show in the library
    tmdb_id     INTEGER NOT NULL DEFAULT 0,
    title       TEXT    NOT NULL,
    year        INTEGER NOT NULL DEFAULT 0,
    poster      TEXT    NOT NULL DEFAULT '',
    category    TEXT    NOT NULL,                 -- wrong-file, no-sound, bad-subtitles, bad-picture or other
    episode     TEXT    NOT NULL DEFAULT '',      -- which episode, for a show
    message     TEXT    NOT NULL DEFAULT '',
    reported_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    status      TEXT    NOT NULL DEFAULT 'open',  -- open or resolved
    comment     TEXT    NOT NULL DEFAULT '',      -- what the administrator answered
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    resolved_at TEXT    NOT NULL DEFAULT '',
    resolved_by INTEGER REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS issues_status ON issues (status, created_at);
