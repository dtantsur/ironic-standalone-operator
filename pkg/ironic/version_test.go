package ironic

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
)

func TestWithIronicOverrides(t *testing.T) {
	testCases := []struct {
		Scenario string

		Configured VersionInfo
		Ironic     metal3api.Ironic

		Expected VersionInfo
	}{
		{
			Scenario: "only defaults",

			Expected: VersionInfo{
				// NOTE(dtantsur): this value will change on stable branches
				InstalledVersion:       metal3api.MustParseVersion("latest"),
				IronicImage:            "quay.io/metal3-io/ironic:latest",
				KeepalivedImage:        "quay.io/metal3-io/keepalived:latest",
				RamdiskDownloaderImage: "quay.io/metal3-io/ironic-ipa-downloader:latest",
			},
		},
		{
			Scenario: "explicit overrides",

			Ironic: metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					Images: metal3api.Images{
						DeployRamdiskBranch:     "stable/x.y",
						DeployRamdiskDownloader: "myorg/ramdisk-downloader:tag",
						Ironic:                  "myorg/ironic:tag",
						Keepalived:              "myorg/keepalived:tag",
					},
				},
			},

			Expected: VersionInfo{
				AgentBranch: "stable/x.y",
				// NOTE(dtantsur): this value will change on stable branches
				InstalledVersion:       metal3api.MustParseVersion("latest"),
				IronicImage:            "myorg/ironic:tag",
				KeepalivedImage:        "myorg/keepalived:tag",
				RamdiskDownloaderImage: "myorg/ramdisk-downloader:tag",
			},
		},
		{
			Scenario: "older version",

			Ironic: metal3api.Ironic{
				Spec: metal3api.IronicSpec{
					Version: "27.0",
				},
			},

			Expected: VersionInfo{
				InstalledVersion:       metal3api.MustParseVersion("27.0"),
				IronicImage:            "quay.io/metal3-io/ironic:release-27.0",
				KeepalivedImage:        "quay.io/metal3-io/keepalived:latest",
				RamdiskDownloaderImage: "quay.io/metal3-io/ironic-ipa-downloader:latest",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			result, err := tc.Configured.WithIronicOverrides(&tc.Ironic)
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, result)
		})
	}
}
