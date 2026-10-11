-- A show's TheTVDB and IMDb ids, read from TMDB when the show is added or
-- refreshed. Apps built for Sonarr name shows by their TVDB id.
ALTER TABLE series ADD COLUMN tvdb_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN imdb_id TEXT NOT NULL DEFAULT '';
-- TMDB's status for the show ("Returning Series", "Ended"…), from the same
-- refresh.
ALTER TABLE series ADD COLUMN tmdb_status TEXT NOT NULL DEFAULT '';

-- Numbers for tags, for apps built for Radarr and Sonarr, which name a tag
-- by its id. Mediarium's own tags are plain words; this only pairs each word
-- with a number that never changes.
CREATE TABLE arr_tags (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    label TEXT NOT NULL UNIQUE COLLATE NOCASE
);
