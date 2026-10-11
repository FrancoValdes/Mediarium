-- More ways to sign in, and a read-only role.
--
-- users.auth_source says where an account's password lives: "local" (the
-- password hash in this table), or "oidc" / "ldap" for accounts made on a
-- first sign-in through single sign-on or the directory. Those never get a
-- password here (password_hash stays empty).
-- users.is_viewer is the read-only role: it can browse, never change.
-- users.disabled turns an account off without deleting it.
ALTER TABLE users ADD COLUMN auth_source TEXT    NOT NULL DEFAULT 'local';
ALTER TABLE users ADD COLUMN is_viewer   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN disabled    INTEGER NOT NULL DEFAULT 0;

-- Read-only API keys may only use GET routes.
ALTER TABLE api_keys ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0;

-- How a session was started ("password", "oidc", "ldap", "passkey"). Used to
-- check that single sign-on works before password sign-in is switched off.
ALTER TABLE sessions ADD COLUMN method TEXT NOT NULL DEFAULT 'password';

-- An outside identity tied to an account: the sign-in provider ("oidc",
-- "ldap") and the stable id it knows the person by (never the name or email,
-- which can change hands).
CREATE TABLE IF NOT EXISTS user_identities (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider   TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (provider, subject)
);
CREATE INDEX IF NOT EXISTS user_identities_user ON user_identities (user_id);

-- Passkeys (WebAuthn). The credential is the library's own JSON record (public
-- key, counter, flags); there is no secret in it. webauthn_handle is the
-- random id this account goes by on the authenticator (never the username).
ALTER TABLE users ADD COLUMN webauthn_handle BLOB;

CREATE TABLE IF NOT EXISTS passkeys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BLOB    NOT NULL UNIQUE,
    name          TEXT    NOT NULL,
    credential    TEXT    NOT NULL,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at  TEXT
);
CREATE INDEX IF NOT EXISTS passkeys_user ON passkeys (user_id);
