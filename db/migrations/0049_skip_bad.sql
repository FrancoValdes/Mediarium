-- "Skip bad releases": a built-in list of releases nobody wants (whole
-- Blu-ray discs, 3D, upscales, burned-in subtitles, known low-quality
-- re-encoders). On for every profile, existing ones included.
ALTER TABLE quality_profiles ADD COLUMN skip_bad INTEGER NOT NULL DEFAULT 1;
