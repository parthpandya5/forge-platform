package httpapi

import (
	"net/http"

	"github.com/parthpandya5/forge-platform/internal/domain"
)

type createProjectRequest struct {
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	RepositoryURL    string `json:"repository_url"`
	RepositoryBranch string `json:"repository_branch"`
	AutoDeploy       bool   `json:"auto_deploy"`
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var request createProjectRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleError(w, err)
		return
	}
	project, err := s.control.CreateProject(r.Context(), mustUserID(r), domain.CreateProjectInput{
		Name: request.Name, Slug: request.Slug, RepositoryURL: request.RepositoryURL,
		RepositoryBranch: request.RepositoryBranch, AutoDeploy: request.AutoDeploy,
	})
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeData(w, http.StatusCreated, project)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		s.handleError(w, err)
		return
	}
	projects, err := s.control.ListProjects(r.Context(), mustUserID(r), limit, offset)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeList(w, projects, limit, offset)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathUUID(r, "projectID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	project, err := s.control.GetProject(r.Context(), mustUserID(r), projectID)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeData(w, http.StatusOK, project)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathUUID(r, "projectID")
	if err == nil {
		err = s.control.DeleteProject(r.Context(), mustUserID(r), projectID)
	}
	if err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createServiceRequest struct {
	Name            string `json:"name"`
	InternalPort    int    `json:"internal_port"`
	DesiredReplicas int    `json:"desired_replicas"`
	HealthCheckPath string `json:"health_check_path"`
	CPURequest      string `json:"cpu_request"`
	CPULimit        string `json:"cpu_limit"`
	MemoryRequest   string `json:"memory_request"`
	MemoryLimit     string `json:"memory_limit"`
}

func (s *Server) createService(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathUUID(r, "projectID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	var request createServiceRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleError(w, err)
		return
	}
	service, err := s.control.CreateService(r.Context(), mustUserID(r), projectID, request.serviceInput())
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeData(w, http.StatusCreated, service)
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathUUID(r, "projectID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	services, err := s.control.ListServices(r.Context(), mustUserID(r), projectID)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": services})
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	serviceID, err := pathUUID(r, "serviceID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	service, err := s.control.GetService(r.Context(), mustUserID(r), serviceID)
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeData(w, http.StatusOK, service)
}

func (s *Server) updateService(w http.ResponseWriter, r *http.Request) {
	serviceID, err := pathUUID(r, "serviceID")
	if err != nil {
		s.handleError(w, err)
		return
	}
	var request createServiceRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleError(w, err)
		return
	}
	service, err := s.control.UpdateService(r.Context(), mustUserID(r), serviceID, request.serviceInput())
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeData(w, http.StatusOK, service)
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	serviceID, err := pathUUID(r, "serviceID")
	if err == nil {
		err = s.control.DeleteService(r.Context(), mustUserID(r), serviceID)
	}
	if err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r createServiceRequest) serviceInput() domain.CreateServiceInput {
	return domain.CreateServiceInput{
		Name: r.Name, InternalPort: r.InternalPort, DesiredReplicas: r.DesiredReplicas,
		HealthCheckPath: r.HealthCheckPath, CPURequest: r.CPURequest, CPULimit: r.CPULimit,
		MemoryRequest: r.MemoryRequest, MemoryLimit: r.MemoryLimit,
	}
}
