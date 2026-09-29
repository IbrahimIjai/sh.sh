-- +goose Up
-- Each click carries an ID from the API instance that saw it, so a batch retried after a lost
-- acknowledgement, or reclaimed from the Redis stream by another instance, is stored once.
-- Older rows have no ID; NULLs never conflict. This replaces the per-batch receipts.
ALTER TABLE clicks ADD COLUMN event_id UUID;
CREATE UNIQUE INDEX idx_clicks_event ON clicks (created_at, event_id);
DROP TABLE click_batches;

-- +goose Down
CREATE TABLE click_batches (
	id TEXT PRIMARY KEY,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_click_batches_created_at ON click_batches (created_at);
DROP INDEX idx_clicks_event;
ALTER TABLE clicks DROP COLUMN event_id;
