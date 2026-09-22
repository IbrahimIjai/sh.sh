# Pool and analytics retry benchmark — 2026-09-22

The updated server passed **1,000 redirects/second plus 5 authenticated metrics
reads/second for 120 seconds**, using the same CPU and memory quotas as the
[original benchmark](../report.md). This raises the highest verified two-minute
workload from 150 to 1,000 redirects/sec (6.67 times the previous test point),
without establishing an exact capacity ceiling or a uniform speedup.

- **120,000** correct redirects and **601** metrics reads.
- Redirect latency: **4.01 ms p95**, **12.20 ms p99**.
- Metrics latency: **21.85 ms p95**, **51.15 ms p99**.
- Zero HTTP errors, incorrect responses, or dropped k6 iterations.
- **120,000/120,000 clicks recorded**: 108,000 human and 12,000 bot clicks.
- Human link counters increased by exactly 108,000; three sampled
  links' human, unique-visitor, and bot totals matched PostgreSQL.

## Changes under test

The Go SQL pool is capped at 20 connections and retains up to 20 idle connections
for reuse. These are configurable through `DB_MAX_OPEN_CONNS` and
`DB_MAX_IDLE_CONNS`; idle/lifetime limits are 5/30 minutes. The same pool serves
auth, redirect lookups, and analytics. A pool limit is per API process and limits
database connections, not requests/second.

Failed analytics batches are retained and retried with exponential backoff and
jitter. A stable batch ID is inserted into `click_batches` atomically with its
click rows and human-counter updates. Replays do nothing after a successful
commit, including when the first acknowledgement was lost. Original click times
are retained across retries. Shutdown cancels pending attempts when its deadline
expires and reports failure instead of claiming that all events drained.

The queue remains in memory, bounded at 10,000 pending clicks plus the current
batch. Crashes, queue overflow, or shutdown expiry can still lose analytics.
Persistent data/schema errors stall the current batch until fixed or stopped.
Batch receipts are retained; there is no automatic receipt pruning.

## Same hardware and workload

API: **2-CPU quota / 512 MiB**. PostgreSQL 17: **2-CPU quota / 1 GiB**.
The load generator shares the Intel Core i7-8706G laptop (4 physical cores,
8 threads, approximately 16 GiB RAM) with both services and the desktop.
Quotas are not dedicated cores. No resource limits were increased.

k6 2.3.0 used constant arrival rates, 200 preallocated redirect VUs and 30 metrics
VUs. All stages include 5 authenticated metrics reads/sec. Each phase starts with
a fresh isolated database and 1,000 links, and accumulates clicks across its
stages. Requests use the same 90% browser / 10% bot mix, deterministic visitors,
UTM forwarding checks, and post-load analytics reconciliation as the baseline.
Redirect targets are never followed. Analytics and Gin request logging stay
enabled. No TLS, CDN, email, geolocation, or safety-service traffic is included.

Baseline commit: `3aab98181316caaf4bdb217a7ce00fb2e55c9632` plus the local source
changes identified in [source-sha256.json](source-sha256.json). Container limits,
image IDs, host details, and exact commands are in [environment.json](environment.json).

## Every measured stage

All runs completed their configured duration. HTTP errors cover redirects and
metrics reads. PASS requires all k6 thresholds and click reconciliation to pass.

| Phase | Offered redirects/sec | Seconds | Successful redirects/sec | p95 ms | p99 ms | HTTP errors | Unstarted iterations | Stored / successful | Result |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| ramp | 150 | 30.0 | 150.0 | 4.55 | 6.90 | 0.00% | 0 | 4,501/4,501 | PASS |
| ramp | 250 | 30.0 | 249.9 | 5.16 | 8.70 | 0.00% | 0 | 7,500/7,500 | PASS |
| ramp | 500 | 30.0 | 500.0 | 5.61 | 11.83 | 0.00% | 0 | 15,001/15,001 | PASS |
| ramp | 1,000 | 30.0 | 999.7 | 3.44 | 6.36 | 0.00% | 0 | 30,001/30,001 | PASS |
| high | 2,000 | 30.0 | 1,998.7 | 3.47 | 9.71 | 0.00% | 31 | 59,970/59,970 | FAIL |
| high | 3,200 | 30.0 | 3,198.7 | 11.42 | 25.03 | 0.00% | 0 | 96,001/96,001 | PASS |
| sustained | 3,200 | 120.0 | 3,184.4 | 17.52 | 51.16 | 0.00% | 1,852 | 382,145/382,145 | FAIL |
| confirmed | 1,000 | 120.0 | 1,000.0 | 4.01 | 12.20 | 0.00% | 0 | 120,000/120,000 | PASS |

The 3,200/sec workload passed for 30 seconds, but its two-minute run missed 1,852
scheduled k6 iterations. The 2,000/sec stage also missed 31. Neither is a clean
pass despite zero HTTP errors and complete analytics for the successful
redirects. These misses are load-generator iterations, not missing database
clicks. The shared host and transient latency/generator effects prevent assigning
them solely to a server capacity limit. The non-monotonic results are a reason to
keep the complete table and validate on separate load-generator infrastructure.

No failed analytics writes or connection-exhaustion messages appeared in the
retained API logs. Separate fault-injection integration tests, rather than this
healthy-database load run, verify recovery from failed or ambiguously committed
writes. Sampled CPU/memory peaks are in [resources.json](resources.json); they are
samples, not continuous maxima. Full results are in [results.json](results.json),
[CSV](results.csv), and [raw k6 summaries](raw/).

## Validation and reproduction

Passed `go test -race -count=1 ./...`, plus real PostgreSQL integration tests with
`TEST_DATABASE_URL` for pool saturation/cancellation, lost acknowledgements,
statement rollback, concurrent replay, original click timestamps, retry recovery,
and shutdown cancellation. Integration tests create and remove an isolated schema.
The PostgreSQL test container and all benchmark containers/volumes were removed;
the normal development API/database were not changed.

From the repository root:

```bash
python3 server/scripts/load/run.py \
  --rates 1000 --duration 120 --stats-rate 5 \
  --output /tmp/sh-load-pool-repeat
```

Before running the updated development API, apply migration
`00006_click_batch_retries.sql` with `goose up` from `server/`, then rebuild the API.

This is a local synthetic test, not actual customer traffic, production capacity,
or a 24-hour endurance test. The two changes were tested together, so these runs
do not isolate how much each change contributed.
