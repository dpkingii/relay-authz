# relay-authz

A relationship-based authorization service in Go, modeled on Google's [Zanzibar paper](https://research.google/pubs/zanzibar-googles-consistent-global-authorization-system/). It answers "can this user do this action on this object?" from relation tuples stored in PostgreSQL.

A tuple says one subject has a relation on one object:

```
document:budget#viewer@user:alice              alice can view the budget doc
document:budget#viewer@group:finance#member    every member of finance can view it
```

## Status

- [x] Tuples table, write/delete API
- [x] Direct checks
- [x] Recursive checks through groups and parent folders, config-defined relation rules, depth limit and cycle detection
- [ ] Revision numbers and a Redis cache keyed by revision (week 3)
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

## API

```
POST   /v1/tuples   {"tuple": "document:budget#viewer@user:alice"}   → 204
DELETE /v1/tuples   {"tuple": "document:budget#viewer@user:alice"}   → 204
GET    /v1/check?object=document:budget&relation=viewer&user=user:alice   → {"allowed": true}
```

Writes and deletes are idempotent. A tuple or check that names a type or relation missing from `rules.yaml` gets a 400, and a check that needs more than 25 hops gets a 422.

## Develop

```
docker compose up -d                 # Postgres on localhost:5434, schema applied on first start
export DATABASE_URL=postgres://authz:authz@localhost:5434/authz

go test ./...
go run ./cmd/server                  # http://localhost:8080
```

The store tests skip when `DATABASE_URL` isn't set. After changing `schema.sql`, reset the database with `docker compose down -v && docker compose up -d`.

## Layout

| path | what |
|---|---|
| `internal/tuple` | tuple types and the `type:id#relation@type:id` text form |
| `internal/store` | Postgres reads and writes |
| `internal/rules` | loads and validates `rules.yaml` |
| `internal/check` | the check algorithm |
| `internal/api` | HTTP handlers |
| `schema.sql` | the `tuples` table |
