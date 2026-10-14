-- Whether an account's email address was set by an administrator (1) or typed
-- by the person on their own profile (0). Linking a first single sign-on to an
-- existing account by email only trusts addresses an administrator set, so
-- nobody can claim an address that isn't theirs by typing it. Addresses saved
-- before this are not known to be checked and start at 0.
ALTER TABLE users ADD COLUMN email_by_admin INTEGER NOT NULL DEFAULT 0;
