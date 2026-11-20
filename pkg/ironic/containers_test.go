package ironic

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
)

const (
	ironicContainerName            = "ironic"
	httpdContainerName             = "httpd"
	ramdiskDownloaderContainerName = "ramdisk-downloader"
)

func TestExpectedContainers(t *testing.T) {
	testCases := []struct {
		Scenario string

		Ironic metal3api.IronicSpec

		ExpectedContainerNames     []string
		ExpectedInitContainerNames []string
		ExpectedError              string
	}{
		{
			Scenario:                   "empty",
			ExpectedContainerNames:     []string{"httpd", "ironic", "ramdisk-logs"},
			ExpectedInitContainerNames: []string{"ramdisk-downloader"},
		},
		{
			Scenario: "Keepalived",
			Ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					Interface:        "eth0",
					IPAddress:        "192.0.2.2",
					IPAddressManager: metal3api.IPAddressManagerKeepalived,
				},
			},
			ExpectedContainerNames:     []string{"httpd", "ironic", "keepalived", "ramdisk-logs"},
			ExpectedInitContainerNames: []string{"ramdisk-downloader"},
		},
		{
			Scenario: "Keepalived and DHCP",
			Ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					DHCP: &metal3api.DHCP{
						NetworkCIDR: "192.0.2.1/24",
						RangeBegin:  "192.0.2.10",
						RangeEnd:    "192.0.2.200",
					},
					Interface:        "eth0",
					IPAddress:        "192.0.2.2",
					IPAddressManager: metal3api.IPAddressManagerKeepalived,
				},
			},
			ExpectedContainerNames:     []string{"dnsmasq", "httpd", "ironic", "keepalived", "ramdisk-logs"},
			ExpectedInitContainerNames: []string{"ramdisk-downloader"},
		},
		{
			Scenario: "Keepalived with additional VIPs",
			Ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					Interface: "eth0",
					IPAddress: "192.0.2.2",
					Keepalived: &metal3api.KeepalivedConfig{
						Enabled: true,
						AdditionalVIPs: []metal3api.KeepalivedIP{
							{IPAddress: "192.168.1.50", Interface: "eth1"},
						},
					},
				},
			},
			ExpectedContainerNames:     []string{"httpd", "ironic", "keepalived", "ramdisk-logs"},
			ExpectedInitContainerNames: []string{"ramdisk-downloader"},
		},
		{
			Scenario: "No ramdisk downloader",
			Ironic: metal3api.IronicSpec{
				DeployRamdisk: metal3api.DeployRamdisk{
					DisableDownloader: true,
				},
			},
			ExpectedContainerNames:     []string{"httpd", "ironic", "ramdisk-logs"},
			ExpectedInitContainerNames: []string{},
		},
		{
			Scenario: "PrometheusExporter enabled",
			Ironic: metal3api.IronicSpec{
				PrometheusExporter: &metal3api.PrometheusExporter{
					Enabled:                  true,
					SensorCollectionInterval: 60,
				},
			},
			ExpectedContainerNames:     []string{"httpd", "ironic", "ironic-prometheus-exporter", "ramdisk-logs"},
			ExpectedInitContainerNames: []string{"ramdisk-downloader"},
		},
		{
			Scenario: "PrometheusExporter disabled",
			Ironic: metal3api.IronicSpec{
				PrometheusExporter: &metal3api.PrometheusExporter{
					Enabled: false,
				},
			},
			ExpectedContainerNames:     []string{"httpd", "ironic", "ramdisk-logs"},
			ExpectedInitContainerNames: []string{"ramdisk-downloader"},
		},
	}

	privilegeEscalationAllowlist := []string{"dnsmasq", "keepalived"}

	securityAsserts := func(cont *corev1.Container) {
		assert.Truef(t, *cont.SecurityContext.ReadOnlyRootFilesystem,
			"container %s must run with read only root filesystem", cont.Name)
		assert.Falsef(t, ptr.Deref(cont.SecurityContext.Privileged, false),
			"container %s must not run as privileged", cont.Name)
		assert.NotEqualf(t, 0, ptr.Deref(cont.SecurityContext.RunAsUser, 0),
			"container %s must use RunAsUser", cont.Name)
		assert.NotEqualf(t, 0, ptr.Deref(cont.SecurityContext.RunAsUser, 0),
			"container %s must use RunAsUser", cont.Name)
		if !slices.Contains(privilegeEscalationAllowlist, cont.Name) {
			// FIXME(dtantsur): true is the default, this statement only checks that we don't explicitly set it to true.
			// Change the default in Deref to true once fixed.
			assert.Falsef(t, ptr.Deref(cont.SecurityContext.AllowPrivilegeEscalation, false),
				"container %s must not allow privilege escalations", cont.Name)
		}
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: tc.Ironic,
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			if tc.ExpectedError == "" {
				require.NoError(t, err)

				var containerNames []string
				for _, cont := range podTemplate.Spec.Containers {
					containerNames = append(containerNames, cont.Name)
					securityAsserts(&cont)
				}
				for _, cont := range podTemplate.Spec.InitContainers {
					securityAsserts(&cont)
				}

				assert.ElementsMatch(t, tc.ExpectedContainerNames, containerNames)
			} else {
				assert.ErrorContains(t, err, tc.ExpectedError)
			}
		})
	}
}

func TestImageOverrides(t *testing.T) {
	cctx := ControllerContext{}
	secret := &corev1.Secret{
		Data: map[string][]byte{"htpasswd": []byte("abcd")},
	}
	expectedImages := map[string]string{
		"httpd":              "myorg/myironic:test",
		"ironic":             "myorg/myironic:test",
		"keepalived":         "myorg/mykeepalived:test",
		"ramdisk-downloader": "myorg/mydownloader:test",
		"ramdisk-logs":       "myorg/myironic:test",
	}
	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Name:      "test",
		},
		Spec: metal3api.IronicSpec{
			Images: metal3api.Images{
				DeployRamdiskBranch:     "stable/x.y",
				DeployRamdiskDownloader: "myorg/mydownloader:test",
				Ironic:                  "myorg/myironic:test",
				Keepalived:              "myorg/mykeepalived:test",
			},
			Networking: metal3api.Networking{
				Interface:        "eth0",
				IPAddress:        "192.0.2.2",
				IPAddressManager: metal3api.IPAddressManagerKeepalived,
			},
		},
	}

	version, err := cctx.VersionInfo.WithIronicOverrides(ironic)
	require.NoError(t, err)
	cctx.VersionInfo = version

	resources := Resources{Ironic: ironic, APISecret: secret}
	podTemplate, err := newIronicPodTemplate(cctx, resources)
	require.NoError(t, err)

	images := make(map[string]string, len(expectedImages))
	var actualBranch string
	for _, cont := range podTemplate.Spec.InitContainers {
		images[cont.Name] = cont.Image
		if cont.Name == "ramdisk-downloader" {
			for _, env := range cont.Env {
				if env.Name == "IPA_BRANCH" {
					actualBranch = env.Value
				}
			}
		}
	}
	for _, cont := range podTemplate.Spec.Containers {
		images[cont.Name] = cont.Image
	}

	assert.Equal(t, expectedImages, images)
	assert.Equal(t, "stable/x.y", actualBranch)
}

func TestExpectedExtraEnvVars(t *testing.T) {
	cctx := ControllerContext{}
	secret := &corev1.Secret{
		Data: map[string][]byte{
			"htpasswd": []byte("abcd"),
		},
	}

	expectedExtraVars := map[string]string{
		"OS_PXE__BOOT_RETRY_TIMEOUT":            "1200",
		"OS_CONDUCTOR__DEPLOY_CALLBACK_TIMEOUT": "4800",
		"OS_CONDUCTOR__INSPECT_TIMEOUT":         "1800",
	}

	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Name:      "test",
		},
		Spec: metal3api.IronicSpec{
			Networking: metal3api.Networking{
				Interface:        "eth0",
				IPAddress:        "192.0.2.2",
				IPAddressManager: metal3api.IPAddressManagerKeepalived,
				RPCPort:          6189,
			},
			ExtraConfig: []metal3api.ExtraConfig{
				{
					Group: "pxe",
					Name:  "boot_retry_timeout",
					Value: "1200",
				},
				{
					Group: "conductor",
					Name:  "deploy_callback_timeout",
					Value: "4800",
				},
				{
					Group: "conductor",
					Name:  "inspect_timeout",
					Value: "1800",
				},
			},
		},
	}

	resources := Resources{Ironic: ironic, APISecret: secret}
	podTemplate, err := newIronicPodTemplate(cctx, resources)
	require.NoError(t, err)

	extraVars := make(map[string]string, len(expectedExtraVars))
	for _, env := range podTemplate.Spec.Containers[0].Env {
		if strings.Contains(env.Name, "OS_") {
			extraVars[env.Name] = env.Value
		}
	}

	assert.Equal(t, expectedExtraVars, extraVars)
}

func TestAnnotationLabelOverrides(t *testing.T) {
	cctx := ControllerContext{}
	secret := &corev1.Secret{
		Data: map[string][]byte{"htpasswd": []byte("abcd")},
	}
	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Name:      "test",
		},
		Spec: metal3api.IronicSpec{
			Overrides: &metal3api.Overrides{
				Annotations: map[string]string{
					"annotation.example.com": "my-annotation",
				},
				Labels: map[string]string{
					"label.example.com": "my-label",
				},
			},
		},
	}

	version, err := cctx.VersionInfo.WithIronicOverrides(ironic)
	require.NoError(t, err)
	cctx.VersionInfo = version

	resources := Resources{Ironic: ironic, APISecret: secret}
	podTemplate, err := newIronicPodTemplate(cctx, resources)
	require.NoError(t, err)

	assert.Equal(t, "my-annotation", podTemplate.Annotations["annotation.example.com"])
	assert.NotEmpty(t, podTemplate.Annotations["ironic.metal3.io/api-secret-version"])
	assert.Equal(t, "my-label", podTemplate.Labels["label.example.com"])
	assert.Equal(t, "test", podTemplate.Labels[metal3api.IronicServiceLabel])
}

func TestTrustedCAConfigMap(t *testing.T) {
	testCases := []struct {
		Scenario                string
		TrustedCAConfigMap      *corev1.ConfigMap
		ExpectedVolumeMountPath string
		ExpectedEnvVarValue     string
	}{
		{
			Scenario: "with TrustedCAConfigMap single key",
			TrustedCAConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trusted-ca",
					Namespace: "test",
				},
				Data: map[string]string{
					"ca-bundle.crt": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----",
				},
			},
			ExpectedVolumeMountPath: "/certs/ca/trusted",
			ExpectedEnvVarValue:     "/certs/ca/trusted/ca-bundle.crt",
		},
		{
			Scenario: "with TrustedCAConfigMap multiple keys",
			TrustedCAConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trusted-ca",
					Namespace: "test",
				},
				Data: map[string]string{
					"ca-bundle.crt": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----",
					"extra-ca.crt":  "-----BEGIN CERTIFICATE-----\nextra\n-----END CERTIFICATE-----",
				},
			},
			ExpectedVolumeMountPath: "/certs/ca/trusted",
			ExpectedEnvVarValue:     "/certs/ca/trusted/ca-bundle.crt", // keys are sorted, ca-bundle.crt comes first
		},
		{
			Scenario:           "without TrustedCAConfigMap",
			TrustedCAConfigMap: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			cctx := ControllerContext{}
			apiSecret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: metal3api.IronicSpec{},
			}

			resources := Resources{
				Ironic:             ironic,
				APISecret:          apiSecret,
				TrustedCAConfigMap: tc.TrustedCAConfigMap,
			}

			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			expectTrustedCA := tc.ExpectedEnvVarValue != ""

			// Check volume
			var foundVolume bool
			for _, vol := range podTemplate.Spec.Volumes {
				if vol.Name == "trusted-ca" {
					foundVolume = true
					if expectTrustedCA {
						assert.NotNil(t, vol.ConfigMap)
						assert.Equal(t, tc.TrustedCAConfigMap.Name, vol.ConfigMap.Name)
					}
					break
				}
			}
			assert.Equal(t, expectTrustedCA, foundVolume, "Volume existence mismatch")

			// Check volume mount on ironic container
			var ironicContainer *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == ironicContainerName {
					ironicContainer = &podTemplate.Spec.Containers[i]
					break
				}
			}
			require.NotNil(t, ironicContainer, "Ironic container should exist")

			var foundMount bool
			for _, mount := range ironicContainer.VolumeMounts {
				if mount.Name == "trusted-ca" {
					foundMount = true
					if expectTrustedCA {
						assert.Equal(t, tc.ExpectedVolumeMountPath, mount.MountPath)
						assert.True(t, mount.ReadOnly)
					}
					break
				}
			}
			assert.Equal(t, expectTrustedCA, foundMount, "Volume mount existence mismatch")

			// Check environment variables
			var foundWebserverCACert, foundIronicCACert bool
			var webserverCACertValue string
			for _, env := range ironicContainer.Env {
				if env.Name == "WEBSERVER_CACERT_FILE" {
					foundWebserverCACert = true
					webserverCACertValue = env.Value
				}
				if env.Name == "IRONIC_CACERT_FILE" {
					foundIronicCACert = true
				}
			}
			assert.Equal(t, expectTrustedCA, foundWebserverCACert, "WEBSERVER_CACERT_FILE environment variable existence mismatch")
			// IRONIC_CACERT_FILE should not be set without TLS
			assert.False(t, foundIronicCACert, "IRONIC_CACERT_FILE should not be set without TLS")

			if expectTrustedCA {
				assert.Equal(t, tc.ExpectedEnvVarValue, webserverCACertValue, "WEBSERVER_CACERT_FILE value mismatch")
			}

			// Check the ramdisk-downloader init container: it should trust the
			// same CA bundle as the ironic/httpd containers, via CURL_CA_BUNDLE
			// and the same "trusted-ca" volume mount.
			var downloaderContainer *corev1.Container
			for i := range podTemplate.Spec.InitContainers {
				if podTemplate.Spec.InitContainers[i].Name == ramdiskDownloaderContainerName {
					downloaderContainer = &podTemplate.Spec.InitContainers[i]
					break
				}
			}
			require.NotNil(t, downloaderContainer, "ramdisk-downloader init container should exist")

			var foundDownloaderMount bool
			for _, mount := range downloaderContainer.VolumeMounts {
				if mount.Name == trustedCAVolumeName {
					foundDownloaderMount = true
					if expectTrustedCA {
						assert.Equal(t, tc.ExpectedVolumeMountPath, mount.MountPath)
						assert.True(t, mount.ReadOnly)
					}
					break
				}
			}
			assert.Equal(t, expectTrustedCA, foundDownloaderMount, "ramdisk-downloader trusted-ca volume mount existence mismatch")

			var foundCurlCABundle bool
			var curlCABundleValue string
			for _, env := range downloaderContainer.Env {
				if env.Name == "CURL_CA_BUNDLE" {
					foundCurlCABundle = true
					curlCABundleValue = env.Value
				}
			}
			assert.Equal(t, expectTrustedCA, foundCurlCABundle, "CURL_CA_BUNDLE environment variable existence mismatch")
			if expectTrustedCA {
				assert.Equal(t, tc.ExpectedEnvVarValue, curlCABundleValue, "CURL_CA_BUNDLE value mismatch")
			}
		})
	}
}

func TestIronicPorts(t *testing.T) {
	testCases := []struct {
		name                string
		spec                metal3api.IronicSpec
		expectedPort        int32
		expectedIronicPorts []int32
		expectedHttpdPorts  []int32
		expectNoHostPort    bool
	}{
		{
			name: "standard API port",
			spec: metal3api.IronicSpec{
				Networking: metal3api.Networking{},
			},
			expectedPort:        6385,
			expectedIronicPorts: []int32{6385},
			expectedHttpdPorts:  []int32{6180},
		},
		{
			name: "standard API port without host networking",
			spec: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					DisableHostNetwork: true,
				},
			},
			expectedPort:        6385,
			expectedIronicPorts: []int32{6385},
			expectedHttpdPorts:  []int32{6180},
			expectNoHostPort:    true,
		},
		{
			name: "custom API port",
			spec: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					APIPort: 6388,
				},
			},
			expectedPort:        6388,
			expectedIronicPorts: []int32{6388},
			expectedHttpdPorts:  []int32{6180},
		},
		{
			name: "custom HTTPD port",
			spec: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					ImageServerPort: 6388,
				},
			},
			expectedPort:        6385,
			expectedIronicPorts: []int32{6385},
			expectedHttpdPorts:  []int32{6388},
		},
		{
			name: "standard API port with TLS",
			spec: metal3api.IronicSpec{
				Networking: metal3api.Networking{},
				TLS: metal3api.TLS{
					CertificateName: "my-tls",
				},
			},
			expectedPort:        6385,
			expectedIronicPorts: []int32{},
			expectedHttpdPorts:  []int32{6180, 6183, 6385},
		},
		{
			name: "standard API port with TLS without host networking",
			spec: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					DisableHostNetwork: true,
				},
				TLS: metal3api.TLS{
					CertificateName: "my-tls",
				},
			},
			expectedPort:        6385,
			expectedIronicPorts: []int32{},
			expectedHttpdPorts:  []int32{6180, 6183, 6385},
			expectNoHostPort:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: tc.spec,
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			var ironicContainer, httpdContainer *corev1.Container
			for _, cont := range podTemplate.Spec.Containers {
				if cont.Name == ironicContainerName {
					ironicContainer = new(cont)
				}
				if cont.Name == httpdContainerName {
					httpdContainer = new(cont)
				}
			}
			require.NotNil(t, ironicContainer, "ironic container should exist")
			require.NotNil(t, httpdContainer, "httpd container should exist")

			collectPorts := func(cont *corev1.Container) []int32 {
				result := make([]int32, 0, len(cont.Ports))
				for _, port := range cont.Ports {
					result = append(result, port.ContainerPort)
					expectedHostPort := port.ContainerPort
					if tc.expectNoHostPort {
						expectedHostPort = 0
					}
					assert.Equal(t, expectedHostPort, port.HostPort)
				}
				return result
			}

			// Check for configured ports
			ironicPorts := collectPorts(ironicContainer)
			httpdPorts := collectPorts(httpdContainer)
			slices.Sort(ironicPorts)
			slices.Sort(httpdPorts)

			assert.Equal(t, tc.expectedIronicPorts, ironicPorts)
			assert.Equal(t, tc.expectedHttpdPorts, httpdPorts)

			// Check for IRONIC_LISTEN_PORT and IRONIC_ACCESS_PORT env vars
			var listenPortValue, accessPortValue string
			for _, env := range ironicContainer.Env {
				if env.Name == "IRONIC_LISTEN_PORT" {
					listenPortValue = env.Value
				}
				if env.Name == "IRONIC_ACCESS_PORT" {
					accessPortValue = env.Value
				}
			}

			expectedPort := strconv.Itoa(int(tc.expectedPort))
			assert.Equal(t, expectedPort, listenPortValue, "IRONIC_LISTEN_PORT value mismatch")
			assert.Equal(t, expectedPort, accessPortValue, "IRONIC_ACCESS_PORT value mismatch")
		})
	}
}

func TestProvisioningIPFieldRef(t *testing.T) {
	testCases := []struct {
		name               string
		disableHostNetwork bool
		expectedFieldPath  string
	}{
		{
			name:               "host networking enabled uses hostIP",
			disableHostNetwork: false,
			expectedFieldPath:  "status.hostIP",
		},
		{
			name:               "host networking disabled uses podIP",
			disableHostNetwork: true,
			expectedFieldPath:  "status.podIP",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: metal3api.IronicSpec{
					Networking: metal3api.Networking{
						DisableHostNetwork: tc.disableHostNetwork,
					},
				},
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			var ironicContainer *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == ironicContainerName {
					ironicContainer = &podTemplate.Spec.Containers[i]
					break
				}
			}
			require.NotNil(t, ironicContainer, "ironic container should exist")

			var provisioningIPEnv *corev1.EnvVar
			for i := range ironicContainer.Env {
				if ironicContainer.Env[i].Name == "PROVISIONING_IP" {
					provisioningIPEnv = &ironicContainer.Env[i]
					break
				}
			}
			require.NotNil(t, provisioningIPEnv, "PROVISIONING_IP env var should be present")

			require.NotNil(t, provisioningIPEnv.ValueFrom, "PROVISIONING_IP should be sourced from a fieldRef")
			require.NotNil(t, provisioningIPEnv.ValueFrom.FieldRef, "PROVISIONING_IP should be sourced from a fieldRef")
			assert.Equal(t, tc.expectedFieldPath, provisioningIPEnv.ValueFrom.FieldRef.FieldPath)
			assert.Empty(t, provisioningIPEnv.Value, "PROVISIONING_IP should not have a static value when using fieldRef")
		})
	}
}

func TestHostNetworkPodSettings(t *testing.T) {
	testCases := []struct {
		name                string
		disableHostNetwork  bool
		expectedHostNetwork bool
		expectedDNSPolicy   corev1.DNSPolicy
	}{
		{
			name:                "host networking enabled",
			disableHostNetwork:  false,
			expectedHostNetwork: true,
			expectedDNSPolicy:   corev1.DNSClusterFirstWithHostNet,
		},
		{
			name:                "host networking disabled",
			disableHostNetwork:  true,
			expectedHostNetwork: false,
			expectedDNSPolicy:   corev1.DNSClusterFirst,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: metal3api.IronicSpec{
					Networking: metal3api.Networking{
						DisableHostNetwork: tc.disableHostNetwork,
					},
				},
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedHostNetwork, podTemplate.Spec.HostNetwork, "HostNetwork mismatch")
			assert.Equal(t, tc.expectedDNSPolicy, podTemplate.Spec.DNSPolicy, "DNSPolicy mismatch")
		})
	}
}

func TestExternalURLEnvVars(t *testing.T) {
	testCases := []struct {
		name                   string
		ingress                *metal3api.Ingress
		externalCallbackURL    string
		imageServerExternalURL string
		expectVarsSet          bool
		expectedCallbackURL    string
		expectedImageServerURL string
	}{
		{
			name:                   "ingress only, URLs derived from ingress host",
			ingress:                &metal3api.Ingress{Host: "ironic.example.com"},
			expectVarsSet:          true,
			expectedCallbackURL:    "https://ironic.example.com",
			expectedImageServerURL: "https://ironic.example.com",
		},
		{
			name:                   "ingress with externalCallbackURL override",
			ingress:                &metal3api.Ingress{Host: "ironic.example.com"},
			externalCallbackURL:    "https://callback.example.com",
			expectVarsSet:          true,
			expectedCallbackURL:    "https://callback.example.com",
			expectedImageServerURL: "https://ironic.example.com",
		},
		{
			name:                   "ingress with imageServerExternalURL override",
			ingress:                &metal3api.Ingress{Host: "ironic.example.com"},
			imageServerExternalURL: "https://image.example.com",
			expectVarsSet:          true,
			expectedCallbackURL:    "https://ironic.example.com",
			expectedImageServerURL: "https://image.example.com",
		},
		{
			name:                   "ingress with both URLs overridden",
			ingress:                &metal3api.Ingress{Host: "ironic.example.com"},
			externalCallbackURL:    "https://callback.example.com",
			imageServerExternalURL: "https://image.example.com",
			expectVarsSet:          true,
			expectedCallbackURL:    "https://callback.example.com",
			expectedImageServerURL: "https://image.example.com",
		},
		{
			name:                   "no ingress, both external URLs provided",
			externalCallbackURL:    "https://callback.example.com",
			imageServerExternalURL: "https://image.example.com",
			expectVarsSet:          true,
			expectedCallbackURL:    "https://callback.example.com",
			expectedImageServerURL: "https://image.example.com",
		},
		{
			name:                "no ingress, only externalCallbackURL provided",
			externalCallbackURL: "https://callback.example.com",
			expectVarsSet:       false,
		},
		{
			name:                   "no ingress, only imageServerExternalURL provided",
			imageServerExternalURL: "https://image.example.com",
			expectVarsSet:          false,
		},
		{
			name:          "no ingress, no external URLs",
			expectVarsSet: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: metal3api.IronicSpec{
					Networking: metal3api.Networking{
						Ingress:                tc.ingress,
						ExternalCallbackURL:    tc.externalCallbackURL,
						ImageServerExternalURL: tc.imageServerExternalURL,
					},
				},
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			var ironicContainer *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == ironicContainerName {
					ironicContainer = &podTemplate.Spec.Containers[i]
					break
				}
			}
			require.NotNil(t, ironicContainer, "ironic container should exist")

			envMap := make(map[string]string)
			for _, env := range ironicContainer.Env {
				envMap[env.Name] = env.Value
			}

			if tc.expectVarsSet {
				assert.Equal(t, tc.expectedCallbackURL, envMap["IRONIC_EXTERNAL_CALLBACK_URL"])
				assert.Equal(t, tc.expectedImageServerURL, envMap["IRONIC_EXTERNAL_HTTP_URL"])
			} else {
				_, hasCallback := envMap["IRONIC_EXTERNAL_CALLBACK_URL"]
				_, hasHTTP := envMap["IRONIC_EXTERNAL_HTTP_URL"]
				assert.False(t, hasCallback, "IRONIC_EXTERNAL_CALLBACK_URL should not be set")
				assert.False(t, hasHTTP, "IRONIC_EXTERNAL_HTTP_URL should not be set")
			}
		})
	}
}

func TestPrometheusExporterEnvVars(t *testing.T) {
	testCases := []struct {
		name                   string
		prometheusExporter     *metal3api.PrometheusExporter
		expectedSendSensorData string
		expectedSensorInterval string
		expectedFlaskRunHost   string
		expectReadinessProbe   bool
		expectedProbeHost      string
	}{
		{
			name: "PrometheusExporter enabled with default interval",
			prometheusExporter: &metal3api.PrometheusExporter{
				Enabled:                  true,
				SensorCollectionInterval: 0, // Should default to 60
			},
			expectedSendSensorData: "true",
			expectedSensorInterval: "60",
			expectedFlaskRunHost:   "0.0.0.0",
			expectReadinessProbe:   true,
			expectedProbeHost:      "127.0.0.1",
		},
		{
			name: "PrometheusExporter enabled with custom interval",
			prometheusExporter: &metal3api.PrometheusExporter{
				Enabled:                  true,
				SensorCollectionInterval: 120,
			},
			expectedSendSensorData: "true",
			expectedSensorInterval: "120",
			expectedFlaskRunHost:   "0.0.0.0",
			expectReadinessProbe:   true,
			expectedProbeHost:      "127.0.0.1",
		},
		{
			name: "PrometheusExporter enabled with bindAddress 0.0.0.0",
			prometheusExporter: &metal3api.PrometheusExporter{
				Enabled:     true,
				BindAddress: "0.0.0.0",
			},
			expectedSendSensorData: "true",
			expectedSensorInterval: "60",
			expectedFlaskRunHost:   "0.0.0.0",
			expectReadinessProbe:   true,
			expectedProbeHost:      "127.0.0.1",
		},
		{
			name: "PrometheusExporter enabled with IPv6 wildcard bindAddress ::",
			prometheusExporter: &metal3api.PrometheusExporter{
				Enabled:     true,
				BindAddress: "::",
			},
			expectedSendSensorData: "true",
			expectedSensorInterval: "60",
			expectedFlaskRunHost:   "::",
			expectReadinessProbe:   true,
			expectedProbeHost:      "127.0.0.1",
		},
		{
			name: "PrometheusExporter enabled with custom bindAddress",
			prometheusExporter: &metal3api.PrometheusExporter{
				Enabled:     true,
				BindAddress: "192.168.1.10",
			},
			expectedSendSensorData: "true",
			expectedSensorInterval: "60",
			expectedFlaskRunHost:   "192.168.1.10",
			expectReadinessProbe:   true,
			expectedProbeHost:      "192.168.1.10", // Specific IP probes itself
		},
		{
			name: "PrometheusExporter enabled with localhost bindAddress",
			prometheusExporter: &metal3api.PrometheusExporter{
				Enabled:     true,
				BindAddress: "127.0.0.1",
			},
			expectedSendSensorData: "true",
			expectedSensorInterval: "60",
			expectedFlaskRunHost:   "127.0.0.1",
			expectReadinessProbe:   false, // No HTTP probe for localhost binding
		},
		{
			name: "PrometheusExporter enabled with empty bindAddress defaults to wildcard",
			prometheusExporter: &metal3api.PrometheusExporter{
				Enabled: true,
			},
			expectedSendSensorData: "true",
			expectedSensorInterval: "60",
			expectedFlaskRunHost:   "0.0.0.0",
			expectReadinessProbe:   true,
			expectedProbeHost:      "127.0.0.1",
		},
		{
			name: "PrometheusExporter disabled",
			prometheusExporter: &metal3api.PrometheusExporter{
				Enabled: false,
			},
		},
		{
			name:               "PrometheusExporter nil",
			prometheusExporter: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: metal3api.IronicSpec{
					PrometheusExporter: tc.prometheusExporter,
				},
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			expectExporter := tc.expectedSendSensorData == "true"

			var ironicContainer *corev1.Container
			var exporterContainer *corev1.Container
			for _, cont := range podTemplate.Spec.Containers {
				contCopy := cont
				if cont.Name == ironicContainerName {
					ironicContainer = &contCopy
				}
				if cont.Name == "ironic-prometheus-exporter" {
					exporterContainer = &contCopy
				}
			}
			require.NotNil(t, ironicContainer, "ironic container not found")
			if expectExporter {
				require.NotNil(t, exporterContainer, "ironic-prometheus-exporter container not found")
				assert.Len(t, exporterContainer.Env, 2)
				assert.Equal(t, "FLASK_RUN_HOST", exporterContainer.Env[0].Name)
				assert.Equal(t, tc.expectedFlaskRunHost, exporterContainer.Env[0].Value)
				assert.Equal(t, "FLASK_RUN_PORT", exporterContainer.Env[1].Name)
				assert.Equal(t, "9608", exporterContainer.Env[1].Value)
				assert.Len(t, exporterContainer.Ports, 1)
				assert.Equal(t, int32(9608), exporterContainer.Ports[0].ContainerPort)

				// Verify readiness probe is configured based on bind address
				if tc.expectReadinessProbe {
					require.NotNil(t, exporterContainer.ReadinessProbe, "readiness probe should be set for non-localhost binding")
					require.NotNil(t, exporterContainer.ReadinessProbe.HTTPGet, "HTTPGet probe should be set")
					assert.Equal(t, "/metrics", exporterContainer.ReadinessProbe.HTTPGet.Path)
					assert.Equal(t, intstr.FromInt32(9608), exporterContainer.ReadinessProbe.HTTPGet.Port)
					assert.Equal(t, corev1.URISchemeHTTP, exporterContainer.ReadinessProbe.HTTPGet.Scheme)
					assert.Equal(t, tc.expectedProbeHost, exporterContainer.ReadinessProbe.HTTPGet.Host)
					assert.Equal(t, int32(5), exporterContainer.ReadinessProbe.InitialDelaySeconds)
					assert.Equal(t, int32(10), exporterContainer.ReadinessProbe.PeriodSeconds)
				} else {
					assert.Nil(t, exporterContainer.ReadinessProbe, "readiness probe should not be set for localhost binding")
				}
			}

			// Check for SEND_SENSOR_DATA env var
			var foundSendSensorData, foundSensorInterval bool
			for _, env := range ironicContainer.Env {
				if env.Name == "SEND_SENSOR_DATA" {
					foundSendSensorData = true
					assert.Equal(t, tc.expectedSendSensorData, env.Value)
				}
				if env.Name == "OS_SENSOR_DATA__INTERVAL" {
					foundSensorInterval = true
					assert.Equal(t, tc.expectedSensorInterval, env.Value)
				}
			}

			assert.Equal(t, expectExporter, foundSendSensorData,
				"SEND_SENSOR_DATA env var presence mismatch")
			assert.Equal(t, expectExporter, foundSensorInterval,
				"OS_SENSOR_DATA__INTERVAL env var presence mismatch")
		})
	}
}

func TestAppendAgentImageEnvVars(t *testing.T) {
	testCases := []struct {
		name            string
		images          []metal3api.AgentImages
		expectedEnvVars map[string]string
		expectNoEnvVars bool
	}{
		{
			name:            "empty images",
			images:          nil,
			expectNoEnvVars: true,
		},
		{
			name: "single architecture x86_64",
			images: []metal3api.AgentImages{
				{
					Kernel:       "file:///shared/html/images/ipa.x86_64.kernel",
					Initramfs:    "file:///shared/html/images/ipa.x86_64.initramfs",
					Architecture: metal3api.ArchX86_64,
				},
			},
			expectedEnvVars: map[string]string{
				"DEPLOY_KERNEL_BY_ARCH":  "x86_64:file:///shared/html/images/ipa.x86_64.kernel",
				"DEPLOY_RAMDISK_BY_ARCH": "x86_64:file:///shared/html/images/ipa.x86_64.initramfs",
			},
		},
		{
			name: "single architecture aarch64",
			images: []metal3api.AgentImages{
				{
					Kernel:       "file:///shared/html/images/ipa.aarch64.kernel",
					Initramfs:    "file:///shared/html/images/ipa.aarch64.initramfs",
					Architecture: metal3api.ArchAarch64,
				},
			},
			expectedEnvVars: map[string]string{
				"DEPLOY_KERNEL_BY_ARCH":  "aarch64:file:///shared/html/images/ipa.aarch64.kernel",
				"DEPLOY_RAMDISK_BY_ARCH": "aarch64:file:///shared/html/images/ipa.aarch64.initramfs",
			},
		},
		{
			name: "multiple architectures",
			images: []metal3api.AgentImages{
				{
					Kernel:       "file:///shared/html/images/ipa.x86_64.kernel",
					Initramfs:    "file:///shared/html/images/ipa.x86_64.initramfs",
					Architecture: metal3api.ArchX86_64,
				},
				{
					Kernel:       "file:///shared/html/images/ipa.aarch64.kernel",
					Initramfs:    "file:///shared/html/images/ipa.aarch64.initramfs",
					Architecture: metal3api.ArchAarch64,
				},
			},
			expectedEnvVars: map[string]string{
				"DEPLOY_KERNEL_BY_ARCH":  "x86_64:file:///shared/html/images/ipa.x86_64.kernel,aarch64:file:///shared/html/images/ipa.aarch64.kernel",
				"DEPLOY_RAMDISK_BY_ARCH": "x86_64:file:///shared/html/images/ipa.x86_64.initramfs,aarch64:file:///shared/html/images/ipa.aarch64.initramfs",
			},
		},
		{
			name: "whitespace trimming",
			images: []metal3api.AgentImages{
				{
					Kernel:       "  file:///path/kernel  ",
					Initramfs:    "\tfile:///path/initramfs\n",
					Architecture: metal3api.ArchX86_64,
				},
			},
			expectedEnvVars: map[string]string{
				"DEPLOY_KERNEL_BY_ARCH":  "x86_64:file:///path/kernel",
				"DEPLOY_RAMDISK_BY_ARCH": "x86_64:file:///path/initramfs",
			},
		},
		{
			name: "default (empty architecture)",
			images: []metal3api.AgentImages{
				{
					Kernel:    "file:///shared/html/images/ipa.kernel",
					Initramfs: "file:///shared/html/images/ipa.initramfs",
				},
			},
			expectedEnvVars: map[string]string{
				"DEPLOY_KERNEL_URL":  "file:///shared/html/images/ipa.kernel",
				"DEPLOY_RAMDISK_URL": "file:///shared/html/images/ipa.initramfs",
			},
		},
		{
			name: "default with architecture-specific",
			images: []metal3api.AgentImages{
				{
					Kernel:    "file:///shared/html/images/ipa.kernel",
					Initramfs: "file:///shared/html/images/ipa.initramfs",
				},
				{
					Kernel:       "file:///shared/html/images/ipa.x86_64.kernel",
					Initramfs:    "file:///shared/html/images/ipa.x86_64.initramfs",
					Architecture: metal3api.ArchX86_64,
				},
			},
			expectedEnvVars: map[string]string{
				"DEPLOY_KERNEL_URL":      "file:///shared/html/images/ipa.kernel",
				"DEPLOY_RAMDISK_URL":     "file:///shared/html/images/ipa.initramfs",
				"DEPLOY_KERNEL_BY_ARCH":  "x86_64:file:///shared/html/images/ipa.x86_64.kernel",
				"DEPLOY_RAMDISK_BY_ARCH": "x86_64:file:///shared/html/images/ipa.x86_64.initramfs",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			envVars := appendAgentImageEnvVars(nil, tc.images)

			if tc.expectNoEnvVars {
				assert.Empty(t, envVars)
				return
			}

			envMap := make(map[string]string, len(envVars))
			for _, env := range envVars {
				envMap[env.Name] = env.Value
			}

			for name, expectedValue := range tc.expectedEnvVars {
				assert.Equal(t, expectedValue, envMap[name], "env var %s", name)
			}
			assert.Len(t, envVars, len(tc.expectedEnvVars))
		})
	}
}

func TestHttpdProbeConfiguration(t *testing.T) {
	testCases := []struct {
		Scenario                   string
		Ironic                     metal3api.IronicSpec
		ExpectExecProbe            bool
		ExpectCurlFail             bool
		ExpectCustomProbe          bool
		ExpectCustomReadinessProbe bool
		ExpectExecReadinessProbe   bool
	}{
		{
			Scenario: "Default - no custom images",
			Ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					ImageServerPort: 8080,
				},
			},
			ExpectExecProbe: true,
			ExpectCurlFail:  true,
		},
		{
			Scenario: "Custom images - exec probe without HTTP success requirement",
			Ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					ImageServerPort: 8080,
				},
				Overrides: &metal3api.Overrides{
					AgentImages: []metal3api.AgentImages{
						{
							Architecture: metal3api.ArchX86_64,
							Kernel:       "file:///custom/kernel",
							Initramfs:    "file:///custom/initramfs",
						},
					},
				},
			},
			ExpectExecProbe: true,
			ExpectCurlFail:  false,
		},
		{
			Scenario: "Custom images with downloader disabled",
			Ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					ImageServerPort: 8080,
				},
				DeployRamdisk: metal3api.DeployRamdisk{
					DisableDownloader: true,
				},
				Overrides: &metal3api.Overrides{
					AgentImages: []metal3api.AgentImages{
						{
							Architecture: metal3api.ArchAarch64,
							Kernel:       "http://external.com/kernel",
							Initramfs:    "http://external.com/initramfs",
						},
					},
				},
			},
			ExpectExecProbe: true,
			ExpectCurlFail:  false,
		},
		{
			Scenario: "Custom images with explicit readiness probe override",
			Ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					ImageServerPort: 8080,
				},
				Overrides: &metal3api.Overrides{
					AgentImages: []metal3api.AgentImages{
						{
							Architecture: metal3api.ArchX86_64,
							Kernel:       "file:///custom/kernel",
							Initramfs:    "file:///custom/initramfs",
						},
					},
					HttpdReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/ready",
								Port: intstr.FromInt(8080),
							},
						},
					},
				},
			},
			ExpectExecProbe:            true, // liveness stays exec default
			ExpectCustomReadinessProbe: true,
		},
		{
			Scenario: "Custom images with explicit liveness probe override",
			Ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					ImageServerPort: 8080,
				},
				Overrides: &metal3api.Overrides{
					AgentImages: []metal3api.AgentImages{
						{
							Architecture: metal3api.ArchX86_64,
							Kernel:       "file:///custom/kernel",
							Initramfs:    "file:///custom/initramfs",
						},
					},
					HttpdLivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/health",
								Port: intstr.FromInt(8080),
							},
						},
					},
				},
			},
			ExpectCustomProbe:        true,
			ExpectExecReadinessProbe: true, // readiness stays exec when only liveness is overridden
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("test"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: tc.Ironic,
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			// Find the httpd container
			var httpdContainer *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == "httpd" {
					httpdContainer = &podTemplate.Spec.Containers[i]
					break
				}
			}
			require.NotNil(t, httpdContainer, "httpd container should exist")
			require.NotNil(t, httpdContainer.LivenessProbe, "liveness probe should not be nil")
			require.NotNil(t, httpdContainer.ReadinessProbe, "readiness probe should not be nil")

			if tc.ExpectCustomProbe {
				assert.NotNil(t, httpdContainer.LivenessProbe.HTTPGet)
				assert.Equal(t, "/health", httpdContainer.LivenessProbe.HTTPGet.Path)
			} else if tc.ExpectExecProbe {
				assert.NotNil(t, httpdContainer.LivenessProbe.Exec, "should have exec probe")
				assert.Nil(t, httpdContainer.LivenessProbe.HTTPGet, "should not have HTTPGet probe")
				if tc.ExpectCurlFail {
					assert.Contains(t, httpdContainer.LivenessProbe.Exec.Command, "--fail", "exec probe should include --fail for default images")
				} else {
					assert.NotContains(t, httpdContainer.LivenessProbe.Exec.Command, "--fail", "exec probe should not include --fail for custom images")
				}
			}

			if tc.ExpectCustomReadinessProbe {
				assert.NotNil(t, httpdContainer.ReadinessProbe.HTTPGet)
				assert.Equal(t, "/ready", httpdContainer.ReadinessProbe.HTTPGet.Path)
			} else if tc.ExpectExecProbe || tc.ExpectExecReadinessProbe {
				assert.NotNil(t, httpdContainer.ReadinessProbe.Exec, "should have exec readiness probe")
				assert.Nil(t, httpdContainer.ReadinessProbe.HTTPGet, "should not have HTTPGet readiness probe")
				if tc.ExpectCurlFail {
					assert.Contains(t, httpdContainer.ReadinessProbe.Exec.Command, "--fail", "exec readiness probe should include --fail for default images")
				} else {
					assert.NotContains(t, httpdContainer.ReadinessProbe.Exec.Command, "--fail", "exec readiness probe should not include --fail for custom images")
				}
			}
		})
	}
}

func TestDnsmasqProbeConfiguration(t *testing.T) {
	ipv4Net := metal3api.Networking{
		Interface: "eth0",
		IPAddress: "192.0.2.2",
		DHCP: &metal3api.DHCP{
			NetworkCIDR: "192.0.2.0/24",
			RangeBegin:  "192.0.2.10",
			RangeEnd:    "192.0.2.200",
		},
	}
	ipv6Net := metal3api.Networking{
		Interface: "eth0",
		IPAddress: "fd00:abcd:ef01:3fff::a",
		DHCP: &metal3api.DHCP{
			NetworkCIDR: "fd00:abcd:ef01:3fff::/64",
			RangeBegin:  "fd00:abcd:ef01:3fff::3000",
			RangeEnd:    "fd00:abcd:ef01:3fff::3fff",
		},
	}
	mixedNet := metal3api.Networking{
		Interface: "eth0",
		IPAddress: "192.0.2.2",
		DHCP: &metal3api.DHCP{
			NetworkCIDR: "192.0.2.0/24",
			RangeBegin:  "192.0.2.10",
			RangeEnd:    "192.0.2.200",
			ExtraRanges: []metal3api.DHCPRange{
				{
					NetworkCIDR: "fd00:abcd:ef01:3fff::/64",
					RangeBegin:  "fd00:abcd:ef01:3fff::3000",
					RangeEnd:    "fd00:abcd:ef01:3fff::3fff",
				},
			},
		},
	}

	testCases := []struct {
		Scenario    string
		Networking  metal3api.Networking
		ExpectedCmd string
	}{
		{
			Scenario:    "IPv4 CIDR uses DHCPv4 port 67",
			Networking:  ipv4Net,
			ExpectedCmd: "ss -lun | grep :67 && ss -lun | grep :69",
		},
		{
			Scenario:    "IPv6 CIDR uses DHCPv6 port 547",
			Networking:  ipv6Net,
			ExpectedCmd: "ss -lun | grep :547 && ss -lun | grep :69",
		},
		{
			Scenario:    "mixed families probe both DHCP ports",
			Networking:  mixedNet,
			ExpectedCmd: "ss -lun | grep :67 && ss -lun | grep :547 && ss -lun | grep :69",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("test"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: metal3api.IronicSpec{
					Networking: tc.Networking,
				},
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			var dnsmasq *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == "dnsmasq" {
					dnsmasq = &podTemplate.Spec.Containers[i]
					break
				}
			}
			require.NotNil(t, dnsmasq, "dnsmasq container should exist")
			require.NotNil(t, dnsmasq.LivenessProbe, "liveness probe should not be nil")
			require.NotNil(t, dnsmasq.LivenessProbe.Exec, "liveness probe should be exec-based")
			require.NotNil(t, dnsmasq.ReadinessProbe, "readiness probe should not be nil")
			require.NotNil(t, dnsmasq.ReadinessProbe.Exec, "readiness probe should be exec-based")

			assert.Equal(t,
				[]string{"sh", "-c", tc.ExpectedCmd},
				dnsmasq.LivenessProbe.Exec.Command,
				"liveness probe command mismatch")
			assert.Equal(t,
				[]string{"sh", "-c", tc.ExpectedCmd},
				dnsmasq.ReadinessProbe.Exec.Command,
				"readiness probe command mismatch")
		})
	}
}

func TestBuildTrustedCAEnvVars(t *testing.T) {
	testCases := []struct {
		name          string
		trustedCARef  *metal3api.ResourceReferenceWithKey
		configMapData map[string]string
		secretData    map[string][]byte
		expectedKey   string
	}{
		{
			name: "ConfigMap with specific key",
			trustedCARef: &metal3api.ResourceReferenceWithKey{
				ResourceReference: metal3api.ResourceReference{
					Name: "trusted-ca",
					Kind: "ConfigMap",
				},
				Key: "custom-ca.crt",
			},
			configMapData: map[string]string{
				"custom-ca.crt": "cert1",
				"other-ca.crt":  "cert2",
			},
			expectedKey: "custom-ca.crt",
		},
		{
			name: "Secret with specific key",
			trustedCARef: &metal3api.ResourceReferenceWithKey{
				ResourceReference: metal3api.ResourceReference{
					Name: "trusted-ca-secret",
					Kind: "Secret",
				},
				Key: "tls.crt",
			},
			secretData: map[string][]byte{
				"tls.crt": []byte("cert1"),
				"ca.crt":  []byte("cert2"),
			},
			expectedKey: "tls.crt",
		},
		{
			name: "Multiple keys without Key specified - ConfigMap",
			trustedCARef: &metal3api.ResourceReferenceWithKey{
				ResourceReference: metal3api.ResourceReference{
					Name: "trusted-ca",
					Kind: "ConfigMap",
				},
			},
			configMapData: map[string]string{
				"ca1.crt": "cert1",
				"ca2.crt": "cert2",
			},
			expectedKey: "ca1.crt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := logr.New(logr.Discard().GetSink())

			cctx := ControllerContext{
				Logger: logger,
			}

			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ironic",
					Namespace: "test",
				},
				Spec: metal3api.IronicSpec{
					TLS: metal3api.TLS{
						TrustedCA: tc.trustedCARef,
					},
				},
			}

			resources := Resources{
				Ironic: ironic,
			}

			if tc.configMapData != nil {
				resources.TrustedCAConfigMap = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tc.trustedCARef.Name,
						Namespace: "test",
					},
					Data: tc.configMapData,
				}
			}

			if tc.secretData != nil {
				resources.TrustedCASecret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tc.trustedCARef.Name,
						Namespace: "test",
					},
					Data: tc.secretData,
				}
			}

			envVars := buildTrustedCAEnvVars(cctx, resources)

			require.Len(t, envVars, 1, "Should return one environment variable")
			assert.Equal(t, "WEBSERVER_CACERT_FILE", envVars[0].Name)

			expectedPath := "/certs/ca/trusted/" + tc.expectedKey
			assert.Equal(t, expectedPath, envVars[0].Value, "WEBSERVER_CACERT_FILE value mismatch")
		})
	}
}

func TestKeepalivedEnvVars(t *testing.T) {
	testCases := []struct {
		name                     string
		ironic                   metal3api.IronicSpec
		expectedKeepalivedVIPEnv string
		expectedProvisioningIP   string
		expectedProvInterface    string
	}{
		{
			name: "legacy single-IP mode",
			ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					Interface:        "eth0",
					IPAddress:        "192.0.2.2",
					IPAddressManager: metal3api.IPAddressManagerKeepalived,
				},
			},
			expectedProvisioningIP: "192.0.2.2",
			expectedProvInterface:  "eth0",
		},
		{
			name: "keepalived enabled with additional VIPs",
			ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					Interface: "eth0",
					IPAddress: "192.0.2.2",
					Keepalived: &metal3api.KeepalivedConfig{
						Enabled: true,
						AdditionalVIPs: []metal3api.KeepalivedIP{
							{IPAddress: "192.168.1.50", Interface: "eth1"},
						},
					},
				},
			},
			expectedKeepalivedVIPEnv: "192.0.2.2,eth0 192.168.1.50,eth1",
		},
		{
			name: "keepalived with DHCP: main IP prefix derived from NetworkCIDR",
			ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					Interface: "eth0",
					IPAddress: "192.0.2.2",
					DHCP: &metal3api.DHCP{
						NetworkCIDR: "192.0.2.0/24",
						RangeBegin:  "192.0.2.10",
						RangeEnd:    "192.0.2.200",
					},
					Keepalived: &metal3api.KeepalivedConfig{
						Enabled: true,
					},
				},
			},
			expectedKeepalivedVIPEnv: "192.0.2.2,eth0,24",
		},
		{
			name: "keepalived with DHCP and additional VIP with explicit prefix",
			ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					Interface: "eth0",
					IPAddress: "192.0.2.2",
					DHCP: &metal3api.DHCP{
						NetworkCIDR: "192.0.2.0/24",
						RangeBegin:  "192.0.2.10",
						RangeEnd:    "192.0.2.200",
					},
					Keepalived: &metal3api.KeepalivedConfig{
						Enabled: true,
						AdditionalVIPs: []metal3api.KeepalivedIP{
							{IPAddress: "192.168.1.50", Interface: "eth1", Prefix: ptr.To(int32(25))},
						},
					},
				},
			},
			expectedKeepalivedVIPEnv: "192.0.2.2,eth0,24 192.168.1.50,eth1,25",
		},
		{
			name: "keepalived without DHCP, additional VIP with explicit prefix",
			ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					Interface: "eth0",
					IPAddress: "192.0.2.2",
					Keepalived: &metal3api.KeepalivedConfig{
						Enabled: true,
						AdditionalVIPs: []metal3api.KeepalivedIP{
							{IPAddress: "fd00::100", Interface: "eth1", Prefix: ptr.To(int32(64))},
						},
					},
				},
			},
			expectedKeepalivedVIPEnv: "192.0.2.2,eth0 fd00::100,eth1,64",
		},
		{
			name: "keepalived enabled without additional VIPs",
			ironic: metal3api.IronicSpec{
				Networking: metal3api.Networking{
					Interface: "eth0",
					IPAddress: "192.0.2.2",
					Keepalived: &metal3api.KeepalivedConfig{
						Enabled: true,
					},
				},
			},
			expectedKeepalivedVIPEnv: "192.0.2.2,eth0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cctx := ControllerContext{}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: tc.ironic,
			}

			resources := Resources{Ironic: ironic, APISecret: secret}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			var keepalivedContainer *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == "keepalived" {
					keepalivedContainer = &podTemplate.Spec.Containers[i]
					break
				}
			}
			require.NotNil(t, keepalivedContainer, "keepalived container should exist")

			envMap := make(map[string]string)
			for _, env := range keepalivedContainer.Env {
				envMap[env.Name] = env.Value
			}

			if tc.expectedKeepalivedVIPEnv != "" {
				assert.Equal(t, tc.expectedKeepalivedVIPEnv, envMap["KEEPALIVED_VIRTUAL_IPS"])
				assert.Empty(t, envMap["PROVISIONING_IP"], "PROVISIONING_IP should not be set in multi-IP mode")
				assert.Empty(t, envMap["PROVISIONING_INTERFACE"], "PROVISIONING_INTERFACE should not be set in multi-IP mode")
			} else {
				assert.Equal(t, tc.expectedProvisioningIP, envMap["PROVISIONING_IP"])
				assert.Equal(t, tc.expectedProvInterface, envMap["PROVISIONING_INTERFACE"])
				assert.Empty(t, envMap["KEEPALIVED_VIRTUAL_IPS"], "KEEPALIVED_VIRTUAL_IPS should not be set in legacy mode")
			}
		})
	}
}

func TestPrefixToNetmask(t *testing.T) {
	testCases := []struct {
		CIDR     string
		Expected string
	}{
		{"192.168.1.0/24", "255.255.255.0"},
		{"10.0.0.0/16", "255.255.0.0"},
		{"10.0.0.0/8", "255.0.0.0"},
		{"192.168.1.0/32", "255.255.255.255"},
		{"fd69:158d:692a:1::/64", "64"},
	}
	for _, tc := range testCases {
		t.Run(tc.CIDR, func(t *testing.T) {
			prefix, err := netip.ParsePrefix(tc.CIDR)
			require.NoError(t, err)
			assert.Equal(t, tc.Expected, prefixToNetmask(prefix))
		})
	}
}

func TestBuildTrustedCAEnvVarsKeySelection(t *testing.T) {
	// Test that verifies the key selection logic directly
	testCases := []struct {
		name          string
		specifiedKey  string
		availableKeys []string
		expectedKey   string
	}{
		{
			name:          "Specified key exists",
			specifiedKey:  "my-ca.crt",
			availableKeys: []string{"other.crt", "my-ca.crt"},
			expectedKey:   "my-ca.crt",
		},
		{
			name:          "No key specified uses first",
			specifiedKey:  "",
			availableKeys: []string{"ignored.crt", "actual.crt"},
			expectedKey:   "actual.crt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cctx := ControllerContext{
				Logger: logr.Discard(),
			}

			trustedCARef := &metal3api.ResourceReferenceWithKey{
				ResourceReference: metal3api.ResourceReference{
					Name: "test-ca",
					Kind: "ConfigMap",
				},
				Key: tc.specifiedKey,
			}

			data := make(map[string]string)
			for _, key := range tc.availableKeys {
				data[key] = "cert-data"
			}

			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: metal3api.IronicSpec{
					TLS: metal3api.TLS{
						TrustedCA: trustedCARef,
					},
				},
			}

			resources := Resources{
				Ironic: ironic,
				TrustedCAConfigMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ca",
						Namespace: "test",
					},
					Data: data,
				},
			}

			envVars := buildTrustedCAEnvVars(cctx, resources)
			require.Len(t, envVars, 1)

			expectedPath := "/certs/ca/trusted/" + tc.expectedKey
			assert.Equal(t, "WEBSERVER_CACERT_FILE", envVars[0].Name)
			assert.Equal(t, expectedPath, envVars[0].Value)
		})
	}
}

func TestIronicCACertFile(t *testing.T) {
	testCases := []struct {
		name                    string
		tlsSecret               *corev1.Secret
		trustedCAConfigMap      *corev1.ConfigMap
		expectedIronicCACert    string
		expectedWebserverCACert string
	}{
		{
			name:                 "no TLS, no trustedCA",
			expectedIronicCACert: "",
		},
		{
			name: "no TLS, with trustedCA — IRONIC_CACERT_FILE must not be set",
			trustedCAConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: "test"},
				Data:       map[string]string{"ca.crt": "cert"},
			},
			expectedIronicCACert:    "",
			expectedWebserverCACert: "/certs/ca/trusted/ca.crt",
		},
		{
			name: "TLS with ca.crt, no trustedCA — uses ca.crt from TLS secret",
			tlsSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "test"},
				Data: map[string][]byte{
					"tls.crt": []byte("cert"),
					"tls.key": []byte("key"),
					"ca.crt":  []byte("ca"),
				},
			},
			expectedIronicCACert: "/certs/ironic/ca.crt",
		},
		{
			name: "TLS without ca.crt, no trustedCA — leaf fallback",
			tlsSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "test"},
				Data: map[string][]byte{
					"tls.crt": []byte("cert"),
					"tls.key": []byte("key"),
				},
			},
			expectedIronicCACert: "",
		},
		{
			name: "TLS with ca.crt and trustedCA — prefers ca.crt",
			tlsSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "test"},
				Data: map[string][]byte{
					"tls.crt": []byte("cert"),
					"tls.key": []byte("key"),
					"ca.crt":  []byte("ca"),
				},
			},
			trustedCAConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: "test"},
				Data:       map[string]string{"ca-bundle.crt": "cert"},
			},
			expectedIronicCACert:    "/certs/ironic/ca.crt",
			expectedWebserverCACert: "/certs/ca/trusted/ca-bundle.crt",
		},
		{
			name: "TLS without ca.crt, with trustedCA — falls back to trustedCA",
			tlsSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "test"},
				Data: map[string][]byte{
					"tls.crt": []byte("cert"),
					"tls.key": []byte("key"),
				},
			},
			trustedCAConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: "test"},
				Data:       map[string]string{"ca-bundle.crt": "cert"},
			},
			expectedIronicCACert:    "/certs/ca/trusted/ca-bundle.crt",
			expectedWebserverCACert: "/certs/ca/trusted/ca-bundle.crt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cctx := ControllerContext{}
			apiSecret := &corev1.Secret{
				Data: map[string][]byte{"htpasswd": []byte("abcd")},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
				Spec:       metal3api.IronicSpec{},
			}
			if tc.tlsSecret != nil {
				ironic.Spec.TLS.CertificateName = tc.tlsSecret.Name
			}

			resources := Resources{
				Ironic:             ironic,
				APISecret:          apiSecret,
				TLSSecret:          tc.tlsSecret,
				TrustedCAConfigMap: tc.trustedCAConfigMap,
			}

			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			var ironicContainer *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == ironicContainerName {
					ironicContainer = &podTemplate.Spec.Containers[i]
					break
				}
			}
			require.NotNil(t, ironicContainer)

			envMap := make(map[string]string)
			for _, env := range ironicContainer.Env {
				envMap[env.Name] = env.Value
			}

			if tc.expectedIronicCACert != "" {
				assert.Equal(t, tc.expectedIronicCACert, envMap["IRONIC_CACERT_FILE"])
			} else {
				_, found := envMap["IRONIC_CACERT_FILE"]
				assert.False(t, found, "IRONIC_CACERT_FILE should not be set")
			}

			if tc.expectedWebserverCACert != "" {
				assert.Equal(t, tc.expectedWebserverCACert, envMap["WEBSERVER_CACERT_FILE"])
			}
		})
	}
}

func TestNetworkingServiceContainer(t *testing.T) {
	testCases := []struct {
		Scenario                string
		NetworkingService       *metal3api.NetworkingService
		ExpectNetworkingEnabled bool
	}{
		{
			Scenario:                "networking service disabled",
			ExpectNetworkingEnabled: false,
		},
		{
			Scenario: "networking service enabled",
			NetworkingService: &metal3api.NetworkingService{
				Enabled: true,
			},
			ExpectNetworkingEnabled: true,
		},
		{
			Scenario: "networking service with provider networks",
			NetworkingService: &metal3api.NetworkingService{
				Enabled: true,
				ProviderNetworks: []metal3api.ProviderNetworkConfig{
					{
						Type:       "idle",
						Mode:       metal3api.SwitchportModeAccess,
						NativeVLAN: 100,
					},
					{
						Type:       "inspection",
						Mode:       metal3api.SwitchportModeTrunk,
						NativeVLAN: 200,
					},
				},
			},
			ExpectNetworkingEnabled: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			cctx := ControllerContext{Domain: "cluster.local"}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-api-secret"},
				Data: map[string][]byte{
					"htpasswd": []byte("abcd"),
				},
			}
			rpcSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rpc-credentials"},
				Data: map[string][]byte{
					"htpasswd": []byte("efgh"),
				},
			}
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
				Spec: metal3api.IronicSpec{
					NetworkingService: tc.NetworkingService,
				},
			}

			resources := Resources{
				Ironic:    ironic,
				APISecret: secret,
			}
			if tc.NetworkingService != nil && tc.NetworkingService.Enabled {
				resources.RPCSecret = rpcSecret
			}
			podTemplate, err := newIronicPodTemplate(cctx, resources)
			require.NoError(t, err)

			// Verify ironic-networking container does NOT exist in Ironic pod
			// (it now runs in a separate deployment)
			var networkingContainer *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == "ironic-networking" {
					networkingContainer = &podTemplate.Spec.Containers[i]
					break
				}
			}
			assert.Nil(t, networkingContainer, "Networking container should not be in Ironic pod (runs separately)")

			// Verify switch-config volume does NOT exist in Ironic pod
			var switchConfigVolume *corev1.Volume
			for i := range podTemplate.Spec.Volumes {
				if podTemplate.Spec.Volumes[i].Name == "switch-config" {
					switchConfigVolume = &podTemplate.Spec.Volumes[i]
					break
				}
			}
			assert.Nil(t, switchConfigVolume, "switch-config volume should not be in Ironic pod (used by networking pod)")

			// Find the ironic container
			var ironicContainer *corev1.Container
			for i := range podTemplate.Spec.Containers {
				if podTemplate.Spec.Containers[i].Name == "ironic" {
					ironicContainer = &podTemplate.Spec.Containers[i]
					break
				}
			}
			require.NotNil(t, ironicContainer, "Expected ironic container to exist")

			// Build map of ironic container environment variables
			ironicEnvVars := make(map[string]string)
			for _, env := range ironicContainer.Env {
				ironicEnvVars[env.Name] = env.Value
			}

			if tc.ExpectNetworkingEnabled {
				// Verify ironic-auth volume mount at /auth/ironic-rpc is present
				var foundAuthMount bool
				for _, mount := range ironicContainer.VolumeMounts {
					if mount.Name == ironicAuthVolumeName && mount.MountPath == "/auth/ironic-rpc" {
						foundAuthMount = true
						break
					}
				}
				assert.True(t, foundAuthMount, "Expected ironic-auth mount at /auth/ironic-rpc when networking is enabled")

				// The ironic-auth volume must use the RPC credentials secret,
				// not the API credentials secret
				var authVolume *corev1.Volume
				for i := range podTemplate.Spec.Volumes {
					if podTemplate.Spec.Volumes[i].Name == ironicAuthVolumeName {
						authVolume = &podTemplate.Spec.Volumes[i]
						break
					}
				}
				require.NotNil(t, authVolume, "Expected ironic-auth volume when networking is enabled")
				assert.Equal(t, rpcSecret.Name, authVolume.Secret.SecretName)

				// The pod must be restarted when the RPC secret changes
				assert.Contains(t, podTemplate.Annotations, "ironic.metal3.io/rpc-secret-version")

				// Verify that ironic container has networking service configuration
				assert.Contains(t, ironicEnvVars, "IRONIC_NETWORKING_ENABLED")
				assert.Equal(t, "test-networking-service.test.svc.cluster.local", ironicEnvVars["IRONIC_NETWORKING_JSON_RPC_HOST"])
				assert.Equal(t, "6190", ironicEnvVars["IRONIC_NETWORKING_JSON_RPC_PORT"])

				// If provider networks are configured, check those env vars
				for _, pn := range tc.NetworkingService.ProviderNetworks {
					envName := fmt.Sprintf("IRONIC_NETWORKING_%s_NETWORK", strings.ToUpper(string(pn.Type)))
					assert.Contains(t, ironicEnvVars, envName)
				}

				// Check network driver is set on ironic container
				assert.Equal(t, "ironic-networking", ironicEnvVars["IRONIC_DEFAULT_NETWORK_INTERFACE"])
			} else {
				// Networking service disabled - env vars should not be present
				assert.NotContains(t, ironicEnvVars, "IRONIC_NETWORKING_ENABLED")
				assert.NotContains(t, ironicEnvVars, "IRONIC_NETWORKING_JSON_RPC_HOST")

				// No RPC credentials are needed without the networking service
				var authVolume *corev1.Volume
				for i := range podTemplate.Spec.Volumes {
					if podTemplate.Spec.Volumes[i].Name == ironicAuthVolumeName {
						authVolume = &podTemplate.Spec.Volumes[i]
						break
					}
				}
				assert.Nil(t, authVolume, "ironic-auth volume should not exist when the RPC bus is not used")
				assert.NotContains(t, podTemplate.Annotations, "ironic.metal3.io/rpc-secret-version")
			}
		})
	}
}

func TestHighAvailabilityRPCSecret(t *testing.T) {
	apiSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-api-secret"},
		Data: map[string][]byte{
			htpasswdKey: []byte("abcd"),
		},
	}
	rpcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rpc-credentials"},
		Data: map[string][]byte{
			htpasswdKey: []byte("efgh"),
		},
	}

	newIronic := func(tls bool) *metal3api.Ironic {
		spec := metal3api.IronicSpec{HighAvailability: true}
		if tls {
			spec.TLS.CertificateName = "test-tls-cert"
		}
		return &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "test"},
			Spec:       spec,
		}
	}

	findIronicContainer := func(t *testing.T, podTemplate *corev1.PodTemplateSpec) *corev1.Container {
		t.Helper()
		for i := range podTemplate.Spec.Containers {
			if podTemplate.Spec.Containers[i].Name == "ironic" {
				return &podTemplate.Spec.Containers[i]
			}
		}
		require.FailNow(t, "ironic container not found")
		return nil
	}

	findAuthVolume := func(t *testing.T, podTemplate *corev1.PodTemplateSpec) *corev1.Volume {
		t.Helper()
		for i := range podTemplate.Spec.Volumes {
			if podTemplate.Spec.Volumes[i].Name == ironicAuthVolumeName {
				return &podTemplate.Spec.Volumes[i]
			}
		}
		return nil
	}

	t.Run("RPC secret is mounted for JSON RPC", func(t *testing.T) {
		cctx := ControllerContext{}
		resources := Resources{
			Ironic:    newIronic(false),
			APISecret: apiSecret,
			RPCSecret: rpcSecret,
		}
		podTemplate, err := newIronicPodTemplate(cctx, resources)
		require.NoError(t, err)

		authVolume := findAuthVolume(t, &podTemplate)
		require.NotNil(t, authVolume, "Expected ironic-auth volume in the HA mode")
		assert.Equal(t, rpcSecret.Name, authVolume.Secret.SecretName,
			"The RPC bus must use the RPC credentials secret, not the API secret")

		ironicContainer := findIronicContainer(t, &podTemplate)
		var foundAuthMount bool
		for _, mount := range ironicContainer.VolumeMounts {
			if mount.Name == ironicAuthVolumeName && mount.MountPath == "/auth/ironic-rpc" {
				foundAuthMount = true
				break
			}
		}
		assert.True(t, foundAuthMount, "Expected ironic-auth mount at /auth/ironic-rpc in the HA mode")

		// Without TLS the API is served by the ironic container itself, so
		// the API secret is still needed for IRONIC_HTPASSWD.
		var htpasswdEnv *corev1.EnvVar
		for i := range ironicContainer.Env {
			if ironicContainer.Env[i].Name == "IRONIC_HTPASSWD" {
				htpasswdEnv = &ironicContainer.Env[i]
				break
			}
		}
		require.NotNil(t, htpasswdEnv)
		require.NotNil(t, htpasswdEnv.ValueFrom.SecretKeyRef)
		assert.Equal(t, apiSecret.Name, htpasswdEnv.ValueFrom.SecretKeyRef.Name)
	})

	t.Run("API htpasswd is not passed to Ironic with TLS", func(t *testing.T) {
		cctx := ControllerContext{}
		resources := Resources{
			Ironic:    newIronic(true),
			APISecret: apiSecret,
			RPCSecret: rpcSecret,
			TLSSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-tls-cert"},
				Data: map[string][]byte{
					"ca.crt": []byte("fake-ca"),
				},
			},
		}
		podTemplate, err := newIronicPodTemplate(cctx, resources)
		require.NoError(t, err)

		authVolume := findAuthVolume(t, &podTemplate)
		require.NotNil(t, authVolume, "Expected ironic-auth volume in the HA mode")
		assert.Equal(t, rpcSecret.Name, authVolume.Secret.SecretName)

		ironicContainer := findIronicContainer(t, &podTemplate)
		for _, env := range ironicContainer.Env {
			assert.NotEqual(t, "IRONIC_HTPASSWD", env.Name,
				"With TLS the API authentication is handled by httpd, the RPC secret is mounted separately")
		}
	})

	t.Run("missing RPC secret is an error", func(t *testing.T) {
		cctx := ControllerContext{}
		resources := Resources{
			Ironic:    newIronic(false),
			APISecret: apiSecret,
		}
		_, err := newIronicPodTemplate(cctx, resources)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "RPC credentials secret")
	})
}

func TestFormatProviderNetwork(t *testing.T) {
	testCases := []struct {
		Scenario string
		Config   *metal3api.ProviderNetworkConfig
		Expected string
	}{
		{
			Scenario: "access mode",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:       metal3api.SwitchportModeAccess,
				NativeVLAN: 100,
			},
			Expected: "access/native_vlan=100",
		},
		{
			Scenario: "trunk mode without allowedVLANs",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:       metal3api.SwitchportModeTrunk,
				NativeVLAN: 200,
			},
			Expected: "trunk/native_vlan=200",
		},
		{
			Scenario: "trunk mode with allowedVLANs",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:         metal3api.SwitchportModeTrunk,
				NativeVLAN:   100,
				AllowedVLANs: []string{"200", "300", "400"},
			},
			Expected: "trunk/native_vlan=100/allowed_vlans=200,300,400",
		},
		{
			Scenario: "trunk mode with VLAN ranges",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:         metal3api.SwitchportModeTrunk,
				NativeVLAN:   100,
				AllowedVLANs: []string{"200-210", "300", "400-500"},
			},
			Expected: "trunk/native_vlan=100/allowed_vlans=200-210,300,400-500",
		},
		{
			Scenario: "hybrid mode with allowedVLANs",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:         metal3api.SwitchportModeHybrid,
				NativeVLAN:   50,
				AllowedVLANs: []string{"10"},
			},
			Expected: "hybrid/native_vlan=50/allowed_vlans=10",
		},
		{
			Scenario: "access mode ignores allowedVLANs",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:         metal3api.SwitchportModeAccess,
				NativeVLAN:   100,
				AllowedVLANs: []string{"200", "300"},
			},
			Expected: "access/native_vlan=100",
		},
		{
			Scenario: "access mode with MTU",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:       metal3api.SwitchportModeAccess,
				NativeVLAN: 100,
				MTU:        1500,
			},
			Expected: "access/native_vlan=100/mtu=1500",
		},
		{
			Scenario: "trunk mode with allowedVLANs and MTU",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:         metal3api.SwitchportModeTrunk,
				NativeVLAN:   100,
				AllowedVLANs: []string{"200-210"},
				MTU:          9000,
			},
			Expected: "trunk/native_vlan=100/allowed_vlans=200-210/mtu=9000",
		},
		{
			Scenario: "MTU zero is omitted",
			Config: &metal3api.ProviderNetworkConfig{
				Mode:       metal3api.SwitchportModeAccess,
				NativeVLAN: 100,
				MTU:        0,
			},
			Expected: "access/native_vlan=100",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			result := formatProviderNetwork(tc.Config)
			assert.Equal(t, tc.Expected, result)
		})
	}
}

func TestIsAuthVolumeRequired(t *testing.T) {
	testCases := []struct {
		Scenario string
		Ironic   *metal3api.Ironic
		Expected bool
	}{
		{
			Scenario: "standard (no HA, no networking)",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{},
			},
			Expected: false,
		},
		{
			Scenario: "HA enabled",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					HighAvailability: true,
				},
			},
			Expected: true,
		},
		{
			Scenario: "networking service enabled",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
				},
			},
			Expected: true,
		},
		{
			Scenario: "networking service disabled",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: &metal3api.NetworkingService{
						Enabled: false,
					},
				},
			},
			Expected: false,
		},
		{
			Scenario: "networking service nil",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					NetworkingService: nil,
				},
			},
			Expected: false,
		},
		{
			Scenario: "both HA and networking enabled",
			Ironic: &metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					HighAvailability: true,
					NetworkingService: &metal3api.NetworkingService{
						Enabled: true,
					},
				},
			},
			Expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			resources := Resources{
				Ironic: tc.Ironic,
			}
			result := isAuthVolumeRequired(resources)
			assert.Equal(t, tc.Expected, result)
		})
	}
}

func TestBuildDHCPRange(t *testing.T) {
	testCases := []struct {
		Scenario string
		DHCP     metal3api.DHCP
		Expected string
	}{
		{
			Scenario: "main range only renders an explicit netmask",
			DHCP: metal3api.DHCP{
				NetworkCIDR: "192.168.1.0/24",
				RangeBegin:  "192.168.1.10",
				RangeEnd:    "192.168.1.200",
			},
			Expected: "192.168.1.10,192.168.1.200,255.255.255.0",
		},
		{
			Scenario: "IPv6 main range only renders a prefix length",
			DHCP: metal3api.DHCP{
				NetworkCIDR: "fd69:158d:692a::/64",
				RangeBegin:  "fd69:158d:692a::3000",
				RangeEnd:    "fd69:158d:692a::3fff",
			},
			Expected: "fd69:158d:692a::3000,fd69:158d:692a::3fff,64",
		},
		{
			Scenario: "two IPv4 extra ranges render as auto-tagged set: entries",
			DHCP: metal3api.DHCP{
				NetworkCIDR: "172.16.0.0/24",
				RangeBegin:  "172.16.0.10",
				RangeEnd:    "172.16.0.100",
				ExtraRanges: []metal3api.DHCPRange{
					{NetworkCIDR: "10.0.0.0/24", RangeBegin: "10.0.0.10", RangeEnd: "10.0.0.100", GatewayAddress: "10.0.0.1"},
					{NetworkCIDR: "192.168.1.0/24", RangeBegin: "192.168.1.10", RangeEnd: "192.168.1.200", GatewayAddress: "192.168.1.1"},
				},
			},
			Expected: "172.16.0.10,172.16.0.100,255.255.255.0;set:range_1,10.0.0.10,10.0.0.100,255.255.255.0;set:range_2,192.168.1.10,192.168.1.200,255.255.255.0",
		},
		{
			Scenario: "IPv6 extra ranges render with prefix length and no gateway options",
			DHCP: metal3api.DHCP{
				NetworkCIDR: "fd69:158d:692a::/64",
				RangeBegin:  "fd69:158d:692a::3000",
				RangeEnd:    "fd69:158d:692a::3fff",
				ExtraRanges: []metal3api.DHCPRange{
					{NetworkCIDR: "fd69:158d:692a:1::/64", RangeBegin: "fd69:158d:692a:1::3000", RangeEnd: "fd69:158d:692a:1::3fff"},
					{NetworkCIDR: "fd69:158d:692a:2::/64", RangeBegin: "fd69:158d:692a:2::3000", RangeEnd: "fd69:158d:692a:2::3fff"},
				},
			},
			Expected: "fd69:158d:692a::3000,fd69:158d:692a::3fff,64;set:range_1,fd69:158d:692a:1::3000,fd69:158d:692a:1::3fff,64;set:range_2,fd69:158d:692a:2::3000,fd69:158d:692a:2::3fff,64",
		},
		{
			Scenario: "main range plus one extra range concatenate",
			DHCP: metal3api.DHCP{
				NetworkCIDR: "10.0.0.0/16",
				RangeBegin:  "10.0.1.1",
				RangeEnd:    "10.0.1.254",
				ExtraRanges: []metal3api.DHCPRange{
					{NetworkCIDR: "192.168.1.0/24", RangeBegin: "192.168.1.10", RangeEnd: "192.168.1.200"},
				},
			},
			Expected: "10.0.1.1,10.0.1.254,255.255.0.0;set:range_1,192.168.1.10,192.168.1.200,255.255.255.0",
		},
		{
			Scenario: "relay-only: extra ranges without a main range",
			DHCP: metal3api.DHCP{
				ExtraRanges: []metal3api.DHCPRange{
					{NetworkCIDR: "10.0.0.0/24", RangeBegin: "10.0.0.10", RangeEnd: "10.0.0.100"},
					{NetworkCIDR: "192.168.1.0/24", RangeBegin: "192.168.1.10", RangeEnd: "192.168.1.200"},
				},
			},
			Expected: "set:range_1,10.0.0.10,10.0.0.100,255.255.255.0;set:range_2,192.168.1.10,192.168.1.200,255.255.255.0",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			assert.Equal(t, tc.Expected, buildDHCPRange(&tc.DHCP))
		})
	}
}

func TestBuildDHCPOptions(t *testing.T) {
	testCases := []struct {
		Scenario string
		DHCP     metal3api.DHCP
		Expected string
	}{
		{
			Scenario: "main-range-only DHCP emits no per-range options",
			DHCP:     metal3api.DHCP{GatewayAddress: "10.0.0.1"},
			Expected: "",
		},
		{
			Scenario: "two extra ranges with gateways",
			DHCP: metal3api.DHCP{
				ExtraRanges: []metal3api.DHCPRange{
					{NetworkCIDR: "10.0.0.0/24", RangeBegin: "10.0.0.10", RangeEnd: "10.0.0.100", GatewayAddress: "10.0.0.1"},
					{NetworkCIDR: "192.168.1.0/24", RangeBegin: "192.168.1.10", RangeEnd: "192.168.1.200", GatewayAddress: "192.168.1.1"},
				},
			},
			Expected: "tag:range_1,option:router,10.0.0.1;tag:range_2,option:router,192.168.1.1",
		},
		{
			Scenario: "extra range without gateway emits nothing when the main gateway is unset",
			DHCP: metal3api.DHCP{
				ExtraRanges: []metal3api.DHCPRange{
					{NetworkCIDR: "10.0.0.0/24", RangeBegin: "10.0.0.10", RangeEnd: "10.0.0.100", GatewayAddress: "10.0.0.1"},
					{NetworkCIDR: "192.168.1.0/24", RangeBegin: "192.168.1.10", RangeEnd: "192.168.1.200"},
				},
			},
			Expected: "tag:range_1,option:router,10.0.0.1",
		},
		{
			Scenario: "extra range without gateway suppresses the main router option",
			DHCP: metal3api.DHCP{
				GatewayAddress: "172.16.0.1",
				ExtraRanges: []metal3api.DHCPRange{
					{NetworkCIDR: "10.0.0.0/24", RangeBegin: "10.0.0.10", RangeEnd: "10.0.0.100", GatewayAddress: "10.0.0.1"},
					{NetworkCIDR: "192.168.1.0/24", RangeBegin: "192.168.1.10", RangeEnd: "192.168.1.200"},
				},
			},
			Expected: "tag:range_1,option:router,10.0.0.1;tag:range_2,option:router",
		},
		{
			Scenario: "IPv6 extra range needs no router suppression",
			DHCP: metal3api.DHCP{
				GatewayAddress: "172.16.0.1",
				ExtraRanges: []metal3api.DHCPRange{
					{NetworkCIDR: "fd69:158d:692a:1::/64", RangeBegin: "fd69:158d:692a:1::3000", RangeEnd: "fd69:158d:692a:1::3fff"},
				},
			},
			Expected: "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			assert.Equal(t, tc.Expected, buildDHCPOptions(&tc.DHCP))
		})
	}
}

// TestGatewayIPWithExtraRanges asserts that the main-range GATEWAY_IP is
// passed through unchanged when extra ranges (with their own per-range
// gateways) are also configured.
func TestGatewayIPWithExtraRanges(t *testing.T) {
	ironic := &metal3api.Ironic{
		Spec: metal3api.IronicSpec{
			Networking: metal3api.Networking{
				Interface: "eth0",
				IPAddress: "10.0.0.5",
				DHCP: &metal3api.DHCP{
					NetworkCIDR:    "10.0.0.0/24",
					RangeBegin:     "10.0.0.10",
					RangeEnd:       "10.0.0.100",
					GatewayAddress: "10.0.0.1",
					ExtraRanges: []metal3api.DHCPRange{
						{NetworkCIDR: "192.168.1.0/24", RangeBegin: "192.168.1.10", RangeEnd: "192.168.1.200", GatewayAddress: "192.168.1.1"},
					},
				},
			},
		},
	}
	c := newDnsmasqContainer(VersionInfo{}, ironic, corev1.VolumeMount{})
	gotEnv := ""
	for _, e := range c.Env {
		if e.Name == "GATEWAY_IP" {
			gotEnv = e.Value
			break
		}
	}
	assert.Equal(t, "10.0.0.1", gotEnv)
}
