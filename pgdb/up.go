package pgdb

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrChecksumMismatch is returned if an already applied migration has changed
// (the file checksum differs from the one recorded in schema_migrations).
var ErrChecksumMismatch = errors.New("pgdb: applied migration changed")

func migrationsTable(schema string) string {
	return pgx.Identifier{schema, "schema_migrations"}.Sanitize()
}

// querier is the common part of pgxpool.Pool and *pgxpool.Conn.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// appliedMigrations reads applied migrations: version -> checksum.
func appliedMigrations(ctx context.Context, q querier, schema string) (map[int]string, error) {
	rows, err := q.Query(ctx, "SELECT version, checksum FROM "+migrationsTable(schema))
	if err != nil {
		return nil, fmt.Errorf("pgdb: read schema_migrations: %w", err)
	}
	defer rows.Close()
	res := make(map[int]string)
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, fmt.Errorf("pgdb: read schema_migrations: %w", err)
		}
		res[v] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgdb: read schema_migrations: %w", err)
	}
	return res, nil
}

// pendingMigrations compares the set with the applied ones and returns the unapplied
// ones in ascending order. Applied versions that are not in the set are ignored.
func pendingMigrations(set []migration, applied map[int]string) ([]migration, error) {
	var res []migration
	for _, m := range set {
		sum, ok := applied[m.version]
		if !ok {
			res = append(res, m)
			continue
		}
		if sum != m.checksum {
			return nil, fmt.Errorf("pgdb: migration %s: %w", m.name, ErrChecksumMismatch)
		}
	}
	return res, nil
}

// Up applies unapplied migrations from the root of fsys (files like 0001_name.sql)
// in schema schema. Forward-only migrations, each in a separate transaction;
// applied ones are recorded in schema.schema_migrations together with the checksum.
//
// Everything runs on one connection under a session advisory lock, so
// concurrent Up calls (multiple instances of the application) are safely serialized.
// The schema is created only if it is absent from pg_namespace: CREATE SCHEMA IF NOT EXISTS
// requires CREATE privilege on the database even if the schema exists, while the production role owns
// the schema but does not have this privilege.
//
// An applied migration with changed content yields ErrChecksumMismatch.
// Applied versions that are not in fsys (DB newer than the image) are ignored.
// An invalid set yields ErrInvalidMigrations before the DB is accessed.
//
// Migration texts must not contain their own BEGIN/COMMIT or
// CREATE INDEX CONCURRENTLY: each runs inside a transaction, and
// search_path in it is set to schema.
func Up(ctx context.Context, pool *pgxpool.Pool, schema string, fsys fs.FS) error {
	if err := ValidSchema(schema); err != nil {
		return err
	}
	set, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pgdb: acquire: %w", err)
	}
	defer conn.Release()

	lockKey := "migrate:" + schema
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", lockKey); err != nil {
		return fmt.Errorf("pgdb: advisory lock: %w", err)
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var ok bool
		uerr := conn.QueryRow(uctx, "SELECT pg_advisory_unlock(hashtext($1))", lockKey).Scan(&ok)
		if uerr != nil || !ok {
			// A session lock dies together with the connection.
			_ = conn.Conn().Close(uctx)
		}
	}()

	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schema).Scan(&exists); err != nil {
		return fmt.Errorf("pgdb: check schema: %w", err)
	}
	if !exists {
		if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
			return fmt.Errorf("pgdb: create schema %q: %w", schema, err)
		}
	}

	table := migrationsTable(schema)
	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+table+
		" (version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return fmt.Errorf("pgdb: create schema_migrations: %w", err)
	}

	applied, err := appliedMigrations(ctx, conn, schema)
	if err != nil {
		return err
	}
	todo, err := pendingMigrations(set, applied)
	if err != nil {
		return err
	}
	for _, m := range todo {
		if err := applyOne(ctx, conn, schema, table, m); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgxpool.Conn, schema, table string, m migration) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("pgdb: migration %s: %w", m.name, err)
	}
	fail := func(err error) error {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rctx)
		return fmt.Errorf("pgdb: migration %s: %w", m.name, err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", schema); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(ctx, m.sql); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO "+table+"(version, name, checksum) VALUES ($1, $2, $3)", m.version, m.name, m.checksum); err != nil {
		return fail(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fail(err)
	}
	return nil
}

// Pending returns the names of unapplied migrations (for example, 0002_users.sql)
// in ascending order; empty if everything is applied. Read-only: it does not take
// an advisory lock and does not create the schema or schema_migrations. Set and
// checksum checks are the same as in Up.
func Pending(ctx context.Context, pool *pgxpool.Pool, schema string, fsys fs.FS) ([]string, error) {
	if err := ValidSchema(schema); err != nil {
		return nil, err
	}
	set, err := loadMigrations(fsys)
	if err != nil {
		return nil, err
	}
	var reg *string
	if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", schema+".schema_migrations").Scan(&reg); err != nil {
		return nil, fmt.Errorf("pgdb: check schema_migrations: %w", err)
	}
	applied := map[int]string{}
	if reg != nil {
		if applied, err = appliedMigrations(ctx, pool, schema); err != nil {
			return nil, err
		}
	}
	todo, err := pendingMigrations(set, applied)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(todo))
	for _, m := range todo {
		names = append(names, m.name)
	}
	return names, nil
}
