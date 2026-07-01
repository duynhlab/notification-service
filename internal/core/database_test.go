package database

import (
	"context"
	"testing"
	"time"

	"github.com/duynhlab/notification-service/config"
)

// TestConnect_ParseError exercises the DSN parse-error path: an invalid
// sslmode makes pgxpool.ParseConfig fail before any network I/O.
func TestConnect_ParseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Host:    "127.0.0.1",
			Port:    "5432",
			Name:    "db",
			User:    "user",
			SSLMode: "bogus",
		},
	}

	pool, err := Connect(context.Background(), cfg)
	if err == nil {
		pool.Close()
		t.Fatal("expected parse error, got nil")
	}
}

// TestConnect_PingError exercises the ping-error path: config parses cleanly
// but the host/port is unreachable, so pool.Ping returns an error.
func TestConnect_PingError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Host:           "127.0.0.1",
			Port:           "1",
			Name:           "db",
			User:           "user",
			SSLMode:        "disable",
			MaxConnections: 25,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := Connect(ctx, cfg)
	if err == nil {
		pool.Close()
		t.Fatal("expected ping error, got nil")
	}
}
