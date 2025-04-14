#!/usr/bin/env bash

set -euo pipefail

# Run e2e tests against nodes deployed to a kind cluster.

# TODO(marun)
# - Support testing against a remote cluster

if ! [[ "$0" =~ scripts/tests.e2e.kube.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"

./bin/tmpnetctl start-kind-cluster

if [[ -z "${SKIP_BUILD_IMAGE:-}" ]]; then
  bash -x ./scripts/build_xsvm_image.sh
fi

E2E_SERIAL=1 PATH="${PWD}/bin:$PATH" bash -x ./scripts/tests.e2e.sh --runtime=kube --kube-image=localhost:5001/avalanchego-xsvm
