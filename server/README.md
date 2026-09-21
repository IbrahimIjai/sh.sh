# sh.sh server

URL shortener API built with Go, Gin, PostgreSQL, [sqlc](https://sqlc.dev) for typed queries,
[goose](https://github.com/pressly/goose) for migrations, and [limen](https://github.com/thecodearcher/limen)
for auth (magic link + Google OAuth).

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

### 3. Start the database

```sh
docker compose up -d db
```

Postgres 17 listens on host port **5434** (5432/5433 are assumed taken), with data persisted
in the `pgdata` volume.

### 4. Run migrations (goose)

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

The server listens on `http://localhost:8080` and exposes a health check at `GET /health`.

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

## Testing

```sh
go test -race ./...                 # unit tests (no database needed)

goose up                            # apply new migrations to the local database
docker compose up --build -d        # run the stack
scripts/e2e.sh                      # end-to-end checks against it (needs curl and jq)
```
