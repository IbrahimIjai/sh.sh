-- name: RecordClicks :exec
-- Inserts a batch of clicks and adds the non-bot ones to links.click_count in one statement.
-- Empty strings in the arrays are stored as NULL.
-- Reuse batch_id on every retry, including after an ambiguous commit outcome.
WITH accepted_batch AS (
	INSERT INTO click_batches (id) VALUES (@batch_id::text)
	ON CONFLICT (id) DO NOTHING
	RETURNING id
),
batch AS (
	SELECT
		unnest(@created_ats::timestamptz[]) AS created_at,
		unnest(@link_ids::bigint[]) AS link_id,
		unnest(@visitor_hashes::text[]) AS visitor_hash,
		unnest(@user_agents::text[]) AS user_agent,
		unnest(@referer_hosts::text[]) AS referer_host,
		unnest(@sources::text[]) AS source,
		unnest(@utm_sources::text[]) AS utm_source,
		unnest(@utm_mediums::text[]) AS utm_medium,
		unnest(@utm_campaigns::text[]) AS utm_campaign,
		unnest(@device_types::text[]) AS device_type,
		unnest(@browsers::text[]) AS browser,
		unnest(@oses::text[]) AS os,
		unnest(@countries::text[]) AS country,
		unnest(@cities::text[]) AS city,
		unnest(@is_bots::boolean[]) AS is_bot
),
inserted AS (
	INSERT INTO clicks (
		created_at,
		link_id,
		visitor_hash,
		user_agent,
		referer_host,
		source,
		utm_source,
		utm_medium,
		utm_campaign,
		device_type,
		browser,
		os,
		country,
		city,
		is_bot
	)
	SELECT
		batch.created_at,
		batch.link_id,
		NULLIF(batch.visitor_hash, ''),
		NULLIF(batch.user_agent, ''),
		NULLIF(batch.referer_host, ''),
		NULLIF(batch.source, ''),
		NULLIF(batch.utm_source, ''),
		NULLIF(batch.utm_medium, ''),
		NULLIF(batch.utm_campaign, ''),
		NULLIF(batch.device_type, ''),
		NULLIF(batch.browser, ''),
		NULLIF(batch.os, ''),
		NULLIF(batch.country, ''),
		NULLIF(batch.city, ''),
		batch.is_bot
	FROM batch
	WHERE EXISTS (SELECT 1 FROM accepted_batch)
	RETURNING clicks.link_id, clicks.is_bot
)
UPDATE links
SET click_count = links.click_count + counts.clicks
FROM (
	SELECT inserted.link_id, COUNT(*) AS clicks
	FROM inserted
	WHERE NOT inserted.is_bot
	GROUP BY inserted.link_id
) AS counts
WHERE links.id = counts.link_id;

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

-- name: GetClickTotalsByLink :one
-- visitor_hash rotates daily, so distinct hashes over a range add up the daily unique visitors.
SELECT
	COUNT(*) FILTER (WHERE NOT is_bot) AS clicks,
	COUNT(DISTINCT visitor_hash) FILTER (WHERE NOT is_bot) AS unique_visitors,
	COUNT(*) FILTER (WHERE is_bot) AS bot_clicks
FROM clicks
WHERE link_id = $1
	AND created_at >= $2;

-- name: ListDailyClicksByLink :many
SELECT
	date_trunc('day', created_at)::date AS day,
	COUNT(*) AS clicks,
	COUNT(DISTINCT visitor_hash) AS unique_visitors
FROM clicks
WHERE link_id = $1
	AND created_at >= $2
	AND NOT is_bot
GROUP BY day
ORDER BY day;

-- name: ListClickBreakdownByLink :many
-- Top values per dimension for non-bot clicks. Each grouping set groups by one column and leaves
-- the others NULL, so COALESCE picks out that set's value (an empty string when the value itself is NULL).
SELECT ranked.dimension, ranked.name, ranked.clicks
FROM (
	SELECT
		grouped.dimension,
		grouped.name,
		grouped.clicks,
		ROW_NUMBER() OVER (PARTITION BY grouped.dimension ORDER BY grouped.clicks DESC, grouped.name) AS position
	FROM (
		SELECT
			(CASE
				WHEN GROUPING(source) = 0 THEN 'sources'
				WHEN GROUPING(referer_host) = 0 THEN 'referrers'
				WHEN GROUPING(country) = 0 THEN 'countries'
				WHEN GROUPING(city) = 0 THEN 'cities'
				WHEN GROUPING(device_type) = 0 THEN 'devices'
				WHEN GROUPING(browser) = 0 THEN 'browsers'
				WHEN GROUPING(os) = 0 THEN 'operating_systems'
				WHEN GROUPING(utm_source) = 0 THEN 'utm_sources'
				ELSE 'utm_campaigns'
			END)::text AS dimension,
			COALESCE(source, referer_host, country, city, device_type, browser, os, utm_source, utm_campaign, '')::text AS name,
			COUNT(*) AS clicks
		FROM clicks
		WHERE link_id = @link_id
			AND created_at >= @since
			AND NOT is_bot
		GROUP BY GROUPING SETS (
			(source), (referer_host), (country), (city), (device_type),
			(browser), (os), (utm_source), (utm_campaign)
		)
	) AS grouped
) AS ranked
WHERE ranked.position <= @per_dimension::bigint
ORDER BY ranked.dimension, ranked.clicks DESC, ranked.name;
