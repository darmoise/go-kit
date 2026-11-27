// Package pgtest provides helpers for Postgres integration tests: an isolated
// schema per test, based on the TEST_DATABASE_URL environment variable.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/darmoise/go-kit/pgdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// URL returns TEST_DATABASE_URL; if the variable is not set, it skips the test.
func URL(t testing.TB) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return u
}

// Name returns prefix + 16 random hex characters (for schemas and roles).
// The caller is responsible for the validity of prefix.
func Name(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("pgtest: crypto/rand: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

// Pool creates a new empty schema t_<rand> and a pool (up to 4 connections) with
// search_path set to it. Migrations are not applied. The schema and pool are dropped in Cleanup.
func Pool(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	url := URL(t)
	schema := Name("t_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgdb.Open(ctx, url, schema, 4)
	if err != nil {
		t.Fatalf("pgtest: open: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ccancel()
		if _, err := pool.Exec(cctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("pgtest: drop schema %s: %v", schema, err)
		}
		pool.Close()
	})
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatalf("pgtest: create schema: %v", err)
	}
	return pool, schema
}
