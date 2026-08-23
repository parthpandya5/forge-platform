package domain

import "testing"

func TestCreateProjectValidation(t *testing.T) {
	input := CreateProjectInput{
		Name: "Demo API", Slug: "demo-api", RepositoryURL: "https://github.com/example/demo.git", RepositoryBranch: "main",
	}
	if err := input.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
}

func TestRepositoryURLRejectsUnsafeForms(t *testing.T) {
	tests := []string{
		"http://github.com/acme/app",
		"https://gitlab.com/acme/app",
		"https://token@github.com/acme/app",
		"https://github.com/acme/app?x=y",
		"https://github.com/acme/app/extra",
		"file:///etc/passwd",
	}
	for _, raw := range tests {
		if err := ValidateRepositoryURL(raw); err == nil {
			t.Errorf("ValidateRepositoryURL(%q) error = nil", raw)
		}
	}
}

func TestProjectRejectsUnsafeRef(t *testing.T) {
	input := CreateProjectInput{
		Name: "Demo", Slug: "demo", RepositoryURL: "https://github.com/example/demo", RepositoryBranch: "../../main",
	}
	if err := input.NormalizeAndValidate(); err == nil {
		t.Fatal("NormalizeAndValidate() error = nil")
	}
}

func TestServiceAppliesSafeDefaults(t *testing.T) {
	input := CreateServiceInput{Name: "web", InternalPort: 8080}
	if err := input.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	if input.DesiredReplicas != 1 || input.HealthCheckPath != "/health" || input.MemoryLimit != "512Mi" {
		t.Fatalf("unexpected defaults: %+v", input)
	}
}

func TestServiceRejectsUnsafeResourceRanges(t *testing.T) {
	tests := []CreateServiceInput{
		{Name: "web", InternalPort: 8080, DesiredReplicas: 1, CPURequest: "2", CPULimit: "1", MemoryRequest: "128Mi", MemoryLimit: "512Mi"},
		{Name: "web", InternalPort: 8080, DesiredReplicas: 1, CPURequest: "1", CPULimit: "9", MemoryRequest: "128Mi", MemoryLimit: "512Mi"},
		{Name: "web", InternalPort: 8080, DesiredReplicas: 1, CPURequest: "100m", CPULimit: "500m", MemoryRequest: "2Gi", MemoryLimit: "1Gi"},
	}
	for _, input := range tests {
		if err := input.NormalizeAndValidate(); err == nil {
			t.Fatalf("NormalizeAndValidate() accepted resources %+v", input)
		}
	}
}
