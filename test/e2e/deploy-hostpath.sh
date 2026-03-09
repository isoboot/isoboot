#!/usr/bin/env bash
# Deploy the controller with the hostPath kustomize overlay.
# Usage: deploy-hostpath.sh <image>
set -euo pipefail

IMG=${1:?usage: deploy-hostpath.sh <image>}

make manifests kustomize

KUSTOMIZE="$(pwd)/bin/kustomize"
(cd config/manager && "$KUSTOMIZE" edit set image "controller=${IMG}")
"$KUSTOMIZE" build config/test-hostpath | kubectl apply -f -
