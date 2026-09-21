-- name: CreateLink :one
INSERT INTO links (
	user_id,
	slug,
	target_url,
	title,
	description,
	expires_at,
	claim_token_hash,
	creator_hash
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetLinkBySlug :one
SELECT *
FROM links
WHERE slug = $1
	AND is_active = TRUE
	AND deleted_at IS NULL
	AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP);

-- name: GetLinkByID :one
SELECT *
FROM links
WHERE id = $1
	AND deleted_at IS NULL;

-- name: ListLinksByUser :many
SELECT *
FROM links
WHERE user_id = $1
	AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountLinksByUser :one
SELECT COUNT(*)
FROM links
WHERE user_id = $1
	AND deleted_at IS NULL;

-- name: UpdateLink :one
UPDATE links
SET target_url = COALESCE($2, target_url),
	title = COALESCE($3, title),
	description = COALESCE($4, description),
	is_active = COALESCE($5, is_active),
	expires_at = COALESCE($6, expires_at),
	updated_at = CURRENT_TIMESTAMP
WHERE id = $1
	AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteLink :exec
UPDATE links
SET deleted_at = CURRENT_TIMESTAMP,
	is_active = FALSE,
	updated_at = CURRENT_TIMESTAMP
WHERE id = $1
	AND deleted_at IS NULL;

-- name: SlugExists :one
-- Soft-deleted links still hold their slug: idx_links_slug is unique across all rows.
SELECT EXISTS (
	SELECT 1
	FROM links
	WHERE slug = $1
);

-- name: CountLinksByCreatorSince :one
SELECT COUNT(*)
FROM links
WHERE creator_hash = $1
	AND created_at >= $2;

-- name: ListLinksByClaimTokens :many
SELECT *
FROM links
WHERE claim_token_hash = ANY(@claim_token_hashes::text[])
	AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: ClaimLinks :many
UPDATE links
SET user_id = @user_id,
	claim_token_hash = NULL,
	updated_at = CURRENT_TIMESTAMP
WHERE claim_token_hash = ANY(@claim_token_hashes::text[])
	AND user_id IS NULL
	AND deleted_at IS NULL
RETURNING *;
