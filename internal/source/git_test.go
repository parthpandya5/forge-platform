package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBuildContext(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBuildContext(directory); err != nil {
		t.Fatalf("ValidateBuildContext() error = %v", err)
	}
}

func TestValidateBuildContextRequiresRegularDockerfile(t *testing.T) {
	directory := t.TempDir()
	if err := ValidateBuildContext(directory); err == nil {
		t.Fatal("missing Dockerfile error = nil")
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(directory, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBuildContext(directory); err == nil {
		t.Fatal("symlink Dockerfile error = nil")
	}
}

func TestValidateBuildContextRejectsEscapingSymlink(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(directory, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBuildContext(directory); err == nil {
		t.Fatal("escaping symlink error = nil")
	}
}

func TestValidateBuildContextRejectsOversizedRepository(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(directory, "large.bin")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maximumBuildContextBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBuildContext(directory); err == nil {
		t.Fatal("oversized build context error = nil")
	}
}

func TestValidateBuildContextHonorsCancellation(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateBuildContext(ctx, directory); !errors.Is(err, context.Canceled) {
		t.Fatalf("validateBuildContext() error = %v", err)
	}
}
