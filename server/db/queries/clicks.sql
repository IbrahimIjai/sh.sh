-- name: RecordClicks :one
-- Inserts a batch of clicks and adds the new non-bot ones to link_counters in one statement.
-- A click whose (created_at, event_id) is already stored is skipped, so a replay never counts
-- twice. Empty strings in the arrays are stored as NULL. Returns how many clicks were new.
-- Counters are upserted in link_id order so concurrent batches lock rows in the same order.
WITH batch AS (
	SELECT
		unnest(@event_ids::text[])::uuid AS event_id,
		unnest(@created_ats::timestamptz[]) AS created_at,
		unnest(@link_ids::bigint[]) AS link_id,
		unnest(@visitor_hashes::text[]) AS visitor_hash,
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
		event_id,
		created_at,
		link_id,
		visitor_hash,
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
		batch.event_id,
		batch.created_at,
		batch.link_id,
		NULLIF(batch.visitor_hash, ''),
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
	ON CONFLICT (created_at, event_id) DO NOTHING
	RETURNING clicks.link_id, clicks.is_bot
),
counted AS (
	INSERT INTO link_counters (link_id, human_clicks)
	SELECT inserted.link_id, COUNT(*)
	FROM inserted
	WHERE NOT inserted.is_bot
	GROUP BY inserted.link_id
	ORDER BY inserted.link_id
	ON CONFLICT (link_id) DO UPDATE
	SET human_clicks = link_counters.human_clicks + EXCLUDED.human_clicks
)
SELECT COUNT(*) FROM inserted;

-- name: ListClickCounts :many
SELECT link_id, human_clicks
FROM link_counters
WHERE link_id = ANY(@link_ids::bigint[]);

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
	(created_at AT TIME ZONE 'UTC')::date AS day,
	COUNT(*) AS clicks,
	COUNT(DISTINCT visitor_hash) AS unique_visitors
FROM clicks
WHERE link_id = $1
	AND created_at >= $2
	AND NOT is_bot
GROUP BY day
ORDER BY day;

-- name: ListClickDimensionsByLink :many
-- Every value per dimension for non-bot clicks. Each grouping set groups by one column and leaves
-- the others NULL, so COALESCE picks out that set's value (an empty string when the value itself is NULL).
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
);

-- name: ListRolledDays :many
SELECT day
FROM click_rollup_days
WHERE day >= @since::date
	AND day < @until::date
ORDER BY day;

-- name: GetRollupTotalsByLink :one
SELECT
	COALESCE(SUM(human), 0)::bigint AS clicks,
	COALESCE(SUM(visitors), 0)::bigint AS unique_visitors,
	COALESCE(SUM(bots), 0)::bigint AS bot_clicks
FROM click_daily
WHERE link_id = @link_id
	AND day >= @since::date
	AND day < @until::date;

-- name: ListRollupDailyByLink :many
SELECT day, human AS clicks, visitors AS unique_visitors
FROM click_daily
WHERE link_id = @link_id
	AND day >= @since::date
	AND day < @until::date
ORDER BY day;

-- name: ListRollupDimensionsByLink :many
SELECT dimension, value AS name, SUM(clicks)::bigint AS clicks
FROM click_daily_dims
WHERE link_id = @link_id
	AND day >= @since::date
	AND day < @until::date
GROUP BY dimension, value;

-- name: GetRollupDay :one
SELECT computed_at
FROM click_rollup_days
WHERE day = @day::date;

-- name: DeleteRollupDaily :exec
DELETE FROM click_daily WHERE day = @day::date;

-- name: DeleteRollupDimensions :exec
DELETE FROM click_daily_dims WHERE day = @day::date;

-- name: InsertRollupDaily :exec
INSERT INTO click_daily (link_id, day, human, bots, visitors)
SELECT
	link_id,
	@day::date,
	COUNT(*) FILTER (WHERE NOT is_bot),
	COUNT(*) FILTER (WHERE is_bot),
	COUNT(DISTINCT visitor_hash) FILTER (WHERE NOT is_bot)
FROM clicks
WHERE created_at >= @day_start
	AND created_at < @day_end
GROUP BY link_id;

-- name: InsertRollupDimensions :exec
INSERT INTO click_daily_dims (link_id, day, dimension, value, clicks)
SELECT
	link_id,
	@day::date,
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
	END)::text,
	COALESCE(source, referer_host, country, city, device_type, browser, os, utm_source, utm_campaign, '')::text,
	COUNT(*)
FROM clicks
WHERE created_at >= @day_start
	AND created_at < @day_end
	AND NOT is_bot
GROUP BY link_id, GROUPING SETS (
	(source), (referer_host), (country), (city), (device_type),
	(browser), (os), (utm_source), (utm_campaign)
);

-- name: MarkRollupDay :exec
INSERT INTO click_rollup_days (day, computed_at)
VALUES (@day::date, CURRENT_TIMESTAMP)
ON CONFLICT (day) DO UPDATE SET computed_at = EXCLUDED.computed_at;
