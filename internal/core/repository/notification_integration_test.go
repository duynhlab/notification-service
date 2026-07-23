//go:build integration

// Integration tests for the PostgreSQL NotificationRepository. They run a real
// Postgres via testcontainers-go and apply the service's migrations, so they
// exercise the actual SQL (not a mock). Run with:
//
//	go test -tags=integration ./internal/core/repository/...
//
// Requires a reachable Docker daemon. Excluded from the default `go test ./...`
// unit run by the `integration` build tag.
package repository

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/duynhlab/notification-service/internal/core/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("notification"),
		postgres.WithUsername("notification"),
		postgres.WithPassword("secret"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	applyMigrations(t, ctx, dsn)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// applyMigrations runs every db/migrations/sql/*.up.sql in lexical order using a
// simple-protocol connection (so multi-statement files execute in one round).
func applyMigrations(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect for migrations: %v", err)
	}
	defer conn.Close(ctx)

	dir := filepath.Join("..", "..", "..", "db", "migrations", "sql")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var files []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && len(n) > 7 && n[len(n)-7:] == ".up.sql" {
			files = append(files, n)
		}
	}
	sort.Strings(files)
	for _, f := range files {
		sqlBytes, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %s: %v", f, err)
		}
	}
}

func TestNotificationRepository_Integration(t *testing.T) {
	pool := newTestDB(t)
	repo := NewNotificationRepository(pool)
	ctx := context.Background()
	const userID = 999 // not present in the seed data

	var createdID int

	t.Run("Create assigns an id and defaults", func(t *testing.T) {
		n := &domain.Notification{Type: "email", Title: "Hi", Message: "Body"}
		if err := repo.Create(ctx, n, userID); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if n.ID == "" {
			t.Fatal("Create did not set the notification ID")
		}
		if n.Read {
			t.Error("new notification should be unread")
		}
		var err error
		if createdID, err = strconv.Atoi(n.ID); err != nil {
			t.Fatalf("notification ID %q is not numeric: %v", n.ID, err)
		}
	})

	t.Run("CreateWithDeliveryKey deduplicates on the key", func(t *testing.T) {
		// Own user so the rows don't skew the count/list assertions below,
		// which assume userID has exactly one notification (998 is bulkUser).
		const userID = 997
		const key = "order:42:type:order_confirmed:version:1"
		first := &domain.Notification{Type: "email", Title: "Confirmed", Message: "Order 42"}
		replayed, err := repo.CreateWithDeliveryKey(ctx, first, userID, key)
		if err != nil {
			t.Fatalf("CreateWithDeliveryKey: %v", err)
		}
		if replayed {
			t.Fatal("first send must not be a replay")
		}

		second := &domain.Notification{Type: "email", Title: "Confirmed", Message: "Order 42"}
		replayed, err = repo.CreateWithDeliveryKey(ctx, second, userID, key)
		if err != nil {
			t.Fatalf("CreateWithDeliveryKey retry: %v", err)
		}
		if !replayed {
			t.Fatal("retry with the same key must replay")
		}
		if second.ID != first.ID {
			t.Fatalf("replay returned id %q, want original %q", second.ID, first.ID)
		}

		var count int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM notifications WHERE delivery_key = $1`, key).Scan(&count); err != nil {
			t.Fatalf("count by delivery_key: %v", err)
		}
		if count != 1 {
			t.Fatalf("rows with key = %d, want exactly 1", count)
		}

		third := &domain.Notification{Type: "email", Title: "Receipt", Message: "Order 42"}
		replayed, err = repo.CreateWithDeliveryKey(ctx, third, userID, "order:42:type:receipt:version:1")
		if err != nil {
			t.Fatalf("CreateWithDeliveryKey distinct key: %v", err)
		}
		if replayed || third.ID == first.ID {
			t.Fatalf("distinct key must insert a new row (replayed=%v id=%q)", replayed, third.ID)
		}
	})

	t.Run("FindByID returns the created row", func(t *testing.T) {
		got, err := repo.FindByID(ctx, createdID, userID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if got == nil {
			t.Fatal("FindByID returned nil for an existing row")
		}
		if got.Title != "Hi" || got.Message != "Body" || got.Type != "email" {
			t.Errorf("row = %+v, want title=Hi message=Body type=email", got)
		}
	})

	t.Run("counts reflect one unread notification", func(t *testing.T) {
		total, err := repo.CountByUserID(ctx, userID)
		if err != nil || total != 1 {
			t.Fatalf("CountByUserID = (%d, %v), want (1, nil)", total, err)
		}
		unread, err := repo.CountUnreadByUserID(ctx, userID)
		if err != nil || unread != 1 {
			t.Fatalf("CountUnreadByUserID = (%d, %v), want (1, nil)", unread, err)
		}
	})

	t.Run("MarkAsRead flips the flag", func(t *testing.T) {
		ok, err := repo.MarkAsRead(ctx, createdID, userID)
		if err != nil || !ok {
			t.Fatalf("MarkAsRead = (%v, %v), want (true, nil)", ok, err)
		}
		unread, err := repo.CountUnreadByUserID(ctx, userID)
		if err != nil || unread != 0 {
			t.Fatalf("CountUnreadByUserID after read = (%d, %v), want (0, nil)", unread, err)
		}
	})

	t.Run("ListByUserID returns the row", func(t *testing.T) {
		list, err := repo.ListByUserID(ctx, userID, 10, 0)
		if err != nil {
			t.Fatalf("ListByUserID: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("ListByUserID len = %d, want 1", len(list))
		}
	})

	t.Run("FindByID for a missing row returns nil, nil", func(t *testing.T) {
		got, err := repo.FindByID(ctx, 999999, userID)
		if err != nil {
			t.Fatalf("FindByID(missing) err = %v, want nil", err)
		}
		if got != nil {
			t.Errorf("FindByID(missing) = %+v, want nil", got)
		}
	})

	t.Run("MarkAsRead on a missing row reports not-found", func(t *testing.T) {
		ok, err := repo.MarkAsRead(ctx, 999999, userID)
		if err != nil {
			t.Fatalf("MarkAsRead(missing) err = %v, want nil", err)
		}
		if ok {
			t.Error("MarkAsRead(missing) = true, want false")
		}
	})

	t.Run("MarkAllByUserID flips every unread row and is idempotent", func(t *testing.T) {
		const bulkUser = 998 // isolated from the userID rows above
		for i := 0; i < 3; i++ {
			if err := repo.Create(ctx, &domain.Notification{Message: "bulk"}, bulkUser); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}

		marked, err := repo.MarkAllByUserID(ctx, bulkUser)
		if err != nil || marked != 3 {
			t.Fatalf("MarkAllByUserID = (%d, %v), want (3, nil)", marked, err)
		}
		unread, err := repo.CountUnreadByUserID(ctx, bulkUser)
		if err != nil || unread != 0 {
			t.Fatalf("CountUnreadByUserID after mark-all = (%d, %v), want (0, nil)", unread, err)
		}

		// Idempotent: a second sweep finds nothing to flip.
		again, err := repo.MarkAllByUserID(ctx, bulkUser)
		if err != nil || again != 0 {
			t.Fatalf("MarkAllByUserID (2nd) = (%d, %v), want (0, nil)", again, err)
		}
	})
}
