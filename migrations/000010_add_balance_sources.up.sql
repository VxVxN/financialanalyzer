-- Which pipeline wrote debt and cash. The row's source stays with the flows,
-- so a later CSV can fill debt on an RSBU row and leave the parent's cash in
-- place. Analytics withholds net debt, EV, EV/EBIT and P/FCF when the two
-- columns come from different reporting kinds.
--
-- Existing values are attributed to the row source. A mix that was already
-- stored cannot be recovered; only merges after this migration are detected.
ALTER TABLE company_financials
    ADD COLUMN debt_source TEXT,
    ADD COLUMN cash_source TEXT;

UPDATE company_financials
SET debt_source = source
WHERE debt IS NOT NULL AND source IS NOT NULL;

UPDATE company_financials
SET cash_source = source
WHERE cash IS NOT NULL AND source IS NOT NULL;
