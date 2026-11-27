package pgdb_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/darmoise/go-kit/pgdb"
	"github.com/darmoise/go-kit/pgdb/pgtest"
)

func TestValidSchema(t *testing.T) {
	t.Parallel()
	good := []string{"auth", "_x", "a1_b", strings.Repeat("a", 63)}
	for _, s := range good {
		if err := pgdb.ValidSchema(s); err != nil {
			t.Errorf("ValidSchema(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{"", strings.Repeat("a", 64), "1a", "A", "a-b", "a b", "a;b", `"x"`, "pg_catalog", "pg_x"}
	for _, s := range bad {
		if err := pgdb.ValidSchema(s); err == nil {
			t.Errorf("ValidSchema(%q) = nil, want error", s)
		}
	}
}

func TestOpenInvalidSchema(t *testing.T) {
	t.Parallel()
	if _, err := pgdb.Open(context.Background(), "postgres://127.0.0.1:1/db", "Bad-Name", 1); err == nil {
		t.Fatal("want error")
	}
}

func TestOpenInvalidURL(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"://bad", "postgres://u:s3cr3t-pw@127.0.0.1:1/db?sslmode=bogus"} {
		_, err := pgdb.Open(context.Background(), u, "demo", 1)
		if err == nil {
			t.Fatalf("Open(%q): want error", u)
		}
		if strings.Contains(err.Error(), "s3cr3t-pw") {
			t.Errorf("error leaks password: %v", err)
		}
	}
}

func TestOpenUnreachable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := pgdb.Open(ctx, "postgres://u:s3cr3t-pw@127.0.0.1:1/db?sslmode=disable", "demo", 1)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "s3cr3t-pw") {
		t.Errorf("error leaks password: %v", err)
	}
}

func TestOpenWorks(t *testing.T) {
	t.Parallel()
	url := pgtest.URL(t)
	s := pgtest.Name("t_")
	ctx := context.Background()
	pool, err := pgdb.Open(ctx, url, s, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if got := pool.Config().MaxConns; got != 3 {
		t.Errorf("MaxConns = %d, want 3", got)
	}
	var sp string
	if err := pool.QueryRow(ctx, "SHOW search_path").Scan(&sp); err != nil {
		t.Fatal(err)
	}
	if sp != s {
		t.Errorf("search_path = %q, want %s", sp, s)
	}
}
