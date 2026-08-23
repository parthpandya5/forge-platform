package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRejectsUndersizedPool(t *testing.T) {
	if _, err := OpenWithMaxConnections(context.Background(), "postgres://unused", 1); err == nil {
		t.Fatal("OpenWithMaxConnections() error = nil")
	}
}

func TestMigrationVersion(t *testing.T) {
	version, err := migrationVersion("000042_add_widget.up.sql")
	if err != nil {
		t.Fatalf("migrationVersion() error = %v", err)
	}
	if version != 42 {
		t.Fatalf("version = %d, want 42", version)
	}
}

func TestMigrationVersionRejectsInvalid(t *testing.T) {
	for _, name := range []string{"migration.sql", "nope_test.up.sql", "000000_initial.up.sql"} {
		if _, err := migrationVersion(name); err == nil {
			t.Errorf("migrationVersion(%q) error = nil", name)
		}
	}
}

func TestMigrationFilesSortNumericallyAndRejectDuplicates(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"10_ten.up.sql", "2_two.up.sql", "README.md"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("SELECT 1;"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := migrationFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].version != 2 || files[1].version != 10 {
		t.Fatalf("migration files = %+v", files)
	}
	if err := os.WriteFile(filepath.Join(directory, "02_duplicate.up.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationFiles(directory); err == nil {
		t.Fatal("migrationFiles() accepted duplicate versions")
	}
}
