-- Provenance of each row's figures (see models.Source*): which pipeline wrote
-- it, so the UI can flag numbers that are not comparable (e.g. holding-level
-- RSBU vs consolidated IFRS). NULL for rows written before this column existed.
ALTER TABLE company_financials
    ADD COLUMN IF NOT EXISTS source VARCHAR(32);
