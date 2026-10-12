# Proof run 20260929180432-e20a6a

Started 2026-09-29T18:04:32.324748+00:00, commit `b58d9faca6b0` with uncommitted changes.

## Topology

| Host | Roles | CPUs | Memory |
|---|---|---:|---:|
| laptop | api, db, gen, redis | 8 | 15.5 GiB |

2 API instances; API settings: `APP_ENV=development`, `DB_MAX_IDLE_CONNS=90`, `DB_MAX_OPEN_CONNS=90`, `EMAIL_PROVIDER=resend`, `LINK_CACHE_SIZE=100000`, `REQUEST_LOG=errors`.

Seeded 200,000 links and 200 signed-in sessions in 6.6 s.

## Stages

| Stage | Offered r/c/s per sec | Delivered r/c/s per sec | Unsent | Incorrect | Failovers | Redirect p50 / p99 | Create p99 | Report p95 | Clicks lost / duplicated | API / DB / Redis cores | Result |
|---|---|---|---:|---:|---:|---|---:|---:|---|---|---|
| warmup | 300 / 30 / 3 | 300 / 30 / 3.0 | 0 | 0 | 0 | 0.79 / 1.49 ms | 4.79 ms | 10.01 ms | 0 / 0 | 0.2 / 0.4 / 0.0 | Pass |
| steady | 600 / 60 / 5 | 600 / 60 / 5.0 | 0 | 0 | 0 | 0.27 / 1.99 ms | 16.44 ms | 8.13 ms | 0 / 0 | 0.3 / 0.3 / 0.1 | Pass |
| faults | 300 / 30 / 3 | 300 / 30 / 3.0 | 0 | 58 | 2,524 | 0.34 / 2.73 ms | 22.0 ms | 10.97 ms | 4 / 0 | 0.2 / 0.2 / 0.0 | Pass |

### faults: timeline

- 10.1 s: sh-proof-redis stopped
- 25.3 s: sh-proof-redis started
- 26.6 s: sh-proof-redis ready
- 35.0 s: sh-proof-db restarting
- 35.6 s: sh-proof-db ready
- 55.1 s: sh-proof-api-0 killed
- 70.2 s: sh-proof-api-0 started
- 70.3 s: sh-proof-api-0 ready

| From second | Redirects/sec | Incorrect | Failovers | Redirect p99 |
|---:|---:|---:|---:|---:|
| 0 | 300 | 0 | 0 | 1.7 ms |
| 5 | 300 | 0 | 0 | 1.4 ms |
| 10 | 300 | 0 | 0 | 51.3 ms |
| 15 | 300 | 0 | 0 | 51.5 ms |
| 20 | 300 | 0 | 0 | 51.2 ms |
| 25 | 300 | 0 | 0 | 3.6 ms |
| 30 | 297 | 22 | 0 | 2.8 ms |
| 35 | 295 | 36 | 0 | 3.7 ms |
| 40 | 300 | 0 | 0 | 1.8 ms |
| 45 | 300 | 0 | 0 | 1.8 ms |
| 50 | 300 | 0 | 29 | 2.2 ms |
| 55 | 300 | 0 | 880 | 1.9 ms |
| 60 | 300 | 0 | 825 | 1.6 ms |
| 65 | 300 | 0 | 784 | 1.8 ms |
| 70 | 300 | 0 | 6 | 2.5 ms |
| 75 | 300 | 0 | 0 | 1.8 ms |
| 80 | 300 | 0 | 0 | 1.6 ms |
| 85 | 300 | 0 | 0 | 2.6 ms |
| 90 | 0 | 0 | 0 | 0.5 ms |

## Takedown

Admin request answered in 0.06 s; the link stopped resolving on 2/2 instances, slowest after 0.13 s. Each check is a docker exec over SSH, so times are upper bounds with ~0.1-0.3 s resolution.

## Scope

- Direct HTTP on a private network: no TLS, CDN or load balancer. Generators pick instances at random and retry an unreachable one on another, as a load balancer would.
- Latency percentiles are the worst generator's, not merged across generators.
- A stage is only a capacity result if no generator was CPU-bound.
- Goals are the proposal targets in docs/010; the article sets throughput, not latency.
