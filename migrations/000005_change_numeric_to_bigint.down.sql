ALTER TABLE company_financials
    ALTER COLUMN capitalization TYPE NUMERIC(15,2) USING (capitalization::numeric(15,2)),
    ALTER COLUMN revenue TYPE NUMERIC(15,2) USING (revenue::numeric(15,2)),
    ALTER COLUMN net_profit TYPE NUMERIC(15,2) USING (net_profit::numeric(15,2)),
    ALTER COLUMN ebitda TYPE NUMERIC(15,2) USING (ebitda::numeric(15,2)),
    ALTER COLUMN debt TYPE NUMERIC(15,2) USING (debt::numeric(15,2)),
    ALTER COLUMN pe TYPE NUMERIC(10,2) USING (pe::numeric(10,2)),
    ALTER COLUMN roe TYPE NUMERIC(10,2) USING (roe::numeric(10,2));

