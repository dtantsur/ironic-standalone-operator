package ironic

import (
	"context"
	"fmt"
	"maps"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	probeInitialDelay     = 1
	probeTimeout          = 5
	probeFailureThreshold = 12

	dataDir        = "/data"
	confDir        = "/conf"
	tmpDir         = "/tmp"
	dataVolumeName = "ironic-data"
	tmpVolumeName  = "ironic-tmp"
)

type ControllerContext struct {
	Context     context.Context
	Client      client.Client
	KubeClient  kubernetes.Interface
	Scheme      *runtime.Scheme
	Logger      logr.Logger
	Domain      string
	VersionInfo VersionInfo
}

type Resources struct {
	Ironic             *metal3api.Ironic
	APISecret          *corev1.Secret
	TLSSecret          *corev1.Secret
	BMCCASecret        *corev1.Secret
	TrustedCAConfigMap *corev1.ConfigMap
}

func mergeContainers(target, source []corev1.Container) []corev1.Container {
	if len(source) == 0 {
		return source
	}

	if len(target) != len(source) {
		// No way to reconcile different lists, re-create
		target = make([]corev1.Container, len(source))
	}

	for idx, src := range source {
		dest := &target[idx]
		dest.Name = src.Name
		dest.Image = src.Image
		dest.Command = src.Command
		dest.Ports = src.Ports
		dest.Env = src.Env
		dest.EnvFrom = src.EnvFrom
		dest.VolumeMounts = src.VolumeMounts
		dest.SecurityContext = src.SecurityContext
		if src.LivenessProbe != nil {
			dest.LivenessProbe = updateProbe(dest.LivenessProbe, src.LivenessProbe.ProbeHandler)
		} else {
			dest.LivenessProbe = nil
		}
		if src.ReadinessProbe != nil {
			dest.ReadinessProbe = updateProbe(dest.ReadinessProbe, src.ReadinessProbe.ProbeHandler)
		} else {
			dest.ReadinessProbe = nil
		}
	}

	return target
}

// mergePodTemplates updates an existing pod template, taking care to avoid
// overriding defaulted values.
func mergePodTemplates(target *corev1.PodTemplateSpec, source corev1.PodTemplateSpec) {
	if target.Labels == nil {
		target.Labels = make(map[string]string, len(source.Labels))
	}
	maps.Copy(target.Labels, source.Labels)
	if source.Annotations != nil {
		if target.Annotations == nil {
			target.Annotations = make(map[string]string, len(source.Annotations))
		}
		maps.Copy(target.Annotations, source.Annotations)
	}

	target.Spec.InitContainers = mergeContainers(target.Spec.InitContainers, source.Spec.InitContainers)
	target.Spec.Containers = mergeContainers(target.Spec.Containers, source.Spec.Containers)
	target.Spec.Volumes = source.Spec.Volumes
	target.Spec.HostNetwork = source.Spec.HostNetwork
	if source.Spec.DNSPolicy != "" {
		target.Spec.DNSPolicy = source.Spec.DNSPolicy
	}
	if source.Spec.NodeSelector != nil {
		target.Spec.NodeSelector = source.Spec.NodeSelector
	}
	if source.Spec.RestartPolicy != "" {
		target.Spec.RestartPolicy = source.Spec.RestartPolicy
	}
}

func getDeploymentStatus(cctx ControllerContext, deploy *appsv1.Deployment) (Status, error) {
	if deploy.Status.ObservedGeneration != deploy.Generation {
		cctx.Logger.Info("deployment not ready yet", "Deployment", deploy.Name,
			"Generation", deploy.Generation, "ObservedGeneration", deploy.Status.ObservedGeneration)
		return inProgress("deployment not ready yet")
	}

	var available, updated bool
	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing {
			if cond.Status == corev1.ConditionFalse && cond.Reason == "ProgressDeadlineExceeded" {
				cctx.Logger.Info("deployment stopped progressing", "Deployment", deploy.Name, "Status", deploy.Status)
				err := fmt.Errorf("deployment stopped progressing: %s", cond.Message)
				return Status{Fatal: err}, nil
			}
			updated = cond.Reason == "NewReplicaSetAvailable" && deploy.Status.UpdatedReplicas >= deploy.Status.Replicas
		}
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
			available = true
		}
		if cond.Type == appsv1.DeploymentReplicaFailure && cond.Status == corev1.ConditionTrue {
			err := fmt.Errorf("deployment failed: %s", cond.Message)
			return transientError(err)
		}
	}

	if available && updated {
		return ready()
	}

	cctx.Logger.Info("deployment not available yet", "Deployment", deploy.Name, "Status", deploy.Status)
	return inProgress("deployment not available yet")
}

func getDaemonSetStatus(cctx ControllerContext, deploy *appsv1.DaemonSet) (Status, error) {
	if deploy.Status.ObservedGeneration != deploy.Generation {
		cctx.Logger.Info("daemon set not ready yet", "DaemonSet", deploy.Name,
			"Generation", deploy.Generation, "ObservedGeneration", deploy.Status.ObservedGeneration)
		return inProgress("daemon set not ready yet")
	}

	// FIXME(dtantsur): the current version of appsv1 does not seem to have
	// constants for conditions types.

	available := deploy.Status.NumberUnavailable == 0
	// NOTE(dtantsur): old replicas are not counted towards NumberUnavailable
	updated := deploy.Status.UpdatedNumberScheduled >= deploy.Status.DesiredNumberScheduled

	if available && updated {
		return ready()
	}

	cctx.Logger.Info("daemon set not available yet", "DaemonSet", deploy.Name,
		"NumberUnavailable", deploy.Status.NumberUnavailable, "UpdatedNumberScheduled", deploy.Status.UpdatedNumberScheduled)
	if !updated {
		return inProgress(fmt.Sprintf("daemon set not available yet: %d replicas need updating",
			deploy.Status.DesiredNumberScheduled-deploy.Status.UpdatedNumberScheduled))
	}
	return inProgress(fmt.Sprintf("daemon set not available yet: %d replicas unavailable", deploy.Status.NumberUnavailable))
}

func getServiceStatus(service *corev1.Service) (Status, error) {
	// TODO(dtantsur): can we check anything else?
	if len(service.Spec.ClusterIPs) == 0 {
		return inProgress("service has no cluster IPs")
	}

	return ready()
}

func getJobStatus(cctx ControllerContext, job *batchv1.Job, jobType string) (Status, error) {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return ready()
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			cctx.Logger.Info(cond.Message, "Job", job.Name)
			err := fmt.Errorf("%s job failed: %s", jobType, cond.Message)
			return Status{Fatal: err}, nil
		}
	}

	messageWithType := jobType + " job not complete yet"
	cctx.Logger.Info(messageWithType, "Job", job.Name, "Conditions", job.Status.Conditions)
	return inProgress(messageWithType)
}

func buildEndpoints(ips []string, port int, includeProto string) (endpoints []string) {
	portString := strconv.Itoa(port)
	for _, ip := range ips {
		var endpoint string
		if (includeProto == "https" && port == 443) || (includeProto == "http" && port == 80) {
			if strings.Contains(ip, ":") {
				endpoint = fmt.Sprintf("%s://[%s]", includeProto, ip) // IPv6
			} else {
				endpoint = fmt.Sprintf("%s://%s", includeProto, ip)
			}
		} else {
			endpoint = net.JoinHostPort(ip, portString)
			if includeProto != "" {
				endpoint = fmt.Sprintf("%s://%s", includeProto, endpoint)
			}
		}

		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)
	return
}

func updateProbe(current *corev1.Probe, handler corev1.ProbeHandler) *corev1.Probe {
	if current == nil {
		current = &corev1.Probe{}
	}
	current.ProbeHandler = handler
	// NOTE(dtantsur): we want some delay because Ironic does not start instantly.
	// Also be conservative about failing the pod since Ironic restars are not cheap (the database is wiped).
	current.InitialDelaySeconds = probeInitialDelay
	current.TimeoutSeconds = probeTimeout
	current.FailureThreshold = probeFailureThreshold
	return current
}

func newProbe(handler corev1.ProbeHandler) *corev1.Probe {
	return updateProbe(nil, handler) // TODO: remove
}

func appendStringEnv(envVars []corev1.EnvVar, name string, value string) []corev1.EnvVar {
	if value != "" {
		return append(envVars, corev1.EnvVar{
			Name:  name,
			Value: value,
		})
	}

	return envVars
}

func appendListOfStringsEnv(envVars []corev1.EnvVar, name string, value []string, sep string) []corev1.EnvVar {
	if len(value) > 0 {
		return append(envVars, corev1.EnvVar{
			Name:  name,
			Value: strings.Join(value, sep),
		})
	}

	return envVars
}

func addDataVolumes(podTemplate corev1.PodTemplateSpec) corev1.PodTemplateSpec {
	podTemplate.Spec.Volumes = append(podTemplate.Spec.Volumes, corev1.Volume{
		Name: dataVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}, corev1.Volume{
		Name: tmpVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})

	containers := make([]corev1.Container, 0, len(podTemplate.Spec.Containers)+1)
	for _, cont := range podTemplate.Spec.Containers {
		cont.VolumeMounts = append(cont.VolumeMounts, []corev1.VolumeMount{
			{
				Name:      dataVolumeName,
				MountPath: dataDir,
			},
			{
				Name:      dataVolumeName,
				MountPath: confDir,
			},
			// NOTE(dtantsur): Ironic relies on a writable /tmp
			{
				Name:      tmpVolumeName,
				MountPath: tmpDir,
			},
		}...)
		containers = append(containers, cont)
	}
	podTemplate.Spec.Containers = containers

	return podTemplate
}

// Merge maps, keys from m1 have priority over ones from m2.
func mergeMaps[M ~map[K]V, K, V comparable](m1 M, m2 M) M {
	if m2 == nil {
		return m1
	}

	result := maps.Clone(m2)
	maps.Copy(result, m1)
	return result
}

// applyContainerOverrides merges override containers into existing containers.
// If an override container has the same name as an existing container, it replaces the existing one.
// Otherwise, the override container is appended to the list.
func applyContainerOverrides(existing []corev1.Container, overrides []corev1.Container) []corev1.Container {
	if len(overrides) == 0 {
		return existing
	}

	result := make([]corev1.Container, 0, len(existing)+len(overrides))

	// Build a map of override containers by name
	overrideMap := make(map[string]corev1.Container, len(overrides))
	for _, container := range overrides {
		overrideMap[container.Name] = container
	}

	// First, add existing containers, replacing with overrides where names match
	for _, container := range existing {
		if override, found := overrideMap[container.Name]; found {
			result = append(result, override)
			delete(overrideMap, container.Name)
		} else {
			result = append(result, container)
		}
	}

	// Then, append any remaining override containers that didn't match existing names
	for _, container := range overrides {
		if _, stillInMap := overrideMap[container.Name]; stillInMap {
			result = append(result, container)
		}
	}

	return result
}

func applyOverridesToPod(overrides *metal3api.Overrides, podTemplate corev1.PodTemplateSpec) corev1.PodTemplateSpec {
	if overrides == nil {
		return podTemplate
	}

	// Always preserve built-in annotations and labels
	podTemplate.Annotations = mergeMaps(podTemplate.Annotations, overrides.Annotations)
	podTemplate.Labels = mergeMaps(podTemplate.Labels, overrides.Labels)

	// Merge containers: replace if name matches, otherwise append
	podTemplate.Spec.Containers = applyContainerOverrides(podTemplate.Spec.Containers, overrides.Containers)

	// Merge init containers: replace if name matches, otherwise append
	podTemplate.Spec.InitContainers = applyContainerOverrides(podTemplate.Spec.InitContainers, overrides.InitContainers)

	return podTemplate
}
