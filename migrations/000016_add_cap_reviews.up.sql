-- Operator classification of a year-over-year capitalization jump.
-- kind is split, issue, buyback, error or price.
CREATE TABLE IF NOT EXISTS cap_reviews (
    company   TEXT NOT NULL,
    year_from INTEGER NOT NULL,
    year_to   INTEGER NOT NULL,
    kind      TEXT NOT NULL CHECK (kind IN ('split', 'issue', 'buyback', 'error', 'price')),
    PRIMARY KEY (company, year_from, year_to)
);
