package healthmonitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
	forgekubernetes "github.com/parthpandya5/forge-platform/internal/kubernetes"
	"github.com/parthpandya5/forge-platform/internal/store"
)

type Repository interface {
	ListHealthTargets(context.Context, int) ([]store.HealthTarget, error)
	UpdateServiceHealth(context.Context, uuid.UUID, domain.ServiceHealthStatus, string) error
	ListServiceIDs(context.Context) ([]uuid.UUID, error)
}

type Runtime interface {
	CheckHealth(context.Context, forgekubernetes.Workload) (domain.ServiceHealthStatus, string, error)
	GarbageCollect(context.Context, map[string]struct{}) error
}

type Monitor struct {
	repository Repository
	runtime    Runtime
	logger     *slog.Logger
	interval   time.Duration
}

func New(repository Repository, runtime Runtime, logger *slog.Logger, interval time.Duration) *Monitor {
	return &Monitor{repository: repository, runtime: runtime, logger: logger, interval: interval}
}

func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		m.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) check(ctx context.Context) {
	listCtx, listCancel := context.WithTimeout(ctx, 5*time.Second)
	targets, err := m.repository.ListHealthTargets(listCtx, 500)
	listCancel()
	if err != nil {
		if ctx.Err() == nil {
			m.logger.Warn("list health targets", "error", err)
		}
		return
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		status, message, checkErr := m.runtime.CheckHealth(checkCtx, forgekubernetes.Workload{
			DeploymentID: target.DeploymentID.String(), ProjectID: target.ProjectID.String(),
			ProjectSlug: target.ProjectSlug, ServiceID: target.ServiceID.String(),
			ServiceName: target.ServiceName, Replicas: int32(target.DesiredReplicas),
		})
		cancel()
		if checkErr != nil {
			status = domain.ServiceHealthUnknown
			message = checkErr.Error()
			m.logger.Warn("check workload health", "service_id", target.ServiceID, "error", checkErr)
		}
		updateCtx, updateCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := m.repository.UpdateServiceHealth(updateCtx, target.ServiceID, status, message); err != nil && ctx.Err() == nil {
			m.logger.Warn("persist workload health", "service_id", target.ServiceID, "error", err)
		}
		updateCancel()
	}
	m.garbageCollect(ctx)
}

func (m *Monitor) garbageCollect(ctx context.Context) {
	listCtx, listCancel := context.WithTimeout(ctx, 5*time.Second)
	ids, err := m.repository.ListServiceIDs(listCtx)
	listCancel()
	if err != nil {
		if ctx.Err() == nil {
			m.logger.Warn("list services for garbage collection", "error", err)
		}
		return
	}
	valid := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		valid[id.String()] = struct{}{}
	}
	gcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := m.runtime.GarbageCollect(gcCtx, valid); err != nil && ctx.Err() == nil {
		m.logger.Warn("garbage collect Kubernetes workloads", "error", err)
	}
}
