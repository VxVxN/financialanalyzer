-- Last P/E-band bucket of each portfolio name, so the Monday note can say
-- the multiple entered a quartile instead of repeating it every week.
-- band is low, mid, high or none.
CREATE TABLE IF NOT EXISTS portfolio_bands (
    company    TEXT PRIMARY KEY,
    band       TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
