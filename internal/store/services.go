package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
)

const serviceColumns = `
	s.id, s.project_id, s.name, s.service_type, s.internal_port, s.desired_replicas,
	s.health_check_path, s.cpu_request, s.cpu_limit, s.memory_request, s.memory_limit,
	s.health_status, s.health_message, s.health_checked_at, s.created_at, s.updated_at`

func (s *Store) CreateService(ctx context.Context, userID, projectID uuid.UUID, input domain.CreateServiceInput) (domain.Service, error) {
	id := uuid.New()
	var service domain.Service
	err := s.pool.QueryRow(ctx, `
		INSERT INTO services (
			id, project_id, name, internal_port, desired_replicas, health_check_path,
			cpu_request, cpu_limit, memory_request, memory_limit
		)
		SELECT $1, p.id, $3, $4, $5, $6, $7, $8, $9, $10
		FROM projects p WHERE p.id = $2 AND p.user_id = $11
		RETURNING id, project_id, name, service_type, internal_port, desired_replicas,
			health_check_path, cpu_request, cpu_limit, memory_request, memory_limit,
			health_status, health_message, health_checked_at, created_at, updated_at
	`, id, projectID, input.Name, input.InternalPort, input.DesiredReplicas, input.HealthCheckPath,
		input.CPURequest, input.CPULimit, input.MemoryRequest, input.MemoryLimit, userID).Scan(
		&service.ID, &service.ProjectID, &service.Name, &service.ServiceType, &service.InternalPort,
		&service.DesiredReplicas, &service.HealthCheckPath, &service.CPURequest, &service.CPULimit,
		&service.MemoryRequest, &service.MemoryLimit, &service.HealthStatus, &service.HealthMessage,
		&service.HealthCheckedAt, &service.CreatedAt, &service.UpdatedAt,
	)
	if err != nil {
		return domain.Service{}, fmt.Errorf("create service: %w", mapError(err))
	}
	return service, nil
}

func (s *Store) ListServices(ctx context.Context, userID, projectID uuid.UUID) ([]domain.Service, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+serviceColumns+` FROM services s
		JOIN projects p ON p.id = s.project_id
		WHERE p.user_id = $1 AND p.id = $2 ORDER BY s.created_at, s.id`, userID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()
	services := make([]domain.Service, 0)
	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, fmt.Errorf("scan service: %w", err)
		}
		services = append(services, service)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate services: %w", err)
	}
	if len(services) == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND user_id = $2)`, projectID, userID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("verify project: %w", err)
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return services, nil
}

func (s *Store) GetService(ctx context.Context, userID, serviceID uuid.UUID) (domain.Service, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+serviceColumns+` FROM services s
		JOIN projects p ON p.id = s.project_id WHERE p.user_id = $1 AND s.id = $2`, userID, serviceID)
	service, err := scanService(row)
	if err != nil {
		return domain.Service{}, fmt.Errorf("get service: %w", mapError(err))
	}
	return service, nil
}

func (s *Store) UpdateService(ctx context.Context, userID, serviceID uuid.UUID, input domain.CreateServiceInput) (domain.Service, error) {
	var service domain.Service
	row := s.pool.QueryRow(ctx, `UPDATE services s SET name = $3, internal_port = $4,
		desired_replicas = $5, health_check_path = $6, cpu_request = $7, cpu_limit = $8,
		memory_request = $9, memory_limit = $10, updated_at = now()
		FROM projects p WHERE s.id = $1 AND p.id = s.project_id AND p.user_id = $2
		RETURNING `+serviceColumns, serviceID, userID, input.Name, input.InternalPort,
		input.DesiredReplicas, input.HealthCheckPath, input.CPURequest, input.CPULimit,
		input.MemoryRequest, input.MemoryLimit)
	if err := row.Scan(serviceDestinations(&service)...); err != nil {
		return domain.Service{}, fmt.Errorf("update service: %w", mapError(err))
	}
	return service, nil
}

func (s *Store) DeleteService(ctx context.Context, userID, serviceID uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM services s USING projects p
		WHERE s.id = $1 AND p.id = s.project_id AND p.user_id = $2`, serviceID, userID)
	if err != nil {
		return fmt.Errorf("delete service: %w", mapError(err))
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func scanService(row scanner) (domain.Service, error) {
	var service domain.Service
	err := row.Scan(serviceDestinations(&service)...)
	return service, err
}

func serviceDestinations(service *domain.Service) []any {
	return []any{
		&service.ID, &service.ProjectID, &service.Name, &service.ServiceType, &service.InternalPort,
		&service.DesiredReplicas, &service.HealthCheckPath, &service.CPURequest, &service.CPULimit,
		&service.MemoryRequest, &service.MemoryLimit, &service.HealthStatus, &service.HealthMessage,
		&service.HealthCheckedAt, &service.CreatedAt, &service.UpdatedAt,
	}
}
