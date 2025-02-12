package v1alpha1

import "fmt"

const versionLatestString = "latest"

type Version struct {
	Major, Minor int
}

func (v Version) IsLatest() bool {
	return v.Major == 0
}

func (v Version) String() string {
	if v.IsLatest() {
		return versionLatestString
	}
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func ParseVersion(vs string) (result Version, err error) {
	if vs == "latest" {
		return Version{}, nil
	}

	_, err = fmt.Sscanf(vs, "%d.%d", &result.Major, &result.Minor)
	if err != nil {
		return Version{}, err
	}

	return result, nil
}

func MustParseVersion(vs string) Version {
	result, err := ParseVersion(vs)
	if err != nil {
		panic(fmt.Sprintf("must parse version %s: %s", vs, err))
	}

	return result
}

var (
	VersionLatest = MustParseVersion("latest")
	Version270    = MustParseVersion("27.0")
)

// Mapping of supported versions to container image tags.
// This mapping must be updated each time a new ironic-image branch is created.
// Also consider updating the version test(s) in test/suite_test.go to verify
// that the new version is installable and its API version matches
// expectations.
var SupportedVersions = map[Version]string{
	VersionLatest: "latest",
	Version270:    "release-27.0",
}
