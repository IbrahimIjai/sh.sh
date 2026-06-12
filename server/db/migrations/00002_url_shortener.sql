-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS links (
	id BIGSERIAL PRIMARY KEY,
	user_id BIGINT,
	slug VARCHAR(64) NOT NULL,
	target_url TEXT NOT NULL,
	title VARCHAR(255),
	description TEXT,
	is_active BOOLEAN NOT NULL DEFAULT TRUE,
	click_count BIGINT NOT NULL DEFAULT 0,
	expires_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ,
	CONSTRAINT fk_links_user_id
		FOREIGN KEY (user_id)
		REFERENCES users (id)
		ON UPDATE CASCADE
		ON DELETE SET NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_links_slug ON links (slug);
CREATE INDEX IF NOT EXISTS idx_links_user_id ON links (user_id);
CREATE INDEX IF NOT EXISTS idx_links_created_at ON links (created_at);
CREATE INDEX IF NOT EXISTS idx_links_active_slug
	ON links (slug)
	WHERE is_active = TRUE AND deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS clicks (
	id BIGSERIAL PRIMARY KEY,
	link_id BIGINT NOT NULL,
	ip_address TEXT,
	user_agent TEXT,
	referer TEXT,
	country VARCHAR(2),
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	CONSTRAINT fk_clicks_link_id
		FOREIGN KEY (link_id)
		REFERENCES links (id)
		ON UPDATE CASCADE
		ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_clicks_link_id ON clicks (link_id);
CREATE INDEX IF NOT EXISTS idx_clicks_created_at ON clicks (created_at);
CREATE INDEX IF NOT EXISTS idx_clicks_link_id_created_at ON clicks (link_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS clicks;
DROP TABLE IF EXISTS links;
-- +goose StatementEnd
