# API Reference

Packages:

- [ironic.metal3.io/v1alpha1](#ironicmetal3iov1alpha1)

# ironic.metal3.io/v1alpha1

Resource Types:

- [Ironic](#ironic)




## Ironic
<sup><sup>[↩ Parent](#ironicmetal3iov1alpha1 )</sup></sup>






Ironic is the Schema for the ironics API.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
      <td><b>apiVersion</b></td>
      <td>string</td>
      <td>ironic.metal3.io/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Ironic</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#ironicspec">spec</a></b></td>
        <td>object</td>
        <td>
          IronicSpec defines the desired state of Ironic.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicstatus">status</a></b></td>
        <td>object</td>
        <td>
          IronicStatus defines the observed state of Ironic.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec
<sup><sup>[↩ Parent](#ironic)</sup></sup>



IronicSpec defines the desired state of Ironic.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>apiCredentialsName</b></td>
        <td>string</td>
        <td>
          APICredentialsName is a reference to the secret with Ironic API credentials.
A new secret will be created if this field is empty.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecdatabase">database</a></b></td>
        <td>object</td>
        <td>
          Database is a reference to a MariaDB database to use for persisting Ironic data.
Must be provided for a highly available architecture, optional otherwise.
If missing, a local SQLite database will be used, and the Ironic state will be reset on each pod restart.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecdeployramdisk">deployRamdisk</a></b></td>
        <td>object</td>
        <td>
          DeployRamdisk defines settings for the provisioning/inspection ramdisk based on Ironic Python Agent.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecextraconfigindex">extraConfig</a></b></td>
        <td>[]object</td>
        <td>
          ExtraConfig defines extra options for Ironic configuration.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>highAvailability</b></td>
        <td>boolean</td>
        <td>
          HighAvailability causes Ironic to be deployed as a DaemonSet on control plane nodes instead of a deployment with 1 replica.
Requires database to be installed and linked in the Database field.
DHCP support is not yet implemented in the highly available architecture.
Requires the HighAvailability feature gate to be set.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecimages">images</a></b></td>
        <td>object</td>
        <td>
          Images is a collection of container images to deploy from.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecinspection">inspection</a></b></td>
        <td>object</td>
        <td>
          Inspection defines inspection settings.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecnetworking">networking</a></b></td>
        <td>object</td>
        <td>
          Networking defines networking settings for Ironic.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>nodeSelector</b></td>
        <td>map[string]string</td>
        <td>
          NodeSelector is a selector which must be true for the Ironic pod to fit on a node.
Selector which must match a node's labels for the vmi to be scheduled on that node.
More info: https://kubernetes.io/docs/concepts/configuration/assign-pod-node/<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecoverrides">overrides</a></b></td>
        <td>object</td>
        <td>
          Overrides for the generated Deployment or Daemon Set.
EXPERIMENTAL: requires feature gate Overrides.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspectls">tls</a></b></td>
        <td>object</td>
        <td>
          TLS defines TLS-related settings for various network interactions.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>version</b></td>
        <td>string</td>
        <td>
          Version is the version of Ironic to be installed.
Must be either "latest" or a MAJOR.MINOR pair, e.g. "27.0".
The default version depends on the operator branch.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.database
<sup><sup>[↩ Parent](#ironicspec)</sup></sup>



Database is a reference to a MariaDB database to use for persisting Ironic data.
Must be provided for a highly available architecture, optional otherwise.
If missing, a local SQLite database will be used, and the Ironic state will be reset on each pod restart.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>credentialsName</b></td>
        <td>string</td>
        <td>
          Name of a secret with database credentials.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>host</b></td>
        <td>string</td>
        <td>
          IP address or host name of the database instance.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Database name.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>tlsCertificateName</b></td>
        <td>string</td>
        <td>
          Name of a secret with the a TLS certificate or a CA for verifying the database host.
If unset, Ironic will request an unencrypted connections, which is insecure,
and the server configuration may forbid it.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.deployRamdisk
<sup><sup>[↩ Parent](#ironicspec)</sup></sup>



DeployRamdisk defines settings for the provisioning/inspection ramdisk based on Ironic Python Agent.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>disableDownloader</b></td>
        <td>boolean</td>
        <td>
          DisableDownloader tells the operator not to start the IPA downloader as the init container.
The user will be responsible for providing the right image to BareMetal Operator.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>extraKernelParams</b></td>
        <td>string</td>
        <td>
          ExtraKernelParams is a string with kernel parameters to pass to the provisioning/inspection ramdisk.
Will not take effect if the host uses a pre-built ISO (either through its PreprovisioningImage or via the DEPLOY_ISO_URL baremetal-operator parameter).<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>sshKey</b></td>
        <td>string</td>
        <td>
          SSHKey is the contents of the public key to inject into the ramdisk for debugging purposes.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.extraConfig[index]
<sup><sup>[↩ Parent](#ironicspec)</sup></sup>



ExtraConfig defines environment variables to override Ironic configuration
More info at the end of description section
https://github.com/metal3-io/ironic-image

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>group</b></td>
        <td>string</td>
        <td>
          The group that config belongs to.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          The name of the config.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          The value of the config.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.images
<sup><sup>[↩ Parent](#ironicspec)</sup></sup>



Images is a collection of container images to deploy from.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>deployRamdiskBranch</b></td>
        <td>string</td>
        <td>
          DeployRamdiskBranch is the branch of IPA to download. The main branch is used by default.
Not used if deployRamdisk.disableDownloader is true.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>deployRamdiskDownloader</b></td>
        <td>string</td>
        <td>
          DeployRamdiskDownloader is the image to be used at pod initialization to download the IPA ramdisk.
Not used if deployRamdisk.disableDownloader is true.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ironic</b></td>
        <td>string</td>
        <td>
          Ironic is the Ironic image (including httpd).<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>keepalived</b></td>
        <td>string</td>
        <td>
          Keepalived is the Keepalived image.
Not used if networking.ipAddressManager is not set to keepalived.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.inspection
<sup><sup>[↩ Parent](#ironicspec)</sup></sup>



Inspection defines inspection settings.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>collectors</b></td>
        <td>[]string</td>
        <td>
          Collectors is a list of inspection collectors to enable.
See https://docs.openstack.org/ironic-python-agent/latest/admin/how_it_works.html#inspection-data for details.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>vlanInterfaces</b></td>
        <td>[]string</td>
        <td>
          List of interfaces to inspect for VLANs.
This can be interface names (to collect all VLANs using LLDP) or pairs <interface>.<vlan ID>.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.networking
<sup><sup>[↩ Parent](#ironicspec)</sup></sup>



Networking defines networking settings for Ironic.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>apiPort</b></td>
        <td>integer</td>
        <td>
          APIPort is the public port used for Ironic.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Default</i>: 6385<br/>
            <i>Minimum</i>: 1<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>bindInterface</b></td>
        <td>boolean</td>
        <td>
          BindInterface makes Ironic API bound to only one interface.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecnetworkingdhcp">dhcp</a></b></td>
        <td>object</td>
        <td>
          DHCP is a configuration of DHCP for the network boot service (dnsmasq).
The service is only deployed when this is set.
This setting is currently incompatible with the highly available architecture.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>externalIP</b></td>
        <td>string</td>
        <td>
          ExternalIP is used for accessing API and the image server from remote hosts.
This settings only applies to virtual media deployments. The IP will not be accessed from the cluster itself.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>imageServerPort</b></td>
        <td>integer</td>
        <td>
          ImageServerPort is the public port used for serving images.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Default</i>: 6180<br/>
            <i>Minimum</i>: 1<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>imageServerTLSPort</b></td>
        <td>integer</td>
        <td>
          ImageServerTLSPort is the public port used for serving virtual media images over TLS.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Default</i>: 6183<br/>
            <i>Minimum</i>: 1<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>interface</b></td>
        <td>string</td>
        <td>
          Interface is a Linux network device to listen on.
Detected from IPAddress if missing.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ipAddress</b></td>
        <td>string</td>
        <td>
          IPAddress is the main IP address to listen on and use for communication.
Detected from Interface if missing. Cannot be provided for a highly available architecture.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ipAddressManager</b></td>
        <td>enum</td>
        <td>
          Configures the way the provided IP address will be managed on the provided interface.
By default, the IP address is expected to be already present.
Use "keepalived" to start a Keepalived container managing the IP address.
Warning: keepalived is not compatible with the highly available architecture.<br/>
          <br/>
            <i>Enum</i>: , keepalived<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>macAddresses</b></td>
        <td>[]string</td>
        <td>
          MACAddresses can be provided to make the start script pick the interface matching any of these addresses.
Only set if no other options can be used.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.networking.dhcp
<sup><sup>[↩ Parent](#ironicspecnetworking)</sup></sup>



DHCP is a configuration of DHCP for the network boot service (dnsmasq).
The service is only deployed when this is set.
This setting is currently incompatible with the highly available architecture.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>dnsAddress</b></td>
        <td>string</td>
        <td>
          DNSAddress is the IP address of the DNS server to pass to hosts via DHCP.
Must not be set together with ServeDNS.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>gatewayAddress</b></td>
        <td>string</td>
        <td>
          GatewayAddress is the IP address of the gateway to pass to hosts via DHCP.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>hosts</b></td>
        <td>[]string</td>
        <td>
          Hosts is a set of DHCP host records to pass to dnsmasq.
Check the dnsmasq documentation on dhcp-host for an explanation of the format.
There is no API-side validation. Most users will leave this unset.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ignore</b></td>
        <td>[]string</td>
        <td>
          Ignore is set of dnsmasq tags to ignore and not provide any DHCP.
Check the dnsmasq documentation on dhcp-ignore for an explanation of the format.
There is no API-side validation. Most users will leave this unset.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>networkCIDR</b></td>
        <td>string</td>
        <td>
          NetworkCIDR is a CIDR of the provisioning network. Required.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>rangeBegin</b></td>
        <td>string</td>
        <td>
          RangeBegin is the first IP that can be given to hosts. Must be inside NetworkCIDR.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>rangeEnd</b></td>
        <td>string</td>
        <td>
          RangeEnd is the last IP that can be given to hosts. Must be inside NetworkCIDR.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>serveDNS</b></td>
        <td>boolean</td>
        <td>
          ServeDNS is set to true to pass the provisioning host as the DNS server on the provisioning network.
Must not be set together with DNSAddress.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.overrides
<sup><sup>[↩ Parent](#ironicspec)</sup></sup>



Overrides for the generated Deployment or Daemon Set.
EXPERIMENTAL: requires feature gate Overrides.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>annotations</b></td>
        <td>map[string]string</td>
        <td>
          Extra annotations to add to each pod (including upgrade jobs).<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>labels</b></td>
        <td>map[string]string</td>
        <td>
          Extra labels to add to each pod (including upgrade jobs).<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#ironicspecoverridespatchcontainersindex">patchContainers</a></b></td>
        <td>[]object</td>
        <td>
          PatchContainers allows applying JSON patches to specific containers or initContainers.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.overrides.patchContainers[index]
<sup><sup>[↩ Parent](#ironicspecoverrides)</sup></sup>



PatchContainer defines a JSON patch to apply to a specific container.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the container or initContainer to patch.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#ironicspecoverridespatchcontainersindexpatchindex">patch</a></b></td>
        <td>[]object</td>
        <td>
          Patch is a JSON patch (RFC 6902) to apply to the container.
This should be a JSON array of patch operations.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Ironic.spec.overrides.patchContainers[index].patch[index]
<sup><sup>[↩ Parent](#ironicspecoverridespatchcontainersindex)</sup></sup>



JSONPatchOperation represents a single JSON patch operation (RFC 6902).

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>op</b></td>
        <td>string</td>
        <td>
          Op is the operation to perform: add, remove, replace, move, copy, test.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>path</b></td>
        <td>string</td>
        <td>
          Path is the JSON pointer to the location in the target document.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>from</b></td>
        <td>string</td>
        <td>
          From is the JSON pointer to the source location (for move and copy operations).<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>object</td>
        <td>
          Value is the value to be used within the operation.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.spec.tls
<sup><sup>[↩ Parent](#ironicspec)</sup></sup>



TLS defines TLS-related settings for various network interactions.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>certificateName</b></td>
        <td>string</td>
        <td>
          CertificateName is a reference to the secret with the TLS certificate.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>disableVirtualMediaTLS</b></td>
        <td>boolean</td>
        <td>
          DisableVirtualMediaTLS turns off TLS on the virtual media server,
which may be required for hardware that cannot accept HTTPS links.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>insecureRPC</b></td>
        <td>boolean</td>
        <td>
          InsecureRPC disables TLS validation for the internal RPC.
Without it, the certificate must be valid for all IP addresses on
which Ironic replicas may end up running.
Has no effect when HighAvailability is false and requires the
HighAvailability feature gate to be set.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.status
<sup><sup>[↩ Parent](#ironic)</sup></sup>



IronicStatus defines the observed state of Ironic.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#ironicstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions describe the state of the Ironic deployment.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>installedVersion</b></td>
        <td>string</td>
        <td>
          InstalledVersion identifies which version of Ironic was installed.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>requestedVersion</b></td>
        <td>string</td>
        <td>
          RequestedVersion identifies which version of Ironic was last requested.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Ironic.status.conditions[index]
<sup><sup>[↩ Parent](#ironicstatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>