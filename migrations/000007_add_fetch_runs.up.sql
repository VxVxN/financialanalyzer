-- One row per cmd/fetch run or scheduled refresh in cmd/plot: what ran, when,
-- and how it went. Backs the "Обновление данных" page and the scheduler's
-- catch-up of missed runs.
CREATE TABLE IF NOT EXISTS fetch_runs (
    id             BIGSERIAL PRIMARY KEY,
    kind           VARCHAR(20)  NOT NULL,            -- quotes | financials
    trigger        VARCHAR(20)  NOT NULL,            -- cli | schedule | catchup
    scope          TEXT         NOT NULL DEFAULT '', -- what was requested
    full_scope     BOOLEAN      NOT NULL,            -- every stored company
    status         VARCHAR(20)  NOT NULL,            -- running | ok | partial | failed | canceled | abandoned
    started_at     TIMESTAMPTZ  NOT NULL,
    finished_at    TIMESTAMPTZ,
    updated        INTEGER      NOT NULL DEFAULT 0,  -- companies with new rows
    up_to_date     INTEGER      NOT NULL DEFAULT 0,
    rows_saved     INTEGER      NOT NULL DEFAULT 0,
    quotes_saved   INTEGER      NOT NULL DEFAULT 0,
    failed         TEXT         NOT NULL DEFAULT '', -- comma-separated tickers
    quotes_failed  TEXT         NOT NULL DEFAULT '',
    error          TEXT         NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS fetch_runs_started_idx ON fetch_runs (started_at DESC);
