#!/usr/bin/env bash
# Phase 4: container images. E2E_IMAGES=ghcr (CI): nothing to do, k3s pulls
# the images the build job pushed. E2E_IMAGES=local: build all five images
# from this checkout and import them into k3s under the tag E2E_VERSION.
# Usage: images.sh <row-id>
set -euo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
load_row "${1:-}"

if [ "$E2E_IMAGES" = ghcr ]; then
  [ -n "$E2E_VERSION" ] || fail "E2E_VERSION must be set when E2E_IMAGES=ghcr"
  log "Using images from GHCR, tag $E2E_VERSION"
  exit 0
fi

cd "$REPO_ROOT"
build() {
  local tag=$1 file=$2
  log "Building $tag"
  sudo DOCKER_BUILDKIT=1 docker build -q -f "$file" \
    --build-arg "ALPINE_VERSION=$ALPINE_VERSION" -t "$tag" . >/dev/null
}
build "$IMAGE:$E2E_VERSION" Dockerfile
build "$IMAGE-httpd:$E2E_VERSION" Dockerfile.httpd
build "$IMAGE-dnsmasq:$E2E_VERSION" Dockerfile.dnsmasq
build "$IMAGE-squid:$E2E_VERSION" Dockerfile.squid
build "$IMAGE-nfsd:$E2E_VERSION" Dockerfile.nfsd

log "Importing images into k3s"
sudo docker save \
  "$IMAGE:$E2E_VERSION" "$IMAGE-httpd:$E2E_VERSION" "$IMAGE-dnsmasq:$E2E_VERSION" \
  "$IMAGE-squid:$E2E_VERSION" "$IMAGE-nfsd:$E2E_VERSION" \
  | sudo k3s ctr images import - >/dev/null
pass "local images $E2E_VERSION imported"
