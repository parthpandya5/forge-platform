package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/parthpandya5/forge-platform/internal/domain"
)

func (s *Store) CreateWebhookDeployments(ctx context.Context, deliveryID, repositoryURL, branch, commitSHA, requestID string, maxAttempts int) ([]domain.Deployment, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin webhook delivery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `INSERT INTO webhook_deliveries (delivery_id, event_type)
		VALUES ($1, 'push') ON CONFLICT DO NOTHING`, deliveryID)
	if err != nil {
		return nil, false, fmt.Errorf("record webhook delivery: %w", err)
	}
	if result.RowsAffected() == 0 {
		return []domain.Deployment{}, true, nil
	}

	rows, err := tx.Query(ctx, `SELECT s.id FROM services s JOIN projects p ON p.id = s.project_id
		WHERE p.auto_deploy = true AND p.repository_url = $1 AND p.repository_branch = $2
		ORDER BY s.id`, repositoryURL, branch)
	if err != nil {
		return nil, false, fmt.Errorf("find auto-deploy services: %w", err)
	}
	var serviceIDs []uuid.UUID
	for rows.Next() {
		var serviceID uuid.UUID
		if err := rows.Scan(&serviceID); err != nil {
			rows.Close()
			return nil, false, fmt.Errorf("scan auto-deploy service: %w", err)
		}
		serviceIDs = append(serviceIDs, serviceID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, fmt.Errorf("iterate auto-deploy services: %w", err)
	}
	rows.Close()

	deployments := make([]domain.Deployment, 0, len(serviceIDs))
	ref := commitSHA
	for _, serviceID := range serviceIDs {
		deployment, err := insertDeployment(ctx, tx, serviceID, &ref, nil, requestID, maxAttempts)
		if err != nil {
			return nil, false, err
		}
		deployments = append(deployments, deployment)
	}
	outcome := fmt.Sprintf("enqueued:%d", len(deployments))
	if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET processed_at = now(), outcome = $2 WHERE delivery_id = $1`, deliveryID, outcome); err != nil {
		return nil, false, fmt.Errorf("complete webhook delivery: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit webhook delivery: %w", err)
	}
	return deployments, false, nil
}
