-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS domains (
	id BIGSERIAL PRIMARY KEY,
	user_id BIGINT NOT NULL,
	host VARCHAR(255) NOT NULL,
	is_verified BOOLEAN NOT NULL DEFAULT FALSE,
	verification_token VARCHAR(255) NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ,
	CONSTRAINT fk_domains_user_id
		FOREIGN KEY (user_id)
		REFERENCES users (id)
		ON UPDATE CASCADE
		ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_domains_host ON domains (host)
	WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_domains_user_id ON domains (user_id)
	WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS domains;
-- +goose StatementEnd
