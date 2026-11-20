package ironic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
	"github.com/metal3-io/ironic-standalone-operator/pkg/secretutils"
)

func TestNetworkingDeploymentName(t *testing.T) {
	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ironic",
		},
	}

	name := NetworkingDeploymentName(ironic)
	assert.Equal(t, "test-ironic-networking", name)
}

func TestNetworkingServiceName(t *testing.T) {
	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ironic",
		},
	}

	name := NetworkingServiceName(ironic)
	assert.Equal(t, "test-ironic-networking-service", name)
}

func TestNetworkingServiceEndpoint(t *testing.T) {
	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ironic",
			Namespace: "test-namespace",
		},
	}

	endpoint := networkingServiceEndpoint(ironic, "cluster.local")
	assert.Equal(t, "test-ironic-networking-service.test-namespace.svc.cluster.local", endpoint)

	endpoint = networkingServiceEndpoint(ironic, "custom.domain")
	assert.Equal(t, "test-ironic-networking-service.test-namespace.svc.custom.domain", endpoint)

	endpoint = networkingServiceEndpoint(ironic, "")
	assert.Equal(t, "test-ironic-networking-service.test-namespace.svc", endpoint)
}

func TestBuildNetworkingDeployment(t *testing.T) {
	testCases := []struct {
		Scenario                       string
		Ironic                         *metal3api.Ironic
		APISecret                      *corev1.Secret
		TLSSecret                      *corev1.Secret
		SwitchConfigSecret             *corev1.Secret
		SwitchCredentialsSecret        *corev1.Secret
		TrustedCAConfigMap             *corev1.ConfigMap
		TrustedCASecret                *corev1.Secret
		CustomIronicImage              string
		ExpectTLSVolume                bool
		ExpectTrustedCAVolume          bool
		ExpectedSwitchConfigSecretName string
		ExpectedTolerations            []corev1.Toleration
	}{
		{
			Scenario: "basic networking deployment",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test-namespace",
				},
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
				},
			},
			SwitchConfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-ironic-switch-config",
					ResourceVersion: "123",
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-cert",
				},
			},
			ExpectTLSVolume:       false,
			ExpectTrustedCAVolume: false,
		},
		{
			Scenario: "networking deployment with TLS",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test-namespace",
				},
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
					TLS: metal3api.TLS{
						CertificateName: "test-tls-cert",
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-cert",
				},
			},
			TLSSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-tls-cert",
				},
			},
			SwitchConfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-ironic-switch-config",
					ResourceVersion: "123",
				},
			},
			ExpectTLSVolume:       true,
			ExpectTrustedCAVolume: false,
		},
		{
			Scenario: "networking deployment with TLS and trusted CA",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test-namespace",
				},
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
					TLS: metal3api.TLS{
						CertificateName: "test-tls-cert",
						TrustedCAName:   "test-trusted-ca",
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-cert",
				},
			},
			TLSSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-tls-cert",
				},
			},
			TrustedCAConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-trusted-ca",
				},
			},
			SwitchConfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-ironic-switch-config",
					ResourceVersion: "123",
				},
			},
			ExpectTLSVolume:       true,
			ExpectTrustedCAVolume: true,
		},
		{
			Scenario: "networking deployment with trusted CA from secret",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test-namespace",
				},
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
					TLS: metal3api.TLS{
						CertificateName: "test-tls-cert",
						TrustedCA: &metal3api.ResourceReferenceWithKey{
							ResourceReference: metal3api.ResourceReference{
								Name: "test-trusted-ca-secret",
								Kind: metal3api.ResourceKindSecret,
							},
						},
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-cert",
				},
			},
			TLSSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-tls-cert",
				},
			},
			TrustedCASecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-trusted-ca-secret",
				},
			},
			SwitchConfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-ironic-switch-config",
					ResourceVersion: "123",
				},
			},
			ExpectTLSVolume:       true,
			ExpectTrustedCAVolume: true,
		},
		{
			Scenario: "networking deployment with switch credentials secret",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test-namespace",
				},
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled:                     true,
						SwitchCredentialsSecretName: "my-switch-creds",
					},
				},
			},
			SwitchConfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-ironic-switch-config",
					ResourceVersion: "123",
				},
			},
			SwitchCredentialsSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "my-switch-creds",
					ResourceVersion: "456",
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-cert",
				},
			},
		},
		{
			Scenario: "networking deployment with custom ironic image",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test-namespace",
				},
				Spec: metal3api.IronicSpec{
					Images: metal3api.Images{
						Ironic: "myorg/myironic:custom",
					},
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
				},
			},
			SwitchConfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-ironic-switch-config",
					ResourceVersion: "123",
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-cert",
				},
			},
			CustomIronicImage: "myorg/myironic:custom",
		},
		{
			Scenario: "networking deployment with custom switchConfigSecretName",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test-namespace",
				},
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled:                true,
						SwitchConfigSecretName: "custom-switch-config",
					},
				},
			},
			SwitchConfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "custom-switch-config",
					ResourceVersion: "789",
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-cert",
				},
			},
			ExpectedSwitchConfigSecretName: "custom-switch-config",
		},
		{
			Scenario: "networking deployment with tolerations override",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test-namespace",
				},
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
					Overrides: &metal3api.Overrides{
						Tolerations: []corev1.Toleration{
							{
								Key:      "node-role.kubernetes.io/control-plane",
								Operator: corev1.TolerationOpExists,
								Effect:   corev1.TaintEffectNoSchedule,
							},
						},
					},
				},
			},
			SwitchConfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-ironic-switch-config",
					ResourceVersion: "123",
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-cert",
				},
			},
			ExpectedTolerations: []corev1.Toleration{
				{
					Key:      "node-role.kubernetes.io/control-plane",
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoSchedule,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			ironicImage := "quay.io/metal3-io/ironic:latest"
			if tc.CustomIronicImage != "" {
				ironicImage = tc.CustomIronicImage
			}
			cctx := ControllerContext{
				VersionInfo: VersionInfo{
					IronicImage: ironicImage,
				},
			}

			resources := Resources{
				Ironic:    tc.Ironic,
				APISecret: tc.APISecret,
				RPCSecret: &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-ironic-rpc-credentials",
					},
				},
				TLSSecret:               tc.TLSSecret,
				SwitchConfigSecret:      tc.SwitchConfigSecret,
				SwitchCredentialsSecret: tc.SwitchCredentialsSecret,
				TrustedCAConfigMap:      tc.TrustedCAConfigMap,
				TrustedCASecret:         tc.TrustedCASecret,
			}

			deployment := buildNetworkingDeployment(cctx, resources)

			// Verify deployment metadata
			assert.Equal(t, "test-ironic-networking", deployment.Name)
			assert.Equal(t, "test-namespace", deployment.Namespace)
			assert.Equal(t, "ironic-networking", deployment.Labels["app"])
			assert.Equal(t, "test-ironic", deployment.Labels["ironic.metal3.io/instance"])

			// Verify replica count
			require.NotNil(t, deployment.Spec.Replicas)
			assert.Equal(t, int32(1), *deployment.Spec.Replicas)

			// Verify pod template labels
			assert.Equal(t, deployment.Labels, deployment.Spec.Template.Labels)

			// Verify pod annotations include switch config secret version
			assert.Contains(t, deployment.Spec.Template.Annotations, "ironic.metal3.io/switch-config-version")

			// Verify pod annotations include the RPC secret version
			assert.Contains(t, deployment.Spec.Template.Annotations, "ironic.metal3.io/rpc-secret-version")

			// Verify TLS secret version annotation when TLS is enabled
			if tc.ExpectTLSVolume {
				assert.Contains(t, deployment.Spec.Template.Annotations, "ironic.metal3.io/tls-secret-version")
			} else {
				assert.NotContains(t, deployment.Spec.Template.Annotations, "ironic.metal3.io/tls-secret-version")
			}

			// Verify selector matches labels
			assert.Equal(t, deployment.Labels, deployment.Spec.Selector.MatchLabels)

			// Verify container
			require.Len(t, deployment.Spec.Template.Spec.Containers, 1)
			container := deployment.Spec.Template.Spec.Containers[0]
			assert.Equal(t, "ironic-networking", container.Name)
			if tc.CustomIronicImage != "" {
				assert.Equal(t, tc.CustomIronicImage, container.Image)
			} else {
				assert.Equal(t, "quay.io/metal3-io/ironic:latest", container.Image)
			}
			assert.Equal(t, []string{"/bin/runironic-networking"}, container.Command)

			// Verify container ports
			require.Len(t, container.Ports, 1)
			assert.Equal(t, "networking-rpc", container.Ports[0].Name)
			assert.Equal(t, int32(6190), container.Ports[0].ContainerPort)

			// Verify container environment variables
			envMap := make(map[string]string)
			envFromMap := make(map[string]corev1.EnvVar)
			for _, env := range container.Env {
				envMap[env.Name] = env.Value
				envFromMap[env.Name] = env
			}
			assert.Equal(t, "true", envMap["IRONIC_NETWORKING_ENABLED"])
			rpcHostEnv := envFromMap["IRONIC_NETWORKING_JSON_RPC_HOST"]
			require.NotNil(t, rpcHostEnv.ValueFrom)
			require.NotNil(t, rpcHostEnv.ValueFrom.FieldRef)
			assert.Equal(t, "status.podIP", rpcHostEnv.ValueFrom.FieldRef.FieldPath)
			assert.Equal(t, "6190", envMap["IRONIC_NETWORKING_JSON_RPC_PORT"])
			assert.Equal(t, "/etc/ironic/networking/configs/switch-configs.conf", envMap["IRONIC_NETWORKING_SWITCH_CONFIGS"])
			assert.Equal(t, "generic-switch", envMap["IRONIC_NETWORKING_ENABLED_SWITCH_DRIVERS"])

			// Verify security context
			require.NotNil(t, container.SecurityContext)
			assert.Equal(t, int64(997), *container.SecurityContext.RunAsUser)
			assert.Equal(t, int64(994), *container.SecurityContext.RunAsGroup)
			require.NotNil(t, container.SecurityContext.Capabilities)
			assert.Contains(t, container.SecurityContext.Capabilities.Drop, corev1.Capability("ALL"))

			// Verify hostNetwork is false (no privileged networking)
			assert.False(t, deployment.Spec.Template.Spec.HostNetwork)

			// Verify tolerations override propagates to the networking pod
			assert.Equal(t, tc.ExpectedTolerations, deployment.Spec.Template.Spec.Tolerations)

			// Verify volumes
			volumeMap := make(map[string]corev1.Volume)
			for _, vol := range deployment.Spec.Template.Spec.Volumes {
				volumeMap[vol.Name] = vol
			}

			// Networking pod should NOT have ironic-shared or database volumes
			assert.NotContains(t, volumeMap, "ironic-shared")

			// ironic-auth is required for JSON-RPC authentication and uses
			// the dedicated RPC credentials secret
			assert.Contains(t, volumeMap, ironicAuthVolumeName)
			assert.Equal(t, "test-ironic-rpc-credentials", volumeMap[ironicAuthVolumeName].Secret.SecretName)

			// Always present: switch-config
			assert.Contains(t, volumeMap, "switch-config")
			if tc.ExpectedSwitchConfigSecretName != "" {
				assert.Equal(t, tc.ExpectedSwitchConfigSecretName, volumeMap["switch-config"].Secret.SecretName)
			} else {
				assert.Equal(t, "test-ironic-switch-config", volumeMap["switch-config"].Secret.SecretName)
			}

			// Switch credentials volume (always present for networking service)
			assert.Contains(t, volumeMap, "switch-credentials")
			expectedCredSecretName := switchCredentialsSecretName(tc.Ironic)
			assert.Equal(t, expectedCredSecretName, volumeMap["switch-credentials"].Secret.SecretName)
			if tc.SwitchCredentialsSecret != nil {
				assert.Contains(t, deployment.Spec.Template.Annotations, "ironic.metal3.io/switch-credentials-version")
			}

			if tc.ExpectTLSVolume {
				assert.Contains(t, volumeMap, "cert-ironic")
				assert.Equal(t, "test-tls-cert", volumeMap["cert-ironic"].Secret.SecretName)
			}

			if tc.ExpectTrustedCAVolume {
				assert.Contains(t, volumeMap, "trusted-ca")
				if tc.TrustedCAConfigMap != nil {
					assert.Equal(t, tc.TrustedCAConfigMap.Name, volumeMap["trusted-ca"].ConfigMap.Name)
				} else if tc.TrustedCASecret != nil {
					assert.Equal(t, tc.TrustedCASecret.Name, volumeMap["trusted-ca"].Secret.SecretName)
				}
			} else {
				assert.NotContains(t, volumeMap, "trusted-ca")
			}

			// Verify volume mounts
			findMount := func(mounts []corev1.VolumeMount, name, path string) *corev1.VolumeMount {
				for _, mount := range mounts {
					if mount.Name == name && mount.MountPath == path {
						return &mount
					}
				}
				return nil
			}

			// Networking pod should NOT have ironic-shared or vmedia mounts
			assert.Nil(t, findMount(container.VolumeMounts, "ironic-shared", "/shared"),
				"ironic-shared should not be mounted in networking pod")
			assert.Nil(t, findMount(container.VolumeMounts, "cert-ironic", "/certs/vmedia"),
				"vmedia cert should not be mounted in networking pod")

			// Always present: switch-config
			switchConfigMount := findMount(container.VolumeMounts, "switch-config", "/etc/ironic/networking/configs")
			require.NotNil(t, switchConfigMount, "Expected switch-config volume to be mounted at /etc/ironic/networking")
			assert.True(t, switchConfigMount.ReadOnly)

			// Switch credentials mount (always present for networking service)
			credsMount := findMount(container.VolumeMounts, "switch-credentials", "/etc/ironic/networking/credentials")
			require.NotNil(t, credsMount, "Expected switch-credentials volume to be mounted at /etc/ironic/networking/credentials")
			assert.True(t, credsMount.ReadOnly)

			if tc.ExpectTLSVolume {
				// Only /certs/ironic, NOT /certs/vmedia
				ironicCertMount := findMount(container.VolumeMounts, "cert-ironic", "/certs/ironic")
				require.NotNil(t, ironicCertMount, "Expected cert-ironic volume to be mounted at /certs/ironic")
				assert.True(t, ironicCertMount.ReadOnly)
			}

			if tc.ExpectTrustedCAVolume {
				trustedCAMount := findMount(container.VolumeMounts, "trusted-ca", "/certs/ca/trusted")
				require.NotNil(t, trustedCAMount, "Expected trusted-ca volume to be mounted at /certs/ca/trusted")
				assert.True(t, trustedCAMount.ReadOnly)
			}
		})
	}
}

func TestBuildNetworkingService(t *testing.T) {
	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ironic",
			Namespace: "test-namespace",
		},
		Spec: metal3api.IronicSpec{
			NetworkingService: &metal3api.NetworkingService{
				Enabled: true,
			},
		},
	}

	service := BuildNetworkingService(ironic)

	// Verify service metadata
	assert.Equal(t, "test-ironic-networking-service", service.Name)
	assert.Equal(t, ironic.Namespace, service.Namespace)
	assert.Equal(t, "ironic-networking", service.Labels["app"])
	assert.Equal(t, ironic.Name, service.Labels["ironic.metal3.io/instance"])

	// Verify service type
	assert.Equal(t, corev1.ServiceTypeClusterIP, service.Spec.Type)

	// Verify selector matches deployment labels
	expectedLabels := map[string]string{
		"app":                       "ironic-networking",
		"ironic.metal3.io/instance": ironic.Name,
	}
	assert.Equal(t, expectedLabels, service.Spec.Selector)

	// Verify ports
	require.Len(t, service.Spec.Ports, 1)
	assert.Equal(t, "networking-rpc", service.Spec.Ports[0].Name)
	assert.Equal(t, corev1.ProtocolTCP, service.Spec.Ports[0].Protocol)
	assert.Equal(t, int32(6190), service.Spec.Ports[0].Port)
}

func TestBuildNetworkingContainerEnv(t *testing.T) {
	testCases := []struct {
		Scenario              string
		Ironic                *metal3api.Ironic
		APISecret             *corev1.Secret
		TLSSecret             *corev1.Secret
		ExpectedEnvs          map[string]string
		NotExpectedEnvs       []string
		ExpectHTPasswdFromRef bool
	}{
		{
			Scenario: "basic configuration",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-secret",
				},
			},
			ExpectedEnvs: map[string]string{
				"IRONIC_NETWORKING_ENABLED":                "true",
				"IRONIC_NETWORKING_JSON_RPC_PORT":          "6190",
				"IRONIC_NETWORKING_SWITCH_CONFIGS":         "/etc/ironic/networking/configs/switch-configs.conf",
				"IRONIC_NETWORKING_ENABLED_SWITCH_DRIVERS": "generic-switch",
			},
			NotExpectedEnvs: []string{"IRONIC_TLS_SETUP", "IRONIC_IP"},
		},
		{
			Scenario: "with IP address",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					Networking: metal3api.Networking{
						IPAddress: "192.168.1.100",
					},
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-secret",
				},
			},
			ExpectedEnvs: map[string]string{
				"IRONIC_IP": "192.168.1.100",
			},
		},
		{
			Scenario: "with TLS",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
					TLS: metal3api.TLS{
						CertificateName: "test-cert",
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-secret",
				},
			},
			TLSSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cert",
				},
			},
		},
		{
			Scenario: "InsecureRPC true with TLS",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
					TLS: metal3api.TLS{
						CertificateName: "test-cert",
						InsecureRPC:     ptr.To(true),
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-secret",
				},
			},
			TLSSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cert",
				},
			},
			ExpectedEnvs: map[string]string{
				"IRONIC_INSECURE": "true",
			},
		},
		{
			Scenario: "InsecureRPC false with TLS",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
					TLS: metal3api.TLS{
						CertificateName: "test-cert",
						InsecureRPC:     ptr.To(false),
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-secret",
				},
			},
			TLSSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cert",
				},
			},
			NotExpectedEnvs: []string{"IRONIC_INSECURE"},
		},
		{
			Scenario: "InsecureRPC true without TLS secret",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
					TLS: metal3api.TLS{
						InsecureRPC: ptr.To(true),
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-secret",
				},
			},
			NotExpectedEnvs: []string{"IRONIC_INSECURE"},
		},
		{
			Scenario: "RPC secret sets IRONIC_HTPASSWD from secret ref",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
				},
			},
			APISecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-api-secret",
				},
			},
			ExpectHTPasswdFromRef: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			rpcSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rpc-secret",
				},
			}
			resources := Resources{
				Ironic:    tc.Ironic,
				APISecret: tc.APISecret,
				RPCSecret: rpcSecret,
				TLSSecret: tc.TLSSecret,
			}

			envVars := buildNetworkingContainerEnv(resources)

			// Build map for easier checking
			envMap := make(map[string]string)
			envVarList := make(map[string]corev1.EnvVar)
			for _, env := range envVars {
				envMap[env.Name] = env.Value
				envVarList[env.Name] = env
			}

			// Check expected env vars
			for key, expectedValue := range tc.ExpectedEnvs {
				assert.Contains(t, envMap, key, "Expected env var %s to be present", key)
				assert.Equal(t, expectedValue, envMap[key], "Expected env var %s to have value %s", key, expectedValue)
			}

			// Check that not expected env vars are not present
			for _, key := range tc.NotExpectedEnvs {
				assert.NotContains(t, envMap, key, "Did not expect env var %s to be present", key)
			}

			// Verify JSON RPC host uses downward API pod IP
			rpcHostEnv, ok := envVarList["IRONIC_NETWORKING_JSON_RPC_HOST"]
			assert.True(t, ok, "Expected IRONIC_NETWORKING_JSON_RPC_HOST to be present")
			if ok {
				require.NotNil(t, rpcHostEnv.ValueFrom)
				require.NotNil(t, rpcHostEnv.ValueFrom.FieldRef)
				assert.Equal(t, "status.podIP", rpcHostEnv.ValueFrom.FieldRef.FieldPath)
			}

			// Check IRONIC_HTPASSWD is set from a secret ref to the RPC secret
			if tc.ExpectHTPasswdFromRef {
				htpasswdEnv, ok := envVarList["IRONIC_HTPASSWD"]
				require.True(t, ok, "Expected IRONIC_HTPASSWD env var to be present")
				require.NotNil(t, htpasswdEnv.ValueFrom, "Expected IRONIC_HTPASSWD to have ValueFrom")
				require.NotNil(t, htpasswdEnv.ValueFrom.SecretKeyRef, "Expected IRONIC_HTPASSWD to have SecretKeyRef")
				assert.Equal(t, rpcSecret.Name, htpasswdEnv.ValueFrom.SecretKeyRef.Name)
				assert.Equal(t, "htpasswd", htpasswdEnv.ValueFrom.SecretKeyRef.Key)
			}
		})
	}
}

// ensureSecretTestCase defines a test case for EnsureSwitchConfigSecret / EnsureSwitchCredentialsSecret.
type ensureSecretTestCase struct {
	Scenario       string
	Ironic         *metal3api.Ironic
	ExistingSecret *corev1.Secret
	ExpectCreate   bool
}

// runEnsureSecretTest runs a single ensure-secret test case using the provided
// ensure function, secret name function, and validation callbacks.
func runEnsureSecretTest(
	t *testing.T,
	tc ensureSecretTestCase,
	scheme *runtime.Scheme,
	ensureFn func(ControllerContext, *metal3api.Ironic) error,
	secretNameFn func(*metal3api.Ironic) string,
	validateCreated func(*testing.T, *corev1.Secret, *metal3api.Ironic),
	validateExisting func(*testing.T, *corev1.Secret),
) {
	t.Helper()
	var objects []client.Object
	if tc.ExistingSecret != nil {
		objects = append(objects, tc.ExistingSecret)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	cctx := ControllerContext{
		Context: t.Context(),
		Client:  c,
		Scheme:  scheme,
	}

	err := ensureFn(cctx, tc.Ironic)
	require.NoError(t, err)

	secretName := secretNameFn(tc.Ironic)
	secret := &corev1.Secret{}
	err = c.Get(t.Context(), client.ObjectKey{
		Namespace: tc.Ironic.Namespace,
		Name:      secretName,
	}, secret)
	require.NoError(t, err)

	if tc.ExpectCreate {
		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, tc.Ironic.Name, secret.OwnerReferences[0].Name)
		assert.Equal(t, "true", secret.Labels[managedSecretLabel])
		assert.Equal(t, metal3api.LabelEnvironmentValue, secret.Labels[metal3api.LabelEnvironmentName])
		validateCreated(t, secret, tc.Ironic)
	} else {
		validateExisting(t, secret)
	}
}

// deleteSecretTestCase defines a test case for EnsureSwitchConfigSecretDeleted / EnsureSwitchCredentialsSecretDeleted.
type deleteSecretTestCase struct {
	Scenario       string
	Ironic         *metal3api.Ironic
	ExistingSecret *corev1.Secret
	ExpectDeleted  bool
}

// runDeleteSecretTest runs a single delete-secret test case using the provided
// delete function and default secret suffix.
func runDeleteSecretTest(
	t *testing.T,
	tc deleteSecretTestCase,
	scheme *runtime.Scheme,
	deleteFn func(ControllerContext, *metal3api.Ironic) error,
	secretSuffix string,
	validateRetained func(*testing.T, *corev1.Secret),
) {
	t.Helper()
	var objects []client.Object
	if tc.ExistingSecret != nil {
		objects = append(objects, tc.ExistingSecret)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	cctx := ControllerContext{
		Context: t.Context(),
		Client:  c,
		Scheme:  scheme,
	}

	err := deleteFn(cctx, tc.Ironic)
	require.NoError(t, err)

	secretName := tc.Ironic.Name + secretSuffix
	secret := &corev1.Secret{}
	getErr := c.Get(t.Context(), client.ObjectKey{
		Namespace: tc.Ironic.Namespace,
		Name:      secretName,
	}, secret)

	switch {
	case tc.ExistingSecret == nil:
		assert.True(t, k8serrors.IsNotFound(getErr))
	case tc.ExpectDeleted:
		assert.True(t, k8serrors.IsNotFound(getErr))
	default:
		require.NoError(t, getErr)
		validateRetained(t, secret)
	}
}

func TestEnsureSwitchConfigSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, metal3api.AddToScheme(scheme))

	testCases := []ensureSecretTestCase{
		{
			Scenario: "creates secret when none exists",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
				Spec:       metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{Enabled: true}},
			},
			ExpectCreate: true,
		},
		{
			Scenario: "does not overwrite existing secret data",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns", UID: "abc-123"},
				Spec:       metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{Enabled: true}},
			},
			ExistingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic-switch-config", Namespace: "test-ns"},
				Data:       map[string][]byte{switchConfigKey: []byte("existing config data")},
			},
			ExpectCreate: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			runEnsureSecretTest(t, tc, scheme, EnsureSwitchConfigSecret, switchConfigSecretName,
				func(t *testing.T, secret *corev1.Secret, _ *metal3api.Ironic) {
					t.Helper()
					assert.Contains(t, secret.Data, switchConfigKey)
					assert.Equal(t, "# This file is managed by the Baremetal Operator\n", string(secret.Data[switchConfigKey]))
				},
				func(t *testing.T, secret *corev1.Secret) {
					t.Helper()
					assert.Equal(t, "existing config data", string(secret.Data[switchConfigKey]))
				},
			)
		})
	}

	t.Run("does not create secret with custom switchConfigSecretName", func(t *testing.T) {
		ironicObj := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
			Spec: metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{
				Enabled: true, SwitchConfigSecretName: "custom-switch-config",
			}},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		cctx := ControllerContext{Context: t.Context(), Client: c, Scheme: scheme}

		err := EnsureSwitchConfigSecret(cctx, ironicObj)
		require.NoError(t, err)

		// Secret should not have been created
		secret := &corev1.Secret{}
		getErr := c.Get(t.Context(), client.ObjectKey{Namespace: "test-ns", Name: "custom-switch-config"}, secret)
		assert.True(t, k8serrors.IsNotFound(getErr), "expected custom-named secret to not be created")
	})
}

func TestEnsureSwitchConfigSecretDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, metal3api.AddToScheme(scheme))

	testCases := []deleteSecretTestCase{
		{
			Scenario: "delete operator-managed secret",
			Ironic:   &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"}},
			ExistingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic-switch-config", Namespace: "test-ns",
					Labels: map[string]string{managedSecretLabel: "true"}},
				Data: map[string][]byte{switchConfigKey: []byte("config data")},
			},
			ExpectDeleted: true,
		},
		{
			Scenario: "skip externally-managed secret",
			Ironic:   &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"}},
			ExistingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic-switch-config", Namespace: "test-ns"},
				Data:       map[string][]byte{switchConfigKey: []byte("external config")},
			},
			ExpectDeleted: false,
		},
		{
			Scenario:      "no-op when secret doesn't exist",
			Ironic:        &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"}},
			ExpectDeleted: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			runDeleteSecretTest(t, tc, scheme, EnsureSwitchConfigSecretDeleted, "-switch-config",
				func(t *testing.T, secret *corev1.Secret) {
					t.Helper()
					assert.Equal(t, "external config", string(secret.Data[switchConfigKey]))
				},
			)
		})
	}
}

func TestEnsureSwitchCredentialsSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, metal3api.AddToScheme(scheme))

	testCases := []ensureSecretTestCase{
		{
			Scenario: "creates empty secret when none exists",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
				Spec:       metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{Enabled: true}},
			},
			ExpectCreate: true,
		},
		{
			Scenario: "does not overwrite existing secret data",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns", UID: "abc-123"},
				Spec:       metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{Enabled: true}},
			},
			ExistingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic-switch-credentials", Namespace: "test-ns"},
				Data:       map[string][]byte{"00-11-22-33-44-55.key": []byte("existing key data")},
			},
			ExpectCreate: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			runEnsureSecretTest(t, tc, scheme, EnsureSwitchCredentialsSecret, switchCredentialsSecretName,
				func(t *testing.T, secret *corev1.Secret, _ *metal3api.Ironic) {
					t.Helper()
					assert.Empty(t, secret.Data)
				},
				func(t *testing.T, secret *corev1.Secret) {
					t.Helper()
					assert.Equal(t, "existing key data", string(secret.Data["00-11-22-33-44-55.key"]))
				},
			)
		})
	}

	t.Run("does not create secret with custom switchCredentialsSecretName", func(t *testing.T) {
		ironicObj := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
			Spec: metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{
				Enabled: true, SwitchCredentialsSecretName: "custom-switch-creds",
			}},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		cctx := ControllerContext{Context: t.Context(), Client: c, Scheme: scheme}

		err := EnsureSwitchCredentialsSecret(cctx, ironicObj)
		require.NoError(t, err)

		// Secret should not have been created
		secret := &corev1.Secret{}
		getErr := c.Get(t.Context(), client.ObjectKey{Namespace: "test-ns", Name: "custom-switch-creds"}, secret)
		assert.True(t, k8serrors.IsNotFound(getErr), "expected custom-named secret to not be created")
	})
}

func TestEnsureSwitchCredentialsSecretDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, metal3api.AddToScheme(scheme))

	testCases := []deleteSecretTestCase{
		{
			Scenario: "delete operator-managed secret",
			Ironic:   &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"}},
			ExistingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic-switch-credentials", Namespace: "test-ns",
					Labels: map[string]string{managedSecretLabel: "true"}},
				Data: map[string][]byte{"00-11-22-33-44-55.key": []byte("key data")},
			},
			ExpectDeleted: true,
		},
		{
			Scenario: "skip externally-managed secret",
			Ironic:   &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"}},
			ExistingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic-switch-credentials", Namespace: "test-ns"},
				Data:       map[string][]byte{"00-11-22-33-44-55.key": []byte("external key")},
			},
			ExpectDeleted: false,
		},
		{
			Scenario:      "no-op when secret doesn't exist",
			Ironic:        &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"}},
			ExpectDeleted: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			runDeleteSecretTest(t, tc, scheme, EnsureSwitchCredentialsSecretDeleted, "-switch-credentials",
				func(t *testing.T, secret *corev1.Secret) {
					t.Helper()
					assert.Equal(t, "external key", string(secret.Data["00-11-22-33-44-55.key"]))
				},
			)
		})
	}
}

// secretNameTestCase defines a test case for switchConfigSecretName / switchCredentialsSecretName.
type secretNameTestCase struct {
	Scenario     string
	Ironic       *metal3api.Ironic
	ExpectedName string
}

func TestSwitchConfigSecretName(t *testing.T) {
	testCases := []secretNameTestCase{
		{
			Scenario:     "default name",
			Ironic:       &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "my-ironic"}},
			ExpectedName: "my-ironic-switch-config",
		},
		{
			Scenario: "custom name via spec",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{Name: "my-ironic"},
				Spec: metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{
					SwitchConfigSecretName: "custom-config",
				}},
			},
			ExpectedName: "custom-config",
		},
		{
			Scenario:     "nil networking service",
			Ironic:       &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "my-ironic"}, Spec: metal3api.IronicSpec{NetworkingService: nil}},
			ExpectedName: "my-ironic-switch-config",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			assert.Equal(t, tc.ExpectedName, switchConfigSecretName(tc.Ironic))
		})
	}
}

func TestSwitchCredentialsSecretName(t *testing.T) {
	testCases := []secretNameTestCase{
		{
			Scenario:     "default name",
			Ironic:       &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "my-ironic"}},
			ExpectedName: "my-ironic-switch-credentials",
		},
		{
			Scenario: "custom name via spec",
			Ironic: &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{Name: "my-ironic"},
				Spec: metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{
					SwitchCredentialsSecretName: "custom-creds",
				}},
			},
			ExpectedName: "custom-creds",
		},
		{
			Scenario:     "nil networking service",
			Ironic:       &metal3api.Ironic{ObjectMeta: metav1.ObjectMeta{Name: "my-ironic"}, Spec: metal3api.IronicSpec{NetworkingService: nil}},
			ExpectedName: "my-ironic-switch-credentials",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			assert.Equal(t, tc.ExpectedName, switchCredentialsSecretName(tc.Ironic))
		})
	}
}

func TestEnsureNetworkingSwitchSecrets(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, metal3api.AddToScheme(scheme))

	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
		Spec:       metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{Enabled: true}},
	}

	t.Run("creates both secrets when none exist", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		cctx := ControllerContext{
			Context: t.Context(),
			Client:  c,
			Scheme:  scheme,
		}

		configSecret, credsSecret, err := EnsureNetworkingSwitchSecrets(cctx, ironic, c)
		require.NoError(t, err)

		require.NotNil(t, configSecret)
		assert.Equal(t, "test-ironic-switch-config", configSecret.Name)
		assert.Equal(t, managedSecretLabelValue, configSecret.Labels[managedSecretLabel])
		assert.Equal(t, metal3api.LabelEnvironmentValue, configSecret.Labels[metal3api.LabelEnvironmentName])
		assert.Contains(t, configSecret.Data, switchConfigKey)

		require.NotNil(t, credsSecret)
		assert.Equal(t, "test-ironic-switch-credentials", credsSecret.Name)
		assert.Equal(t, managedSecretLabelValue, credsSecret.Labels[managedSecretLabel])
		assert.Equal(t, metal3api.LabelEnvironmentValue, credsSecret.Labels[metal3api.LabelEnvironmentName])
	})

	t.Run("returns user-provided secrets with environment label", func(t *testing.T) {
		userConfig := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-switch-config",
				Namespace: "test-ns",
				Labels: map[string]string{
					metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
				},
			},
			Data: map[string][]byte{switchConfigKey: []byte("user config data")},
		}
		userCreds := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-switch-credentials",
				Namespace: "test-ns",
				Labels: map[string]string{
					metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
				},
			},
			Data: map[string][]byte{"key.pem": []byte("user key data")},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(userConfig, userCreds).Build()
		cctx := ControllerContext{
			Context: t.Context(),
			Client:  c,
			Scheme:  scheme,
		}

		configSecret, credsSecret, err := EnsureNetworkingSwitchSecrets(cctx, ironic, c)
		require.NoError(t, err)

		// User data should be preserved
		assert.Equal(t, "user config data", string(configSecret.Data[switchConfigKey]))
		assert.Equal(t, "user key data", string(credsSecret.Data["key.pem"]))

		// Should NOT have the managed label
		assert.Empty(t, configSecret.Labels[managedSecretLabel])
		assert.Empty(t, credsSecret.Labels[managedSecretLabel])
	})

	t.Run("rejects user-provided config secret without environment label", func(t *testing.T) {
		userConfig := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-switch-config",
				Namespace: "test-ns",
			},
			Data: map[string][]byte{switchConfigKey: []byte("user config")},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(userConfig).Build()
		cctx := ControllerContext{
			Context: t.Context(),
			Client:  c,
			Scheme:  scheme,
		}

		_, _, err := EnsureNetworkingSwitchSecrets(cctx, ironic, c)
		require.Error(t, err)

		var missingLabelErr *secretutils.MissingLabelError
		assert.ErrorAs(t, err, &missingLabelErr)
	})

	t.Run("rejects user-provided credentials secret without environment label", func(t *testing.T) {
		// Config secret with label (passes), credentials secret without (fails)
		userConfig := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-switch-config",
				Namespace: "test-ns",
				Labels: map[string]string{
					metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
				},
			},
			Data: map[string][]byte{switchConfigKey: []byte("config")},
		}
		userCreds := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-switch-credentials",
				Namespace: "test-ns",
			},
			Data: map[string][]byte{"key.pem": []byte("key")},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(userConfig, userCreds).Build()
		cctx := ControllerContext{
			Context: t.Context(),
			Client:  c,
			Scheme:  scheme,
		}

		_, _, err := EnsureNetworkingSwitchSecrets(cctx, ironic, c)
		require.Error(t, err)

		var missingLabelErr *secretutils.MissingLabelError
		assert.ErrorAs(t, err, &missingLabelErr)
	})

	t.Run("deletes operator-managed secrets when networking disabled", func(t *testing.T) {
		disabledIronic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
			Spec:       metal3api.IronicSpec{NetworkingService: &metal3api.NetworkingService{Enabled: false}},
		}
		existingConfig := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-switch-config",
				Namespace: "test-ns",
				Labels:    map[string]string{managedSecretLabel: managedSecretLabelValue},
			},
		}
		existingCreds := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-switch-credentials",
				Namespace: "test-ns",
				Labels:    map[string]string{managedSecretLabel: managedSecretLabelValue},
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingConfig, existingCreds).Build()
		cctx := ControllerContext{
			Context: t.Context(),
			Client:  c,
			Scheme:  scheme,
		}

		configSecret, credsSecret, err := EnsureNetworkingSwitchSecrets(cctx, disabledIronic, c)
		require.NoError(t, err)
		assert.Nil(t, configSecret)
		assert.Nil(t, credsSecret)

		// Verify both secrets are deleted
		var s corev1.Secret
		assert.True(t, k8serrors.IsNotFound(c.Get(t.Context(), client.ObjectKey{
			Namespace: "test-ns", Name: "test-ironic-switch-config",
		}, &s)))
		assert.True(t, k8serrors.IsNotFound(c.Get(t.Context(), client.ObjectKey{
			Namespace: "test-ns", Name: "test-ironic-switch-credentials",
		}, &s)))
	})
}
