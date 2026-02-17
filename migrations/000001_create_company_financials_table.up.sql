CREATE TABLE IF NOT EXISTS company_financials (
      id SERIAL PRIMARY KEY,
      year INTEGER NOT NULL,
      quarter VARCHAR(2) NOT NULL,
      company VARCHAR(100) NOT NULL,
      category VARCHAR(100) NOT NULL,
      capitalization NUMERIC(15,2),
      revenue NUMERIC(15,2),
      net_profit NUMERIC(15,2),
      ebitda NUMERIC(15,2),
      debt NUMERIC(15,2),
      pe NUMERIC(10,2),
      roe NUMERIC(10,2),
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
      UNIQUE(year, quarter, company)
);