package kubernetes

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/parthpandya5/forge-platform/internal/domain"
	"github.com/parthpandya5/forge-platform/internal/failure"
)

type Config struct {
	Kubeconfig   string
	Namespace    string
	IngressClass string
	BaseDomain   string
	PullPolicy   string
}

type Workload struct {
	DeploymentID  string
	ProjectID     string
	ProjectSlug   string
	ServiceID     string
	ServiceName   string
	Image         string
	Port          int32
	Replicas      int32
	HealthPath    string
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
	Environment   map[string]string
}

type Runtime struct {
	client       kubernetes.Interface
	namespace    string
	ingressClass string
	baseDomain   string
	pullPolicy   corev1.PullPolicy
}

func New(cfg Config) (*Runtime, error) {
	restConfig, err := loadRESTConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return NewWithClient(client, cfg)
}

func NewWithClient(client kubernetes.Interface, cfg Config) (*Runtime, error) {
	if client == nil {
		return nil, errors.New("Kubernetes client is required")
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "forge-apps"
	}
	if cfg.IngressClass == "" {
		cfg.IngressClass = "nginx"
	}
	if cfg.BaseDomain == "" {
		cfg.BaseDomain = "localhost"
	}
	cfg.BaseDomain = strings.TrimPrefix(cfg.BaseDomain, ".")
	if problems := validation.IsDNS1123Label(cfg.Namespace); len(problems) > 0 {
		return nil, fmt.Errorf("invalid Kubernetes namespace %q: %s", cfg.Namespace, strings.Join(problems, "; "))
	}
	if problems := validation.IsDNS1123Subdomain(cfg.IngressClass); len(problems) > 0 {
		return nil, fmt.Errorf("invalid ingress class %q: %s", cfg.IngressClass, strings.Join(problems, "; "))
	}
	if problems := validation.IsDNS1123Subdomain(cfg.BaseDomain); len(problems) > 0 || len(cfg.BaseDomain) > 189 {
		return nil, fmt.Errorf("invalid base domain %q", cfg.BaseDomain)
	}
	pullPolicy := corev1.PullIfNotPresent
	switch cfg.PullPolicy {
	case "", string(corev1.PullIfNotPresent):
	case string(corev1.PullAlways):
		pullPolicy = corev1.PullAlways
	case string(corev1.PullNever):
		pullPolicy = corev1.PullNever
	default:
		return nil, fmt.Errorf("unsupported image pull policy %q", cfg.PullPolicy)
	}
	return &Runtime{
		client: client, namespace: cfg.Namespace, ingressClass: cfg.IngressClass,
		baseDomain: cfg.BaseDomain, pullPolicy: pullPolicy,
	}, nil
}

func loadRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig: %w", err)
		}
		return config, nil
	}
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load default kubeconfig: %w", err)
	}
	return config, nil
}

func (r *Runtime) Reconcile(ctx context.Context, workload Workload) (string, error) {
	if err := validateWorkload(workload); err != nil {
		return "", failure.Permanent(err)
	}
	if err := r.ensureNamespace(ctx); err != nil {
		return "", err
	}
	name := resourceName(workload.ProjectSlug, workload.ServiceID)
	labels := workloadLabels(workload)
	if err := r.reconcileSecret(ctx, name, labels, workload.Environment); err != nil {
		return "", err
	}
	if err := r.reconcileDeployment(ctx, name, labels, workload); err != nil {
		return "", err
	}
	if err := r.reconcileService(ctx, name, labels, workload.Port); err != nil {
		return "", err
	}
	host := routeHost(workload.ProjectSlug, workload.ServiceName, workload.ServiceID, r.baseDomain)
	if err := r.reconcileIngress(ctx, name, labels, host); err != nil {
		return "", err
	}
	return "http://" + host, nil
}

func (r *Runtime) WaitReady(ctx context.Context, workload Workload) error {
	name := resourceName(workload.ProjectSlug, workload.ServiceID)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		deployment, err := r.client.AppsV1().Deployments(r.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if ctx.Err() != nil {
				return r.rolloutTimeoutError(context.Cause(ctx), name, workload)
			}
			return fmt.Errorf("read Kubernetes deployment: %w", err)
		}
		for _, condition := range deployment.Status.Conditions {
			if condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionFalse && condition.Reason == "ProgressDeadlineExceeded" {
				details := r.diagnostics(ctx, name, workload)
				return failure.Permanent(fmt.Errorf("rollout exceeded its progress deadline: %s", details))
			}
		}
		if deployment.Status.ObservedGeneration >= deployment.Generation &&
			deployment.Status.UpdatedReplicas == workload.Replicas &&
			deployment.Status.AvailableReplicas == workload.Replicas &&
			deployment.Status.UnavailableReplicas == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return r.rolloutTimeoutError(context.Cause(ctx), name, workload)
		case <-ticker.C:
		}
	}
}

func (r *Runtime) RuntimeLogs(ctx context.Context, workload Workload, tailLines int64) ([]string, error) {
	selector := "forge.service-id=" + workload.ServiceID
	pods, err := r.client.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("list application pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return []string{}, nil
	}
	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].CreationTimestamp.After(pods.Items[j].CreationTimestamp.Time)
	})
	stream, err := r.client.CoreV1().Pods(r.namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{
		Container: "web", TailLines: &tailLines,
	}).Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("open application log stream: %w", err)
	}
	lines, readErr := readBoundedLines(stream, 2000)
	if closeErr := stream.Close(); closeErr != nil {
		readErr = errors.Join(readErr, fmt.Errorf("close application log stream: %w", closeErr))
	}
	return lines, readErr
}

func (r *Runtime) CheckHealth(ctx context.Context, workload Workload) (domain.ServiceHealthStatus, string, error) {
	name := resourceName(workload.ProjectSlug, workload.ServiceID)
	deployment, err := r.client.AppsV1().Deployments(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return domain.ServiceHealthUnhealthy, "managed Kubernetes Deployment is missing", nil
	}
	if err != nil {
		return domain.ServiceHealthUnknown, "", fmt.Errorf("read workload health: %w", err)
	}
	if deployment.Status.AvailableReplicas >= workload.Replicas && deployment.Status.UnavailableReplicas == 0 {
		return domain.ServiceHealthRunning, fmt.Sprintf("%d/%d replicas available", deployment.Status.AvailableReplicas, workload.Replicas), nil
	}
	if deployment.Status.AvailableReplicas > 0 {
		return domain.ServiceHealthDegraded, fmt.Sprintf("%d/%d replicas available", deployment.Status.AvailableReplicas, workload.Replicas), nil
	}
	return domain.ServiceHealthUnhealthy, r.diagnostics(ctx, name, workload), nil
}

func (r *Runtime) DeleteWorkload(ctx context.Context, projectSlug, serviceID string) error {
	name := resourceName(projectSlug, serviceID)
	return r.deleteResources(ctx, name)
}

func (r *Runtime) GarbageCollect(ctx context.Context, validServiceIDs map[string]struct{}) error {
	selector := "app.kubernetes.io/managed-by=forge"
	orphanNames := make(map[string]struct{})
	addOrphan := func(name, serviceID string) {
		if _, valid := validServiceIDs[serviceID]; !valid {
			orphanNames[name] = struct{}{}
		}
	}

	deployments, err := r.client.AppsV1().Deployments(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list managed Deployments for garbage collection: %w", err)
	}
	for _, deployment := range deployments.Items {
		addOrphan(deployment.Name, deployment.Labels["forge.service-id"])
	}
	services, err := r.client.CoreV1().Services(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list managed Services for garbage collection: %w", err)
	}
	for _, service := range services.Items {
		addOrphan(service.Name, service.Labels["forge.service-id"])
	}
	ingresses, err := r.client.NetworkingV1().Ingresses(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list managed Ingresses for garbage collection: %w", err)
	}
	for _, ingress := range ingresses.Items {
		addOrphan(ingress.Name, ingress.Labels["forge.service-id"])
	}
	secrets, err := r.client.CoreV1().Secrets(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list managed Secrets for garbage collection: %w", err)
	}
	for _, secret := range secrets.Items {
		addOrphan(strings.TrimSuffix(secret.Name, "-env"), secret.Labels["forge.service-id"])
	}

	for name := range orphanNames {
		if err := r.deleteResources(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) deleteResources(ctx context.Context, name string) error {
	deletePolicy := metav1.DeletePropagationBackground
	options := metav1.DeleteOptions{PropagationPolicy: &deletePolicy}
	deletions := []struct {
		kind string
		call func() error
	}{
		{"Ingress", func() error { return r.client.NetworkingV1().Ingresses(r.namespace).Delete(ctx, name, options) }},
		{"Service", func() error { return r.client.CoreV1().Services(r.namespace).Delete(ctx, name, options) }},
		{"Deployment", func() error { return r.client.AppsV1().Deployments(r.namespace).Delete(ctx, name, options) }},
		{"Secret", func() error { return r.client.CoreV1().Secrets(r.namespace).Delete(ctx, name+"-env", options) }},
	}
	var result error
	for _, deletion := range deletions {
		if err := deletion.call(); err != nil && !apierrors.IsNotFound(err) {
			result = errors.Join(result, fmt.Errorf("delete workload %s: %w", deletion.kind, err))
		}
	}
	return result
}

func (r *Runtime) ensureNamespace(ctx context.Context) error {
	namespaces := r.client.CoreV1().Namespaces()
	_, err := namespaces.Get(ctx, r.namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("read Forge namespace: %w", err)
	}
	_, err = namespaces.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: r.namespace,
		Labels: map[string]string{
			"app.kubernetes.io/managed-by":       "forge",
			"pod-security.kubernetes.io/enforce": "baseline",
		},
	}}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create Forge namespace: %w", err)
	}
	return nil
}

func (r *Runtime) reconcileSecret(ctx context.Context, name string, labels, environment map[string]string) error {
	secrets := r.client.CoreV1().Secrets(r.namespace)
	desiredData := make(map[string][]byte, len(environment))
	for key, value := range environment {
		desiredData[key] = []byte(value)
	}
	desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-env", Namespace: r.namespace, Labels: labels}, Type: corev1.SecretTypeOpaque, Data: desiredData}
	existing, err := secrets.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := secrets.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create workload Secret: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read workload Secret: %w", err)
	}
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := secrets.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update workload Secret: %w", err)
	}
	return nil
}

func (r *Runtime) reconcileDeployment(ctx context.Context, name string, labels map[string]string, workload Workload) error {
	replicas := workload.Replicas
	progressDeadline := int32(300)
	maxUnavailable := intstr.FromInt32(0)
	maxSurge := intstr.FromInt32(1)
	allowPrivilegeEscalation := false
	runAsNonRoot := true
	const unprivilegedUID int64 = 65532
	probe := &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: workload.HealthPath, Port: intstr.FromString("http")}},
		InitialDelaySeconds: 2, PeriodSeconds: 5, TimeoutSeconds: 2, FailureThreshold: 12, SuccessThreshold: 1,
	}
	requests := corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse(workload.CPURequest), corev1.ResourceMemory: resource.MustParse(workload.MemoryRequest),
	}
	limits := corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse(workload.CPULimit), corev1.ResourceMemory: resource.MustParse(workload.MemoryLimit),
	}
	container := corev1.Container{
		Name: "web", Image: workload.Image, ImagePullPolicy: r.pullPolicy,
		Ports:          []corev1.ContainerPort{{Name: "http", ContainerPort: workload.Port}},
		ReadinessProbe: probe.DeepCopy(), LivenessProbe: probe.DeepCopy(),
		Resources: corev1.ResourceRequirements{Requests: requests, Limits: limits},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &allowPrivilegeEscalation, RunAsNonRoot: &runAsNonRoot,
			RunAsUser: int64Pointer(unprivilegedUID), RunAsGroup: int64Pointer(unprivilegedUID),
			Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
	if len(workload.Environment) > 0 {
		container.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: name + "-env"}}}}
	}
	podLabels := copyMap(labels)
	podLabels["forge.deployment-id"] = workload.DeploymentID
	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas, ProgressDeadlineSeconds: &progressDeadline,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"forge.service-id": workload.ServiceID}},
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: &maxUnavailable, MaxSurge: &maxSurge}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: boolPointer(false),
					SecurityContext:              &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
					Containers:                   []corev1.Container{container},
				},
			},
		},
	}
	deployments := r.client.AppsV1().Deployments(r.namespace)
	existing, err := deployments.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := deployments.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create workload Deployment: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read workload Deployment: %w", err)
	}
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := deployments.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update workload Deployment: %w", err)
	}
	return nil
}

func (r *Runtime) reconcileService(ctx context.Context, name string, labels map[string]string, port int32) error {
	services := r.client.CoreV1().Services(r.namespace)
	desiredSelector := map[string]string{"forge.service-id": labels["forge.service-id"]}
	existing, err := services.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := services.Create(ctx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.namespace, Labels: labels},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: desiredSelector, Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(port)}}},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create workload Service: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read workload Service: %w", err)
	}
	existing.Labels = labels
	existing.Spec.Selector = desiredSelector
	existing.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(port)}}
	if _, err := services.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update workload Service: %w", err)
	}
	return nil
}

func (r *Runtime) reconcileIngress(ctx context.Context, name string, labels map[string]string, host string) error {
	pathType := networkingv1.PathTypePrefix
	desired := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.namespace, Labels: labels},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &r.ingressClass,
			Rules: []networkingv1.IngressRule{{Host: host, IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
					Path: "/", PathType: &pathType,
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: name, Port: networkingv1.ServiceBackendPort{Number: 80}}},
				}}},
			}}},
		},
	}
	ingresses := r.client.NetworkingV1().Ingresses(r.namespace)
	existing, err := ingresses.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := ingresses.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create workload Ingress: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read workload Ingress: %w", err)
	}
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := ingresses.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update workload Ingress: %w", err)
	}
	return nil
}

func validateWorkload(workload Workload) error {
	if workload.DeploymentID == "" || workload.ProjectSlug == "" || workload.ServiceID == "" || workload.Image == "" {
		return errors.New("workload identifiers and image are required")
	}
	if problems := validation.IsDNS1123Label(workload.ProjectSlug); len(problems) > 0 {
		return fmt.Errorf("invalid project slug: %s", strings.Join(problems, "; "))
	}
	if workload.Port < 1 || workload.Port > 65535 || workload.Replicas < 1 || workload.Replicas > 20 {
		return errors.New("workload port or replica count is outside allowed bounds")
	}
	for name, value := range map[string]string{
		"deployment ID": workload.DeploymentID,
		"project ID":    workload.ProjectID,
		"service ID":    workload.ServiceID,
	} {
		if problems := validation.IsValidLabelValue(value); len(problems) > 0 {
			return fmt.Errorf("invalid %s label: %s", name, strings.Join(problems, "; "))
		}
	}
	if !strings.HasPrefix(workload.HealthPath, "/") || len(workload.HealthPath) > 512 {
		return errors.New("workload health path must be an absolute path of at most 512 bytes")
	}
	for key := range workload.Environment {
		if !domain.ValidEnvironmentKey(key) {
			return fmt.Errorf("invalid environment key %q", key)
		}
	}
	cpuRequest, err := resource.ParseQuantity(workload.CPURequest)
	if err != nil {
		return fmt.Errorf("invalid CPU request: %w", err)
	}
	cpuLimit, err := resource.ParseQuantity(workload.CPULimit)
	if err != nil {
		return fmt.Errorf("invalid CPU limit: %w", err)
	}
	memoryRequest, err := resource.ParseQuantity(workload.MemoryRequest)
	if err != nil {
		return fmt.Errorf("invalid memory request: %w", err)
	}
	memoryLimit, err := resource.ParseQuantity(workload.MemoryLimit)
	if err != nil {
		return fmt.Errorf("invalid memory limit: %w", err)
	}
	if cpuRequest.Cmp(cpuLimit) > 0 || memoryRequest.Cmp(memoryLimit) > 0 {
		return errors.New("resource requests must not exceed resource limits")
	}
	if cpuLimit.Cmp(resource.MustParse("8")) > 0 || memoryLimit.Cmp(resource.MustParse("16Gi")) > 0 {
		return errors.New("resource limits exceed Forge safety bounds")
	}
	return nil
}

func (r *Runtime) rolloutTimeoutError(cause error, name string, workload Workload) error {
	if errors.Is(cause, context.Canceled) {
		return fmt.Errorf("rollout interrupted: %w", cause)
	}
	details := r.diagnostics(context.Background(), name, workload)
	return failure.Permanent(fmt.Errorf("rollout did not become ready: %v; %s", cause, details))
}

func (r *Runtime) diagnostics(parent context.Context, name string, workload Workload) string {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	pods, err := r.client.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: "forge.service-id=" + workload.ServiceID})
	if err != nil {
		return "pod diagnostics unavailable: " + err.Error()
	}
	details := make([]string, 0)
	for _, pod := range pods.Items {
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Waiting != nil {
				details = append(details, fmt.Sprintf("pod %s waiting: %s (%s)", pod.Name, status.State.Waiting.Reason, status.State.Waiting.Message))
			}
			if status.State.Terminated != nil {
				details = append(details, fmt.Sprintf("pod %s terminated: %s exit=%d", pod.Name, status.State.Terminated.Reason, status.State.Terminated.ExitCode))
			}
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Status == corev1.ConditionFalse && condition.Message != "" {
				details = append(details, fmt.Sprintf("pod %s: %s", pod.Name, condition.Message))
			}
		}
	}
	if len(details) == 0 {
		return "no terminal pod condition reported for " + name
	}
	message := strings.Join(details, "; ")
	if len(message) > 2000 {
		cut := 2000
		for cut > 0 && !utf8.RuneStart(message[cut]) {
			cut--
		}
		message = message[:cut]
	}
	return message
}

func workloadLabels(workload Workload) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "forge",
		"app.kubernetes.io/name":       resourceName(workload.ProjectSlug, workload.ServiceID),
		"forge.project-id":             workload.ProjectID,
		"forge.service-id":             workload.ServiceID,
	}
}

func resourceName(projectSlug, serviceID string) string {
	suffix := strings.ReplaceAll(serviceID, "-", "")
	if len(suffix) > 10 {
		suffix = suffix[:10]
	}
	prefix := sanitizeDNSLabel(projectSlug)
	maxPrefix := 63 - len("forge--") - len(suffix)
	if len(prefix) > maxPrefix {
		prefix = strings.Trim(prefix[:maxPrefix], "-")
	}
	return "forge-" + prefix + "-" + suffix
}

func routeHost(projectSlug, serviceName, serviceID, baseDomain string) string {
	service := sanitizeDNSLabel(serviceName)
	if serviceName == "web" {
		return projectSlug + "." + baseDomain
	}
	suffix := strings.ReplaceAll(serviceID, "-", "")
	if len(suffix) > 10 {
		suffix = suffix[:10]
	}
	label := service + "-" + projectSlug + "-" + suffix
	if len(label) > 63 {
		label = strings.Trim(label[:63-len(suffix)-1], "-") + "-" + suffix
	}
	return label + "." + baseDomain
}

func sanitizeDNSLabel(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	lastHyphen := false
	for _, character := range value {
		valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if valid {
			builder.WriteRune(character)
			lastHyphen = false
		} else if !lastHyphen && builder.Len() > 0 {
			builder.WriteByte('-')
			lastHyphen = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "service"
	}
	return result
}

func copyMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input)+1)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func boolPointer(value bool) *bool    { return &value }
func int64Pointer(value int64) *int64 { return &value }

func readBoundedLines(reader io.Reader, maximum int) ([]string, error) {
	lines := make([]string, 0)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() && len(lines) < maximum {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read application logs: %w", err)
	}
	return lines, nil
}
