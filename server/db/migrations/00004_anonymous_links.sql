-- +goose Up
-- +goose StatementBegin
-- Anonymous links have no user_id. The creator's browser keeps a claim token (we store only its
-- SHA-256) so the links can be looked up and moved into an account after sign-up.
-- creator_hash is a keyed hash of the creator's IP, used to rate-limit anonymous creation.
ALTER TABLE links
	ADD COLUMN claim_token_hash VARCHAR(64),
	ADD COLUMN creator_hash VARCHAR(64);

CREATE UNIQUE INDEX IF NOT EXISTS idx_links_claim_token_hash
	ON links (claim_token_hash)
	WHERE claim_token_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_links_creator_hash_created_at
	ON links (creator_hash, created_at)
	WHERE creator_hash IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_links_creator_hash_created_at;
DROP INDEX IF EXISTS idx_links_claim_token_hash;
ALTER TABLE links
	DROP COLUMN IF EXISTS creator_hash,
	DROP COLUMN IF EXISTS claim_token_hash;
-- +goose StatementEnd
