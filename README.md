# relay-authz

A relationship-based authorization service in Go, modeled on Google's [Zanzibar paper](https://research.google/pubs/zanzibar-googles-consistent-global-authorization-system/). It answers "can this user do this action on this object?" from relation tuples stored in PostgreSQL, with check results cached in Redis.

A tuple says one subject has a relation on one object:

```
document:budget#viewer@user:alice              alice can view the budget doc
document:budget#viewer@group:finance#member    every member of finance can view it
```

## Status

- [x] Tuples table, write/delete API
- [x] Direct checks
- [x] Recursive checks through groups and parent folders, config-defined relation rules, depth limit and cycle detection
- [x] Revision numbers and a Redis cache keyed by revision
- [x] Docker image, Compose stack and k6 load test
- [x] Load test results

## Rules

`rules.yaml` declares each object type's relations and how they grant each other:

```yaml
document:
  parent:
  owner:
  editor:
    implied_by: [owner]                         # owners can edit
  viewer:
    implied_by: [editor]                        # editors can view
    via:
      - {tupleset: parent, relation: viewer}    # viewers of the parent folder can view
```

A check for `document:budget#viewer@user:abe` walks the tuple graph: abe's own tuples, then groups whose members are viewers (and groups inside those groups), then editors and owners, then viewers of the parent folder and its parents. Every rule is a union, so the walk stops at the first match. It skips any `object#relation` it has already visited, which handles cycles like two groups containing each other, and it gives up after 25 hops.

The tests compare every check against a brute-force computation of all grants on 300 random tuple sets, including group and folder cycles.

## Revisions and caching

Every write and delete bumps a global revision in the same transaction and returns it. A check result is cached in Redis with the revision it was computed at, and a check is served from the cache only if that revision is recent enough:

- **By default** a check must reflect every change committed so far. It reads the current revision (one cheap query), and any write since the cached result forces a recompute.
- **With `at_least=N`** a check accepts any result computed at revision N or later. A caller that just revoked access passes the revision the delete returned, so the answer can't predate the revoke. This is Zanzibar's "new enemy" problem, solved with a simplified version of its zookies. A caller that can tolerate older answers passes an older revision and gets more cache hits without touching Postgres.

A test grants access through a folder and a group, caches the allow, revokes the group membership and checks that the stale allow is never served.

## API

```
POST   /v1/tuples   {"tuple": "document:budget#viewer@user:alice"}   → {"revision": 41}
DELETE /v1/tuples   {"tuple": "document:budget#viewer@user:alice"}   → {"revision": 42}
GET    /v1/check?object=document:budget&relation=viewer&user=user:alice[&at_least=42]
                                                     → {"allowed": false, "revision": 42}
```

Writes and deletes are idempotent. A tuple or check that names a type or relation missing from `rules.yaml` gets a 400, as does an `at_least` newer than the latest revision. A check that needs more than 25 hops gets a 422. Check responses carry `X-Cache: hit` or `miss`.

## Benchmarks

`go test ./internal/cache -run '^$' -bench . -benchtime 2000x`, median of 3 runs on an Apple M4 Pro, with Postgres 17 and Redis 7 in Docker Desktop on the same machine. Each document check asks for `viewer`.

| graph | queries per uncached check | uncached | cached, latest | cached, `at_least` |
|---|---|---|---|---|
| direct tuple | 1 | 0.34 ms | 0.31 ms | 0.15 ms |
| 5 nested groups | 6 | 1.09 ms | 0.29 ms | 0.14 ms |
| 5 nested parent folders | 29 | 4.61 ms | 0.31 ms | 0.11 ms |
| 5 nested parent folders, denied | 30 | 4.83 ms | 0.31 ms | 0.15 ms |

Uncached and "cached, latest" checks also read the current revision, which isn't counted in the query column. The cached numbers are all hits, since nothing writes during the benchmark. The real hit rate depends on the write rate and on how often the same check repeats, which the [load test](#load-test) measures.

## Load test

`loadtest/check.js` seeds 4,705 tuples, then sends checks at a constant rate with [k6](https://k6.io):

- **Data:** 500 users in 50 groups, half of them nested in the other half. 5 root folders, 25 child folders and 125 leaf folders, with groups as root viewers and child editors. 2,000 documents spread evenly across the leaf folders, each with an owner. Most allowed checks walk document → leaf → child → root → group → nested group → user.
- **Traffic:** a random document and user per check, skewed so the top 1% of each gets about 22% of picks, as popular objects do. 80% ask for `viewer` and 20% for `editor`.
- **Modes:** `loadtest/run-all.sh` runs each rate in four modes, with Redis flushed before every cached run:
  - `uncached`: the server without Redis, the baseline
  - `cached`: default checks, which need the latest revision
  - `cached-at-least`: checks that accept any result from the seed revision on
  - `cached-writes`: default checks with 1% of requests writing a tuple

  Each run appends a JSON line to `loadtest/results/<LABEL>.jsonl`.

- **Setup:** the server, Postgres, Redis and k6 all run in Docker Compose, on one machine. k6 runs inside Docker's network because on a Mac, Docker Desktop's port forwarding becomes the bottleneck before the server does: with k6 or the server on the host, checks slowed to tens or hundreds of milliseconds at rates the all-Docker setup handled easily.

```
docker compose up -d
LABEL=m4pro RATES="500 1000 2000 3000" RUNS=3 DURATION=60s loadtest/run-all.sh
python3 loadtest/summarize.py loadtest/results/m4pro.jsonl 60
```

### Results

Apple M4 Pro (12 cores, 24 GB), Docker Desktop's VM given all 12 cores and 8 GB, Postgres 17, Redis 7, Go 1.27, k6 1.4. Each row is the median of 3 runs of 60 seconds; the p99 range is across those runs. No run had a failed request.

| target checks/s | mode | achieved checks/s | cache hits | p50 | p95 | p99 (range) | dropped |
|---|---|---|---|---|---|---|---|
| 500 | uncached | 500 | 0% | 1.5 ms | 1.8 ms | 2.1 ms (2.0–7.3) | 0 |
| 500 | cached | 500 | 17% | 1.6 ms | 1.9 ms | 2.4 ms (2.2–4.4) | 0 |
| 500 | cached-at-least | 500 | 17% | 1.6 ms | 1.9 ms | 2.2 ms (2.1–2.2) | 0 |
| 500 | cached-writes | 495 | 1% | 1.6 ms | 1.9 ms | 2.2 ms (2.1–2.3) | 0 |
| 1,000 | uncached | 999 | 0% | 1.5 ms | 2.0 ms | 6.6 ms (2.9–16.4) | 44 |
| 1,000 | cached | 1,000 | 23% | 1.5 ms | 2.0 ms | 2.3 ms (2.3–3.0) | 0 |
| 1,000 | cached-at-least | 1,000 | 23% | 1.5 ms | 1.9 ms | 2.6 ms (2.3–2.7) | 0 |
| 1,000 | cached-writes | 990 | 1% | 1.6 ms | 2.1 ms | 4.6 ms (2.5–21.3) | 6 |
| 2,000 | uncached | 2,000 | 0% | 1.6 ms | 2.2 ms | 3.0 ms (3.0–3.5) | 0 |
| 2,000 | cached | 2,000 | 30% | 1.5 ms | 2.2 ms | 2.8 ms (2.8–2.9) | 0 |
| 2,000 | cached-at-least | 2,000 | 30% | 1.5 ms | 2.2 ms | 3.0 ms (2.7–3.3) | 4 |
| 2,000 | cached-writes | 1,980 | 1% | 1.7 ms | 2.4 ms | 3.6 ms (3.1–3.9) | 32 |
| 3,000 | uncached | 2,998 | 0% | 2.1 ms | 3.3 ms | 5.2 ms (4.0–11.4) | 145 |
| 3,000 | cached | 2,996 | 34% | 1.6 ms | 3.3 ms | 7.7 ms (3.4–8.5) | 216 |
| 3,000 | cached-at-least | 2,996 | 34% | 1.6 ms | 3.1 ms | 5.7 ms (3.2–5.8) | 234 |
| 3,000 | cached-writes | 2,969 | 1% | 2.3 ms | 3.5 ms | 4.5 ms (4.0–6.4) | 0 |
| 4,000 | uncached | 3,997 | 0% | 2.6 ms | 3.9 ms | 5.4 ms (5.1–12.6) | 209 |
| 4,000 | cached | 3,997 | 37% | 1.6 ms | 3.2 ms | 4.2 ms (4.2–4.5) | 207 |
| 4,000 | cached-at-least | 3,999 | 37% | 1.6 ms | 3.1 ms | 4.0 ms (4.0–4.2) | 48 |
| 4,000 | cached-writes | 3,959 | 1% | 2.8 ms | 4.0 ms | 5.2 ms (5.0–5.9) | 117 |

**Up to 4,000 checks/s the median p99 stays under 8 ms in every mode**, though single runs reach 21 ms, and under 0.1% of requests are dropped (k6 drops a request when it can't start it on time). The cache barely changes latency at these rates: an uncached check is already about 2 ms at the median.

To find the ceiling, one run per mode at 6,000 and 8,000 checks/s:

| target checks/s | mode | achieved checks/s | cache hits | p50 | p95 | p99 | dropped |
|---|---|---|---|---|---|---|---|
| 6,000 | uncached | 5,991 | 0% | 3.0 ms | 5.5 ms | 13.7 ms | 570 |
| 6,000 | cached | 5,997 | 42% | 1.8 ms | 3.9 ms | 5.5 ms | 212 |
| 6,000 | cached-at-least | 5,973 | 42% | 1.9 ms | 5.0 ms | 35.2 ms | 1,596 |
| 6,000 | cached-writes | 5,927 | 1% | 3.2 ms | 5.1 ms | 11.0 ms | 727 |
| 8,000 | uncached | 7,868 | 0% | 4.7 ms | 66.0 ms | 95.3 ms | 7,944 |
| 8,000 | cached | 7,973 | 46% | 1.8 ms | 4.7 ms | 12.6 ms | 1,644 |
| 8,000 | cached-at-least | 7,928 | 46% | 1.9 ms | 6.8 ms | 52.3 ms | 4,306 |
| 8,000 | cached-writes | 7,739 | 1% | 9.5 ms | 80.3 ms | 101.1 ms | 11,124 |

**Uncached, the server saturates between 6,000 and 8,000 checks/s**: at 8,000 its p99 jumps to 95 ms and it drops 1.7% of requests. With the cache it holds a 12.6 ms p99 at 8,000, since 46% of checks never reach Postgres. These are single runs, so the `cached-at-least` p99s, worse than `cached` despite the same hit rate, are more likely noise than a real difference.

What the numbers say about the design:

- **The hit rate is the whole story for the cache.** It rises with load (17% at 500 checks/s, 46% at 8,000) because more repeats land within an entry's lifetime, and it only pays off once Postgres is the bottleneck.
- **1% writes take the hit rate to 1%.** Every write bumps the one global revision, so a default check almost never finds a fresh-enough entry. That's the single-revision tradeoff from [Notes and limits](#notes-and-limits), measured. `at_least` avoids it, but only for callers that can accept older answers.
- **A 10-minute stretch of the first sweep collapsed** (p99s of 1 to 7 seconds at 2,000 checks/s, then fine again at 3,000), most likely from other activity on the laptop. Those runs were discarded and the 2,000 and 4,000 rows rerun. On a shared laptop, treat these numbers as a rough guide, not a spec.

## Notes and limits

- **One revision for everything.** Any write anywhere makes every cached result too old for a default check. Callers that pass `at_least` don't have this problem. Zanzibar instead lets a server pick a slightly stale snapshot by default; this keeps the simple version and its tradeoff visible.
- **Only whole checks are cached.** Zanzibar also caches subproblems, such as one group's membership, which would help checks that share them.
- **Checks run sequentially** and query Postgres once per step; a folder chain re-reads the same `parent` tuples for each relation it tries. Zanzibar evaluates branches in parallel.
- **The revision row serializes writes.** Every write waits for the one revision row, which caps write throughput.
- **No snapshot reads.** A check reads tuples as they are at each query. It always reflects at least the revision read first, possibly newer.
- Single Postgres, no sharding.

## Develop

```
docker compose up -d                 # Postgres on localhost:5434, Redis on localhost:6380
export DATABASE_URL=postgres://authz:authz@localhost:5434/authz
export REDIS_URL=redis://localhost:6380

go test ./...
go run ./cmd/server                  # http://localhost:8080; without REDIS_URL it runs uncached
```

Tests that need Postgres or Redis skip when the variable isn't set. After changing `schema.sql`, reset the database with `docker compose down -v && docker compose up -d`.

## Layout

| path | what |
|---|---|
| `internal/tuple` | tuple types and the `type:id#relation@type:id` text form |
| `internal/store` | Postgres reads and writes, and the revision |
| `internal/rules` | loads and validates `rules.yaml` |
| `internal/check` | the check algorithm |
| `internal/cache` | Redis cache keyed by revision, and the benchmarks |
| `internal/api` | HTTP handlers |
| `schema.sql` | the `tuples` and `revision` tables |
| `loadtest/` | k6 script, the script that runs every mode, the summary script, and results |
