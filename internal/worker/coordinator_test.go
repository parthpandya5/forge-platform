package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
	"github.com/parthpandya5/forge-platform/internal/failure"
)

func TestRetryDelayUsesCappedJitterRange(t *testing.T) {
	for _, attempt := range []int{1, 2, 10, 100} {
		got := retryDelay(attempt)
		base := time.Second << min(max(attempt, 1)-1, 6)
		if got < base || got >= base+base/2 {
			t.Fatalf("retryDelay(%d) = %s, want [%s, %s)", attempt, got, base, base+base/2)
		}
	}
}

func TestSlotWorkerIDFencesConcurrentSlots(t *testing.T) {
	first := slotWorkerID("worker-a", 0)
	second := slotWorkerID("worker-a", 1)
	if first == second || first != "worker-a/slot-0" || second != "worker-a/slot-1" {
		t.Fatalf("slot worker IDs = %q and %q", first, second)
	}
}

func TestProcessJobCompletesSuccessfulWork(t *testing.T) {
	jobStore := &fakeJobStore{lockAcquired: true}
	processor := &fakeProcessor{}
	coordinator := testCoordinator(jobStore, processor)
	coordinator.processJob(context.Background(), discardLogger(), testJob())
	if jobStore.completed != 1 || jobStore.retried != 0 || processor.markedFailed != 0 {
		t.Fatalf("completed=%d retried=%d marked_failed=%d", jobStore.completed, jobStore.retried, processor.markedFailed)
	}
}

func TestProcessJobSchedulesTransientRetry(t *testing.T) {
	jobStore := &fakeJobStore{lockAcquired: true}
	processor := &fakeProcessor{processErr: errors.New("temporary registry outage")}
	coordinator := testCoordinator(jobStore, processor)
	coordinator.processJob(context.Background(), discardLogger(), testJob())
	if jobStore.retried != 1 || jobStore.events != 1 || jobStore.failed != 0 || processor.markedFailed != 0 {
		t.Fatalf("retried=%d events=%d failed=%d marked_failed=%d", jobStore.retried, jobStore.events, jobStore.failed, processor.markedFailed)
	}
}

func TestProcessJobFinalizesPermanentFailure(t *testing.T) {
	jobStore := &fakeJobStore{lockAcquired: true}
	processor := &fakeProcessor{processErr: failure.Permanent(errors.New("invalid Dockerfile"))}
	coordinator := testCoordinator(jobStore, processor)
	coordinator.processJob(context.Background(), discardLogger(), testJob())
	if jobStore.failed != 1 || jobStore.retried != 0 || processor.markedFailed != 1 {
		t.Fatalf("failed=%d retried=%d marked_failed=%d", jobStore.failed, jobStore.retried, processor.markedFailed)
	}
}

func TestProcessJobFinalizesExhaustedTransientFailure(t *testing.T) {
	jobStore := &fakeJobStore{lockAcquired: true}
	processor := &fakeProcessor{processErr: errors.New("registry still unavailable")}
	coordinator := testCoordinator(jobStore, processor)
	job := testJob()
	job.Attempts = job.MaxAttempts
	coordinator.processJob(context.Background(), discardLogger(), job)
	if jobStore.failed != 1 || jobStore.retried != 0 || processor.markedFailed != 1 {
		t.Fatalf("failed=%d retried=%d marked_failed=%d", jobStore.failed, jobStore.retried, processor.markedFailed)
	}
}

func TestHeartbeatLossCancelsWorkWithoutStaleFinalization(t *testing.T) {
	jobStore := &fakeJobStore{lockAcquired: true, heartbeatErr: errors.New("lease ownership lost")}
	processor := &fakeProcessor{waitForCancellation: true}
	coordinator := New(jobStore, processor, discardLogger(), nil, Config{
		WorkerID: "worker-test", Concurrency: 1, Lease: 50 * time.Millisecond, Heartbeat: time.Millisecond,
	})
	coordinator.processJob(context.Background(), discardLogger(), testJob())
	if !processor.cancelled || jobStore.retried != 0 || jobStore.failed != 0 || jobStore.completed != 0 || processor.markedFailed != 0 {
		t.Fatalf("cancelled=%t retried=%d failed=%d completed=%d marked_failed=%d",
			processor.cancelled, jobStore.retried, jobStore.failed, jobStore.completed, processor.markedFailed)
	}
}

func TestShutdownAbandonsClaimWithoutFinalizing(t *testing.T) {
	jobStore := &fakeJobStore{lockAcquired: true}
	processor := &fakeProcessor{}
	coordinator := testCoordinator(jobStore, processor)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coordinator.processJob(ctx, discardLogger(), testJob())
	if jobStore.abandoned != 1 || jobStore.completed != 0 || jobStore.failed != 0 {
		t.Fatalf("abandoned=%d completed=%d failed=%d", jobStore.abandoned, jobStore.completed, jobStore.failed)
	}
}

func TestProcessJobAbandonsContendedDeploymentLock(t *testing.T) {
	jobStore := &fakeJobStore{lockAcquired: false}
	processor := &fakeProcessor{}
	coordinator := testCoordinator(jobStore, processor)
	coordinator.processJob(context.Background(), discardLogger(), testJob())
	if jobStore.abandoned != 1 || processor.processed != 0 {
		t.Fatalf("abandoned=%d processed=%d", jobStore.abandoned, processor.processed)
	}
}

func testCoordinator(jobStore *fakeJobStore, processor *fakeProcessor) *Coordinator {
	return New(jobStore, processor, discardLogger(), nil, Config{
		WorkerID: "worker-test", Concurrency: 1, Lease: time.Minute, Heartbeat: 30 * time.Second,
	})
}

func testJob() domain.Job {
	owner := "worker-test/slot-0"
	return domain.Job{ID: 1, DeploymentID: uuid.New(), Attempts: 1, MaxAttempts: 3, LockedBy: &owner, CreatedAt: time.Now()}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeJobStore struct {
	lockAcquired bool
	completed    int
	retried      int
	failed       int
	abandoned    int
	events       int
	heartbeatErr error
}

func (f *fakeJobStore) ClaimJob(context.Context, string, time.Duration) (*domain.Job, error) {
	return nil, nil
}
func (f *fakeJobStore) HeartbeatJob(context.Context, int64, string, time.Duration) error {
	return f.heartbeatErr
}
func (f *fakeJobStore) CompleteJob(context.Context, int64, string) error {
	f.completed++
	return nil
}
func (f *fakeJobStore) RetryJob(context.Context, domain.Job, string, string, time.Duration) (bool, error) {
	f.retried++
	return false, nil
}
func (f *fakeJobStore) FailJob(context.Context, int64, string, string) error {
	f.failed++
	return nil
}
func (f *fakeJobStore) AbandonJob(context.Context, int64, string, time.Duration) error {
	f.abandoned++
	return nil
}
func (f *fakeJobStore) TryServiceDeploymentLock(context.Context, uuid.UUID) (func(context.Context) error, bool, error) {
	return func(context.Context) error { return nil }, f.lockAcquired, nil
}
func (f *fakeJobStore) QueueDepth(context.Context) (int64, error) { return 0, nil }
func (f *fakeJobStore) AppendEvent(context.Context, uuid.UUID, string, string, map[string]any) error {
	f.events++
	return nil
}

type fakeProcessor struct {
	processErr          error
	processed           int
	markedFailed        int
	waitForCancellation bool
	cancelled           bool
}

func (f *fakeProcessor) Process(ctx context.Context, _ uuid.UUID) error {
	f.processed++
	if f.waitForCancellation {
		<-ctx.Done()
		f.cancelled = true
		return ctx.Err()
	}
	return f.processErr
}
func (f *fakeProcessor) MarkFailed(context.Context, uuid.UUID, error) error {
	f.markedFailed++
	return nil
}
