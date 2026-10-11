-- ListenBrainz scrobbling. Each account can connect its own ListenBrainz user
-- token. token is encrypted with the server key.
CREATE TABLE IF NOT EXISTS music_listenbrainz (
    user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token        TEXT    NOT NULL,
    lb_user      TEXT    NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    connected_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_error   TEXT    NOT NULL DEFAULT '',
    submitted    INTEGER NOT NULL DEFAULT 0
);

-- Listens that could not be sent (ListenBrainz was unreachable), tried again
-- with the next one. Only the track and the time are kept.
CREATE TABLE IF NOT EXISTS music_listens_pending (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    track_id    INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    listened_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_music_listens_pending_user ON music_listens_pending(user_id, listened_at);
