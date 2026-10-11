-- What a person chose for one torrent of the built-in client: downloading in
-- order, its own speed limits, which files to leave out, and the category the
-- download was filed under. A torrent nobody touched has no row (it follows the
-- global settings). The row goes when the download is removed from the list.
CREATE TABLE IF NOT EXISTS torrent_options (
    queue_id       INTEGER PRIMARY KEY REFERENCES download_queue(id) ON DELETE CASCADE,
    sequential     INTEGER NOT NULL DEFAULT -1,   -- -1 follows the global default, 0 off, 1 on
    down_limit_kb  INTEGER NOT NULL DEFAULT 0,    -- KB/s for this torrent, 0 = no limit of its own
    up_limit_kb    INTEGER NOT NULL DEFAULT 0,
    files_decided  INTEGER NOT NULL DEFAULT 0,    -- 1 once the file selection was made (by the rules or by a person)
    skip_files     TEXT NOT NULL DEFAULT '[]',    -- JSON list of file paths inside the torrent that are left out
    category       TEXT NOT NULL DEFAULT '',      -- the category name when the download started
    subfolder      TEXT NOT NULL DEFAULT '',      -- its folder inside the downloads working folder
    dir_decided    INTEGER NOT NULL DEFAULT 0     -- 1 once the folder was chosen (a paused download keeps it)
);
