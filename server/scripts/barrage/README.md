# Barrage tests

[Barrage](https://github.com/codetesla51/barrage) drives HTTP and database load on one clock and
reports which layer caused a slowdown. These scripts point it at shortened.cv, then check that
every click was recorded. Published throughput numbers still come from `../load/run.py` (k6),
which also verifies each redirect's exact destination.

Run from the repository root. Needs Go, Python 3, and the dev database (`docker compose up -d db`
in `server/`). Barrage v0.7.0 and Toxiproxy v2.12.0 are fetched into `~/.cache/shortened-bench`
on first use: Barrage is built from source, and Toxiproxy's checksum is verified.

```bash
# HTTP load on a short link + a read-only Postgres probe, against the running API (default :8080)
python3 server/scripts/barrage/barrage.py run --output /tmp/barrage-run

# the same through a fresh API on :18080 whose Postgres connection runs through Toxiproxy:
# 500 ms slower from 8 s to 16 s, completely down from 22 s to 28 s
python3 server/scripts/barrage/barrage.py chaos --output /tmp/barrage-chaos
```

Useful options: `--rate`, `--duration`, `--concurrency`, `--db-rate`, and for chaos
`--slow-at/--slow-for/--slow-ms/--down-at/--down-for`. See `--help`.

## What each run does

1. Starts a local sink on `:9999`. Barrage follows redirects, so test links point there instead
   of real websites.
2. Creates one anonymous test link (limit: 20 per hour per IP) and warms it into the cache.
3. Writes `config.yaml` and runs Barrage. Sleep is inhibited while it runs.
4. Reconciles clicks through `POST /api/v1/links/lookup`: recorded clicks must equal the
   redirects Barrage completed plus the warm-up. Exits 1 on a mismatch.
5. Stops everything it started.

Output: `report.html` (Barrage's report), `results.json`, `summary.json`, `barrage.log`, and for
chaos runs `api.log` (look for `click recorder:` lines).

In chaos mode, keep `--duration` under about 55 s: the link's cache entry lasts 60 s, and once it
expires during an outage, redirects for it fail.
