package ironic

import (
	"errors"
	"fmt"
	"maps"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
	"github.com/metal3-io/ironic-standalone-operator/pkg/secretutils"
)

const (
	switchConfigKey     = "switch-configs.conf"
	defaultSwitchDriver = "generic-switch"

	// managedSecretLabel is set on secrets created by the operator to distinguish
	// them from externally-provided secrets. EnsureSwitchConfigSecretDeleted only
	// deletes secrets that carry this label.
	managedSecretLabel      = "ironic.metal3.io/managed"
	managedSecretLabelValue = "true"
)

// NetworkingDeploymentName returns the name of the networking service deployment.
func NetworkingDeploymentName(ironic *metal3api.Ironic) string {
	return ironic.Name + "-networking"
}

// NetworkingServiceName returns the name of the networking service.
func NetworkingServiceName(ironic *metal3api.Ironic) string {
	return ironic.Name + "-networking-service"
}

// networkingLabels returns common labels for networking service resources.
func networkingLabels(ironic *metal3api.Ironic) map[string]string {
	return map[string]string{
		"app":                       "ironic-networking",
		"ironic.metal3.io/instance": ironic.Name,
	}
}

// buildNetworkingContainerEnv builds the environment variables for the networking container.
func buildNetworkingContainerEnv(resources Resources) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{
			Name:  "IRONIC_NETWORKING_ENABLED",
			Value: "true",
		},
		{
			Name: "IRONIC_NETWORKING_JSON_RPC_HOST",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		{
			Name:  "IRONIC_NETWORKING_JSON_RPC_PORT",
			Value: strconv.Itoa(ironicNetworkingRPCPort),
		},
		{
			Name:  "IRONIC_NETWORKING_SWITCH_CONFIGS",
			Value: "/etc/ironic/networking/configs/switch-configs.conf",
		},
	}

	envVars = append(envVars, corev1.EnvVar{
		Name:  "IRONIC_NETWORKING_ENABLED_SWITCH_DRIVERS",
		Value: defaultSwitchDriver,
	})

	envVars = append(envVars,
		corev1.EnvVar{
			Name: "IRONIC_HTPASSWD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: resources.RPCSecret.Name,
					},
					Key: htpasswdKey,
				},
			},
		},
	)

	// Get Ironic IP
	if resources.Ironic.Spec.Networking.IPAddress != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "IRONIC_IP",
			Value: resources.Ironic.Spec.Networking.IPAddress,
		})
	}

	// Add TLS settings if enabled
	if resources.TLSSecret != nil {
		if insecureRPC := resources.Ironic.Spec.TLS.InsecureRPC; insecureRPC != nil && *insecureRPC {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "IRONIC_INSECURE",
				Value: "true",
			})
		}
	}

	return envVars
}

// buildNetworkingContainer builds the networking service container.
func buildNetworkingContainer(cctx ControllerContext, resources Resources, mounts []corev1.VolumeMount) corev1.Container {
	return corev1.Container{
		Name:    "ironic-networking",
		Image:   cctx.VersionInfo.IronicImage,
		Command: []string{"/bin/runironic-networking"},
		Env:     buildNetworkingContainerEnv(resources),
		Ports: []corev1.ContainerPort{
			{
				Name:          ironicNetworkingPortName,
				ContainerPort: ironicNetworkingRPCPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: mounts,
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:              ptr.To(ironicUser),
			RunAsGroup:             ptr.To(ironicGroup),
			ReadOnlyRootFilesystem: ptr.To(true),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
	}
}

func buildNetworkingVolumesAndMounts(resources Resources) (volumes []corev1.Volume, mounts []corev1.VolumeMount) {
	volumes = append(volumes, corev1.Volume{
		Name: ironicAuthVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  resources.RPCSecret.Name,
				DefaultMode: ptr.To(corev1.SecretVolumeSourceDefaultMode),
			},
		},
	})
	mounts = append(mounts, corev1.VolumeMount{
		Name:      ironicAuthVolumeName,
		MountPath: authDir + "/ironic-rpc",
	})

	// Switch config (always required for networking service)
	volumes = append(volumes, corev1.Volume{
		Name: "switch-config",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  switchConfigSecretName(resources.Ironic),
				DefaultMode: ptr.To(corev1.SecretVolumeSourceDefaultMode),
			},
		},
	})
	mounts = append(mounts, corev1.VolumeMount{
		Name:      "switch-config",
		MountPath: "/etc/ironic/networking/configs",
		ReadOnly:  true,
	})

	// Switch credentials (always required for networking service)
	volumes = append(volumes, corev1.Volume{
		Name: "switch-credentials",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  switchCredentialsSecretName(resources.Ironic),
				DefaultMode: ptr.To(corev1.SecretVolumeSourceDefaultMode),
			},
		},
	})
	mounts = append(mounts, corev1.VolumeMount{
		Name:      "switch-credentials",
		MountPath: "/etc/ironic/networking/credentials",
		ReadOnly:  true,
	})

	// TLS certificate (only /certs/ironic, not vmedia — networking service
	// doesn't serve virtual media)
	if resources.TLSSecret != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "cert-ironic",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  resources.TLSSecret.Name,
					DefaultMode: ptr.To(corev1.SecretVolumeSourceDefaultMode),
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "cert-ironic",
			MountPath: certsDir + "/ironic",
			ReadOnly:  true,
		})
	}

	// Trusted CA bundle
	if maybeVolume := volumeForSecretOrConfigMap(trustedCAVolumeName, resources.TrustedCASecret, resources.TrustedCAConfigMap); maybeVolume != nil {
		volumes = append(volumes, *maybeVolume)
		mounts = append(mounts, corev1.VolumeMount{
			Name:      trustedCAVolumeName,
			MountPath: certsDir + "/ca/trusted",
			ReadOnly:  true,
		})
	}

	return volumes, mounts
}

// buildNetworkingDeployment builds the Deployment for the networking service.
func buildNetworkingDeployment(cctx ControllerContext, resources Resources) *appsv1.Deployment {
	labels := networkingLabels(resources.Ironic)

	volumes, mounts := buildNetworkingVolumesAndMounts(resources)

	// Pod annotations include secret versions for auto-restart
	annotations := make(map[string]string)
	if resources.RPCSecret != nil {
		maps.Copy(annotations, secretVersionAnnotations("rpc-secret", resources.RPCSecret))
	}
	if resources.SwitchConfigSecret != nil {
		maps.Copy(annotations, secretVersionAnnotations("switch-config", resources.SwitchConfigSecret))
	}
	if resources.SwitchCredentialsSecret != nil {
		maps.Copy(annotations, secretVersionAnnotations("switch-credentials", resources.SwitchCredentialsSecret))
	}
	// Track TLS secret so networking pod restarts when TLS cert is updated
	if resources.TLSSecret != nil {
		maps.Copy(annotations, secretVersionAnnotations("tls-secret", resources.TLSSecret))
	}

	var tolerations []corev1.Toleration
	if resources.Ironic.Spec.Overrides != nil {
		tolerations = resources.Ironic.Spec.Overrides.Tolerations
	}

	podTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				buildNetworkingContainer(cctx, resources, mounts),
			},
			Volumes:      volumes,
			NodeSelector: resources.Ironic.Spec.NodeSelector,
			Tolerations:  tolerations,
		},
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetworkingDeploymentName(resources.Ironic),
			Namespace: resources.Ironic.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: addDataVolumes(podTemplate),
		},
	}

	return deployment
}

// BuildNetworkingService builds the Service for the networking service.
func BuildNetworkingService(ironic *metal3api.Ironic) *corev1.Service {
	labels := networkingLabels(ironic)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetworkingServiceName(ironic),
			Namespace: ironic.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:     ironicNetworkingPortName,
					Protocol: corev1.ProtocolTCP,
					Port:     ironicNetworkingRPCPort,
				},
			},
		},
	}

	return service
}

func networkingServiceEndpoint(ironic *metal3api.Ironic, domain string) string {
	if domain != "" && domain[0] != '.' {
		domain = "." + domain
	}
	return fmt.Sprintf("%s.%s.svc%s", NetworkingServiceName(ironic), ironic.Namespace, domain)
}

// EnsureIronicNetworking manages the networking service state.
// When the networking service is enabled, it creates/updates the deployment and
// service, waiting for the deployment to become ready.
// When disabled, it cleans up any existing resources.
func EnsureIronicNetworking(cctx ControllerContext, resources Resources) (Status, error) {
	ironic := resources.Ironic

	if !ironic.IsNetworkingServiceEnabled() {
		if err := removeNetworkingResources(cctx, ironic); err != nil {
			return transientError(err)
		}
		return ready()
	}

	if err := ensureNetworkingDeployment(cctx, resources); err != nil {
		return transientError(err)
	}
	if err := ensureNetworkingService(cctx, ironic); err != nil {
		return transientError(err)
	}

	// Wait for the networking deployment to be ready before proceeding
	// so that Ironic doesn't start before its networking service is available.
	deploy := &appsv1.Deployment{}
	err := cctx.Client.Get(cctx.Context, client.ObjectKey{
		Name:      NetworkingDeploymentName(ironic),
		Namespace: ironic.Namespace,
	}, deploy)
	if err != nil {
		return transientError(err)
	}
	if deploy.Status.ReadyReplicas < 1 {
		return inProgress("waiting for networking service deployment to become ready")
	}

	return ready()
}

// RemoveIronicNetworking removes all networking service resources.
func RemoveIronicNetworking(cctx ControllerContext, ironic *metal3api.Ironic) error {
	if err := removeNetworkingResources(cctx, ironic); err != nil {
		return err
	}
	if err := EnsureSwitchConfigSecretDeleted(cctx, ironic); err != nil {
		return err
	}
	return EnsureSwitchCredentialsSecretDeleted(cctx, ironic)
}

// ensureNetworkingDeployment creates or updates the networking service deployment.
func ensureNetworkingDeployment(cctx ControllerContext, resources Resources) error {
	desired := buildNetworkingDeployment(cctx, resources)

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}
	result, err := controllerutil.CreateOrUpdate(cctx.Context, cctx.Client, deploy, func() error {
		if deploy.CreationTimestamp.IsZero() {
			cctx.Logger.Info("creating networking deployment", "Deployment", desired.Name)
		}
		deploy.Labels = desired.Labels
		deploy.Spec = desired.Spec
		return controllerutil.SetControllerReference(resources.Ironic, deploy, cctx.Scheme)
	})
	if err != nil {
		return fmt.Errorf("failed to ensure networking deployment: %w", err)
	}
	if result != controllerutil.OperationResultNone {
		cctx.Logger.Info("networking deployment", "Deployment", deploy.Name, "Status", result)
	}
	return nil
}

// ensureNetworkingService creates or updates the networking service.
func ensureNetworkingService(cctx ControllerContext, ironic *metal3api.Ironic) error {
	desired := BuildNetworkingService(ironic)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}
	result, err := controllerutil.CreateOrUpdate(cctx.Context, cctx.Client, service, func() error {
		if service.CreationTimestamp.IsZero() {
			cctx.Logger.Info("creating networking service", "Service", desired.Name)
		}
		service.Labels = desired.Labels
		service.Spec.Type = desired.Spec.Type
		service.Spec.Selector = desired.Spec.Selector
		service.Spec.Ports = desired.Spec.Ports
		return controllerutil.SetControllerReference(ironic, service, cctx.Scheme)
	})
	if err != nil {
		return fmt.Errorf("failed to ensure networking service: %w", err)
	}
	if result != controllerutil.OperationResultNone {
		cctx.Logger.Info("networking service", "Service", service.Name, "Status", result)
	}
	return nil
}

// removeNetworkingResources deletes the networking deployment and service if they exist.
func removeNetworkingResources(cctx ControllerContext, ironic *metal3api.Ironic) error {
	// Delete deployment
	deployment := &appsv1.Deployment{}
	err := cctx.Client.Get(cctx.Context, client.ObjectKey{
		Name:      NetworkingDeploymentName(ironic),
		Namespace: ironic.Namespace,
	}, deployment)

	if err == nil {
		cctx.Logger.Info("deleting networking deployment", "Deployment", deployment.Name)
		if err = cctx.Client.Delete(cctx.Context, deployment); err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete networking deployment: %w", err)
		}
	} else if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to get networking deployment: %w", err)
	}

	// Delete service
	service := &corev1.Service{}
	err = cctx.Client.Get(cctx.Context, client.ObjectKey{
		Name:      NetworkingServiceName(ironic),
		Namespace: ironic.Namespace,
	}, service)

	if err == nil {
		cctx.Logger.Info("deleting networking service", "Service", service.Name)
		if err = cctx.Client.Delete(cctx.Context, service); err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete networking service: %w", err)
		}
	} else if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to get networking service: %w", err)
	}

	return nil
}

// getExternalSecret fetches a secret using SecretManager, which enforces the
// environment label check and falls back to the API reader for secrets
// not yet synced to the cache.
func getExternalSecret(cctx ControllerContext, apiReader client.Reader, namespace, secretName string) (*corev1.Secret, error) {
	sm := secretutils.NewSecretManager(cctx.Context, cctx.Logger, cctx.Client, apiReader)
	secret, err := sm.ObtainSecret(types.NamespacedName{
		Name:      secretName,
		Namespace: namespace,
	})
	if err != nil {
		wrappedErr := fmt.Errorf("cannot load secret %s/%s: %w", namespace, secretName, err)
		var missingLabelErr *secretutils.MissingLabelError
		if errors.As(err, &missingLabelErr) || k8serrors.IsNotFound(err) {
			cctx.Logger.Info("secret requires user intervention", "secret", secretName, "error", wrappedErr)
		}
		return nil, wrappedErr
	}
	return secret, nil
}

// EnsureNetworkingSwitchSecrets manages switch config and credentials secrets.
// When networking is enabled, it ensures both secrets exist (creating them if needed)
// and returns them for inclusion in Resources.
// When networking is disabled, it deletes any operator-managed secrets.
// The apiReader is used as a fallback for secrets not yet synced to the cache.
func EnsureNetworkingSwitchSecrets(cctx ControllerContext, ironic *metal3api.Ironic, apiReader client.Reader) (configSecret *corev1.Secret, credsSecret *corev1.Secret, retErr error) {
	if !ironic.IsNetworkingServiceEnabled() {
		if err := EnsureSwitchConfigSecretDeleted(cctx, ironic); err != nil {
			return nil, nil, err
		}
		if err := EnsureSwitchCredentialsSecretDeleted(cctx, ironic); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}

	if err := EnsureSwitchConfigSecret(cctx, ironic); err != nil {
		return nil, nil, fmt.Errorf("failed to ensure switch config secret: %w", err)
	}
	configSecret, retErr = getExternalSecret(cctx, apiReader, ironic.Namespace, switchConfigSecretName(ironic))
	if retErr != nil {
		return nil, nil, retErr
	}

	if err := EnsureSwitchCredentialsSecret(cctx, ironic); err != nil {
		return nil, nil, fmt.Errorf("failed to ensure switch credentials secret: %w", err)
	}
	credsSecret, retErr = getExternalSecret(cctx, apiReader, ironic.Namespace, switchCredentialsSecretName(ironic))
	if retErr != nil {
		return nil, nil, retErr
	}

	return configSecret, credsSecret, nil
}

// EnsureSwitchConfigSecret ensures the switch config secret exists.
// If a custom name is specified, the secret is expected to be user-provided
// and is not created by the operator. If using the default name and the secret
// does not exist, it is created with placeholder config so the networking pod
// can start cleanly.
func EnsureSwitchConfigSecret(cctx ControllerContext, ironic *metal3api.Ironic) error {
	secretName := switchConfigSecretName(ironic)
	customName := ironic.Spec.NetworkingService != nil && ironic.Spec.NetworkingService.SwitchConfigSecretName != ""

	// Check if the secret already exists
	existing := &corev1.Secret{}
	err := cctx.Client.Get(cctx.Context, types.NamespacedName{
		Name:      secretName,
		Namespace: ironic.Namespace,
	}, existing)

	if err == nil {
		return nil
	}

	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to get switch config secret: %w", err)
	}

	// Custom-named secrets are user-provided; don't create them automatically
	if customName {
		return nil
	}

	// Create a new secret with placeholder config
	cctx.Logger.Info("creating switch config secret", "secret", secretName)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ironic.Namespace,
			Labels: map[string]string{
				managedSecretLabel:             managedSecretLabelValue,
				metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
			},
		},
		Data: map[string][]byte{
			switchConfigKey: []byte("# This file is managed by the Baremetal Operator\n"),
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(ironic, secret, cctx.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on switch config secret: %w", err)
	}

	if err := cctx.Client.Create(cctx.Context, secret); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Race condition: another reconcile created it first
			return nil
		}
		return fmt.Errorf("failed to create switch config secret: %w", err)
	}

	return nil
}

// EnsureSwitchConfigSecretDeleted deletes the switch config secret only if it
// was created by the operator (identified by the managedSecretLabel). Externally-
// provided secrets are left untouched.
func EnsureSwitchConfigSecretDeleted(cctx ControllerContext, ironic *metal3api.Ironic) error {
	secretName := switchConfigSecretName(ironic)
	secret := &corev1.Secret{}
	if err := cctx.Client.Get(cctx.Context, types.NamespacedName{
		Name:      secretName,
		Namespace: ironic.Namespace,
	}, secret); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get switch config secret: %w", err)
	}

	// Only delete secrets that the operator created
	if secret.Labels[managedSecretLabel] != managedSecretLabelValue {
		cctx.Logger.Info("skipping deletion of externally-managed switch config secret", "secret", secretName)
		return nil
	}

	if err := cctx.Client.Delete(cctx.Context, secret); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete switch config secret: %w", err)
	}

	cctx.Logger.Info("deleted switch config secret", "secret", secretName)
	return nil
}

// EnsureSwitchCredentialsSecret ensures the switch credentials secret exists.
// If a custom name is specified, the secret is expected to be user-provided
// and is not created by the operator. If using the default name and the secret
// does not exist, it is created empty so that BMO can populate it with SSH key files.
func EnsureSwitchCredentialsSecret(cctx ControllerContext, ironic *metal3api.Ironic) error {
	secretName := switchCredentialsSecretName(ironic)
	customName := ironic.Spec.NetworkingService != nil && ironic.Spec.NetworkingService.SwitchCredentialsSecretName != ""

	// Check if the secret already exists
	existing := &corev1.Secret{}
	err := cctx.Client.Get(cctx.Context, types.NamespacedName{
		Name:      secretName,
		Namespace: ironic.Namespace,
	}, existing)

	if err == nil {
		return nil
	}

	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to get switch credentials secret: %w", err)
	}

	// Custom-named secrets are user-provided; don't create them automatically
	if customName {
		return nil
	}

	// Create a new empty secret for BMO to populate
	cctx.Logger.Info("creating switch credentials secret", "secret", secretName)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ironic.Namespace,
			Labels: map[string]string{
				managedSecretLabel:             managedSecretLabelValue,
				metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
			},
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(ironic, secret, cctx.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on switch credentials secret: %w", err)
	}

	if err := cctx.Client.Create(cctx.Context, secret); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Race condition: another reconcile created it first
			return nil
		}
		return fmt.Errorf("failed to create switch credentials secret: %w", err)
	}

	return nil
}

// EnsureSwitchCredentialsSecretDeleted deletes the switch credentials secret only if it
// was created by the operator (identified by the managedSecretLabel). Externally-
// provided secrets are left untouched.
func EnsureSwitchCredentialsSecretDeleted(cctx ControllerContext, ironic *metal3api.Ironic) error {
	secretName := switchCredentialsSecretName(ironic)
	secret := &corev1.Secret{}
	if err := cctx.Client.Get(cctx.Context, types.NamespacedName{
		Name:      secretName,
		Namespace: ironic.Namespace,
	}, secret); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get switch credentials secret: %w", err)
	}

	// Only delete secrets that the operator created
	if secret.Labels[managedSecretLabel] != managedSecretLabelValue {
		cctx.Logger.Info("skipping deletion of externally-managed switch credentials secret", "secret", secretName)
		return nil
	}

	if err := cctx.Client.Delete(cctx.Context, secret); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete switch credentials secret: %w", err)
	}

	cctx.Logger.Info("deleted switch credentials secret", "secret", secretName)
	return nil
}
