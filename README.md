# notification-service

The notification inbox: the records a customer sees, their read state, and the
east-west entry points that create them.

## Responsibilities

- **Owns:** `notifications` rows — title, message, type, read state, and the
  delivery key that makes a repeated send converge on one record.
- **Does not own:** actual email or SMS delivery. No provider is wired; a record
  is stored with a fixed sent status. Identity belongs to `auth-service`, and the
  order saga belongs to `order-service` — this service is a participant it calls,
  never the orchestrator.

## Tech

| Area | Technology |
|------|------------|
| Runtime | Go 1.27 |
| Transports | HTTP (private inbox, internal create) · gRPC (east-west send) |
| Data | PostgreSQL — one table, `notifications` |
| Platform libraries | `authmw`, `dbx`, `grpcx`, `httpx`, `logger/zapx`, `migratex`, `obsx`, `proto` |

## API

- **Canonical contract:** [`homelab/docs/api/notification.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/notification.md)
- **Shared conventions:** [`homelab/docs/api/api.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/api.md)
- **Surfaces:** a JWT-protected inbox for the customer, a cluster-only internal
  create, and `notification.v1.NotificationService` east-west — the order worker
  calls it as a saga participant. HTTP `:8080` also carries `/health` and
  `/ready`.

Routes, payloads and error codes live in the contract, so there is one place to
change when they change.

## Run locally

Prefer the homelab **local-stack** — the inbox needs a signed token, and the
interesting traffic arrives from the order workflow.

Standalone you need PostgreSQL reachable through the `DB_*` variables and three
roles: `notification_owner` (owns the schema, cannot log in),
`notification_migrator` (logs in, may only `SET ROLE notification_owner`) and
`notification_runtime` (serves traffic, CRUD only). `migrate` and `seed` log in
as the migrator with its password in `DB_PASSWORD` and need
`DB_MIGRATION_ROLE=notification_owner`; they refuse to run without it, and they
must reach PostgreSQL directly, not through a transaction pooler. The app logs in
as `notification_runtime`; when `DB_PASSWORD_FILE` is set it reads the password
from that file on every new connection, so a rotated password needs no restart.
`notification_owner` must own the `notification` database: on PostgreSQL 15+
that is what lets it create objects in the `public` schema.

```bash
DB_USER=notification_migrator DB_MIGRATION_ROLE=notification_owner go run cmd/main.go migrate
DB_USER=notification_migrator DB_MIGRATION_ROLE=notification_owner go run cmd/main.go seed   # development only
DB_USER=notification_runtime go run cmd/main.go   # serve HTTP :8080 + gRPC :9090
```

## Verify

The commands CI runs, so a green local run means a green pipeline:

```bash
go build ./...
go test -race ./...
go test -tags=integration ./internal/core/repository/... ./db/seed/...   # needs Docker (testcontainers)
golangci-lint run
```

## Docs

- [Canonical contract](https://github.com/duynhlab/homelab/blob/main/docs/api/notification.md)
- [local-stack guide](https://github.com/duynhlab/homelab/blob/main/local-stack/README.md)

## License

MIT
