#!/usr/bin/env bash
# Build QEMU from source with the custom RTL8168 device and an iPXE EFI
# ROM so OVMF can PXE-boot through it. Both sources are pinned and checked
# before anything is built: QEMU by version and the sha256 of its release
# tarball (taken from a tarball whose GPG signature checked out against
# QEMU's release key CEACC9E15534EBABB82D3FA03353C9CEF108B584), iPXE by tag
# and commit. The E2E cache key and stamp hash this file, so a new pin
# rebuilds. Usage: ./build-qemu.sh
set -euo pipefail

QEMU_VERSION=8.2.2
QEMU_SHA256=847346c1b82c1a54b2c38f6edbd85549edeb17430b7d4d3da12620e2962bc4f3
# The iPXE release the chart ships (dnsmasq.ipxe.url in values.yaml).
IPXE_TAG=v2.0.0
IPXE_COMMIT=12798ec29aa8a64d8675c4378b99f5fe28447afb
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT

echo "=== Building QEMU ${QEMU_VERSION} + iPXE ${IPXE_TAG} ROM for RTL8168 ==="

# ── Fetch and check the sources ─────────────────────────────────
curl -fsSLo "$BUILD_DIR/qemu.tar.xz" "https://download.qemu.org/qemu-${QEMU_VERSION}.tar.xz"
echo "$QEMU_SHA256  $BUILD_DIR/qemu.tar.xz" | sha256sum -c --quiet - \
  || { echo "FAIL: checksum mismatch for qemu-${QEMU_VERSION}.tar.xz" >&2; exit 1; }
git -c advice.detachedHead=false clone -q --depth=1 --branch "$IPXE_TAG" https://github.com/ipxe/ipxe.git "$BUILD_DIR/ipxe"
ipxe_commit=$(git -C "$BUILD_DIR/ipxe" rev-parse HEAD)
[ "$ipxe_commit" = "$IPXE_COMMIT" ] \
  || { echo "FAIL: iPXE $IPXE_TAG is commit $ipxe_commit, expected $IPXE_COMMIT" >&2; exit 1; }

sudo apt-get update -qq
sudo apt-get install -y -qq \
  build-essential ninja-build python3-venv pkg-config \
  libglib2.0-dev libpixman-1-dev libslirp-dev zlib1g-dev \
  liblzma-dev libpng-dev

# ── Build iPXE EFI ROM for PCI 10ec:8168 ────────────────────────
make -C "$BUILD_DIR/ipxe/src" -j"$(nproc)" bin-x86_64-efi/10ec8168.efirom \
  DEBUG=realtek,netdevice
sudo mkdir -p /usr/local/share/qemu
sudo cp "$BUILD_DIR/ipxe/src/bin-x86_64-efi/10ec8168.efirom" \
  /usr/local/share/qemu/efi-rtl8168.rom

# ── Build QEMU with RTL8168 device ──────────────────────────────
tar -xJf "$BUILD_DIR/qemu.tar.xz" -C "$BUILD_DIR"
cd "$BUILD_DIR/qemu-${QEMU_VERSION}"

cp "$SCRIPT_DIR/rtl8168.c" hw/net/rtl8168.c

if ! grep -q 'rtl8168' hw/net/meson.build; then
  sed -i "/system_ss.add.*CONFIG_RTL8139_PCI/a system_ss.add(files('rtl8168.c'))" \
    hw/net/meson.build
fi

# VNC and PNG: the E2E shows the VM's screen over VNC and saves it as PNG
# (lib.sh qemu_start and screendump).
./configure \
  --target-list=x86_64-softmmu \
  --enable-kvm \
  --enable-slirp \
  --enable-vnc \
  --enable-png \
  --disable-docs \
  --disable-gtk \
  --disable-sdl \
  --disable-opengl \
  --disable-virglrenderer \
  --disable-xkbcommon

ninja -C build -j"$(nproc)"

sudo cp build/qemu-system-x86_64 /usr/local/bin/qemu-system-x86_64
sudo cp -a pc-bios/*.bin pc-bios/*.rom pc-bios/keymaps /usr/local/share/qemu/ 2>/dev/null || true
sudo cp -a build/pc-bios/*.bin build/pc-bios/*.rom /usr/local/share/qemu/ 2>/dev/null || true

/usr/local/bin/qemu-system-x86_64 --version
/usr/local/bin/qemu-system-x86_64 -device help 2>&1 | grep '"rtl8168"' >/dev/null \
  || { echo "FAIL: built QEMU has no rtl8168 device (meson.build patch missed?)" >&2; exit 1; }
echo "iPXE ROM: $(ls -la /usr/local/share/qemu/efi-rtl8168.rom)"
