package domain

import (
	"time"

	"github.com/google/uuid"
)

type Job struct {
	ID             int64      `json:"id"`
	DeploymentID   uuid.UUID  `json:"deployment_id"`
	JobType        string     `json:"job_type"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	AvailableAt    time.Time  `json:"available_at"`
	LockedBy       *string    `json:"locked_by"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at"`
	LastError      *string    `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at"`
}
