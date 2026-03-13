ALTER TABLE company_financials
    DROP COLUMN IF EXISTS dividends,
    DROP COLUMN IF EXISTS roa;