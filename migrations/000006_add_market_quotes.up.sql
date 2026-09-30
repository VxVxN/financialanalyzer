-- Latest exchange close per company (one row, overwritten by each cmd/fetch
-- run). Backs "current" valuation: current cap / trailing earnings.
CREATE TABLE IF NOT EXISTS market_quotes (
    company        VARCHAR(100) PRIMARY KEY,
    price          NUMERIC(18,6) NOT NULL,
    capitalization NUMERIC(15,2) NOT NULL,
    price_date     DATE NOT NULL,
    updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
