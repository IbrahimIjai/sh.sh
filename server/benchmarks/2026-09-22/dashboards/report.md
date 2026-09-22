# Dashboard analytics under redirect load — 2026-09-22

The current server **did not meet the hypothetical 20,000-dashboard target** on
the unchanged resource limits. That target schedules 666.67 authenticated analytics
requests/sec alongside 1,000 redirects/sec. PostgreSQL approached its two-core
quota, analytics requests reached the five-second client timeout, and redirect
performance deteriorated. The target stage aborted after 31.8 seconds.

A separate fresh-database run passed the aggregate thresholds for **1,000 dashboard
equivalents plus 1,000 redirects/sec for 120 seconds**, with all acknowledged
clicks recorded. Popular-link analytics still had a **1.77-second p95**, hidden by
the much faster ordinary links in the overall percentile. A lower-load confirmation
immediately following overload failed; recovery remains an open investigation.

These are synthetic local measurements, not evidence of Bitly's traffic, 20,000
simultaneously executing requests, or a day of sustained production service.

## Setup

| Component | Configuration |
| --- | --- |
| API | Go server, 2 CPU quota, 512 MiB limit, 20 open/idle database connections |
| Database | PostgreSQL 17, 2 CPU quota, 1 GiB limit |
| Generator | Grafana k6 2.3.0, same host, no generator CPU/memory quota |
| Host | Intel i7-8706G, four physical cores/eight threads, 16 GiB installed RAM |
| Fixtures | 20,000 verified accounts/sessions; 100,000 links, five per account |
| History | 3,000,000 clicks across 90 UTC days; initially about 936 MiB of database storage |
| Requests | HTTP over an isolated Docker network; no TLS/CDN; normal API request logging enabled |

One third of the history belongs to one link, another third is distributed across
99 other popular links, and the remaining third across every account's first
link. The largest link starts with 1,000,050 clicks. The database is vacuumed and
analyzed after seeding. Redirects send 80% of traffic to the 100 popular links;
roughly 10% of requests represent bots.

Each active account requests its own first link's 30-day report once per
30 seconds, with arrivals evenly staggered. One report currently performs an
ownership lookup and three analytics queries, in addition to authentication work.
This models one report per refresh, not an entire browser dashboard or all five
links at once. Signup, email delivery, and link creation throughput are excluded.

Accounts use a fixed shuffled order. The million-click account is at zero-based
index 13,377: smaller stages exclude it, and the aborted 20,000-dashboard stage
never reached it. It is covered by preflight/postflight checks, query profiling,
and one explicitly scheduled request in the separate 90-day report burst.

The generator preallocates 250 redirect workers, 400 analytics workers, and 600
additional burst workers. These are reusable request workers, not browser-session
counts. Slow requests occupy workers longer and eventually cause missed arrivals.
Therefore achieved throughput during overload is **not a precise server capacity
ceiling**. See [k6's arrival-rate worker guidance](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/arrival-rate-vu-allocation/).

All 65 application Go/SQL/module files match the preceding pool/retry benchmark's
source manifest. This experiment adds the workload runner and evidence; it does
not change application behavior or resource limits. Full source hashes and image
identifiers are in [environment.json](environment.json).

## Measured results

All stages target 1,000 redirects/sec. Throughput below counts successful responses
over the actual measured duration, including request drain time. Latency p95 is in
milliseconds. A pass requires correct responses, no missed arrivals, redirect p95
below 100 ms, analytics p95 below 500 ms, the configured duration, and click-count
reconciliation. The generator can abort after 30 seconds when overall HTTP errors
reach 1% or redirect p99 reaches two seconds.

| Run | Active dashboards | Target analytics/sec | Successful analytics/sec | Successful redirects/sec | Measured seconds | Redirect p95 | Analytics p95 | HTTP errors | Missed arrivals | Result |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| Initial ramp | 1,000 | 33.33 | 33.33 | 999.80 | 60.01 | 4.62 | 28.01 | 0% | 0 | Pass |
| Ramp | 5,000 | 166.67 | 162.67 | 972.85 | 61.26 | 237.01 | 1,253.54 | 0.0488% | 400 | Fail |
| Ramp | 10,000 | 333.33 | 194.37 | 704.21 | 63.59 | 979.91 | 3,544.39 | 0.1625% | 22,767 | Fail |
| Ramp, aborted | 20,000 | 666.67 | 110.32 | 448.95 | 31.83 | 1,694.40 | 5,000.90 | 2.5186% | 33,963 | Fail |
| After-overload confirmation | 1,000 | 33.33 | 33.04 | 920.78 | 120.50 | 521.49 | 2,199.16 | 0.0174% | 9,043 | Fail |
| With report burst, aborted | 20,000 | 666.67 | 75.57 | 350.21 | 31.81 | 2,383.23 | 5,001.96 | 10.3625% | 38,834 | Fail |
| Separate fresh database | 1,000 | 33.33 | 33.34 | 999.86 | 120.02 | 3.09 | 23.62 | 0% | 0 | Pass |

The first four ramp stages and the burst were configured for 60 seconds each;
both confirmations were configured for 120 seconds. Main-run stages reuse the
same database and accumulate clicks, with three-second cooldowns after settling.
The separate fresh run recreates the complete initial dataset.

At the 20,000-dashboard target, only 4,367 distinct dashboard accounts were
attempted before abortion. There were 650 interrupted in-flight requests in that
stage, separate from the completed HTTP error metric. The burst interrupted
another 609. Unsuccessful attempt counts must not all be described as HTTP errors.

The burst schedules 2,000 extra 90-day reports over two seconds, starting ten
seconds into the target stage. Only 729 burst requests started and 279 succeeded;
burst p95 was 5,001.60 ms. The table's analytics throughput and latency refer to
the ordinary polling scenario, excluding these extra reports.

The fresh pass completed **120,001 redirects and 4,001 reports**, without HTTP
errors, missed arrivals, or interrupted requests. Redirect p99 was 6.32 ms and
overall analytics p99 was 40.23 ms. However, the 20 reports for popular links had
p95 **1,770.88 ms** and maximum **1,938.84 ms**. Those slow accounts are too small a
fraction to appear in the overall p95. This pass does not establish acceptable
latency for every account or a maximum supported dashboard population.

The failed confirmation after overload is retained, despite the later fresh
success. Accumulated database state, recovery behavior, and shared-host effects
were not isolated. A longer recovery/soak experiment is needed before claiming
the same performance after sustained overload.

## Bottleneck evidence

| Stage | Mean PostgreSQL CPU | Mean API CPU | Mean generator CPU |
| --- | ---: | ---: | ---: |
| Fresh 1,000 dashboards | 79.6% | 45.3% | 53.6% |
| 5,000 dashboards | 178.5% | 55.1% | 65.9% |
| 10,000 dashboards | 192.4% | 50.5% | 61.1% |
| 20,000 dashboards | 196.8% | 47.2% | 71.2% |
| After-overload 1,000 dashboards | 196.7% | 82.8% | 105.1% |

Docker reports 100% per CPU core; approximately 200% represents the database's
two-core quota. Short samples can exceed that figure. Database CPU saturation is
the clearest measured constraint, while query plans also show substantial page
access and temporary-file work. The test does not isolate CPU from storage/cache
effects. Sampled memory usage below container limits does not prove additional
memory could have no benefit.

After the fresh run, sequential `EXPLAIN (ANALYZE, BUFFERS)` probes captured the
existing analytics queries with no concurrent load:

| Link / query | Execution time | Relevant work |
| --- | ---: | --- |
| Million-click link: totals | 1,792.60 ms | Scanned 1,001,011 link rows, filtered out 666,694 older rows; external sort used 24,928 KiB |
| Million-click link: daily | 1,386.82 ms | Scanned the same full link history; external sort used 23,296 KiB |
| Million-click link: breakdown | 1,061.65 ms | Scanned the same full link history again |
| Popular link: totals | 1,252.71 ms | Examined 11,111 rows; 10,212 shared read blocks, about 79.8 MiB |
| Same popular link: daily / breakdown | 37.60 / 38.75 ms | Subsequent probes used warmed pages |
| Ordinary link: totals / daily / breakdown | 1.03 / 0.26 / 0.72 ms | Only 51 rows in its history |

The million-click queries each read roughly 253 MiB of PostgreSQL pages. Shared
read blocks may be served from the operating system cache; these figures are not
measurements of physical disk reads. The profiler runs direct SQL with constants,
so its plans/timings are diagnostic evidence rather than captured live API plans
or concurrent request latency.

Captured scans chose `idx_clicks_link_id`, filtering dates afterward. A composite
`(link_id, created_at)` index **already exists**; simply proposing that same index
would miss the issue. Query/access-path investigation and precomputed analytics
are concrete next experiments. Any daily rollup design must preserve the current
unique-visitor semantics as well as totals and breakdowns.

JIT generation took about 12–37 ms for the million-click queries. Repeating those
probes with JIT disabled in a diagnostic session still took roughly 1.0–1.5 seconds.
Disabling JIT alone does not remove the dominant scan and aggregation work. No API
or global PostgreSQL settings were changed by these probes.

## Correctness and limits

- The small-fixture smoke run completed both steady traffic and the burst.
- Cross-account analytics access returned 404. Eight representative 30/90-day
  report totals matched SQL before and after each full invocation, including the
  largest link. These checks cover human clicks, bots, and unique visitors; they
  do not exhaustively compare every breakdown or every account's report.
- Every non-burst stage's recorded human/bot clicks and human link-counter deltas
  matched acknowledged redirects. In the fresh pass, all 120,001 click rows were
  present: 107,996 human and 12,005 bot clicks.
- The aborted burst stored 11,149 clicks against 11,140 acknowledged redirects,
  including eight extra human clicks and one extra bot click. Recording a request
  before its client times out or is interrupted can explain this difference.
  Exact reconciliation failed; without per-request correlation this does not prove
  duplicate recording or account for each extra event.
- Missed arrivals are requests the generator did not start, not evidence of
  application-side click loss. Early-abort latency metrics also exclude requests
  interrupted before they report their duration.
- The final main database contained 3,300,781 clicks. Retained logs contained no
  PostgreSQL connection-exhaustion errors or click-recorder failure messages;
  API logs are bounded, so this is not complete-run log coverage.
- Database samples exclude their own monitoring connection. Other diagnostic
  connections can still appear. The `waiting` field includes all PostgreSQL wait
  events, including idle client waits; it is not a Go connection-pool wait metric.
- The application still uses an in-memory click queue. Count reconciliation here
  does not establish durable delivery through a process crash or full queue.

The previous 1,000-redirects/sec plus five-reads/sec benchmark used a much smaller
fixture and no seeded historical clicks. Its results should not be treated as a
capacity prediction for this workload. Likewise, multiplying these short-run
rates by 86,400 would only produce a hypothetical daily equivalent.

## Reproduce and inspect

From the repository root, with Docker Compose and Python 3:

```bash
python3 server/scripts/load/dashboards.py \
  --levels 1000,5000,10000,20000 --duration 60 \
  --confirm-duration 120 --burst-requests 2000 \
  --output /tmp/sh-dashboard-rerun

python3 server/scripts/load/dashboards.py \
  --levels 1000 --duration 120 --confirm-duration 0 \
  --burst-requests 0 --profile-queries \
  --output /tmp/sh-dashboard-fresh-rerun
```

Choose unused output directories. The runner builds the current application,
applies migrations, creates fixtures, runs checks, saves results, and removes its
isolated containers and volumes. Both measured invocations were cleaned up; the
development API and database remain running. No email or redirect-destination
traffic was sent.

Evidence: [results](results.json), [CSV](results.csv), [raw k6 summaries](raw/),
[resource summary](resources.json), [query plans](query-plans.json), and the
preflight/reconciliation JSON files beside this report. Raw resource samples and
database logs are also retained. Exact tested script snapshots are archived in
[tested-scripts](tested-scripts/); use the commands above to run the maintained
scripts, whose relative paths expect their original location. See the
[runner documentation](../../../scripts/load/README.md) for workload options.
