-- Two-step sign-in: a code from an authenticator app after the password.
-- The secret is stored encrypted with the server key; recovery codes only as
-- hashes. totp_last_step stops one code from being used twice.
ALTER TABLE users ADD COLUMN totp_secret    TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN recovery_codes TEXT    NOT NULL DEFAULT '';
