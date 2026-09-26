#!/usr/bin/env bash
# End-to-end check against the running compose stack (docker compose up --build -d).
# Needs curl and jq. Signs in through the magic link printed in the api logs, so run it while
# email sending is disabled. Each run leaves a test user and links in the dev database.
set -u
B=http://localhost:8080
JAR=$(mktemp)
IPHONE='Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1'
MAC='Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0'
pass=0; fail=0
check() { if [ "$2" = "$3" ]; then echo "  ok   $1"; pass=$((pass+1)); else echo "  FAIL $1: got '$2' want '$3'"; fail=$((fail+1)); fi; }
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
json() { curl -s -H 'Content-Type: application/json' "$@"; }

echo "== anonymous create"
R=$(json -X POST $B/api/v1/links -d '{"target_url":"https://example.com/page?ref=1"}')
SLUG=$(echo "$R" | jq -r .slug); TOKEN=$(echo "$R" | jq -r .claim_token); ID=$(echo "$R" | jq -r .id)
check "has claim_token" "$( [ ${#TOKEN} -ge 40 ] && echo yes)" yes
check "short_url" "$(echo "$R" | jq -r .short_url)" "$B/$SLUG"
check "no internal columns" "$(echo "$R" | jq 'has("claim_token_hash") or has("creator_hash") or has("user_id")')" false
check "anon custom slug -> 401" "$(code -X POST $B/api/v1/links -H 'Content-Type: application/json' -d '{"target_url":"https://example.com","slug":"mine"}')" 401
check "shortener target -> 400" "$(code -X POST $B/api/v1/links -H 'Content-Type: application/json' -d '{"target_url":"https://bit.ly/x"}')" 400
check "own-domain target -> 400" "$(code -X POST $B/api/v1/links -H 'Content-Type: application/json' -d "{\"target_url\":\"$B/abc\"}")" 400

echo "== redirect + tracking"
LOC=$(curl -s -o /dev/null -w '%{redirect_url}' -A "$IPHONE" -e https://t.co/abc "$B/$SLUG?utm_source=newsletter&utm_campaign=launch")
check "302 forwards utm" "$LOC" "https://example.com/page?ref=1&utm_campaign=launch&utm_source=newsletter"
curl -s -o /dev/null -A "$MAC" -e "https://l.facebook.com/l.php?u=x" "$B/$SLUG"
check "bot still redirected" "$(code -A 'WhatsApp/2.23.20.0 A' $B/$SLUG)" 302
check "curl counts as bot" "$(code $B/$SLUG)" 302
check "unknown slug -> 404" "$(code $B/doesnotexist1)" 404
sleep 2  # recorder flushes every second
check "lookup click_count (bots excluded)" "$(json -X POST $B/api/v1/links/lookup -d "{\"claim_tokens\":[\"$TOKEN\"]}" | jq '.[0].click_count')" 2

echo "== preview"
check "preview 200" "$(code $B/$SLUG+)" 200
check "preview shows target" "$(curl -s $B/$SLUG+ | grep -c 'example.com/page?ref=1')" 1
check "missing preview 404" "$(code $B/doesnotexist1+)" 404

echo "== sign in (magic link from logs)"
EMAIL="e2e-$(date +%s)@example.com"
check "magic link request" "$(code -X POST $B/auth/magic-link/signin -H 'Content-Type: application/json' -H "Origin: $B" -d "{\"email\":\"$EMAIL\"}")" 200
sleep 1
MAGIC=$(docker compose logs api 2>/dev/null | grep "magic link for $EMAIL" | tail -1 | sed 's/.*: //')
curl -s -o /dev/null -c "$JAR" "$MAGIC"
check "session cookie set" "$(grep -c limen_session "$JAR")" 1
check "claim without session -> 401" "$(code -X POST $B/api/v1/links/claim -H 'Content-Type: application/json' -d "{\"claim_tokens\":[\"$TOKEN\"]}")" 401
CL=$(json -b "$JAR" -X POST $B/api/v1/links/claim -d "{\"claim_tokens\":[\"$TOKEN\"]}")
check "claimed link" "$(echo "$CL" | jq '.claimed[0].id')" "$ID"
check "token spent after claim" "$(json -X POST $B/api/v1/links/lookup -d "{\"claim_tokens\":[\"$TOKEN\"]}" | jq length)" 0
check "claimed link in my list" "$(curl -s -b "$JAR" $B/api/v1/links | jq "[.[] | select(.id==$ID)] | length")" 1

echo "== stats"
S=$(curl -s -b "$JAR" "$B/api/v1/links/$ID/stats?days=7")
check "clicks" "$(echo "$S" | jq .clicks)" 2
check "bot_clicks" "$(echo "$S" | jq .bot_clicks)" 2
check "unique_visitors" "$(echo "$S" | jq .unique_visitors)" 2
check "daily has 7 days" "$(echo "$S" | jq '.daily | length')" 7
check "sources" "$(echo "$S" | jq -c '[.breakdown.sources[].name] | sort')" '["Facebook","X (Twitter)"]'
check "devices" "$(echo "$S" | jq -c '[.breakdown.devices[].name] | sort')" '["desktop","mobile"]'
check "os" "$(echo "$S" | jq -c '[.breakdown.operating_systems[].name] | sort')" '["iOS","macOS"]'
check "utm_sources" "$(echo "$S" | jq -c '.breakdown.utm_sources')" '[{"name":"newsletter","clicks":1}]'
check "countries (no geo configured)" "$(echo "$S" | jq -c '.breakdown.countries')" '[{"name":"Unknown","clicks":2}]'
check "other user's stats -> 404" "$(code "$B/api/v1/links/999999/stats" -b "$JAR")" 404

echo "== signed-in custom slug + delete"
CUSTOM="e2e-$(date +%s)"
C=$(json -b "$JAR" -X POST $B/api/v1/links -d "{\"target_url\":\"https://go.dev\",\"slug\":\"$CUSTOM\"}")
check "custom slug" "$(echo "$C" | jq -r .slug)" "$CUSTOM"
check "no claim token when signed in" "$(echo "$C" | jq 'has("claim_token")')" false
check "reserved slug -> 400" "$(code -b "$JAR" -X POST $B/api/v1/links -H 'Content-Type: application/json' -d '{"target_url":"https://go.dev","slug":"health"}')" 400
check "duplicate slug -> 409" "$(code -b "$JAR" -X POST $B/api/v1/links -H 'Content-Type: application/json' -d "{\"target_url\":\"https://go.dev\",\"slug\":\"$CUSTOM\"}")" 409
check "delete -> 204" "$(code -b "$JAR" -X DELETE $B/api/v1/links/$(echo "$C" | jq .id))" 204
check "deleted slug -> 404" "$(code $B/$CUSTOM)" 404

echo "== database"
ROW=$(docker exec server-db-1 psql -U postgres -d myapp -tAc "select count(*) filter (where is_bot), count(*) filter (where not is_bot), count(distinct length(visitor_hash)) from clicks where link_id=$ID")
check "clicks rows bot|human|hash-len-kinds" "$ROW" "2|2|1"
check "no ip/referer columns" "$(docker exec server-db-1 psql -U postgres -d myapp -tAc "select count(*) from information_schema.columns where table_name='clicks' and column_name in ('ip_address','referer')")" 0

rm -f "$JAR"
echo; echo "passed=$pass failed=$fail"
