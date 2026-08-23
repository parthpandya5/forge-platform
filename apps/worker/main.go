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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/parthpandya5/forge-platform/internal/build"
	"github.com/parthpandya5/forge-platform/internal/config"
	"github.com/parthpandya5/forge-platform/internal/database"
	"github.com/parthpandya5/forge-platform/internal/deployments"
	"github.com/parthpandya5/forge-platform/internal/healthmonitor"
	forgekubernetes "github.com/parthpandya5/forge-platform/internal/kubernetes"
	"github.com/parthpandya5/forge-platform/internal/logging"
	forgemetrics "github.com/parthpandya5/forge-platform/internal/metrics"
	"github.com/parthpandya5/forge-platform/internal/secrets"
	"github.com/parthpandya5/forge-platform/internal/source"
	"github.com/parthpandya5/forge-platform/internal/store"
	"github.com/parthpandya5/forge-platform/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.Environment).With("component", "worker", "worker_id", cfg.WorkerID)
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer startupCancel()
	pool, err := database.OpenWithMaxConnections(startupCtx, cfg.DatabaseURL, int32(2*cfg.WorkerConcurrency+5))
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	cipher, err := secrets.New(cfg.EncryptionKey, cfg.Environment)
	if err != nil {
		return fmt.Errorf("initialize secret encryption: %w", err)
	}
	runtime, err := forgekubernetes.New(forgekubernetes.Config{
		Kubeconfig: cfg.Kubeconfig, Namespace: cfg.KubeNamespace, IngressClass: cfg.IngressClass,
		BaseDomain: cfg.BaseDomain, PullPolicy: cfg.PullPolicy,
	})
	if err != nil {
		return fmt.Errorf("initialize Kubernetes: %w", err)
	}
	repository := store.New(pool)
	builder := build.NewDocker("docker", cfg.DockerHost, cfg.Registry, cfg.ImageRepository)
	registry := prometheus.NewRegistry()
	metrics := forgemetrics.NewWorker(registry)
	engine := deployments.NewEngine(repository, source.NewGit("git"), builder, runtime, cipher, logger,
		deployments.Config{
			CloneTimeout: cfg.CloneTimeout, BuildTimeout: cfg.BuildTimeout,
			RolloutTimeout: cfg.RolloutTimeout, PushImages: cfg.PushImages,
			ObserveBuildDuration: func(duration time.Duration) { metrics.BuildDuration.Observe(duration.Seconds()) },
		})
	coordinator := worker.New(repository, engine, logger, metrics, worker.Config{
		WorkerID: cfg.WorkerID, Concurrency: cfg.WorkerConcurrency, Lease: cfg.JobLease,
		Heartbeat: cfg.JobHeartbeat, PollInterval: 500 * time.Millisecond,
	})
	monitor := healthmonitor.New(repository, runtime, logger.With("component", "health-monitor"), cfg.HealthInterval)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	metricsServer := &http.Server{
		Addr: cfg.WorkerMetricsAddr, Handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	metricsListener, err := net.Listen("tcp", cfg.WorkerMetricsAddr)
	if err != nil {
		return fmt.Errorf("listen on worker metrics address %s: %w", cfg.WorkerMetricsAddr, err)
	}
	metricsResult := make(chan error, 1)
	go func() {
		logger.Info("worker metrics listening", "address", cfg.WorkerMetricsAddr)
		serveErr := metricsServer.Serve(metricsListener)
		metricsResult <- serveErr
		if !errors.Is(serveErr, http.ErrServerClosed) {
			stop()
		}
	}()
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		monitor.Run(ctx)
	}()

	coordinatorErr := coordinator.Run(ctx)
	if coordinatorErr != nil {
		stop()
	}
	<-monitorDone
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down worker metrics server: %w", err)
	}
	serveErr := <-metricsResult
	if coordinatorErr != nil {
		return fmt.Errorf("run worker coordinator: %w", coordinatorErr)
	}
	if serveErr == nil {
		return errors.New("worker metrics server stopped unexpectedly")
	}
	if !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve worker metrics: %w", serveErr)
	}
	return nil
}
