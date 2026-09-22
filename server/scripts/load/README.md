# Local load benchmark

Uses Grafana k6 2.3.0, an isolated API container, and a disposable PostgreSQL 17
database. Requires Docker Compose and Python 3. Run from the repository root:

```bash
python3 server/scripts/load/run.py \
  --rates 100,500,1000,2000,3200 \
  --duration 30 \
  --output /tmp/sh-load-ramp
```

Docker context `default` is used explicitly. The existing development stack and
its database are not changed. The benchmark builds the current server code,
applies its migrations, and seeds a verified fixture account, session, and 1,000
links. No email is sent. Test containers and their volumes are removed when the
runner finishes; result files remain in the output directory.

For a longer run with simultaneous authenticated analytics reads:

```bash
python3 server/scripts/load/run.py \
  --rates 1000 --duration 120 --stats-rate 5 \
  --output /tmp/sh-load-sustained
```

Choose a rate that the initial ramp supports. Each rate is a separate fixed-rate
run, with 200 preallocated virtual users by default (`--vus` changes this). The
API has a two-CPU quota and 512 MiB memory limit; PostgreSQL has a two-CPU quota
and 1 GiB memory limit. The generator shares the host with both containers.

## What is measured

- One GET per redirect iteration, spread evenly over `--links` seeded links.
- HTTP 302 and the complete Location value, including forwarded campaign tags.
- Redirect latency: mean, median, p95, p99, and maximum.
- 90% browser-like requests and 10% curl-like bots, with analytics enabled.
- Successful redirects reconciled against stored click rows and human counters.
- Authenticated stats for three sample links reconciled against database totals.
- Optional stats-read latency under concurrent redirect load (`--stats-rate`).
- API, database, and generator CPU/memory samples in `resources.jsonl`.

Redirects are **never followed**, so target websites receive no benchmark load.
Setup fixtures and post-run reconciliation requests are outside the measured
load. Human and bot requests are deterministically distributed; this is synthetic
traffic, not a model of real users. A single hot-link case can be tested with
`--links 1`.

Each stage requires zero incorrect redirects, zero unscheduled iterations,
p95 redirect latency below 100 ms, and HTTP errors below 1%. A p99 above 2 seconds
or error rate above 1% can abort a stage after 15 seconds. Optional stats reads
require p95 below 500 ms and correct responses. Raw k6 thresholds and exit codes
are saved even when a stage fails. The runner records failures and continues to
the next requested rate so the report includes overload behavior.

`redirect_rps` is successful redirects divided by k6's actual elapsed stage
duration, including time to finish in-flight iterations. `completed_duration`
distinguishes completed stages from early aborts. Do not describe an aborted
stage as sustained load. `dropped_iterations` measures work k6
could not start, not application-side lost clicks. Either application slowdown
or generator/VU limitations can cause these drops.

## Results and interpretation

- `results.json`: rates, latency, pass/fail, click reconciliation, stats checks.
- `run-*.json`: complete k6 summaries, including HTTP errors and thresholds.
- `run-*.log`: generator output and warnings.
- `metadata.json`: commit, hardware, workload, and benchmark-container details.
- `resources.jsonl`: sampled resource usage; `api.log`: bounded server logs.
- `database.json`: final database size and click-row count.

The database grows across stages within one invocation; each invocation starts
with a fresh database. Warm caches, request logging, Docker networking, shared
hardware, the selected key distribution, and the small link dataset affect the
results. External geolocation and safety integrations are disabled, matching
the optional nature of those settings.

Report the tested rate, duration, latency percentiles, errors, and analytics
retention with the hardware and workload. Multiplying RPS by 86,400 is only a
daily equivalent, not evidence of an actual day-long run or real customer traffic.

Reference: [k6 constant-arrival-rate](https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/constant-arrival-rate/).

## Independent dashboards with historical analytics

`dashboards.py` models independent authenticated dashboards polling every 30 seconds,
alongside 1,000 redirects/sec. It uses the same Compose resource limits as the redirect
benchmark. This is a hypothetical workload, not a measurement of Bitly's users.

```bash
python3 server/scripts/load/dashboards.py \
  --levels 1000,5000,10000,20000 --duration 60 \
  --confirm-duration 120 --burst-requests 2000 \
  --output /tmp/sh-dashboard-load
```

Defaults seed 20,000 verified accounts and sessions, 100,000 owned links (five per
account), and three million historical click rows across 90 UTC days. One link
gets a third of the historical clicks, the next 99 popular links share another
third, and the remaining third is spread across accounts' first links. Visitor
hashes, sources, devices, countries, and bots are synthetic. The database is
vacuumed/analyzed after seeding. Fixtures bypass signup/email and link creation;
those endpoints' throughput is not measured.

Each dashboard requests its own first link's 30-day analytics. A fixed shuffled
account order spreads popular accounts throughout the test. At 20,000 dashboards,
one request every 30 seconds means **666.67 analytics API requests/sec**. Smaller
stages model fewer active dashboards at the same refresh interval. Traffic is
evenly staggered, rather than 20,000 browsers loading at exactly the same instant.
The optional final burst adds 2,000 90-day report requests over two seconds,
starting ten seconds into the largest configured dashboard workload. It models
extra dashboard openings/filter changes by existing accounts.
The burst explicitly includes one 90-day request for the largest link.

Redirect traffic sends 80% of clicks to the 100 popular links and 20% across all
links, with approximately 10% bots independently distributed. Redirect destinations
are not followed. The load generator reuses a shared fixture array and defaults
to 250 redirect VUs, 400 analytics VUs, and 600 extra burst VUs. These are generator
workers, not numbers of browser sessions. They provide headroom around the latency
targets; very slow responses can exhaust them, which is reported as dropped
iterations. The generator shares the host, so dropped iterations alone cannot
establish a server capacity ceiling.

The runner checks cross-account access, verifies representative large/popular/
ordinary links against PostgreSQL for 30/90-day windows before and after load,
and reconciles each stage's newly recorded human/bot clicks and link counters.
Historical clicks are excluded from stage reconciliation using campaign tags.
Every stage requires correct responses, zero missed iterations, redirect p95
under 100 ms, and analytics p95 under 500 ms. It aborts after 30 seconds if overall
HTTP errors exceed 1% or redirect p99 exceeds two seconds. It records failing
stages and still tests the requested higher levels. The highest passing ramp
level gets a longer confirmation; a confirmation failure remains visible.

Results include separate steady/burst analytics throughput, latency by link
popularity, missed iterations by scenario, sampled API/DB/generator resources,
and database connection counts (excluding the diagnostic connection). Metadata
records source hashes, exact container limits, images, fixture settings, and
host hardware. `dashboards.json` contains only public synthetic fixture tokens.
All temporary containers and volumes are removed, leaving the development stack
untouched. Useful options: `--users`, `--links-per-user`, `--history`,
`--refresh-seconds`, `--stats-vus`, `--redirect-vus`, `--burst-vus`.

Use `--profile-queries` to save `EXPLAIN ANALYZE` plans for the three analytics
queries on large, popular, and ordinary links after all load traffic stops.
Where a plan uses PostgreSQL JIT, the profiler also repeats it with JIT disabled
in that diagnostic connection only. These timings are individual query probes,
not another throughput benchmark; the API's database settings stay the same.

Smoke test the complete workflow with smaller fixtures:

```bash
python3 server/scripts/load/dashboards.py \
  --users 50 --history 3000 --levels 50 --redirect-rate 20 \
  --duration 12 --confirm-duration 0 --burst-requests 10 \
  --stats-vus 10 --redirect-vus 10 --burst-vus 10 \
  --output /tmp/sh-dashboard-smoke
```
