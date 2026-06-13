-- name: CreateDomain :one
INSERT INTO domains (
	user_id,
	host,
	verification_token
) VALUES (
	$1, $2, $3
)
RETURNING *;

-- name: ListDomainsByUser :many
SELECT *
FROM domains
WHERE user_id = $1
	AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: GetDomainByHost :one
SELECT *
FROM domains
WHERE host = $1
	AND deleted_at IS NULL;

-- name: SoftDeleteDomain :exec
UPDATE domains
SET deleted_at = CURRENT_TIMESTAMP,
	updated_at = CURRENT_TIMESTAMP
WHERE id = $1
	AND user_id = $2
	AND deleted_at IS NULL;

-- name: MarkDomainVerified :one
UPDATE domains
SET is_verified = TRUE,
	updated_at = CURRENT_TIMESTAMP
WHERE host = $1
	AND verification_token = $2
	AND deleted_at IS NULL
RETURNING *;
