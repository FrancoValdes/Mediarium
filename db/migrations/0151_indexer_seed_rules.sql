-- Seeding rules of one torrent indexer. Private trackers have their own: seed
-- until this ratio, or this many hours, whichever comes first. An empty value
-- follows the global seeding limit in Settings; 0 means no limit of that kind.
CREATE TABLE IF NOT EXISTS indexer_seed_rules (
    indexer_id INTEGER PRIMARY KEY REFERENCES indexers(id) ON DELETE CASCADE,
    ratio      REAL,
    hours      REAL
);
