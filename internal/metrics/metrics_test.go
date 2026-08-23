package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAPIMetricFamiliesAreRegistered(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewAPI(registry)
	metrics.ObserveHTTP("/health", "GET", 200, time.Now())
	metrics.Deployments.WithLabelValues("deploy").Inc()
	assertMetricFamilies(t, registry, []string{
		"forge_http_request_duration_seconds",
		"forge_deployments_total",
	})
}

func TestWorkerMetricFamiliesAreRegistered(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewWorker(registry)
	metrics.DeploymentDuration.Observe(1)
	metrics.BuildDuration.Observe(1)
	metrics.DeploymentFailures.Inc()
	metrics.ActiveDeployments.Set(1)
	metrics.QueueDepth.Set(1)
	metrics.WorkerJobs.WithLabelValues("completed").Inc()
	metrics.WorkerJobDuration.WithLabelValues("completed").Observe(1)
	assertMetricFamilies(t, registry, []string{
		"forge_deployment_duration_seconds",
		"forge_build_duration_seconds",
		"forge_deployment_failures_total",
		"forge_active_deployments",
		"forge_worker_queue_depth",
		"forge_worker_jobs_total",
		"forge_worker_job_duration_seconds",
	})
}

func assertMetricFamilies(t *testing.T, registry *prometheus.Registry, required []string) {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]struct{}, len(families))
	for _, family := range families {
		got[family.GetName()] = struct{}{}
	}
	for _, name := range required {
		if _, exists := got[name]; !exists {
			t.Errorf("metric family %q was not gathered", name)
		}
	}
}
