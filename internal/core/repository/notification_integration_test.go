//go:build integration

// Integration tests for the PostgreSQL NotificationRepository. They run a real
// Postgres via testcontainers-go, migrate and seed it as the migrator, and run
// the repository as the runtime login, so they exercise the actual SQL and
// grants (not a mock). Run with:
//
//	go test -tags=integration ./internal/core/repository/...
//
// Requires a reachable Docker daemon. Excluded from the default `go test ./...`
// unit run by the `integration` build tag.
package repository

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/duynhlab/notification-service/db/migrations"
	"github.com/duynhlab/notification-service/db/seed"
	"github.com/duynhlab/notification-service/internal/core/domain"
	"github.com/duynhlab/pkg/migratex"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// testDB holds the three logins of the platform's database shape: a superuser
// that plays the platform (creates the roles, as CNPG does), the migrator, and
// the runtime pool the repository runs on.
type testDB struct {
	adminDSN    string
	migratorDSN string
	runtimeDSN  string
	runtime     *pgxpool.Pool
}

const ownerRole = "notification_owner"

// startWithRoles starts a throwaway Postgres and creates notification_owner /
// notification_migrator / notification_runtime the way the platform does.
// Everything is torn down via t.Cleanup.
func startWithRoles(t *testing.T) *testDB {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("notification"),
		postgres.WithUsername("platform"),
		postgres.WithPassword("secret"),
		// Ready twice (initdb restarts the server once), then the published
		// port: the module's own strategy, so a test never races the restart.
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	adminDSN, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db := &testDB{
		adminDSN:    adminDSN,
		migratorDSN: withUser(t, adminDSN, "notification_migrator", "migrator"),
		runtimeDSN:  withUser(t, adminDSN, "notification_runtime", "runtime"),
	}

	execAll(t, ctx, adminDSN,
		`CREATE ROLE notification_owner NOLOGIN`,
		`CREATE ROLE notification_migrator LOGIN NOINHERIT PASSWORD 'migrator'`,
		`CREATE ROLE notification_runtime LOGIN PASSWORD 'runtime'`,
		`GRANT notification_owner TO notification_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE`,
		// The platform makes the owner own the database; on PG15+ that is what
		// gives it CREATE on the public schema (owned by pg_database_owner).
		`ALTER DATABASE notification OWNER TO notification_owner`,
	)
	return db
}

// newBareDB is newTestDB without migrations or seed; it returns the
// migrator's DSN.
func newBareDB(t *testing.T) string {
	t.Helper()
	return startWithRoles(t).migratorDSN
}

// newTestDB starts a throwaway Postgres with the three roles, migrates and
// seeds as the migrator after SET ROLE notification_owner, and returns it with
// a pool connected as notification_runtime.
func newTestDB(t *testing.T) *testDB {
	t.Helper()
	ctx := context.Background()
	db := startWithRoles(t)

	if err := migratex.Run(migrations.FS, "sql", db.migratorDSN, migratex.WithSetRole(ownerRole)); err != nil {
		t.Fatalf("migrate as the migrator: %v", err)
	}
	if err := seed.Apply(ctx, db.migratorDSN, ownerRole); err != nil {
		t.Fatalf("seed as the migrator: %v", err)
	}

	pool, err := pgxpool.New(ctx, db.runtimeDSN)
	if err != nil {
		t.Fatalf("new runtime pool: %v", err)
	}
	t.Cleanup(pool.Close)
	db.runtime = pool
	return db
}

func withUser(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

func execAll(t *testing.T, ctx context.Context, dsn string, stmts ...string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, stmt := range stmts {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func TestNotificationRepository_Integration(t *testing.T) {
	db := newTestDB(t)
	repo := NewNotificationRepository(db.runtime)
	ctx := context.Background()
	// userID is an opaque OIDC token subject (ADR-042), not present in the seed data.
	const userID = "17e57000-0000-4000-8000-000000000999"

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
		// which assume userID has exactly one notification (…998 is bulkUser).
		const userID = "17e57000-0000-4000-8000-000000000997"
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
		if err := db.runtime.QueryRow(ctx,
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
		const bulkUser = "17e57000-0000-4000-8000-000000000998" // isolated from the userID rows above
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

// The authorization contract, checked as the real logins: the owner owns every
// object, the runtime can serve traffic and nothing more, and the migration
// refuses to run without its role.
func TestAuthorization_Integration(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	t.Run("every relation belongs to notification_owner", func(t *testing.T) {
		// Extension members are owned by whoever ran CREATE EXTENSION; skip
		// them so a future extension does not read as a leak.
		rows, err := db.runtime.Query(ctx, `
			SELECT c.relname || ':' || pg_get_userbyid(c.relowner)
			  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE n.nspname = 'public' AND pg_get_userbyid(c.relowner) <> 'notification_owner'
			   AND NOT EXISTS (SELECT 1 FROM pg_depend d
			                    WHERE d.classid = 'pg_class'::regclass
			                      AND d.objid = c.oid AND d.deptype = 'e')`)
		if err != nil {
			t.Fatalf("query owners: %v", err)
		}
		others, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("scan owners: %v", err)
		}
		if len(others) != 0 {
			t.Fatalf("relations not owned by notification_owner: %v", others)
		}
	})

	t.Run("the runtime cannot change the schema or reach the migration table", func(t *testing.T) {
		for _, stmt := range []string{
			`CREATE TABLE public.evil (i int)`,
			`ALTER TABLE public.notifications ADD COLUMN evil int`,
			`DROP TABLE public.notifications`,
			`SELECT version FROM public.schema_migrations`,
			`SET ROLE notification_owner`,
		} {
			_, err := db.runtime.Exec(ctx, stmt)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || (pgErr.Code != "42501" && pgErr.Code != "42704") {
				t.Errorf("%s as notification_runtime: got %v, want permission denied", stmt, err)
			}
		}
		// Without GRANT OPTION, PostgreSQL only warns "no privileges were
		// granted"; the assertion is the effect, not an error code.
		if _, err := db.runtime.Exec(ctx, `GRANT SELECT ON public.notifications TO PUBLIC`); err != nil {
			t.Fatalf("grant attempt: %v", err)
		}
		var leaked bool
		if err := db.runtime.QueryRow(ctx,
			`SELECT has_table_privilege('public', 'public.notifications', 'SELECT')`).Scan(&leaked); err != nil {
			t.Fatalf("check PUBLIC access: %v", err)
		}
		if leaked {
			t.Fatal("notification_runtime handed SELECT on notifications to PUBLIC")
		}
	})

	t.Run("migrate and seed refuse an empty DB_MIGRATION_ROLE", func(t *testing.T) {
		if err := migratex.Run(migrations.FS, "sql", db.migratorDSN, migratex.WithSetRole("")); err == nil {
			t.Fatal("migrate with an empty role succeeded")
		}
		if err := seed.Apply(ctx, db.migratorDSN, ""); err == nil {
			t.Fatal("seed with an empty role succeeded")
		}
	})

	t.Run("the migrator creates nothing as itself", func(t *testing.T) {
		// No SET ROLE at all: the NOINHERIT migrator has no right on the
		// owner's schema, so even golang-migrate's version table is refused.
		fresh := newBareDB(t)
		err := migratex.Run(migrations.FS, "sql", fresh)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("migrate without SET ROLE = %v, want permission denied", err)
		}
	})

	t.Run("seed as a role the login cannot switch to fails", func(t *testing.T) {
		err := seed.Apply(ctx, db.runtimeDSN, ownerRole)
		if err == nil || !strings.Contains(err.Error(), "SET ROLE") {
			t.Fatalf("seed as notification_runtime = %v, want SET ROLE error", err)
		}
	})
}
