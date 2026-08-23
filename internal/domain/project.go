package domain

import (
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	slugPattern        = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	namePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,98}[A-Za-z0-9]$|^[A-Za-z0-9]$`)
	gitRefPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)
	healthPathExpr     = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/-]*$`)
	githubSegmentExpr  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	cpuQuantityExpr    = regexp.MustCompile(`^(?:[1-9][0-9]*m|[1-9][0-9]*(?:\.[0-9]+)?)$`)
	memoryQuantityExpr = regexp.MustCompile(`^[1-9][0-9]*(?:Ki|Mi|Gi)$`)
)

type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Project struct {
	ID                uuid.UUID          `json:"id"`
	UserID            uuid.UUID          `json:"user_id"`
	Name              string             `json:"name"`
	Slug              string             `json:"slug"`
	RepositoryURL     string             `json:"repository_url"`
	RepositoryBranch  string             `json:"repository_branch"`
	AutoDeploy        bool               `json:"auto_deploy"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	CurrentDeployment *DeploymentSummary `json:"current_deployment,omitempty"`
}

type DeploymentSummary struct {
	ID        uuid.UUID        `json:"id"`
	Status    DeploymentStatus `json:"status"`
	CommitSHA *string          `json:"commit_sha"`
	CreatedAt time.Time        `json:"created_at"`
}

type Service struct {
	ID              uuid.UUID           `json:"id"`
	ProjectID       uuid.UUID           `json:"project_id"`
	Name            string              `json:"name"`
	ServiceType     string              `json:"service_type"`
	InternalPort    int                 `json:"internal_port"`
	DesiredReplicas int                 `json:"desired_replicas"`
	HealthCheckPath string              `json:"health_check_path"`
	CPURequest      string              `json:"cpu_request"`
	CPULimit        string              `json:"cpu_limit"`
	MemoryRequest   string              `json:"memory_request"`
	MemoryLimit     string              `json:"memory_limit"`
	HealthStatus    ServiceHealthStatus `json:"health_status"`
	HealthMessage   *string             `json:"health_message"`
	HealthCheckedAt *time.Time          `json:"health_checked_at"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

type ServiceHealthStatus string

const (
	ServiceHealthUnknown   ServiceHealthStatus = "UNKNOWN"
	ServiceHealthRunning   ServiceHealthStatus = "RUNNING"
	ServiceHealthDegraded  ServiceHealthStatus = "DEGRADED"
	ServiceHealthUnhealthy ServiceHealthStatus = "UNHEALTHY"
)

type CreateProjectInput struct {
	Name             string
	Slug             string
	RepositoryURL    string
	RepositoryBranch string
	AutoDeploy       bool
}

func (input *CreateProjectInput) NormalizeAndValidate() error {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.RepositoryURL = strings.TrimSpace(input.RepositoryURL)
	input.RepositoryBranch = strings.TrimSpace(input.RepositoryBranch)

	if !namePattern.MatchString(input.Name) {
		return errors.New("name must be 1-100 characters and contain letters, numbers, spaces, hyphens, or underscores")
	}
	if !slugPattern.MatchString(input.Slug) {
		return errors.New("slug must be a lowercase DNS label of at most 63 characters")
	}
	if err := ValidateRepositoryURL(input.RepositoryURL); err != nil {
		return err
	}
	canonicalURL, err := CanonicalRepositoryURL(input.RepositoryURL)
	if err != nil {
		return err
	}
	input.RepositoryURL = canonicalURL
	if !ValidGitRef(input.RepositoryBranch) {
		return errors.New("repository_branch is not a safe Git ref")
	}
	return nil
}

func CanonicalRepositoryURL(raw string) (string, error) {
	if err := ValidateRepositoryURL(raw); err != nil {
		return "", err
	}
	parsed, _ := url.Parse(raw)
	path := strings.TrimSuffix(strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git"), "/")
	return "https://github.com/" + strings.ToLower(path), nil
}

func ValidGitRef(ref string) bool {
	return gitRefPattern.MatchString(ref) && !strings.Contains(ref, "..") &&
		!strings.Contains(ref, "//") && !strings.HasSuffix(ref, ".lock") && !strings.HasSuffix(ref, "/")
}

func ValidateRepositoryURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("repository_url must be a valid URL")
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return errors.New("repository_url must be an HTTPS github.com URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return errors.New("repository_url must not contain credentials, a port, query, or fragment")
	}
	segments := strings.Split(strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/"), "/")
	if len(segments) != 2 || !githubSegmentExpr.MatchString(segments[0]) || !githubSegmentExpr.MatchString(segments[1]) ||
		segments[0] == "." || segments[0] == ".." || segments[1] == "." || segments[1] == ".." {
		return errors.New("repository_url must identify a GitHub owner and repository")
	}
	return nil
}

type CreateServiceInput struct {
	Name            string
	InternalPort    int
	DesiredReplicas int
	HealthCheckPath string
	CPURequest      string
	CPULimit        string
	MemoryRequest   string
	MemoryLimit     string
}

func (input *CreateServiceInput) NormalizeAndValidate() error {
	input.Name = strings.TrimSpace(input.Name)
	if !namePattern.MatchString(input.Name) {
		return errors.New("name must be 1-100 characters and contain letters, numbers, spaces, hyphens, or underscores")
	}
	if input.InternalPort < 1 || input.InternalPort > 65535 {
		return errors.New("internal_port must be between 1 and 65535")
	}
	if input.DesiredReplicas == 0 {
		input.DesiredReplicas = 1
	}
	if input.DesiredReplicas < 1 || input.DesiredReplicas > 20 {
		return errors.New("desired_replicas must be between 1 and 20")
	}
	if input.HealthCheckPath == "" {
		input.HealthCheckPath = "/health"
	}
	if len(input.HealthCheckPath) > 512 || !healthPathExpr.MatchString(input.HealthCheckPath) || strings.Contains(input.HealthCheckPath, "..") {
		return errors.New("health_check_path must be a safe absolute URL path")
	}
	defaults := map[*string]string{
		&input.CPURequest: "100m", &input.CPULimit: "500m",
		&input.MemoryRequest: "128Mi", &input.MemoryLimit: "512Mi",
	}
	for target, fallback := range defaults {
		*target = strings.TrimSpace(*target)
		if *target == "" {
			*target = fallback
		}
		if len(*target) > 32 {
			return fmt.Errorf("resource quantity %q is too long", *target)
		}
	}
	if !cpuQuantityExpr.MatchString(input.CPURequest) || !cpuQuantityExpr.MatchString(input.CPULimit) {
		return errors.New("cpu_request and cpu_limit must be positive Kubernetes CPU quantities")
	}
	if !memoryQuantityExpr.MatchString(input.MemoryRequest) || !memoryQuantityExpr.MatchString(input.MemoryLimit) {
		return errors.New("memory_request and memory_limit must use positive Ki, Mi, or Gi quantities")
	}
	cpuRequest, cpuRequestOK := parseCPU(input.CPURequest)
	cpuLimit, cpuLimitOK := parseCPU(input.CPULimit)
	memoryRequest, memoryRequestOK := parseMemory(input.MemoryRequest)
	memoryLimit, memoryLimitOK := parseMemory(input.MemoryLimit)
	if !cpuRequestOK || !cpuLimitOK || !memoryRequestOK || !memoryLimitOK {
		return errors.New("resource quantities could not be parsed")
	}
	if cpuRequest.Cmp(cpuLimit) > 0 || memoryRequest.Cmp(memoryLimit) > 0 {
		return errors.New("resource requests must not exceed resource limits")
	}
	maximumCPU, _ := parseCPU("8")
	maximumMemory, _ := parseMemory("16Gi")
	if cpuLimit.Cmp(maximumCPU) > 0 || memoryLimit.Cmp(maximumMemory) > 0 {
		return errors.New("resource limits may not exceed 8 CPU or 16Gi memory")
	}
	return nil
}

func parseCPU(value string) (*big.Rat, bool) {
	if strings.HasSuffix(value, "m") {
		milli, ok := new(big.Rat).SetString(strings.TrimSuffix(value, "m"))
		if !ok {
			return nil, false
		}
		return milli.Quo(milli, big.NewRat(1000, 1)), true
	}
	return new(big.Rat).SetString(value)
}

func parseMemory(value string) (*big.Int, bool) {
	if len(value) < 3 {
		return nil, false
	}
	unit := value[len(value)-2:]
	amount, ok := new(big.Int).SetString(value[:len(value)-2], 10)
	if !ok {
		return nil, false
	}
	power := uint(0)
	switch unit {
	case "Ki":
		power = 10
	case "Mi":
		power = 20
	case "Gi":
		power = 30
	default:
		return nil, false
	}
	return amount.Mul(amount, new(big.Int).Lsh(big.NewInt(1), power)), true
}
