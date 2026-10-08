-- Mondays for which the cheap-list note was delivered. A restart after
-- that Monday's slot sends the note when this week's Monday is missing.
CREATE TABLE IF NOT EXISTS cheap_list_sends (
    monday  DATE PRIMARY KEY,
    sent_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
