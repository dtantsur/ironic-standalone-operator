package ironic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
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
			Scenario: "Overrides",
			Ironic: metal3api.IronicSpec{
				Overrides: &metal3api.Overrides{
					InitContainers: []corev1.Container{
						{
							Name: "new-init",
						},
					},
					Containers: []corev1.Container{
						{
							Name: "new-1",
						},
						{
							Name: "new-2",
						},
					},
				},
			},
			ExpectedContainerNames:     []string{"httpd", "ironic", "ramdisk-logs", "new-1", "new-2"},
			ExpectedInitContainerNames: []string{"ramdisk-downloader", "new-init"},
		},
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

			podTemplate, err := newIronicPodTemplate(cctx, ironic, nil, secret, "test-domain.example.com")
			if tc.ExpectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.ExpectedError)
			}

			var containerNames []string
			for _, cont := range podTemplate.Spec.Containers {
				containerNames = append(containerNames, cont.Name)
			}

			assert.ElementsMatch(t, tc.ExpectedContainerNames, containerNames)
		})
	}
}
