-- RSBU lines behind net debt, EV/EBIT and free cash flow, all in billions of
-- RUB, NULL = not reported. cash (line 1250) is a year-end stock like debt;
-- operating_profit (2200, profit from sales), operating_cash_flow (4100) and
-- capex (4221, a positive outflow) are flows like revenue: annual on an RSBU
-- Q4 row, single-quarter on CSV rows.
ALTER TABLE company_financials
    ADD COLUMN IF NOT EXISTS cash NUMERIC(15,2),
    ADD COLUMN IF NOT EXISTS operating_profit NUMERIC(15,2),
    ADD COLUMN IF NOT EXISTS operating_cash_flow NUMERIC(15,2),
    ADD COLUMN IF NOT EXISTS capex NUMERIC(15,2);
