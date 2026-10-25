# k6 tests

Start here for API checks and load tests. Each JavaScript file does one job.
Recorded benchmark results live in [`../../k6-benchmarks`](../../k6-benchmarks/README.md).

## Start a local test environment

Only Docker Compose is required. From the repository root:

```sh
cd server/k6
docker compose up -d --build --wait
docker compose exec -T db psql -U postgres -d k6 < seed.sql
docker compose run --rm k6 run smoke.js
```

Run the remaining commands from `server/k6/`.

This creates a separate `sh-k6` project with PostgreSQL, Redis, and the API at
`http://localhost:18080`. It does not read `server/.env`. `seed.sql` creates two test accounts
with sessions valid for one day; rerun it to refresh them. Load tests create their own links.

## Choose a test

```sh
docker compose run --rm k6 run health.js
docker compose run --rm k6 run smoke.js
docker compose run --rm k6 run clicks.js
docker compose run --rm k6 run redirects.js
docker compose run --rm k6 run create.js
docker compose run --rm k6 run stats.js
docker compose run --rm k6 run mixed.js
```

| File | What it checks | Default load |
|---|---|---|
| `health.js` | API liveness and database readiness | 5 pairs of requests/s, 30 seconds |
| `smoke.js` | Create, claim, lookup, ownership, preview, UTM forwarding, analytics, rename, delete, concurrent slug conflicts, domains, admin takedown | One pass |
| `clicks.js` | Send a known number of human/bot visits, then wait for analytics and compare exact totals | 100 visits, 5 concurrent users |
| `redirects.js` | Correct 302 status and destination; destinations are never followed | 100 redirects/s, 30 seconds |
| `create.js` | Signed-in link creation and immediate preview visibility | 10 creations/s, 30 seconds; one preview per creation |
| `stats.js` | Analytics response, link ownership and requested date range | 10 reports/s, 30 seconds |
| `mixed.js` | Redirects, creations and reports at the same time | 100 redirects + 10 creations + 5 reports/s, 30 seconds |
| `rate-limit.js` | Anonymous quota: first 20 links succeed, the 21st returns 429 | One pass; use a fresh stack |

Run `rate-limit.js` before `smoke.js` on a fresh stack, then reset before further anonymous
tests. The quota is per client IP and lasts an hour. All load tests use signed-in accounts.
`smoke.js` consumes one anonymous creation per run. Admin checks run when `ADMIN_TOKEN` is set
(it is supplied by this Compose file). Email login itself is outside these seeded-session checks; the Go auth tests remain separate.

## Change the load

Pass settings with `-e` before `k6`:

```sh
# 1,000 redirects per second across 100 links for one minute
docker compose run --rm -e RATE=1000 -e LINKS=100 -e DURATION=1m k6 run redirects.js

# One popular link
docker compose run --rm -e RATE=1000 -e LINKS=1 k6 run redirects.js

# Mixed traffic
docker compose run --rm -e REDIRECT_RATE=1000 -e CREATE_RATE=50 -e STATS_RATE=10 k6 run mixed.js

# Verify 1,000 clicks are stored, with 20 concurrent users
docker compose run --rm -e REQUESTS=1000 -e VUS=20 k6 run clicks.js

# Longer run or a short report burst
docker compose run --rm -e DURATION=30m k6 run mixed.js
docker compose run --rm -e RATE=100 -e DURATION=5s k6 run stats.js
```

| Setting | Meaning | Default |
|---|---|---|
| `RATE` | Operations started each second in a single load test | See test table |
| `DURATION` | Load duration, including a unit (`30s`, `1m`) | `30s` |
| `LINKS` | Links created before redirect, analytics, or mixed tests | `10` |
| `VUS` / `MAX_VUS` | Initial / maximum concurrent k6 users, per load scenario | `20` / at least `100` |
| `P95_MS` | Maximum acceptable 95th percentile request time, in milliseconds | `500` |
| `DAYS` | Analytics date range | `30` |
| `LINK_ID` | Existing owned link for `stats.js`, instead of creating links | Unset |
| `SLUG` and `TARGET` | Existing link and exact destination for `redirects.js` | Unset |
| `REDIRECT_RATE`, `CREATE_RATE`, `STATS_RATE` | Rates in `mixed.js`; use `0` to disable a workload | `100`, `10`, `5` |
| `REQUESTS` | Total visits in `clicks.js` | `100` |

`RATE` counts operations: creation also sends one preview request, and health sends two requests.
Setup requests appear in overall HTTP totals, but not the per-scenario latency thresholds.
Redirect tests spread visits evenly across links and rotate human/bot traffic; they do not load
the destination website. The local stack has no CPU or memory caps. Record any limits and the
machine used when publishing results; older capped benchmark numbers are not directly comparable.

## Read the result

k6 prints checks, request counts, latency and pass/fail thresholds. A failed check makes the
command exit unsuccessfully. Load tests require correct responses, fewer than 1% HTTP errors,
95th percentile latency below `P95_MS`, and zero dropped iterations.

`dropped_iterations` means k6 could not start all requested operations. The offered rate was
not sustained; inspect API latency and generator CPU, then adjust load or `VUS` / `MAX_VUS`.
The scripts use k6's [constant arrival rate executor](https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/constant-arrival-rate/).

Use `clicks.js` for an exact persistence check. A successful load run alone does not prove every
click was stored. It leaves the link ID in the output so you can also inspect the database:

```sh
docker compose exec db psql -U postgres -d k6
# In psql, replace 123 with the link ID printed by clicks.js:
# SELECT count(*), count(*) FILTER (WHERE is_bot) FROM clicks WHERE link_id = 123;
```

## Test analytics with more data

```sh
docker compose exec -T db psql -U postgres -d k6 -v clicks=100000 < history.sql
docker compose restart api
docker compose up -d --wait
# Use the link_id printed by history.sql:
docker compose run --rm -e LINK_ID=123 -e RATE=20 k6 run stats.js
```

The fixture adds clicks over the last 30 days to `k6-history`. Restarting runs maintenance so
closed days are rolled up. Rerunning the fixture adds another batch. This measures one large
link's reports; it does not simulate thousands of separately authenticated dashboard accounts.

## Test cache and dependency recovery

To compare caching or click queues, recreate only the local test API:

```sh
LINK_CACHE_SIZE=0 docker compose up -d --force-recreate --wait api
docker compose run --rm k6 run redirects.js
CLICK_QUEUE=memory docker compose up -d --force-recreate --wait api
docker compose run --rm k6 run clicks.js
```

For recovery checks, run `redirects.js` with `DURATION=2m` and `LINKS=1`. Once requests are
flowing, use a second terminal in this directory to stop one dependency and then start it:

```sh
docker compose stop redis
docker compose start redis
# Or repeat with db. Restart api to exercise loss of an API instance.
```

Observe errors and latency during the interruption. A cached redirect may keep working while
PostgreSQL is down; uncached links and writes can fail. Do not remove the thresholds to hide
those failures. After recovery, rerun `health.js`, `smoke.js` and `clicks.js`. These checks verify
recovery and new click persistence, not durability of every click sent during the outage.
The Go integration tests cover database retries, deduplication, and stream handling.

## Run against another test deployment

With a local [k6 installation](https://grafana.com/docs/k6/latest/set-up/install-k6/):

```sh
BASE_URL=https://your-test-host.example SLUG=abc123 TARGET=https://example.com/test \
  k6 run redirects.js
```

For creation, analytics, mixed, and click tests, set `SESSION_COOKIE` to the full cookie
(`limen_session=...`) for a test account. Smoke also needs `OTHER_SESSION_COOKIE` for a second
account. Point `BASE_URL` at a load balancer to exercise multiple API instances. The local
fixture credentials are only valid in the disposable stack. Use dedicated links and accounts:
these tests record clicks and create data.

## Save results and clean up

```sh
mkdir -p ../../k6-benchmarks/local
docker compose run --rm -v "$PWD/../../k6-benchmarks/local:/results" \
  --user "$(id -u):$(id -g)" k6 run --summary-export=/results/mixed.json mixed.js

# Removes only this sh-k6 test project and its data.
docker compose down -v
```

New local result files are ignored by Git. Keep the default terminal summary; no custom report
generator is needed. The old Python `article`, `proof`, dashboard runners and Barrage harness
have been removed. Historical results remain in `k6-benchmarks/`; their thresholds, fixtures,
resource limits and fault schedules describe those old runs, not this suite. Go unit/integration
tests and frontend checks remain in their existing locations.
