package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type DeploymentStatus string

const (
	DeploymentQueued    DeploymentStatus = "QUEUED"
	DeploymentCloning   DeploymentStatus = "CLONING"
	DeploymentBuilding  DeploymentStatus = "BUILDING"
	DeploymentPushing   DeploymentStatus = "PUSHING"
	DeploymentDeploying DeploymentStatus = "DEPLOYING"
	DeploymentVerifying DeploymentStatus = "VERIFYING"
	DeploymentLive      DeploymentStatus = "LIVE"
	DeploymentFailed    DeploymentStatus = "FAILED"
	DeploymentCancelled DeploymentStatus = "CANCELLED"
)

var ErrInvalidTransition = errors.New("invalid deployment state transition")

var transitions = map[DeploymentStatus]map[DeploymentStatus]struct{}{
	DeploymentQueued: {
		DeploymentCloning: {}, DeploymentDeploying: {}, DeploymentFailed: {}, DeploymentCancelled: {},
	},
	DeploymentCloning: {
		DeploymentBuilding: {}, DeploymentFailed: {}, DeploymentCancelled: {},
	},
	DeploymentBuilding: {
		DeploymentPushing: {}, DeploymentDeploying: {}, DeploymentFailed: {}, DeploymentCancelled: {},
	},
	DeploymentPushing: {
		DeploymentDeploying: {}, DeploymentFailed: {}, DeploymentCancelled: {},
	},
	DeploymentDeploying: {
		DeploymentVerifying: {}, DeploymentFailed: {}, DeploymentCancelled: {},
	},
	DeploymentVerifying: {
		DeploymentLive: {}, DeploymentFailed: {}, DeploymentCancelled: {},
	},
}

func (s DeploymentStatus) IsTerminal() bool {
	return s == DeploymentLive || s == DeploymentFailed || s == DeploymentCancelled
}

func (s DeploymentStatus) CanTransition(to DeploymentStatus) bool {
	_, ok := transitions[s][to]
	return ok
}

func ValidateTransition(from, to DeploymentStatus) error {
	if !from.CanTransition(to) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, from, to)
	}
	return nil
}

type Deployment struct {
	ID           uuid.UUID        `json:"id"`
	ServiceID    uuid.UUID        `json:"service_id"`
	CommitSHA    *string          `json:"commit_sha"`
	RequestedRef *string          `json:"requested_ref"`
	Status       DeploymentStatus `json:"status"`
	ImageTag     *string          `json:"image_tag"`
	RouteURL     *string          `json:"route_url"`
	RollbackOf   *uuid.UUID       `json:"rollback_of"`
	RequestID    string           `json:"request_id"`
	ErrorMessage *string          `json:"error_message"`
	CreatedAt    time.Time        `json:"created_at"`
	StartedAt    *time.Time       `json:"started_at"`
	FinishedAt   *time.Time       `json:"finished_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Version      int64            `json:"version"`
}

type DeploymentEvent struct {
	ID           int64          `json:"id"`
	DeploymentID uuid.UUID      `json:"deployment_id"`
	EventType    string         `json:"event_type"`
	Message      string         `json:"message"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
}

type DeploymentLog struct {
	ID           int64     `json:"id"`
	DeploymentID uuid.UUID `json:"deployment_id"`
	Stream       string    `json:"stream"`
	Message      string    `json:"message"`
	CreatedAt    time.Time `json:"created_at"`
}
