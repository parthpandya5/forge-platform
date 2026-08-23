package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealth(t *testing.T) {
	server := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(response.Body.String()); got != `{"status":"ok"}` {
		t.Fatalf("body = %s", got)
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("response did not include a request ID")
	}
}

func TestRequestIDDoesNotTrustClientValue(t *testing.T) {
	server := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("X-Request-ID", "attacker-controlled")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" || requestID == "attacker-controlled" {
		t.Fatalf("response request ID = %q", requestID)
	}
}

func TestRequestDeadlineReachesHandlers(t *testing.T) {
	var hasDeadline bool
	handler := requestDeadline(time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		_, hasDeadline = request.Context().Deadline()
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !hasDeadline {
		t.Fatal("request context had no deadline")
	}
}

func TestRoutingErrorsUseJSONEnvelope(t *testing.T) {
	server := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), "").Handler()
	for _, test := range []struct {
		method string
		path   string
		status int
		code   string
	}{
		{method: http.MethodGet, path: "/missing", status: http.StatusNotFound, code: "ROUTE_NOT_FOUND"},
		{method: http.MethodPost, path: "/health", status: http.StatusMethodNotAllowed, code: "METHOD_NOT_ALLOWED"},
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
			t.Errorf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestDecodeJSONRejectsTrailingGarbage(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"valid"} trailing`))
	response := httptest.NewRecorder()
	var target struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(response, request, &target); err == nil {
		t.Fatal("decodeJSON() accepted trailing garbage")
	}
}

func TestWriteSSEFramesJSON(t *testing.T) {
	var output strings.Builder
	if err := writeSSE(&output, "deployment.log", map[string]string{"message": "line\nnext"}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "event: deployment.log\ndata: {\"message\":\"line\\nnext\"}\n\n" {
		t.Fatalf("SSE frame = %q", got)
	}
}
