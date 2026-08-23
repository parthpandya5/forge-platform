package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/parthpandya5/forge-platform/internal/auth"
	"github.com/parthpandya5/forge-platform/internal/config"
	"github.com/parthpandya5/forge-platform/internal/controlplane"
	"github.com/parthpandya5/forge-platform/internal/database"
	forgegithub "github.com/parthpandya5/forge-platform/internal/github"
	"github.com/parthpandya5/forge-platform/internal/httpapi"
	"github.com/parthpandya5/forge-platform/internal/logging"
	forgemetrics "github.com/parthpandya5/forge-platform/internal/metrics"
	"github.com/parthpandya5/forge-platform/internal/secrets"
	"github.com/parthpandya5/forge-platform/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.Environment).With("component", "api")
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer startupCancel()
	pool, err := database.Open(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	cipher, err := secrets.New(cfg.EncryptionKey, cfg.Environment)
	if err != nil {
		return fmt.Errorf("initialize secret encryption: %w", err)
	}
	repository := store.New(pool)
	userID := auth.LocalUserID(cfg.LocalUserEmail)
	user, err := repository.EnsureUser(startupCtx, userID, cfg.LocalUserEmail, cfg.LocalUserName)
	if err != nil {
		return fmt.Errorf("ensure local user: %w", err)
	}
	userID = user.ID
	control := controlplane.New(repository, cipher, cfg.JobMaxAttempts)
	registry := prometheus.NewRegistry()
	forgeMetrics := forgemetrics.NewAPI(registry)
	handler := httpapi.NewServer(logger, cfg.CORSOrigin).
		WithControlPlane(control, userID, pool.Ping).
		WithMetrics(forgeMetrics, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	if cfg.GitHubWebhookSecret != "" {
		webhook, err := forgegithub.NewWebhookHandler(cfg.GitHubWebhookSecret, repository, cfg.JobMaxAttempts)
		if err != nil {
			return fmt.Errorf("initialize GitHub webhook: %w", err)
		}
		webhook.WithDeploymentObserver(func(count int) {
			forgeMetrics.Deployments.WithLabelValues("webhook").Add(float64(count))
		})
		handler.WithGitHubWebhook(webhook)
	}

	server := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.APIAddr)
	if err != nil {
		return fmt.Errorf("listen on API address %s: %w", cfg.APIAddr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverResult := make(chan error, 1)
	go func() {
		logger.Info("api listening", "address", cfg.APIAddr)
		serveErr := server.Serve(listener)
		serverResult <- serveErr
		if !errors.Is(serveErr, http.ErrServerClosed) {
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down API server: %w", err)
	}
	serveErr := <-serverResult
	if serveErr == nil {
		return errors.New("API server stopped unexpectedly")
	}
	if !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve API: %w", serveErr)
	}
	logger.Info("api stopped")
	return nil
}
