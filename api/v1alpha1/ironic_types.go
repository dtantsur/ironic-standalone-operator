/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	majorVersion27 = 27
	majorVersion28 = 28
	majorVersion29 = 29
	majorVersion30 = 30
	majorVersion31 = 31
)

var (
	VersionLatest = Version{}
	Version310    = Version{Major: majorVersion31, Minor: 0}
	Version300    = Version{Major: majorVersion30, Minor: 0}
	Version290    = Version{Major: majorVersion29, Minor: 0}
	Version280    = Version{Major: majorVersion28, Minor: 0}
	Version270    = Version{Major: majorVersion27, Minor: 0}
)

// SupportedVersions is a mapping of supported versions to container image tags.
// This mapping must be updated each time a new ironic-image branch is created.
// Also consider updating the version test(s) in test/suite_test.go to verify
// that the new version is installable and its API version matches
// expectations.
var SupportedVersions = map[Version]string{
	VersionLatest: "latest",
	Version310:    "release-31.0",
	Version300:    "release-30.0",
	Version290:    "release-29.0",
	Version280:    "release-28.0",
	Version270:    "release-27.0",
}

// Inspection defines inspection settings.
type Inspection struct {
	// Collectors is a list of inspection collectors to enable.
	// See https://docs.openstack.org/ironic-python-agent/latest/admin/how_it_works.html#inspection-data for details.
	// +optional
	Collectors []string `json:"collectors,omitempty"`

	// List of interfaces to inspect for VLANs.
	// This can be interface names (to collect all VLANs using LLDP) or pairs <interface>.<vlan ID>.
	// +optional
	VLANInterfaces []string `json:"vlanInterfaces,omitempty"`
}

type DHCP struct {
	// DNSAddress is the IP address of the DNS server to pass to hosts via DHCP.
	// Must not be set together with ServeDNS.
	// +optional
	DNSAddress string `json:"dnsAddress,omitempty"`

	// GatewayAddress is the IP address of the gateway to pass to hosts via DHCP.
	// +optional
	GatewayAddress string `json:"gatewayAddress,omitempty"`

	// Hosts is a set of DHCP host records to pass to dnsmasq.
	// Check the dnsmasq documentation on dhcp-host for an explanation of the format.
	// There is no API-side validation. Most users will leave this unset.
	// +optional
	Hosts []string `json:"hosts,omitempty"`

	// Ignore is set of dnsmasq tags to ignore and not provide any DHCP.
	// Check the dnsmasq documentation on dhcp-ignore for an explanation of the format.
	// There is no API-side validation. Most users will leave this unset.
	// +optional
	Ignore []string `json:"ignore,omitempty"`

	// NetworkCIDR is a CIDR of the provisioning network. Required.
	NetworkCIDR string `json:"networkCIDR,omitempty"`

	// RangeBegin is the first IP that can be given to hosts. Must be inside NetworkCIDR.
	RangeBegin string `json:"rangeBegin,omitempty"`

	// RangeEnd is the last IP that can be given to hosts. Must be inside NetworkCIDR.
	RangeEnd string `json:"rangeEnd,omitempty"`

	// ServeDNS is set to true to pass the provisioning host as the DNS server on the provisioning network.
	// Must not be set together with DNSAddress.
	// +optional
	ServeDNS bool `json:"serveDNS,omitempty"`
}

type IPAddressManager string

const (
	IPAddressManagerNone       IPAddressManager = ""
	IPAddressManagerKeepalived IPAddressManager = "keepalived"
)

// Networking defines networking settings for Ironic.
type Networking struct {
	// APIPort is the public port used for Ironic.
	// +kubebuilder:default=6385
	// +kubebuilder:validation:Minimum=1
	// +optional
	APIPort int32 `json:"apiPort,omitempty"`

	// BindInterface makes Ironic API bound to only one interface.
	// +optional
	BindInterface bool `json:"bindInterface,omitempty"`

	// DHCP is a configuration of DHCP for the network boot service (dnsmasq).
	// The service is only deployed when this is set.
	// This setting is currently incompatible with the highly available architecture.
	DHCP *DHCP `json:"dhcp,omitempty"`

	// ExternalIP is used for accessing API and the image server from remote hosts.
	// This settings only applies to virtual media deployments. The IP will not be accessed from the cluster itself.
	// +optional
	ExternalIP string `json:"externalIP,omitempty"`

	// ImageServerPort is the public port used for serving images.
	// +kubebuilder:default=6180
	// +kubebuilder:validation:Minimum=1
	// +optional
	ImageServerPort int32 `json:"imageServerPort,omitempty"`

	// ImageServerTLSPort is the public port used for serving virtual media images over TLS.
	// +kubebuilder:default=6183
	// +kubebuilder:validation:Minimum=1
	// +optional
	ImageServerTLSPort int32 `json:"imageServerTLSPort,omitempty"`

	// Interface is a Linux network device to listen on.
	// Detected from IPAddress if missing.
	// +optional
	Interface string `json:"interface,omitempty"`

	// IPAddress is the main IP address to listen on and use for communication.
	// Detected from Interface if missing. Cannot be provided for a highly available architecture.
	// +optional
	IPAddress string `json:"ipAddress,omitempty"`

	// Configures the way the provided IP address will be managed on the provided interface.
	// By default, the IP address is expected to be already present.
	// Use "keepalived" to start a Keepalived container managing the IP address.
	// Warning: keepalived is not compatible with the highly available architecture.
	// +kubebuilder:validation:Enum="";keepalived
	// +optional
	IPAddressManager IPAddressManager `json:"ipAddressManager,omitempty"`

	// MACAddresses can be provided to make the start script pick the interface matching any of these addresses.
	// Only set if no other options can be used.
	// +optional
	MACAddresses []string `json:"macAddresses,omitempty"`
}

// DeployRamdisk defines IPA ramdisk settings.
type DeployRamdisk struct {
	// DisableDownloader tells the operator not to start the IPA downloader as the init container.
	// The user will be responsible for providing the right image to BareMetal Operator.
	// +optional
	DisableDownloader bool `json:"disableDownloader,omitempty"`

	// ExtraKernelParams is a string with kernel parameters to pass to the provisioning/inspection ramdisk.
	// Will not take effect if the host uses a pre-built ISO (either through its PreprovisioningImage or via the DEPLOY_ISO_URL baremetal-operator parameter).
	// +optional
	ExtraKernelParams string `json:"extraKernelParams,omitempty"`

	// SSHKey is the contents of the public key to inject into the ramdisk for debugging purposes.
	// +optional
	SSHKey string `json:"sshKey,omitempty"`
}

// TLS defines the TLS settings.
type TLS struct {
	// CertificateName is a reference to the secret with the TLS certificate.
	// +optional
	CertificateName string `json:"certificateName,omitempty"`

	// DisableVirtualMediaTLS turns off TLS on the virtual media server,
	// which may be required for hardware that cannot accept HTTPS links.
	// +optional
	DisableVirtualMediaTLS bool `json:"disableVirtualMediaTLS,omitempty"`

	// InsecureRPC disables TLS validation for the internal RPC.
	// Without it, the certificate must be valid for all IP addresses on
	// which Ironic replicas may end up running.
	// Has no effect when HighAvailability is false and requires the
	// HighAvailability feature gate to be set.
	// +optional
	InsecureRPC *bool `json:"insecureRPC,omitempty"`
}

type Images struct {
	// DeployRamdiskBranch is the branch of IPA to download. The main branch is used by default.
	// Not used if deployRamdisk.disableDownloader is true.
	// +optional
	DeployRamdiskBranch string `json:"deployRamdiskBranch,omitempty"`

	// DeployRamdiskDownloader is the image to be used at pod initialization to download the IPA ramdisk.
	// Not used if deployRamdisk.disableDownloader is true.
	// +optional
	DeployRamdiskDownloader string `json:"deployRamdiskDownloader,omitempty"`

	// Ironic is the Ironic image (including httpd).
	// +optional
	Ironic string `json:"ironic,omitempty"`

	// Keepalived is the Keepalived image.
	// Not used if networking.ipAddressManager is not set to keepalived.
	// +optional
	Keepalived string `json:"keepalived,omitempty"`
}

// ExtraConfig defines environment variables to override Ironic configuration
// More info at the end of description section
// https://github.com/metal3-io/ironic-image
type ExtraConfig struct {

	// The group that config belongs to.
	// +optional
	Group string `json:"group,omitempty"`

	// The name of the config.
	// +optional
	Name string `json:"name,omitempty"`

	// The value of the config.
	// +optional
	Value string `json:"value,omitempty"`
}

// Database is a reference to a MariaDB database to use.
type Database struct {
	// Name of a secret with database credentials.
	CredentialsName string `json:"credentialsName"`

	// IP address or host name of the database instance.
	Host string `json:"host"`

	// Database name.
	Name string `json:"name"`

	// Name of a secret with the a TLS certificate or a CA for verifying the database host.
	// If unset, Ironic will request an unencrypted connections, which is insecure,
	// and the server configuration may forbid it.
	// +optional
	TLSCertificateName string `json:"tlsCertificateName,omitempty"`
}

type Overrides struct {
	// Extra annotations to add to each pod (including upgrade jobs).
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Extra labels to add to each pod (including upgrade jobs).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// PatchContainers allows applying JSON patches to specific containers or initContainers.
	// +optional
	PatchContainers []PatchContainer `json:"patchContainers,omitempty"`
}

// JSONPatchOperation represents a single JSON patch operation (RFC 6902).
type JSONPatchOperation struct {
	// Op is the operation to perform: add, remove, replace, move, copy, test.
	Op string `json:"op"`

	// Path is the JSON pointer to the location in the target document.
	Path string `json:"path"`

	// From is the JSON pointer to the source location (for move and copy operations).
	// +optional
	From string `json:"from,omitempty"`

	// Value is the value to be used within the operation.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Value *runtime.RawExtension `json:"value,omitempty"`
}

// PatchContainer defines a JSON patch to apply to a specific container.
type PatchContainer struct {
	// Name is the name of the container or initContainer to patch.
	Name string `json:"name"`

	// Patch is a JSON patch (RFC 6902) to apply to the container.
	// This should be a JSON array of patch operations.
	// +kubebuilder:validation:Type=array
	Patch []JSONPatchOperation `json:"patch"`
}

// IronicSpec defines the desired state of Ironic.
type IronicSpec struct {
	// APICredentialsName is a reference to the secret with Ironic API credentials.
	// A new secret will be created if this field is empty.
	// +optional
	APICredentialsName string `json:"apiCredentialsName,omitempty"`

	// Database is a reference to a MariaDB database to use for persisting Ironic data.
	// Must be provided for a highly available architecture, optional otherwise.
	// If missing, a local SQLite database will be used, and the Ironic state will be reset on each pod restart.
	// +optional
	Database *Database `json:"database,omitempty"`

	// DeployRamdisk defines settings for the provisioning/inspection ramdisk based on Ironic Python Agent.
	// +optional
	DeployRamdisk DeployRamdisk `json:"deployRamdisk,omitempty"`

	// ExtraConfig defines extra options for Ironic configuration.
	// +optional
	ExtraConfig []ExtraConfig `json:"extraConfig,omitempty"`

	// HighAvailability causes Ironic to be deployed as a DaemonSet on control plane nodes instead of a deployment with 1 replica.
	// Requires database to be installed and linked in the Database field.
	// DHCP support is not yet implemented in the highly available architecture.
	// Requires the HighAvailability feature gate to be set.
	// +optional
	HighAvailability bool `json:"highAvailability,omitempty"`

	// Images is a collection of container images to deploy from.
	// +optional
	Images Images `json:"images,omitempty"`

	// Inspection defines inspection settings.
	// +optional
	Inspection Inspection `json:"inspection,omitempty"`

	// Networking defines networking settings for Ironic.
	// +optional
	Networking Networking `json:"networking,omitempty"`

	// NodeSelector is a selector which must be true for the Ironic pod to fit on a node.
	// Selector which must match a node's labels for the vmi to be scheduled on that node.
	// More info: https://kubernetes.io/docs/concepts/configuration/assign-pod-node/
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Overrides for the generated Deployment or Daemon Set.
	// EXPERIMENTAL: requires feature gate Overrides.
	// +optional
	Overrides *Overrides `json:"overrides,omitempty"`

	// TLS defines TLS-related settings for various network interactions.
	// +optional
	TLS TLS `json:"tls,omitempty"`

	// Version is the version of Ironic to be installed.
	// Must be either "latest" or a MAJOR.MINOR pair, e.g. "27.0".
	// The default version depends on the operator branch.
	// +optional
	Version string `json:"version,omitempty"`
}

// IronicStatus defines the observed state of Ironic.
type IronicStatus struct {
	// Conditions describe the state of the Ironic deployment.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// RequestedVersion identifies which version of Ironic was last requested.
	RequestedVersion string `json:"requestedVersion,omitempty"`

	// InstalledVersion identifies which version of Ironic was installed.
	// +optional
	InstalledVersion string `json:"installedVersion,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Requested Version",type="string",JSONPath=".status.requestedVersion",description="Currently requested version",priority=1
//+kubebuilder:printcolumn:name="Installed Version",type="string",JSONPath=".status.installedVersion",description="Currently installed version"
//+kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Is ready"

// Ironic is the Schema for the ironics API.
type Ironic struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IronicSpec   `json:"spec,omitempty"`
	Status IronicStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// IronicList contains a list of Ironic.
type IronicList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Ironic `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &Ironic{}, &IronicList{})
}
