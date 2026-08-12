# AGENTS.md

Agent-focused guide for `notification-service`. Keep changes minimal, verified
against the code, and consistent with existing patterns.

## Authority and scope

This repository implements the service. It does **not** define the contract.

- **Canonical contract:** [`homelab/docs/api/notification.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/notification.md)
- **Shared API rules:** [`homelab/docs/api/api.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/api.md)

Implement against those files. When this repository and the contract disagree,
**stop and classify the mismatch** using
[Resolving a mismatch](https://github.com/duynhlab/homelab/blob/main/docs/api/README.md#resolving-a-mismatch)
before changing either side. One class — an implementation that violates the
intended contract — **blocks the release tag**.

**Known open mismatch:** the contract still describes sends as at-least-once with
no deduplication and no idempotency key. Delivery-key idempotency shipped after
that page was last updated, so the contract is behind the code here. It is
classified as a canonical-doc gap, not a defect in this repository, and is being
corrected in homelab. Do not "restore" at-least-once behaviour to match the page.

No route, RPC, payload or error inventory belongs in this file. Manifests,
gateway routing, NetworkPolicy, database topology and platform observability
belong to [duynhlab/homelab](https://github.com/duynhlab/homelab).

## Contribution workflow for AI agents

- Never push to `main`. Branch first, then open a PR and let CI gate the merge.
- Use conventional branch prefixes: `feat/`, `fix/`, `docs/`, `chore/`, `refactor/`.
- Commits use a Conventional Commits prefix. Subject is imperative, capitalised,
  no trailing period.
- Do not add attribution trailers (`Signed-off-by`, `Co-authored-by`,
  `Assisted-by`, `Generated-by`, etc.).
- No GitHub issue references (`Fixes #123`) and no `@`-mentions in commit messages;
  put context in the PR description instead.
- One logical change per PR. PRs are merged via squash, so keep the branch focused.

## Build, test, lint

These are the commands CI runs, so a green local run means a green pipeline.

```bash
go build ./...
go vet ./...
go test -race ./...
go test -tags=integration ./internal/core/repository/...   # needs Docker (testcontainers)
golangci-lint run
```

`.golangci.yml` is the v2 config with 63 linters enabled. Sonar new-code coverage
must be ≥80%; `**/cmd/**`, `**/db/migrations/**` and `**/core/repository/**` are
excluded, everything else counts.

Local development against an unreleased `pkg`: `pkg` is one module per package,
so its root has no `go.mod` and a single `replace github.com/duynhlab/pkg` can no
longer resolve. Use one commented `replace` line per module — the trailer in
`go.mod` shows the shape, and
[`docs/api/pkg.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/pkg.md)
explains why.

## Architecture boundaries

**3-layer, dependencies flow one way only: transport → logic → core.**

- **Transport** — `internal/web/v1/` (HTTP) and `internal/grpc/v1/` (gRPC). The
  gRPC server is explicitly a thin adapter mirroring the HTTP one, so both paths
  share the same business logic and return identical data. Neither may run SQL or
  reach the pool.
- **Logic** — `internal/logic/v1/` holds the rules and the business metrics.
- **Core** — `internal/core/` owns the domain model, the repository interface and
  the Postgres implementation.

Observability is wired once through `github.com/duynhlab/pkg/obsx`; the pool comes
from `github.com/duynhlab/pkg/dbx`; the gRPC server is built by
`github.com/duynhlab/pkg/grpcx`; HTTP responses use the shared
`github.com/duynhlab/pkg/httpx` envelope; JWTs are verified by
`github.com/duynhlab/pkg/authmw`.

## Invariants

Rules an implementer can violate at the keyboard.

- **Delivery-key idempotency is race-safe by database constraint, not by
  read-then-write.** The insert conflicts on a partial unique index and the loser
  loads the winner's row, so two concurrent sends with the same key converge on
  one notification. Replacing it with select-then-insert reopens the race the
  index exists to close.
- **An empty delivery key means at-least-once, and that is deliberate.** The index
  is partial for exactly that reason. Do not make the key required and do not
  synthesise one server-side — producers own the deterministic key.
- **Both write paths must persist identically.** The title/message fallback exists
  in the plain create and in the deduplicating create; changing one without the
  other forks what is written from what is read.
- **Recipient validation lives in the logic layer**, because the gRPC path does no
  validation of its own. Moving it into the HTTP binding would silently leave
  gRPC unguarded.
- **A rejected recipient is not a send.** The latency histogram is deferred
  *after* the validation return on purpose, so bad requests do not pollute
  send-latency. Order of statements matters here.
- **Owner scoping is unconditional, and not-found must be indistinguishable from
  not-owned.** Every read and mutation carries the user id; no path may fetch a
  notification by id alone, and a miss must not leak that the row exists.
- **Metric labels are bounded enums — no ids, no recipient text, no message
  bodies.** Spans do carry recipient and user id; that pattern must not be copied
  into metrics, where it becomes cardinality.
- **The send histogram's explicit buckets are load-bearing.** The shared duration
  view matches semconv instruments by name only, so without explicit boundaries
  the SDK defaults collapse every sub-ten-second send into one bucket.
- **A fabricated trace id must never reach telemetry.** Only the active span's id
  may be logged; an invented one looks joinable while joining to nothing, which is
  worse mid-incident than an absent field. The generated fallback belongs on the
  response header only.
- **Probe suppression is one contract across logs and traces**, through the same
  skip list; a **failing** probe is still recorded. 4xx logs at warn, 5xx at
  error — a rejected request is not a broken service.
- **Pooler-safe database settings live in `pkg/dbx`.** The one local exception is
  the seed path, which sets simple protocol itself because it runs multi-statement
  files in a single exec.
- **`seed` is development-only** and refuses production. It is invoked explicitly
  — never from `migrate` or the serve path — and must not share the
  `schema_migrations` version table.
- **JWT verifier failure is fatal.** Local verification is the only auth path;
  there is no fallback, so failing closed at startup is correct. Observability and
  profiling failures only warn — do not level them together.
- **Observability must be set up before the gRPC server is built,** or the
  instrumentation handlers pick up a meter provider that does not exist yet and
  gRPC metrics silently disappear.
- **Migrations are forward-only** — `*.up.sql` only.

## Repository map

- `cmd/main.go` — wiring, subcommand dispatch, HTTP + gRPC bootstrap, graceful shutdown
- `config/config.go` — env config, `Validate()`, environment predicates
- `internal/web/v1/` — HTTP handlers
- `internal/grpc/v1/` — the `NotificationService` implementation
- `internal/logic/v1/` — business rules, sentinel errors with their status mapping, metrics
- `internal/core/` — pool wiring, domain model, repository interface and Postgres implementation
- `db/migrations/` — forward-only golang-migrate SQL, embedded
- `db/seed/` — development-only demo seed, embedded
- `middleware/` — tracing and logging only

## Gotchas

- Kyverno admission rejects a workload image tagged `:latest` or unpinned. The
  published image is
  `ghcr.io/duynhlab/notification-service/notification-service:<tag>` — the
  repository path repeats, and the tag carries no `v` prefix. There is no separate
  migration image; the init container reuses the app image with `args: ["migrate"]`,
  which is why the Dockerfile uses `ENTRYPOINT` rather than `CMD`.
- Metrics leave over OTLP. There is no `/metrics` endpoint and nothing scrapes
  this service.
- Graceful shutdown runs readiness-503 → drain → HTTP → **gRPC** → pool → OTel.
  OTel is last so pending spans, metrics and logs flush.

## API change synchronization

An API change is not done when the code compiles.

- The contract in homelab and this repository move **together** — same change,
  and either the same PR pair or an immediate follow-up. The open mismatch noted
  at the top of this file is what happens when they do not.
- Behaviour that is designed but not deployed is marked **`Planned`** in the
  contract; it is never described as current.
- A material mismatch between the contract and this implementation **blocks the
  release tag** until it is reconciled or explicitly accepted.
