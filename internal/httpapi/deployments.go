package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

type createDeploymentRequest struct {
	Ref string `json:"ref"`
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	serviceID, err := pathUUID(r, "serviceID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	var request createDeploymentRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &request); err != nil {
			s.handleError(w, err)
			return
		}
	}
	deployment, err := s.control.CreateDeployment(r.Context(), mustUserID(r), serviceID, request.Ref, middleware.GetReqID(r.Context()))
	if err != nil {
		s.handleError(w, err)
		return
	}
	if s.metrics != nil {
		s.metrics.Deployments.WithLabelValues("deploy").Inc()
	}
	w.Header().Set("Location", "/api/deployments/"+deployment.ID.String())
	writeData(w, http.StatusAccepted, deployment)
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	serviceID, err := pathUUID(r, "serviceID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	limit, offset, err := pagination(r)
	if err != nil {
		s.handleError(w, err)
		return
	}
	deployments, err := s.control.ListDeployments(r.Context(), mustUserID(r), serviceID, limit, offset)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeList(w, deployments, limit, offset)
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID, err := pathUUID(r, "deploymentID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	deployment, err := s.control.GetDeployment(r.Context(), mustUserID(r), deploymentID)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeData(w, http.StatusOK, deployment)
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	deploymentID, err := pathUUID(r, "deploymentID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	afterID, limit, err := cursor(r)
	if err != nil {
		s.handleError(w, err)
		return
	}
	events, err := s.control.ListEvents(r.Context(), mustUserID(r), deploymentID, afterID, limit)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": events})
}

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	deploymentID, err := pathUUID(r, "deploymentID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	afterID, limit, err := cursor(r)
	if err != nil {
		s.handleError(w, err)
		return
	}
	logs, err := s.control.ListLogs(r.Context(), mustUserID(r), deploymentID, afterID, limit)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": logs})
}

func (s *Server) streamDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID, err := pathUUID(r, "deploymentID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	userID := mustUserID(r)
	if _, err := s.control.GetDeployment(r.Context(), userID, deploymentID); err != nil {
		s.handleError(w, err)
		return
	}
	eventID, err := nonnegativeQueryInt64(r, "after_event_id")
	if err != nil {
		s.handleError(w, err)
		return
	}
	logID, err := nonnegativeQueryInt64(r, "after_log_id")
	if err != nil {
		s.handleError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "STREAMING_UNAVAILABLE", "HTTP streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	deadline := time.NewTimer(25 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		events, eventsErr := s.control.ListEvents(r.Context(), userID, deploymentID, eventID, 250)
		logs, logsErr := s.control.ListLogs(r.Context(), userID, deploymentID, logID, 250)
		if eventsErr != nil || logsErr != nil {
			s.logger.Warn("deployment stream query failed", "deployment_id", deploymentID, "error", errors.Join(eventsErr, logsErr))
			if err := writeSSE(w, "stream.error", map[string]string{"message": "stream query failed"}); err != nil {
				s.logger.Debug("write deployment stream error", "deployment_id", deploymentID, "error", err)
			}
			flusher.Flush()
			return
		}
		for _, event := range events {
			if err := writeSSE(w, "deployment.event", event); err != nil {
				return
			}
			eventID = event.ID
		}
		for _, log := range logs {
			if err := writeSSE(w, "deployment.log", log); err != nil {
				return
			}
			logID = log.ID
		}
		if len(events) > 0 || len(logs) > 0 {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, eventType string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode server event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, encoded); err != nil {
		return fmt.Errorf("write server event: %w", err)
	}
	return nil
}

func (s *Server) rollbackDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID, err := pathUUID(r, "deploymentID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	deployment, err := s.control.Rollback(r.Context(), mustUserID(r), deploymentID, middleware.GetReqID(r.Context()))
	if err != nil {
		s.handleError(w, err)
		return
	}
	if s.metrics != nil {
		s.metrics.Deployments.WithLabelValues("rollback").Inc()
	}
	w.Header().Set("Location", "/api/deployments/"+deployment.ID.String())
	writeData(w, http.StatusAccepted, deployment)
}
