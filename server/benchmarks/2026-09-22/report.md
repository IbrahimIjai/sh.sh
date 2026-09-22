# shortened.cv local load benchmark — 2026-09-22

The highest workload verified without errors or missing analytics was **150
redirects/second plus 5 authenticated metrics reads/second for 120 seconds**.
This is a verified test point, not an exact capacity ceiling.

- **18,001** correct redirects and **601** successful metrics queries.
- Redirect latency: **4.29 ms p95**, **8.41 ms p99**,
  2.55 ms mean, 145.45 ms maximum.
- Metrics-read latency: **23.20 ms p95**,
  51.22 ms p99.
- **Zero HTTP errors**, incorrect redirects, or k6 dropped iterations.
- **18,001/18,001 click rows persisted**: 16,200 human and 1,801 bot clicks.
  The human link counters also increased by exactly 16,200.
- Three sampled links' authenticated stats matched database human clicks,
  distinct visitors, and bot clicks after each stage.

## Setup and scope

Application commit: `3aab98181316caaf4bdb217a7ce00fb2e55c9632`. Application code was not tuned or changed.
Benchmark tooling was added locally. Tool: **Grafana k6 2.3.0** with its
constant-arrival-rate executor and 200 preallocated redirect VUs (plus 30 VUs
when metrics reads were enabled). VUs are generator capacity, not a claim about
concurrent real users.

One Intel Core i7-8706G laptop, 4 physical cores / 8 threads, approximately 16 GiB
RAM. API: **2-CPU quota / 512 MiB**. PostgreSQL 17: **2-CPU quota / 1 GiB**.
The load generator, existing development services, and desktop shared the same
host. CPU quotas are not dedicated cores. This measured the default development
server with Gin request logging and analytics enabled, without TLS, a CDN,
external geolocation, or URL-safety service calls.

Each phase used a fresh isolated database seeded with 1,000 owned short links.
Traffic was distributed uniformly across those keys, with 90% synthetic browser
user agents and 10% bot user agents. Identity and key distributions were
synthetic/deterministic, not representative of a large real-user population.
Metrics reads used a seeded fixture session; signup/email and link creation
throughput were not measured. Redirect destinations were never contacted.

The database accumulated click rows across stages within each phase. The
sustained phase started fresh. This is a small, warm-cache dataset, not a test
of billions of links. Percentiles are k6 HTTP request duration, excluding
connection/DNS/TLS setup. Setup and post-load reconciliation are outside the
load window. Full data is in [results.json](results.json), [CSV](results.csv),
[environment.json](environment.json), and the [raw summaries](raw/).

## All measured stages

Targets are redirects/sec + metrics reads/sec. HTTP error percentage covers
both request types. Failed stress stages aborted after approximately 16 seconds;
they did not complete their configured 30-second duration. Successful RPS uses
the actual elapsed k6 stage time. Lost generator iterations are separate from
application-side lost click records.

| Phase | Offered RPS | Actual time | Successful redirect RPS | p95 ms | p99 ms | HTTP errors | Unstarted iterations | Stored / successful redirects | Result |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| ramp | 100 + 0 | 30.0s | 100.0 | 3.61 | 5.30 | 0.00% | 0 | 3,001 / 3,001 | PASS |
| ramp | 500 + 0 | 16.0s | 228.8 | 2474.01 | 2984.84 | 16.34% | 3,427 | 3,336 / 3,655 | FAIL |
| ramp | 1,000 + 0 | 16.0s | 294.6 | 1776.99 | 2301.68 | 13.00% | 10,345 | 3,947 / 4,703 | FAIL |
| ramp | 2,000 + 0 | 15.9s | 267.8 | 3050.11 | 4310.81 | 10.86% | 26,883 | 3,999 / 4,270 | FAIL |
| ramp | 3,200 + 0 | 16.0s | 205.9 | 3312.62 | 3880.71 | 7.92% | 47,287 | 3,293 / 3,288 | FAIL |
| refine | 150 + 5 | 30.0s | 150.0 | 4.95 | 15.77 | 0.00% | 0 | 4,501 / 4,501 | PASS |
| refine | 250 + 5 | 16.0s | 160.5 | 2972.47 | 3362.70 | 10.19% | 1,004 | 2,561 / 2,561 | FAIL |
| refine | 350 + 5 | 16.0s | 204.4 | 2274.93 | 3353.72 | 19.52% | 1,339 | 2,136 / 3,264 | FAIL |
| sustained | 150 + 5 | 120.0s | 150.0 | 4.29 | 8.41 | 0.00% | 0 | 18,001 / 18,001 | PASS |

At 3,200 offered RPS, stored clicks slightly exceeded client-observed successful
redirects: requests can time out or be interrupted after the server records the
click. This is not proof of duplicate analytics. At other overload points,
acknowledged redirects exceeded stored rows and server logs explicitly recorded
failed write batches.

## Bottleneck and next changes

The observed database error was `pq: sorry, too many clients already (53300)`.
Failed analytics batches contained hundreds of clicks. See
[server-errors.log](server-errors.log). During the ramp, PostgreSQL reached
its CPU quota and a sampled 97 processes, while the API stayed below its CPU
quota. Sampled resource peaks are recorded in [resources.json](resources.json).

`internal/database/database.go` opens the SQL pool without setting connection
limits or idle-pool size. `internal/analytics/recorder.go` logs failed writes but
does not retry or retain the failed batch. Database connection exhaustion and
lost analytics therefore need attention before claiming higher reliable load.

The first follow-up is to configure a bounded, reusable connection pool with
headroom under PostgreSQL's connection limit. Then make failed analytics batches
retry safely or persist through a durable queue, and repeat this same benchmark.
Those changes are recommendations, not part of this measured result.

## Bitly and TinyURL context

These are company-published aggregate figures, not independently measured peak
RPS or maximum server capacity. The daily/second values below are arithmetic
averages using 365 days/year and an assumed 30-day month.

| Company / metric | Published figure | Approximate daily baseline | Approximate per-second baseline |
|---|---|---:|---:|
| Bitly clicks and QR scans | More than 100 billion/year | 274 million | 3,171 |
| Bitly links and QR codes created | More than 2 billion/year | 5.48 million | 63 |
| TinyURL API calls | Over 2 billion last month | 66.7 million | 772 |

Bitly source: [How Bitly Keeps Every Click and Scan Safe](https://bitly.com/blog/trust-safety-at-bitly/),
August 11, 2026. TinyURL's official site separately states billions of redirects
per month but provides no precise redirect count in the checked page:
[TinyURL public counters](https://tinyurl.com/preview/1). API calls, link creation,
and redirects are different workloads. Neither checked source supplies peak RPS.

150 redirects/sec has a mathematical daily equivalent of 12.96 million, but
this run lasted two minutes. It does not establish 24-hour endurance, production
capacity, or actual customer traffic. Larger production comparisons require
separate load generators, deployment-representative infrastructure, larger data,
realistic traffic distributions, and longer tests.

## Repeat the benchmark

From the repository root:

```bash
python3 server/scripts/load/run.py \
  --rates 150 --duration 120 --stats-rate 5 \
  --output /tmp/sh-load-repeat
```

For all options, see [the load-test README](../../scripts/load/README.md).
All test containers and volumes were removed after each run. The normal local
API and database were not used for benchmark data.

## X draft

> Load-tested shortened.cv locally: 150 redirects/sec + 5 analytics reads/sec for 2 minutes. p95 4.29ms, p99 8.41ms, zero HTTP errors, all 18,001 clicks recorded. Go + PostgreSQL, each capped at 2 CPUs, sharing one laptop. Synthetic benchmark; next up: database pool tuning.
