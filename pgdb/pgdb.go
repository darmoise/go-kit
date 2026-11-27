// Package pgdb provides a Postgres connection pool (pgx/v5) with a dedicated application schema
// and a forward-only SQL migration runner.
package pgdb

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var schemaRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ValidSchema checks a schema name: lowercase Latin letters, digits and `_`,
// up to 63 characters, the first character is not a digit, the `pg_` prefix is reserved by Postgres.
func ValidSchema(name string) error {
	if !schemaRe.MatchString(name) {
		return fmt.Errorf("pgdb: invalid schema name %q: must match %s", name, schemaRe.String())
	}
	if strings.HasPrefix(name, "pg_") {
		return fmt.Errorf("pgdb: invalid schema name %q: prefix pg_ is reserved", name)
	}
	return nil
}

// Open creates a connection pool and checks that the DB is reachable (Ping).
//
// The search_path of each connection is set to schema
// (overriding any value from url). This is deliberate isolation:
// search_path contains only the application schema, objects from public must be
// qualified explicitly. maxConns <= 0 leaves the pgx default.
//
// Errors do not contain url (it may contain a password).
func Open(ctx context.Context, url, schema string, maxConns int) (*pgxpool.Pool, error) {
	if err := ValidSchema(schema); err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("pgdb: invalid database url")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgdb: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgdb: ping: %w", err)
	}
	return pool, nil
}
