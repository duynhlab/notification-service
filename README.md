# notification-service

Notification microservice for email, SMS, and in-app notifications.

Module path: `github.com/duynhlab/notification-service`.

## Features

- Email notifications
- SMS notifications
- In-app notifications (list, unread count, get by ID)
- Mark as read

## Transports

The service exposes two listeners:

- **HTTP (`:8080`)** — browser-facing `private` routes (JWT) and in-cluster
  `internal` routes, plus `/health`, `/ready`, `/metrics`.
- **gRPC (`:9090`)** — east-west transport (the official one). Always runs.

### gRPC role

- **Server**: implements `notification.v1.NotificationService` (`SendEmail`,
  `SendSMS`). `SendEmail` is called best-effort by `order-service` on checkout.
  Built via the shared `pkg/grpcx` bootstrap (OpenTelemetry interceptors, health,
  reflection).
- No gRPC client: JWTs are verified locally via the shared `pkg/authmw` JWKS
  verifier (`AUTH_JWKS_URL`) — no per-request call to auth.

## API Endpoints

All HTTP routes follow Variant A naming — single path for browser and in-cluster callers. See [homelab naming convention](https://github.com/duynhlab/homelab/blob/main/docs/api/api-naming-convention.md).

| Method | Path | Audience |
|--------|------|----------|
| `GET` | `/notification/v1/private/notifications` | private |
| `GET` | `/notification/v1/private/notifications/count` | private |
| `PATCH` | `/notification/v1/private/notifications/read-all` | private |
| `GET` | `/notification/v1/private/notifications/:id` | private |
| `PATCH` | `/notification/v1/private/notifications/:id` | private |
| `POST` | `/notification/v1/internal/notify/email` | internal (in-cluster only) |
| `POST` | `/notification/v1/internal/notify/sms` | internal (in-cluster only) |

`private` routes require a valid JWT (enforced by `authmw.MiddlewareJWT`, which
verifies RS256 tokens locally against auth's JWKS). `internal` routes are
reachable only via service DNS — never on the gateway. The same `SendEmail`/`SendSMS` operations are also exposed over gRPC.

## Observability

Uses the shared `github.com/duynhlab/pkg/obsx` package.

- **Metrics**: `obsx.SetupMetrics()` bridges gRPC RED metrics
  (`rpc_server_*` and `rpc_client_*`) onto the **existing** `/metrics` endpoint
  via the shared Prometheus registry. There is **no separate metrics port**; the
  platform `ServiceMonitor` scrapes `/metrics` on `:8080`. HTTP RED metrics
  (`request_duration_seconds`, `requests_in_flight`, `request_size_bytes`,
  `response_size_bytes`) are emitted by the Gin Prometheus middleware on the same
  endpoint, with trace exemplars.
- **Tracing**: OpenTelemetry → OTLP HTTP to the OTel Collector
  (`OTEL_COLLECTOR_ENDPOINT`), sampled at `OTEL_SAMPLE_RATE` (10% default).
- **Logging**: structured Zap (JSON). The logging middleware uses
  `obsx.TraceIDFromContext` so log `trace_id` matches the exported trace, falling
  back to the `traceparent` / `X-Trace-ID` header or a generated ID.
- **Profiling**: Pyroscope continuous profiling (`PYROSCOPE_ENDPOINT`).

Gin middleware chain (order): **tracing → logging → metrics**.

## Tech Stack

- Go 1.26 + Gin
- gRPC (`google.golang.org/grpc`) via shared `pkg/grpcx`
- PostgreSQL via pgx/v5 (simple protocol, statement cache disabled for
  transaction-mode poolers like PgBouncer/PgCat)
- Shared `pkg` modules: `obsx`, `grpcx`, `authmw`, `proto/notification/v1`
- OpenTelemetry tracing, Pyroscope profiling, Prometheus metrics

## Configuration

Loaded from environment via `config.Load()` (12-factor; `.env` supported locally,
env vars take precedence). Key variables:

| Variable | Default | Purpose |
|----------|---------|---------|
| `SERVICE_NAME` | _(required)_ | Service name (traces, profiling) |
| `PORT` | `8080` | HTTP listen port |
| `GRPC_PORT` | `9090` | gRPC listen port |
| `ENV` | `development` | `development`/`staging`/`production` |
| `AUTH_JWKS_URL` | `http://auth.auth.svc.cluster.local:8080/auth/v1/public/jwks` | Auth JWKS endpoint (local JWT verification) |
| `DB_HOST` / `DB_PORT` / `DB_NAME` / `DB_USER` / `DB_PASSWORD` | — | PostgreSQL connection |
| `DB_SSLMODE` | `disable` | PostgreSQL SSL mode |
| `DB_POOL_MAX_CONNECTIONS` | `25` | pgx pool max conns |
| `TRACING_ENABLED` | `true` | Toggle OpenTelemetry tracing |
| `OTEL_COLLECTOR_ENDPOINT` | `otel-collector-…:4318` | OTLP HTTP endpoint |
| `OTEL_SAMPLE_RATE` | `0.1` | Trace sample rate (0.0–1.0) |
| `PROFILING_ENABLED` | `true` | Toggle Pyroscope profiling |
| `PYROSCOPE_ENDPOINT` | `http://pyroscope.monitoring…:4040` | Pyroscope endpoint |
| `METRICS_ENABLED` | `true` | Toggle metrics setup |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | Structured logging |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout (max 60s) |
| `READINESS_DRAIN_DELAY` | `5s` | Pre-shutdown drain delay (max 30s) |

## Development

### Prerequisites

- Go 1.26+
- [golangci-lint](https://golangci-lint.run/welcome/install/) v2+
- Docker (only for the integration tests — see [Testing](#testing))

### Local Development

```bash
# Install dependencies
go mod tidy
go mod download

# Build
go build ./...

# Test
go test ./...

# Lint (must pass before PR merge)
golangci-lint run --timeout=10m

# Run locally (requires .env or env vars)
go run cmd/main.go
```

### Testing

Unit tests use the stdlib `testing` package with hand-written mocks and table-driven
subtests (no testify/gomock). The **repository layer** is covered by **integration tests**
against a real PostgreSQL via [testcontainers](https://golang.testcontainers.org/).

```bash
# Unit tests (no Docker)
go test ./...

# With coverage (as CI runs it)
go test -race -coverprofile=coverage.out ./...

# Integration tests — repository layer, real Postgres (needs a running Docker daemon)
go test -tags=integration ./internal/core/repository/...
```

Integration tests are build-tagged `//go:build integration`, so the default `go test ./...`
skips them and the service binary never links testcontainers. CI runs both jobs and merges
their coverage into SonarCloud (gate: ≥ 80% on new code).

### Pre-push Checklist

```bash
go build ./... && \
  go test ./... && \
  go test -tags=integration ./internal/core/repository/... && \
  golangci-lint run --timeout=10m
```
