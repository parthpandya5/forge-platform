package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/parthpandya5/forge-platform/internal/failure"
)

func TestReconcileCreatesWorkloadResources(t *testing.T) {
	client := fake.NewSimpleClientset()
	runtime, err := NewWithClient(client, Config{Namespace: "forge-apps", IngressClass: "nginx", BaseDomain: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	workload := testWorkload()
	route, err := runtime.Reconcile(context.Background(), workload)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if route != "http://demo-api.localhost" {
		t.Fatalf("route = %q", route)
	}
	name := resourceName(workload.ProjectSlug, workload.ServiceID)
	if _, err := client.AppsV1().Deployments("forge-apps").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("Deployment not created: %v", err)
	}
	if _, err := client.CoreV1().Services("forge-apps").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("Service not created: %v", err)
	}
	if _, err := client.NetworkingV1().Ingresses("forge-apps").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("Ingress not created: %v", err)
	}
	if _, err := client.CoreV1().Secrets("forge-apps").Get(context.Background(), name+"-env", metav1.GetOptions{}); err != nil {
		t.Fatalf("Secret not created: %v", err)
	}
}

func TestReconcileUpdatesDeploymentImage(t *testing.T) {
	client := fake.NewSimpleClientset()
	runtime, _ := NewWithClient(client, Config{Namespace: "forge-apps", BaseDomain: "localhost"})
	workload := testWorkload()
	if _, err := runtime.Reconcile(context.Background(), workload); err != nil {
		t.Fatal(err)
	}
	workload.DeploymentID = "new-deployment"
	workload.Image = "forge/demo:new-deployment"
	if _, err := runtime.Reconcile(context.Background(), workload); err != nil {
		t.Fatal(err)
	}
	deployment, err := client.AppsV1().Deployments("forge-apps").Get(context.Background(), resourceName(workload.ProjectSlug, workload.ServiceID), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := deployment.Spec.Template.Spec.Containers[0].Image; got != workload.Image {
		t.Fatalf("image = %q", got)
	}
	security := deployment.Spec.Template.Spec.Containers[0].SecurityContext
	if security.RunAsUser == nil || *security.RunAsUser != 65532 {
		t.Fatalf("RunAsUser = %v", security.RunAsUser)
	}
}

func TestGarbageCollectRemovesOnlyOrphanedForgeWorkloads(t *testing.T) {
	client := fake.NewSimpleClientset()
	runtime, _ := NewWithClient(client, Config{Namespace: "forge-apps", BaseDomain: "localhost"})
	workload := testWorkload()
	if _, err := runtime.Reconcile(context.Background(), workload); err != nil {
		t.Fatal(err)
	}
	name := resourceName(workload.ProjectSlug, workload.ServiceID)
	if err := client.AppsV1().Deployments("forge-apps").Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.GarbageCollect(context.Background(), map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Services("forge-apps").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("orphaned Service still exists")
	}
	if _, err := client.NetworkingV1().Ingresses("forge-apps").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("orphaned Ingress still exists")
	}
	if _, err := client.CoreV1().Secrets("forge-apps").Get(context.Background(), name+"-env", metav1.GetOptions{}); err == nil {
		t.Fatal("orphaned Secret still exists")
	}
}

func TestGarbageCollectPreservesKnownService(t *testing.T) {
	client := fake.NewSimpleClientset()
	runtime, _ := NewWithClient(client, Config{Namespace: "forge-apps", BaseDomain: "localhost"})
	workload := testWorkload()
	if _, err := runtime.Reconcile(context.Background(), workload); err != nil {
		t.Fatal(err)
	}
	if err := runtime.GarbageCollect(context.Background(), map[string]struct{}{workload.ServiceID: {}}); err != nil {
		t.Fatal(err)
	}
	name := resourceName(workload.ProjectSlug, workload.ServiceID)
	if _, err := client.AppsV1().Deployments("forge-apps").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("known Deployment was removed: %v", err)
	}
}

func TestDeleteWorkloadContinuesAfterOneResourceFails(t *testing.T) {
	client := fake.NewSimpleClientset()
	runtime, _ := NewWithClient(client, Config{Namespace: "forge-apps", BaseDomain: "localhost"})
	workload := testWorkload()
	if _, err := runtime.Reconcile(context.Background(), workload); err != nil {
		t.Fatal(err)
	}
	client.PrependReactor("delete", "ingresses", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, errors.New("simulated ingress deletion failure")
	})
	name := resourceName(workload.ProjectSlug, workload.ServiceID)
	if err := runtime.DeleteWorkload(context.Background(), workload.ProjectSlug, workload.ServiceID); err == nil {
		t.Fatal("DeleteWorkload() error = nil")
	}
	if _, err := client.AppsV1().Deployments("forge-apps").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("Deployment still exists after best-effort cleanup")
	}
	if _, err := client.CoreV1().Services("forge-apps").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("Service still exists after best-effort cleanup")
	}
	if _, err := client.CoreV1().Secrets("forge-apps").Get(context.Background(), name+"-env", metav1.GetOptions{}); err == nil {
		t.Fatal("Secret still exists after best-effort cleanup")
	}
}

func TestValidateWorkloadRejectsRequestAboveLimit(t *testing.T) {
	workload := testWorkload()
	workload.CPURequest = "2"
	workload.CPULimit = "1"
	if err := validateWorkload(workload); err == nil {
		t.Fatal("validateWorkload() error = nil")
	}
}

func TestNewWithClientRejectsInvalidRoutingConfiguration(t *testing.T) {
	client := fake.NewSimpleClientset()
	for _, config := range []Config{
		{Namespace: "Not Valid", IngressClass: "nginx", BaseDomain: "localhost"},
		{Namespace: "forge-apps", IngressClass: "not/valid", BaseDomain: "localhost"},
		{Namespace: "forge-apps", IngressClass: "nginx", BaseDomain: "https://example.com"},
	} {
		if _, err := NewWithClient(client, config); err == nil {
			t.Fatalf("NewWithClient() accepted %+v", config)
		}
	}
}

func TestRouteHostBoundsLongServiceLabel(t *testing.T) {
	host := routeHost(
		"project-with-a-deliberately-long-name-that-nearly-fills-a-dns-label",
		"background-worker-with-another-long-name",
		"11111111-2222-3333-4444-555555555555",
		"localhost",
	)
	label := strings.TrimSuffix(host, ".localhost")
	if len(label) > 63 || !strings.HasSuffix(label, "-1111111122") {
		t.Fatalf("route host = %q", host)
	}
}

func TestRouteHostDisambiguatesNormalizedServiceNames(t *testing.T) {
	first := routeHost("project", "API Worker", "11111111-2222-3333-4444-555555555555", "localhost")
	second := routeHost("project", "api-worker", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "localhost")
	if first == second || first != "api-worker-project-1111111122.localhost" {
		t.Fatalf("secondary service hosts = %q and %q", first, second)
	}
	if primary := routeHost("project", "web", "ffffffff-bbbb-cccc-dddd-eeeeeeeeeeee", "localhost"); primary != "project.localhost" {
		t.Fatalf("primary service host = %q", primary)
	}
}

func TestRolloutCancellationRemainsRetryable(t *testing.T) {
	client := fake.NewSimpleClientset()
	runtime, err := NewWithClient(client, Config{Namespace: "forge-apps", BaseDomain: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.rolloutTimeoutError(context.Canceled, "workload", testWorkload()); failure.IsPermanent(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled rollout error = %v", err)
	}
	if err := runtime.rolloutTimeoutError(context.DeadlineExceeded, "workload", testWorkload()); !failure.IsPermanent(err) {
		t.Fatalf("deadline rollout error = %v, want permanent", err)
	}
}

func testWorkload() Workload {
	return Workload{
		DeploymentID: "deployment-id", ProjectID: "project-id", ProjectSlug: "demo-api",
		ServiceID: "11111111-2222-3333-4444-555555555555", ServiceName: "web",
		Image: "forge/demo:deployment-id", Port: 8080, Replicas: 1, HealthPath: "/health",
		CPURequest: "100m", CPULimit: "500m", MemoryRequest: "128Mi", MemoryLimit: "512Mi",
		Environment: map[string]string{"GREETING": "hello"},
	}
}
