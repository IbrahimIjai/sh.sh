-- +goose Up
-- +goose StatementBegin
-- Raw IPs and full referrer URLs are personal data. Keep a per-day visitor hash and the
-- referrer's host instead, plus the parsed fields the stats endpoint groups by.
ALTER TABLE clicks
	DROP COLUMN IF EXISTS ip_address,
	DROP COLUMN IF EXISTS referer,
	ADD COLUMN visitor_hash VARCHAR(64),
	ADD COLUMN referer_host VARCHAR(255),
	ADD COLUMN source VARCHAR(255),
	ADD COLUMN utm_source VARCHAR(255),
	ADD COLUMN utm_medium VARCHAR(255),
	ADD COLUMN utm_campaign VARCHAR(255),
	ADD COLUMN device_type VARCHAR(16),
	ADD COLUMN browser VARCHAR(64),
	ADD COLUMN os VARCHAR(64),
	ADD COLUMN city VARCHAR(128),
	ADD COLUMN is_bot BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE clicks
	DROP COLUMN IF EXISTS is_bot,
	DROP COLUMN IF EXISTS city,
	DROP COLUMN IF EXISTS os,
	DROP COLUMN IF EXISTS browser,
	DROP COLUMN IF EXISTS device_type,
	DROP COLUMN IF EXISTS utm_campaign,
	DROP COLUMN IF EXISTS utm_medium,
	DROP COLUMN IF EXISTS utm_source,
	DROP COLUMN IF EXISTS source,
	DROP COLUMN IF EXISTS referer_host,
	DROP COLUMN IF EXISTS visitor_hash,
	ADD COLUMN ip_address TEXT,
	ADD COLUMN referer TEXT;
-- +goose StatementEnd
