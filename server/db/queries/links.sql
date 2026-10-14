-- name: CreateLink :one
-- Returns no row when the slug is taken: link_slugs keeps every slug ever used, deleted and
-- replaced ones included, so a slug is never reused.
WITH reserved AS (
	INSERT INTO link_slugs (slug, link_id, custom)
	VALUES (@slug, nextval(pg_get_serial_sequence('links', 'id')), @custom)
	ON CONFLICT DO NOTHING
	RETURNING slug, link_id
)
INSERT INTO links (
	id,
	user_id,
	slug,
	target_url,
	title,
	description,
	expires_at,
	claim_token_hash,
	creator_hash
)
SELECT
	reserved.link_id,
	sqlc.narg(user_id)::bigint,
	reserved.slug,
	@target_url::text,
	sqlc.narg(title)::varchar,
	sqlc.narg(description)::text,
	sqlc.narg(expires_at)::timestamptz,
	sqlc.narg(claim_token_hash)::varchar,
	sqlc.narg(creator_hash)::varchar
FROM reserved
RETURNING *;

-- name: GetLinkBySlug :one
-- Current slugs are read from links directly; replaced ones through link_slugs.
SELECT *
FROM links
WHERE id = COALESCE(
		(SELECT l.id FROM links l WHERE l.slug = @slug),
		(SELECT s.link_id FROM link_slugs s WHERE s.slug = @slug)
	)
	AND is_active = TRUE
	AND deleted_at IS NULL
	AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP);

-- name: ChangeLinkSlug :one
-- Returns no row when another link holds the slug. Changing back to one of the link's own old
-- slugs makes it current again.
WITH owned AS (
	SELECT links.id, links.slug
	FROM links
	WHERE links.id = @id
		AND links.user_id = @user_id::bigint
		AND links.deleted_at IS NULL
	FOR UPDATE
),
added AS (
	INSERT INTO link_slugs (slug, link_id, custom)
	SELECT @slug::varchar, owned.id, TRUE
	FROM owned
	ON CONFLICT (slug) DO UPDATE SET replaced_at = link_slugs.replaced_at
	WHERE link_slugs.link_id = EXCLUDED.link_id
	RETURNING link_id
),
replaced AS (
	INSERT INTO link_slugs (slug, link_id, replaced_at)
	SELECT owned.slug, owned.id, CURRENT_TIMESTAMP
	FROM owned
	JOIN added ON added.link_id = owned.id
	ON CONFLICT (slug) DO UPDATE SET replaced_at = EXCLUDED.replaced_at
	WHERE link_slugs.link_id = EXCLUDED.link_id
)
UPDATE links
SET slug = @slug::varchar,
	updated_at = CURRENT_TIMESTAMP
FROM added
WHERE links.id = added.link_id
RETURNING links.*;

-- name: CountSlugChangesSince :one
SELECT COUNT(*)
FROM link_slugs s
JOIN links l ON l.id = s.link_id
WHERE l.user_id = @user_id::bigint
	AND s.replaced_at >= @since::timestamptz;

-- name: ListLinkSlugs :many
SELECT slug
FROM link_slugs
WHERE link_id = ANY(@link_ids::bigint[]);

-- name: ListPreviousSlugs :many
SELECT s.link_id, s.slug
FROM link_slugs s
JOIN links l ON l.id = s.link_id
WHERE s.link_id = ANY(@link_ids::bigint[])
	AND s.replaced_at IS NOT NULL
	AND s.slug <> l.slug
ORDER BY s.replaced_at DESC;

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

-- name: DeactivateLinksBySlugs :many
-- Any of a link's slugs, current or replaced, takes down the whole link.
UPDATE links
SET is_active = FALSE,
	updated_at = CURRENT_TIMESTAMP
WHERE is_active
	AND (
		slug = ANY(@slugs::text[])
		OR id IN (SELECT link_id FROM link_slugs WHERE link_slugs.slug = ANY(@slugs::text[]))
	)
RETURNING id, slug;

-- name: DeactivateLinksByTargetHost :many
-- Matches the host and its subdomains, ignoring any user info or port in the target URL.
WITH matched AS (
	SELECT
		id,
		lower(substring(target_url from '^[A-Za-z][A-Za-z0-9+.-]*://(?:[^/?#@]*@)?([^/?#:]+)')) AS host
	FROM links
	WHERE is_active
		AND deleted_at IS NULL
)
UPDATE links
SET is_active = FALSE,
	updated_at = CURRENT_TIMESTAMP
FROM matched
WHERE links.id = matched.id
	AND (matched.host = lower(@host::text) OR matched.host LIKE '%.' || lower(@host::text))
RETURNING links.id, links.slug;

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
