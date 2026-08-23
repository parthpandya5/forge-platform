package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/auth"
	"github.com/parthpandya5/forge-platform/internal/controlplane"
	"github.com/parthpandya5/forge-platform/internal/store"
)

func mustUserID(r *http.Request) uuid.UUID {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		panic("authentication middleware missing")
	}
	return userID
}

func pathUUID(r *http.Request, parameter string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, parameter))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %s must be a UUID", controlplane.ErrValidation, parameter)
	}
	return id, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: request body must be valid JSON: %v", controlplane.ErrValidation, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body must contain exactly one JSON object", controlplane.ErrValidation)
	}
	return nil
}

func pagination(r *http.Request) (int, int, error) {
	limit, err := positiveQueryInt(r, "limit", 50, 100)
	if err != nil {
		return 0, 0, err
	}
	offset, err := nonnegativeQueryInt(r, "offset", 0)
	return limit, offset, err
}

func cursor(r *http.Request) (int64, int, error) {
	limit, err := positiveQueryInt(r, "limit", 250, 1000)
	if err != nil {
		return 0, 0, err
	}
	var afterID int64
	if raw := r.URL.Query().Get("after_id"); raw != "" {
		afterID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || afterID < 0 {
			return 0, 0, fmt.Errorf("%w: after_id must be a non-negative integer", controlplane.ErrValidation)
		}
	}
	return afterID, limit, nil
}

func positiveQueryInt(r *http.Request, key string, fallback, maximum int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximum {
		return 0, fmt.Errorf("%w: %s must be between 1 and %d", controlplane.ErrValidation, key, maximum)
	}
	return value, nil
}

func nonnegativeQueryInt(r *http.Request, key string, fallback int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > 1_000_000 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", controlplane.ErrValidation, key)
	}
	return value, nil
}

func nonnegativeQueryInt64(r *http.Request, key string) (int64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", controlplane.ErrValidation, key)
	}
	return value, nil
}

func (s *Server) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "REQUEST_TIMEOUT", "request exceeded its processing deadline")
	case errors.Is(err, controlplane.ErrValidation):
		message := strings.TrimPrefix(err.Error(), controlplane.ErrValidation.Error()+": ")
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource does not exist")
	case errors.Is(err, store.ErrConflict), store.IsInvalidTransition(err):
		writeError(w, http.StatusConflict, "CONFLICT", "request conflicts with current resource state")
	default:
		s.logger.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an internal error occurred")
	}
}

func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data})
}

func writeList[T any](w http.ResponseWriter, data []T, limit, offset int) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data":       data,
		"pagination": map[string]int{"limit": limit, "offset": offset, "count": len(data)},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "response encoding failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
