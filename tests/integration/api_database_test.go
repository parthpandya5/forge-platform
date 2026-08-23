//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/parthpandya5/forge-platform/internal/auth"
	"github.com/parthpandya5/forge-platform/internal/controlplane"
	"github.com/parthpandya5/forge-platform/internal/database"
	"github.com/parthpandya5/forge-platform/internal/domain"
	"github.com/parthpandya5/forge-platform/internal/httpapi"
	"github.com/parthpandya5/forge-platform/internal/secrets"
	"github.com/parthpandya5/forge-platform/internal/store"
)

func TestAPIAndDatabaseLifecycle(t *testing.T) {
	pool, repository, userID := setup(t)
	cipher, err := secrets.New("", "test")
	if err != nil {
		t.Fatal(err)
	}
	control := controlplane.New(repository, cipher, 3)
	handler := httpapi.NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), "").
		WithControlPlane(control, userID, pool.Ping).Handler()

	invalid := perform(t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Unsafe", "slug": "unsafe", "repository_url": "file:///etc/passwd", "repository_branch": "main",
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid project status = %d, body = %s", invalid.Code, invalid.Body.String())
	}

	created := perform(t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Integration API", "slug": "integration-api", "repository_url": "https://github.com/example/integration-api", "repository_branch": "main",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body = %s", created.Code, created.Body.String())
	}
	var projectEnvelope struct {
		Data domain.Project `json:"data"`
	}
	decode(t, created, &projectEnvelope)

	duplicate := perform(t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Duplicate", "slug": "integration-api", "repository_url": "https://github.com/example/other", "repository_branch": "main",
	})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate project status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}

	serviceResponse := perform(t, handler, http.MethodPost, "/api/projects/"+projectEnvelope.Data.ID.String()+"/services", map[string]any{
		"name": "web", "internal_port": 8080, "desired_replicas": 1, "health_check_path": "/health",
	})
	if serviceResponse.Code != http.StatusCreated {
		t.Fatalf("create service status = %d, body = %s", serviceResponse.Code, serviceResponse.Body.String())
	}
	var serviceEnvelope struct {
		Data domain.Service `json:"data"`
	}
	decode(t, serviceResponse, &serviceEnvelope)

	serviceUpdate := perform(t, handler, http.MethodPut, "/api/services/"+serviceEnvelope.Data.ID.String(), map[string]any{
		"name": "web", "internal_port": 8080, "desired_replicas": 2, "health_check_path": "/health",
		"cpu_request": "100m", "cpu_limit": "750m", "memory_request": "128Mi", "memory_limit": "768Mi",
	})
	if serviceUpdate.Code != http.StatusOK {
		t.Fatalf("update service status = %d, body = %s", serviceUpdate.Code, serviceUpdate.Body.String())
	}

	secretResponse := perform(t, handler, http.MethodPut, "/api/services/"+serviceEnvelope.Data.ID.String()+"/environment/API_TOKEN", map[string]string{"value": "must-not-be-returned"})
	if secretResponse.Code != http.StatusOK {
		t.Fatalf("set environment status = %d, body = %s", secretResponse.Code, secretResponse.Body.String())
	}
	if bytes.Contains(secretResponse.Body.Bytes(), []byte("must-not-be-returned")) {
		t.Fatal("environment response exposed secret value")
	}
	var variableEnvelope struct {
		Data domain.EnvironmentVariable `json:"data"`
	}
	decode(t, secretResponse, &variableEnvelope)
	variablesResponse := perform(t, handler, http.MethodGet, "/api/services/"+serviceEnvelope.Data.ID.String()+"/environment", nil)
	if variablesResponse.Code != http.StatusOK || bytes.Contains(variablesResponse.Body.Bytes(), []byte("must-not-be-returned")) {
		t.Fatalf("list environment status = %d, body = %s", variablesResponse.Code, variablesResponse.Body.String())
	}

	deploymentResponse := perform(t, handler, http.MethodPost, "/api/services/"+serviceEnvelope.Data.ID.String()+"/deployments", map[string]any{})
	if deploymentResponse.Code != http.StatusAccepted {
		t.Fatalf("create deployment status = %d, body = %s", deploymentResponse.Code, deploymentResponse.Body.String())
	}
	var deploymentEnvelope struct {
		Data domain.Deployment `json:"data"`
	}
	decode(t, deploymentResponse, &deploymentEnvelope)
	if deploymentEnvelope.Data.Status != domain.DeploymentQueued {
		t.Fatalf("deployment status = %s", deploymentEnvelope.Data.Status)
	}
	if deploymentEnvelope.Data.RequestID == "" || deploymentEnvelope.Data.RequestID != deploymentResponse.Header().Get("X-Request-ID") {
		t.Fatalf("deployment request ID %q does not match response header", deploymentEnvelope.Data.RequestID)
	}

	var jobs, events int
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM jobs WHERE deployment_id = $1),
		(SELECT count(*) FROM deployment_events WHERE deployment_id = $1)`, deploymentEnvelope.Data.ID).Scan(&jobs, &events); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || events != 1 {
		t.Fatalf("atomic deployment records jobs=%d events=%d, want 1/1", jobs, events)
	}

	getResponse := perform(t, handler, http.MethodGet, "/api/deployments/"+deploymentEnvelope.Data.ID.String(), nil)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get deployment status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}
	historyResponse := perform(t, handler, http.MethodGet, "/api/services/"+serviceEnvelope.Data.ID.String()+"/deployments?limit=10", nil)
	if historyResponse.Code != http.StatusOK || !bytes.Contains(historyResponse.Body.Bytes(), []byte(deploymentEnvelope.Data.ID.String())) {
		t.Fatalf("deployment history status = %d, body = %s", historyResponse.Code, historyResponse.Body.String())
	}
	eventsResponse := perform(t, handler, http.MethodGet, "/api/deployments/"+deploymentEnvelope.Data.ID.String()+"/events?limit=10", nil)
	if eventsResponse.Code != http.StatusOK || !bytes.Contains(eventsResponse.Body.Bytes(), []byte("deployment.queued")) {
		t.Fatalf("deployment events status = %d, body = %s", eventsResponse.Code, eventsResponse.Body.String())
	}
	logsResponse := perform(t, handler, http.MethodGet, "/api/deployments/"+deploymentEnvelope.Data.ID.String()+"/logs?limit=10", nil)
	if logsResponse.Code != http.StatusOK {
		t.Fatalf("deployment logs status = %d, body = %s", logsResponse.Code, logsResponse.Body.String())
	}
	missingResponse := perform(t, handler, http.MethodGet, "/api/projects/"+uuid.NewString(), nil)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing project status = %d, body = %s", missingResponse.Code, missingResponse.Body.String())
	}
	deleteVariableResponse := perform(t, handler, http.MethodDelete, "/api/services/"+serviceEnvelope.Data.ID.String()+"/environment/"+variableEnvelope.Data.ID.String(), nil)
	if deleteVariableResponse.Code != http.StatusNoContent {
		t.Fatalf("delete environment status = %d, body = %s", deleteVariableResponse.Code, deleteVariableResponse.Body.String())
	}

	otherUser := uuid.New()
	if _, err := repository.EnsureUser(context.Background(), otherUser, otherUser.String()+"@example.test", "Other User"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, otherUser); err != nil {
			t.Errorf("delete secondary test user: %v", err)
		}
	}()
	if _, err := repository.GetProject(context.Background(), otherUser, projectEnvelope.Data.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user GetProject error = %v, want ErrNotFound", err)
	}
}

func TestWebhookDeliveryDeduplicatesDeploymentAndJob(t *testing.T) {
	pool, repository, userID := setup(t)
	project, service := createFixture(t, repository, userID, true)
	commit := "1234567890abcdef1234567890abcdef12345678"

	deployments, duplicate, err := repository.CreateWebhookDeployments(
		context.Background(), "delivery-"+uuid.NewString(), project.RepositoryURL,
		project.RepositoryBranch, commit, "webhook-request", 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate || len(deployments) != 1 || deployments[0].ServiceID != service.ID {
		t.Fatalf("first delivery duplicate=%t deployments=%v", duplicate, deployments)
	}
	deliveryID := "duplicate-" + uuid.NewString()
	first, duplicate, err := repository.CreateWebhookDeployments(context.Background(), deliveryID, project.RepositoryURL, project.RepositoryBranch, commit, "request-1", 3)
	if err != nil || duplicate || len(first) != 1 {
		t.Fatalf("first duplicate delivery call: duplicate=%t deployments=%d error=%v", duplicate, len(first), err)
	}
	second, duplicate, err := repository.CreateWebhookDeployments(context.Background(), deliveryID, project.RepositoryURL, project.RepositoryBranch, commit, "request-2", 3)
	if err != nil || !duplicate || len(second) != 0 {
		t.Fatalf("second duplicate delivery call: duplicate=%t deployments=%d error=%v", duplicate, len(second), err)
	}
	var deploymentCount, jobCount int
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM deployments WHERE service_id = $1),
		(SELECT count(*) FROM jobs j JOIN deployments d ON d.id = j.deployment_id WHERE d.service_id = $1)`, service.ID).
		Scan(&deploymentCount, &jobCount); err != nil {
		t.Fatal(err)
	}
	if deploymentCount != 2 || jobCount != 2 {
		t.Fatalf("webhook transaction counts deployments=%d jobs=%d, want 2/2", deploymentCount, jobCount)
	}
}

func TestConcurrentJobClaimsAreUniqueAndExpiredLeaseRecovers(t *testing.T) {
	_, repository, userID := setup(t)
	project, service := createFixture(t, repository, userID, false)
	const count = 12
	services := []domain.Service{service}
	for index := 1; index < count; index++ {
		input := domain.CreateServiceInput{
			Name: fmt.Sprintf("service-%02d", index), InternalPort: 8080,
			DesiredReplicas: 1, HealthCheckPath: "/health",
		}
		if err := input.NormalizeAndValidate(); err != nil {
			t.Fatal(err)
		}
		created, err := repository.CreateService(context.Background(), userID, project.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, created)
	}
	wanted := make(map[uuid.UUID]struct{}, count)
	for _, targetService := range services {
		deployment, err := repository.CreateDeployment(context.Background(), userID, targetService.ID, "main", "concurrency", 3)
		if err != nil {
			t.Fatal(err)
		}
		wanted[deployment.ID] = struct{}{}
	}

	type result struct {
		job *domain.Job
		err error
	}
	results := make(chan result, count)
	var workers sync.WaitGroup
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			job, err := repository.ClaimJob(context.Background(), "integration-worker-"+uuid.NewString(), time.Minute)
			results <- result{job: job, err: err}
		}()
	}
	workers.Wait()
	close(results)
	claimed := make(map[uuid.UUID]struct{}, count)
	for claim := range results {
		if claim.err != nil || claim.job == nil {
			t.Fatalf("claim result job=%v error=%v", claim.job, claim.err)
		}
		if _, expected := wanted[claim.job.DeploymentID]; !expected {
			t.Fatalf("claimed unexpected deployment %s", claim.job.DeploymentID)
		}
		if _, duplicate := claimed[claim.job.DeploymentID]; duplicate {
			t.Fatalf("deployment %s was claimed twice", claim.job.DeploymentID)
		}
		claimed[claim.job.DeploymentID] = struct{}{}
		if err := repository.CompleteJob(context.Background(), claim.job.ID, *claim.job.LockedBy); err != nil {
			t.Fatal(err)
		}
	}
	if len(claimed) != count {
		t.Fatalf("claimed %d jobs, want %d", len(claimed), count)
	}

	older, err := repository.CreateDeployment(context.Background(), userID, service.ID, "main", "fifo-older", 3)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := repository.CreateDeployment(context.Background(), userID, service.ID, "main", "fifo-newer", 3)
	if err != nil {
		t.Fatal(err)
	}
	depth, err := repository.QueueDepth(context.Background())
	if err != nil || depth != 1 {
		t.Fatalf("FIFO queue depth = %d, error=%v, want 1", depth, err)
	}
	olderJob, err := repository.ClaimJob(context.Background(), "fifo-worker", time.Minute)
	if err != nil || olderJob == nil || olderJob.DeploymentID != older.ID {
		t.Fatalf("older FIFO claim job=%v error=%v", olderJob, err)
	}
	blockedJob, err := repository.ClaimJob(context.Background(), "fifo-worker-2", time.Minute)
	if err != nil || blockedJob != nil {
		t.Fatalf("newer same-service job was not blocked: job=%v error=%v", blockedJob, err)
	}
	if err := repository.CompleteJob(context.Background(), olderJob.ID, "fifo-worker"); err != nil {
		t.Fatal(err)
	}
	newerJob, err := repository.ClaimJob(context.Background(), "fifo-worker-2", time.Minute)
	if err != nil || newerJob == nil || newerJob.DeploymentID != newer.ID {
		t.Fatalf("newer FIFO claim job=%v error=%v", newerJob, err)
	}
	if err := repository.CompleteJob(context.Background(), newerJob.ID, "fifo-worker-2"); err != nil {
		t.Fatal(err)
	}
	unlock, acquired, err := repository.TryServiceDeploymentLock(context.Background(), older.ID)
	if err != nil || !acquired {
		t.Fatalf("first service lock acquired=%t error=%v", acquired, err)
	}
	secondUnlock, acquired, err := repository.TryServiceDeploymentLock(context.Background(), newer.ID)
	if err != nil || acquired || secondUnlock != nil {
		t.Fatalf("second same-service lock acquired=%t error=%v", acquired, err)
	}
	if err := unlock(context.Background()); err != nil {
		t.Fatal(err)
	}
	unsafeUnlock, acquired, err := repository.TryServiceDeploymentLock(context.Background(), older.ID)
	if err != nil || !acquired {
		t.Fatalf("service lock for failed-unlock test acquired=%t error=%v", acquired, err)
	}
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := unsafeUnlock(cancelledCtx); err == nil {
		t.Fatal("unlock with cancelled context unexpectedly succeeded")
	}
	// A failed session unlock must destroy its pooled connection. Otherwise the
	// invisible advisory lock would poison the pool and permanently block this service.
	recoveredUnlock, acquired, err := repository.TryServiceDeploymentLock(context.Background(), older.ID)
	if err != nil || !acquired {
		t.Fatalf("service lock after failed unlock acquired=%t error=%v", acquired, err)
	}
	if err := recoveredUnlock(context.Background()); err != nil {
		t.Fatal(err)
	}

	deployment, err := repository.CreateDeployment(context.Background(), userID, service.ID, "main", "lease-recovery", 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.ClaimJob(context.Background(), "crashed-worker", 50*time.Millisecond)
	if err != nil || first == nil || first.DeploymentID != deployment.ID {
		t.Fatalf("initial lease claim job=%v error=%v", first, err)
	}
	time.Sleep(100 * time.Millisecond)
	recovered, err := repository.ClaimJob(context.Background(), "recovery-worker", time.Minute)
	if err != nil || recovered == nil || recovered.ID != first.ID || recovered.Attempts != 1 {
		t.Fatalf("recovered lease job=%v error=%v", recovered, err)
	}
	if err := repository.CompleteJob(context.Background(), recovered.ID, "recovery-worker"); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRunnerIsIdempotent(t *testing.T) {
	pool, _, _ := setup(t)
	if err := database.MigrateUp(context.Background(), pool, "../../migrations"); err != nil {
		t.Fatalf("second MigrateUp() error = %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations WHERE version IN (1, 2, 3)`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("migration version rows = %d, want 3", count)
	}
}

func createFixture(t *testing.T, repository *store.Store, userID uuid.UUID, autoDeploy bool) (domain.Project, domain.Service) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	projectInput := domain.CreateProjectInput{
		Name: "Integration Fixture", Slug: "fixture-" + suffix,
		RepositoryURL: "https://github.com/example/fixture", RepositoryBranch: "main", AutoDeploy: autoDeploy,
	}
	if err := projectInput.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	project, err := repository.CreateProject(context.Background(), userID, projectInput)
	if err != nil {
		t.Fatal(err)
	}
	serviceInput := domain.CreateServiceInput{Name: "web", InternalPort: 8080, DesiredReplicas: 1, HealthCheckPath: "/health"}
	if err := serviceInput.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	service, err := repository.CreateService(context.Background(), userID, project.ID, serviceInput)
	if err != nil {
		t.Fatal(err)
	}
	return project, service
}

func setup(t *testing.T) (*databasePool, *store.Store, uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("FORGE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("FORGE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.MigrateUp(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}
	repository := store.New(pool)
	userID := auth.LocalUserID("integration-" + uuid.NewString() + "@forge.test")
	if _, err := repository.EnsureUser(ctx, userID, userID.String()+"@forge.test", "Integration User"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Errorf("delete integration user: %v", err)
		}
	})
	return pool, repository, userID
}

// databasePool captures the pgx pool surface used here while retaining the concrete return type.
type databasePool = pgxpool.Pool

func perform(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decode(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
