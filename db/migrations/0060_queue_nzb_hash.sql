-- A fingerprint of the NZB a Usenet download was started from: a hash of the
-- sorted article ids it lists. It lets Mediarium see that two releases with
-- different names are the same post, for the "skip duplicate downloads"
-- setting. Empty until the NZB has been read.
ALTER TABLE download_queue ADD COLUMN nzb_hash TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_download_queue_nzb_hash ON download_queue(nzb_hash) WHERE nzb_hash != '';
