package ironic

import (
	"encoding/json"
	"testing"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Helper function to create a RawExtension from any value.
func mustRawExtension(value interface{}) *runtime.RawExtension {
	bytes, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &runtime.RawExtension{Raw: bytes}
}

func TestBuildEndpoints(t *testing.T) {
	testCases := []struct {
		Scenario string

		IPs          []string
		Port         int
		IncludeProto string

		Expected []string
	}{
		{
			Scenario: "non-standard-port-no-protocol",

			IPs:          []string{"2001:db8::42", "192.0.2.42"},
			Port:         6385,
			IncludeProto: "",

			Expected: []string{"192.0.2.42:6385", "[2001:db8::42]:6385"},
		},
		{
			Scenario: "non-standard-port-with-protocol",

			IPs:          []string{"2001:db8::42", "192.0.2.42"},
			Port:         6385,
			IncludeProto: "http",

			Expected: []string{"http://192.0.2.42:6385", "http://[2001:db8::42]:6385"},
		},
		{
			Scenario: "http-with-protocol",

			IPs:          []string{"2001:db8::42", "192.0.2.42"},
			Port:         80,
			IncludeProto: "http",

			Expected: []string{"http://192.0.2.42", "http://[2001:db8::42]"},
		},
		{
			Scenario: "https-with-protocol",

			IPs:          []string{"2001:db8::42", "192.0.2.42"},
			Port:         443,
			IncludeProto: "https",

			Expected: []string{"https://192.0.2.42", "https://[2001:db8::42]"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			result := buildEndpoints(tc.IPs, tc.Port, tc.IncludeProto)
			assert.Equal(t, tc.Expected, result)
		})
	}
}

func TestApplyOverridesToPod(t *testing.T) {
	initial := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"key1": "value1"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "c1",
					Env: []corev1.EnvVar{
						{
							Name:  "env1",
							Value: "value1",
						},
					},
				},
				{Name: "c2"},
			},
		},
	}

	initialEnvs := map[string][]corev1.EnvVar{
		"c1": {
			{
				Name:  "env1",
				Value: "value1",
			},
		},
		"c2": nil,
	}

	testCases := []struct {
		Scenario string

		Overrides *metal3api.Overrides

		ExpectedAnnotations map[string]string
		ExpectedLabels      map[string]string
	}{
		{
			Scenario: "No overrides",

			ExpectedLabels: map[string]string{"key1": "value1"},
		},
		{
			Scenario:  "Empty overrides",
			Overrides: &metal3api.Overrides{},

			ExpectedLabels: map[string]string{"key1": "value1"},
		},
		{
			Scenario: "Keep builtin labels",
			Overrides: &metal3api.Overrides{
				Labels: map[string]string{"key1": "no value"},
			},

			ExpectedLabels: map[string]string{"key1": "value1"},
		},
		{
			Scenario: "New labels and annotations",
			Overrides: &metal3api.Overrides{
				Annotations: map[string]string{"key2": "value2"},
				Labels:      map[string]string{"key3": "value3"},
			},

			ExpectedAnnotations: map[string]string{"key2": "value2"},
			ExpectedLabels:      map[string]string{"key1": "value1", "key3": "value3"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			result := applyOverridesToPod(tc.Overrides, *initial.DeepCopy())

			var containerNames []string
			envs := make(map[string][]corev1.EnvVar)

			for _, cont := range result.Spec.Containers {
				containerNames = append(containerNames, cont.Name)
				envs[cont.Name] = cont.Env
			}

			// Will be test case specific in the future
			expectedContainerNames := []string{"c1", "c2"}
			expectedEnvs := initialEnvs

			assert.Equal(t, expectedContainerNames, containerNames)
			assert.Equal(t, tc.ExpectedAnnotations, result.Annotations)
			assert.Equal(t, tc.ExpectedLabels, result.Labels)
			assert.Equal(t, expectedEnvs, envs)
		})
	}
}

func TestApplyOverridesToPodWithPatches(t *testing.T) {
	baseContainer := corev1.Container{
		Name:  "test-container",
		Image: "test:latest",
		Env: []corev1.EnvVar{
			{Name: "ENV1", Value: "value1"},
		},
	}

	initContainer := corev1.Container{
		Name:  "init-container",
		Image: "init:latest",
	}

	initial := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"key1": "value1"},
		},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{initContainer},
			Containers:     []corev1.Container{baseContainer},
		},
	}

	testCases := []struct {
		name      string
		overrides *metal3api.Overrides
		validate  func(t *testing.T, result corev1.PodTemplateSpec)
	}{
		{
			name: "Add environment variable",
			overrides: &metal3api.Overrides{
				PatchContainers: []metal3api.PatchContainer{
					{
						Name: "test-container",
						Patch: []metal3api.JSONPatchOperation{
							{
								Op:   "add",
								Path: "/env/-",
								Value: mustRawExtension(map[string]string{
									"name":  "ENV2",
									"value": "value2",
								}),
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result corev1.PodTemplateSpec) {
				require.Len(t, result.Spec.Containers, 1)
				container := result.Spec.Containers[0]
				assert.Equal(t, "test-container", container.Name)
				require.Len(t, container.Env, 2)
				assert.Equal(t, "ENV1", container.Env[0].Name)
				assert.Equal(t, "value1", container.Env[0].Value)
				assert.Equal(t, "ENV2", container.Env[1].Name)
				assert.Equal(t, "value2", container.Env[1].Value)
			},
		},
		{
			name: "Replace image",
			overrides: &metal3api.Overrides{
				PatchContainers: []metal3api.PatchContainer{
					{
						Name: "test-container",
						Patch: []metal3api.JSONPatchOperation{
							{
								Op:    "replace",
								Path:  "/image",
								Value: mustRawExtension("new-image:v2"),
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result corev1.PodTemplateSpec) {
				require.Len(t, result.Spec.Containers, 1)
				container := result.Spec.Containers[0]
				assert.Equal(t, "test-container", container.Name)
				assert.Equal(t, "new-image:v2", container.Image)
				assert.Len(t, container.Env, 1) // Should preserve existing env
			},
		},
		{
			name: "Patch init container",
			overrides: &metal3api.Overrides{
				PatchContainers: []metal3api.PatchContainer{
					{
						Name: "init-container",
						Patch: []metal3api.JSONPatchOperation{
							{
								Op:    "replace",
								Path:  "/image",
								Value: mustRawExtension("new-init:v2"),
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result corev1.PodTemplateSpec) {
				require.Len(t, result.Spec.InitContainers, 1)
				container := result.Spec.InitContainers[0]
				assert.Equal(t, "init-container", container.Name)
				assert.Equal(t, "new-init:v2", container.Image)
				// Regular containers should be unchanged
				require.Len(t, result.Spec.Containers, 1)
				assert.Equal(t, "test:latest", result.Spec.Containers[0].Image)
			},
		},
		{
			name: "Patch non-existent container - should not affect anything",
			overrides: &metal3api.Overrides{
				PatchContainers: []metal3api.PatchContainer{
					{
						Name: "non-existent",
						Patch: []metal3api.JSONPatchOperation{
							{
								Op:    "replace",
								Path:  "/image",
								Value: mustRawExtension("should-not-apply"),
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result corev1.PodTemplateSpec) {
				// Should be unchanged
				require.Len(t, result.Spec.Containers, 1)
				assert.Equal(t, "test:latest", result.Spec.Containers[0].Image)
				require.Len(t, result.Spec.InitContainers, 1)
				assert.Equal(t, "init:latest", result.Spec.InitContainers[0].Image)
			},
		},
		{
			name: "Multiple patches",
			overrides: &metal3api.Overrides{
				PatchContainers: []metal3api.PatchContainer{
					{
						Name: "test-container",
						Patch: []metal3api.JSONPatchOperation{
							{
								Op:    "replace",
								Path:  "/image",
								Value: mustRawExtension("patched:v1"),
							},
						},
					},
					{
						Name: "init-container",
						Patch: []metal3api.JSONPatchOperation{
							{
								Op:    "replace",
								Path:  "/image",
								Value: mustRawExtension("patched-init:v1"),
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result corev1.PodTemplateSpec) {
				require.Len(t, result.Spec.Containers, 1)
				assert.Equal(t, "patched:v1", result.Spec.Containers[0].Image)
				require.Len(t, result.Spec.InitContainers, 1)
				assert.Equal(t, "patched-init:v1", result.Spec.InitContainers[0].Image)
			},
		},
		{
			name: "Empty patch list",
			overrides: &metal3api.Overrides{
				PatchContainers: []metal3api.PatchContainer{},
			},
			validate: func(t *testing.T, result corev1.PodTemplateSpec) {
				// Should be unchanged
				require.Len(t, result.Spec.Containers, 1)
				assert.Equal(t, "test:latest", result.Spec.Containers[0].Image)
				require.Len(t, result.Spec.InitContainers, 1)
				assert.Equal(t, "init:latest", result.Spec.InitContainers[0].Image)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := applyOverridesToPod(tc.overrides, *initial.DeepCopy())
			tc.validate(t, result)
		})
	}
}

func TestApplyJSONPatchToContainer(t *testing.T) {
	baseContainer := corev1.Container{
		Name:  "test",
		Image: "test:v1",
		Env: []corev1.EnvVar{
			{Name: "VAR1", Value: "value1"},
		},
	}

	testCases := []struct {
		name       string
		operations []metal3api.JSONPatchOperation
		expectErr  bool
		validate   func(t *testing.T, result corev1.Container)
	}{
		{
			name: "Replace image",
			operations: []metal3api.JSONPatchOperation{
				{
					Op:    "replace",
					Path:  "/image",
					Value: mustRawExtension("test:v2"),
				},
			},
			validate: func(t *testing.T, result corev1.Container) {
				assert.Equal(t, "test:v2", result.Image)
				assert.Equal(t, "test", result.Name)
				assert.Len(t, result.Env, 1)
			},
		},
		{
			name: "Add environment variable",
			operations: []metal3api.JSONPatchOperation{
				{
					Op:   "add",
					Path: "/env/-",
					Value: mustRawExtension(map[string]string{
						"name":  "VAR2",
						"value": "value2",
					}),
				},
			},
			validate: func(t *testing.T, result corev1.Container) {
				assert.Equal(t, "test:v1", result.Image)
				require.Len(t, result.Env, 2)
				assert.Equal(t, "VAR2", result.Env[1].Name)
				assert.Equal(t, "value2", result.Env[1].Value)
			},
		},
		{
			name: "Remove environment variable",
			operations: []metal3api.JSONPatchOperation{
				{
					Op:   "remove",
					Path: "/env/0",
				},
			},
			validate: func(t *testing.T, result corev1.Container) {
				assert.Equal(t, "test:v1", result.Image)
				assert.Empty(t, result.Env)
			},
		},
		{
			name: "Invalid path",
			operations: []metal3api.JSONPatchOperation{
				{
					Op:    "replace",
					Path:  "/nonexistent",
					Value: mustRawExtension("value"),
				},
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := applyJSONPatchToContainer(baseContainer, tc.operations)

			if tc.expectErr {
				require.Error(t, err)
				assert.Equal(t, baseContainer, result) // Should return original on error
			} else {
				require.NoError(t, err)
				tc.validate(t, result)
			}
		})
	}
}

func TestApplyContainerPatches(t *testing.T) {
	containers := []corev1.Container{
		{Name: "container1", Image: "image1:v1"},
		{Name: "container2", Image: "image2:v1"},
		{Name: "container3", Image: "image3:v1"},
	}

	testCases := []struct {
		name     string
		patches  []metal3api.PatchContainer
		validate func(t *testing.T, result []corev1.Container)
	}{
		{
			name:    "No patches",
			patches: nil,
			validate: func(t *testing.T, result []corev1.Container) {
				assert.Equal(t, containers, result)
			},
		},
		{
			name: "Patch single container",
			patches: []metal3api.PatchContainer{
				{
					Name: "container2",
					Patch: []metal3api.JSONPatchOperation{
						{
							Op:    "replace",
							Path:  "/image",
							Value: mustRawExtension("image2:v2"),
						},
					},
				},
			},
			validate: func(t *testing.T, result []corev1.Container) {
				require.Len(t, result, 3)
				assert.Equal(t, "image1:v1", result[0].Image)
				assert.Equal(t, "image2:v2", result[1].Image)
				assert.Equal(t, "image3:v1", result[2].Image)
			},
		},
		{
			name: "Patch multiple containers",
			patches: []metal3api.PatchContainer{
				{
					Name: "container1",
					Patch: []metal3api.JSONPatchOperation{
						{
							Op:    "replace",
							Path:  "/image",
							Value: mustRawExtension("image1:v2"),
						},
					},
				},
				{
					Name: "container3",
					Patch: []metal3api.JSONPatchOperation{
						{
							Op:    "replace",
							Path:  "/image",
							Value: mustRawExtension("image3:v2"),
						},
					},
				},
			},
			validate: func(t *testing.T, result []corev1.Container) {
				require.Len(t, result, 3)
				assert.Equal(t, "image1:v2", result[0].Image)
				assert.Equal(t, "image2:v1", result[1].Image)
				assert.Equal(t, "image3:v2", result[2].Image)
			},
		},
		{
			name: "Patch non-existent container",
			patches: []metal3api.PatchContainer{
				{
					Name: "non-existent",
					Patch: []metal3api.JSONPatchOperation{
						{
							Op:    "replace",
							Path:  "/image",
							Value: mustRawExtension("should-not-apply"),
						},
					},
				},
			},
			validate: func(t *testing.T, result []corev1.Container) {
				assert.Equal(t, containers, result)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := applyContainerPatches(tc.patches, containers)
			tc.validate(t, result)
		})
	}
}
