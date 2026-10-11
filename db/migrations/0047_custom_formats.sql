-- Custom formats (named sets of conditions on a release, as in Radarr and
-- Sonarr, TRaSH Guides ones included) and what quality profiles do with them.
CREATE TABLE custom_formats (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    app        TEXT NOT NULL DEFAULT 'radarr',  -- whose numbering the conditions use: radarr or sonarr
    trash_id   TEXT NOT NULL DEFAULT '',        -- the TRaSH Guides id, for formats imported from there
    specs      TEXT NOT NULL,                   -- the conditions, JSON
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- A profile's score for each format (JSON {"<format id>": score}), the
-- lowest total a release needs, the total at which better-scoring releases
-- stop being looked for (0: scores don't drive upgrades), and whether
-- season packs are refused.
ALTER TABLE quality_profiles ADD COLUMN format_scores TEXT NOT NULL DEFAULT '{}';
ALTER TABLE quality_profiles ADD COLUMN min_format_score INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quality_profiles ADD COLUMN cutoff_format_score INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quality_profiles ADD COLUMN no_season_packs INTEGER NOT NULL DEFAULT 0;

-- The release a downloaded file came from, kept because the file itself is
-- renamed on import: what its custom-format score is worked out from.
ALTER TABLE movies ADD COLUMN release_title TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN release_title TEXT NOT NULL DEFAULT '';
