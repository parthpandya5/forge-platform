package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/parthpandya5/forge-platform/internal/domain"
)

const deploymentColumns = `
	id, service_id, commit_sha, requested_ref, status, image_tag, route_url, rollback_of,
	request_id, error_message, created_at, started_at, finished_at, updated_at, version`

const deploymentSelectColumns = `
	d.id, d.service_id, d.commit_sha, d.requested_ref, d.status, d.image_tag, d.route_url, d.rollback_of,
	d.request_id, d.error_message, d.created_at, d.started_at, d.finished_at, d.updated_at, d.version`

type DeploymentContext struct {
	Deployment       domain.Deployment
	ProjectID        uuid.UUID
	ProjectName      string
	ProjectSlug      string
	RepositoryURL    string
	RepositoryBranch string
	Service          domain.Service
}

func (s *Store) CreateDeployment(ctx context.Context, userID, serviceID uuid.UUID, requestedRef, requestID string, maxAttempts int) (domain.Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("begin create deployment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var branch string
	err = tx.QueryRow(ctx, `SELECT p.repository_branch FROM services s JOIN projects p ON p.id = s.project_id
		WHERE s.id = $1 AND p.user_id = $2 FOR SHARE OF s, p`, serviceID, userID).Scan(&branch)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("find deployment service: %w", mapError(err))
	}
	if requestedRef == "" {
		requestedRef = branch
	}
	deployment, err := insertDeployment(ctx, tx, serviceID, &requestedRef, nil, requestID, maxAttempts)
	if err != nil {
		return domain.Deployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Deployment{}, fmt.Errorf("commit create deployment: %w", err)
	}
	return deployment, nil
}

func (s *Store) CreateRollback(ctx context.Context, userID, sourceDeploymentID uuid.UUID, requestID string, maxAttempts int) (domain.Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("begin rollback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var serviceID uuid.UUID
	var imageTag string
	var commitSHA *string
	err = tx.QueryRow(ctx, `SELECT d.service_id, d.image_tag, d.commit_sha
		FROM deployments d
		JOIN services s ON s.id = d.service_id
		JOIN projects p ON p.id = s.project_id
		WHERE d.id = $1 AND p.user_id = $2 AND d.status = 'LIVE' AND d.image_tag IS NOT NULL
		FOR SHARE OF d, s, p`, sourceDeploymentID, userID).Scan(&serviceID, &imageTag, &commitSHA)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("find rollback artifact: %w", mapError(err))
	}
	deployment, err := insertDeployment(ctx, tx, serviceID, nil, &sourceDeploymentID, requestID, maxAttempts)
	if err != nil {
		return domain.Deployment{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE deployments SET image_tag = $2, commit_sha = $3, updated_at = now(), version = version + 1 WHERE id = $1`, deployment.ID, imageTag, commitSHA)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("attach rollback artifact: %w", err)
	}
	deployment.ImageTag = &imageTag
	deployment.CommitSHA = commitSHA
	deployment.RollbackOf = &sourceDeploymentID
	deployment.Version++
	if err := tx.Commit(ctx); err != nil {
		return domain.Deployment{}, fmt.Errorf("commit rollback: %w", err)
	}
	return deployment, nil
}

func insertDeployment(ctx context.Context, tx pgx.Tx, serviceID uuid.UUID, requestedRef *string, rollbackOf *uuid.UUID, requestID string, maxAttempts int) (domain.Deployment, error) {
	// Serialize sequence allocation for one service so job identity is also its durable FIFO order.
	// Seed 1 deliberately differs from the worker's long-lived service lock (seed 0).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1))`, serviceID.String()); err != nil {
		return domain.Deployment{}, fmt.Errorf("acquire service enqueue lock: %w", err)
	}
	id := uuid.New()
	var deployment domain.Deployment
	err := tx.QueryRow(ctx, `INSERT INTO deployments (id, service_id, requested_ref, status, rollback_of, request_id)
		VALUES ($1, $2, $3, 'QUEUED', $4, $5)
		RETURNING `+deploymentColumns, id, serviceID, requestedRef, rollbackOf, requestID).Scan(deploymentDestinations(&deployment)...)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("insert deployment: %w", mapError(err))
	}
	message := "Deployment queued"
	if rollbackOf != nil {
		message = "Rollback deployment queued"
	}
	if err := insertEvent(ctx, tx, id, "deployment.queued", message, map[string]any{"request_id": requestID}); err != nil {
		return domain.Deployment{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO jobs (deployment_id, max_attempts) VALUES ($1, $2)`, id, maxAttempts)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("enqueue deployment: %w", mapError(err))
	}
	return deployment, nil
}

func (s *Store) ListDeployments(ctx context.Context, userID, serviceID uuid.UUID, limit, offset int) ([]domain.Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deploymentSelectColumns+` FROM deployments d
		JOIN services s ON s.id = d.service_id JOIN projects p ON p.id = s.project_id
		WHERE p.user_id = $1 AND d.service_id = $2
		ORDER BY d.created_at DESC, d.id DESC LIMIT $3 OFFSET $4`, userID, serviceID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()
	deployments := make([]domain.Deployment, 0)
	for rows.Next() {
		deployment, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		deployments = append(deployments, deployment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deployments: %w", err)
	}
	if len(deployments) == 0 {
		if _, err := s.GetService(ctx, userID, serviceID); err != nil {
			return nil, err
		}
	}
	return deployments, nil
}

func (s *Store) GetDeployment(ctx context.Context, userID, deploymentID uuid.UUID) (domain.Deployment, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+deploymentSelectColumns+` FROM deployments d
		JOIN services s ON s.id = d.service_id JOIN projects p ON p.id = s.project_id
		WHERE p.user_id = $1 AND d.id = $2`, userID, deploymentID)
	deployment, err := scanDeployment(row)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("get deployment: %w", mapError(err))
	}
	return deployment, nil
}

func (s *Store) GetDeploymentContext(ctx context.Context, deploymentID uuid.UUID) (DeploymentContext, error) {
	var result DeploymentContext
	row := s.pool.QueryRow(ctx, `SELECT `+deploymentSelectColumns+`,
		p.id, p.name, p.slug, p.repository_url, p.repository_branch,
		s.id, s.project_id, s.name, s.service_type, s.internal_port, s.desired_replicas,
		s.health_check_path, s.cpu_request, s.cpu_limit, s.memory_request, s.memory_limit,
		s.health_status, s.health_message, s.health_checked_at, s.created_at, s.updated_at
		FROM deployments d JOIN services s ON s.id = d.service_id JOIN projects p ON p.id = s.project_id
		WHERE d.id = $1`, deploymentID)
	dests := deploymentDestinations(&result.Deployment)
	dests = append(dests,
		&result.ProjectID, &result.ProjectName, &result.ProjectSlug, &result.RepositoryURL, &result.RepositoryBranch,
		&result.Service.ID, &result.Service.ProjectID, &result.Service.Name, &result.Service.ServiceType,
		&result.Service.InternalPort, &result.Service.DesiredReplicas, &result.Service.HealthCheckPath,
		&result.Service.CPURequest, &result.Service.CPULimit, &result.Service.MemoryRequest, &result.Service.MemoryLimit,
		&result.Service.HealthStatus, &result.Service.HealthMessage, &result.Service.HealthCheckedAt,
		&result.Service.CreatedAt, &result.Service.UpdatedAt,
	)
	if err := row.Scan(dests...); err != nil {
		return DeploymentContext{}, fmt.Errorf("get deployment context: %w", mapError(err))
	}
	return result, nil
}

func (s *Store) TransitionDeployment(ctx context.Context, deploymentID uuid.UUID, to domain.DeploymentStatus, eventType, message string, failure error) (domain.Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("begin deployment transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from domain.DeploymentStatus
	var version int64
	if err := tx.QueryRow(ctx, `SELECT status, version FROM deployments WHERE id = $1 FOR UPDATE`, deploymentID).Scan(&from, &version); err != nil {
		return domain.Deployment{}, fmt.Errorf("lock deployment: %w", mapError(err))
	}
	if err := domain.ValidateTransition(from, to); err != nil {
		return domain.Deployment{}, err
	}
	var errorMessage *string
	if failure != nil {
		text := boundedText(failure.Error(), 4000)
		errorMessage = &text
	}
	var deployment domain.Deployment
	err = tx.QueryRow(ctx, `UPDATE deployments SET
		status = $2,
		started_at = CASE WHEN started_at IS NULL AND $2 <> 'QUEUED' THEN now() ELSE started_at END,
		finished_at = CASE WHEN $2 IN ('LIVE', 'FAILED', 'CANCELLED') THEN now() ELSE finished_at END,
		error_message = $3,
		updated_at = now(), version = version + 1
		WHERE id = $1 AND version = $4 RETURNING `+deploymentColumns,
		deploymentID, to, errorMessage, version).Scan(deploymentDestinations(&deployment)...)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("update deployment status: %w", mapError(err))
	}
	metadata := map[string]any{"from": from, "to": to}
	if err := insertEvent(ctx, tx, deploymentID, eventType, message, metadata); err != nil {
		return domain.Deployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Deployment{}, fmt.Errorf("commit deployment transition: %w", err)
	}
	return deployment, nil
}

func (s *Store) SetDeploymentSource(ctx context.Context, deploymentID uuid.UUID, commitSHA string) error {
	result, err := s.pool.Exec(ctx, `UPDATE deployments SET commit_sha = $2, updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'CLONING'`, deploymentID, commitSHA)
	if err != nil {
		return fmt.Errorf("set deployment source: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) SetDeploymentImage(ctx context.Context, deploymentID uuid.UUID, imageTag string) error {
	result, err := s.pool.Exec(ctx, `UPDATE deployments SET image_tag = $2, updated_at = now(), version = version + 1
		WHERE id = $1 AND status IN ('BUILDING', 'PUSHING')`, deploymentID, imageTag)
	if err != nil {
		return fmt.Errorf("set deployment image: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) SetDeploymentRoute(ctx context.Context, deploymentID uuid.UUID, routeURL string) error {
	result, err := s.pool.Exec(ctx, `UPDATE deployments SET route_url = $2, updated_at = now(), version = version + 1
		WHERE id = $1 AND status IN ('DEPLOYING', 'VERIFYING')`, deploymentID, routeURL)
	if err != nil {
		return fmt.Errorf("set deployment route: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func scanDeployment(row scanner) (domain.Deployment, error) {
	var deployment domain.Deployment
	err := row.Scan(deploymentDestinations(&deployment)...)
	return deployment, err
}

func deploymentDestinations(deployment *domain.Deployment) []any {
	return []any{
		&deployment.ID, &deployment.ServiceID, &deployment.CommitSHA, &deployment.RequestedRef,
		&deployment.Status, &deployment.ImageTag, &deployment.RouteURL, &deployment.RollbackOf,
		&deployment.RequestID, &deployment.ErrorMessage, &deployment.CreatedAt, &deployment.StartedAt,
		&deployment.FinishedAt, &deployment.UpdatedAt, &deployment.Version,
	}
}

func insertEvent(ctx context.Context, tx pgx.Tx, deploymentID uuid.UUID, eventType, message string, metadata map[string]any) error {
	eventType = boundedText(eventType, 200)
	message = boundedText(message, 4000)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode deployment event: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO deployment_events (deployment_id, event_type, message, metadata)
		VALUES ($1, $2, $3, $4)`, deploymentID, eventType, message, encoded)
	if err != nil {
		return fmt.Errorf("insert deployment event: %w", err)
	}
	return nil
}

func (s *Store) AppendEvent(ctx context.Context, deploymentID uuid.UUID, eventType, message string, metadata map[string]any) error {
	eventType = boundedText(eventType, 200)
	message = boundedText(message, 4000)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode deployment event: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO deployment_events (deployment_id, event_type, message, metadata)
		VALUES ($1, $2, $3, $4)`, deploymentID, eventType, message, encoded)
	if err != nil {
		return fmt.Errorf("append deployment event: %w", mapError(err))
	}
	return nil
}

func (s *Store) ListEvents(ctx context.Context, userID, deploymentID uuid.UUID, afterID int64, limit int) ([]domain.DeploymentEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id, e.deployment_id, e.event_type, e.message, e.metadata, e.created_at
		FROM deployment_events e JOIN deployments d ON d.id = e.deployment_id
		JOIN services s ON s.id = d.service_id JOIN projects p ON p.id = s.project_id
		WHERE p.user_id = $1 AND e.deployment_id = $2 AND e.id > $3 ORDER BY e.id LIMIT $4`, userID, deploymentID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list deployment events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.DeploymentEvent, 0)
	for rows.Next() {
		var event domain.DeploymentEvent
		var metadata []byte
		if err := rows.Scan(&event.ID, &event.DeploymentID, &event.EventType, &event.Message, &metadata, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan deployment event: %w", err)
		}
		if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
			return nil, fmt.Errorf("decode deployment event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deployment events: %w", err)
	}
	if len(events) == 0 {
		if _, err := s.GetDeployment(ctx, userID, deploymentID); err != nil {
			return nil, err
		}
	}
	return events, nil
}

func (s *Store) AppendLog(ctx context.Context, deploymentID uuid.UUID, stream, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	message = boundedText(message, 16384)
	_, err := s.pool.Exec(ctx, `INSERT INTO deployment_logs (deployment_id, stream, message) VALUES ($1, $2, $3)`, deploymentID, stream, message)
	if err != nil {
		return fmt.Errorf("append deployment log: %w", mapError(err))
	}
	return nil
}

func (s *Store) ListLogs(ctx context.Context, userID, deploymentID uuid.UUID, afterID int64, limit int) ([]domain.DeploymentLog, error) {
	rows, err := s.pool.Query(ctx, `SELECT l.id, l.deployment_id, l.stream, l.message, l.created_at
		FROM deployment_logs l JOIN deployments d ON d.id = l.deployment_id
		JOIN services s ON s.id = d.service_id JOIN projects p ON p.id = s.project_id
		WHERE p.user_id = $1 AND l.deployment_id = $2 AND l.id > $3 ORDER BY l.id LIMIT $4`, userID, deploymentID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list deployment logs: %w", err)
	}
	defer rows.Close()
	logs := make([]domain.DeploymentLog, 0)
	for rows.Next() {
		var log domain.DeploymentLog
		if err := rows.Scan(&log.ID, &log.DeploymentID, &log.Stream, &log.Message, &log.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan deployment log: %w", err)
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deployment logs: %w", err)
	}
	if len(logs) == 0 {
		if _, err := s.GetDeployment(ctx, userID, deploymentID); err != nil {
			return nil, err
		}
	}
	return logs, nil
}

func IsInvalidTransition(err error) bool {
	return errors.Is(err, domain.ErrInvalidTransition)
}

func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second << min(attempt-1, 6)
	return delay
}
