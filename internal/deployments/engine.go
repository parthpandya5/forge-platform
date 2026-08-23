package deployments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/parthpandya5/forge-platform/internal/domain"
	"github.com/parthpandya5/forge-platform/internal/failure"
	forgekubernetes "github.com/parthpandya5/forge-platform/internal/kubernetes"
	"github.com/parthpandya5/forge-platform/internal/secrets"
	"github.com/parthpandya5/forge-platform/internal/source"
	"github.com/parthpandya5/forge-platform/internal/store"
)

type Repository interface {
	GetDeploymentContext(context.Context, uuid.UUID) (store.DeploymentContext, error)
	TransitionDeployment(context.Context, uuid.UUID, domain.DeploymentStatus, string, string, error) (domain.Deployment, error)
	SetDeploymentSource(context.Context, uuid.UUID, string) error
	SetDeploymentImage(context.Context, uuid.UUID, string) error
	SetDeploymentRoute(context.Context, uuid.UUID, string) error
	AppendEvent(context.Context, uuid.UUID, string, string, map[string]any) error
	AppendLog(context.Context, uuid.UUID, string, string) error
	DeploymentEnvironment(context.Context, uuid.UUID) ([]store.EncryptedEnvironmentVariable, error)
}

type Source interface {
	Checkout(context.Context, uuid.UUID, string, string, source.LogFunc) (*source.Checkout, error)
}

type ImageBuilder interface {
	ImageTag(projectSlug, deploymentID string) string
	Build(context.Context, string, string, source.LogFunc) error
	Push(context.Context, string, source.LogFunc) error
}

type Runtime interface {
	Reconcile(context.Context, forgekubernetes.Workload) (string, error)
	WaitReady(context.Context, forgekubernetes.Workload) error
	RuntimeLogs(context.Context, forgekubernetes.Workload, int64) ([]string, error)
}

type Config struct {
	CloneTimeout         time.Duration
	BuildTimeout         time.Duration
	RolloutTimeout       time.Duration
	PushImages           bool
	ObserveBuildDuration func(time.Duration)
}

type Engine struct {
	repository Repository
	source     Source
	builder    ImageBuilder
	runtime    Runtime
	cipher     *secrets.Cipher
	logger     *slog.Logger
	config     Config
}

func NewEngine(repository Repository, sourceAdapter Source, builder ImageBuilder, runtime Runtime, cipher *secrets.Cipher, logger *slog.Logger, cfg Config) *Engine {
	return &Engine{
		repository: repository, source: sourceAdapter, builder: builder, runtime: runtime,
		cipher: cipher, logger: logger, config: cfg,
	}
}

func (e *Engine) Process(ctx context.Context, deploymentID uuid.UUID) error {
	details, err := e.repository.GetDeploymentContext(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("load deployment: %w", err)
	}
	deployment := details.Deployment
	logger := e.logger.With(
		"deployment_id", deploymentID,
		"request_id", deployment.RequestID,
		"project_id", details.ProjectID,
		"service_id", details.Service.ID,
	)
	if deployment.Status.IsTerminal() {
		logger.Info("deployment job already terminal", "status", deployment.Status)
		return nil
	}
	logger.Info("deployment processing started", "status", deployment.Status)
	var checkout *source.Checkout
	defer func() {
		if checkout != nil {
			if err := checkout.Cleanup(); err != nil {
				logger.Warn("remove checkout directory", "error", err)
			}
		}
	}()

	for !deployment.Status.IsTerminal() {
		switch deployment.Status {
		case domain.DeploymentQueued:
			if deployment.RollbackOf != nil {
				deployment, err = e.transition(ctx, deploymentID, domain.DeploymentDeploying, "deployment.rollback_started", "Restoring previous immutable image", nil)
			} else {
				deployment, err = e.transition(ctx, deploymentID, domain.DeploymentCloning, "deployment.clone_started", "Source checkout started", nil)
			}

		case domain.DeploymentCloning:
			ref := valueOr(deployment.CommitSHA, valueOr(deployment.RequestedRef, details.RepositoryBranch))
			cloneCtx, cancel := context.WithTimeout(ctx, e.config.CloneTimeout)
			logWriter := newStageLogger(cloneCtx, e.repository, deploymentID, "system")
			checkout, err = e.source.Checkout(cloneCtx, deploymentID, details.RepositoryURL, ref, logWriter.Write)
			cancel()
			if err == nil {
				err = logWriter.Error()
			}
			if err != nil {
				return fmt.Errorf("clone source: %w", err)
			}
			if deployment.CommitSHA != nil && checkout.CommitSHA != *deployment.CommitSHA {
				return failure.Permanent(fmt.Errorf("retried checkout resolved %s, expected immutable commit %s", checkout.CommitSHA, *deployment.CommitSHA))
			}
			if deployment.CommitSHA == nil {
				if err := e.repository.SetDeploymentSource(ctx, deploymentID, checkout.CommitSHA); err != nil {
					return fmt.Errorf("record source commit: %w", err)
				}
			}
			commit := checkout.CommitSHA
			deployment.CommitSHA = &commit
			if err := e.repository.AppendEvent(ctx, deploymentID, "deployment.clone_completed", "Source checkout completed", map[string]any{"commit_sha": commit}); err != nil {
				return err
			}
			deployment, err = e.transition(ctx, deploymentID, domain.DeploymentBuilding, "deployment.build_started", "Docker image build started", nil)

		case domain.DeploymentBuilding:
			imageTag := valueOr(deployment.ImageTag, e.builder.ImageTag(details.ProjectSlug, deploymentID.String()))
			if deployment.ImageTag == nil {
				if checkout == nil {
					ref := valueOr(deployment.CommitSHA, valueOr(deployment.RequestedRef, details.RepositoryBranch))
					cloneCtx, cancel := context.WithTimeout(ctx, e.config.CloneTimeout)
					logWriter := newStageLogger(cloneCtx, e.repository, deploymentID, "system")
					checkout, err = e.source.Checkout(cloneCtx, deploymentID, details.RepositoryURL, ref, logWriter.Write)
					cancel()
					if err == nil {
						err = logWriter.Error()
					}
					if err != nil {
						return fmt.Errorf("restore source checkout: %w", err)
					}
					if deployment.CommitSHA != nil && checkout.CommitSHA != *deployment.CommitSHA {
						return failure.Permanent(fmt.Errorf("retried checkout resolved %s, expected immutable commit %s", checkout.CommitSHA, *deployment.CommitSHA))
					}
				}
				buildCtx, cancel := context.WithTimeout(ctx, e.config.BuildTimeout)
				logWriter := newStageLogger(buildCtx, e.repository, deploymentID, "build")
				buildStarted := time.Now()
				err = e.builder.Build(buildCtx, checkout.Directory, imageTag, logWriter.Write)
				if e.config.ObserveBuildDuration != nil {
					e.config.ObserveBuildDuration(time.Since(buildStarted))
				}
				cancel()
				if err == nil {
					err = logWriter.Error()
				}
				if err != nil {
					return fmt.Errorf("build source: %w", err)
				}
				if err := e.repository.SetDeploymentImage(ctx, deploymentID, imageTag); err != nil {
					return fmt.Errorf("record image tag: %w", err)
				}
				deployment.ImageTag = &imageTag
			}
			if err := e.repository.AppendEvent(ctx, deploymentID, "deployment.build_completed", "Docker image artifact is available", map[string]any{"image_tag": imageTag}); err != nil {
				return err
			}
			if e.config.PushImages {
				deployment, err = e.transition(ctx, deploymentID, domain.DeploymentPushing, "deployment.push_started", "Image registry push started", nil)
			} else {
				deployment, err = e.transition(ctx, deploymentID, domain.DeploymentDeploying, "deployment.reconcile_started", "Kubernetes reconciliation started", nil)
			}

		case domain.DeploymentPushing:
			if deployment.ImageTag == nil {
				return failure.Permanent(errors.New("deployment reached PUSHING without an image tag"))
			}
			pushCtx, cancel := context.WithTimeout(ctx, e.config.BuildTimeout)
			logWriter := newStageLogger(pushCtx, e.repository, deploymentID, "build")
			err = e.builder.Push(pushCtx, *deployment.ImageTag, logWriter.Write)
			cancel()
			if err == nil {
				err = logWriter.Error()
			}
			if err != nil {
				return fmt.Errorf("push image: %w", err)
			}
			if err := e.repository.AppendEvent(ctx, deploymentID, "deployment.push_completed", "Image registry push completed", map[string]any{"image_tag": *deployment.ImageTag}); err != nil {
				return err
			}
			deployment, err = e.transition(ctx, deploymentID, domain.DeploymentDeploying, "deployment.reconcile_started", "Kubernetes reconciliation started", nil)

		case domain.DeploymentDeploying:
			if deployment.ImageTag == nil {
				return failure.Permanent(errors.New("deployment reached DEPLOYING without an image tag"))
			}
			workload, workloadErr := e.workload(ctx, details, deployment)
			if workloadErr != nil {
				return workloadErr
			}
			reconcileCtx, cancel := context.WithTimeout(ctx, e.config.RolloutTimeout)
			route, reconcileErr := e.runtime.Reconcile(reconcileCtx, workload)
			cancel()
			if reconcileErr != nil {
				return fmt.Errorf("reconcile Kubernetes workload: %w", reconcileErr)
			}
			if err := e.repository.SetDeploymentRoute(ctx, deploymentID, route); err != nil {
				return fmt.Errorf("record deployment route: %w", err)
			}
			deployment.RouteURL = &route
			if err := e.repository.AppendEvent(ctx, deploymentID, "deployment.reconciled", "Kubernetes resources reconciled", map[string]any{"route_url": route}); err != nil {
				return err
			}
			deployment, err = e.transition(ctx, deploymentID, domain.DeploymentVerifying, "deployment.verification_started", "Waiting for Kubernetes readiness", nil)

		case domain.DeploymentVerifying:
			workload, workloadErr := e.workload(ctx, details, deployment)
			if workloadErr != nil {
				return workloadErr
			}
			rolloutCtx, cancel := context.WithTimeout(ctx, e.config.RolloutTimeout)
			err = e.runtime.WaitReady(rolloutCtx, workload)
			cancel()
			if err != nil {
				return fmt.Errorf("verify Kubernetes rollout: %w", err)
			}
			e.captureRuntimeLogs(ctx, deploymentID, workload, logger)
			deployment, err = e.transition(ctx, deploymentID, domain.DeploymentLive, "deployment.live", "Deployment is live and ready", nil)

		default:
			return failure.Permanent(fmt.Errorf("unsupported deployment status %s", deployment.Status))
		}
		if err != nil {
			return err
		}
	}
	logger.Info("deployment processing finished", "status", deployment.Status)
	return nil
}

func (e *Engine) MarkFailed(ctx context.Context, deploymentID uuid.UUID, cause error) error {
	details, err := e.repository.GetDeploymentContext(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("load deployment to fail: %w", err)
	}
	if details.Deployment.Status.IsTerminal() {
		return nil
	}
	message := "Deployment failed: " + cause.Error()
	_, err = e.transition(ctx, deploymentID, domain.DeploymentFailed, "deployment.failed", message, cause)
	return err
}

func (e *Engine) transition(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, eventType, message string, cause error) (domain.Deployment, error) {
	deployment, err := e.repository.TransitionDeployment(ctx, id, status, eventType, message, cause)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("transition deployment to %s: %w", status, err)
	}
	return deployment, nil
}

func (e *Engine) workload(ctx context.Context, details store.DeploymentContext, deployment domain.Deployment) (forgekubernetes.Workload, error) {
	if deployment.ImageTag == nil {
		return forgekubernetes.Workload{}, failure.Permanent(errors.New("deployment image is missing"))
	}
	encrypted, err := e.repository.DeploymentEnvironment(ctx, deployment.ID)
	if err != nil {
		return forgekubernetes.Workload{}, fmt.Errorf("load deployment environment: %w", err)
	}
	environment := make(map[string]string, len(encrypted))
	for _, item := range encrypted {
		variable := item.EnvironmentVariable
		plaintext, err := e.cipher.Decrypt(item.Ciphertext, item.Nonce, []byte(variable.ServiceID.String()+":"+variable.Key))
		if err != nil {
			return forgekubernetes.Workload{}, failure.Permanent(fmt.Errorf("decrypt environment variable %s: %w", variable.Key, err))
		}
		environment[variable.Key] = string(plaintext)
	}
	return forgekubernetes.Workload{
		DeploymentID: deployment.ID.String(), ProjectID: details.ProjectID.String(), ProjectSlug: details.ProjectSlug,
		ServiceID: details.Service.ID.String(), ServiceName: details.Service.Name, Image: *deployment.ImageTag,
		Port: int32(details.Service.InternalPort), Replicas: int32(details.Service.DesiredReplicas),
		HealthPath: details.Service.HealthCheckPath, CPURequest: details.Service.CPURequest,
		CPULimit: details.Service.CPULimit, MemoryRequest: details.Service.MemoryRequest,
		MemoryLimit: details.Service.MemoryLimit, Environment: environment,
	}, nil
}

func (e *Engine) captureRuntimeLogs(ctx context.Context, deploymentID uuid.UUID, workload forgekubernetes.Workload, logger *slog.Logger) {
	logCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lines, err := e.runtime.RuntimeLogs(logCtx, workload, 200)
	if err != nil {
		logger.Warn("capture initial runtime logs", "error", err)
		return
	}
	for _, line := range lines {
		if err := e.repository.AppendLog(logCtx, deploymentID, "runtime", redactSecrets(line, workload.Environment)); err != nil {
			logger.Warn("persist runtime log", "error", err)
			return
		}
	}
}

func redactSecrets(line string, environment map[string]string) string {
	values := make([]string, 0, len(environment))
	for _, value := range environment {
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		line = strings.ReplaceAll(line, value, "[REDACTED]")
	}
	return line
}

func valueOr(value *string, fallback string) string {
	if value != nil && *value != "" {
		return *value
	}
	return fallback
}

type stageLogger struct {
	ctx          context.Context
	repository   Repository
	deploymentID uuid.UUID
	stream       string
	mu           sync.Mutex
	err          error
	lines        int
	bytes        int
	truncated    bool
}

const (
	maximumStageLogLines = 20_000
	maximumStageLogBytes = 16 << 20
)

func newStageLogger(ctx context.Context, repository Repository, deploymentID uuid.UUID, stream string) *stageLogger {
	return &stageLogger{ctx: ctx, repository: repository, deploymentID: deploymentID, stream: stream}
}

func (l *stageLogger) Write(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return
	}
	if l.lines >= maximumStageLogLines || l.bytes+len(line) > maximumStageLogBytes {
		if !l.truncated {
			l.err = l.repository.AppendLog(l.ctx, l.deploymentID, l.stream, "Forge truncated stage output after reaching the 16 MiB or 20,000-line limit")
			l.truncated = true
		}
		return
	}
	if err := l.repository.AppendLog(l.ctx, l.deploymentID, l.stream, line); err != nil {
		l.err = err
		return
	}
	l.lines++
	l.bytes += len(line)
}

func (l *stageLogger) Error() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func IsPermanent(err error) bool { return failure.IsPermanent(err) }
