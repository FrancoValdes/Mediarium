-- E-reader access. Each account can have an e-reader password (for the OPDS
-- catalogue apps such as KOReader read) and a secret Kobo sync address. Both
-- are off until the account makes them. Only a SHA-256 of each secret is used
-- to find the account; the Kobo token is also kept encrypted so the account
-- can see its address again.
CREATE TABLE IF NOT EXISTS ereader_access (
    user_id          INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    opds_hash        TEXT NOT NULL DEFAULT '',
    opds_created_at  TEXT NOT NULL DEFAULT '',
    kobo_hash        TEXT NOT NULL DEFAULT '',
    kobo_token_enc   TEXT NOT NULL DEFAULT '',
    kobo_created_at  TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS ereader_kobo_hash ON ereader_access (kobo_hash) WHERE kobo_hash <> '';

-- The books already sent to one account's Kobo. book_id has no foreign key on
-- purpose: when a book is removed from the library the row stays until the
-- Kobo has been told to drop it.
CREATE TABLE IF NOT EXISTS kobo_library (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id   INTEGER NOT NULL,
    title     TEXT    NOT NULL DEFAULT '',
    file_sig  TEXT    NOT NULL DEFAULT '',  -- size and date of the file that was sent
    synced_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    archived  INTEGER NOT NULL DEFAULT 0,   -- 1 = taken off the Kobo by hand; not sent again
    PRIMARY KEY (user_id, book_id)
);

-- What the Kobo itself reported about a book, beyond the place and percentage
-- that are kept with the rest of the reading progress (book_progress).
CREATE TABLE IF NOT EXISTS kobo_state (
    user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id           INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    status            TEXT    NOT NULL DEFAULT '',  -- ReadyToRead, Reading or Finished
    location_value    TEXT    NOT NULL DEFAULT '',
    location_type     TEXT    NOT NULL DEFAULT '',
    location_source   TEXT    NOT NULL DEFAULT '',
    content_percent   REAL,                         -- progress inside the current chapter file
    spent_minutes     INTEGER NOT NULL DEFAULT 0,
    remaining_minutes INTEGER NOT NULL DEFAULT 0,
    times_started     INTEGER NOT NULL DEFAULT 0,
    last_started      TEXT    NOT NULL DEFAULT '',
    device_ts         TEXT    NOT NULL DEFAULT '',  -- the version of the reading progress the Kobo already has
    modified_at       TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, book_id)
);
