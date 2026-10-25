# k6 benchmarks

Measured results for shortened.cv, with the environment each run used. The write-up that explains
them is at [iamibrahim.xyz/articles/building-shortened-cv](https://iamibrahim.xyz/articles/building-shortened-cv),
and the longer technical version is [`WRITEUP.md`](../WRITEUP.md).

| Run | Where | What it shows |
|---|---|---|
| [2026-09-29 local](2026-09-29-local/) | One laptop, Docker, plain HTTP | How far the design goes: mixed workloads, analytics under load, dependency failures |
| [2026-10-12 production](2026-10-12-production/) | The live VPS, through HTTPS and Traefik | What the deployed service sustains on real infrastructure |
| [2026-10-14 local redirect](2026-10-14-local-redirect/) | One laptop, no limits, plain HTTP | What a cached redirect and its click cost, and whether every click is stored |

## 2026-10-14: local, one link, no limits

Redirects to one link on a laptop with nothing capped: the Go API as a native process, PostgreSQL
17.11 and Redis 7.2.16 in Docker, and k6 v0.57.0 on the same machine, over plain HTTP.

**Host:** Intel Core i7-8706G (4 cores / 8 threads), 16 GB RAM, also running a desktop, editor and
browser. Details in [`environment.json`](2026-10-14-local-redirect/environment.json).

Each step ramps for 20 s, then holds the target for 60 s, using
[`redirect-live.js`](2026-10-12-production/scripts/redirect-live.js) with the corrected warm-up timing.

| Target | Delivered | Errors | p50 | p95 | p99 | Dropped iterations | Clicks stored |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 4,000 r/s | 4,000 r/s | 0% | 0.2 ms | 3.2 ms | 8.8 ms | 0 | 283,945 of 283,945 |
| 10,000 r/s | 9,807 r/s | 0% | 1.3 ms | 26.8 ms | 61.3 ms | 11,829 | 698,171 of 698,171 |

Clicks stored covers every request each run sent, warm-up included, counted in PostgreSQL after the
Redis stream drained. The stream and its dead-letter stream were both empty afterwards. An earlier
10,000 r/s attempt delivered 9,558 r/s (p95 39.3 ms) and also stored every click.

CPU during the 4,000 r/s hold, in logical CPUs: API 0.70 (including the click workers), PostgreSQL
0.34, Redis 0.02, k6 1.53. Samples: [per process](2026-10-14-local-redirect/cpu-4000-pidstat.txt),
[host](2026-10-14-local-redirect/cpu-4000-mpstat.txt). The k6 output of the 10,000 r/s run is in
[`k6-10000.png`](2026-10-14-local-redirect/k6-10000.png).

**What this supports:** on this CPU, a cached redirect and its click cost about 0.27 cores per
1,000 r/s across the API, PostgreSQL and Redis, and the click pipeline kept up with about 9,800
clicks a second without losing any. At 10,000 r/s, the shared laptop CPU was the limit: k6 couldn't
keep its schedule, and every request it did send got a correct redirect.

**What it does not:**
- Every request hit one link, always in the in-process cache. Cache misses and many links were not
  tested here (the 2026-09-29 run covers a mixed workload).
- Plain HTTP over keep-alive connections, with no TLS or proxy. Not comparable to the production
  run, or to the capped 2026-09-29 runs.
- k6 and a desktop session shared the CPU, so neither the API's own limit nor a production capacity
  is established. Scaling the 4,000 r/s figures puts 10,000 r/s at about 2.7 cores for the
  service; that is an estimate, not a measurement.

## 2026-10-12: production VPS

Redirects to one link on `https://shortened.cv`, through the full production path: TLS termination
in Traefik, the Go API, and click recording through Redis into PostgreSQL.

**Host:** Contabo VPS, 4 vCPU AMD EPYC (KVM), 7.8 GiB RAM, Ubuntu 24.04, shared with two other
applications. **Load generator:** k6 v2.3.0 on the same VPS. Details in
[`environment.json`](2026-10-12-production/environment.json).

Each step ramps to the target rate for 20 s, then holds it for 60 s. Only the held minute is measured.

The script used for this run started its 20 s warm-up clock separately for each k6 virtual user, so
users added mid-run had their first 20 s left out of the held-minute metrics. The figures below are
therefore approximate. [`redirect-live.js`](2026-10-12-production/scripts/redirect-live.js) now times
the warm-up from the scenario start.

| Target | Delivered | Errors | p50 | p95 | p99 | Host CPU | API | Traefik | k6 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1,000 r/s | 1,000 r/s | 0% | 2.1 ms | 13.8 ms | 39.6 ms | 71% | 0.4 vCPU | 0.7 vCPU | 0.7 vCPU |
| 2,000 r/s | 1,978 r/s | 0% | 5.5 ms | 68.1 ms | 152 ms | 86% | 0.5 vCPU | 1.0 vCPU | 1.0 vCPU |
| 3,000 r/s | stopped | 0% | — | 7.7 s | — | 98% | 0.2 vCPU | 1.4–2.8 vCPU | 1.1–3.4 vCPU |

Every response was a `302` to the right target. At 2,000 r/s the API process used about half a vCPU
and 115 MB of memory. At 3,000 r/s the host ran out of CPU while k6 was still ramping up; requests
queued for 5–8 s and the run stopped itself. The API stayed healthy: `/health/ready` returned 200 after every step.

An earlier pass without the warm-up stepped from 50 to 1,000 r/s, 60 s per step. Every step up to
800 r/s passed with p95 under 10 ms. Its 1,000 r/s step failed (p95 784 ms, no errors) because k6
opened new TLS connections mid-run, which is why the second pass warms up first.
[Results](2026-10-12-production/run1-50-to-1000/).

**What this supports:** on this 4 vCPU VPS, the deployed service handled about 2,000 redirects a
second through HTTPS with no errors, and the host saturated between 2,000 and 3,000. The Go API was
not the limit: TLS in Traefik and the load generator used most of the CPU.

**What it does not:**
- k6 reused connections. Real visitors usually open a new TLS connection, which costs Traefik more,
  so the rate for new visitors will be lower.
- Every request hit one link, always served from the in-process cache. Many links with cache misses
  were not tested here (the local run covers that).
- k6 shared the 4 vCPUs. A load generator on another machine would leave more CPU for the service.
- One minute per step is not a statement about sustained daily traffic.

## 2026-09-29: local

One laptop: Intel Core i7-8706G, 8 threads, 16 GB RAM. API limited to 2 CPUs and 512 MiB,
PostgreSQL to 2 CPUs and 1 GiB, Redis to 0.5 CPU and 256 MiB. Plain HTTP over Docker networking,
k6 on the same machine. Details in [`environment.json`](2026-09-29-local/environment.json).

| Offered workload | Duration | Redirect p99 | Result |
|---|---|---|---|
| 1,000 redirects/s + 50 creates/s + 10 reports/s | 120 s | 1.3 ms | Completed |
| 3,200 redirects/s + 100 creates/s + 10 reports/s | 60 s | 7.9 ms | Completed |
| 5,000 redirects/s + 100 creates/s + 10 reports/s | 30 s | 52.6 ms | Fell behind |
| 5,000 redirects/s to one popular link | 30 s | 14.2 ms | Completed |

- [`load-results.json`](2026-09-29-local/load-results.json): every load stage, including failed attempts
- [`reporting-results.json`](2026-09-29-local/reporting-results.json) and
  [`reporting-seed.json`](2026-09-29-local/reporting-seed.json): analytics with three million stored clicks
- [`fault-checks.json`](2026-09-29-local/fault-checks.json): PostgreSQL outage, API crash, graceful shutdown
- [`two-instance-rehearsal.md`](2026-09-29-local/two-instance-rehearsal.md): two API instances with Redis and API faults

The run was taken at commit `b58d9fa` with uncommitted changes, listed in `environment.json`.
Secrets in the container environment are redacted; they were local test values.

## Run the current tests

Use the [k6 guide](../server/k6/README.md). The maintained tests live in `server/k6/`, with
separate files for health, API behavior, redirects, creation, analytics, mixed traffic, and
click persistence. All use the same settings and a separate local Docker Compose stack.

These recorded results came from earlier harnesses. The Python `load` / `proof` runners and
Barrage were removed when the tests were simplified. Their recorded numbers remain unchanged;
the current suite does not recreate their exact fixtures, limits, or fault schedules.
The [production scripts](2026-10-12-production/scripts/) are kept only as historical context
for the published measurements. Use `server/k6/redirects.js` for new runs.
