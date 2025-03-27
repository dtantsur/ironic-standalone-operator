# Ironic Standalone Operator releasing

This document details the steps to create a release for Ironic Standalone
Operator (IrSO).

**NOTE**: Always follow [release documentation from the main
branch][this-document]. Release documentation in release branches may be
outdated.

[this-document]: https://github.com/metal3-io/ironic-standalone-operator/blob/main/docs/releasing.md

## Before making a release

Things you should check before making a release:

- Check the
  [Metal3 release process](https://github.com/metal3-io/metal3-docs/blob/main/processes/releasing.md)
  for high-level process and possible follow-up actions.
- Use the `./hack/verify-release.sh` script as helper to identify possible
  issues to be addressed before creating any release tags. You will need to
  generate an access token on Github to verify the release information.
- Add any missing versions and their branch names to the [list of versions
  in the API][supported-versions].
- Extend the [functional tests suite][suite_test] with new tests if needed:
  - update [the list of known Ironic API versions][known-api-versions] using
    the [Ironic API versions listing][api-versions-list].
  - upgrade from the newest version to `latest` with and without HA
  - upgrade to the newest version from the one before it (with and without HA)
  Hint: you can usually copy existing tests, modifying only the `spec.version`
  field and the API versions on the `TestAssumptions` structure.

[supported-versions]: https://github.com/metal3-io/ironic-standalone-operator/blob/e576bce1aea0a1dc198c0f15f006c8e56d6271f4/api/v1alpha1/ironic_types.go#L23-L38
[suite_test]: https://github.com/metal3-io/ironic-standalone-operator/blob/main/test/suite_test.go
[known-api-versions]: https://github.com/metal3-io/ironic-standalone-operator/blob/e576bce1aea0a1dc198c0f15f006c8e56d6271f4/test/suite_test.go#L59-L69
[api-versions-list]: https://docs.openstack.org/ironic/latest/contributor/webapi-version-history.html

## Permissions

Creating a release requires repository `write` permissions for:

- Tag pushing
- Branch creation
- GitHub Release publishing

These permissions are implicit for the org admins and repository admins. Release
team member gets his/her permissions via `metal3-release-team` membership. This
GitHub team has the required permissions in each repository required to release
IrSO. Adding person to the team gives him/her the necessary rights in all
relevant repositories in the organization. Individual persons should not be
given permissions directly.

## Process

IrSO uses [semantic versioning](https://semver.org).

- Regular releases: `v0.x.y`
- Beta releases: `v0.x.y-beta.z`
- Release candidate releases: `v0.x.y-rc.z`

### Repository setup

Clone the repository: `git clone git@github.com:metal3-io/ironic-standalone-operator`

or if using existing repository, verify your intended remote is set to
`metal3-io`: `git remote -v`. For this document, we assume it is `origin`.

- If creating a new minor branch, identify the commit you wish to create the
  branch from, and create a branch `release-0.x`:
  `git checkout <sha> -b release-0.x` and push it to remote:
  `git push origin release-0.x` to create it
- If creating a new patch release, use existing branch `release-0.x`:
  `git checkout origin/release-0.x`

### Prepare branch

The following actions must be taken in a commit on the freshly created branch
(**not** on the main branch) before tagging:

- Change [the default Ironic version][default-version] to the most recent
  version of the Ironic image.
- Change the branch of IrSO itself from `latest` to `release-0.x` in two
  places: [IMG in Makefile][img-makefile] and
  [Kustomize configuration][kustomize].

[default-version]: https://github.com/metal3-io/ironic-standalone-operator/blob/e576bce1aea0a1dc198c0f15f006c8e56d6271f4/pkg/ironic/version.go#L14-L15
[img-makefile]: https://github.com/metal3-io/ironic-standalone-operator/blob/e576bce1aea0a1dc198c0f15f006c8e56d6271f4/Makefile#L74-L75
[kustomize]: https://github.com/metal3-io/ironic-standalone-operator/blob/e576bce1aea0a1dc198c0f15f006c8e56d6271f4/config/manager/manager.yaml#L47

### Tags

First we create a primary release tag, that triggers release note creation and
image building processes.

- Create a signed, annotated tag with: `git tag -s -a v0.x.y -m v0.x.y`
- Push the tags to the GitHub repository: `git push origin v0.x.y`

This triggers two things:

- GitHub action workflow for automated release process creates a draft release
  in GitHub repository with correct content, comparing the pushed tag to
  previous tag. Running actions are visible on the
  [Actions](https://github.com/metal3-io/ironic-standalone-operator/actions)
  page, and draft release will be visible on top of the
  [Releases](https://github.com/metal3-io/ironic-standalone-operator/releases)
  page.
- GH action `build-images-action` starts building release image with the release
  tag in Jenkins, and it gets pushed to Quay. Make sure the release tag is
  visible in
  [Quay tags page](https://quay.io/repository/metal3-io/ironic-standalone-operator?tab=tags).
  If the release tag build is not visible, check if the action has failed and
  retrigger as necessary.

We also need to create one or more tags for the Go modules ecosystem:

- For any subdirectory with `go.mod` in it, create another Git tag with
  directory prefix, i.e. `git tag apis/v0.x.y`, and `git tag test/v0.x.y`. This
  enables the tags to be used as a Gomodule version for any downstream users.

  **NOTE**: Do not create annotated tags (`-a`, or implicitly via `-m` or `-s`)
  for Go modules. Release notes expects only the main tag to be annotated,
  otherwise it might create incorrect release notes.

### Release notes

Next step is to clean up the release note manually. Release note has been
generated by the `release` action, do not click the `Generate release notes`
button. In case there is issue with release action, you may rerun it via
`Actions` tab, or you can `make release-notes` to get a markdown file with
the release content to be inserted.

- If release is not a beta or release candidate, check for duplicates, reverts,
  and incorrect classifications of PRs, and whatever release creation tagged to
  be manually checked.
  - For any superseded PRs (like same dependency uplifted multiple times, or
    commit revertions) that provide no value to the release, move them to
    Superseded section. This way the changes are acknowledged to be part of the
    release, but not overwhelming the important changes contained by the
    release.
- If the release you're making is not a new major release, new minor release,
  or a new patch release from the latest release branch, uncheck the box for
  latest release.
- If it is a release candidate (RC) or a beta release, tick pre-release box.
- Save the release note as a draft, and have others review it.

### Release artifacts

We need to verify all release artifacts are correctly built or generated by the
release workflow. For a release, we should have the following artifacts:

We can use `./hack/verify-release.sh` to check for existence of release artifacts,
which should include the following:

Git tags pushed:

- Primary release tag: `v0.x.y`
- Go module tags: `apis/v0.x.y` and `test/v0.x.y`

Container image built and tagged at Quay registry:

- [ironic-standalone-operator:v0.x.y](https://quay.io/repository/metal3-io/ironic-standalone-operator?tab=tags)

Files included in the release page:

- Source code

### Make the release

After everything is checked out, hit the `Publish` button your GitHub draft
release!

## Post-release actions for new release branches

Some post-release actions are needed if new minor or major branch was created.

### Branch protection rules

Branch protection rules need to be applied to the new release branch. Copy the
settings after the previous release branch, with the exception of
`Required tests` selection. Required tests can only be selected after new
keywords are implemented in Jenkins JJB, and project-infra, and have been run at
least once in the PR targeting the branch in question. Branch protection rules
require user to have `admin` permissions in the repository.

## Additional actions outside this repository

Further additional actions are required in the Metal3 project after IrSO release.
For that, please continue following the instructions provided in
[Metal3 release process](https://github.com/metal3-io/metal3-docs/blob/main/processes/releasing.md)
