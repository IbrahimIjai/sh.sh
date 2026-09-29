-- +goose Up
-- +goose StatementBegin
-- Clicks move to a table partitioned by UTC day that keeps only what reports read: no raw user
-- agent, one index, and no foreign key to check on every insert (links are only soft-deleted).
-- Days without their own partition, including all history copied here, land in the default one.
ALTER TABLE clicks RENAME TO clicks_old;

CREATE TABLE clicks (
	created_at TIMESTAMPTZ NOT NULL,
	link_id BIGINT NOT NULL,
	visitor_hash VARCHAR(64),
	referer_host VARCHAR(255),
	source VARCHAR(255),
	utm_source VARCHAR(255),
	utm_medium VARCHAR(255),
	utm_campaign VARCHAR(255),
	device_type VARCHAR(16),
	browser VARCHAR(64),
	os VARCHAR(64),
	country VARCHAR(2),
	city VARCHAR(128),
	is_bot BOOLEAN NOT NULL DEFAULT FALSE
) PARTITION BY RANGE (created_at);

CREATE TABLE clicks_default PARTITION OF clicks DEFAULT;

DO $$
DECLARE
	d date;
BEGIN
	FOR d IN SELECT generate_series((now() AT TIME ZONE 'UTC')::date - 1, (now() AT TIME ZONE 'UTC')::date + 3, interval '1 day')::date LOOP
		EXECUTE format('CREATE TABLE %I PARTITION OF clicks FOR VALUES FROM (%L) TO (%L)',
			'clicks_p' || to_char(d, 'YYYYMMDD'),
			d::timestamp AT TIME ZONE 'UTC',
			(d + 1)::timestamp AT TIME ZONE 'UTC');
	END LOOP;
END $$;

INSERT INTO clicks (created_at, link_id, visitor_hash, referer_host, source, utm_source, utm_medium,
	utm_campaign, device_type, browser, os, country, city, is_bot)
SELECT created_at, link_id, visitor_hash, referer_host, source, utm_source, utm_medium,
	utm_campaign, device_type, browser, os, country, city, is_bot
FROM clicks_old;

DROP TABLE clicks_old;

CREATE INDEX idx_clicks_link_created ON clicks (link_id, created_at);
-- Only the default partition needs a time index: daily partitions are already one day each.
CREATE INDEX idx_clicks_default_created ON clicks_default (created_at);

-- Human-click counters live apart from the wide links rows, which click batches used to rewrite.
CREATE TABLE link_counters (
	link_id BIGINT PRIMARY KEY,
	human_clicks BIGINT NOT NULL DEFAULT 0
);
INSERT INTO link_counters (link_id, human_clicks)
SELECT id, click_count FROM links WHERE click_count > 0;
ALTER TABLE links DROP COLUMN click_count;

-- Daily rollups of closed UTC days. Reports read these for past days and raw clicks for the rest.
CREATE TABLE click_daily (
	link_id BIGINT NOT NULL,
	day DATE NOT NULL,
	human BIGINT NOT NULL,
	bots BIGINT NOT NULL,
	visitors BIGINT NOT NULL,
	PRIMARY KEY (link_id, day)
);
CREATE INDEX idx_click_daily_day ON click_daily (day);

CREATE TABLE click_daily_dims (
	link_id BIGINT NOT NULL,
	day DATE NOT NULL,
	dimension TEXT NOT NULL,
	value TEXT NOT NULL,
	clicks BIGINT NOT NULL,
	PRIMARY KEY (link_id, day, dimension, value)
);
CREATE INDEX idx_click_daily_dims_day ON click_daily_dims (day);

-- A day listed here has rollups; raw clicks for it may be dropped once retention passes.
CREATE TABLE click_rollup_days (
	day DATE PRIMARY KEY,
	computed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_click_batches_created_at ON click_batches (created_at);

-- The unique index on slug already serves every lookup.
DROP INDEX IF EXISTS idx_links_active_slug;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_links_active_slug
	ON links (slug)
	WHERE is_active = TRUE AND deleted_at IS NULL;
DROP INDEX IF EXISTS idx_click_batches_created_at;
DROP TABLE click_rollup_days;
DROP TABLE click_daily_dims;
DROP TABLE click_daily;

ALTER TABLE links ADD COLUMN click_count BIGINT NOT NULL DEFAULT 0;
UPDATE links SET click_count = link_counters.human_clicks
FROM link_counters WHERE link_counters.link_id = links.id;
DROP TABLE link_counters;

ALTER TABLE clicks RENAME TO clicks_partitioned;
CREATE TABLE clicks (
	id BIGSERIAL PRIMARY KEY,
	link_id BIGINT NOT NULL,
	user_agent TEXT,
	country VARCHAR(2),
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	visitor_hash VARCHAR(64),
	referer_host VARCHAR(255),
	source VARCHAR(255),
	utm_source VARCHAR(255),
	utm_medium VARCHAR(255),
	utm_campaign VARCHAR(255),
	device_type VARCHAR(16),
	browser VARCHAR(64),
	os VARCHAR(64),
	city VARCHAR(128),
	is_bot BOOLEAN NOT NULL DEFAULT FALSE,
	CONSTRAINT fk_clicks_link_id FOREIGN KEY (link_id) REFERENCES links (id) ON UPDATE CASCADE ON DELETE CASCADE
);
INSERT INTO clicks (link_id, country, created_at, visitor_hash, referer_host, source, utm_source,
	utm_medium, utm_campaign, device_type, browser, os, city, is_bot)
SELECT link_id, country, created_at, visitor_hash, referer_host, source, utm_source,
	utm_medium, utm_campaign, device_type, browser, os, city, is_bot
FROM clicks_partitioned;
DROP TABLE clicks_partitioned;
CREATE INDEX idx_clicks_link_id ON clicks (link_id);
CREATE INDEX idx_clicks_created_at ON clicks (created_at);
CREATE INDEX idx_clicks_link_id_created_at ON clicks (link_id, created_at);
-- +goose StatementEnd
