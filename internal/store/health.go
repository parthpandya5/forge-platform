package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
)

type HealthTarget struct {
	DeploymentID    uuid.UUID
	ProjectID       uuid.UUID
	ProjectSlug     string
	ServiceID       uuid.UUID
	ServiceName     string
	DesiredReplicas int
}

func (s *Store) ListHealthTargets(ctx context.Context, limit int) ([]HealthTarget, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id, p.id, p.slug, s.id, s.name, s.desired_replicas
		FROM services s JOIN projects p ON p.id = s.project_id
		JOIN LATERAL (
			SELECT id FROM deployments WHERE service_id = s.id AND status = 'LIVE'
			ORDER BY finished_at DESC, id DESC LIMIT 1
		) d ON true
		ORDER BY s.health_checked_at NULLS FIRST, s.id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list health targets: %w", err)
	}
	defer rows.Close()
	targets := make([]HealthTarget, 0)
	for rows.Next() {
		var target HealthTarget
		if err := rows.Scan(&target.DeploymentID, &target.ProjectID, &target.ProjectSlug, &target.ServiceID, &target.ServiceName, &target.DesiredReplicas); err != nil {
			return nil, fmt.Errorf("scan health target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate health targets: %w", err)
	}
	return targets, nil
}

func (s *Store) UpdateServiceHealth(ctx context.Context, serviceID uuid.UUID, status domain.ServiceHealthStatus, message string) error {
	message = boundedText(message, 2000)
	result, err := s.pool.Exec(ctx, `UPDATE services SET health_status = $2, health_message = NULLIF($3, ''),
		health_checked_at = now() WHERE id = $1`, serviceID, status, message)
	if err != nil {
		return fmt.Errorf("update service health: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListServiceIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM services ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list service IDs: %w", err)
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan service ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service IDs: %w", err)
	}
	return ids, nil
}
