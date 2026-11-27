package pgdb

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ErrInvalidMigrations is returned if the migration set is invalid: empty,
// contains a file with a wrong name, a duplicate or a gap in versions.
var ErrInvalidMigrations = errors.New("pgdb: invalid migration set")

var migrationNameRe = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

// loadMigrations parses the root of fsys into an ordered set of migrations.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("%w: read dir: %v", ErrInvalidMigrations, err)
	}
	var list []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := migrationNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("%w: bad file name %q", ErrInvalidMigrations, e.Name())
		}
		ver, _ := strconv.Atoi(m[1])
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidMigrations, e.Name(), err)
		}
		sum := sha256.Sum256(data)
		list = append(list, migration{
			version:  ver,
			name:     e.Name(),
			sql:      string(data),
			checksum: hex.EncodeToString(sum[:]),
		})
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("%w: no migrations found", ErrInvalidMigrations)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	for i, m := range list {
		want := i + 1
		if m.version == want {
			continue
		}
		if i > 0 && m.version == list[i-1].version {
			return nil, fmt.Errorf("%w: duplicate version %04d (%s)", ErrInvalidMigrations, m.version, m.name)
		}
		return nil, fmt.Errorf("%w: expected version %04d, got %s", ErrInvalidMigrations, want, m.name)
	}
	return list, nil
}
