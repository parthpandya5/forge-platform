package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/parthpandya5/forge-platform/internal/domain"
)

func TestWebhookVerifiesAndEnqueuesPush(t *testing.T) {
	const secret = "strong-test-secret"
	body := []byte(`{"ref":"refs/heads/main","after":"1111111111111111111111111111111111111111","repository":{"html_url":"https://github.com/acme/demo.git"}}`)
	enqueuer := &fakeEnqueuer{}
	handler, err := NewWebhookHandler(secret, enqueuer, 3)
	if err != nil {
		t.Fatal(err)
	}
	observed := -1
	handler.WithDeploymentObserver(func(count int) { observed = count })
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-Hub-Signature-256", signature(t, secret, body))
	request.Header.Set("X-GitHub-Delivery", "delivery-1")
	request.Header.Set("X-GitHub-Event", "push")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if enqueuer.repository != "https://github.com/acme/demo" || enqueuer.branch != "main" {
		t.Fatalf("enqueued repository=%q branch=%q", enqueuer.repository, enqueuer.branch)
	}
	if observed != 0 {
		t.Fatalf("observed deployment count = %d", observed)
	}
}

func TestWebhookRejectsInvalidSignature(t *testing.T) {
	handler, _ := NewWebhookHandler("strong-test-secret", &fakeEnqueuer{}, 3)
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{}`))
	request.Header.Set("X-Hub-Signature-256", "sha256=00")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestWebhookIgnoresBranchDeletion(t *testing.T) {
	const secret = "strong-test-secret"
	body := []byte(`{"ref":"refs/heads/main","after":"0000000000000000000000000000000000000000","repository":{"html_url":"https://github.com/acme/demo"}}`)
	enqueuer := &fakeEnqueuer{}
	handler, _ := NewWebhookHandler(secret, enqueuer, 3)
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	request.Header.Set("X-Hub-Signature-256", signature(t, secret, body))
	request.Header.Set("X-GitHub-Delivery", "delivery-delete")
	request.Header.Set("X-GitHub-Event", "push")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d", response.Code)
	}
	if enqueuer.repository != "" {
		t.Fatal("branch deletion enqueued a deployment")
	}
}

type fakeEnqueuer struct{ repository, branch string }

func (f *fakeEnqueuer) CreateWebhookDeployments(_ context.Context, _, repository, branch, _, _ string, _ int) ([]domain.Deployment, bool, error) {
	f.repository, f.branch = repository, branch
	return []domain.Deployment{}, false, nil
}

func signature(t *testing.T, secret string, body []byte) string {
	t.Helper()
	digest := hmac.New(sha256.New, []byte(secret))
	if _, err := digest.Write(body); err != nil {
		t.Fatal(err)
	}
	return "sha256=" + hex.EncodeToString(digest.Sum(nil))
}
