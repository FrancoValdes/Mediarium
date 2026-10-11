-- Indexes for the questions asked of the episodes table all the time: how many
-- of a show's episodes are downloaded or missing (the dashboard, the Library
-- and the "recently added" list), which episodes have aired but are still
-- missing (Wanted), and what airs in a window of dates (the calendar). With
-- these the database answers from the index alone, without reading every
-- episode row (and its overview text) for a library of tens of thousands.
CREATE INDEX IF NOT EXISTS episodes_state ON episodes (series_id, status, monitored, season);
CREATE INDEX IF NOT EXISTS episodes_status_air ON episodes (status, air_date);
CREATE INDEX IF NOT EXISTS episodes_air ON episodes (air_date);
