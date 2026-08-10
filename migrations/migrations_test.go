package migrations

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestDiscoverSortsMigrationsByNumericVersion(t *testing.T) {
	source := fstest.MapFS{
		"010_tenth.sql":  {Data: []byte("SELECT 10")},
		"002_second.sql": {Data: []byte("SELECT 2")},
		"notes.txt":      {Data: []byte("ignored")},
	}

	got, err := discover(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].version != 2 || got[1].version != 10 {
		t.Fatalf("unexpected migration order: %#v", got)
	}
	if got[0].checksum == "" {
		t.Fatal("checksum was not calculated")
	}
}

func TestDiscoverRejectsDuplicateVersions(t *testing.T) {
	source := fstest.MapFS{
		"001_first.sql": {Data: []byte("SELECT 1")},
		"001_again.sql": {Data: []byte("SELECT 2")},
	}

	_, err := discover(source)
	if err == nil || !strings.Contains(err.Error(), "duplicate migration version") {
		t.Fatalf("expected duplicate version error, got %v", err)
	}
}

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	got, err := discover(files)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].version != 1 {
		t.Fatalf("first migration version is %d, want 1", got[0].version)
	}
}
