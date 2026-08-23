package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/deployments"
	"github.com/parthpandya5/forge-platform/internal/domain"
	forgemetrics "github.com/parthpandya5/forge-platform/internal/metrics"
	"github.com/parthpandya5/forge-platform/internal/store"
)

type JobStore interface {
	ClaimJob(context.Context, string, time.Duration) (*domain.Job, error)
	HeartbeatJob(context.Context, int64, string, time.Duration) error
	CompleteJob(context.Context, int64, string) error
	RetryJob(context.Context, domain.Job, string, string, time.Duration) (bool, error)
	FailJob(context.Context, int64, string, string) error
	AbandonJob(context.Context, int64, string, time.Duration) error
	TryServiceDeploymentLock(context.Context, uuid.UUID) (func(context.Context) error, bool, error)
	QueueDepth(context.Context) (int64, error)
	AppendEvent(context.Context, uuid.UUID, string, string, map[string]any) error
}

type Processor interface {
	Process(context.Context, uuid.UUID) error
	MarkFailed(context.Context, uuid.UUID, error) error
}

type Config struct {
	WorkerID     string
	Concurrency  int
	Lease        time.Duration
	Heartbeat    time.Duration
	PollInterval time.Duration
}

type Coordinator struct {
	store            JobStore
	processor        Processor
	logger           *slog.Logger
	metrics          *forgemetrics.Metrics
	config           Config
	leaseOwnerPrefix string
}

func New(store JobStore, processor Processor, logger *slog.Logger, metrics *forgemetrics.Metrics, cfg Config) *Coordinator {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	return &Coordinator{
		store: store, processor: processor, logger: logger, metrics: metrics, config: cfg,
		leaseOwnerPrefix: cfg.WorkerID + "/" + uuid.NewString(),
	}
}

func (c *Coordinator) Run(ctx context.Context) error {
	if c.config.Concurrency < 1 {
		return errors.New("worker concurrency must be positive")
	}
	if c.config.Heartbeat <= 0 || c.config.Lease <= c.config.Heartbeat {
		return errors.New("job lease must be longer than a positive heartbeat interval")
	}
	c.logger.Info("worker started", "concurrency", c.config.Concurrency)
	var wait sync.WaitGroup
	wait.Add(c.config.Concurrency + 1)
	for index := 0; index < c.config.Concurrency; index++ {
		go func(slot int) {
			defer wait.Done()
			c.runSlot(ctx, slot)
		}(index)
	}
	go func() {
		defer wait.Done()
		c.observeQueue(ctx)
	}()
	wait.Wait()
	c.logger.Info("worker stopped")
	return nil
}

func (c *Coordinator) runSlot(ctx context.Context, slot int) {
	workerID := slotWorkerID(c.leaseOwnerPrefix, slot)
	logger := c.logger.With("worker_slot", slot, "lease_owner", workerID)
	for ctx.Err() == nil {
		claimCtx, claimCancel := context.WithTimeout(ctx, 10*time.Second)
		job, err := c.store.ClaimJob(claimCtx, workerID, c.config.Lease)
		claimCancel()
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("claim job", "error", err)
			}
			if !waitFor(ctx, c.config.PollInterval) {
				return
			}
			continue
		}
		if job == nil {
			if !waitFor(ctx, c.config.PollInterval) {
				return
			}
			continue
		}
		c.processJob(ctx, logger.With("job_id", job.ID, "deployment_id", job.DeploymentID, "attempt", job.Attempts), *job)
	}
}

func (c *Coordinator) processJob(parent context.Context, logger *slog.Logger, job domain.Job) {
	started := time.Now()
	if job.LockedBy == nil || *job.LockedBy == "" {
		logger.Error("claimed job has no lease owner")
		c.observeJob("invalid_claim", started)
		return
	}
	workerID := *job.LockedBy
	if c.metrics != nil {
		c.metrics.ActiveDeployments.Inc()
		defer c.metrics.ActiveDeployments.Dec()
	}

	lockCtx, lockCancel := context.WithTimeout(parent, 5*time.Second)
	unlock, acquired, err := c.store.TryServiceDeploymentLock(lockCtx, job.DeploymentID)
	lockCancel()
	if err != nil || !acquired {
		if err != nil {
			logger.Error("acquire service deployment lock", "error", err)
		}
		c.abandon(job, workerID, 2*time.Second, logger)
		c.observeJob("lock_contended", started)
		return
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := unlock(unlockCtx); err != nil {
			logger.Error("release service deployment lock", "error", err)
		}
	}()

	processCtx, cancel := context.WithCancel(parent)
	heartbeatDone := make(chan struct{})
	heartbeatResult := make(chan error, 1)
	go c.heartbeat(processCtx, cancel, job.ID, workerID, heartbeatDone, heartbeatResult)
	err = c.processor.Process(processCtx, job.DeploymentID)
	close(heartbeatDone)
	heartbeatErr := <-heartbeatResult
	cancel()
	if heartbeatErr != nil {
		// Once a heartbeat fails, this worker can no longer prove that it owns the
		// lease. A replacement may already have reclaimed it, so neither retry nor
		// failure finalization is safe here. The service advisory lock fences that
		// replacement until this function returns; lease expiry then drives recovery.
		logger.Warn("job lease lost", "error", heartbeatErr, "process_error", err)
		c.observeJob("lease_lost", started)
		return
	}

	if parent.Err() != nil {
		c.abandon(job, workerID, 0, logger)
		c.observeJob("shutdown", started)
		return
	}
	if err == nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer finalizeCancel()
		if completeErr := c.store.CompleteJob(finalizeCtx, job.ID, workerID); completeErr != nil {
			logger.Error("complete job", "error", completeErr)
			c.observeJob("completion_error", started)
			return
		}
		logger.Info("deployment job completed", "duration", time.Since(started))
		if c.metrics != nil {
			c.metrics.DeploymentDuration.Observe(time.Since(job.CreatedAt).Seconds())
		}
		c.observeJob("completed", started)
		return
	}

	logger.Warn("deployment job failed", "error", err)
	finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer finalizeCancel()
	permanent := deployments.IsPermanent(err) || job.Attempts >= job.MaxAttempts
	if permanent {
		if markErr := c.processor.MarkFailed(finalizeCtx, job.DeploymentID, err); markErr != nil {
			logger.Error("mark deployment failed", "error", markErr)
			c.abandon(job, workerID, 0, logger)
			c.observeJob("finalization_error", started)
			return
		}
		if failErr := c.store.FailJob(finalizeCtx, job.ID, workerID, err.Error()); failErr != nil {
			logger.Error("mark job failed", "error", failErr)
			c.observeJob("finalization_error", started)
			return
		}
		if c.metrics != nil {
			c.metrics.DeploymentFailures.Inc()
			c.metrics.DeploymentDuration.Observe(time.Since(job.CreatedAt).Seconds())
		}
		c.observeJob("failed", started)
		return
	}

	delay := retryDelay(job.Attempts)
	message := fmt.Sprintf("Deployment attempt %d failed; retrying in %s", job.Attempts, delay.Round(time.Millisecond))
	if eventErr := c.store.AppendEvent(finalizeCtx, job.DeploymentID, "deployment.retry_scheduled", message, map[string]any{"attempt": job.Attempts, "delay_ms": delay.Milliseconds()}); eventErr != nil {
		logger.Error("record retry event", "error", eventErr)
	}
	if _, retryErr := c.store.RetryJob(finalizeCtx, job, workerID, err.Error(), delay); retryErr != nil {
		logger.Error("schedule retry", "error", retryErr)
		c.observeJob("retry_error", started)
		return
	}
	c.observeJob("retry", started)
}

func (c *Coordinator) heartbeat(ctx context.Context, cancel context.CancelFunc, jobID int64, workerID string, done <-chan struct{}, result chan<- error) {
	var finalError error
	defer func() { result <- finalError }()
	ticker := time.NewTicker(c.config.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, c.config.Heartbeat)
			err := c.store.HeartbeatJob(heartbeatCtx, jobID, workerID, c.config.Lease)
			heartbeatCancel()
			if err != nil {
				finalError = err
				cancel()
				return
			}
		}
	}
}

func (c *Coordinator) abandon(job domain.Job, workerID string, delay time.Duration, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.store.AbandonJob(ctx, job.ID, workerID, delay); err != nil {
		logger.Error("abandon job lease", "error", err)
	}
}

func slotWorkerID(workerID string, slot int) string {
	return fmt.Sprintf("%s/slot-%d", workerID, slot)
}

func (c *Coordinator) observeQueue(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		queryCtx, queryCancel := context.WithTimeout(ctx, 5*time.Second)
		depth, err := c.store.QueueDepth(queryCtx)
		queryCancel()
		if err == nil && c.metrics != nil {
			c.metrics.QueueDepth.Set(float64(depth))
		} else if err != nil && ctx.Err() == nil {
			c.logger.Warn("observe queue depth", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) observeJob(outcome string, started time.Time) {
	if c.metrics == nil {
		return
	}
	c.metrics.WorkerJobs.WithLabelValues(outcome).Inc()
	c.metrics.WorkerJobDuration.WithLabelValues(outcome).Observe(time.Since(started).Seconds())
}

func retryDelay(attempt int) time.Duration {
	base := store.Backoff(attempt)
	return base + time.Duration(rand.Float64()*float64(base)/2)
}

func waitFor(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
