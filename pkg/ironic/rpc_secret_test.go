package ironic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
)

func newRPCSecretTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, metal3api.AddToScheme(scheme))
	return scheme
}

func newRPCSecretTestContext(t *testing.T, objects ...client.Object) (ControllerContext, client.Client) {
	t.Helper()
	scheme := newRPCSecretTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return ControllerContext{
		Context: t.Context(),
		Client:  c,
		Scheme:  scheme,
	}, c
}

func TestRPCCredentialsSecretName(t *testing.T) {
	ironic := &metal3api.Ironic{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
	}
	assert.Equal(t, "test-ironic-rpc-credentials", rpcCredentialsSecretName(ironic))
}

func TestRPCSecretRequired(t *testing.T) {
	testCases := []struct {
		Scenario string
		Spec     metal3api.IronicSpec
		Expected bool
	}{
		{
			Scenario: "standard (no HA, no networking)",
			Expected: false,
		},
		{
			Scenario: "HA enabled",
			Spec:     metal3api.IronicSpec{HighAvailability: true},
			Expected: true,
		},
		{
			Scenario: "networking service enabled",
			Spec: metal3api.IronicSpec{
				NetworkingService: &metal3api.NetworkingService{Enabled: true},
			},
			Expected: true,
		},
		{
			Scenario: "networking service disabled",
			Spec: metal3api.IronicSpec{
				NetworkingService: &metal3api.NetworkingService{Enabled: false},
			},
			Expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			ironic := &metal3api.Ironic{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
				Spec:       tc.Spec,
			}
			assert.Equal(t, tc.Expected, RPCSecretRequired(ironic))
		})
	}
}

func TestEnsureRPCSecret(t *testing.T) {
	t.Run("creates a new secret when the RPC bus is used", func(t *testing.T) {
		ironic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns", UID: "abc-123"},
			Spec:       metal3api.IronicSpec{HighAvailability: true},
		}
		cctx, c := newRPCSecretTestContext(t)

		secret, err := EnsureRPCSecret(cctx, ironic, c)
		require.NoError(t, err)
		require.NotNil(t, secret)

		assert.Equal(t, "test-ironic-rpc-credentials", secret.Name)
		assert.Equal(t, "test-ns", secret.Namespace)
		assert.Equal(t, corev1.SecretTypeBasicAuth, secret.Type)
		assert.Equal(t, "test-ironic", string(secret.Data[corev1.BasicAuthUsernameKey]))
		assert.NotEmpty(t, secret.Data[corev1.BasicAuthPasswordKey])
		assert.NotEmpty(t, secret.Data[htpasswdKey])

		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, "test-ironic", secret.OwnerReferences[0].Name)
		assert.Equal(t, "true", secret.Labels[managedSecretLabel])
		assert.Equal(t, metal3api.LabelEnvironmentValue, secret.Labels[metal3api.LabelEnvironmentName])
	})

	t.Run("re-uses an existing secret without changing the credentials", func(t *testing.T) {
		ironic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
			Spec: metal3api.IronicSpec{
				NetworkingService: &metal3api.NetworkingService{Enabled: true},
			},
		}
		existingHash, err := generateHtpasswd([]byte("test-ironic"), []byte("existing-password"))
		require.NoError(t, err)
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-rpc-credentials",
				Namespace: "test-ns",
				Labels: map[string]string{
					metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
				},
			},
			Data: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("test-ironic"),
				corev1.BasicAuthPasswordKey: []byte("existing-password"),
				htpasswdKey:                 []byte(existingHash),
			},
			Type: corev1.SecretTypeBasicAuth,
		}
		cctx, c := newRPCSecretTestContext(t, existing)

		secret, err := EnsureRPCSecret(cctx, ironic, c)
		require.NoError(t, err)
		require.NotNil(t, secret)

		assert.Equal(t, []byte("existing-password"), secret.Data[corev1.BasicAuthPasswordKey])
		assert.Equal(t, []byte(existingHash), secret.Data[htpasswdKey])
	})

	t.Run("fills in a missing htpasswd in an existing secret", func(t *testing.T) {
		ironic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
			Spec:       metal3api.IronicSpec{HighAvailability: true},
		}
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-rpc-credentials",
				Namespace: "test-ns",
				Labels: map[string]string{
					metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
				},
			},
			Data: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("test-ironic"),
				corev1.BasicAuthPasswordKey: []byte("existing-password"),
			},
			Type: corev1.SecretTypeBasicAuth,
		}
		cctx, c := newRPCSecretTestContext(t, existing)

		secret, err := EnsureRPCSecret(cctx, ironic, c)
		require.NoError(t, err)
		require.NotNil(t, secret)

		assert.NotEmpty(t, secret.Data[htpasswdKey])
		assert.Contains(t, string(secret.Data[htpasswdKey]), "test-ironic:")
	})

	t.Run("returns nil and deletes the secret when the RPC bus is not used", func(t *testing.T) {
		ironic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
		}
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-rpc-credentials",
				Namespace: "test-ns",
				Labels: map[string]string{
					managedSecretLabel:             "true",
					metal3api.LabelEnvironmentName: metal3api.LabelEnvironmentValue,
				},
			},
			Data: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("test-ironic"),
				corev1.BasicAuthPasswordKey: []byte("password"),
			},
		}
		cctx, c := newRPCSecretTestContext(t, existing)

		secret, err := EnsureRPCSecret(cctx, ironic, c)
		require.NoError(t, err)
		assert.Nil(t, secret)

		getErr := c.Get(t.Context(), client.ObjectKey{Namespace: "test-ns", Name: "test-ironic-rpc-credentials"}, &corev1.Secret{})
		assert.True(t, k8serrors.IsNotFound(getErr))
	})

	t.Run("rejects an existing secret without the environment label", func(t *testing.T) {
		ironic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
			Spec:       metal3api.IronicSpec{HighAvailability: true},
		}
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-rpc-credentials",
				Namespace: "test-ns",
			},
			Data: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("test-ironic"),
				corev1.BasicAuthPasswordKey: []byte("password"),
			},
		}
		cctx, c := newRPCSecretTestContext(t, existing)

		_, err := EnsureRPCSecret(cctx, ironic, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test-ironic-rpc-credentials")
	})
}

func TestEnsureRPCSecretDeleted(t *testing.T) {
	t.Run("deletes an operator-managed secret", func(t *testing.T) {
		ironic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
		}
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-rpc-credentials",
				Namespace: "test-ns",
				Labels:    map[string]string{managedSecretLabel: "true"},
			},
		}
		cctx, c := newRPCSecretTestContext(t, existing)

		require.NoError(t, EnsureRPCSecretDeleted(cctx, ironic))

		getErr := c.Get(t.Context(), client.ObjectKey{Namespace: "test-ns", Name: "test-ironic-rpc-credentials"}, &corev1.Secret{})
		assert.True(t, k8serrors.IsNotFound(getErr))
	})

	t.Run("keeps an externally-managed secret", func(t *testing.T) {
		ironic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
		}
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ironic-rpc-credentials",
				Namespace: "test-ns",
			},
		}
		cctx, c := newRPCSecretTestContext(t, existing)

		require.NoError(t, EnsureRPCSecretDeleted(cctx, ironic))

		secret := &corev1.Secret{}
		getErr := c.Get(t.Context(), client.ObjectKey{Namespace: "test-ns", Name: "test-ironic-rpc-credentials"}, secret)
		require.NoError(t, getErr)
	})

	t.Run("no-op when the secret does not exist", func(t *testing.T) {
		ironic := &metal3api.Ironic{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"},
		}
		cctx, _ := newRPCSecretTestContext(t)

		require.NoError(t, EnsureRPCSecretDeleted(cctx, ironic))
	})
}

func TestGenerateRPCSecret(t *testing.T) {
	owner := &metav1.ObjectMeta{Name: "test-ironic", Namespace: "test-ns"}

	secret, err := GenerateRPCSecret(owner)
	require.NoError(t, err)

	assert.Equal(t, "test-ironic-rpc-credentials", secret.Name)
	assert.Empty(t, secret.GenerateName)
	assert.Equal(t, "test-ns", secret.Namespace)
	assert.Equal(t, "test-ironic", string(secret.Data[corev1.BasicAuthUsernameKey]))
	assert.NotEmpty(t, secret.Data[corev1.BasicAuthPasswordKey])
	assert.NotEmpty(t, secret.Data[htpasswdKey])
	assert.Equal(t, metal3api.LabelEnvironmentValue, secret.Labels[metal3api.LabelEnvironmentName])
}
