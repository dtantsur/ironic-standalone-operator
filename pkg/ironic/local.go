package ironic

import (
	"errors"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
)

// ParseLocalIronic parses a YAML file containing Ironic resources and secrets.
func ParseLocalIronic(inputFile string, scheme *runtime.Scheme) (*Resources, error) {
	data, err := os.ReadFile(inputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read input file: %w", err)
	}

	// Create a decoder for YAML
	codecFactory := serializer.NewCodecFactory(scheme)
	decoder := codecFactory.UniversalDeserializer()

	// Split YAML documents
	docs := splitYAMLDocuments(data)
	if len(docs) == 0 {
		return nil, errors.New("no documents found in input file")
	}

	ironics := make([]*metal3api.Ironic, 0, 1)
	var secrets []*corev1.Secret
	var configMaps []*corev1.ConfigMap

	for i, doc := range docs {
		obj, gvk, err := decoder.Decode(doc, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to decode document %d: %w", i, err)
		}

		switch gvk.Kind {
		case "Ironic":
			if ironicObj, ok := obj.(*metal3api.Ironic); ok {
				ironics = append(ironics, ironicObj)
			} else {
				return nil, fmt.Errorf("document %d: expected Ironic object", i)
			}
		case "Secret":
			if secretObj, ok := obj.(*corev1.Secret); ok {
				secrets = append(secrets, secretObj)
			} else {
				return nil, fmt.Errorf("document %d: expected Secret object", i)
			}
		case "ConfigMap":
			if configMapObj, ok := obj.(*corev1.ConfigMap); ok {
				configMaps = append(configMaps, configMapObj)
			} else {
				return nil, fmt.Errorf("document %d: expected ConfigMap object", i)
			}
		default:
			return nil, fmt.Errorf("object of unexpected kind %s", gvk.Kind)
		}
	}

	if len(ironics) != 1 {
		return nil, fmt.Errorf("exactly one Ironic resources expected in the input, got %d", len(ironics))
	}

	resources := &Resources{
		Ironic: ironics[0],
	}

	// Get effective CA references
	bmcCARef := GetBMCCA(&resources.Ironic.Spec.TLS)
	trustedCARef := GetTrustedCA(&resources.Ironic.Spec.TLS)

	for _, secretObj := range secrets {
		// Determine secret type based on name patterns or labels
		switch secretObj.Name {
		case resources.Ironic.Spec.APICredentialsName:
			resources.APISecret = secretObj
		case resources.Ironic.Spec.TLS.CertificateName:
			resources.TLSSecret = secretObj
		case rpcCredentialsSecretName(resources.Ironic):
			resources.RPCSecret = secretObj
		default:
			matched := false
			if bmcCARef != nil && bmcCARef.Kind == metal3api.ResourceKindSecret && bmcCARef.Name == secretObj.Name {
				resources.BMCCASecret = secretObj
				matched = true
			}
			if trustedCARef != nil && trustedCARef.Kind == metal3api.ResourceKindSecret && trustedCARef.Name == secretObj.Name {
				resources.TrustedCASecret = secretObj
				matched = true
			}
			if !matched {
				return nil, fmt.Errorf("secret %s does not belong to the Ironic resource", secretObj.Name)
			}
		}
	}

	for _, configMapObj := range configMaps {
		// Determine configmap type based on name patterns or labels
		matched := false
		if bmcCARef != nil && bmcCARef.Kind == metal3api.ResourceKindConfigMap && bmcCARef.Name == configMapObj.Name {
			resources.BMCCAConfigMap = configMapObj
			matched = true
		}
		if trustedCARef != nil && trustedCARef.Kind == metal3api.ResourceKindConfigMap && trustedCARef.Name == configMapObj.Name {
			resources.TrustedCAConfigMap = configMapObj
			matched = true
		}
		if !matched {
			return nil, fmt.Errorf("configmap %s does not belong to the Ironic resource", configMapObj.Name)
		}
	}

	// Generate API secret if not provided
	if resources.APISecret == nil {
		apiSecret, err := GenerateSecret(&resources.Ironic.ObjectMeta, "", true)
		if err != nil {
			return nil, fmt.Errorf("failed to generate API secret: %w", err)
		}

		// Podman does not support generateName, nor do we need it
		apiSecret.GenerateName = ""
		apiSecret.Name = resources.Ironic.Name + "-api-credentials"
		resources.APISecret = apiSecret
	}

	// Generate RPC credentials secret if not provided and the RPC bus is used
	// (the networking service requires it, HA is not supported locally)
	if resources.RPCSecret == nil && resources.Ironic.IsNetworkingServiceEnabled() {
		rpcSecret, err := GenerateRPCSecret(&resources.Ironic.ObjectMeta)
		if err != nil {
			return nil, fmt.Errorf("failed to generate RPC credentials secret: %w", err)
		}
		resources.RPCSecret = rpcSecret
	}

	return resources, nil
}

func checkAndUpdateIronicForLocal(ironicSpec *metal3api.IronicSpec) error {
	if ironicSpec.HighAvailability {
		return errors.New("highAvailability does not make sense for local installations")
	}

	net := &ironicSpec.Networking

	// It's not possible to use hostIP on podman, but localhost is a reasonable default for this case
	if net.IPAddress == "" && net.MACAddresses == nil {
		switch net.Interface {
		case "lo":
			return errors.New("setting interface to lo does not work")
		case "":
			net.IPAddress = "127.0.0.1"
		}
	}

	// There is no Kubernetes API to apply defaults
	if net.APIPort == 0 {
		net.APIPort = 6385
	}
	if net.ImageServerPort == 0 {
		net.ImageServerPort = 6180
	}
	if net.ImageServerTLSPort == 0 {
		net.ImageServerTLSPort = 6183
	}
	if net.RPCPort == 0 {
		net.RPCPort = 6189
	}

	return nil
}

func fixContainerForLocal(container corev1.Container) corev1.Container {
	// Ports are neither necessary nor supported with host networking
	container.Ports = nil

	return container
}

// GenerateLocalManifests creates Kubernetes manifests for local deployment.
func GenerateLocalManifests(cctx ControllerContext, resources *Resources) ([]runtime.Object, error) {
	if err := checkAndUpdateIronicForLocal(&resources.Ironic.Spec); err != nil {
		return nil, err
	}

	podTemplate, err := newIronicPodTemplate(cctx, *resources)
	if err != nil {
		return nil, fmt.Errorf("failed to generate pod template: %w", err)
	}

	newContainers := make([]corev1.Container, 0, len(podTemplate.Spec.Containers))
	for _, container := range podTemplate.Spec.Containers {
		newContainers = append(newContainers, fixContainerForLocal(container))
	}
	podTemplate.Spec.Containers = newContainers

	manifests := []runtime.Object{resources.APISecret}
	if resources.RPCSecret != nil {
		manifests = append(manifests, resources.RPCSecret)
	}
	if resources.TLSSecret != nil {
		manifests = append(manifests, resources.TLSSecret)
	}
	if resources.BMCCASecret != nil {
		manifests = append(manifests, resources.BMCCASecret)
	}
	if resources.BMCCAConfigMap != nil {
		manifests = append(manifests, resources.BMCCAConfigMap)
	}
	if resources.TrustedCASecret != nil {
		manifests = append(manifests, resources.TrustedCASecret)
	}
	if resources.TrustedCAConfigMap != nil {
		manifests = append(manifests, resources.TrustedCAConfigMap)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ironicDeploymentName(resources.Ironic),
			Namespace: resources.Ironic.Namespace,
		},
	}
	populateIronicDeployment(cctx, *resources, deployment, podTemplate)
	manifests = append(manifests, deployment)

	return manifests, nil
}

// splitYAMLDocuments splits a YAML file into separate documents.
func splitYAMLDocuments(data []byte) [][]byte {
	docs := [][]byte{}
	current := []byte{}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "---" && len(current) > 0 {
			docs = append(docs, current)
			current = []byte{}
		} else {
			current = append(current, []byte(line+"\n")...)
		}
	}

	if len(current) > 0 {
		docs = append(docs, current)
	}

	return docs
}
