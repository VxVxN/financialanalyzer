-- Share count behind the stored quote. The next refresh compares it with
-- the current ISSUESIZE and warns when no split explains the change.
ALTER TABLE market_quotes ADD COLUMN shares NUMERIC(24, 4);
