# shortened.cv

A URL shortener with click analytics. Paste a link, get a short one, and see who clicked it, from
where, and on what device, without storing raw IPs.

- **Short links:** `https://shortened.cv/{slug}`
- **Web app:** `https://app.shortened.cv`

## Features

- Anonymous shortening (rate-limited), with links you can claim after signing in
- Custom slugs for signed-in users
- Sign-in with a magic link or Google
- Per-link stats: clicks, unique visitors, daily series, sources, referrers, countries, devices,
  browsers, UTM tags, with bots filtered out
- Preview any link before visiting it: `shortened.cv/{slug}+`
- Malware and phishing checks (Google Web Risk), and an admin takedown API

## Performance

| Where | Result |
|---|---|
| Production VPS (4 vCPU), through HTTPS and Traefik | 2,000 redirects/s for 60 s, 0 errors, p95 68 ms; the Go API used about half a vCPU |
| One laptop, plain HTTP | 3,200 redirects/s with 100 creates/s and 10 reports/s for 60 s, p99 7.9 ms |

Environment, raw results, and what each number does and does not show: [`benchmarks/`](benchmarks/).
The story behind them: [Building shortened.cv](https://iamibrahim.xyz/articles/building-shortened-cv).

## Stack

| | |
|---|---|
| API | Go, Gin, PostgreSQL, Redis (optional), sqlc, goose, limen |
| Web app | Nuxt 4, Nuxt UI, TanStack Query |
| Hosting | Coolify on one VPS, behind Traefik |

```
visitor ──▶ Traefik ─┬─ shortened.cv      ──▶ api :8080 ──▶ PostgreSQL, Redis
                     └─ app.shortened.cv  ──▶ ui  :3000
```

`shortened.cv/` redirects to the web app. Every other unknown path is treated as a slug.

## Repository

```
server/       Go API: redirects, /api/v1, /auth
ui/           Nuxt web app
benchmarks/   Load test results and the scripts that produced them
WRITEUP.md    Technical write-up: design, caching, click pipeline, load and failure tests
```

## Quick start

You need Go 1.26+, Docker and [Bun](https://bun.sh). The `goose` and `sqlc` CLIs are only needed
to inspect migrations or regenerate queries.

```sh
# API
cd server
cp .env.example .env              # fill in LIMEN_SECRET, plus Google and Resend keys if you need them
docker compose up -d db redis
set -a; . ./.env; set +a
go run ./cmd/api                  # applies migrations, then serves http://localhost:8080

# Web app (in another terminal)
cd ui
bun install
bun run dev                       # http://localhost:3000
```

Without email keys, magic links are printed to the API log.

## Tests

```sh
cd server && go test -race ./...
cd ui && bun run lint && bun run typecheck
```

[`server/README.md`](server/README.md) explains how to run the PostgreSQL and Redis integration
tests and the end-to-end script.

## Documentation

- [`server/README.md`](server/README.md): API reference, configuration, analytics, caching
- [`WRITEUP.md`](WRITEUP.md): how it works and how it was tested
- [`benchmarks/README.md`](benchmarks/README.md): results and environments
- [`server/scripts/load/README.md`](server/scripts/load/README.md): the local load harness
