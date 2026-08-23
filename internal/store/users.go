package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
)

func (s *Store) EnsureUser(ctx context.Context, id uuid.UUID, email, name string) (domain.User, error) {
	var user domain.User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, email, name, created_at
	`, id, email, name).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("ensure local user: %w", mapError(err))
	}
	return user, nil
}
