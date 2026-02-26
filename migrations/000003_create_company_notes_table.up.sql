CREATE TABLE IF NOT EXISTS company_notes (
     id SERIAL PRIMARY KEY,
     company VARCHAR(100) NOT NULL UNIQUE,
     note TEXT,
     updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
     created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_company_notes_company ON company_notes(company);