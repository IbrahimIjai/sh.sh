-- +goose Up
-- A batch receipt commits atomically with its click rows and link counters. Retrying
-- the same batch after a lost database acknowledgement must not count it twice.
CREATE TABLE click_batches (
	id TEXT PRIMARY KEY,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE click_batches;
