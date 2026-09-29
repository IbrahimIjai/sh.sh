-- +goose Up
-- Each click carries an ID from the API instance that saw it, so a batch retried after a lost
-- acknowledgement, or reclaimed from the Redis stream by another instance, is stored once.
-- Older rows have no ID; NULLs never conflict. This replaces the per-batch receipts.
ALTER TABLE clicks ADD COLUMN event_id UUID;
CREATE UNIQUE INDEX idx_clicks_event ON clicks (created_at, event_id);
DROP TABLE click_batches;

-- A click for a day that is already rolled up marks the day stale; maintenance rolls it up again.
ALTER TABLE click_rollup_days ADD COLUMN stale BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX idx_click_rollup_days_stale ON click_rollup_days (day) WHERE stale;

-- +goose StatementBegin
CREATE FUNCTION mark_late_click_days() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	UPDATE click_rollup_days r
	SET stale = TRUE
	FROM (SELECT DISTINCT (n.created_at AT TIME ZONE 'UTC')::date AS day FROM new_clicks n) late
	WHERE r.day = late.day AND NOT r.stale;
	RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER clicks_mark_late_days AFTER INSERT ON clicks
REFERENCING NEW TABLE AS new_clicks
FOR EACH STATEMENT EXECUTE FUNCTION mark_late_click_days();

-- +goose Down
DROP TRIGGER clicks_mark_late_days ON clicks;
DROP FUNCTION mark_late_click_days();
DROP INDEX idx_click_rollup_days_stale;
ALTER TABLE click_rollup_days DROP COLUMN stale;
CREATE TABLE click_batches (
	id TEXT PRIMARY KEY,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_click_batches_created_at ON click_batches (created_at);
DROP INDEX idx_clicks_event;
ALTER TABLE clicks DROP COLUMN event_id;
