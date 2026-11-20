package ironic

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
)

const (
	ironicPortName              = "ironic-api"
	imagesPortName              = "image-svc"
	imagesTLSPortName           = "image-svc-tls"
	metricsPortName             = "metrics"
	ironicNetworkingPortName    = "networking-rpc"
	ironicNetworkingRPCPort     = 6190
	defaultNetworkInterfaceName = "ironic-networking"

	ironicUser      int64 = 997
	ironicGroup     int64 = 994
	keepalivedUser  int64 = 65532
	keepalivedGroup int64 = 65532

	authDir   = "/auth"
	certsDir  = "/certs"
	sharedDir = "/shared"

	metricsPort            = 9608
	defaultMetricsBindAddr = "0.0.0.0"

	knownExistingPath = "/images/ironic-python-agent.kernel"

	trustedCAVolumeName = "trusted-ca"
	bmcCAVolumeName     = "cert-bmc"

	ironicAuthVolumeName = "ironic-auth"
)

func buildCommonEnvVars(ironic *metal3api.Ironic) []corev1.EnvVar {
	result := []corev1.EnvVar{
		{
			Name:  "IRONIC_LISTEN_PORT",
			Value: strconv.Itoa(int(ironic.Spec.Networking.APIPort)),
		},
		{
			Name:  "IRONIC_ACCESS_PORT",
			Value: strconv.Itoa(int(ironic.Spec.Networking.APIPort)),
		},
		{
			Name:  "HTTP_PORT",
			Value: strconv.Itoa(int(ironic.Spec.Networking.ImageServerPort)),
		},
		{
			Name:  "LISTEN_ALL_INTERFACES",
			Value: strconv.FormatBool(!ironic.Spec.Networking.BindInterface),
		},
	}

	networkingProvided := false
	if ironic.Spec.Networking.IPAddress != "" {
		result = append(result,
			corev1.EnvVar{
				Name:  "PROVISIONING_IP",
				Value: ironic.Spec.Networking.IPAddress,
			},
		)
		networkingProvided = true
	}
	if ironic.Spec.Networking.Interface != "" {
		result = append(result,
			corev1.EnvVar{
				Name:  "PROVISIONING_INTERFACE",
				Value: ironic.Spec.Networking.Interface,
			},
		)
		networkingProvided = true
	}
	if len(ironic.Spec.Networking.MACAddresses) > 0 {
		result = append(result,
			corev1.EnvVar{
				Name:  "PROVISIONING_MACS",
				Value: strings.Join(ironic.Spec.Networking.MACAddresses, ","),
			},
		)
		networkingProvided = true
	}
	if !networkingProvided {
		fieldPath := "status.hostIP"
		if ironic.Spec.Networking.DisableHostNetwork {
			fieldPath = "status.podIP"
		}
		result = append(result,
			corev1.EnvVar{
				Name: "PROVISIONING_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  fieldPath,
					},
				},
			},
		)
	}

	if ironic.Spec.TLS.CertificateName != "" {
		// Ironic will listen on a Unix socket, httpd will be responsible for serving HTTPS.
		result = append(result, []corev1.EnvVar{
			{
				Name:  "IRONIC_PRIVATE_PORT",
				Value: "unix",
			},
			{
				Name:  "IRONIC_REVERSE_PROXY_SETUP",
				Value: "true",
			},
		}...)

		if !ironic.Spec.TLS.DisableVirtualMediaTLS {
			result = append(result,
				corev1.EnvVar{
					Name:  "VMEDIA_TLS_PORT",
					Value: strconv.Itoa(int(ironic.Spec.Networking.ImageServerTLSPort)),
				},
			)
		}
	}

	result = appendStringEnv(result,
		"IRONIC_KERNEL_PARAMS", strings.TrimSpace(ironic.Spec.DeployRamdisk.ExtraKernelParams))

	result = appendStringEnv(result,
		"IRONIC_RAMDISK_SSH_KEY", strings.TrimSpace(ironic.Spec.DeployRamdisk.SSHKey))

	result = appendListOfStringsEnv(result,
		"IRONIC_IPA_COLLECTORS", ironic.Spec.Inspection.Collectors, ",")

	result = appendListOfStringsEnv(result,
		"IRONIC_ENABLE_VLAN_INTERFACES", ironic.Spec.Inspection.VLANInterfaces, ",")

	if ironic.Spec.Overrides != nil {
		result = appendAgentImageEnvVars(result, ironic.Spec.Overrides.AgentImages)
	}

	return result
}

// appendAgentImageEnvVars renders AgentImages into DEPLOY_KERNEL_URL/DEPLOY_RAMDISK_URL
// and DEPLOY_KERNEL_BY_ARCH/DEPLOY_RAMDISK_BY_ARCH env vars.
// Validation is handled by webhook (see validation.go).
func appendAgentImageEnvVars(envVars []corev1.EnvVar, images []metal3api.AgentImages) []corev1.EnvVar {
	if len(images) == 0 {
		return envVars
	}

	kernelByArch := make([]string, 0, len(images))
	ramdiskByArch := make([]string, 0, len(images))

	for _, img := range images {
		if img.Architecture == "" {
			envVars = appendStringEnv(envVars, "DEPLOY_KERNEL_URL", strings.TrimSpace(img.Kernel))
			envVars = appendStringEnv(envVars, "DEPLOY_RAMDISK_URL", strings.TrimSpace(img.Initramfs))
		} else {
			arch := string(img.Architecture)
			kernelByArch = append(kernelByArch, arch+":"+strings.TrimSpace(img.Kernel))
			ramdiskByArch = append(ramdiskByArch, arch+":"+strings.TrimSpace(img.Initramfs))
		}
	}

	envVars = appendStringEnv(envVars, "DEPLOY_KERNEL_BY_ARCH", strings.Join(kernelByArch, ","))
	envVars = appendStringEnv(envVars, "DEPLOY_RAMDISK_BY_ARCH", strings.Join(ramdiskByArch, ","))
	return envVars
}

func buildExtraConfigVars(ironic *metal3api.Ironic) []corev1.EnvVar {
	var result []corev1.EnvVar

	for _, extraConfig := range ironic.Spec.ExtraConfig {
		// Default group value
		group := "DEFAULT"
		if extraConfig.Group != "" {
			group = extraConfig.Group
		}

		if extraConfig.Name != "" && extraConfig.Value != "" {
			name := extraConfig.Name
			value := extraConfig.Value

			result = append(result,
				corev1.EnvVar{
					Name:  fmt.Sprintf("OS_%s__%s", strings.ToUpper(group), strings.ToUpper(name)),
					Value: value,
				})
		}
	}

	return result
}

func databaseClientEnvVars(db *metal3api.Database) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{
			Name:  "MARIADB_HOST",
			Value: db.Host,
		},
		{
			Name:  "MARIADB_DATABASE",
			Value: db.Name,
		},
	}

	return envVars
}

// resolveTrustedCACertPath returns the in-container path to the trusted CA
// bundle file selected from resources.TrustedCASecret/TrustedCAConfigMap,
// honoring an explicitly requested key (spec.tls.trustedCA.key) and falling
// back to the first key (sorted) otherwise. Returns "" if no trusted CA is
// configured or the resource has no data.
func resolveTrustedCACertPath(cctx ControllerContext, resources Resources) string {
	var keys []string
	var resourceName string
	var namespace string
	var resourceKind string

	switch {
	case resources.TrustedCASecret != nil:
		keys = slices.Sorted(maps.Keys(resources.TrustedCASecret.Data))
		resourceName = resources.TrustedCASecret.Name
		namespace = resources.TrustedCASecret.Namespace
		resourceKind = metal3api.ResourceKindSecret
	case resources.TrustedCAConfigMap != nil:
		keys = slices.Sorted(maps.Keys(resources.TrustedCAConfigMap.Data))
		resourceName = resources.TrustedCAConfigMap.Name
		namespace = resources.TrustedCAConfigMap.Namespace
		resourceKind = metal3api.ResourceKindConfigMap
	default:
		return ""
	}

	if len(keys) == 0 {
		return ""
	}

	// Get the TrustedCA reference to check if a specific key was requested
	var requestedKey string
	if resources.Ironic.Spec.TLS.TrustedCA != nil {
		requestedKey = resources.Ironic.Spec.TLS.TrustedCA.Key
	}

	// resources.Validate checks that the key exists if provided
	selectedKey := requestedKey

	// If no key was specified or the specified key doesn't exist, select the first key
	if selectedKey == "" {
		first := true
		for _, key := range keys {
			if first {
				selectedKey = key
				first = false
			} else {
				cctx.Logger.Info("ignoring duplicate key in Trusted CA",
					"key", key, "resourceKind", resourceKind, "resource", namespace+"/"+resourceName)
			}
		}
	}

	// Build the path to the CA bundle file
	return fmt.Sprintf("%s/ca/trusted/%s", certsDir, selectedKey)
}

func buildTrustedCAEnvVars(cctx ControllerContext, resources Resources) []corev1.EnvVar {
	caPath := resolveTrustedCACertPath(cctx, resources)
	if caPath == "" {
		return nil
	}

	return []corev1.EnvVar{
		{
			Name:  "WEBSERVER_CACERT_FILE",
			Value: caPath,
		},
	}
}

func buildIronicEnvVars(cctx ControllerContext, resources Resources) []corev1.EnvVar {
	result := buildCommonEnvVars(resources.Ironic)
	result = append(result, []corev1.EnvVar{
		{
			Name:  "IRONIC_USE_MARIADB",
			Value: strconv.FormatBool(resources.Ironic.Spec.Database != nil),
		},
		{
			Name:  "IRONIC_EXPOSE_JSON_RPC",
			Value: strconv.FormatBool(resources.Ironic.Spec.HighAvailability),
		},
		{
			Name:  "IRONIC_JSON_RPC_PORT",
			Value: strconv.Itoa(int(resources.Ironic.Spec.Networking.RPCPort)),
		},
	}...)

	if resources.Ironic.Spec.Database != nil {
		result = append(result, databaseClientEnvVars(resources.Ironic.Spec.Database)...)
		// NOTE(dtantsur): upgrades are handled by a separate job
		result = append(result, corev1.EnvVar{
			Name:  "IRONIC_SKIP_DBSYNC",
			Value: "true",
		})
	}

	if resources.Ironic.Spec.HighAvailability {
		result = append(result, []corev1.EnvVar{
			// NOTE(dtantsur): this is not strictly correct but is required for JSON RPC authentication
			{
				Name:  "IRONIC_DEPLOYMENT",
				Value: "Conductor",
			},
		}...)
		if insecureRPC := resources.Ironic.Spec.TLS.InsecureRPC; insecureRPC != nil && *insecureRPC {
			result = append(result, corev1.EnvVar{
				Name:  "OS_JSON_RPC__INSECURE",
				Value: "true",
			})
		}
	}

	// When TLS is not used, the Ironic container serves the API itself and
	// needs the API credentials for HTTP basic authentication. The JSON RPC
	// bus authentication is handled separately through /auth/ironic-rpc.
	if resources.TLSSecret == nil {
		result = append(result,
			corev1.EnvVar{
				Name: "IRONIC_HTPASSWD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: resources.APISecret.Name,
						},
						Key: htpasswdKey,
					},
				},
			},
		)
	}

	// Add networking service integration if enabled
	if resources.Ironic.IsNetworkingServiceEnabled() {
		result = append(result, []corev1.EnvVar{
			{
				Name:  "IRONIC_NETWORKING_ENABLED",
				Value: "true",
			},
			{
				// Hosts may have static network data for their final deployed
				// state that is not valid on the inspection VLAN; force DHCP
				// so inspection always works regardless of provisioning config.
				Name:  "IRONIC_FORCE_DHCP",
				Value: "true",
			},
			{
				Name:  "IRONIC_NETWORKING_JSON_RPC_HOST",
				Value: networkingServiceEndpoint(resources.Ironic, cctx.Domain),
			},
			{
				Name:  "IRONIC_NETWORKING_JSON_RPC_PORT",
				Value: strconv.Itoa(ironicNetworkingRPCPort),
			},
		}...)

		// Set provider network configuration
		for _, pn := range resources.Ironic.Spec.NetworkingService.ProviderNetworks {
			envName := fmt.Sprintf("IRONIC_NETWORKING_%s_NETWORK", strings.ToUpper(string(pn.Type)))
			result = append(result, corev1.EnvVar{
				Name:  envName,
				Value: formatProviderNetwork(&pn),
			})
		}

		result = append(result, corev1.EnvVar{
			Name:  "IRONIC_DEFAULT_NETWORK_INTERFACE",
			Value: defaultNetworkInterfaceName,
		})
	}

	if resources.TrustedCAConfigMap != nil || resources.TrustedCASecret != nil {
		trustedCAEnvVars := buildTrustedCAEnvVars(cctx, resources)
		result = append(result, trustedCAEnvVars...)

		// When TLS is enabled, set IRONIC_CACERT_FILE for JSON-RPC TLS verification.
		// Prefer the CA from the TLS secret (ca.crt, populated by cert-manager);
		// fall back to the trusted CA bundle if the TLS secret lacks ca.crt.
		if resources.TLSSecret != nil {
			if _, hasCACert := resources.TLSSecret.Data["ca.crt"]; hasCACert {
				result = append(result, corev1.EnvVar{
					Name:  "IRONIC_CACERT_FILE",
					Value: certsDir + "/ironic/ca.crt",
				})
			} else if len(trustedCAEnvVars) > 0 {
				result = append(result, corev1.EnvVar{
					Name:  "IRONIC_CACERT_FILE",
					Value: trustedCAEnvVars[0].Value,
				})
			}
		}
	} else if resources.TLSSecret != nil {
		if _, hasCACert := resources.TLSSecret.Data["ca.crt"]; hasCACert {
			result = append(result, corev1.EnvVar{
				Name:  "IRONIC_CACERT_FILE",
				Value: certsDir + "/ironic/ca.crt",
			})
		}
	}

	if resources.Ironic.Spec.ExtraConfig != nil {
		result = append(result, buildExtraConfigVars(resources.Ironic)...)
	}

	result = appendStringEnv(result, "IRONIC_EXTERNAL_IP", resources.Ironic.Spec.Networking.ExternalIP)

	var externalCallbackURL, imageServerExternalURL string
	if resources.Ironic.Spec.Networking.Ingress != nil {
		externalCallbackURL = "https://" + resources.Ironic.Spec.Networking.Ingress.Host
		imageServerExternalURL = "https://" + resources.Ironic.Spec.Networking.Ingress.Host
		// allow overrides of the external URLs when ingress is used
		if resources.Ironic.Spec.Networking.ExternalCallbackURL != "" {
			externalCallbackURL = resources.Ironic.Spec.Networking.ExternalCallbackURL
		}
		if resources.Ironic.Spec.Networking.ImageServerExternalURL != "" {
			imageServerExternalURL = resources.Ironic.Spec.Networking.ImageServerExternalURL
		}
	} else if resources.Ironic.Spec.Networking.ExternalCallbackURL != "" && resources.Ironic.Spec.Networking.ImageServerExternalURL != "" {
		// only set the external URLs when both are provided, to avoid misconfiguration
		externalCallbackURL = resources.Ironic.Spec.Networking.ExternalCallbackURL
		imageServerExternalURL = resources.Ironic.Spec.Networking.ImageServerExternalURL
	}
	if externalCallbackURL != "" && imageServerExternalURL != "" {
		result = appendStringEnv(result, "IRONIC_EXTERNAL_CALLBACK_URL", externalCallbackURL)
		result = appendStringEnv(result, "IRONIC_EXTERNAL_HTTP_URL", imageServerExternalURL)
	}

	// Add sensor data environment variables when PrometheusExporter is enabled
	if resources.Ironic.Spec.PrometheusExporter != nil && resources.Ironic.Spec.PrometheusExporter.Enabled {
		result = append(result, corev1.EnvVar{
			Name:  "SEND_SENSOR_DATA",
			Value: "true",
		})
		sensorInterval := resources.Ironic.Spec.PrometheusExporter.SensorCollectionInterval
		if sensorInterval == 0 {
			sensorInterval = 60 // default
		}
		result = append(result, corev1.EnvVar{
			Name:  "OS_SENSOR_DATA__INTERVAL",
			Value: strconv.Itoa(sensorInterval),
		})
	}

	return result
}

// formatProviderNetwork formats a provider network config for Ironic.
// Format is "mode/native_vlan=VALUE[/allowed_vlans=VLAN_LIST][/mtu=VALUE]".
func formatProviderNetwork(pn *metal3api.ProviderNetworkConfig) string {
	val := fmt.Sprintf("%s/native_vlan=%d", pn.Mode, pn.NativeVLAN)
	if (pn.Mode == metal3api.SwitchportModeTrunk || pn.Mode == metal3api.SwitchportModeHybrid) && len(pn.AllowedVLANs) > 0 {
		val += "/allowed_vlans=" + strings.Join(pn.AllowedVLANs, ",")
	}
	if pn.MTU > 0 {
		val += fmt.Sprintf("/mtu=%d", pn.MTU)
	}
	return val
}

func buildHttpdEnvVars(resources Resources) []corev1.EnvVar {
	result := buildCommonEnvVars(resources.Ironic)

	// When TLS is used, httpd is responsible for authentication
	if resources.TLSSecret != nil {
		result = append(result,
			corev1.EnvVar{
				Name: "IRONIC_HTPASSWD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: resources.APISecret.Name,
						},
						Key: htpasswdKey,
					},
				},
			},
		)
	}

	return result
}

func databaseClientMounts(db *metal3api.Database) (volumes []corev1.Volume, mounts []corev1.VolumeMount) {
	volumes = append(volumes, corev1.Volume{
		Name: "auth-mariadb",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  db.CredentialsName,
				DefaultMode: ptr.To(corev1.SecretVolumeSourceDefaultMode),
			},
		},
	})
	mounts = append(mounts, corev1.VolumeMount{
		Name:      "auth-mariadb",
		MountPath: authDir + "/mariadb",
	})

	if db.TLSCertificateName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "cert-mariadb",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  db.TLSCertificateName,
					DefaultMode: ptr.To(corev1.SecretVolumeSourceDefaultMode),
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "cert-mariadb",
			MountPath: certsDir + "/ca/mariadb",
		})
	}

	return volumes, mounts
}

// isAuthVolumeRequired returns true when the ironic-rpc auth mount is needed.
// HA requires it for inter-conductor RPC; networking service requires it for
// authenticated JSON-RPC between Ironic and the networking service.
func isAuthVolumeRequired(resources Resources) bool {
	return resources.Ironic.Spec.HighAvailability || resources.Ironic.IsNetworkingServiceEnabled()
}

func buildIronicVolumesAndMounts(resources Resources) (volumes []corev1.Volume, mounts []corev1.VolumeMount) {
	volumes = []corev1.Volume{
		{
			Name: "ironic-shared",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}
	mounts = []corev1.VolumeMount{
		{
			Name:      "ironic-shared",
			MountPath: sharedDir,
		},
	}
	if isAuthVolumeRequired(resources) {
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
	}

	if resources.TLSSecret != nil {
		volumes = append(volumes,
			corev1.Volume{
				Name: "cert-ironic",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  resources.TLSSecret.Name,
						DefaultMode: ptr.To(corev1.SecretVolumeSourceDefaultMode),
					},
				},
			},
		)
		mounts = append(mounts,
			corev1.VolumeMount{
				Name:      "cert-ironic",
				MountPath: certsDir + "/ironic",
				ReadOnly:  true,
			},
		)
		if !resources.Ironic.Spec.TLS.DisableVirtualMediaTLS {
			mounts = append(mounts,
				corev1.VolumeMount{
					Name:      "cert-ironic",
					MountPath: certsDir + "/vmedia",
					ReadOnly:  true,
				},
			)
		}
	}

	if maybeVolume := volumeForSecretOrConfigMap(bmcCAVolumeName, resources.BMCCASecret, resources.BMCCAConfigMap); maybeVolume != nil {
		volumes = append(volumes, *maybeVolume)
		mounts = append(mounts,
			corev1.VolumeMount{
				Name:      bmcCAVolumeName,
				MountPath: certsDir + "/ca/bmc",
				ReadOnly:  true,
			},
		)
	}

	if maybeVolume := volumeForSecretOrConfigMap(trustedCAVolumeName, resources.TrustedCASecret, resources.TrustedCAConfigMap); maybeVolume != nil {
		volumes = append(volumes, *maybeVolume)
		mounts = append(mounts,
			corev1.VolumeMount{
				Name:      trustedCAVolumeName,
				MountPath: certsDir + "/ca/trusted",
				ReadOnly:  true,
			},
		)
	}

	if resources.Ironic.Spec.Database != nil {
		dbVolumes, dbMounts := databaseClientMounts(resources.Ironic.Spec.Database)
		volumes = append(volumes, dbVolumes...)
		mounts = append(mounts, dbMounts...)
	}

	return volumes, mounts
}

func containerPort(name string, port int32, disableHostNetwork bool) corev1.ContainerPort {
	result := corev1.ContainerPort{
		Name:          name,
		Protocol:      corev1.ProtocolTCP,
		ContainerPort: port,
	}
	if !disableHostNetwork {
		result.HostPort = port
	}
	return result
}

func buildIronicHttpdPorts(ironic *metal3api.Ironic) (ironicPorts []corev1.ContainerPort, httpdPorts []corev1.ContainerPort) {
	httpdPorts = []corev1.ContainerPort{
		containerPort(imagesPortName, ironic.Spec.Networking.ImageServerPort,
			ironic.Spec.Networking.DisableHostNetwork),
	}

	apiPort := containerPort(ironicPortName, ironic.Spec.Networking.APIPort,
		ironic.Spec.Networking.DisableHostNetwork)

	if ironic.Spec.TLS.CertificateName == "" {
		ironicPorts = append(ironicPorts, apiPort)
	} else {
		httpdPorts = append(httpdPorts, apiPort)
		if !ironic.Spec.TLS.DisableVirtualMediaTLS {
			httpdPorts = append(httpdPorts, containerPort(
				imagesTLSPortName,
				ironic.Spec.Networking.ImageServerTLSPort,
				ironic.Spec.Networking.DisableHostNetwork),
			)
		}
	}

	return ironicPorts, httpdPorts
}

// rangeTag returns the auto-generated dnsmasq tag for an extra range: its
// 1-based position in the extraRanges list.
func rangeTag(idx int) string {
	return fmt.Sprintf("range_%d", idx+1)
}

func buildDHCPRange(dhcp *metal3api.DHCP) string {
	var parts []string

	if dhcp.NetworkCIDR != "" && dhcp.RangeBegin != "" && dhcp.RangeEnd != "" {
		if prefix, err := netip.ParsePrefix(dhcp.NetworkCIDR); err == nil {
			parts = append(parts, fmt.Sprintf("%s,%s,%s", dhcp.RangeBegin, dhcp.RangeEnd, prefixToNetmask(prefix)))
		}
	}

	for i, r := range dhcp.ExtraRanges {
		prefix, err := netip.ParsePrefix(r.NetworkCIDR)
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("set:%s,%s,%s,%s",
			rangeTag(i), r.RangeBegin, r.RangeEnd, prefixToNetmask(prefix)))
	}

	return strings.Join(parts, ";")
}

// buildDHCPOptions is the single source of truth for per-range dhcp-option
// directives. The ironic-image template splits the value on ";" and emits one
// "dhcp-option=" line per item, so DHCP_RANGE must not also carry any
// "dhcp-option=" content (would render twice).
func buildDHCPOptions(dhcp *metal3api.DHCP) string {
	var parts []string
	for i, r := range dhcp.ExtraRanges {
		if r.GatewayAddress != "" {
			parts = append(parts, fmt.Sprintf("tag:%s,option:router,%s", rangeTag(i), r.GatewayAddress))
			continue
		}

		if dhcp.GatewayAddress == "" {
			continue
		}

		// The main GatewayAddress renders as an untagged option:router that
		// dnsmasq would otherwise serve to every range. A router from the
		// main subnet is unreachable from any other IPv4 subnet, so emit a
		// valueless tagged option to suppress it for extra ranges that do
		// not define their own gateway. IPv6 clients never receive the
		// DHCPv4-only option:router, so they need no suppression.
		if prefix, err := netip.ParsePrefix(r.NetworkCIDR); err == nil && prefix.Addr().Is6() {
			continue
		}
		parts = append(parts, fmt.Sprintf("tag:%s,option:router", rangeTag(i)))
	}
	return strings.Join(parts, ";")
}

func buildDNSIP(dhcp *metal3api.DHCP) string {
	if dhcp.ServeDNS {
		return "provisioning" // magical value for serving DNS from the provisioning host using its settings
	}

	if dhcp.DNSAddress != "" {
		return dhcp.DNSAddress
	}

	return ""
}

func newURLProbeHandler(https bool, port int, path string, requiresOk bool) corev1.ProbeHandler {
	proto := "http"
	if https {
		proto = "https"
	}

	curlCmd := []string{
		"curl",
		"--head",
		"--insecure",
		// NOTE(dtantsur): prevent redirects from being treated as success
		"--location",
		"--silent",
		"--show-error",
		// NOTE(dtantsur): --head outputs headers even with --silent
		"--output", "/dev/null",
	}
	if requiresOk {
		curlCmd = append(curlCmd, "--fail")
	}

	// NOTE(dtantsur): we could use HTTP GET probe but we cannot pass the certificate there.
	url := fmt.Sprintf("%s://127.0.0.1:%d%s", proto, port, path)
	return corev1.ProbeHandler{
		Exec: &corev1.ExecAction{
			Command: append(curlCmd, url),
		},
	}
}

// dhcpFamilies reports which address families the configured DHCP CIDRs
// (main and extra ranges) cover, determining the ports dnsmasq listens on:
// 67 for DHCPv4 and 547 for DHCPv6. Unparsable CIDRs count as IPv4 to keep
// the pre-existing default of probing port 67.
func dhcpFamilies(dhcp *metal3api.DHCP) (hasV4, hasV6 bool) {
	cidrs := make([]string, 0, len(dhcp.ExtraRanges)+1)
	if dhcp.NetworkCIDR != "" {
		cidrs = append(cidrs, dhcp.NetworkCIDR)
	}
	for _, r := range dhcp.ExtraRanges {
		cidrs = append(cidrs, r.NetworkCIDR)
	}

	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err == nil && prefix.Addr().Is6() {
			hasV6 = true
		} else {
			hasV4 = true
		}
	}
	return
}

func newDnsmasqContainer(versionInfo VersionInfo, ironic *metal3api.Ironic, sharedVolumeMount corev1.VolumeMount) corev1.Container {
	dhcp := ironic.Spec.Networking.DHCP

	envVars := buildCommonEnvVars(ironic)
	envVars = append(envVars, corev1.EnvVar{
		Name:  "DHCP_RANGE",
		Value: buildDHCPRange(dhcp),
	})

	envVars = appendStringEnv(envVars,
		"DNS_IP", buildDNSIP(dhcp))

	envVars = appendStringEnv(envVars,
		"GATEWAY_IP", dhcp.GatewayAddress)

	envVars = appendStringEnv(envVars,
		"DHCP_OPTIONS", buildDHCPOptions(dhcp))
	envVars = appendListOfStringsEnv(envVars,
		"DHCP_HOSTS", dhcp.Hosts, ";")
	envVars = appendListOfStringsEnv(envVars,
		"DHCP_IGNORE", dhcp.Ignore, ",")

	// IPv4 deployments listen on port 67 (DHCPv4), IPv6 ones on 547 (DHCPv6),
	// mixed-family ones on both; TFTP port 69 is unchanged.
	hasV4, hasV6 := dhcpFamilies(dhcp)
	checks := make([]string, 0, 3)
	if hasV4 || !hasV6 {
		checks = append(checks, "ss -lun | grep :67")
	}
	if hasV6 {
		checks = append(checks, "ss -lun | grep :547")
	}
	checks = append(checks, "ss -lun | grep :69")

	probe := newProbe(corev1.ProbeHandler{
		Exec: &corev1.ExecAction{
			Command: []string{"sh", "-c", strings.Join(checks, " && ")},
		},
	})

	return corev1.Container{
		Name:         "dnsmasq",
		Image:        versionInfo.IronicImage,
		Command:      []string{"/bin/rundnsmasq"},
		Env:          envVars,
		VolumeMounts: []corev1.VolumeMount{sharedVolumeMount},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:                ptr.To(ironicUser),
			RunAsGroup:               ptr.To(ironicGroup),
			ReadOnlyRootFilesystem:   ptr.To(true),
			AllowPrivilegeEscalation: ptr.To(true),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
				Add:  []corev1.Capability{"NET_ADMIN", "NET_BIND_SERVICE", "NET_RAW"},
			},
		},
		LivenessProbe:  probe,
		ReadinessProbe: probe,
	}
}

// formatKeepalivedEntry formats one entry for the KEEPALIVED_VIRTUAL_IPS
// environment variable. The format is "ip,interface[,prefix]".
func formatKeepalivedEntry(ip, iface string, prefix *int32) string {
	if prefix != nil {
		return fmt.Sprintf("%s,%s,%d", ip, iface, *prefix)
	}
	return fmt.Sprintf("%s,%s", ip, iface)
}

func newKeepalivedContainer(versionInfo VersionInfo, ironic *metal3api.Ironic) corev1.Container {
	var envVars []corev1.EnvVar

	if ironic.Spec.Networking.Keepalived != nil && ironic.Spec.Networking.Keepalived.Enabled {
		// New multi-IP mode: use KEEPALIVED_VIRTUAL_IPS env var.
		// The main ipAddress/interface is always included first,
		// followed by any additional VIPs.
		// Format: space-separated "ip,interface[,prefix]" entries.
		entries := make([]string, 1, 1+len(ironic.Spec.Networking.Keepalived.AdditionalVIPs))

		// Derive the prefix for the main IP from DHCP.NetworkCIDR when available.
		// This is the common case: when DHCP is active on the provisioning network,
		// the VIP should be announced with the subnet prefix (e.g. /24) rather than
		// the keepalived default of /32 (IPv4) or /128 (IPv6).
		var mainPrefix *int32
		if ironic.Spec.Networking.DHCP != nil && ironic.Spec.Networking.DHCP.NetworkCIDR != "" {
			if p, err := netip.ParsePrefix(ironic.Spec.Networking.DHCP.NetworkCIDR); err == nil {
				bits := int32(p.Bits()) //nolint:gosec // prefix length is always 0-128, safe to convert
				mainPrefix = &bits
			}
		}
		entries[0] = formatKeepalivedEntry(ironic.Spec.Networking.IPAddress, ironic.Spec.Networking.Interface, mainPrefix)

		for _, vip := range ironic.Spec.Networking.Keepalived.AdditionalVIPs {
			entries = append(entries, formatKeepalivedEntry(vip.IPAddress, vip.Interface, vip.Prefix))
		}
		envVars = append(envVars, corev1.EnvVar{
			Name:  "KEEPALIVED_VIRTUAL_IPS",
			Value: strings.Join(entries, " "),
		})
	} else {
		// Legacy single-IP mode (ipAddressManager: keepalived)
		envVars = append(envVars, corev1.EnvVar{
			Name:  "PROVISIONING_IP",
			Value: ironic.Spec.Networking.IPAddress,
		}, corev1.EnvVar{
			Name:  "PROVISIONING_INTERFACE",
			Value: ironic.Spec.Networking.Interface,
		})
	}

	return corev1.Container{
		Name:  "keepalived",
		Image: versionInfo.KeepalivedImage,
		Env:   envVars,
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:                ptr.To(keepalivedUser),
			RunAsGroup:               ptr.To(keepalivedGroup),
			ReadOnlyRootFilesystem:   ptr.To(true),
			Privileged:               ptr.To(false),
			AllowPrivilegeEscalation: ptr.To(true),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
				Add:  []corev1.Capability{"NET_ADMIN", "NET_BROADCAST", "NET_RAW"},
			},
		},
	}
}

func flaskRunHost(ironic *metal3api.Ironic) string {
	if ironic.Spec.PrometheusExporter != nil && ironic.Spec.PrometheusExporter.BindAddress != "" {
		return ironic.Spec.PrometheusExporter.BindAddress
	}
	return defaultMetricsBindAddr
}

func newPrometheusExporterContainer(versionInfo VersionInfo, ironic *metal3api.Ironic, volumeMount corev1.VolumeMount) corev1.Container {
	port := int32(metricsPort)
	if ironic.Spec.Networking.PrometheusExporterPort != 0 {
		port = ironic.Spec.Networking.PrometheusExporterPort
	}

	bindAddress := flaskRunHost(ironic)

	container := corev1.Container{
		Name:    "ironic-prometheus-exporter",
		Image:   versionInfo.IronicImage,
		Command: []string{"/bin/runironic-exporter"},
		Env: []corev1.EnvVar{
			{
				// Bind address for the metrics endpoint. Defaults to "0.0.0.0" (all interfaces).
				// Can be overridden via spec.prometheusExporter.bindAddress, e.g. to
				// "127.0.0.1" to restrict access to the local host only.
				Name:  "FLASK_RUN_HOST",
				Value: bindAddress,
			},
			{
				Name:  "FLASK_RUN_PORT",
				Value: strconv.Itoa(int(port)),
			},
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          metricsPortName,
				Protocol:      corev1.ProtocolTCP,
				ContainerPort: port,
			},
		},
		VolumeMounts: []corev1.VolumeMount{volumeMount},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:              ptr.To(ironicUser),
			RunAsGroup:             ptr.To(ironicGroup),
			ReadOnlyRootFilesystem: ptr.To(true),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
	}

	// Add readiness probe, handling different bind address scenarios.
	// With HostNetwork: true, kubelet probes use PodIP (the node's primary IP),
	// which may differ from bindAddress when set to a specific interface IP.
	// We must set HTTPGet.Host to ensure the probe reaches the correct address.
	parsedIP := net.ParseIP(bindAddress)
	isLoopback := parsedIP != nil && parsedIP.IsLoopback()

	if !isLoopback {
		// Determine the host to use in the probe.
		// For unspecified/wildcard addresses, probe localhost.
		// For specific IPs, probe that specific address.
		probeHost := bindAddress
		isWildcard := parsedIP != nil && parsedIP.IsUnspecified()
		if isWildcard {
			probeHost = "127.0.0.1"
		}

		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/metrics",
					Port:   intstr.FromInt32(port),
					Host:   probeHost,
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		}
	}

	return container
}

func enforcePortValues(networking *metal3api.Networking) {
	if networking.APIPort == 0 {
		networking.APIPort = 6385
	}
	if networking.ImageServerPort == 0 {
		networking.ImageServerPort = 6180
	}
	if networking.ImageServerTLSPort == 0 {
		networking.ImageServerTLSPort = 6183
	}
	if networking.PrometheusExporterPort == 0 {
		networking.PrometheusExporterPort = 9608
	}
	if networking.RPCPort == 0 {
		networking.RPCPort = 6189
	}
}

func newIronicPodTemplate(cctx ControllerContext, resources Resources) (corev1.PodTemplateSpec, error) {
	if len(resources.APISecret.Data[htpasswdKey]) == 0 {
		return corev1.PodTemplateSpec{}, errors.New("no htpasswd in the API secret")
	}

	if isAuthVolumeRequired(resources) {
		if resources.RPCSecret == nil {
			return corev1.PodTemplateSpec{}, errors.New("the JSON RPC bus is used but no RPC credentials secret is provided")
		}
		if len(resources.RPCSecret.Data[htpasswdKey]) == 0 {
			return corev1.PodTemplateSpec{}, errors.New("no htpasswd in the RPC credentials secret")
		}
	}

	// Sanity: ensure port values are correct even if the webhook is disabled/malfunctioning
	enforcePortValues(&resources.Ironic.Spec.Networking)

	var ipaDownloaderVars []corev1.EnvVar
	ipaDownloaderVars = appendStringEnv(ipaDownloaderVars,
		"IPA_BRANCH", cctx.VersionInfo.AgentBranch)
	ipaDownloaderVars = appendStringEnv(ipaDownloaderVars,
		"IPA_BASEURI", os.Getenv("IPA_BASEURI"))
	ipaDownloaderVars = appendStringEnv(ipaDownloaderVars,
		"IPA_FLAVOR", os.Getenv("IPA_FLAVOR"))
	ipaDownloaderVars = appendStringEnv(ipaDownloaderVars,
		"IPA_ARCH", os.Getenv("IPA_ARCH"))
	ipaDownloaderVars = appendStringEnv(ipaDownloaderVars,
		"HTTPS_PROXY", os.Getenv("IPA_HTTPS_PROXY"))
	ipaDownloaderVars = appendStringEnv(ipaDownloaderVars,
		"HTTP_PROXY", os.Getenv("IPA_HTTP_PROXY"))
	ipaDownloaderVars = appendStringEnv(ipaDownloaderVars,
		"NO_PROXY", os.Getenv("IPA_NO_PROXY"))

	volumes, mounts := buildIronicVolumesAndMounts(resources)
	sharedVolumeMount := mounts[0]

	downloaderVolumeMounts := []corev1.VolumeMount{sharedVolumeMount}

	// Let the ramdisk-downloader init container trust the same CA bundle as
	// Ironic itself (spec.tls.trustedCA), so it can fetch the IPA
	// kernel/ramdisk from an image server with a custom/self-signed
	// certificate. CURL_CA_BUNDLE is honored natively by curl.
	if caPath := resolveTrustedCACertPath(cctx, resources); caPath != "" {
		ipaDownloaderVars = appendStringEnv(ipaDownloaderVars, "CURL_CA_BUNDLE", caPath)

		for _, m := range mounts {
			if m.Name == trustedCAVolumeName {
				downloaderVolumeMounts = append(downloaderVolumeMounts, m)
				break
			}
		}
	}

	var initContainers []corev1.Container
	if !resources.Ironic.Spec.DeployRamdisk.DisableDownloader {
		initContainers = append(initContainers, corev1.Container{
			Name:         "ramdisk-downloader",
			Image:        cctx.VersionInfo.RamdiskDownloaderImage,
			Env:          ipaDownloaderVars,
			VolumeMounts: downloaderVolumeMounts,
			SecurityContext: &corev1.SecurityContext{
				RunAsUser:              ptr.To(ironicUser),
				RunAsGroup:             ptr.To(ironicGroup),
				ReadOnlyRootFilesystem: ptr.To(true),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
		})
	}

	ironicPorts, httpdPorts := buildIronicHttpdPorts(resources.Ironic)

	ironicHandler := newURLProbeHandler(resources.TLSSecret != nil, int(resources.Ironic.Spec.Networking.APIPort), "/v1", true)

	// Httpd probes are always exec-based (curl). HTTP 2xx is only required when the default IPA
	// kernel is expected to be served (i.e. no custom images and downloader is enabled).
	hasCustomImages := resources.Ironic.Spec.Overrides != nil && len(resources.Ironic.Spec.Overrides.AgentImages) > 0
	httpPathExpected := !hasCustomImages && !resources.Ironic.Spec.DeployRamdisk.DisableDownloader
	httpdHandler := newURLProbeHandler(false, int(resources.Ironic.Spec.Networking.ImageServerPort), knownExistingPath, httpPathExpected)
	httpdLivenessProbe := newProbe(httpdHandler)
	httpdReadinessProbe := newProbe(httpdHandler)

	// Apply explicit probe overrides. mergeProbe fills in timing defaults for any fields the user has not explicitly set.
	if resources.Ironic.Spec.Overrides != nil {
		// FIXME(dtantsur): stop applying IrSO defaults to user overrides.
		// This will be a breaking change with a corresponding record in the release notes.
		if p := resources.Ironic.Spec.Overrides.HttpdLivenessProbe; p != nil {
			httpdLivenessProbe = mergeProbe(newProbe(p.ProbeHandler), p)
		}
		if p := resources.Ironic.Spec.Overrides.HttpdReadinessProbe; p != nil {
			httpdReadinessProbe = mergeProbe(newProbe(p.ProbeHandler), p)
		}
	}

	ironicEnvVars := buildIronicEnvVars(cctx, resources)
	httpdEnvVars := buildHttpdEnvVars(resources)

	containers := []corev1.Container{
		{
			Name:         "ironic",
			Image:        cctx.VersionInfo.IronicImage,
			Command:      []string{"/bin/runironic"},
			Env:          ironicEnvVars,
			VolumeMounts: mounts,
			SecurityContext: &corev1.SecurityContext{
				RunAsUser:              ptr.To(ironicUser),
				RunAsGroup:             ptr.To(ironicGroup),
				ReadOnlyRootFilesystem: ptr.To(true),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
			Ports:          ironicPorts,
			LivenessProbe:  newProbe(ironicHandler),
			ReadinessProbe: newProbe(ironicHandler),
		},
		{
			Name:         "httpd",
			Image:        cctx.VersionInfo.IronicImage,
			Command:      []string{"/bin/runhttpd"},
			Env:          httpdEnvVars,
			VolumeMounts: mounts,
			SecurityContext: &corev1.SecurityContext{
				RunAsUser:              ptr.To(ironicUser),
				RunAsGroup:             ptr.To(ironicGroup),
				ReadOnlyRootFilesystem: ptr.To(true),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
			Ports:          httpdPorts,
			LivenessProbe:  httpdLivenessProbe,
			ReadinessProbe: httpdReadinessProbe,
		},
		{
			Name:         "ramdisk-logs",
			Image:        cctx.VersionInfo.IronicImage,
			Command:      []string{"/bin/runlogwatch.sh"},
			VolumeMounts: []corev1.VolumeMount{sharedVolumeMount},
			SecurityContext: &corev1.SecurityContext{
				RunAsUser:              ptr.To(ironicUser),
				RunAsGroup:             ptr.To(ironicGroup),
				ReadOnlyRootFilesystem: ptr.To(true),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
		},
	}
	if resources.Ironic.Spec.Networking.DHCP != nil && !resources.Ironic.Spec.HighAvailability {
		err := ValidateDHCP(&resources.Ironic.Spec)
		if err != nil {
			return corev1.PodTemplateSpec{}, err
		}
		dnsmasqContainer := newDnsmasqContainer(cctx.VersionInfo, resources.Ironic, sharedVolumeMount)
		containers = append(containers, dnsmasqContainer)
	}

	if resources.Ironic.Spec.Networking.IPAddressManager == metal3api.IPAddressManagerKeepalived || //nolint:staticcheck // backward compat
		(resources.Ironic.Spec.Networking.Keepalived != nil && resources.Ironic.Spec.Networking.Keepalived.Enabled) {
		containers = append(containers, newKeepalivedContainer(cctx.VersionInfo, resources.Ironic))
	}

	if resources.Ironic.Spec.PrometheusExporter != nil && resources.Ironic.Spec.PrometheusExporter.Enabled {
		containers = append(containers, newPrometheusExporterContainer(cctx.VersionInfo, resources.Ironic, sharedVolumeMount))
	}

	// Make sure the pod is restarted when secrets change.
	annotations := secretVersionAnnotations("api-secret", resources.APISecret)
	if resources.RPCSecret != nil {
		maps.Copy(annotations, secretVersionAnnotations("rpc-secret", resources.RPCSecret))
	}
	if resources.TLSSecret != nil {
		maps.Copy(annotations, secretVersionAnnotations("tls-secret", resources.TLSSecret))
	}

	hostNetwork := !resources.Ironic.Spec.Networking.DisableHostNetwork
	dnsPolicy := corev1.DNSClusterFirstWithHostNet
	if !hostNetwork {
		dnsPolicy = corev1.DNSClusterFirst
	}

	return applyOverridesToPod(resources.Ironic.Spec.Overrides, addDataVolumes(corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				metal3api.IronicAppLabel:     ironicDeploymentName(resources.Ironic),
				metal3api.IronicServiceLabel: resources.Ironic.Name,
				metal3api.IronicVersionLabel: cctx.VersionInfo.InstalledVersion.String(),
			},
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers:                   containers,
			InitContainers:               initContainers,
			Volumes:                      volumes,
			HostNetwork:                  hostNetwork,
			DNSPolicy:                    dnsPolicy,
			NodeSelector:                 resources.Ironic.Spec.NodeSelector,
			AutomountServiceAccountToken: ptr.To(false),
		},
	})), nil
}
