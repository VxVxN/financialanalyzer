ALTER TABLE company_financials
    DROP COLUMN IF EXISTS cash,
    DROP COLUMN IF EXISTS operating_profit,
    DROP COLUMN IF EXISTS operating_cash_flow,
    DROP COLUMN IF EXISTS capex;
