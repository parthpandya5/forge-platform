package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
)

type EncryptedEnvironmentVariable struct {
	EnvironmentVariable domain.EnvironmentVariable
	Ciphertext          []byte
	Nonce               []byte
}

func (s *Store) UpsertEnvironmentVariable(ctx context.Context, userID, serviceID uuid.UUID, key string, ciphertext, nonce []byte) (domain.EnvironmentVariable, error) {
	id := uuid.New()
	var variable domain.EnvironmentVariable
	err := s.pool.QueryRow(ctx, `INSERT INTO environment_variables (id, service_id, key, encrypted_value, nonce)
		SELECT $1, s.id, $3, $4, $5 FROM services s
		JOIN projects p ON p.id = s.project_id WHERE s.id = $2 AND p.user_id = $6
		ON CONFLICT (service_id, key) DO UPDATE SET encrypted_value = EXCLUDED.encrypted_value,
			nonce = EXCLUDED.nonce, updated_at = now()
		RETURNING id, service_id, key, created_at, updated_at`, id, serviceID, key, ciphertext, nonce, userID).Scan(
		&variable.ID, &variable.ServiceID, &variable.Key, &variable.CreatedAt, &variable.UpdatedAt,
	)
	if err != nil {
		return domain.EnvironmentVariable{}, fmt.Errorf("upsert environment variable: %w", mapError(err))
	}
	return variable, nil
}

func (s *Store) ListEnvironmentVariables(ctx context.Context, userID, serviceID uuid.UUID) ([]domain.EnvironmentVariable, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id, e.service_id, e.key, e.created_at, e.updated_at
		FROM environment_variables e JOIN services s ON s.id = e.service_id
		JOIN projects p ON p.id = s.project_id
		WHERE p.user_id = $1 AND e.service_id = $2 ORDER BY e.key`, userID, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list environment variables: %w", err)
	}
	defer rows.Close()
	variables := make([]domain.EnvironmentVariable, 0)
	for rows.Next() {
		var variable domain.EnvironmentVariable
		if err := rows.Scan(&variable.ID, &variable.ServiceID, &variable.Key, &variable.CreatedAt, &variable.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan environment variable: %w", err)
		}
		variables = append(variables, variable)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate environment variables: %w", err)
	}
	if len(variables) == 0 {
		if _, err := s.GetService(ctx, userID, serviceID); err != nil {
			return nil, err
		}
	}
	return variables, nil
}

func (s *Store) DeleteEnvironmentVariable(ctx context.Context, userID, serviceID, variableID uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM environment_variables e USING services s, projects p
		WHERE e.id = $1 AND e.service_id = $2 AND s.id = e.service_id
		AND p.id = s.project_id AND p.user_id = $3`, variableID, serviceID, userID)
	if err != nil {
		return fmt.Errorf("delete environment variable: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeploymentEnvironment(ctx context.Context, deploymentID uuid.UUID) ([]EncryptedEnvironmentVariable, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id, e.service_id, e.key, e.created_at, e.updated_at, e.encrypted_value, e.nonce
		FROM environment_variables e JOIN deployments d ON d.service_id = e.service_id
		WHERE d.id = $1 ORDER BY e.key`, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("load deployment environment: %w", err)
	}
	defer rows.Close()
	variables := make([]EncryptedEnvironmentVariable, 0)
	for rows.Next() {
		var item EncryptedEnvironmentVariable
		variable := &item.EnvironmentVariable
		if err := rows.Scan(&variable.ID, &variable.ServiceID, &variable.Key, &variable.CreatedAt, &variable.UpdatedAt, &item.Ciphertext, &item.Nonce); err != nil {
			return nil, fmt.Errorf("scan encrypted environment variable: %w", err)
		}
		variables = append(variables, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate encrypted environment variables: %w", err)
	}
	return variables, nil
}
