//go:build kubernetes

package kubernetes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/failure"
	forgekubernetes "github.com/parthpandya5/forge-platform/internal/kubernetes"
)

func TestRuntimeReconcilesReadyRoutedWorkload(t *testing.T) {
	image := os.Getenv("FORGE_TEST_IMAGE")
	if image == "" {
		t.Skip("FORGE_TEST_IMAGE is required for the real-cluster integration test")
	}

	serviceID := uuid.New()
	projectID := uuid.New()
	projectSlug := "k8s-it-" + strings.ReplaceAll(serviceID.String()[:8], "-", "")
	runtime, err := forgekubernetes.New(forgekubernetes.Config{
		Kubeconfig:   os.Getenv("FORGE_KUBECONFIG"),
		Namespace:    envOr("FORGE_KUBERNETES_NAMESPACE", "forge-kubernetes-tests"),
		IngressClass: envOr("FORGE_INGRESS_CLASS", "nginx"),
		BaseDomain:   envOr("FORGE_BASE_DOMAIN", "localhost"),
		PullPolicy:   "Always",
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	workload := forgekubernetes.Workload{
		DeploymentID: uuid.NewString(), ProjectID: projectID.String(), ProjectSlug: projectSlug,
		ServiceID: serviceID.String(), ServiceName: "web", Image: image, Port: 8080, Replicas: 1,
		HealthPath: "/health", CPURequest: "50m", CPULimit: "250m",
		MemoryRequest: "32Mi", MemoryLimit: "128Mi", Environment: map[string]string{"FORGE_INTEGRATION": "true"},
	}

	operationCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	route, err := runtime.Reconcile(operationCtx, workload)
	if err != nil {
		t.Fatalf("reconcile workload: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := runtime.DeleteWorkload(cleanupCtx, projectSlug, serviceID.String()); err != nil {
			t.Errorf("delete integration workload: %v", err)
		}
	})
	if err := runtime.WaitReady(operationCtx, workload); err != nil {
		t.Fatalf("wait for workload: %v", err)
	}
	status, message, err := runtime.CheckHealth(operationCtx, workload)
	if err != nil {
		t.Fatalf("check workload health: %v", err)
	}
	if status != "RUNNING" {
		t.Fatalf("health status = %q (%s)", status, message)
	}

	routeURL, err := url.Parse(route)
	if err != nil {
		t.Fatalf("parse route: %v", err)
	}
	health := waitForRoute(t, operationCtx, routeURL.Host)
	if health.Status != "ok" {
		t.Fatalf("route health status = %q", health.Status)
	}
	logs, err := runtime.RuntimeLogs(operationCtx, workload, 100)
	if err != nil {
		t.Fatalf("read runtime logs: %v", err)
	}
	if !contains(logs, "hello-go listening") {
		t.Fatalf("runtime logs did not contain startup message: %v", logs)
	}
	t.Logf("verified image=%s route=%s health=%s", image, route, message)
}

func TestRuntimeReportsFailedWorkloads(t *testing.T) {
	tests := []struct {
		name  string
		image string
	}{
		{name: "crash", image: os.Getenv("FORGE_FAILURE_CRASH_IMAGE")},
		{name: "unhealthy", image: os.Getenv("FORGE_FAILURE_UNHEALTHY_IMAGE")},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if test.image == "" {
				t.Skip("failure image environment variables are required")
			}
			t.Parallel()
			serviceID := uuid.New()
			projectSlug := "failure-" + test.name + "-" + serviceID.String()[:8]
			runtime, err := forgekubernetes.New(forgekubernetes.Config{
				Kubeconfig:   os.Getenv("FORGE_KUBECONFIG"),
				Namespace:    envOr("FORGE_KUBERNETES_NAMESPACE", "forge-kubernetes-tests"),
				IngressClass: envOr("FORGE_INGRESS_CLASS", "nginx"),
				BaseDomain:   envOr("FORGE_BASE_DOMAIN", "localhost"),
				PullPolicy:   "Always",
			})
			if err != nil {
				t.Fatal(err)
			}
			workload := forgekubernetes.Workload{
				DeploymentID: uuid.NewString(), ProjectID: uuid.NewString(), ProjectSlug: projectSlug,
				ServiceID: serviceID.String(), ServiceName: "web", Image: test.image, Port: 8080, Replicas: 1,
				HealthPath: "/health", CPURequest: "25m", CPULimit: "250m",
				MemoryRequest: "32Mi", MemoryLimit: "128Mi", Environment: map[string]string{},
			}
			operationCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if _, err := runtime.Reconcile(operationCtx, workload); err != nil {
				t.Fatalf("reconcile failed workload: %v", err)
			}
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cleanupCancel()
				if err := runtime.DeleteWorkload(cleanupCtx, projectSlug, serviceID.String()); err != nil {
					t.Errorf("delete failed workload: %v", err)
				}
			})
			err = runtime.WaitReady(operationCtx, workload)
			if err == nil || !failure.IsPermanent(err) {
				t.Fatalf("WaitReady() error = %v, want permanent rollout failure", err)
			}
			t.Logf("observed expected %s rollout failure: %v", test.name, err)
		})
	}
}

type healthResponse struct {
	Status string `json:"status"`
}

func waitForRoute(t *testing.T, parent context.Context, host string) healthResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	var lastFailure string
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = host
		response, err := client.Do(request)
		if err == nil {
			var health healthResponse
			decodeErr := json.NewDecoder(response.Body).Decode(&health)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil {
				return health
			}
			lastFailure = fmt.Sprintf("status=%d decode_error=%v", response.StatusCode, decodeErr)
		} else {
			lastFailure = err.Error()
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Ingress route did not converge: %s", lastFailure)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func contains(lines []string, substring string) bool {
	for _, line := range lines {
		if strings.Contains(line, substring) {
			return true
		}
	}
	return false
}
