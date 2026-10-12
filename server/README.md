# sh.sh server

URL shortener API built with Go, Gin, PostgreSQL, optional Redis, [sqlc](https://sqlc.dev) for typed
queries, [goose](https://github.com/pressly/goose) for migrations, and [limen](https://github.com/thecodearcher/limen)
for auth (magic link + Google OAuth).

Code layout: `cmd/api` (wiring), `internal/server` (routes, shutdown, metrics listener),
`internal/middleware`, `internal/handler` (HTTP only), `internal/service` (business rules),
`internal/db/generated` (sqlc), plus `cache`, `rediscache`, `analytics`, `clickstream`. The maintenance job is `service/maintenance.go`.
Metrics live in `service/metrics.go` (what is counted) and `handler/metrics.go` (request timing and the
internal `/metrics` listener). `scripts/load` and `scripts/proof` are the benchmark harnesses.

## How to run locally

### Prerequisites

- Go 1.26+
- Docker (for the local Postgres)
- CLI tooling:

  ```sh
  go install github.com/pressly/goose/v3/cmd/goose@latest
  go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
  ```

  Make sure `$(go env GOPATH)/bin` is on your `PATH`.

### 1. Install dependencies

```sh
cd server
go mod download        # fetch modules into the module cache
go mod tidy            # only if you changed imports
go build ./...         # verify everything compiles
```

### 2. Configure the environment

```sh
cp .env.example .env
```

Then fill in `LIMEN_SECRET`, the `GOOGLE_*` values, `RESEND_API_KEY`, and `RESEND_FROM`.
`PORT`, `BASE_URL`, `DATABASE_URL`, and the `GOOSE_*` variables already point at the
local Docker Postgres.

Magic-link emails use Resend by default (`EMAIL_PROVIDER=resend`). Transend remains
available: set `EMAIL_PROVIDER=transend` and fill in `TRANSEND_API_KEY` and
`TRANSEND_FROM` to switch back. If the selected provider's API key or sender is
missing, magic links are logged instead of emailed.

The database pool defaults to `DB_MAX_OPEN_CONNS=20` and `DB_MAX_IDLE_CONNS=20`.
These limits apply per API process and are shared by auth, redirects, and analytics.
When the pool is busy, queries wait for a connection until their context is canceled.
Idle connections are reused; unused ones expire after 5 minutes, and connections
are recycled after 30 minutes. Invalid pool settings stop startup with an error.
Across replicas, keep the combined connection budget below PostgreSQL's limit,
leaving room for migrations and administration.

### 3. Start the database and Redis

```sh
docker compose up -d db redis
```

Postgres 17 listens on host port **5434** (5432/5433 are assumed taken), with data persisted
in the `pgdata` volume. Redis listens on host port **6380**; set `REDIS_URL=redis://localhost:6380/0`
in `.env` to use it from `go run`. Without `REDIS_URL` the API runs with its in-process cache only.

### 4. Run migrations (goose)

The API applies pending migrations itself at startup (`RUN_MIGRATIONS=true`, the default). The SQL
files are embedded in the binary, and a Postgres advisory lock lets one instance migrate while
others wait. If a migration fails, the API logs which one and exits without serving.

Use the goose CLI to inspect the schema, roll back, or migrate without starting the API.
`.env` defines `GOOSE_DRIVER`, `GOOSE_DBSTRING`, and `GOOSE_MIGRATION_DIR`, and the goose CLI
auto-loads `.env` from the current directory — so run it bare from `server/`:

```sh
goose status                       # list applied / pending migrations
goose up                           # apply all pending migrations
goose up-by-one                    # apply just the next one
goose up-to 2                      # migrate up to version 2
goose down                         # roll back the last migration
goose down-to 0                    # roll back everything
goose redo                         # down + up the latest migration
goose version                      # current DB version
goose reset                        # roll back all (follow with `goose up`)
goose validate                     # lint migration files
goose create add_something sql     # new migration in db/migrations
goose fix                          # convert timestamped names to sequential
```

Without relying on `.env`, pass everything explicitly:

```sh
goose -dir db/migrations postgres "postgres://postgres:postgres@localhost:5434/myapp?sslmode=disable" up
```

### 5. Generate the query layer (sqlc)

Run this after editing `db/queries/*.sql` or `db/migrations/*.sql`:

```sh
sqlc generate      # writes internal/db/generated
```

Generated code is committed, so this is only needed when the SQL changes.

### 6. Run the API

`cmd/api/main.go` reads plain `os.Getenv` (there is no dotenv loader), so export `.env` first:

```sh
set -a; . ./.env; set +a
go run ./cmd/api
```

Or build a binary:

```sh
go build -o bin/api ./cmd/api && ./bin/api
```

The server listens on `http://localhost:8080`. `GET /health` reports that the process is up
(use it for liveness), and `GET /health/ready` also checks the database (use it for readiness,
so a replica that can't reach Postgres stops receiving traffic without being restarted).

With `APP_ENV=production`, the API refuses to start unless magic-link email is configured;
elsewhere, magic links are logged instead of emailed.

### Run everything in Docker

Builds the API image and starts it alongside Postgres (the API container reads `.env` via
`env_file` and overrides `DATABASE_URL` to use the `db` service hostname):

```sh
docker compose up --build
```

Useful extras:

```sh
docker compose logs -f api     # tail API logs
docker compose down            # stop containers
docker compose down -v         # stop and wipe the Postgres volume
```

## API

| Method & path | Auth | What it does |
|---|---|---|
| `POST /api/v1/links` | optional | Shorten a URL. Signed in: you own it and may pick a `slug`. Anonymous: random slug, 20 links/hour per IP, response includes a one-time `claim_token`. |
| `POST /api/v1/links/lookup` | none | `{"claim_tokens": [...]}` → the anonymous links behind those tokens (for the "recent links" list). |
| `POST /api/v1/links/claim` | session | `{"claim_tokens": [...]}` → moves those anonymous links (and their clicks) into your account. Tokens stop working afterwards. |
| `GET /api/v1/links` | session | Your links. |
| `GET /api/v1/links/:id/stats?days=30` | session | Clicks, unique visitors, bot clicks, a zero-filled daily series, and top sources, referrers, countries, cities, devices, browsers, operating systems, UTM sources and campaigns. |
| `DELETE /api/v1/links/:id` | session | Soft-delete one of your links. |
| `GET /{slug}` | none | 302 to the target. UTM tags on the short link are recorded and passed on to the target. |
| `GET /{slug}+` | none | Preview page: shows where the link goes without redirecting. |
| `POST /api/v1/admin/takedown` | admin token | `{"slugs": [...]}` and/or `{"target_host": "bad.example"}` (subdomains included) → deactivates the links and stops them redirecting on every instance. |

Admin routes exist only when `ADMIN_TOKEN` is set (32+ characters) and need
`Authorization: Bearer <token>`.

Custom slugs are 3–64 letters, digits, `-` or `_`, and can't be a reserved word (`api`, `auth`, `health`, ...).
Links pointing at our own domain or at other URL shorteners are rejected.

### Anonymous links in the frontend

1. `POST /api/v1/links` without a session. Store `{short_url, target_url, claim_token}` in `localStorage`.
2. Show the "recent links" list from `localStorage`; refresh click counts with `POST /api/v1/links/lookup`.
3. After the user signs in, `POST /api/v1/links/claim` with every stored token, then forget the tokens.

### Click analytics

Clicks are written in the background in batches, so redirects only do one lookup. Each click stores:
source (referring site, else the in-app browser such as Instagram, else "Direct"), referrer host,
UTM source/medium/campaign, device, browser, OS, country, city, and whether it was a bot.
Link-preview bots (WhatsApp, Slack, X, Discord, ...) and scripts such as `curl` still get redirected,
but they don't count as clicks; use a browser, or `curl -A` with a browser user agent, when testing.

Apply migrations `00007_click_storage.sql` and `00008_click_events.sql` (with the old API stopped)
before starting this version.

Every click gets an event ID when it is recorded. The click rows and their `link_counters`
increments commit in one statement that skips event IDs already stored, so a batch retried after a
lost acknowledgement, or written twice by two instances, never counts twice. Click timestamps
reflect the original request, including when retries cross midnight.

**Click queue (`CLICK_QUEUE`):**

- `redis` (the default when `REDIS_URL` is set): within 20 ms, clicks are appended to the Redis
  stream `sh:clicks` as entries of up to `CLICK_BATCH_SIZE` (1,000). `CLICK_WRITERS` (2) consumers
  per instance write entries to PostgreSQL and delete them only after the commit. An instance that
  crashes loses at most the clicks it hadn't appended yet; entries it was writing are taken over by
  another instance after a minute. While Redis is down, or the stream holds more than
  `CLICK_STREAM_MAX_MB` (64), new clicks take the memory path instead. Entries PostgreSQL rejects as
  invalid move to `sh:clicks:dead`. Redis needs `appendonly yes` (`appendfsync everysec`) so the
  stream survives a Redis restart.
- `memory`: clicks wait in RAM (`CLICK_BUFFER_SIZE`, default 100,000) and `CLICK_WRITERS`
  goroutines write batches of `CLICK_BATCH_SIZE`, or every second. A crash loses what's in RAM; a
  full buffer drops new clicks without blocking redirects.

Failed writes retry with exponential backoff and jitter (capped at 5 seconds; each attempt has a
10-second timeout). Shutdown appends or writes what's buffered within the 15-second shutdown
budget. With `CLICK_ASYNC_COMMIT=true` (default), click batches commit without waiting for the WAL
flush: a PostgreSQL crash can lose up to about 0.6 s of committed clicks. Links and accounts always
commit synchronously.

Storage: `clicks` is partitioned by UTC day and keeps only the parsed fields (no raw user agent).
The API's maintenance job (every `MAINTENANCE_INTERVAL`, default 15 minutes, one instance at a time)
creates partitions ahead, rolls closed days up into `click_daily` / `click_daily_dims`, drops raw
partitions older than `CLICK_RAW_RETENTION_DAYS` (30) once rolled up. Stats read rollups for past
days and raw clicks for the rest. It runs at every start too.

No raw IP or full referrer URL is stored. Visitors are counted with a keyed hash of IP + user agent
that changes every UTC day, so visits can't be linked across days.

Country/city need one of:

- `TRUSTED_PLATFORM=cloudflare` when the API is only reachable through Cloudflare (uses
  `CF-IPCountry`; enable Cloudflare's "Add visitor location headers" for `CF-IPCity`), or
- `GEOIP_DB_PATH` pointing at a MaxMind **GeoLite2-City** `.mmdb` file (free account at maxmind.com;
  mount it into the container and keep it updated).

Set `TRUSTED_PROXIES` to your load balancer's IPs/CIDRs if one sits in front of the API; otherwise
leave it empty so clients can't fake their IP with `X-Forwarded-For`.

`WEB_RISK_API_KEY` (Google Cloud Web Risk) makes new links pointing at known malware or phishing
fail with 400. If Web Risk is unreachable, links are still created.

### Caching, Redis and operations

- **Redirect cache:** each API process keeps up to `LINK_CACHE_SIZE` links for `LINK_CACHE_TTL`.
  Concurrent misses for one slug share a single lookup, and database lookups from misses are capped
  at half the pool. If PostgreSQL fails, an entry is served up to `LINK_CACHE_STALE` (5m) after it
  expired; uncached slugs then get 503.
- **Redis (`REDIS_URL`, optional):** a shared cache behind the in-process one (5-minute TTL),
  deletions and takedowns written as 10-minute revocations and broadcast so every instance forgets
  the slug, and an exact 20-per-hour anonymous quota. Every key has a TTL, so run Redis with
  `maxmemory-policy volatile-lru`. If Redis is down, the API keeps working and falls back.
- **Reports** run at most a quarter of the pool at a time, so they can't starve redirects.
- **Metrics (`METRICS_ADDR`, e.g. `:9090`):** `/metrics` (Prometheus) and `/debug/pprof` on a separate
  listener. Keep that port off the public proxy.
- **Access log (`REQUEST_LOG`):** `all` (default), `errors` (status 400 and up), or `off`.

## Testing

```sh
go test -race ./...                 # unit tests (no database needed)

# Before pushing a new migration: apply every migration to a fresh schema, roll all of them
# back, and apply them again. Catches broken Up and Down sections.
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5434/myapp?sslmode=disable' \
  go test -count=1 -run TestMigrations ./internal/database

# Optional PostgreSQL integration tests. Creates and removes an isolated schema;
# the test user needs permission to create schemas. Use a local test database.
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5434/myapp?sslmode=disable' \
  go test -race -count=1 ./internal/analytics ./internal/database ./internal/service

# The click stream against PostgreSQL and Redis. Uses its own stream key and schema.
TEST_DATABASE_URL='...' TEST_REDIS_URL='redis://localhost:6380/0' go test -race -count=1 ./internal/clickstream

goose up                            # apply new migrations to the local database
docker compose up --build -d        # run the stack
scripts/e2e.sh                      # end-to-end checks against it (needs curl and jq)
```
