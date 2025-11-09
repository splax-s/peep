package kubernetes

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"

	"github.com/splax/localvercel/builder/internal/runtime"
)

const (
	runtimeDeploymentLabel = "peep.dev/deployment-id"
	runtimeProjectLabel    = "peep.dev/project-id"
)

// Manager provisions runtime workloads inside Kubernetes.
type Manager struct {
	client           kubernetes.Interface
	namespace        string
	serviceDomain    string
	servicePort      int
	logger           *slog.Logger
	readinessTimeout time.Duration
}

// New creates a Kubernetes-backed runtime manager. It prefers in-cluster configuration
// and falls back to KUBECONFIG when running locally.
func New(namespace, serviceDomain string, servicePort int, readinessTimeout time.Duration, log *slog.Logger) (*Manager, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := strings.TrimSpace(os.Getenv("KUBECONFIG"))
		if kubeconfig == "" {
			return nil, fmt.Errorf("create in-cluster config: %w", err)
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("create kubeconfig client: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	if readinessTimeout <= 0 {
		readinessTimeout = 2 * time.Minute
	}
	if servicePort <= 0 {
		servicePort = 3000
	}

	mgr := &Manager{
		client:           clientset,
		namespace:        namespace,
		serviceDomain:    strings.TrimSuffix(serviceDomain, "."),
		servicePort:      servicePort,
		logger:           log,
		readinessTimeout: readinessTimeout,
	}
	return mgr, nil
}

// Deploy applies or updates the runtime Deployment and Service, returning once the pod is ready.
func (m *Manager) Deploy(ctx context.Context, req runtime.Request) (runtime.Deployment, error) {
	name := runtimeResourceName(req.DeploymentID)
	if name == "" {
		return runtime.Deployment{}, fmt.Errorf("deployment id required")
	}

	labels := map[string]string{
		runtimeDeploymentLabel:        req.DeploymentID,
		runtimeProjectLabel:           req.ProjectID,
		"app.kubernetes.io/name":      "runtime",
		"app.kubernetes.io/component": "preview",
	}

	var replicas int32 = 1
	revisionHistory := pointer.Int32(1)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:             &replicas,
			RevisionHistoryLimit: revisionHistory,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					runtimeDeploymentLabel: req.DeploymentID,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						buildRuntimeContainer(req),
					},
				},
			},
		},
	}

	if err := m.applyDeployment(ctx, deployment); err != nil {
		return runtime.Deployment{}, err
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				runtimeDeploymentLabel: req.DeploymentID,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       int32(m.servicePort),
					TargetPort: intstr.FromInt(req.Port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	if err := m.applyService(ctx, svc); err != nil {
		return runtime.Deployment{}, err
	}

	pod, err := m.waitForReadyPod(ctx, req.DeploymentID, req.Timeout)
	if err != nil {
		return runtime.Deployment{}, err
	}

	host := m.runtimeHost(name)
	result := runtime.Deployment{
		DeploymentName: name,
		ServiceName:    name,
		PodName:        pod.Name,
		Host:           host,
		Port:           m.servicePort,
		StartedAt:      podStartTime(pod),
	}
	return result, nil
}

// Cancel removes runtime resources for the deployment id.
func (m *Manager) Cancel(ctx context.Context, deploymentID string) error {
	name := runtimeResourceName(deploymentID)
	if name == "" {
		return fmt.Errorf("deployment id required")
	}
	if err := m.client.AppsV1().Deployments(m.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete deployment: %w", err)
	}
	if err := m.client.CoreV1().Services(m.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete service: %w", err)
	}
	return nil
}

// PodStatus reports the latest pod state for the deployment.
func (m *Manager) PodStatus(ctx context.Context, deploymentID string) (runtime.PodStatus, error) {
	pods, err := m.client.CoreV1().Pods(m.namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector(deploymentID)})
	if err != nil {
		return runtime.PodStatus{}, fmt.Errorf("list runtime pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return runtime.PodStatus{}, fmt.Errorf("runtime pod not found")
	}
	pod := pods.Items[0]
	status := runtime.PodStatus{
		Phase:       string(pod.Status.Phase),
		Reason:      pod.Status.Reason,
		Message:     pod.Status.Message,
		ContainerID: firstContainerID(pod.Status.ContainerStatuses),
	}
	if pod.Status.StartTime != nil {
		started := pod.Status.StartTime.Time
		status.StartedAt = &started
	}
	if ready := isPodReady(&pod); ready {
		status.Ready = true
	}
	if status.Reason == "" {
		status.Reason = containerReason(pod.Status.ContainerStatuses)
	}
	if status.Message == "" {
		status.Message = containerMessage(pod.Status.ContainerStatuses)
	}
	return status, nil
}

func (m *Manager) applyDeployment(ctx context.Context, desired *appsv1.Deployment) error {
	deployments := m.client.AppsV1().Deployments(m.namespace)
	_, err := deployments.Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create deployment: %w", err)
	}
	existing, getErr := deployments.Get(ctx, desired.Name, metav1.GetOptions{})
	if getErr != nil {
		return fmt.Errorf("get deployment: %w", getErr)
	}
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := deployments.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update deployment: %w", err)
	}
	return nil
}

func (m *Manager) applyService(ctx context.Context, desired *corev1.Service) error {
	services := m.client.CoreV1().Services(m.namespace)
	_, err := services.Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create service: %w", err)
	}
	existing, getErr := services.Get(ctx, desired.Name, metav1.GetOptions{})
	if getErr != nil {
		return fmt.Errorf("get service: %w", getErr)
	}
	desired.ResourceVersion = existing.ResourceVersion
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	desired.Spec.ClusterIPs = existing.Spec.ClusterIPs
	if _, err := services.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update service: %w", err)
	}
	return nil
}

func (m *Manager) waitForReadyPod(ctx context.Context, deploymentID string, timeout time.Duration) (*corev1.Pod, error) {
	waitTimeout := timeout
	if waitTimeout <= 0 {
		waitTimeout = m.readinessTimeout
	}
	var readyPod *corev1.Pod
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, waitTimeout, true, func(ctx context.Context) (bool, error) {
		pods, err := m.client.CoreV1().Pods(m.namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector(deploymentID)})
		if err != nil {
			return false, err
		}
		if len(pods.Items) == 0 {
			return false, nil
		}
		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodFailed {
				msg := pod.Status.Message
				if msg == "" {
					s := containerMessage(pod.Status.ContainerStatuses)
					if s != "" {
						msg = s
					}
				}
				return false, fmt.Errorf("runtime pod failed: %s", msg)
			}
			if pod.Status.Phase == corev1.PodRunning && isPodReady(&pod) {
				copy := pod.DeepCopy()
				readyPod = copy
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	if readyPod == nil {
		return nil, fmt.Errorf("runtime pod not ready")
	}
	return readyPod, nil
}

func (m *Manager) runtimeHost(serviceName string) string {
	if m.serviceDomain != "" {
		return fmt.Sprintf("%s.%s", serviceName, m.serviceDomain)
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, m.namespace)
}

func buildRuntimeContainer(req runtime.Request) corev1.Container {
	container := corev1.Container{
		Name:  "runtime",
		Image: req.Image,
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: int32(req.Port),
		}},
		Env: []corev1.EnvVar{{
			Name:  "PORT",
			Value: fmt.Sprintf("%d", req.Port),
		}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/",
					Port: intstr.FromInt(req.Port),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
			FailureThreshold:    6,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/",
					Port: intstr.FromInt(req.Port),
				},
			},
			InitialDelaySeconds: 20,
			PeriodSeconds:       20,
			FailureThreshold:    6,
		},
	}
	if len(req.Command) > 0 {
		container.Command = []string{req.Command[0]}
		if len(req.Command) > 1 {
			container.Args = req.Command[1:]
		}
	}
	return container
}

func runtimeResourceName(deploymentID string) string {
	trimmed := strings.ToLower(strings.TrimSpace(deploymentID))
	if trimmed == "" {
		return ""
	}
	alnum := make([]rune, 0, len(trimmed))
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			alnum = append(alnum, r)
		}
	}
	value := string(alnum)
	if len(value) > 24 {
		value = value[:24]
	}
	if value == "" {
		value = "runtime"
	}
	return fmt.Sprintf("runtime-%s", value)
}

func labelSelector(deploymentID string) string {
	return fmt.Sprintf("%s=%s", runtimeDeploymentLabel, deploymentID)
}

func isPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podStartTime(pod *corev1.Pod) time.Time {
	if pod.Status.StartTime != nil {
		return pod.Status.StartTime.Time
	}
	return time.Now().UTC()
}

func firstContainerID(statuses []corev1.ContainerStatus) string {
	if len(statuses) == 0 {
		return ""
	}
	return statuses[0].ContainerID
}

func containerReason(statuses []corev1.ContainerStatus) string {
	for _, s := range statuses {
		if s.State.Waiting != nil && s.State.Waiting.Reason != "" {
			return s.State.Waiting.Reason
		}
		if s.State.Terminated != nil && s.State.Terminated.Reason != "" {
			return s.State.Terminated.Reason
		}
	}
	return ""
}

func containerMessage(statuses []corev1.ContainerStatus) string {
	for _, s := range statuses {
		if s.State.Waiting != nil && s.State.Waiting.Message != "" {
			return s.State.Waiting.Message
		}
		if s.State.Terminated != nil && s.State.Terminated.Message != "" {
			return s.State.Terminated.Message
		}
	}
	return ""
}
