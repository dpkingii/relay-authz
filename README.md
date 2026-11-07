# relay-authz

[![CI](https://github.com/dpkingii/relay-authz/actions/workflows/ci.yml/badge.svg)](https://github.com/dpkingii/relay-authz/actions/workflows/ci.yml)

A relationship-based authorization service in Go, modeled on Google's [Zanzibar paper](https://research.google/pubs/zanzibar-googles-consistent-global-authorization-system/). It answers "can this user do this action on this object?" from relation tuples stored in PostgreSQL, with check results cached in Redis, and the reverse question, "what can this user access?"

![Granting alice access through a group and a folder, checking it, looking up what she can view, then revoking it](docs/demo.gif)

## Highlights

- **4,000 checks/s with a p99 under 8 ms** over a 6-hop permission graph. At 8,000 checks/s the Redis cache holds a 12.6 ms p99 where uncached checks degrade to 95 ms.
- **Revoked access is never served from the cache.** Every write returns a revision, and a check can demand a result at least that new: a simplified version of Zanzibar's zookies, for its "new enemy" problem.
- **"What can alice see?" in 7 queries instead of 45,760**, by walking the graph from the user instead of checking every document.
- **Checks and lookups are tested against brute force**: both are compared with a simple closure over every grant on 300 random tuple sets, cycles included.
- **The rules are data.** The same service runs a [clinic's records](#another-domain-a-clinic) from a second YAML file.

## Quick start

```sh
docker compose --profile server up -d --build      # server on localhost:8080

curl -X POST localhost:8080/v1/tuples -d '{"tuple": "group:finance#member@user:alice"}'
curl -X POST localhost:8080/v1/tuples -d '{"tuple": "folder:finance#viewer@group:finance#member"}'
curl -X POST localhost:8080/v1/tuples -d '{"tuple": "document:budget#parent@folder:finance"}'
# {"revision":3}

curl 'localhost:8080/v1/check?object=document:budget&relation=viewer&user=user:alice'
# {"allowed":true,"revision":3}

curl 'localhost:8080/v1/lookup?type=document&relation=viewer&user=user:alice'
# {"objects":["document:budget"],"revision":3}

curl -X DELETE localhost:8080/v1/tuples -d '{"tuple": "group:finance#member@user:alice"}'
# {"revision":4}

curl 'localhost:8080/v1/check?object=document:budget&relation=viewer&user=user:alice&at_least=4'
# {"allowed":false,"revision":4}
```

The first check is cached; the last one can't reuse it, because it asks for revision 4 or later.

## How it works

### Rules and checks

A tuple like `document:budget#viewer@group:finance#member` says a subject has a relation on an object. `rules.yaml` says how relations grant each other:

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

Together they form a graph. Here alice is in finance, finance can view a folder, the budget is in that folder, and alice owns the roadmap:

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/diagrams/check-graph-dark.svg">
  <img alt="user:alice reaches document:budget viewer through group:finance member and folder:finance viewer, and reaches document:roadmap viewer through owner and editor" src="docs/diagrams/check-graph-light.svg">
</picture>

A check starts at a document and searches back toward alice, stopping at the first path it finds. It skips any `object#relation` it has already visited, which handles cycles like two groups containing each other, and gives up after 25 hops.

### Revisions and caching

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/diagrams/caching-dark.svg">
  <img alt="A check works out its required revision, then either returns a cached result at that revision or newer, or walks the tuple graph in Postgres and caches the result with its revision" src="docs/diagrams/caching-light.svg">
</picture>

Every write bumps a global revision in the same transaction and returns it. Results are cached in Redis with the revision they were computed at.

- **With `at_least=N`**, a check accepts any result from revision N on. A caller that just revoked access passes the revision the delete returned, so the answer can't predate the revoke.
- **By default**, a check uses a revision the server read at most `MAX_STALENESS` ago. The default of `0s` means the current revision. A larger value lets checks share cached results despite writes elsewhere, at the cost of missing that much of the latest changes. It's opt-in, because a caller that doesn't pass `at_least` would otherwise see revoked access for that long.

### Lookup

`GET /v1/lookup` starts at the user and follows the arrows, one hop per round, collecting every object it reaches: the budget and the roadmap above. Each round reads the tuples for every newly reached object in one query, so a lookup costs one query per hop however many objects it finds.

### Another domain: a clinic

Nothing in the service knows about documents. [`examples/clinic.yaml`](examples/clinic.yaml) gives a patient's records to the patient, their primary physician, their care teams and referred specialists. Run it with `RULES=examples/clinic.yaml go run ./cmd/server`:

```
POST /v1/tuples {"tuple": "patient:p1#care_team@team:cardiology"}
POST /v1/tuples {"tuple": "team:cardiology#member@user:nurse_ana"}
POST /v1/tuples {"tuple": "record:p1_echo#patient@patient:p1"}

GET /v1/check?object=record:p1_echo&relation=viewer&user=user:nurse_ana   → allowed (care team)
```

[`clinic_test.go`](internal/check/clinic_test.go) runs the full example. The data is made up.

## Performance

Apple M4 Pro, with the server, Postgres 17, Redis 7 and k6 all in Docker Compose:

| measurement | uncached | cached |
|---|---|---|
| p99 at 4,000 checks/s (median of 3 × 60 s) | 5.4 ms | 4.2 ms, 37% hits |
| p99 at 8,000 checks/s (one 60 s run) | 95.3 ms | 12.6 ms, 46% hits |
| hit rate with 1% writes at 4,000 checks/s | | 1%, or 14% with `MAX_STALENESS=5s` |

Looking up the 400 documents a user can view takes 7 queries and 3.5 ms; checking each of the 2,000 documents takes 45,760 queries and 11.9 s.

The method, every table and what the numbers say about the design are in [docs/benchmarks.md](docs/benchmarks.md).

## API

```
POST   /v1/tuples   {"tuple": "document:budget#viewer@user:alice"}   → {"revision": 41}
DELETE /v1/tuples   {"tuple": "document:budget#viewer@user:alice"}   → {"revision": 42}
GET    /v1/check?object=document:budget&relation=viewer&user=user:alice[&at_least=42]
                                                     → {"allowed": false, "revision": 42}
GET    /v1/lookup?type=document&relation=viewer&user=user:alice
                                                     → {"objects": ["document:roadmap"], "revision": 42}
```

Writes and deletes are idempotent. Unknown types or relations, and an `at_least` newer than the latest revision, get a 400. Anything needing more than 25 hops gets a 422. Check responses carry `X-Cache: hit` or `miss`. Lookups always read the latest revision and aren't cached.

## Notes and limits

- **One revision for everything.** Any write makes every cached result too old for a check that needs the latest revision. Snapshots and `at_least` trade staleness for hits.
- **Only whole checks are cached.** Zanzibar also caches subproblems, such as one folder's viewers, which is the next lever for the hit rate.
- **Checks run sequentially**, one Postgres query per step. Zanzibar evaluates branches in parallel.
- **Lookups explore everything the user can reach** and aren't cached or paged.
- Each server keeps its own snapshot, checks don't read at a fixed snapshot, the revision row serializes writes, and there's one Postgres with no sharding.

## Develop

```
docker compose up -d                 # Postgres on localhost:5434, Redis on localhost:6380
export DATABASE_URL=postgres://authz:authz@localhost:5434/authz
export REDIS_URL=redis://localhost:6380

go test ./...                        # tests needing Postgres or Redis skip without these
go run ./cmd/server                  # also reads RULES, ADDR and MAX_STALENESS
```

After changing `schema.sql`, reset the database with `docker compose down -v && docker compose up -d`.

## Layout

| path | what |
|---|---|
| `internal/tuple` | tuple types and the `type:id#relation@type:id` text form |
| `internal/store` | Postgres reads and writes, and the revision |
| `internal/rules` | loads and validates `rules.yaml` |
| `internal/check` | the check and lookup algorithms |
| `internal/cache` | Redis cache keyed by revision, and the benchmarks |
| `internal/api` | HTTP handlers |
| `schema.sql` | the `tuples` and `revision` tables |
| `examples/clinic.yaml` | rules for a clinic's records |
| `loadtest/` | k6 script, the script that runs every mode, the summary script, and results |
| `docs/` | benchmark results, the README diagrams, the demo GIF and the [vhs](https://github.com/charmbracelet/vhs) script that records it |
