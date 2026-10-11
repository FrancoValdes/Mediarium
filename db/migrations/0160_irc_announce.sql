-- IRC announce: connections to the IRC channels where trackers announce new
-- releases, and a short log of what each announcement led to. Nothing here is
-- used until a person adds a connection and switches it on.
--
-- Secrets (the NickServ or SASL password, the invite command, which holds a
-- key, channel passwords and the tracker keys) are encrypted with the server
-- key; the template and the channel list are not secret.
CREATE TABLE IF NOT EXISTS irc_connections (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT    NOT NULL,
    enabled             INTEGER NOT NULL DEFAULT 0,
    preset              TEXT    NOT NULL DEFAULT '',
    server              TEXT    NOT NULL,
    port                INTEGER NOT NULL DEFAULT 6697,
    tls                 INTEGER NOT NULL DEFAULT 1,
    insecure_tls        INTEGER NOT NULL DEFAULT 0,
    nick                TEXT    NOT NULL,
    auth_mode           TEXT    NOT NULL DEFAULT 'none',
    auth_account        TEXT    NOT NULL DEFAULT '',
    auth_password_enc   TEXT    NOT NULL DEFAULT '',
    invite_enc          TEXT    NOT NULL DEFAULT '',
    channels            TEXT    NOT NULL DEFAULT '[]',
    announcers          TEXT    NOT NULL DEFAULT '[]',
    tracker_url         TEXT    NOT NULL DEFAULT '',
    template            TEXT    NOT NULL DEFAULT '{}',
    secrets_enc         TEXT    NOT NULL DEFAULT '',
    freeleech_only      INTEGER NOT NULL DEFAULT 0,
    max_per_hour        INTEGER NOT NULL DEFAULT 5,
    max_per_day         INTEGER NOT NULL DEFAULT 20,
    created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- What each announcement led to. decision is grabbed, would-grab (a test),
-- skipped (not wanted, a limit, a filter), duplicate, ignored (could not be
-- read) or failed (wanted, but the grab did not go through). Only the last 50
-- rows of each connection are kept.
CREATE TABLE IF NOT EXISTS irc_announce_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    connection_id INTEGER NOT NULL REFERENCES irc_connections(id) ON DELETE CASCADE,
    at            TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    channel       TEXT    NOT NULL DEFAULT '',
    announcer     TEXT    NOT NULL DEFAULT '',
    release       TEXT    NOT NULL DEFAULT '',
    category      TEXT    NOT NULL DEFAULT '',
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    decision      TEXT    NOT NULL,
    reason        TEXT    NOT NULL DEFAULT '',
    item          TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_irc_announce_log_conn ON irc_announce_log (connection_id, id);
CREATE INDEX IF NOT EXISTS idx_irc_announce_log_decision ON irc_announce_log (connection_id, decision, announcer, at);
