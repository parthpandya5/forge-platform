package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey struct{}

func LocalUserID(email string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("forge-local-user:"+email))
}

func Middleware(userID uuid.UUID) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), contextKey{}, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserID(ctx context.Context) (uuid.UUID, bool) {
	value, ok := ctx.Value(contextKey{}).(uuid.UUID)
	return value, ok
}
