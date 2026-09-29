# Article-scale proof

`proof.py` runs the article's traffic (34,722 redirects/s, 3,472 creates/s, plus reports) against
the real stack spread over separate machines: API instances, one PostgreSQL, one Redis, and k6
generators. The laptop harness in `../load` can't do this, because k6 needs about 2.5 times the
API's CPU per request and shares the machine with everything else.

```
gen1, gen2 (k6) ──HTTP──▶ api1..apiN (Go API: L1 cache, click stream producer + consumers)
                                  │                    │
                                  ▼                    ▼
                          data1: PostgreSQL 17    Redis 7.2 (L2 cache, invalidation, click stream)
```

## What a run checks

| Article requirement | How the run shows it |
|---|---|
| 3B redirects/day (34,722/s) | `steady`: one hour at 34,722/s with creates and reports running |
| 300M creates/day (3,472/s) | Same stage; every acknowledged create must exist once in PostgreSQL |
| Spiky traffic | `burst`: 10 minutes at 3× |
| Unique short codes | Created rows vs distinct slugs, per stage |
| Analytics | Every acknowledged redirect's click stored exactly once (event IDs), per stage |
| Availability over consistency | `faults`: Redis stopped 60 s, PostgreSQL restarted, one API instance killed for 60 s; redirects per 10-second window |
| New links readable at once | 1 in 100 creates is read back from a *different* instance |
| Takedown | Seconds until a taken-down link stops resolving on every instance |

A stage passes when delivered rates are ≥ 99.9% of offered, ≤ 0.1% of iterations went unsent,
every response was correct, redirect p99 < 50 ms, report p95 < 500 ms, and clicks and creates
reconcile. Fault stages need ≥ 99% of redirects answered correctly and report click loss instead.
**A stage where a generator averaged over 85% of its CPU fails as generator-bound**: its numbers
show the generator's limit, not the server's.

## Rehearsal on one machine

```bash
python3 server/scripts/proof/proof.py --inventory server/scripts/proof/local.json \
  --profile rehearsal --output docs/benchmark-results/$(date +%F)/proof-rehearsal
```

Two API instances, PostgreSQL, Redis and k6 on this machine at small rates (ports 15432, 16379,
18080–18081, 19090–19091). It checks the harness, the fault injection and the reconciliation,
not capacity.

## The real run

1. **Rent machines** with dedicated vCPUs, all in one region with a private network. Rough sizing
   from the laptop runs (about 8,000 cached redirects/s per API core; PostgreSQL ~0.85 core per
   10,000 clicks/s; creates are PostgreSQL-bound):

   | Role | Steady (1×) | With the 3× burst |
   |---|---|---|
   | API | 3 × 8 vCPU | same |
   | PostgreSQL + Redis | 1 × 16 vCPU, 64 GB, ≥ 200 GB NVMe | 1 × 32 vCPU, 128 GB |
   | k6 | 2 × 16 vCPU | 4 × 16 vCPU |

   These are estimates to be confirmed by the run, not results. Hourly billing makes a 2–3 hour
   run affordable; check current prices.
2. **Firewall:** allow SSH from your machine only. The API ports (18080+) listen on every
   interface of the API hosts, so block them publicly; PostgreSQL, Redis and metrics bind to
   the private address.
3. **Install Docker** on every host, and make sure `ssh root@<host> docker ps` works without a prompt.
4. **Inventory:** copy `inventory.example.json`, fill in public (`ssh`) and private addresses.
   A host can hold several roles; `api_instances` runs more than one API on a host.
5. **Check the plan**, then run the short profile before the full one:

   ```bash
   python3 server/scripts/proof/proof.py --inventory my-inventory.json --profile article --dry-run
   python3 server/scripts/proof/proof.py --inventory my-inventory.json --profile article-short --output docs/benchmark-results/$(date +%F)/proof-short
   python3 server/scripts/proof/proof.py --inventory my-inventory.json --profile article --output docs/benchmark-results/$(date +%F)/proof
   ```

   `article` takes about 1 h 40 min of traffic plus seeding (10M links) and reconciliation.
6. **Destroy the machines** afterwards. Everything the run created on them is already removed
   unless you passed `--keep`; `--teardown` removes leftovers.

## What it does

- Builds the API image from a snapshot of `server/` (uncommitted changes included, hashes
  recorded) and ships it with `docker save | ssh docker load`.
- Starts PostgreSQL (inventory `postgres_args`) and Redis (AOF every second, `volatile-lru`) with
  host networking and random passwords, applies the migrations, and seeds links `p-1…p-N`,
  one session per seeded user, and a link for the takedown probe.
- Starts the API instances with `REQUEST_LOG=errors`, `METRICS_ADDR`, a PostgreSQL pool split
  between instances, and any `api_env` overrides (`--click-queue memory` compares the old path).
- Per stage: k6 on every generator with its share of the rates; link popularity is Zipf (s = 1);
  1 in 10 redirects is a bot. Faults run on schedule. Afterwards it waits for the click queues
  and stream to drain before the next stage.
- Every 5 s it samples `docker stats` and each API's `/metrics` on every host.
- At the end, one pass over `clicks` and `links` reconciles every stage.

## Output

| File | Contents |
|---|---|
| `results.md` | Topology, a table per stage, fault timelines, takedown |
| `results.json` | Everything, including per-stage API counters, response codes and CPU per container |
| `k6/<stage>-<generator>.json` | Raw k6 summaries |
| `samples/<host>.jsonl` | 5-second resource and metric samples |
| `logs/` | API (errors only), PostgreSQL and Redis logs |
| `source/`, `build.log` | The exact code that ran |

No passwords or tokens are written to the output.

## Limits

- No TLS, CDN or load balancer: generators talk to instances directly and retry an unreachable
  one elsewhere, as a load balancer would. Production latency adds Traefik and TLS.
- Latency percentiles are the worst generator's, not merged.
- One region, one PostgreSQL, one Redis: the run measures this design's ceiling, not a
  multi-region system. The 20-year storage requirement is out of scope.
