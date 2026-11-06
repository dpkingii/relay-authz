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
- [ ] Docker deploy to EC2, k6 load test (week 4)

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

Uncached and "cached, latest" checks also read the current revision, which isn't counted in the query column. The cached numbers are all hits, since nothing writes during the benchmark. The real hit rate depends on the write rate and on how often the same check repeats, which the week 4 load test measures.

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
