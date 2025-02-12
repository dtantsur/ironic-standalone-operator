package ironic

import (
	"fmt"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
)

const defaultRegistry = "quay.io/metal3-io"

// NOTE(dtantsur): DefaultVersion must be updated after branching
var DefaultVersion = metal3api.VersionLatest

var defaultMariaDBImage = fmt.Sprintf("%s/mariadb:latest", defaultRegistry)

type VersionInfo struct {
	InstalledVersion       metal3api.Version
	IronicImage            string
	MariaDBImage           string
	RamdiskDownloaderImage string
	AgentBranch            string
	AgentDownloadURL       string
	KeepalivedImage        string
}

// Helper to build a VersionInfo object for a given version and tag.
func buildVersionInfo(version metal3api.Version) VersionInfo {
	tag := metal3api.SupportedVersions[version]
	// NOTE(dtantsur): we don't have explicit support for IPA branches other than master yet.
	return VersionInfo{
		InstalledVersion: version,
		IronicImage:      fmt.Sprintf("%s/ironic:%s", defaultRegistry, tag),
		// MariaDBImage is not actually used here but is set for consistency.
		MariaDBImage:           defaultMariaDBImage,
		RamdiskDownloaderImage: fmt.Sprintf("%s/ironic-ipa-downloader:latest", defaultRegistry),
		KeepalivedImage:        fmt.Sprintf("%s/keepalived:latest", defaultRegistry),
	}
}

// Takes VersionInfo with defaults from the configuration and applies any overrides from the Ironic object.
// Explicit images from the Images object take priority. Otherwise, the defaults are taken from the hardcoded defaults for the given version.
func (versionInfo VersionInfo) WithIronicOverrides(ironic *metal3api.Ironic) (VersionInfo, error) {
	if ironic.Spec.Version != "" {
		parsedVersion, err := metal3api.ParseVersion(ironic.Spec.Version)
		if err != nil {
			return VersionInfo{}, err
		}
		versionInfo.InstalledVersion = parsedVersion
	}

	defaults := buildVersionInfo(versionInfo.InstalledVersion)
	images := &ironic.Spec.Images

	if images.DeployRamdiskBranch != "" {
		versionInfo.AgentBranch = images.DeployRamdiskBranch
	} else if versionInfo.AgentBranch == "" {
		versionInfo.AgentBranch = defaults.AgentBranch
	}

	if images.DeployRamdiskDownloader != "" {
		versionInfo.RamdiskDownloaderImage = images.DeployRamdiskDownloader
	} else if versionInfo.RamdiskDownloaderImage == "" {
		versionInfo.RamdiskDownloaderImage = defaults.RamdiskDownloaderImage
	}

	if images.Ironic != "" {
		versionInfo.IronicImage = images.Ironic
	} else if versionInfo.IronicImage == "" {
		versionInfo.IronicImage = defaults.IronicImage
	}

	if images.Keepalived != "" {
		versionInfo.KeepalivedImage = images.Keepalived
	} else if versionInfo.KeepalivedImage == "" {
		versionInfo.KeepalivedImage = defaults.KeepalivedImage
	}

	return versionInfo, nil
}

// Takes VersionInfo with defaults from the configuration and applies any overrides from the IronicDatabase object.
func (versionInfo VersionInfo) WithIronicDatabaseOverrides(db *metal3api.IronicDatabase) VersionInfo {
	if db.Spec.Image != "" {
		versionInfo.MariaDBImage = db.Spec.Image
	} else if versionInfo.MariaDBImage == "" {
		versionInfo.MariaDBImage = defaultMariaDBImage
	}

	return versionInfo
}
