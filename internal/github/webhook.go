package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/parthpandya5/forge-platform/internal/domain"
)

type Enqueuer interface {
	CreateWebhookDeployments(context.Context, string, string, string, string, string, int) ([]domain.Deployment, bool, error)
}

type Handler struct {
	secret      []byte
	enqueuer    Enqueuer
	maxAttempts int
	observer    func(int)
}

func (h *Handler) WithDeploymentObserver(observer func(int)) *Handler {
	h.observer = observer
	return h
}

func NewWebhookHandler(secret string, enqueuer Enqueuer, maxAttempts int) (*Handler, error) {
	if len(secret) < 16 {
		return nil, errors.New("GitHub webhook secret must contain at least 16 characters")
	}
	if enqueuer == nil {
		return nil, errors.New("GitHub webhook enqueuer is required")
	}
	return &Handler{secret: []byte(secret), enqueuer: enqueuer, maxAttempts: maxAttempts}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_WEBHOOK", "webhook body is invalid or too large")
		return
	}
	if !h.validSignature(body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "webhook signature is invalid")
		return
	}
	deliveryID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if deliveryID == "" || len(deliveryID) > 200 {
		writeError(w, http.StatusBadRequest, "INVALID_DELIVERY", "X-GitHub-Delivery is required")
		return
	}
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "ping" {
		writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{"accepted": true, "deployments": 0}})
		return
	}
	if eventType != "push" {
		writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{"accepted": false, "reason": "event ignored"}})
		return
	}
	var payload struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Repository struct {
			HTMLURL string `json:"html_url"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_WEBHOOK", "push payload is not valid JSON")
		return
	}
	if !strings.HasPrefix(payload.Ref, "refs/heads/") {
		writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{"accepted": false, "reason": "non-branch push ignored"}})
		return
	}
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
	if !domain.ValidGitRef(branch) || len(payload.After) != 40 || !isHex(payload.After) {
		writeError(w, http.StatusBadRequest, "INVALID_WEBHOOK", "push branch or commit SHA is invalid")
		return
	}
	if payload.After == strings.Repeat("0", 40) {
		writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{"accepted": false, "reason": "branch deletion ignored"}})
		return
	}
	repositoryURL, err := domain.CanonicalRepositoryURL(payload.Repository.HTMLURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_WEBHOOK", "repository URL is not an allowed GitHub URL")
		return
	}
	requestID := r.Header.Get("X-Request-ID")
	deployments, duplicate, err := h.enqueuer.CreateWebhookDeployments(
		r.Context(), deliveryID, repositoryURL, branch, payload.After, requestID, h.maxAttempts,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WEBHOOK_PROCESSING_FAILED", "webhook could not be processed")
		return
	}
	if !duplicate && h.observer != nil {
		h.observer(len(deployments))
	}
	ids := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		ids = append(ids, deployment.ID.String())
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{
		"accepted": true, "duplicate": duplicate, "deployments": ids,
	}})
}

func (h *Handler) validSignature(body []byte, header string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	digest := hmac.New(sha256.New, h.secret)
	if _, err := digest.Write(body); err != nil {
		return false
	}
	return hmac.Equal(provided, digest.Sum(nil))
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
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
