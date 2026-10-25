\set ON_ERROR_STOP on
-- Public test credentials for the isolated k6 database only. Rerunning refreshes sessions.
BEGIN;
INSERT INTO users (email, email_verified_at, updated_at)
VALUES ('k6-owner@example.com', now(), now()), ('k6-other@example.com', now(), now())
ON CONFLICT (email) DO NOTHING;

INSERT INTO sessions (token, user_id, expires_at, last_access)
SELECT 'k6-local-owner', id, now() + interval '1 day', now()
FROM users WHERE email = 'k6-owner@example.com'
ON CONFLICT (token) DO UPDATE SET expires_at = now() + interval '1 day';

INSERT INTO sessions (token, user_id, expires_at, last_access)
SELECT 'k6-local-other', id, now() + interval '1 day', now()
FROM users WHERE email = 'k6-other@example.com'
ON CONFLICT (token) DO UPDATE SET expires_at = now() + interval '1 day';
COMMIT;
