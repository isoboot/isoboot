#!/usr/bin/env bash
# Phase 5: install the chart into isoboot-system and wait for every component.
# E2E_IMAGES=ghcr installs the chart pushed to GHCR; local packages the chart
# from this checkout with version and appVersion E2E_VERSION.
# Usage: helm-install.sh <row-id>
set -euo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
load_row "${1:-}"

# nfsd needs TCP 111 (port mapper) and 2049 on the host. A runner's own
# rpcbind is runner setup, so stop it; anything else is a hard failure.
nfs_ports_busy() { sudo ss -Htlnp '( sport = :111 or sport = :2049 )' | grep .; }
if nfs_ports_busy >/dev/null; then
  log "TCP 111/2049 in use; stopping the host's rpcbind"
  sudo systemctl disable --now rpcbind.socket rpcbind.service 2>/dev/null || true
fi
if nfs_ports_busy; then
  fail "something on this host listens on TCP 111 or 2049 (above); nfsd needs both ports"
fi
pass "TCP 111 and 2049 are free for nfsd"

node=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
if [ "$E2E_IMAGES" = ghcr ]; then
  [ -n "$E2E_VERSION" ] || fail "E2E_VERSION must be set when E2E_IMAGES=ghcr"
  chart=(oci://ghcr.io/isoboot/charts/isoboot --version "$E2E_VERSION")
else
  rm -rf "$WORK/chart"
  helm package "$REPO_ROOT/charts/isoboot" --version "$E2E_VERSION" \
    --app-version "$E2E_VERSION" --destination "$WORK/chart" >/dev/null
  chart=("$WORK/chart/isoboot-$E2E_VERSION.tgz")
fi
values=(--set nodeName="$node" --set dnsmasq.subnet="$SUBNET"
  --set alpineImage="alpine:$ALPINE_VERSION" --set squid.log.access=true)

# A plain install, as a user would run it: the CRDs come from the chart's
# crds/ directory, which Helm installs and waits for before the templates and
# the post-install BootArtifact hook.
helm install "$RELEASE" "${chart[@]}" --namespace "$NS" --create-namespace \
  "${values[@]}"

wait_pods controller 300
wait_pods nginx 300
wait_pods httpd 120
kubectl get crd bootartifacts.isoboot.github.io bootconfigs.isoboot.github.io \
  machines.isoboot.github.io provisions.isoboot.github.io provisionautomations.isoboot.github.io
wait_ready bootartifact isoboot-ipxe
wait_pods dnsmasq 180
wait_pods nfsd 120
wait_pods squid 180

# An upgrade must be able to replace every pod. The node-pinned ones hold host
# ports or repel their own replacement, so a rolling update would hang: roll
# each Deployment once and wait for it.
deployments=$(kc get deployments -o name)
[ -n "$deployments" ] || fail "no Deployments in $NS to roll"
for deployment in $deployments; do
  kc rollout restart "$deployment" >/dev/null
done
for deployment in $deployments; do
  kc rollout status "$deployment" --timeout=300s
done
pass "every Deployment rolled out a replacement pod"
kc get pods -o wide
pass "isoboot $E2E_VERSION installed"
