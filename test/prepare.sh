#!/usr/bin/env bash

set -eux -o pipefail

REPO_ROOT=$(realpath "$(dirname "${BASH_SOURCE[0]}")/..")
cd "${REPO_ROOT}"
mkdir -p bin

IMG="${IMG:-localhost/controller:test}"
LOGDIR="${LOGDIR:-/tmp/logs}"
CONTAINER_RUNTIME="${CONTAINER_RUNTIME:-}"

HELM_VERSION=3.17.3
HELM_CHECKSUM=ee88b3c851ae6466a3de507f7be73fe94d54cbf2987cbaa3d1a3832ea331f2cd
HELM_FILE="helm-v${HELM_VERSION}-linux-amd64.tar.gz"

CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-1.17.1}"
CLUSTER_TYPE="${CLUSTER_TYPE:-kind}"


. test/testing.env

mkdir -p "${LOGDIR}"

if [[ -z "${CONTAINER_RUNTIME}" ]]; then
    if command -v podman &> /dev/null;  then
        CONTAINER_RUNTIME=podman
    else
        CONTAINER_RUNTIME=docker
    fi
fi

pushd /tmp
curl -OL "https://get.helm.sh/${HELM_FILE}"
echo "${HELM_CHECKSUM} ${HELM_FILE}" | sha256sum -c
tar -xzf "${HELM_FILE}"
HELM="${REPO_ROOT}/bin/helm"
mv linux-amd64/helm "${HELM}"
popd

# Installing cert-manager

"${HELM}" repo add jetstack https://charts.jetstack.io --force-update
"${HELM}" install cert-manager jetstack/cert-manager \
  --namespace cert-manager --create-namespace \
  --version "v${CERT_MANAGER_VERSION}" --set crds.enabled=true

# Caching required images

image_load() {
    local image="$1"
    local archive="$(mktemp --suffix=.tar)"
    "${CONTAINER_RUNTIME}" save "${image}" > "${archive}"
    if [[ "${CLUSTER_TYPE}" == "kind" ]]; then
        kind load image-archive -v 2 "${archive}"
    else
        minikube image load --logtostderr "${archive}"
    fi
    rm -f "${archive}"
}

for image in ironic mariadb ironic-ipa-downloader; do
    if [[ "${CLUSTER_TYPE}" == "kind" ]]; then
        "${CONTAINER_RUNTIME}" pull "quay.io/metal3-io/${image}"
        image_load "quay.io/metal3-io/${image}"
    else
        minikube image pull --logtostderr "quay.io/metal3-io/${image}"
    fi
done

# Building and installing the operator

"${CONTAINER_RUNTIME}" build -t "${IMG}" . 2>&1 | tee "${LOGDIR}/docker-build.log"
image_load "${IMG}" 2>&1

make install deploy IMG="${IMG}" DEPLOY_TARGET=testing

kubectl wait --for=condition=Available --timeout=60s \
    -n ironic-standalone-operator-system deployment/ironic-standalone-operator-controller-manager

# Preparing the TLS certificate

SUBJECT_ALT_NAME="IP:${PROVISIONING_IP}"
if [[ "${CLUSTER_TYPE}" == kind ]]; then
    SUBJECT_ALT_NAME+=",IP:${IRONIC_IP}"
else
    for node_ip in $(minikube node list | awk '{ print $2; }'); do
        SUBJECT_ALT_NAME+=",IP:${node_ip}"
    done
fi
openssl req -x509 -new -subj "/CN=ironic" \
    -addext "subjectAltName = ${SUBJECT_ALT_NAME}" \
    -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
    -keyout "${IRONIC_KEY_FILE}" -out "${IRONIC_CERT_FILE}"
