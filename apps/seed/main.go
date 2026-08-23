package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/auth"
	"github.com/parthpandya5/forge-platform/internal/config"
	"github.com/parthpandya5/forge-platform/internal/database"
	"github.com/parthpandya5/forge-platform/internal/domain"
	"github.com/parthpandya5/forge-platform/internal/logging"
	"github.com/parthpandya5/forge-platform/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.Environment).With("component", "seed")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	repository := store.New(pool)
	userID := auth.LocalUserID(cfg.LocalUserEmail)
	user, err := repository.EnsureUser(ctx, userID, cfg.LocalUserEmail, cfg.LocalUserName)
	if err != nil {
		return fmt.Errorf("ensure local user: %w", err)
	}
	userID = user.ID

	project, err := ensureProject(ctx, repository, userID)
	if err != nil {
		return fmt.Errorf("seed project: %w", err)
	}
	service, err := ensureService(ctx, repository, userID, project)
	if err != nil {
		return fmt.Errorf("seed service: %w", err)
	}
	logger.Info("seed complete", "project_id", project.ID, "service_id", service.ID)
	return nil
}

func ensureProject(ctx context.Context, repository *store.Store, userID uuid.UUID) (domain.Project, error) {
	input := domain.CreateProjectInput{
		Name: "Forge Demo", Slug: "forge-demo", RepositoryURL: "https://github.com/traefik/whoami",
		RepositoryBranch: "master",
	}
	if err := input.NormalizeAndValidate(); err != nil {
		return domain.Project{}, err
	}
	project, err := repository.CreateProject(ctx, userID, input)
	if err == nil {
		return project, nil
	}
	if !errors.Is(err, store.ErrConflict) {
		return domain.Project{}, err
	}
	projects, err := repository.ListProjects(ctx, userID, 100, 0)
	if err != nil {
		return domain.Project{}, err
	}
	for _, existing := range projects {
		if existing.Slug == input.Slug {
			return existing, nil
		}
	}
	return domain.Project{}, store.ErrConflict
}

func ensureService(ctx context.Context, repository *store.Store, userID uuid.UUID, project domain.Project) (domain.Service, error) {
	input := domain.CreateServiceInput{Name: "web", InternalPort: 80, DesiredReplicas: 1, HealthCheckPath: "/"}
	if err := input.NormalizeAndValidate(); err != nil {
		return domain.Service{}, err
	}
	service, err := repository.CreateService(ctx, userID, project.ID, input)
	if err == nil {
		return service, nil
	}
	if !errors.Is(err, store.ErrConflict) {
		return domain.Service{}, err
	}
	services, err := repository.ListServices(ctx, userID, project.ID)
	if err != nil {
		return domain.Service{}, err
	}
	for _, existing := range services {
		if existing.Name == input.Name {
			return existing, nil
		}
	}
	return domain.Service{}, store.ErrConflict
}
