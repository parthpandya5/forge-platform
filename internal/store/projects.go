package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/parthpandya5/forge-platform/internal/domain"
)

const projectColumns = `
	p.id, p.user_id, p.name, p.slug, p.repository_url, p.repository_branch,
	p.auto_deploy, p.created_at, p.updated_at,
	d.id, d.status, d.commit_sha, d.created_at`

const currentDeploymentJoin = `
	LEFT JOIN LATERAL (
		SELECT d.id, d.status, d.commit_sha, d.created_at
		FROM deployments d
		JOIN services s ON s.id = d.service_id
		WHERE s.project_id = p.id
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT 1
	) d ON true`

func (s *Store) CreateProject(ctx context.Context, userID uuid.UUID, input domain.CreateProjectInput) (domain.Project, error) {
	id := uuid.New()
	var project domain.Project
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (id, user_id, name, slug, repository_url, repository_branch, auto_deploy)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, user_id, name, slug, repository_url, repository_branch, auto_deploy, created_at, updated_at
	`, id, userID, input.Name, input.Slug, input.RepositoryURL, input.RepositoryBranch, input.AutoDeploy).Scan(
		&project.ID, &project.UserID, &project.Name, &project.Slug, &project.RepositoryURL,
		&project.RepositoryBranch, &project.AutoDeploy, &project.CreatedAt, &project.UpdatedAt,
	)
	if err != nil {
		return domain.Project{}, fmt.Errorf("create project: %w", mapError(err))
	}
	return project, nil
}

func (s *Store) ListProjects(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects p `+currentDeploymentJoin+`
		WHERE p.user_id = $1 ORDER BY p.created_at DESC, p.id DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	projects := make([]domain.Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return projects, nil
}

func (s *Store) GetProject(ctx context.Context, userID, projectID uuid.UUID) (domain.Project, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects p `+currentDeploymentJoin+`
		WHERE p.user_id = $1 AND p.id = $2`, userID, projectID)
	project, err := scanProject(row)
	if err != nil {
		return domain.Project{}, fmt.Errorf("get project: %w", mapError(err))
	}
	return project, nil
}

func (s *Store) DeleteProject(ctx context.Context, userID, projectID uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE user_id = $1 AND id = $2`, userID, projectID)
	if err != nil {
		return fmt.Errorf("delete project: %w", mapError(err))
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanProject(row scanner) (domain.Project, error) {
	var project domain.Project
	var deploymentID pgtype.UUID
	var deploymentStatus pgtype.Text
	var commitSHA pgtype.Text
	var deploymentCreated pgtype.Timestamptz
	err := row.Scan(
		&project.ID, &project.UserID, &project.Name, &project.Slug, &project.RepositoryURL,
		&project.RepositoryBranch, &project.AutoDeploy, &project.CreatedAt, &project.UpdatedAt,
		&deploymentID, &deploymentStatus, &commitSHA, &deploymentCreated,
	)
	if err != nil {
		return domain.Project{}, err
	}
	if deploymentID.Valid {
		project.CurrentDeployment = &domain.DeploymentSummary{
			ID: deploymentID.Bytes, Status: domain.DeploymentStatus(deploymentStatus.String), CreatedAt: deploymentCreated.Time,
		}
		if commitSHA.Valid {
			project.CurrentDeployment.CommitSHA = &commitSHA.String
		}
	}
	return project, nil
}
