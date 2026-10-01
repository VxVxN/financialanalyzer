-- Figures entered by hand on the dashboard (usually the group's IFRS annual
-- report), one row per company-year, billions of RUB, NULL = not entered.
-- Kept apart from company_financials so no loader can overwrite them; they
-- are overlaid on the fetched history when read (models.ApplyManual).
CREATE TABLE IF NOT EXISTS manual_financials (
    company             VARCHAR(100) NOT NULL,
    year                INTEGER      NOT NULL,
    revenue             NUMERIC(15,2),
    net_profit          NUMERIC(15,2),
    ebitda              NUMERIC(15,2),
    operating_profit    NUMERIC(15,2),
    operating_cash_flow NUMERIC(15,2),
    capex               NUMERIC(15,2),
    debt                NUMERIC(15,2),
    cash                NUMERIC(15,2),
    equity              NUMERIC(15,2),
    dividends           NUMERIC(15,2),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (company, year)
);
