package source

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
	"github.com/parthpandya5/forge-platform/internal/failure"
)

type LogFunc func(string)

type Checkout struct {
	Directory string
	CommitSHA string
	cleanup   func() error
}

func NewCheckout(directory, commitSHA string, cleanup func() error) *Checkout {
	return &Checkout{Directory: directory, CommitSHA: commitSHA, cleanup: cleanup}
}

func (c *Checkout) Cleanup() error {
	if c.cleanup == nil {
		return nil
	}
	cleanup := c.cleanup
	c.cleanup = nil
	return cleanup()
}

type Git struct {
	executable string
}

func NewGit(executable string) *Git {
	if executable == "" {
		executable = "git"
	}
	return &Git{executable: executable}
}

func (g *Git) Checkout(ctx context.Context, deploymentID uuid.UUID, repositoryURL, ref string, log LogFunc) (*Checkout, error) {
	if err := domain.ValidateRepositoryURL(repositoryURL); err != nil {
		return nil, failure.Permanent(fmt.Errorf("validate repository: %w", err))
	}
	if !domain.ValidGitRef(ref) {
		return nil, failure.Permanent(errors.New("validate repository ref: unsafe Git ref"))
	}
	directory, err := os.MkdirTemp("", "forge-checkout-"+deploymentID.String()+"-")
	if err != nil {
		return nil, fmt.Errorf("create checkout directory: %w", err)
	}
	checkout := NewCheckout(directory, "", func() error { return os.RemoveAll(directory) })
	fail := func(stage string, commandErr error) (*Checkout, error) {
		stageErr := fmt.Errorf("%s: %w", stage, commandErr)
		if cleanupErr := checkout.Cleanup(); cleanupErr != nil {
			return nil, errors.Join(stageErr, fmt.Errorf("clean checkout directory: %w", cleanupErr))
		}
		return nil, stageErr
	}

	if err := g.run(ctx, log, "init", "--quiet", directory); err != nil {
		return fail("initialize repository", err)
	}
	if err := g.run(ctx, log, "-C", directory, "remote", "add", "origin", repositoryURL); err != nil {
		return fail("configure repository remote", err)
	}
	if err := g.run(ctx, log, "-C", directory, "fetch", "--depth=1", "--no-tags", "origin", ref); err != nil {
		return fail("fetch repository ref", err)
	}
	if err := g.run(ctx, log, "-C", directory, "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return fail("checkout repository ref", err)
	}
	sha, err := g.output(ctx, "-C", directory, "rev-parse", "HEAD")
	if err != nil {
		return fail("resolve commit SHA", err)
	}
	sha = strings.TrimSpace(sha)
	if len(sha) != 40 {
		return fail("resolve commit SHA", fmt.Errorf("unexpected SHA %q", sha))
	}
	if err := validateBuildContext(ctx, directory); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fail("validate build context", err)
		}
		return fail("validate build context", failure.Permanent(err))
	}
	checkout.CommitSHA = sha
	return checkout, nil
}

func ValidateBuildContext(directory string) error {
	return validateBuildContext(context.Background(), directory)
}

const (
	maximumBuildContextBytes = int64(512 << 20)
	maximumBuildContextFiles = 100_000
)

func validateBuildContext(ctx context.Context, directory string) error {
	root, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("resolve build context: %w", err)
	}
	dockerfile := filepath.Join(root, "Dockerfile")
	info, err := os.Lstat(dockerfile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("repository root does not contain a Dockerfile")
		}
		return fmt.Errorf("inspect Dockerfile: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return errors.New("Dockerfile must be a regular file no larger than 1 MiB")
	}
	var totalBytes int64
	fileCount := 0
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			fileCount++
			if fileCount > maximumBuildContextFiles {
				return fmt.Errorf("build context contains more than %d files", maximumBuildContextFiles)
			}
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect build context entry: %w", err)
			}
			if info.Mode().IsRegular() {
				totalBytes += info.Size()
				if totalBytes > maximumBuildContextBytes {
					return fmt.Errorf("build context exceeds %d MiB", maximumBuildContextBytes>>20)
				}
			}
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve symlink %s: %w", filepath.Base(path), err)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return fmt.Errorf("resolve symlink target: %w", err)
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("symlink %s escapes the build context", filepath.Base(path))
		}
		return nil
	})
}

func (g *Git) run(ctx context.Context, log LogFunc, args ...string) error {
	command := exec.CommandContext(ctx, g.executable, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	var wait sync.WaitGroup
	scanErrors := make(chan error, 2)
	wait.Add(2)
	go scanLines(stdout, log, &wait, scanErrors)
	go scanLines(stderr, log, &wait, scanErrors)
	wait.Wait()
	close(scanErrors)
	waitErr := command.Wait()
	for scanErr := range scanErrors {
		if scanErr != nil {
			return fmt.Errorf("read git output: %w", scanErr)
		}
	}
	if waitErr != nil {
		return fmt.Errorf("git exited unsuccessfully: %w", waitErr)
	}
	return nil
}

func (g *Git) output(ctx context.Context, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, g.executable, args...).Output()
	if err != nil {
		return "", fmt.Errorf("git exited unsuccessfully: %w", err)
	}
	return string(output), nil
}

func scanLines(reader io.Reader, log LogFunc, wait *sync.WaitGroup, result chan<- error) {
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
