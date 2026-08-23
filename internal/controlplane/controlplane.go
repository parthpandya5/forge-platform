package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
	"github.com/parthpandya5/forge-platform/internal/secrets"
	"github.com/parthpandya5/forge-platform/internal/store"
)

var ErrValidation = errors.New("validation failed")

type ControlPlane struct {
	store          *store.Store
	cipher         *secrets.Cipher
	jobMaxAttempts int
}

func New(repository *store.Store, cipher *secrets.Cipher, jobMaxAttempts int) *ControlPlane {
	return &ControlPlane{store: repository, cipher: cipher, jobMaxAttempts: jobMaxAttempts}
}

func (c *ControlPlane) CreateProject(ctx context.Context, userID uuid.UUID, input domain.CreateProjectInput) (domain.Project, error) {
	if err := input.NormalizeAndValidate(); err != nil {
		return domain.Project{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return c.store.CreateProject(ctx, userID, input)
}

func (c *ControlPlane) ListProjects(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Project, error) {
	return c.store.ListProjects(ctx, userID, limit, offset)
}

func (c *ControlPlane) GetProject(ctx context.Context, userID, projectID uuid.UUID) (domain.Project, error) {
	return c.store.GetProject(ctx, userID, projectID)
}

func (c *ControlPlane) DeleteProject(ctx context.Context, userID, projectID uuid.UUID) error {
	return c.store.DeleteProject(ctx, userID, projectID)
}

func (c *ControlPlane) CreateService(ctx context.Context, userID, projectID uuid.UUID, input domain.CreateServiceInput) (domain.Service, error) {
	if err := input.NormalizeAndValidate(); err != nil {
		return domain.Service{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return c.store.CreateService(ctx, userID, projectID, input)
}

func (c *ControlPlane) ListServices(ctx context.Context, userID, projectID uuid.UUID) ([]domain.Service, error) {
	return c.store.ListServices(ctx, userID, projectID)
}

func (c *ControlPlane) GetService(ctx context.Context, userID, serviceID uuid.UUID) (domain.Service, error) {
	return c.store.GetService(ctx, userID, serviceID)
}

func (c *ControlPlane) UpdateService(ctx context.Context, userID, serviceID uuid.UUID, input domain.CreateServiceInput) (domain.Service, error) {
	if err := input.NormalizeAndValidate(); err != nil {
		return domain.Service{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return c.store.UpdateService(ctx, userID, serviceID, input)
}

func (c *ControlPlane) DeleteService(ctx context.Context, userID, serviceID uuid.UUID) error {
	return c.store.DeleteService(ctx, userID, serviceID)
}

func (c *ControlPlane) CreateDeployment(ctx context.Context, userID, serviceID uuid.UUID, requestedRef, requestID string) (domain.Deployment, error) {
	requestedRef = strings.TrimSpace(requestedRef)
	if requestedRef != "" && !domain.ValidGitRef(requestedRef) {
		return domain.Deployment{}, fmt.Errorf("%w: ref is not a safe Git ref", ErrValidation)
	}
	return c.store.CreateDeployment(ctx, userID, serviceID, requestedRef, requestID, c.jobMaxAttempts)
}

func (c *ControlPlane) ListDeployments(ctx context.Context, userID, serviceID uuid.UUID, limit, offset int) ([]domain.Deployment, error) {
	return c.store.ListDeployments(ctx, userID, serviceID, limit, offset)
}

func (c *ControlPlane) GetDeployment(ctx context.Context, userID, deploymentID uuid.UUID) (domain.Deployment, error) {
	return c.store.GetDeployment(ctx, userID, deploymentID)
}

func (c *ControlPlane) Rollback(ctx context.Context, userID, deploymentID uuid.UUID, requestID string) (domain.Deployment, error) {
	return c.store.CreateRollback(ctx, userID, deploymentID, requestID, c.jobMaxAttempts)
}

func (c *ControlPlane) ListEvents(ctx context.Context, userID, deploymentID uuid.UUID, afterID int64, limit int) ([]domain.DeploymentEvent, error) {
	return c.store.ListEvents(ctx, userID, deploymentID, afterID, limit)
}

func (c *ControlPlane) ListLogs(ctx context.Context, userID, deploymentID uuid.UUID, afterID int64, limit int) ([]domain.DeploymentLog, error) {
	return c.store.ListLogs(ctx, userID, deploymentID, afterID, limit)
}

func (c *ControlPlane) ListEnvironmentVariables(ctx context.Context, userID, serviceID uuid.UUID) ([]domain.EnvironmentVariable, error) {
	return c.store.ListEnvironmentVariables(ctx, userID, serviceID)
}

func (c *ControlPlane) SetEnvironmentVariable(ctx context.Context, userID, serviceID uuid.UUID, key, value string) (domain.EnvironmentVariable, error) {
	key = strings.TrimSpace(key)
	if !domain.ValidEnvironmentKey(key) {
		return domain.EnvironmentVariable{}, fmt.Errorf("%w: key must be a valid environment variable name", ErrValidation)
	}
	if value == "" || len(value) > 65536 {
		return domain.EnvironmentVariable{}, fmt.Errorf("%w: value must contain 1-65536 bytes", ErrValidation)
	}
	associatedData := []byte(serviceID.String() + ":" + key)
	ciphertext, nonce, err := c.cipher.Encrypt([]byte(value), associatedData)
	if err != nil {
		return domain.EnvironmentVariable{}, fmt.Errorf("encrypt environment variable: %w", err)
	}
	return c.store.UpsertEnvironmentVariable(ctx, userID, serviceID, key, ciphertext, nonce)
}

func (c *ControlPlane) DeleteEnvironmentVariable(ctx context.Context, userID, serviceID, variableID uuid.UUID) error {
	return c.store.DeleteEnvironmentVariable(ctx, userID, serviceID, variableID)
}
