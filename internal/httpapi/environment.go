package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listEnvironment(w http.ResponseWriter, r *http.Request) {
	serviceID, err := pathUUID(r, "serviceID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	variables, err := s.control.ListEnvironmentVariables(r.Context(), mustUserID(r), serviceID)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": variables})
}

type setEnvironmentRequest struct {
	Value string `json:"value"`
}

func (s *Server) setEnvironment(w http.ResponseWriter, r *http.Request) {
	serviceID, err := pathUUID(r, "serviceID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	var request setEnvironmentRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleError(w, err)
		return
	}
	variable, err := s.control.SetEnvironmentVariable(r.Context(), mustUserID(r), serviceID, chi.URLParam(r, "key"), request.Value)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeData(w, http.StatusOK, variable)
}

func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	serviceID, err := pathUUID(r, "serviceID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	variableID, err := pathUUID(r, "variableID")
	if err == nil {
		err = s.control.DeleteEnvironmentVariable(r.Context(), mustUserID(r), serviceID, variableID)
	}
	if err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
