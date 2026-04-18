# notification-service

Notification microservice for email, SMS, and in-app notifications.

## Features

- Email notifications
- SMS notifications
- In-app notifications
- Mark as read

## API Endpoints

All routes follow Variant A naming — single path for browser and in-cluster callers. See [homelab naming convention](https://github.com/duynhlab/homelab/blob/main/docs/api/api-naming-convention.md).

| Method | Path | Audience |
|--------|------|----------|
| `GET` | `/notification/v1/private/notifications` | private |
| `GET` | `/notification/v1/private/notifications/count` | private |
| `GET` | `/notification/v1/private/notifications/:id` | private |
| `PATCH` | `/notification/v1/private/notifications/:id` | private |
| `POST` | `/notification/v1/internal/notify/email` | internal (in-cluster only) |
| `POST` | `/notification/v1/internal/notify/sms` | internal (in-cluster only) |

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
