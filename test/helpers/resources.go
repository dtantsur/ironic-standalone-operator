package helpers

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
)

var (
	customIronicImage        string
	CustomImageVersion       string
	customIPADownloaderImage string
	customKeepalivedImage    string
)

func LoadCustomImages() {
	customIronicImage = os.Getenv("IRONIC_CUSTOM_IMAGE")
	CustomImageVersion = os.Getenv("IRONIC_CUSTOM_VERSION")
	customIPADownloaderImage = os.Getenv("IPA_DOWNLOADER_CUSTOM_IMAGE")
	customKeepalivedImage = os.Getenv("KEEPALIVED_CUSTOM_IMAGE")
}

func UsesCustomImage() bool {
	return customIronicImage != "" || CustomImageVersion != ""
}

func SkipIfCustomImage() {
	GinkgoHelper()
	if UsesCustomImage() {
		Skip("skipping because a custom image is provided")
	}
}

func SkipIfVersionBefore(minVersion string) {
	GinkgoHelper()
	if CustomImageVersion == "" || CustomImageVersion == "latest" {
		return
	}
	custom, err := metal3api.ParseVersion(CustomImageVersion)
	Expect(err).NotTo(HaveOccurred(), "invalid IRONIC_CUSTOM_VERSION %q", CustomImageVersion)
	minimum := metal3api.MustParseVersion(minVersion)
	if custom.Compare(minimum) < 0 {
		Skip(fmt.Sprintf("skipping because custom image version %s is before %s", CustomImageVersion, minVersion))
	}
}

func NewTLSSecret(ctx context.Context, k8sClient client.Client, namespace, name string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
			},
		},
		Data: map[string][]byte{
			corev1.TLSCertKey:       ironicCertPEM,
			corev1.TLSPrivateKeyKey: ironicKeyPEM,
		},
		Type: corev1.SecretTypeTLS,
	}
	err := k8sClient.Create(ctx, secret)
	Expect(err).NotTo(HaveOccurred())
	return secret
}

func NewAuthSecret(ctx context.Context, k8sClient client.Client, namespace, name string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
			},
		},
		Data: map[string][]byte{
			corev1.BasicAuthUsernameKey: []byte("admin"),
			corev1.BasicAuthPasswordKey: []byte("test-password"),
		},
		Type: corev1.SecretTypeBasicAuth,
	}
	err := k8sClient.Create(ctx, secret)
	Expect(err).NotTo(HaveOccurred())
	return secret
}

// VerifyRPCSecretExists verifies that the automatically generated RPC bus
// credentials secret exists and is not shared with the API credentials.
func VerifyRPCSecretExists(ctx context.Context, clientset *kubernetes.Clientset, ironic *metal3api.Ironic) {
	By("verifying the RPC credentials secret")

	rpcSecret, err := clientset.CoreV1().Secrets(ironic.Namespace).Get(ctx, ironic.Name+"-rpc-credentials", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	Expect(rpcSecret.Labels["ironic.metal3.io/managed"]).To(Equal("true"))
	Expect(rpcSecret.Labels[metal3api.LabelEnvironmentName]).To(Equal(metal3api.LabelEnvironmentValue))
	Expect(rpcSecret.Data).To(HaveKey(corev1.BasicAuthUsernameKey))
	Expect(rpcSecret.Data).To(HaveKey(corev1.BasicAuthPasswordKey))
	Expect(rpcSecret.Data).To(HaveKey("htpasswd"))

	// The RPC credentials must not be reused from the API credentials
	apiSecret, err := clientset.CoreV1().Secrets(ironic.Namespace).Get(ctx, ironic.Spec.APICredentialsName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	Expect(rpcSecret.Data[corev1.BasicAuthPasswordKey]).ToNot(Equal(apiSecret.Data[corev1.BasicAuthPasswordKey]))
}

// VerifyRPCSecretGone verifies that the RPC credentials secret is cleaned up.
func VerifyRPCSecretGone(ctx context.Context, clientset *kubernetes.Clientset, namespace, ironicName string) {
	By("verifying the RPC credentials secret is cleaned up")

	secretName := ironicName + "-rpc-credentials"
	Eventually(func() bool {
		_, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		return err != nil && k8serrors.IsNotFound(err)
	}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())
}

func NewIronic(ctx context.Context, k8sClient client.Client, nname types.NamespacedName, spec metal3api.IronicSpec) *metal3api.Ironic {
	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nname.Name,
			Namespace: nname.Namespace,
		},
		Spec: spec,
	}

	if customIronicImage != "" {
		ironic.Spec.Images.Ironic = customIronicImage
	}
	if CustomImageVersion != "" {
		ironic.Spec.Version = CustomImageVersion
	}
	if customIPADownloaderImage != "" {
		ironic.Spec.Images.DeployRamdiskDownloader = customIPADownloaderImage
	}
	if customKeepalivedImage != "" {
		ironic.Spec.Images.Keepalived = customKeepalivedImage
	}

	err := k8sClient.Create(ctx, ironic)
	Expect(err).NotTo(HaveOccurred())
	return ironic
}
