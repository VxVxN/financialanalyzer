ALTER TABLE company_financials
    ALTER COLUMN capitalization TYPE BIGINT USING (capitalization::bigint),
    ALTER COLUMN revenue TYPE BIGINT USING (revenue::bigint),
    ALTER COLUMN net_profit TYPE BIGINT USING (net_profit::bigint),
    ALTER COLUMN ebitda TYPE BIGINT USING (ebitda::bigint),
    ALTER COLUMN debt TYPE BIGINT USING (debt::bigint),
    ALTER COLUMN pe TYPE BIGINT USING (pe::bigint),
    ALTER COLUMN roe TYPE BIGINT USING (roe::bigint);