package deployments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
	"github.com/parthpandya5/forge-platform/internal/failure"
	forgekubernetes "github.com/parthpandya5/forge-platform/internal/kubernetes"
	"github.com/parthpandya5/forge-platform/internal/secrets"
	"github.com/parthpandya5/forge-platform/internal/source"
	"github.com/parthpandya5/forge-platform/internal/store"
)

func TestEngineRunsDeploymentLifecycle(t *testing.T) {
	id := uuid.New()
	serviceID := uuid.New()
	ref := "main"
	repository := &fakeRepository{details: store.DeploymentContext{
		Deployment: domain.Deployment{ID: id, ServiceID: serviceID, RequestedRef: &ref, Status: domain.DeploymentQueued, CreatedAt: time.Now()},
		ProjectID:  uuid.New(), ProjectSlug: "demo", RepositoryURL: "https://github.com/acme/demo", RepositoryBranch: "main",
		Service: domain.Service{ID: serviceID, Name: "web", InternalPort: 8080, DesiredReplicas: 1, HealthCheckPath: "/health", CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi"},
	}}
	cipher, _ := secrets.New("", "test")
	engine := NewEngine(repository, fakeSource{}, fakeBuilder{}, fakeRuntime{}, cipher,
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{CloneTimeout: time.Second, BuildTimeout: time.Second, RolloutTimeout: time.Second})

	if err := engine.Process(context.Background(), id); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if repository.details.Deployment.Status != domain.DeploymentLive {
		t.Fatalf("status = %s, want LIVE", repository.details.Deployment.Status)
	}
	want := []domain.DeploymentStatus{domain.DeploymentCloning, domain.DeploymentBuilding, domain.DeploymentDeploying, domain.DeploymentVerifying, domain.DeploymentLive}
	if len(repository.transitions) != len(want) {
		t.Fatalf("transitions = %v", repository.transitions)
	}
	for i := range want {
		if repository.transitions[i] != want[i] {
			t.Fatalf("transition %d = %s, want %s", i, repository.transitions[i], want[i])
		}
	}
}

func TestEngineBuildFailureCleansCheckoutAndCanBeFinalized(t *testing.T) {
	id := uuid.New()
	serviceID := uuid.New()
	ref := "main"
	repository := &fakeRepository{details: store.DeploymentContext{
		Deployment: domain.Deployment{ID: id, ServiceID: serviceID, RequestedRef: &ref, Status: domain.DeploymentQueued},
		ProjectID:  uuid.New(), ProjectSlug: "broken", RepositoryURL: "https://github.com/acme/broken", RepositoryBranch: "main",
		Service: domain.Service{ID: serviceID, Name: "web", InternalPort: 8080, DesiredReplicas: 1, HealthCheckPath: "/health", CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi"},
	}}
	cleaned := false
	cipher, _ := secrets.New("", "test")
	engine := NewEngine(repository, trackingSource{cleaned: &cleaned}, failingBuilder{}, fakeRuntime{}, cipher,
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{CloneTimeout: time.Second, BuildTimeout: time.Second, RolloutTimeout: time.Second})
	err := engine.Process(context.Background(), id)
	if err == nil || !failure.IsPermanent(err) {
		t.Fatalf("Process() error = %v, want permanent build failure", err)
	}
	if !cleaned {
		t.Fatal("failed build did not clean its source checkout")
	}
	if err := engine.MarkFailed(context.Background(), id, err); err != nil {
		t.Fatal(err)
	}
	if repository.details.Deployment.Status != domain.DeploymentFailed || repository.details.Deployment.ErrorMessage == nil {
		t.Fatalf("deployment was not finalized: %+v", repository.details.Deployment)
	}
}

func TestEngineReadinessFailureRemainsVerifyingUntilWorkerFinalizes(t *testing.T) {
	id := uuid.New()
	serviceID := uuid.New()
	image := "forge/demo:deployment"
	repository := &fakeRepository{details: store.DeploymentContext{
		Deployment: domain.Deployment{ID: id, ServiceID: serviceID, Status: domain.DeploymentVerifying, ImageTag: &image},
		ProjectID:  uuid.New(), ProjectSlug: "demo", Service: domain.Service{ID: serviceID, Name: "web", InternalPort: 8080, DesiredReplicas: 1, HealthCheckPath: "/health", CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi"},
	}}
	cipher, _ := secrets.New("", "test")
	engine := NewEngine(repository, fakeSource{}, fakeBuilder{}, failingRuntime{}, cipher,
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{RolloutTimeout: time.Second})
	err := engine.Process(context.Background(), id)
	if err == nil || !failure.IsPermanent(err) {
		t.Fatalf("Process() error = %v", err)
	}
	if repository.details.Deployment.Status != domain.DeploymentVerifying {
		t.Fatalf("status = %s, want VERIFYING before failure finalization", repository.details.Deployment.Status)
	}
}

func TestEngineRollbackSkipsSourceAndBuild(t *testing.T) {
	id := uuid.New()
	sourceID := uuid.New()
	serviceID := uuid.New()
	image := "forge/demo:old"
	repository := &fakeRepository{details: store.DeploymentContext{
		Deployment: domain.Deployment{ID: id, ServiceID: serviceID, Status: domain.DeploymentQueued, RollbackOf: &sourceID, ImageTag: &image},
		ProjectID:  uuid.New(), ProjectSlug: "demo", Service: domain.Service{ID: serviceID, Name: "web", InternalPort: 8080, DesiredReplicas: 1, HealthCheckPath: "/health", CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi"},
	}}
	cipher, _ := secrets.New("", "test")
	engine := NewEngine(repository, panicSource{}, panicBuilder{}, fakeRuntime{}, cipher,
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{RolloutTimeout: time.Second})
	if err := engine.Process(context.Background(), id); err != nil {
		t.Fatalf("Process() rollback error = %v", err)
	}
	if repository.details.Deployment.Status != domain.DeploymentLive {
		t.Fatalf("status = %s", repository.details.Deployment.Status)
	}
}

func TestEngineCloningRetryPinsRecordedCommit(t *testing.T) {
	id := uuid.New()
	serviceID := uuid.New()
	commit := "1111111111111111111111111111111111111111"
	branch := "main"
	repository := &fakeRepository{details: store.DeploymentContext{
		Deployment: domain.Deployment{ID: id, ServiceID: serviceID, Status: domain.DeploymentCloning, RequestedRef: &branch, CommitSHA: &commit},
		ProjectID:  uuid.New(), ProjectSlug: "demo", RepositoryURL: "https://github.com/acme/demo", RepositoryBranch: branch,
		Service: domain.Service{ID: serviceID, Name: "web", InternalPort: 8080, DesiredReplicas: 1, HealthCheckPath: "/health", CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi"},
	}}
	sourceAdapter := &recordingSource{sha: commit}
	cipher, _ := secrets.New("", "test")
	engine := NewEngine(repository, sourceAdapter, fakeBuilder{}, fakeRuntime{}, cipher,
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{CloneTimeout: time.Second, BuildTimeout: time.Second, RolloutTimeout: time.Second})

	if err := engine.Process(context.Background(), id); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if sourceAdapter.ref != commit {
		t.Fatalf("checkout ref = %q, want recorded commit %q", sourceAdapter.ref, commit)
	}
	if repository.sourceUpdates != 0 {
		t.Fatalf("source commit was rewritten %d times", repository.sourceUpdates)
	}
}

func TestEngineBuildingRetryWithArtifactSkipsCheckout(t *testing.T) {
	id := uuid.New()
	serviceID := uuid.New()
	image := "forge/demo:existing"
	repository := &fakeRepository{details: store.DeploymentContext{
		Deployment: domain.Deployment{ID: id, ServiceID: serviceID, Status: domain.DeploymentBuilding, ImageTag: &image},
		ProjectID:  uuid.New(), ProjectSlug: "demo",
		Service: domain.Service{ID: serviceID, Name: "web", InternalPort: 8080, DesiredReplicas: 1, HealthCheckPath: "/health", CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi"},
	}}
	cipher, _ := secrets.New("", "test")
	engine := NewEngine(repository, panicSource{}, fakeBuilder{}, fakeRuntime{}, cipher,
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{CloneTimeout: time.Second, BuildTimeout: time.Second, RolloutTimeout: time.Second})

	if err := engine.Process(context.Background(), id); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if repository.details.Deployment.Status != domain.DeploymentLive {
		t.Fatalf("status = %s, want LIVE", repository.details.Deployment.Status)
	}
	if !containsString(repository.events, "deployment.build_completed") {
		t.Fatalf("events = %v, want repaired build completion event", repository.events)
	}
}

type fakeRepository struct {
	details       store.DeploymentContext
	transitions   []domain.DeploymentStatus
	logs          []string
	sourceUpdates int
	events        []string
}

func (f *fakeRepository) GetDeploymentContext(context.Context, uuid.UUID) (store.DeploymentContext, error) {
	return f.details, nil
}
func (f *fakeRepository) TransitionDeployment(_ context.Context, _ uuid.UUID, to domain.DeploymentStatus, _ string, _ string, failure error) (domain.Deployment, error) {
	if err := domain.ValidateTransition(f.details.Deployment.Status, to); err != nil {
		return domain.Deployment{}, err
	}
	f.details.Deployment.Status = to
	if failure != nil {
		message := failure.Error()
		f.details.Deployment.ErrorMessage = &message
	}
	f.transitions = append(f.transitions, to)
	return f.details.Deployment, nil
}
func (f *fakeRepository) SetDeploymentSource(_ context.Context, _ uuid.UUID, sha string) error {
	f.details.Deployment.CommitSHA = &sha
	f.sourceUpdates++
	return nil
}
func (f *fakeRepository) SetDeploymentImage(_ context.Context, _ uuid.UUID, image string) error {
	f.details.Deployment.ImageTag = &image
	return nil
}
func (f *fakeRepository) SetDeploymentRoute(_ context.Context, _ uuid.UUID, route string) error {
	f.details.Deployment.RouteURL = &route
	return nil
}
func (f *fakeRepository) AppendEvent(_ context.Context, _ uuid.UUID, eventType, _ string, _ map[string]any) error {
	f.events = append(f.events, eventType)
	return nil
}
func (f *fakeRepository) AppendLog(_ context.Context, _ uuid.UUID, _ string, message string) error {
	f.logs = append(f.logs, message)
	return nil
}
func (f *fakeRepository) DeploymentEnvironment(context.Context, uuid.UUID) ([]store.EncryptedEnvironmentVariable, error) {
	return nil, nil
}

type fakeSource struct{}

func (fakeSource) Checkout(context.Context, uuid.UUID, string, string, source.LogFunc) (*source.Checkout, error) {
	return source.NewCheckout("/tmp", "1111111111111111111111111111111111111111", func() error { return nil }), nil
}

type recordingSource struct {
	ref string
	sha string
}

func (s *recordingSource) Checkout(_ context.Context, _ uuid.UUID, _, ref string, _ source.LogFunc) (*source.Checkout, error) {
	s.ref = ref
	return source.NewCheckout("/tmp", s.sha, func() error { return nil }), nil
}

type trackingSource struct{ cleaned *bool }

func (s trackingSource) Checkout(context.Context, uuid.UUID, string, string, source.LogFunc) (*source.Checkout, error) {
	return source.NewCheckout("/tmp", "1111111111111111111111111111111111111111", func() error {
		*s.cleaned = true
		return nil
	}), nil
}

type panicSource struct{}

func (panicSource) Checkout(context.Context, uuid.UUID, string, string, source.LogFunc) (*source.Checkout, error) {
	panic("source called for rollback")
}

type fakeBuilder struct{}

func (fakeBuilder) ImageTag(_, deploymentID string) string                      { return "forge/demo:" + deploymentID }
func (fakeBuilder) Build(context.Context, string, string, source.LogFunc) error { return nil }
func (fakeBuilder) Push(context.Context, string, source.LogFunc) error          { return nil }

type failingBuilder struct{ fakeBuilder }

func (failingBuilder) Build(context.Context, string, string, source.LogFunc) error {
	return failure.Permanent(errors.New("Dockerfile is invalid"))
}

type panicBuilder struct{}

func (panicBuilder) ImageTag(string, string) string { panic("builder called for rollback") }
func (panicBuilder) Build(context.Context, string, string, source.LogFunc) error {
	panic("builder called for rollback")
}
func (panicBuilder) Push(context.Context, string, source.LogFunc) error {
	panic("builder called for rollback")
}

type fakeRuntime struct{}

func (fakeRuntime) Reconcile(context.Context, forgekubernetes.Workload) (string, error) {
	return "http://demo.localhost", nil
}
func (fakeRuntime) WaitReady(context.Context, forgekubernetes.Workload) error { return nil }
func (fakeRuntime) RuntimeLogs(context.Context, forgekubernetes.Workload, int64) ([]string, error) {
	return []string{"ready"}, nil
}

type failingRuntime struct{ fakeRuntime }

func (failingRuntime) WaitReady(context.Context, forgekubernetes.Workload) error {
	return failure.Permanent(errors.New("readiness probe never passed"))
}

func TestStageLoggerBoundsPersistedOutput(t *testing.T) {
	repository := &fakeRepository{}
	logger := newStageLogger(context.Background(), repository, uuid.New(), "build")
	logger.lines = maximumStageLogLines
	logger.Write("discarded")
	logger.Write("also discarded")
	if len(repository.logs) != 1 || !strings.Contains(repository.logs[0], "truncated") {
		t.Fatalf("truncation logs = %v", repository.logs)
	}
}

func TestRedactSecretsUsesLongestValueFirst(t *testing.T) {
	got := redactSecrets("token=secret-value short=secret", map[string]string{
		"TOKEN": "secret-value", "PREFIX": "secret",
	})
	if strings.Contains(got, "secret") || got != "token=[REDACTED] short=[REDACTED]" {
		t.Fatalf("redacted log = %q", got)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
