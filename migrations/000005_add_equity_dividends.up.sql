-- equity: shareholders' equity (RSBU line 1300) or, for banks, regulatory
-- capital (CBR form 123); backs P/B. dividends: total dividends for the year by
-- record date (per-share value x shares outstanding), stored on the Q4 row.
-- Both in billions of RUB, NULL = not reported.
ALTER TABLE company_financials
    ADD COLUMN IF NOT EXISTS equity NUMERIC(15,2),
    ADD COLUMN IF NOT EXISTS dividends NUMERIC(15,2);
