package pgdb

import (
	"errors"
	"testing"
	"testing/fstest"
)

func TestLoadMigrations(t *testing.T) {
	t.Parallel()
	f := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	tests := []struct {
		name string
		fs   fstest.MapFS
		ok   bool
	}{
		{"valid", fstest.MapFS{"0001_a.sql": f("a"), "0002_b.sql": f("b")}, true},
		{"gap", fstest.MapFS{"0001_a.sql": f("a"), "0003_b.sql": f("b")}, false},
		{"duplicate", fstest.MapFS{"0001_a.sql": f("a"), "0002_a.sql": f("a"), "0002_b.sql": f("b")}, false},
		{"not from one", fstest.MapFS{"0002_a.sql": f("a")}, false},
		{"zero", fstest.MapFS{"0000_a.sql": f("a")}, false},
		{"bad name short", fstest.MapFS{"1_init.sql": f("a")}, false},
		{"bad name upper", fstest.MapFS{"0001_Init.sql": f("a")}, false},
		{"empty", fstest.MapFS{}, false},
		{"only non sql", fstest.MapFS{"README.md": f("x")}, false},
		{"ignores non sql and dirs", fstest.MapFS{"0001_a.sql": f("a"), "README.md": f("x"), "sub/0009_x.sql": f("x")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadMigrations(tt.fs)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidMigrations) {
				t.Fatalf("err = %v, want ErrInvalidMigrations", err)
			}
		})
	}
}

func TestLoadMigrationsSortedAndChecksum(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"0003_c.sql": {Data: []byte("c")},
		"0001_a.sql": {Data: []byte("a")},
		"0002_b.sql": {Data: []byte("b")},
	}
	got, err := loadMigrations(fsys)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range got {
		if m.version != i+1 {
			t.Errorf("index %d: version %d", i, m.version)
		}
		if len(m.checksum) != 64 {
			t.Errorf("checksum length %d", len(m.checksum))
		}
	}
	// sha256("a")
	if got[0].checksum != "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb" {
		t.Errorf("checksum = %s", got[0].checksum)
	}
}
