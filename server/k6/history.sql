\set ON_ERROR_STOP on
\if :{?clicks}
\else
  \set clicks 100000
\endif

-- Optional larger analytics dataset, for the local k6 database only.
BEGIN;
INSERT INTO links (user_id, slug, target_url)
SELECT id, 'k6-history', 'https://example.com/k6/history'
FROM users WHERE email = 'k6-owner@example.com'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO clicks (created_at, link_id, event_id, visitor_hash, source,
                    device_type, browser, os, country, is_bot)
SELECT now() - (i % 30) * interval '1 day', l.id, gen_random_uuid(),
       md5((i % 1000)::text), 'Direct', 'desktop', 'Firefox', 'macOS', 'US', i % 10 = 0
FROM links l CROSS JOIN generate_series(1, :clicks) i
WHERE l.slug = 'k6-history';

INSERT INTO link_counters (link_id, human_clicks)
SELECT l.id, count(*) FILTER (WHERE NOT c.is_bot)
FROM links l JOIN clicks c ON c.link_id = l.id
WHERE l.slug = 'k6-history' GROUP BY l.id
ON CONFLICT (link_id) DO UPDATE SET human_clicks = EXCLUDED.human_clicks;
COMMIT;
ANALYZE clicks;
SELECT id AS link_id, slug FROM links WHERE slug = 'k6-history';
