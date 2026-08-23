package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func MigrateUp(ctx context.Context, pool *pgxpool.Pool, directory string) error {
	files, err := migrationFiles(directory)
	if err != nil {
		return err
	}
	for _, file := range files {
		contents, err := os.ReadFile(filepath.Join(directory, file.name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", file.name, err)
		}
		if err := applyMigration(ctx, pool, file.version, string(contents)); err != nil {
			return fmt.Errorf("apply migration %s: %w", file.name, err)
		}
	}
	return nil
}

type migrationFile struct {
	name    string
	version int64
}

func migrationFiles(directory string) ([]migrationFile, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}
	files := make([]migrationFile, 0)
	seen := make(map[int64]string)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			version, err := migrationVersion(entry.Name())
			if err != nil {
				return nil, err
			}
			if existing, duplicate := seen[version]; duplicate {
				return nil, fmt.Errorf("migrations %q and %q use duplicate version %d", existing, entry.Name(), version)
			}
			seen[version] = entry.Name()
			files = append(files, migrationFile{name: entry.Name(), version: version})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	return files, nil
}

func migrationVersion(name string) (int64, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %q must begin with a numeric version and underscore", name)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %q has invalid version", name)
	}
	return version, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version int64, sql string) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(704673)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	var applied bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied)
	if err != nil {
		return err
	}
	if applied {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	// The first migration creates this table. Later migrations reuse it.
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
