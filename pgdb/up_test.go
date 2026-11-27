package pgdb_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darmoise/go-kit/pgdb"
	"github.com/darmoise/go-kit/pgdb/pgtest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func mfs(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func threeMigrations() map[string]string {
	return map[string]string{
		"0001_init.sql":  "CREATE TABLE t1 (id int PRIMARY KEY);",
		"0002_more.sql":  "CREATE TABLE t2 (id int); CREATE TABLE t3 (id int);",
		"0003_third.sql": "ALTER TABLE t1 ADD COLUMN note text;",
	}
}

// regclass returns true if the relation exists.
func regclass(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var reg *string
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass($1)::text", name).Scan(&reg); err != nil {
		t.Fatalf("to_regclass(%s): %v", name, err)
	}
	return reg != nil
}

type migRow struct {
	Version   int
	Name      string
	Checksum  string
	AppliedAt time.Time
}

func readMigrations(t *testing.T, pool *pgxpool.Pool, schema string) []migRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		"SELECT version, name, checksum, applied_at FROM "+pgx.Identifier{schema, "schema_migrations"}.Sanitize()+" ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var res []migRow
	for rows.Next() {
		var r migRow
		if err := rows.Scan(&r.Version, &r.Name, &r.Checksum, &r.AppliedAt); err != nil {
			t.Fatal(err)
		}
		res = append(res, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return res
}

func adminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// assertLockFree checks on a separate connection that the migration advisory lock is not held.
func assertLockFree(t *testing.T, admin *pgxpool.Pool, schema string) {
	t.Helper()
	ctx := context.Background()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext('migrate:' || $1))", schema).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("advisory lock for %s is still held", schema)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtext('migrate:' || $1))", schema); err != nil {
		t.Fatal(err)
	}
}

func TestUpApplies(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	ctx := context.Background()
	fsys := mfs(threeMigrations())

	pend, err := pgdb.Pending(ctx, pool, schema, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 3 || pend[0] != "0001_init.sql" || pend[2] != "0003_third.sql" {
		t.Fatalf("Pending before = %v", pend)
	}
	if regclass(t, pool, schema+".schema_migrations") {
		t.Fatal("Pending must not create schema_migrations")
	}
	if err := pgdb.Up(ctx, pool, schema, fsys); err != nil {
		t.Fatal(err)
	}
	for _, tb := range []string{"t1", "t2", "t3"} {
		if !regclass(t, pool, schema+"."+tb) {
			t.Errorf("table %s missing", tb)
		}
	}
	rows := readMigrations(t, pool, schema)
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	for i, r := range rows {
		if r.Version != i+1 || len(r.Checksum) != 64 {
			t.Errorf("row %d = %+v", i, r)
		}
	}
	if rows[1].Name != "0002_more.sql" {
		t.Errorf("name = %s", rows[1].Name)
	}
	pend, err = pgdb.Pending(ctx, pool, schema, fsys)
	if err != nil || len(pend) != 0 {
		t.Fatalf("Pending after = %v, %v", pend, err)
	}
}

func TestUpIdempotentAndIncremental(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	ctx := context.Background()
	files := threeMigrations()
	if err := pgdb.Up(ctx, pool, schema, mfs(files)); err != nil {
		t.Fatal(err)
	}
	before := readMigrations(t, pool, schema)
	if err := pgdb.Up(ctx, pool, schema, mfs(files)); err != nil {
		t.Fatal(err)
	}
	after := readMigrations(t, pool, schema)
	if len(after) != 3 {
		t.Fatalf("rows = %d", len(after))
	}
	for i := range before {
		if !before[i].AppliedAt.Equal(after[i].AppliedAt) {
			t.Errorf("applied_at changed for %d", before[i].Version)
		}
	}
	files["0004_fourth.sql"] = "CREATE TABLE t4 (id int);"
	if err := pgdb.Up(ctx, pool, schema, mfs(files)); err != nil {
		t.Fatal(err)
	}
	rows := readMigrations(t, pool, schema)
	if len(rows) != 4 || rows[3].Name != "0004_fourth.sql" {
		t.Fatalf("rows = %+v", rows)
	}
	if !before[0].AppliedAt.Equal(rows[0].AppliedAt) {
		t.Error("old row changed")
	}
}

func TestUpChecksumMismatch(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	ctx := context.Background()
	files := threeMigrations()
	if err := pgdb.Up(ctx, pool, schema, mfs(files)); err != nil {
		t.Fatal(err)
	}
	files["0002_more.sql"] = "CREATE TABLE secretmark_table (id int);"
	changed := mfs(files)
	err := pgdb.Up(ctx, pool, schema, changed)
	if !errors.Is(err, pgdb.ErrChecksumMismatch) {
		t.Fatalf("Up err = %v", err)
	}
	if !strings.Contains(err.Error(), "0002_more.sql") || strings.Contains(err.Error(), "secretmark") {
		t.Errorf("bad error text: %v", err)
	}
	if _, err := pgdb.Pending(ctx, pool, schema, changed); !errors.Is(err, pgdb.ErrChecksumMismatch) {
		t.Fatalf("Pending err = %v", err)
	}
}

func TestUpDatabaseNewerThanImage(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	ctx := context.Background()
	files := threeMigrations()
	if err := pgdb.Up(ctx, pool, schema, mfs(files)); err != nil {
		t.Fatal(err)
	}
	delete(files, "0003_third.sql")
	if err := pgdb.Up(ctx, pool, schema, mfs(files)); err != nil {
		t.Fatalf("Up with older set: %v", err)
	}
	if n := len(readMigrations(t, pool, schema)); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
	pend, err := pgdb.Pending(ctx, pool, schema, mfs(files))
	if err != nil || len(pend) != 0 {
		t.Errorf("Pending = %v, %v", pend, err)
	}
	files["0001_init.sql"] = "CREATE TABLE other (id int);"
	if err := pgdb.Up(ctx, pool, schema, mfs(files)); !errors.Is(err, pgdb.ErrChecksumMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpMigrationErrorRollsBack(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	admin := adminPool(t)
	ctx := context.Background()
	files := map[string]string{
		"0001_init.sql": "CREATE TABLE t1 (id int);",
		"0002_bad.sql":  "CREATE TABLE a (id int); INSERT INTO no_such_table VALUES (1);",
		"0003_next.sql": "CREATE TABLE t3 (id int);",
	}
	err := pgdb.Up(ctx, pool, schema, mfs(files))
	if err == nil || !strings.Contains(err.Error(), "0002_") {
		t.Fatalf("err = %v", err)
	}
	if regclass(t, pool, schema+".a") {
		t.Error("table a must be rolled back")
	}
	if regclass(t, pool, schema+".t3") {
		t.Error("0003 must not be applied")
	}
	rows := readMigrations(t, pool, schema)
	if len(rows) != 1 || rows[0].Version != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	pend, err := pgdb.Pending(ctx, pool, schema, mfs(files))
	if err != nil || len(pend) != 2 || pend[0] != "0002_bad.sql" {
		t.Fatalf("Pending = %v, %v", pend, err)
	}
	assertLockFree(t, admin, schema)

	files["0002_bad.sql"] = "CREATE TABLE a (id int);"
	if err := pgdb.Up(ctx, pool, schema, mfs(files)); err != nil {
		t.Fatal(err)
	}
	if n := len(readMigrations(t, pool, schema)); n != 3 {
		t.Errorf("rows = %d", n)
	}
}

func TestUpInvalidSetDoesNotTouchDB(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	ctx := context.Background()
	sets := map[string]map[string]string{
		"gap":   {"0001_a.sql": "SELECT 1;", "0003_c.sql": "SELECT 1;"},
		"dup":   {"0001_a.sql": "SELECT 1;", "0001_b.sql": "SELECT 1;"},
		"empty": {},
	}
	for name, s := range sets {
		if err := pgdb.Up(ctx, pool, schema, mfs(s)); !errors.Is(err, pgdb.ErrInvalidMigrations) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if regclass(t, pool, schema+".schema_migrations") {
		t.Error("schema_migrations must not be created")
	}
	assertLockFree(t, adminPool(t), schema)
}

func TestUpParallel(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	ctx := context.Background()
	fsys := mfs(map[string]string{
		"0001_init.sql": "CREATE TABLE counter (n int); SELECT pg_sleep(0.3); INSERT INTO counter VALUES (1);",
		"0002_next.sql": "CREATE TABLE counter2 (n int); INSERT INTO counter2 VALUES (1);",
	})
	runParallel(t, 6, func() error { return pgdb.Up(ctx, pool, schema, fsys) })
	if n := len(readMigrations(t, pool, schema)); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
	for _, tb := range []string{"counter", "counter2"} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{schema, tb}.Sanitize()).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s rows = %d, want 1", tb, n)
		}
	}
}

func runParallel(t *testing.T, n int, fn func() error) {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn()
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

func TestUpParallelCreatesSchema(t *testing.T) {
	t.Parallel()
	url := pgtest.URL(t)
	ctx := context.Background()
	schema := pgtest.Name("t_")
	pool, err := pgdb.Open(ctx, url, schema, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dcancel()
		if _, err := pool.Exec(dctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop schema: %v", err)
		}
		pool.Close()
	})
	fsys := mfs(map[string]string{
		"0001_init.sql": "CREATE TABLE x (id int); SELECT pg_sleep(0.2);",
		"0002_next.sql": "CREATE TABLE y (id int);",
	})
	runParallel(t, 4, func() error { return pgdb.Up(ctx, pool, schema, fsys) })
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schema).Scan(&exists); err != nil || !exists {
		t.Fatalf("schema exists = %v, %v", exists, err)
	}
	if n := len(readMigrations(t, pool, schema)); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func TestUpReleasesLock(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	admin := adminPool(t)
	ctx := context.Background()
	fsys := mfs(threeMigrations())
	if err := pgdb.Up(ctx, pool, schema, fsys); err != nil {
		t.Fatal(err)
	}
	assertLockFree(t, admin, schema)

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := pgdb.Up(cctx, pool, schema, fsys); err == nil {
		t.Fatal("Up with cancelled context: want error")
	}
	assertLockFree(t, admin, schema)
}

func TestUpBarePoolSearchPath(t *testing.T) {
	t.Parallel()
	url := pgtest.URL(t)
	_, schema := pgtest.Pool(t)
	ctx := context.Background()
	bare, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	tbl := "bare_" + schema
	fsys := mfs(map[string]string{"0001_init.sql": "CREATE TABLE " + tbl + " (id int);"})
	if err := pgdb.Up(ctx, bare, schema, fsys); err != nil {
		t.Fatal(err)
	}
	if !regclass(t, bare, schema+"."+tbl) {
		t.Error("table not in service schema")
	}
	if regclass(t, bare, "public."+tbl) {
		t.Error("table leaked into public")
	}
}

// newRestrictedRole creates a role without CREATE on the database and returns a URL for it.
func newRestrictedRole(t *testing.T, admin *pgxpool.Pool) (roleURL, role string) {
	t.Helper()
	ctx := context.Background()
	role = pgtest.Name("pgdb_t_")
	rq := pgx.Identifier{role}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE ROLE "+rq+" LOGIN"); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "42501" {
			t.Skip("TEST_DATABASE_URL user cannot create roles")
		}
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP ROLE IF EXISTS "+rq); err != nil {
			t.Errorf("drop role %s: %v", role, err)
		}
	})
	var has bool
	if err := admin.QueryRow(ctx, "SELECT has_database_privilege($1, current_database(), 'CREATE')", role).Scan(&has); err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("role unexpectedly has CREATE on database; test is not conclusive")
	}
	u, err := url.Parse(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.User(role)
	return u.String(), role
}

func TestUpNoCreateExistingSchema(t *testing.T) {
	t.Parallel()
	admin := adminPool(t)
	ctx := context.Background()
	roleURL, role := newRestrictedRole(t, admin)
	s := pgtest.Name("t_")
	sq := pgx.Identifier{s}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+sq+" AUTHORIZATION "+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+sq+" CASCADE"); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	rolePool, err := pgdb.Open(ctx, roleURL, s, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rolePool.Close)

	_, err = rolePool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+sq)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "42501" {
		t.Fatalf("CREATE SCHEMA IF NOT EXISTS under role: err = %v; the bug is not reproduced, the test is not conclusive", err)
	}

	fsys := mfs(map[string]string{
		"0001_init.sql": "CREATE TABLE a (id int);",
		"0002_next.sql": "CREATE TABLE b (id int);",
	})
	if err := pgdb.Up(ctx, rolePool, s, fsys); err != nil {
		t.Fatalf("Up under restricted role: %v", err)
	}
	for _, tb := range []string{"a", "b"} {
		if !regclass(t, rolePool, s+"."+tb) {
			t.Errorf("table %s missing", tb)
		}
	}
	if n := len(readMigrations(t, rolePool, s)); n != 2 {
		t.Errorf("rows = %d", n)
	}
	if err := pgdb.Up(ctx, rolePool, s, fsys); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	pend, err := pgdb.Pending(ctx, rolePool, s, fsys)
	if err != nil || len(pend) != 0 {
		t.Fatalf("Pending = %v, %v", pend, err)
	}
}

func TestUpNoCreateMissingSchema(t *testing.T) {
	t.Parallel()
	admin := adminPool(t)
	ctx := context.Background()
	roleURL, _ := newRestrictedRole(t, admin)
	s2 := pgtest.Name("t_")
	rolePool, err := pgdb.Open(ctx, roleURL, s2, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rolePool.Close)
	err = pgdb.Up(ctx, rolePool, s2, mfs(map[string]string{"0001_init.sql": "CREATE TABLE a (id int);"}))
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "42501" {
		t.Fatalf("err = %v, want PgError 42501", err)
	}
	if !strings.Contains(err.Error(), "create schema") || !strings.Contains(err.Error(), s2) {
		t.Errorf("error text: %v", err)
	}
	assertLockFree(t, admin, s2)
}

func TestUpCancelDuringMigrationReleasesLock(t *testing.T) {
	t.Parallel()
	pool, schema := pgtest.Pool(t)
	admin := adminPool(t)
	fsys := mfs(map[string]string{"0001_slow.sql": "CREATE TABLE s (id int); SELECT pg_sleep(1);"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := pgdb.Up(ctx, pool, schema, fsys); err == nil {
		t.Fatal("Up cancelled mid-migration: want error")
	}
	assertLockFree(t, admin, schema)
}
