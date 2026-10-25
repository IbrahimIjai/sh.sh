# Building shortened.cv with Go, Redis and PostgreSQL

A short link has a small job: get someone to the right destination. The service behind it has
more to keep track of. It needs to allocate a unique address, resolve it quickly, respect deletions,
record visits, and make those visits useful to the person who shared the link.

I built shortened.cv to work through those problems in code. The backend is a Go service with
Gin, PostgreSQL, an in-process cache, and Redis. A Nuxt frontend handles link creation, sign-in,
and recent-link history.

In a local mixed workload, the service sustained **3,200 redirects per second for 60 seconds**,
alongside 100 link creations and 10 reports per second. Redirect p99 was **7.9 ms**, with no
HTTP errors or missed iterations. After the click workers caught up, all **192,000 redirect
events** were stored, including bot visits.

Those numbers came from containers on one laptop. The more useful part of the project is how
the pieces fit together, what the failure tests exposed, and where the measured limits are.

## The requirements I started with

[Andreyka26's URL-shortener system design](https://andreyka26.com/url-shortener-bitly-system-design-with-microsoft-engineer)
helped frame the project. Its exercise uses 300 million new links and 3 billion redirects per day:
roughly 3,472 creations and 34,722 redirects per second on average.

I used those figures as a workload to test against. My starting deployment plan is one API,
Redis, and PostgreSQL on an existing Coolify VPS shared with two other projects. The measurements
later in this article show how far that smaller setup gets in local tests.

The application has three main responsibilities:

1. **Create a short link** for a valid destination.
2. **Redirect visitors** while enforcing the link's active state.
3. **Report activity** through totals, daily counts, and traffic breakdowns.

I also added anonymous creation, account claiming, custom slugs, previews, and administrative
takedowns. The backend checks stored expiry timestamps, although the current creation endpoint
doesn't accept an expiry date.

For performance, I set a redirect target of p99 below 50 ms and a reporting target of p95 below
500 ms during redirect traffic. Analytics can arrive asynchronously. A shared URL must keep its
identity: deleting a link reserves its slug so an old address cannot later send visitors to a
different owner's destination.

## One service, two kinds of work

Redirects need a quick response. Recording and summarizing clicks can happen in batches.
That distinction drives the architecture.

| Layer | Choice | Responsibility |
|---|---|---|
| Web interface | Nuxt 4, Vue, TanStack Query | Create links, sign in, and manage recent links |
| HTTP API | Go and Gin | Parse requests and return responses |
| Services | Go | Apply ownership, creation, redirect, and reporting rules |
| Query layer | sqlc | Generate typed Go methods from SQL |
| Local cache | LRU with TTL | Resolve frequently visited links in memory |
| Shared state | Redis | Cache destinations, distribute invalidations, and queue clicks |
| Persistent storage | PostgreSQL 17 | Store links, click events, counters, and rollups |

The HTTP handlers call services; services use the generated queries. The server wires up those
dependencies and starts the click consumers and maintenance jobs.

```text
Visitor
   |
   v
Go API ----> Local cache ----> Redis cache ----> PostgreSQL
   |             Lookup continues on a cache miss
   |
   +----> Click buffer ----> Redis stream
                                |
                                v
                          Click consumers
                                |
                                v
                           PostgreSQL
                                |
                                v
                        Daily rollups / reports
```

All of this runs in one API process for the starting topology. I can follow a request through
one codebase, and the database transaction remains the boundary for related stored changes.

Concurrency still needs a budget. The connection pool defaults to 20 open connections per API.
Cache misses can run at most half that many database loads at once, and reporting concurrency
is capped at a quarter of the pool. Creation, auth, and click workers share the remaining pool
capacity and compete with those queries. These limits reduce the chance that one kind of work
occupies every connection.

## Generating short codes without a separate ID service

Generated slugs use seven Base62 characters: lowercase letters, uppercase letters, and digits.

```text
62^7 = 3,521,614,606,208 possible slugs
```

I generate candidates with `crypto/rand`. The random bytes use rejection sampling before being
mapped to the alphabet: 256 is not divisible by 62, so accepting every byte with `b % 62` would
favor the first eight characters.

Randomness gives me a candidate. PostgreSQL provides the uniqueness guarantee. The insert is
equivalent to this simplified query:

```sql
INSERT INTO links (user_id, slug, target_url)
VALUES ($1, $2, $3)
ON CONFLICT (slug) DO NOTHING
RETURNING *;
```

If a generated candidate conflicts, the service tries again, up to five attempts. A conflicting
custom slug returns `409 Conflict`. The unique index includes soft-deleted rows.

At one billion occupied seven-character slugs, a new random candidate has about a one-in-3,500
chance of hitting an occupied value. That makes retries inexpensive at that occupancy. As the
namespace fills, the collision rate grows; longer slugs would give the generator more room.

The implementation lives in [the slug service](server/internal/service/slug.go) and
[the link queries](server/db/queries/links.sql).

### Anonymous links and ownership

Someone can create a link before signing in. The response includes a 256-bit random claim token;
the database stores its SHA-256 hash. The frontend keeps the token in local storage and uses it
to retrieve recent anonymous links.

After sign-in, a claim request assigns those links to the account and clears the stored token
hashes. The link IDs stay the same, so their existing analytics stays attached.

This gives the first-time user a short path from pasting a URL to sharing it. The claim token
carries the authority to retrieve and claim the anonymous link until it has an owner.

## Following a redirect

The redirect path starts with the local cache, then checks Redis, then PostgreSQL.

```text
GET /{slug}
    |
    v
Local cache ---- hit ----+
    |                   |
   miss                 |
    v                   |
Redis cache ---- hit ----+
    |                   |
 miss / error           |
    v                   |
PostgreSQL -------------+
                        |
                        v
                   Check expiry
                        |
                        v
                 Collect and queue click
                        |
                        v
                   302 redirect
```

The local cache defaults to **100,000 entries with a 60-second TTL**. Redis holds destinations
for five minutes. A popular link can therefore be resolved repeatedly inside the Go process,
while Redis reduces database reads when a local entry is missing or the API has restarted.

Concurrent misses for the same slug share one lookup. This matters when many visitors arrive
before the first request has populated the cache. The separate database-load limit bounds work
across different missing slugs.

A Redis error falls back to PostgreSQL. After a failed Redis operation, request paths skip Redis
for a second, so later requests can reach the fallback without each paying another timeout.

Once the destination is resolved, the service checks its stored expiry even on a cache hit.
The handler then collects a click event, queues it, and returns `302`. I chose that response
because a permanently cached redirect could bypass the service on later visits and leave those
visits uncounted.

Appending `+` opens a preview, such as `/{slug}+`, without recording a click. UTM parameters on
the short URL are recorded and forwarded to the destination, preserving any values the
destination already sets.

The key request paths are:

| Endpoint | Purpose |
|---|---|
| `POST /api/v1/links` | Create a link |
| `POST /api/v1/links/lookup` | Retrieve anonymous links using claim tokens |
| `POST /api/v1/links/claim` | Assign anonymous links to the signed-in account |
| `GET /api/v1/links` | List owned links |
| `GET /api/v1/links/:id/stats?days=30` | Read owned-link analytics |
| `DELETE /api/v1/links/:id` | Soft-delete an owned link |
| `GET /{slug}` | Redirect |
| `GET /{slug}+` | Preview |

## Recording clicks while the visitor moves on

A cache makes destination lookup cheap. Analytics still produces a write for every redirect,
so its delivery path needs its own design.

The collector extracts device, browser, operating system, referrer host, source, UTM tags, and
available country or city data. It classifies bots separately from human visitors. Click rows
store parsed fields and a keyed visitor hash derived from IP, user agent, and the UTC day.
They don't store raw IP addresses or full referrer URLs. The daily hash also means the same
visitor is not assigned a persistent identifier across days.

With Redis configured, events enter a bounded memory buffer and are appended to a Redis stream
every 20 ms or when a batch fills. The defaults are batches of up to 1,000 events and two
consumers per API.

```text
Redirect handler
    |
    v
Memory buffer -> Redis stream -> Consumer -> PostgreSQL commit
                                                |
                                                v
                                  Acknowledge and delete stream entry
```

A failed database write retries with backoff. Another consumer can reclaim entries left pending
by a crashed API. Each event keeps its original timestamp and ID through retries.

The database's unique key is `(created_at, event_id)`. Inserting events and increasing human-click
counters happens in one SQL statement, and only newly inserted events contribute to the counter.
That handles the awkward case where a write succeeds but its acknowledgement is lost: replaying
the batch skips the events already stored.

### Where a click can still be lost

There are three persistence boundaries to account for.

**The API buffer.** Events begin in RAM. A process crash can lose events that haven't reached
Redis, and a full buffer drops new events while allowing redirects to continue. The 20 ms flush
schedule is not a maximum loss window; backlog or a slow append can keep events in memory longer.

**Redis persistence.** The benchmark uses AOF with `appendfsync everysec`. Redis can lose roughly
the latest second of writes in a crash before they have been flushed to disk. That trade-off is
described in the [Redis persistence documentation](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/).

**PostgreSQL commit mode.** By default, `CLICK_ASYNC_COMMIT=true` sets
`synchronous_commit=off` for click batches. Consumers remove stream entries after PostgreSQL
acknowledges the commit, which can happen before the write-ahead log reaches durable storage.
A database crash in that interval can lose acknowledged click data. Setting
`CLICK_ASYNC_COMMIT=false` removes this particular loss window at a disk-flush cost; it still
leaves the earlier memory and Redis boundaries. PostgreSQL explains the behavior in its
[asynchronous commit documentation](https://www.postgresql.org/docs/17/wal-async-commit.html).

If Redis is down or the stream exceeds its configured memory budget, new events use the
in-memory recorder to write directly to PostgreSQL. That fallback keeps redirects available,
but events waiting there depend on the API process surviving.

These are deliberate trade-offs for click analytics. Link and account writes keep synchronous
commit behavior.

## Reporting without rescanning every visit

The dashboard API returns human clicks, bot clicks, unique visitors, a daily series, and
breakdowns by source, referrer, location, device, browser, operating system, and UTM fields.
It checks ownership before returning a link's report.

For storage, PostgreSQL keeps raw clicks in daily UTC partitions, human-click counters per link,
and daily aggregates for totals and dimensions. Reports combine rolled-up days with raw data
for days that haven't been summarized. Maintenance runs at startup and every 15 minutes by
default, using a database advisory lock so only one instance performs a pass at a time.

Raw retention defaults to 30 days. Once an old partition's day has been rolled up, maintenance
can drop the partition while keeping its aggregates.

Late events exposed an edge case. A click recovered after an outage can belong to a day that
has already been summarized. A database trigger now marks that rollup stale, and maintenance
recomputes it. Retention also handles old rows in the default partition.

In a separate test seeded with **three million clicks**, the service handled about **167 reports
per second alongside 1,000 redirects per second for 60 seconds**. Report p95 was **16.5 ms** and
redirect p99 was **2.9 ms**. During that confirmation stage, reports for the largest link,
with **1,000,200 clicks**, took at most **122 ms**.

Those reports benefited from the stored aggregates. The result measures the implemented reporting
path, including its data layout. The [reporting results](k6-benchmarks/2026-09-29-local/reporting-results.json)
and [seed details](k6-benchmarks/2026-09-29-local/reporting-seed.json) preserve the evidence.

## What the load tests measured

The throughput tests ran on September 29, 2026, on an Intel Core i7-8706G laptop with four cores,
eight threads, and about 16 GB RAM. The API, PostgreSQL, Redis, and k6 all ran on that machine.

| Container | CPU limit | Memory limit |
|---|---:|---:|
| Go API | 2 CPUs | 512 MiB |
| PostgreSQL | 2 CPUs | 1 GiB |
| Redis | 0.5 CPU | 256 MiB |

The main redirect fixture started with 1,000 links, which fit comfortably inside the local cache.
Traffic included 10% bot user agents. Creation requests were authenticated and each was followed
by a preview read to verify the new link was immediately visible.

Requests used direct HTTP over Docker networking, with access logging enabled. The runs included
Redis AOF and the default asynchronous click commits described above. They excluded Traefik, TLS,
remote-network latency, outbound email, and Web Risk calls.

k6 scheduled redirects, creations, and reports independently at fixed arrival rates. I checked
response correctness, missed iterations, created rows, and stored human and bot events after the
workers drained. A request that k6 couldn't start counts as unsent work even if every request
that reached the API succeeded.

### Mixed traffic and a hot link

| Workload per second | Duration | Delivered redirects/sec | Redirect p99 | Result |
|---|---:|---:|---:|---|
| 1,000 redirects + 50 creates + 10 reports | 120 s | 1,000 | 1.3 ms | Passed |
| 3,200 redirects + 100 creates + 10 reports | 60 s | 3,200 | 7.9 ms | Passed |
| 5,000 redirects + 100 creates + 10 reports | 30 s | 4,963 | 52.6 ms | Failed |
| 5,000 redirects to one link | 30 s | 5,000 | 14.2 ms | Passed |

The 3,200/sec stage had no HTTP errors, incorrect responses, or dropped iterations. All 192,000
events were stored: 172,800 human events and 19,200 bot events.

At the mixed 5,000/sec target, k6 missed 1,023 scheduled iterations and redirect p99 exceeded
the 50 ms target. All 148,978 completed redirect events were eventually stored, but that stage
failed the workload criteria.

The single-hot-link test completed its offered load with no HTTP errors or missed iterations.
Its repeated destination lookups stayed in the local cache. That is useful evidence for a viral
link, while the fixture's small working set leaves larger, mostly uncached workloads to test.

### Creation and the reference workload

Creation completed 1,000 requests per second for 30 seconds, with creation p99 around 51 ms.
At the offered 3,472/sec rate, it delivered about 1,388/sec, with p99 around 773 ms and 62,222
unsent iterations. PostgreSQL reached its two-CPU allocation.

The full reference workload—34,722 redirects, 3,472 creations, and 50 reports per second—also
failed. It delivered roughly 8,779 redirects, 315 creations, and two reports per second. Redirect
p99 was 118 ms, with 867,271 unsent iterations and 284 incorrect responses.

One creation committed after its client had timed out. The database therefore contained 9,693
created rows against 9,692 client-confirmed creations. This is an example of an uncertain outcome
after a timeout: the response alone doesn't tell the client whether the write happened.

The click workers eventually stored all 270,048 successful redirect events from that overloaded
stage. The generator and services were competing for the same machine, so this run identifies
a failed local workload without establishing the ceiling of a separately hosted API.

The [load results](k6-benchmarks/2026-09-29-local/load-results.json) record successful and
failed stages. The [current k6 guide](server/k6/README.md) documents new runs; the older harness
used for these measurements has been retired.
These short synthetic runs establish capacity under their stated conditions; longer runs and
a separate generator are needed to assess sustained deployment capacity.

## What failure testing changed

Steady traffic exercises the usual paths. Dependency failures and restarts exposed problems
around delivery and invalidation.

**PostgreSQL outage.** Warm links continued redirecting at 500/sec through a roughly six-second
database interruption in a 30-second stage. Redirect p99 was about 0.9 ms, and all 15,001 events
were stored after recovery. An uncached link returned `503` during the outage. Cached data can
keep a known destination available, but creation and unknown destinations still need PostgreSQL.

**API crash.** In the single-instance recovery sample, the database was stopped before 100
redirects and the API was killed before database recovery. All 100 events were eventually stored
after restart; reclaiming pending stream entries took about 61 seconds. That sample demonstrates
replay from the surviving stream. Its scope doesn't include a simultaneous Redis or host crash.
The [fault-check results](k6-benchmarks/2026-09-29-local/fault-checks.json) also record
a graceful-shutdown sample.

**Redis outage and replica failure.** A separate two-instance rehearsal included a 15-second
Redis interruption. During it, redirect p99 rose to about 51 ms as reads fell back to PostgreSQL.
Killing one API later caused the generator to retry unreachable requests on the other instance.
The complete fault stage recorded 58 incorrect responses and four lost clicks, with no duplicate
clicks. It provides useful recovery evidence, including failures, for that separate topology.

**Takedowns.** A failed Redis revocation could previously let the same API reload a deleted link
into its local cache. The service now remembers the revocation locally and retries Redis in the
background. The cache also discards an older in-flight fill after invalidation. In the healthy
Redis takedown check, both APIs stopped resolving the link within about 0.13 seconds. Propagation
during a Redis failure still depends on retries and cache expiry.

The [two-instance rehearsal](k6-benchmarks/2026-09-29-local/two-instance-rehearsal.md)
records the topology and fault timeline. It used direct requests with client-side failover;
a real load balancer remains part of deployment testing.

## Deploying the first version

The first deployment plan keeps one API behind Traefik on Coolify, with Redis and PostgreSQL
alongside it.

```text
Internet -> Traefik / TLS -> Go API
                               |
                    +----------+----------+
                    |                     |
                  Redis               PostgreSQL
                                          |
                                    Off-site backups
```

## Measuring the deployed service

On October 12, 2026, I ran redirects against the live service on that VPS: 4 vCPU AMD EPYC under
KVM, 7.8 GiB RAM, shared with two other applications. Requests went through the whole production
path, with TLS in Traefik, the Go API, and every click recorded through Redis into PostgreSQL. k6
ran on the same VPS and ramped to each rate for 20 seconds before holding it for a measured minute.

| Rate held for 60 s | Errors | p50 | p95 | p99 | Host CPU | Go API | Traefik | k6 |
|---|---|---|---|---|---|---|---|---|
| 1,000 redirects/sec | 0% | 2.1 ms | 13.8 ms | 39.6 ms | 71% | 0.4 vCPU | 0.7 vCPU | 0.7 vCPU |
| 2,000 redirects/sec (1,978 delivered) | 0% | 5.5 ms | 68.1 ms | 152 ms | 86% | 0.5 vCPU | 1.0 vCPU | 1.0 vCPU |

At 3,000 redirects/sec the host ran out of CPU before the ramp finished. Requests queued for five to
eight seconds and the run stopped itself without a failed response. The ceiling of this setup sits
between 2,000 and 3,000 redirects a second.

The Go service was the cheapest part of the path. At 2,000 redirects/sec it used about half a vCPU
and 115 MB of memory, while TLS termination and the load generator took two of the four vCPUs.

The script used for this run started its 20-second warm-up clock separately for each k6 virtual
user, so users added mid-run had their first 20 seconds left out of the measured minute. The figures
are approximate; the script now times the warm-up from the scenario start.

These numbers are an upper bound for real visitors. k6 kept its connections open, but a visitor
arriving from a shared link usually performs a fresh TLS handshake, which costs Traefik more. Every
request hit one cached link, and the load generator competed with the service for CPU. The
[production results](k6-benchmarks/2026-10-12-production/) include both passes, the CPU samples, and the
scripts.

## What comes next

The operational work I still need to verify is a backup restore, traffic spread across many links,
and load from a separate machine with new connections per visitor.

Adding API replicas would spread HTTP work across more CPU, but each brings its own connection
pool, local cache, and click consumers. Their combined database connection budget needs to leave
room for maintenance, migrations, and administration. A second API on the same machine also
shares that machine's failure boundary.

Storage deserves its own capacity plan. The reporting fixture used about 758 MB for three million
clicks, roughly 250 bytes per event including its storage overhead. At that measured density,
three billion events would add about 750 GB a day. That is a rough extrapolation from the fixture,
and a reason to reconsider raw retention and analytics storage before attempting the reference
workload.

Building shortened.cv gave me a concrete way to connect caching, ownership, retries, retention,
and operations. The next measurements need to follow real visitors through the deployed path
and watch the queues, database, and disk over time. Those are the measurements that will guide the
first upgrade.
