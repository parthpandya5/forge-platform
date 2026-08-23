package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/parthpandya5/forge-platform/internal/domain"
)

const jobColumns = `
	id, deployment_id, job_type, state, attempts, max_attempts, available_at,
	locked_by, lease_expires_at, last_error, created_at, updated_at, completed_at`

func (s *Store) ClaimJob(ctx context.Context, workerID string, lease time.Duration) (*domain.Job, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin job claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var job domain.Job
	err = tx.QueryRow(ctx, `WITH candidate AS (
		SELECT j.id FROM jobs j
		JOIN deployments d ON d.id = j.deployment_id
		WHERE (
			(j.state = 'available' AND j.attempts < j.max_attempts AND j.available_at <= now())
			OR (j.state = 'running' AND j.lease_expires_at <= now())
		) AND NOT EXISTS (
			SELECT 1 FROM jobs earlier
			JOIN deployments earlier_deployment ON earlier_deployment.id = earlier.deployment_id
			WHERE earlier_deployment.service_id = d.service_id
				AND earlier.id < j.id
				AND ((earlier.state = 'available' AND earlier.attempts < earlier.max_attempts)
					OR earlier.state = 'running')
		)
		ORDER BY j.available_at, j.id
		FOR UPDATE OF j SKIP LOCKED
		LIMIT 1
	)
	UPDATE jobs j SET state = 'running',
		attempts = CASE WHEN j.state = 'available' THEN j.attempts + 1 ELSE j.attempts END,
		locked_by = $1,
		lease_expires_at = now() + $2::interval, updated_at = now()
	FROM candidate WHERE j.id = candidate.id
	RETURNING `+qualifiedJobColumns("j"), workerID, lease.String()).Scan(jobDestinations(&job)...)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("claim job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit job claim: %w", err)
	}
	return &job, nil
}

func (s *Store) HeartbeatJob(ctx context.Context, jobID int64, workerID string, lease time.Duration) error {
	result, err := s.pool.Exec(ctx, `UPDATE jobs SET lease_expires_at = now() + $3::interval, updated_at = now()
		WHERE id = $1 AND state = 'running' AND locked_by = $2`, jobID, workerID, lease.String())
	if err != nil {
		return fmt.Errorf("heartbeat job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) CompleteJob(ctx context.Context, jobID int64, workerID string) error {
	result, err := s.pool.Exec(ctx, `UPDATE jobs SET state = 'completed', locked_by = NULL,
		lease_expires_at = NULL, completed_at = now(), updated_at = now()
		WHERE id = $1 AND state = 'running' AND locked_by = $2`, jobID, workerID)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) RetryJob(ctx context.Context, job domain.Job, workerID, reason string, delay time.Duration) (bool, error) {
	reason = boundedText(reason, 4000)
	exhausted := job.Attempts >= job.MaxAttempts
	state := "available"
	if exhausted {
		state = "failed"
	}
	result, err := s.pool.Exec(ctx, `UPDATE jobs SET state = $3, available_at = now() + $4::interval,
		locked_by = NULL, lease_expires_at = NULL, last_error = $5, updated_at = now(),
		completed_at = CASE WHEN $3 = 'failed' THEN now() ELSE completed_at END
		WHERE id = $1 AND state = 'running' AND locked_by = $2`, job.ID, workerID, state, delay.String(), reason)
	if err != nil {
		return false, fmt.Errorf("retry job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return false, ErrConflict
	}
	return exhausted, nil
}

func (s *Store) FailJob(ctx context.Context, jobID int64, workerID, reason string) error {
	reason = boundedText(reason, 4000)
	result, err := s.pool.Exec(ctx, `UPDATE jobs SET state = 'failed', locked_by = NULL,
		lease_expires_at = NULL, last_error = $3, completed_at = now(), updated_at = now()
		WHERE id = $1 AND state = 'running' AND locked_by = $2`, jobID, workerID, reason)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) AbandonJob(ctx context.Context, jobID int64, workerID string, delay time.Duration) error {
	result, err := s.pool.Exec(ctx, `UPDATE jobs SET state = 'available', available_at = now() + $3::interval,
		attempts = greatest(attempts - 1, 0), locked_by = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $1 AND state = 'running' AND locked_by = $2`, jobID, workerID, delay.String())
	if err != nil {
		return fmt.Errorf("abandon job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) QueueDepth(ctx context.Context) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM jobs j
		JOIN deployments d ON d.id = j.deployment_id
		WHERE (
			(j.state = 'available' AND j.attempts < j.max_attempts AND j.available_at <= now())
			OR (j.state = 'running' AND j.lease_expires_at <= now())
		) AND NOT EXISTS (
			SELECT 1 FROM jobs earlier
			JOIN deployments earlier_deployment ON earlier_deployment.id = earlier.deployment_id
			WHERE earlier_deployment.service_id = d.service_id
				AND earlier.id < j.id
				AND ((earlier.state = 'available' AND earlier.attempts < earlier.max_attempts)
					OR earlier.state = 'running')
		)`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("query queue depth: %w", err)
	}
	return count, nil
}

func (s *Store) TryServiceDeploymentLock(ctx context.Context, deploymentID uuid.UUID) (func(context.Context) error, bool, error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire service deployment lock connection: %w", err)
	}
	var serviceID string
	if err := connection.QueryRow(ctx, `SELECT service_id::text FROM deployments WHERE id = $1`, deploymentID).Scan(&serviceID); err != nil {
		connection.Release()
		return nil, false, fmt.Errorf("resolve deployment service lock: %w", mapError(err))
	}
	var acquired bool
	err = connection.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, serviceID).Scan(&acquired)
	if err != nil {
		connection.Release()
		return nil, false, fmt.Errorf("acquire service deployment advisory lock: %w", err)
	}
	if !acquired {
		connection.Release()
		return nil, false, nil
	}
	unlock := func(unlockCtx context.Context) error {
		var released bool
		if err := connection.QueryRow(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, serviceID).Scan(&released); err != nil {
			// A session-level lock must never return to the pool if its explicit
			// unlock failed. Hijacking and closing guarantees PostgreSQL releases
			// the lock with the connection rather than poisoning a pooled session.
			raw := connection.Hijack()
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closeErr := raw.Close(closeCtx)
			return errors.Join(fmt.Errorf("release service deployment advisory lock: %w", err), closeErr)
		}
		connection.Release()
		if !released {
			return fmt.Errorf("release service deployment advisory lock: lock was not held")
		}
		return nil
	}
	return unlock, true, nil
}

func qualifiedJobColumns(alias string) string {
	return alias + ".id, " + alias + ".deployment_id, " + alias + ".job_type, " + alias + ".state, " +
		alias + ".attempts, " + alias + ".max_attempts, " + alias + ".available_at, " +
		alias + ".locked_by, " + alias + ".lease_expires_at, " + alias + ".last_error, " +
		alias + ".created_at, " + alias + ".updated_at, " + alias + ".completed_at"
}

func jobDestinations(job *domain.Job) []any {
	return []any{
		&job.ID, &job.DeploymentID, &job.JobType, &job.State, &job.Attempts, &job.MaxAttempts,
		&job.AvailableAt, &job.LockedBy, &job.LeaseExpiresAt, &job.LastError, &job.CreatedAt,
		&job.UpdatedAt, &job.CompletedAt,
	}
}
