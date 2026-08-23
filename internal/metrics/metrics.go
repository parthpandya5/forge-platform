package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	HTTPDuration       *prometheus.HistogramVec
	Deployments        *prometheus.CounterVec
	DeploymentDuration prometheus.Histogram
	BuildDuration      prometheus.Histogram
	DeploymentFailures prometheus.Counter
	ActiveDeployments  prometheus.Gauge
	QueueDepth         prometheus.Gauge
	WorkerJobs         *prometheus.CounterVec
	WorkerJobDuration  *prometheus.HistogramVec
}

func NewAPI(registerer prometheus.Registerer) *Metrics {
	metrics := &Metrics{
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "forge_http_request_duration_seconds", Help: "Forge HTTP request latency by route, method, and status.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "status"}),
		Deployments: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "forge_deployments_total", Help: "Deployment requests by kind.",
		}, []string{"kind"}),
	}
	registerer.MustRegister(metrics.HTTPDuration, metrics.Deployments)
	return metrics
}

func NewWorker(registerer prometheus.Registerer) *Metrics {
	metrics := &Metrics{
		DeploymentDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "forge_deployment_duration_seconds", Help: "Elapsed time from durable enqueue to a terminal deployment outcome.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200},
		}),
		BuildDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "forge_build_duration_seconds", Help: "Docker image build duration per execution attempt.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200},
		}),
		DeploymentFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "forge_deployment_failures_total", Help: "Permanently failed deployments.",
		}),
		ActiveDeployments: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "forge_active_deployments", Help: "Deployments currently being processed by this worker process.",
		}),
		QueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "forge_worker_queue_depth", Help: "Immediately claimable deployment jobs.",
		}),
		WorkerJobs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "forge_worker_jobs_total", Help: "Worker jobs by outcome.",
		}, []string{"outcome"}),
		WorkerJobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "forge_worker_job_duration_seconds", Help: "Worker job duration by outcome.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200},
		}, []string{"outcome"}),
	}
	registerer.MustRegister(
		metrics.DeploymentDuration, metrics.BuildDuration, metrics.DeploymentFailures,
		metrics.ActiveDeployments, metrics.QueueDepth, metrics.WorkerJobs, metrics.WorkerJobDuration,
	)
	return metrics
}

func (m *Metrics) ObserveHTTP(route, method string, status int, started time.Time) {
	if route == "" {
		route = "unmatched"
	}
	m.HTTPDuration.WithLabelValues(route, method, strconv.Itoa(status)).Observe(time.Since(started).Seconds())
}
