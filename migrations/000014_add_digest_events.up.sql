-- Events the Monday operator note should mention once: an IFRS pull that
-- wrote a year which had no manual row. Cleared after the note is delivered.
CREATE TABLE IF NOT EXISTS digest_events (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL,
    company    TEXT NOT NULL,
    detail     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
