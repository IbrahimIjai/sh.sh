-- name: RecordClick :one
INSERT INTO clicks (
	link_id,
	ip_address,
	user_agent,
	referer,
	country
) VALUES (
	$1, $2, $3, $4, $5
)
RETURNING *;

-- name: ListClicksByLink :many
SELECT *
FROM clicks
WHERE link_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountClicksByLink :one
SELECT COUNT(*)
FROM clicks
WHERE link_id = $1;

-- name: CountClicksByLinkSince :one
SELECT COUNT(*)
FROM clicks
WHERE link_id = $1
	AND created_at >= $2;

-- name: ListDailyClicksByLink :many
SELECT
	date_trunc('day', created_at)::date AS day,
	COUNT(*) AS clicks
FROM clicks
WHERE link_id = $1
	AND created_at >= $2
GROUP BY day
ORDER BY day;

-- name: ListTopReferersByLink :many
SELECT
	COALESCE(NULLIF(referer, ''), 'direct') AS referer,
	COUNT(*) AS clicks
FROM clicks
WHERE link_id = $1
GROUP BY COALESCE(NULLIF(referer, ''), 'direct')
ORDER BY clicks DESC
LIMIT $2;

-- name: ListTopCountriesByLink :many
SELECT
	COALESCE(NULLIF(country, ''), 'unknown') AS country,
	COUNT(*) AS clicks
FROM clicks
WHERE link_id = $1
GROUP BY COALESCE(NULLIF(country, ''), 'unknown')
ORDER BY clicks DESC
LIMIT $2;
