-- Existing streams stay accessible by direct link, without automatic directory listing.
ALTER TABLE stations ADD COLUMN visibility TEXT NOT NULL DEFAULT 'unlisted' CHECK(visibility IN ('listed','unlisted','private'));
