package pgtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/darmoise/go-kit/pgdb/pgtest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestName(t *testing.T) {
	t.Parallel()
	a, b := pgtest.Name("t_"), pgtest.Name("t_")
	if !strings.HasPrefix(a, "t_") || len(a) != len("t_")+16 {
		t.Errorf("Name = %q", a)
	}
	if a == b {
		t.Error("names must differ")
	}
}

func TestPool(t *testing.T) {
	url := pgtest.URL(t)
	ctx := context.Background()
	check, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()

	var schema string
	t.Run("inner", func(t *testing.T) {
		pool, s := pgtest.Pool(t)
		schema = s
		var exists bool
		if err := check.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", s).Scan(&exists); err != nil || !exists {
			t.Fatalf("schema exists = %v, %v", exists, err)
		}
		var sp string
		if err := pool.QueryRow(ctx, "SHOW search_path").Scan(&sp); err != nil || sp != s {
			t.Fatalf("search_path = %q, %v", sp, err)
		}
		if _, err := pool.Exec(ctx, "CREATE TABLE x (id int)"); err != nil {
			t.Fatal(err)
		}
	})
	var exists bool
	if err := check.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schema).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("schema must be dropped after subtest")
	}
}
