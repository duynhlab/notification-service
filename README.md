# notification-service

Notification microservice for email, SMS, and in-app notifications.

## Features

- Email notifications
- SMS notifications
- In-app notifications
- Mark as read

## API Endpoints

> **Browser callers** hit `https://gateway.duynhne.me/notification/v1/private/notifications/…` (JWT required); Kong rewrites to the cluster paths below. `POST /api/v1/notify/{email,sms}` stays internal — service-to-service only. See [homelab naming convention](https://github.com/duynhlab/homelab/blob/main/docs/api/api-naming-convention.md).

| Method | Cluster path | Edge path (via gateway) |
|--------|--------------|-------------------------|
| `GET` | `/api/v1/notifications` | `/notification/v1/private/notifications` |
| `GET` | `/api/v1/notifications/count` | `/notification/v1/private/notifications/count` |
| `GET` | `/api/v1/notifications/:id` | `/notification/v1/private/notifications/:id` |
| `PATCH` | `/api/v1/notifications/:id` | `/notification/v1/private/notifications/:id` |
| `POST` | `/api/v1/notify/email` | *(internal — not on gateway)* |
| `POST` | `/api/v1/notify/sms` | *(internal — not on gateway)* |

## Tech Stack

- Go + Gin framework
- PostgreSQL 16 (supporting-db cluster, cross-namespace)
- PgBouncer connection pooling
- OpenTelemetry tracing

## Development

### Prerequisites

- Go 1.25+
- [golangci-lint](https://golangci-lint.run/welcome/install/) v2+

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

### Pre-push Checklist

```bash
go build ./... && go test ./... && golangci-lint run --timeout=10m
```
