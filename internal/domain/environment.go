package domain

import (
	"regexp"
	"time"

	"github.com/google/uuid"
)

var environmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

type EnvironmentVariable struct {
	ID        uuid.UUID `json:"id"`
	ServiceID uuid.UUID `json:"service_id"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ValidEnvironmentKey(key string) bool {
	return environmentKeyPattern.MatchString(key)
}
