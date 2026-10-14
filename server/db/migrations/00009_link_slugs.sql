-- +goose Up
-- +goose StatementBegin
-- Every slug a link has ever had, current and replaced, so a renamed link keeps redirecting from
-- its old slugs and no slug is ever given to a second link. Custom slugs are also unique ignoring
-- case, so nobody can register a look-alike of someone else's name.
ALTER TABLE links ALTER COLUMN slug TYPE VARCHAR(128);

CREATE TABLE IF NOT EXISTS link_slugs (
	slug VARCHAR(128) PRIMARY KEY,
	link_id BIGINT NOT NULL,
	custom BOOLEAN NOT NULL DEFAULT FALSE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	replaced_at TIMESTAMPTZ,
	CONSTRAINT fk_link_slugs_link_id
		FOREIGN KEY (link_id)
		REFERENCES links (id)
		ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_link_slugs_link_id ON link_slugs (link_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_link_slugs_custom_lower ON link_slugs (lower(slug)) WHERE custom;

-- Generated slugs are seven letters and digits. Anything else was chosen by a user; if two of
-- those differ only by case, the older keeps the look-alike protection.
INSERT INTO link_slugs (slug, link_id, custom, created_at)
SELECT
	slug,
	id,
	slug !~ '^[A-Za-z0-9]{7}$'
		AND row_number() OVER (PARTITION BY lower(slug), slug ~ '^[A-Za-z0-9]{7}$' ORDER BY id) = 1,
	created_at
FROM links
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS link_slugs;
ALTER TABLE links ALTER COLUMN slug TYPE VARCHAR(64);
-- +goose StatementEnd
