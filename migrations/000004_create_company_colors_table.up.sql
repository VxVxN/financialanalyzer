CREATE TABLE IF NOT EXISTS company_colors (
      id SERIAL PRIMARY KEY,
      company VARCHAR(100) NOT NULL UNIQUE,
      color VARCHAR(7) NOT NULL DEFAULT '#000000',
      updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_company_colors_company ON company_colors(company);