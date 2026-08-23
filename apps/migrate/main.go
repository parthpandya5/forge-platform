package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/parthpandya5/forge-platform/internal/config"
	"github.com/parthpandya5/forge-platform/internal/database"
	"github.com/parthpandya5/forge-platform/internal/logging"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.Environment).With("component", "migrate")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	if err := database.MigrateUp(ctx, pool, cfg.MigrationsPath); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	logger.Info("migrations applied")
	return nil
}
