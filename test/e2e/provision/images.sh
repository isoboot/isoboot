#!/usr/bin/env bash
# Phase 4: container images. E2E_IMAGES=ghcr (CI): nothing to do, k3s pulls
# the images the build job pushed. E2E_IMAGES=local: build the three images
# (isoboot with the manager, httpd and nfsd; dnsmasq; squid) from this checkout
# and import them into k3s under the tag E2E_VERSION.
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
# Builds fetch base images and packages over the network, so a flaky link
# (seen on a WiFi test box: apk add failing once) gets two more tries. A
# broken Dockerfile fails all three.
build() {
  local tag=$1 file=$2 attempt
  for attempt in 1 2 3; do
    log "Building $tag (attempt $attempt of 3)"
    if sudo DOCKER_BUILDKIT=1 docker build -q -f "$file" \
      --build-arg "ALPINE_VERSION=$ALPINE_VERSION" -t "$tag" . >/dev/null; then
      return 0
    fi
    [ "$attempt" = 3 ] || sleep 15
  done
  fail "could not build $tag in 3 attempts"
}
build "$IMAGE:$E2E_VERSION" Dockerfile
build "$IMAGE-dnsmasq:$E2E_VERSION" Dockerfile.dnsmasq
build "$IMAGE-squid:$E2E_VERSION" Dockerfile.squid

log "Importing images into k3s"
sudo docker save \
  "$IMAGE:$E2E_VERSION" "$IMAGE-dnsmasq:$E2E_VERSION" "$IMAGE-squid:$E2E_VERSION" \
  | sudo k3s ctr images import - >/dev/null
pass "local images $E2E_VERSION imported"
