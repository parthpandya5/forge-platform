package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/auth"
	"github.com/parthpandya5/forge-platform/internal/controlplane"
	forgemetrics "github.com/parthpandya5/forge-platform/internal/metrics"
)

type Server struct {
	logger         *slog.Logger
	corsOrigin     string
	control        *controlplane.ControlPlane
	userID         uuid.UUID
	ready          func(context.Context) error
	metrics        *forgemetrics.Metrics
	metricsHandler http.Handler
	githubWebhook  http.Handler
}

func NewServer(logger *slog.Logger, corsOrigin string) *Server {
	return &Server{logger: logger, corsOrigin: corsOrigin}
}

func (s *Server) WithControlPlane(control *controlplane.ControlPlane, userID uuid.UUID, ready func(context.Context) error) *Server {
	s.control = control
	s.userID = userID
	s.ready = ready
	return s
}

func (s *Server) WithMetrics(metrics *forgemetrics.Metrics, handler http.Handler) *Server {
	s.metrics = metrics
	s.metricsHandler = handler
	return s
}

func (s *Server) WithGitHubWebhook(handler http.Handler) *Server {
	s.githubWebhook = handler
	return s
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Use(serverRequestID)
	router.Use(requestDeadline(28 * time.Second))
	router.Use(s.requestIDHeader)
	router.Use(securityHeaders)
	router.Use(s.recoverer)
	router.Use(s.accessLog)
	router.Use(s.cors)
	router.Get("/health", s.health)
	if s.ready != nil {
		router.Get("/ready", s.readiness)
	}
	if s.metricsHandler != nil {
		router.Handle("/metrics", s.metricsHandler)
	}
	if s.githubWebhook != nil {
		router.Method(http.MethodPost, "/api/webhooks/github", s.githubWebhook)
	}
	if s.control != nil {
		router.Group(func(api chi.Router) {
			api.Use(auth.Middleware(s.userID))
			api.Route("/api", s.routes)
		})
	}
	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "route does not exist")
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed for this route")
	})
	return router
}

func requestDeadline(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func serverRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		ctx := context.WithValue(r.Context(), middleware.RequestIDKey, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.ready(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "NOT_READY", "a required dependency is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", "request_id", middleware.GetReqID(r.Context()), "panic", recovered)
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an internal error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		routePattern := chi.RouteContext(r.Context()).RoutePattern()
		if s.metrics != nil {
			s.metrics.ObserveHTTP(routePattern, r.Method, wrapped.Status(), started)
		}
		s.logger.Info("http request",
			"request_id", middleware.GetReqID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.Status(),
			"bytes", wrapped.BytesWritten(),
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.corsOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestIDHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := middleware.GetReqID(r.Context())
		w.Header().Set("X-Request-ID", requestID)
		r.Header.Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes(router chi.Router) {
	router.Post("/projects", s.createProject)
	router.Get("/projects", s.listProjects)
	router.Get("/projects/{projectID}", s.getProject)
	router.Delete("/projects/{projectID}", s.deleteProject)
	router.Post("/projects/{projectID}/services", s.createService)
	router.Get("/projects/{projectID}/services", s.listServices)
	router.Get("/services/{serviceID}", s.getService)
	router.Put("/services/{serviceID}", s.updateService)
	router.Delete("/services/{serviceID}", s.deleteService)
	router.Post("/services/{serviceID}/deployments", s.createDeployment)
	router.Get("/services/{serviceID}/deployments", s.listDeployments)
	router.Get("/services/{serviceID}/environment", s.listEnvironment)
	router.Put("/services/{serviceID}/environment/{key}", s.setEnvironment)
	router.Delete("/services/{serviceID}/environment/{variableID}", s.deleteEnvironment)
	router.Get("/deployments/{deploymentID}", s.getDeployment)
	router.Get("/deployments/{deploymentID}/events", s.listEvents)
	router.Get("/deployments/{deploymentID}/logs", s.listLogs)
	router.Get("/deployments/{deploymentID}/stream", s.streamDeployment)
	router.Post("/deployments/{deploymentID}/rollback", s.rollbackDeployment)
}
