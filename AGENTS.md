# notification-service

> AI Agent context for understanding this repository

## 📋 Overview

Notification microservice. Handles email, SMS, and in-app notifications.

Module path: `github.com/duynhlab/notification-service`.

Serves two transports: **HTTP `:8080`** (browser `private` + in-cluster `internal`
routes) and **gRPC `:9090`** (east-west — the official transport, always on).

## 🏗️ Architecture

```
notification-service/
├── cmd/main.go              # Wiring: HTTP + gRPC servers, DI, graceful shutdown
├── config/config.go         # Env-based config (Load + Validate)
├── db/migrations/sql/       # Flyway migrations (notifications table)
├── internal/
│   ├── core/
│   │   ├── database.go                # pgx/v5 pool (Connect/GetPool)
│   │   ├── notification_repository.go # Repository impl (SQL lives here)
│   │   └── domain/                    # Domain models + repository interface
│   ├── logic/v1/
│   │   ├── service.go       # Business rules
│   │   └── errors.go        # Sentinel errors
│   ├── grpc/v1/server.go    # gRPC adapter over logic (mirrors web/v1)
│   └── web/v1/handler.go    # HTTP handlers
├── middleware/              # tracing, logging, prometheus, profiling, resource
└── Dockerfile
```

## 🔌 API Endpoints

Routes are mounted directly at `/{service}/v1/{audience}/…` (Variant A — single URL shape). Kong is pure pass-through for `private`; `internal` is reachable only via service DNS.

| Method | Path | Audience | Description |
|--------|------|----------|-------------|
| `GET` | `/notification/v1/private/notifications` | private | Get all notifications for the current user |
| `GET` | `/notification/v1/private/notifications/count` | private | Unread count (badge poll) |
| `GET` | `/notification/v1/private/notifications/:id` | private | Get notification by ID |
| `PATCH` | `/notification/v1/private/notifications/:id` | private | Mark as read |
| `POST` | `/notification/v1/internal/notify/email` | internal | Send email — called by other services via `http://notification.notification.svc.cluster.local:8080` |
| `POST` | `/notification/v1/internal/notify/sms` | internal | Send SMS — same (in-cluster only) |

Full convention + inventory: [`homelab/docs/api/api-naming-convention.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/api-naming-convention.md).

## 🔗 gRPC (east-west)

gRPC is the official east-west transport and **always runs** on `:9090` (built via
shared `pkg/grpcx` — OpenTelemetry interceptors, health, reflection).

| Role | Detail |
|------|--------|
| **Server** | Implements `notification.v1.NotificationService` (`SendEmail`, `SendSMS`). `SendEmail` is called best-effort by `order-service` on checkout. Impl in `internal/grpc/v1/server.go` is a thin adapter over the logic layer (mirrors `web/v1`). |
| **Client** | Validates JWTs via `auth.v1.AuthService/GetMe` through shared `pkg/authmw`. Target: `AUTH_GRPC_ADDR` (default `dns:///auth.auth.svc.cluster.local:9090`). |

## 📐 3-Layer Architecture

| Layer | Location | Responsibility |
|-------|----------|----------------|
| **Web** | `internal/web/v1/handler.go` | HTTP, validation |
| **gRPC** | `internal/grpc/v1/server.go` | gRPC adapter → Logic (mirrors Web) |
| **Logic** | `internal/logic/v1/service.go` | Business rules (❌ NO SQL) |
| **Core** | `internal/core/` | Domain models, repository interface + impl, pgx pool |

Both the Web and gRPC layers call the **same** `logic/v1` service.

## 🗄️ Database

| Component | Value |
|-----------|-------|
| **Cluster** | supporting-db (shared with user, shipping) |
| **PostgreSQL** | 16 |
| **HA** | Single instance |
| **Pooler** | PgBouncer Sidecar |
| **Endpoint** | `supporting-db-pooler.user.svc.cluster.local:5432` |
| **Pool Mode** | Transaction |
| **Cross-namespace** | Yes (cluster in `user` namespace) |

**Note:** Database cluster is in `user` namespace. Zalando Operator syncs credentials via cross-namespace secret.

## 🚀 Graceful Shutdown

**VictoriaMetrics Pattern:**
1. `/ready` → 503 when shutting down
2. Drain delay (5s)
3. Sequential: HTTP → Database → Tracer

## 📊 Observability

Uses shared `github.com/duynhlab/pkg/obsx`.

- **Metrics**: `obsx.SetupMetrics()` (called in `main` before the gRPC server)
  puts gRPC RED metrics (`rpc_server_*`, `rpc_client_*`) on the **existing**
  `/metrics` endpoint via the shared Prometheus registry — **no separate port**.
  The platform `ServiceMonitor` scrapes `/metrics` on `:8080`. HTTP RED metrics
  come from `middleware/prometheus.go` on the same endpoint (with trace exemplars).
- **Logging**: `middleware/logging.go` uses `obsx.TraceIDFromContext` so log
  `trace_id` correlates with exported traces (falls back to `traceparent` /
  `X-Trace-ID` header or a generated ID).
- **Tracing**: OpenTelemetry → OTLP HTTP (OTel Collector).
- **Profiling**: Pyroscope.
- **Middleware chain order**: tracing → logging → metrics.

## 🔧 Tech Stack

| Component | Technology |
|-----------|------------|
| **Framework** | Gin (HTTP) + gRPC (`google.golang.org/grpc` via `pkg/grpcx`) |
| **Database** | PostgreSQL via pgx/v5 (simple protocol, no stmt cache — pooler-safe) |
| **Shared pkg** | `obsx`, `grpcx`, `authmw`, `proto/auth/v1`, `proto/notification/v1` |
| **Observability** | OpenTelemetry tracing, Pyroscope profiling, Prometheus metrics |
| **Auth** | JWT validated via auth gRPC (`pkg/authmw` → `AUTH_GRPC_ADDR`) |

## Code Quality

**MANDATORY**: All code changes MUST pass lint before committing.

- Linter: `golangci-lint` v2+ with `.golangci.yml` config (60+ linters enabled)
- Zero tolerance: PRs with lint errors will NOT be merged
- CI enforces: `go-check` job runs lint on every PR

### Commands (run in order)

```bash
go mod tidy              # Clean dependencies
go build ./...           # Verify compilation
go test ./...            # Run tests
golangci-lint run --timeout=10m  # Lint (MUST pass)
```

### Pre-commit One-liner

```bash
go build ./... && go test ./... && golangci-lint run --timeout=10m
```

### Common Lint Fixes

- `perfsprint`: Use `errors.New()` instead of `fmt.Errorf()` when no format verbs
- `nosprintfhostport`: Use `net.JoinHostPort()` instead of `fmt.Sprintf("%s:%s", host, port)`
- `errcheck`: Always check error returns (or explicitly `_ = fn()`)
- `goconst`: Extract repeated string literals to constants
- `gocognit`: Extract helper functions to reduce complexity
- `noctx`: Use `http.NewRequestWithContext()` instead of `http.NewRequest()`

## 3-Layer Coding Rules

**CRITICAL**: Strict layer boundaries. Violations will be rejected in code review.

### Layer Boundaries

| Layer | Location | ALLOWED | FORBIDDEN |
|-------|----------|---------|-----------|
| **Web** | `internal/web/v1/` | HTTP handling, JSON binding, DTO mapping, call Logic, aggregation | SQL queries, direct DB access, business rules |
| **Logic** | `internal/logic/v1/` | Business rules, call repository interfaces, domain errors | SQL queries, `database.GetPool()`, HTTP handling, `*gin.Context` |
| **Core** | `internal/core/` | Domain models + repository interface (`core/domain/`), repository impl + SQL (`core/notification_repository.go`), DB connection (`core/database.go`) | HTTP handling, business orchestration |

### Dependency Direction

```
Web -> Logic -> Core (one-way only, never reverse)
```

- Web imports Logic and Core/domain
- Logic imports Core/domain and Core/repository interfaces
- Core imports nothing from Web or Logic

### DO

- Put HTTP handlers, request validation, error-to-status mapping in `web/`
- Put business rules, orchestration, transaction logic in `logic/`
- Put SQL queries in the repository impl (`core/notification_repository.go`)
- Use repository interfaces (defined in `core/domain/`) for data access in Logic layer
- Use dependency injection (constructor parameters) for all service dependencies

### DO NOT

- Write SQL or call `database.GetPool()` in Logic layer
- Import `gin` or handle HTTP in Logic layer
- Put business rules in Web layer (Web only translates and delegates)
- Call Logic functions directly from another service (use HTTP aggregation in Web layer)
- Skip the Logic layer (Web must not call Core/repository directly)
