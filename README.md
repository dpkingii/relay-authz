# relay-authz

A relationship-based authorization service in Go, modeled on Google's [Zanzibar paper](https://research.google/pubs/zanzibar-googles-consistent-global-authorization-system/). It answers "can this user do this action on this object?" from relation tuples stored in PostgreSQL.

A tuple says one subject has a relation on one object:

```
document:budget#viewer@user:alice              alice can view the budget doc
document:budget#viewer@group:finance#member    every member of finance can view it
```

## Status

- [x] Tuples table, write/delete API
- [ ] Direct checks (week 1)
- [ ] Recursive checks through groups and parent folders, config-defined relation rules (week 2)
- [ ] Revision numbers and a Redis cache keyed by revision (week 3)
- [ ] Docker deploy to EC2, k6 load test (week 4)

## API

```
POST   /v1/tuples   {"tuple": "document:budget#viewer@user:alice"}   → 204
DELETE /v1/tuples   {"tuple": "document:budget#viewer@user:alice"}   → 204
GET    /v1/check?object=document:budget&relation=viewer&user=user:alice   → {"allowed": true}
```

Writes and deletes are idempotent.

## Develop

```
docker compose up -d                 # Postgres on localhost:5433, schema applied on first start
export DATABASE_URL=postgres://authz:authz@localhost:5433/authz

go test ./...
go run ./cmd/server                  # http://localhost:8080
```

The store tests skip when `DATABASE_URL` isn't set. After changing `schema.sql`, reset the database with `docker compose down -v && docker compose up -d`.

## Layout

| path | what |
|---|---|
| `internal/tuple` | tuple types and the `type:id#relation@type:id` text form |
| `internal/store` | Postgres reads and writes |
| `internal/check` | the check algorithm |
| `internal/api` | HTTP handlers |
| `schema.sql` | the `tuples` table |
