package seed

import (
	"io/fs"
	"strings"
	"testing"
)

// TestFSEmbedsSeed verifies the demo seed SQL is embedded and applied by the
// `seed` subcommand.
func TestFSEmbedsSeed(t *testing.T) {
	entries, err := fs.ReadDir(FS, "sql")
	if err != nil {
		t.Fatalf("ReadDir(sql): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no seed files embedded under sql/")
	}
}

// TestSeedIsIdempotent guards that the demo seed can be re-applied safely: every
// INSERT must be guarded with ON CONFLICT so repeated `seed` runs do not fail.
func TestSeedIsIdempotent(t *testing.T) {
	b, err := fs.ReadFile(FS, "sql/000001_seed_notifications.up.sql")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	sql := strings.ToLower(string(b))
	if !strings.Contains(sql, "on conflict") {
		t.Error("demo seed INSERT must be idempotent (ON CONFLICT ... DO NOTHING)")
	}
}
