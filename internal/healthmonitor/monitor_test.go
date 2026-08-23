package healthmonitor

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
	forgekubernetes "github.com/parthpandya5/forge-platform/internal/kubernetes"
	"github.com/parthpandya5/forge-platform/internal/store"
)

func TestCheckPersistsRuntimeHealth(t *testing.T) {
	repository := &fakeRepository{target: store.HealthTarget{
		DeploymentID: uuid.New(), ProjectID: uuid.New(), ProjectSlug: "demo",
		ServiceID: uuid.New(), ServiceName: "web", DesiredReplicas: 2,
	}}
	monitor := New(repository, fakeRuntime{}, slog.New(slog.NewTextHandler(io.Discard, nil)), 1)
	monitor.check(context.Background())
	if repository.status != domain.ServiceHealthDegraded {
		t.Fatalf("status = %s, want DEGRADED", repository.status)
	}
}

type fakeRepository struct {
	target  store.HealthTarget
	status  domain.ServiceHealthStatus
	message string
}

func (f *fakeRepository) ListHealthTargets(context.Context, int) ([]store.HealthTarget, error) {
	return []store.HealthTarget{f.target}, nil
}
func (f *fakeRepository) UpdateServiceHealth(_ context.Context, _ uuid.UUID, status domain.ServiceHealthStatus, message string) error {
	f.status, f.message = status, message
	return nil
}
func (f *fakeRepository) ListServiceIDs(context.Context) ([]uuid.UUID, error) {
	return []uuid.UUID{f.target.ServiceID}, nil
}

type fakeRuntime struct{}

func (fakeRuntime) CheckHealth(context.Context, forgekubernetes.Workload) (domain.ServiceHealthStatus, string, error) {
	return domain.ServiceHealthDegraded, "1/2 replicas available", nil
}
func (fakeRuntime) GarbageCollect(context.Context, map[string]struct{}) error { return nil }
