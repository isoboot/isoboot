#!/usr/bin/env bash
# Phase 1: host packages, KVM access, the data directory, helm and, for rows
# with the RTL8168 NIC, the custom QEMU (test/qemu). Usage: host-setup.sh <row-id>
set -euo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
load_row "${1:-}"

log "Host: $(uname -srm), $(nproc) CPUs, $(free -m | awk '/^Mem:/ {print $2}') MB RAM"
df -h / | tail -1
[ -e /dev/kvm ] || fail "/dev/kvm is missing: KVM (nested virtualisation inside a VM) is required"

pkgs=(qemu-utils ovmf socat jq curl iptables)
[ "$(row nic)" = virtio ] && pkgs+=(qemu-system-x86 ipxe-qemu)
command -v docker >/dev/null || pkgs+=(docker.io docker-buildx)
sudo apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
  -o DPkg::Lock::Timeout=300 "${pkgs[@]}"
sudo systemctl enable --now docker >/dev/null 2>&1 || true
sudo docker info >/dev/null || fail "docker is not working"

# CI runner images ship their own helm; install the pinned one over it.
if [ "$(helm version --template '{{.Version}}' 2>/dev/null || true)" != "$HELM_VERSION" ]; then
  log "Installing helm $HELM_VERSION"
  tarball="helm-$HELM_VERSION-linux-amd64.tar.gz"
  rm -rf "$WORK/helm" && mkdir -p "$WORK/helm"
  curl -fsSLo "$WORK/helm/$tarball" "https://get.helm.sh/$tarball"
  sum=$(curl -fsSL "https://get.helm.sh/$tarball.sha256sum" | awk '{print $1}')
  [ -n "$sum" ] || fail "no published checksum for $tarball"
  echo "$sum  $WORK/helm/$tarball" | sha256sum -c --quiet - || fail "checksum mismatch for $tarball"
  tar -xzf "$WORK/helm/$tarball" -C "$WORK/helm" linux-amd64/helm
  sudo install -m 0755 "$WORK/helm/linux-amd64/helm" /usr/local/bin/helm
fi

echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' \
  | sudo tee /etc/udev/rules.d/99-kvm4all.rules >/dev/null
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=kvm

sudo mkdir -p "$DATA_DIR"
sudo chown 65532:65532 "$DATA_DIR"

if [ "$(row nic)" = rtl8168 ]; then
  # The RTL8168 model and its iPXE ROM are only needed by the Debian rows (#380).
  stamp=$(cat "$REPO_ROOT/test/qemu/build-qemu.sh" "$REPO_ROOT/test/qemu/rtl8168.c" | sha256sum | cut -d' ' -f1)
  if [ "$(cat "$E2E_QEMU_CACHE/stamp" 2>/dev/null || true)" != "$stamp" ]; then
    log "Building QEMU with the RTL8168 device (no cached build for $stamp)"
    "$REPO_ROOT/test/qemu/build-qemu.sh"
    rm -rf "$E2E_QEMU_CACHE"
    mkdir -p "$E2E_QEMU_CACHE/share"
    cp /usr/local/bin/qemu-system-x86_64 "$E2E_QEMU_CACHE/"
    cp -a /usr/local/share/qemu/. "$E2E_QEMU_CACHE/share/"
    echo "$stamp" > "$E2E_QEMU_CACHE/stamp"
  else
    log "Using cached RTL8168 QEMU build from $E2E_QEMU_CACHE"
  fi
  sudo install -m 0755 "$E2E_QEMU_CACHE/qemu-system-x86_64" /usr/local/bin/qemu-system-x86_64
  sudo mkdir -p /usr/local/share/qemu
  sudo cp -a "$E2E_QEMU_CACHE/share/." /usr/local/share/qemu/
  /usr/local/bin/qemu-system-x86_64 -device help 2>&1 | grep '"rtl8168"' >/dev/null \
    || fail "custom QEMU has no rtl8168 device"
  pass "custom QEMU with rtl8168: $(/usr/local/bin/qemu-system-x86_64 --version | sed -n 1p)"
else
  pass "stock QEMU: $(/usr/bin/qemu-system-x86_64 --version | sed -n 1p)"
fi
