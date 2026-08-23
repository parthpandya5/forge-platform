package build

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/parthpandya5/forge-platform/internal/failure"
	"github.com/parthpandya5/forge-platform/internal/source"
)

type Docker struct {
	executable string
	dockerHost string
	registry   string
	repository string
}

func NewDocker(executable, dockerHost, registry, repository string) *Docker {
	if executable == "" {
		executable = "docker"
	}
	return &Docker{
		executable: executable, dockerHost: dockerHost, registry: strings.TrimSuffix(registry, "/"),
		repository: strings.Trim(repository, "/"),
	}
}

func (d *Docker) ImageTag(projectSlug, deploymentID string) string {
	name := d.repository + "/" + projectSlug + ":" + deploymentID
	if d.registry != "" {
		return d.registry + "/" + name
	}
	return name
}

func (d *Docker) Build(ctx context.Context, directory, imageTag string, log source.LogFunc) error {
	args := []string{"buildx", "build", "--load", "--progress=plain", "--tag", imageTag, "--file", "Dockerfile", "."}
	tail := &outputTail{}
	logOutput := func(line string) {
		tail.Add(line)
		if log != nil {
			log(line)
		}
	}
	if err := d.run(ctx, directory, logOutput, args...); err != nil {
		buildErr := fmt.Errorf("build image %s: %w", imageTag, err)
		if ctx.Err() != nil {
			return buildErr
		}
		if errors.Is(err, exec.ErrNotFound) || isPermanentBuildOutput(tail.String()) {
			return failure.Permanent(buildErr)
		}
		return buildErr
	}
	return nil
}

func (d *Docker) Push(ctx context.Context, imageTag string, log source.LogFunc) error {
	if d.registry == "" {
		return failure.Permanent(fmt.Errorf("push image %s: registry is not configured", imageTag))
	}
	if err := d.run(ctx, "", log, "push", imageTag); err != nil {
		return fmt.Errorf("push image %s: %w", imageTag, err)
	}
	return nil
}

func (d *Docker) run(ctx context.Context, directory string, log source.LogFunc, args ...string) error {
	command := exec.CommandContext(ctx, d.executable, args...)
	command.Dir = directory
	if d.dockerHost != "" {
		command.Env = append(os.Environ(), "DOCKER_HOST="+d.dockerHost)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start docker: %w", err)
	}
	var wait sync.WaitGroup
	scanErrors := make(chan error, 2)
	wait.Add(2)
	go relay(stdout, log, &wait, scanErrors)
	go relay(stderr, log, &wait, scanErrors)
	wait.Wait()
	close(scanErrors)
	waitErr := command.Wait()
	for scanErr := range scanErrors {
		if scanErr != nil {
			return fmt.Errorf("read docker output: %w", scanErr)
		}
	}
	if waitErr != nil {
		return fmt.Errorf("docker exited unsuccessfully: %w", waitErr)
	}
	return nil
}

func relay(reader io.Reader, log source.LogFunc, wait *sync.WaitGroup, result chan<- error) {
	defer wait.Done()
	buffered := bufio.NewReaderSize(reader, 16*1024)
	for {
		chunk, err := buffered.ReadSlice('\n')
		if len(chunk) > 0 && log != nil {
			log(strings.TrimSuffix(strings.TrimSuffix(string(chunk), "\n"), "\r"))
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			result <- nil
		default:
			result <- err
		}
		return
	}
}

type outputTail struct {
	mu    sync.Mutex
	lines []string
}

func (t *outputTail) Add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(line) > 4096 {
		line = line[len(line)-4096:]
	}
	t.lines = append(t.lines, line)
	if len(t.lines) > 100 {
		copy(t.lines, t.lines[len(t.lines)-100:])
		t.lines = t.lines[:100]
	}
}

func (t *outputTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

func isPermanentBuildOutput(output string) bool {
	output = strings.ToLower(output)
	for _, marker := range []string{
		"dockerfile parse error",
		"failed to read dockerfile",
		"unknown instruction",
		"failed to compute cache key",
		"failed to calculate checksum",
		"circular dependency detected",
		"no source files were specified",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}
