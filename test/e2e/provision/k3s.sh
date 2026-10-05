#!/usr/bin/env bash
# Phase 2: single-node k3s (no traefik), waited on until pod networking works.
# Usage: k3s.sh <row-id>
set -euo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
load_row "${1:-}"

if ! command -v k3s >/dev/null; then
  # The install script from the release tag, checked against its pinned hash;
  # it checks the release's published sha256 for the k3s binary.
  installer=$WORK/k3s-install.sh
  curl -fsSLo "$installer" "https://raw.githubusercontent.com/k3s-io/k3s/${K3S_VERSION/+/%2B}/install.sh"
  echo "$K3S_INSTALL_SHA256  $installer" | sha256sum -c --quiet - \
    || fail "checksum mismatch for the k3s $K3S_VERSION install script"
  INSTALL_K3S_EXEC="--disable=traefik" INSTALL_K3S_VERSION="$K3S_VERSION" sh "$installer"
fi
installed=$(k3s --version | awk 'NR == 1 {print $3}')
[ "$installed" = "$K3S_VERSION" ] || fail "k3s $installed is installed, expected $K3S_VERSION"
sudo chmod 644 /etc/rancher/k3s/k3s.yaml

# "kubectl wait" fails at once while no node is registered yet, so wait for one first.
for i in $(seq 1 90); do
  [ -n "$(kubectl get nodes -o name 2>/dev/null)" ] && break
  sleep 2
done
kubectl wait --for=condition=Ready node --all --timeout=180s

# #381: pods created before flannel has written subnet.env fail with
# "failed to load flannel 'subnet.env' file". Wait for it, then for CoreDNS.
for i in $(seq 1 90); do
  [ -s /run/flannel/subnet.env ] && break
  [ "$i" = 90 ] && fail "flannel did not write /run/flannel/subnet.env within 180 s"
  sleep 2
done
for i in $(seq 1 90); do
  kubectl -n kube-system get deployment coredns >/dev/null 2>&1 && break
  sleep 2
done
kubectl -n kube-system rollout status deployment/coredns --timeout=300s
pass "k3s $(k3s --version | awk 'NR == 1 {print $3}') ready, node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')"
