# Prometheus

The API's request middleware records counts, response times, and requests currently in progress.
It also exports Go runtime, database-pool, redirect-cache, and click-pipeline metrics.

## Start it

From `server/`, after configuring `.env` as described in the server README:

```sh
docker compose up -d --build
```

Open <http://localhost:9090> and check <http://localhost:9090/targets>. The `api` target should
be UP. Prometheus scrapes `api:9090/metrics` and its own metrics every **15 seconds**, with a
5-second timeout. Rule evaluation is set to 10 seconds; no alert rules are configured yet.

The public API is on port 8080. Its metrics and pprof listener is accessible only inside the
Docker network. The Prometheus UI binds to host loopback. Samples persist in `prometheusdata`;
Prometheus's default retention is 15 days.

For a native `go run ./cmd/api` process, set `METRICS_ADDR=127.0.0.1:9091` and inspect
<http://localhost:9091/metrics>. Leaving `METRICS_ADDR` empty disables instrumentation and the
internal listener. The supplied scrape target is for the Docker API, not a native host process.

## Request metrics

| Metric | Meaning |
|---|---|
| `http_requests_total{route,method,status}` | Completed requests, including errors |
| `http_request_duration_seconds{route,method}` | Response-time histogram, in seconds |
| `http_requests_in_flight` | Requests currently being handled |

Routes use templates such as `/api/v1/links/:id/stats`. Short-link requests share `redirect`;
previews share `preview`; other unrecognized paths share `unmatched`. Unknown HTTP methods use
`OTHER`. Slugs, link IDs, query strings, and user details never become metric labels. The
middleware wraps recovery, so recovered panics count as 500 responses. Internal scrapes do not
count as public API requests. Preview and unmatched traffic previously shared the `redirect` label.

## Useful queries

Paste these into Prometheus's query page. Rate queries need at least two scrapes; allow a minute
of traffic for a useful graph. Health checks are included in totals unless filtered out.

```promql
# Is the API scrape succeeding? 1 = yes, 0 = no.
up{job="api"}

# Requests per second, grouped by route.
sum by (route) (rate(http_requests_total{job="api"}[1m]))

# Server errors per second.
sum(rate(http_requests_total{job="api",status=~"5.."}[1m]))

# 95th percentile response time by route, in milliseconds.
1000 * histogram_quantile(0.95,
  sum by (le, route) (rate(http_request_duration_seconds_bucket{job="api"}[1m]))
)

# Requests being handled right now.
sum(http_requests_in_flight{job="api"})

# Clicks waiting in the in-memory buffers; Redis backlog is separate.
sum(click_queue_depth{job="api"})
sum(click_stream_backlog_entries{job="api"})

# Redirect cache hits per second.
sum(rate(link_cache_hits_total{job="api"}[1m]))
```

## Watch k6 tests

The separate test stack can run Prometheus using the same configuration:

```sh
cd server/k6                         # from the repository root
docker compose --profile monitoring up -d --build --wait
docker compose exec -T db psql -U postgres -d k6 < seed.sql
docker compose run --rm -e DURATION=1m k6 run mixed.js
```

Open <http://localhost:19090> for test metrics. The test API remains on port 18080, with its
metrics listener private. Cleanup: `docker compose --profile monitoring down -v`.

## Change the scrape configuration

Edit [`prometheus.yml`](prometheus.yml), then validate and restart from `server/`:

```sh
docker compose run --rm --no-deps --entrypoint /bin/promtool prometheus \
  check config /etc/prometheus/prometheus.yml
docker compose restart prometheus
```

For the k6 stack, run those commands from `server/k6/` instead.
See the [Prometheus configuration reference](https://prometheus.io/docs/prometheus/latest/configuration/configuration/)
for scrape settings.
