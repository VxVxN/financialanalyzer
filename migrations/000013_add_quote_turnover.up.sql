-- Average daily exchange turnover (billions of RUB) next to the latest close.
-- NULL until the next quote refresh measures it.
ALTER TABLE market_quotes ADD COLUMN turnover NUMERIC(15, 4);
