ALTER TABLE company_financials
    DROP COLUMN IF EXISTS cash_source,
    DROP COLUMN IF EXISTS debt_source;
