-- A download that is meant to take the place of the file a movie has now (the
-- "Replace with a smaller version" action) remembers that file's path, so the
-- import can swap it in and remove the old one, also after a restart.
ALTER TABLE download_queue ADD COLUMN replaces_file TEXT NOT NULL DEFAULT '';
