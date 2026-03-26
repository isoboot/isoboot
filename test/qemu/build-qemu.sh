#!/usr/bin/env bash
# Build QEMU from source with the custom RTL8168 device and an iPXE EFI
# ROM so OVMF can PXE-boot through it.
# Usage: ./build-qemu.sh [qemu-version]
set -euo pipefail

QEMU_VERSION="${1:-8.2.2}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="/tmp/qemu-build"

echo "=== Building QEMU ${QEMU_VERSION} + iPXE ROM for RTL8168 ==="

sudo apt-get update -qq
sudo apt-get install -y -qq \
  build-essential ninja-build python3-venv pkg-config \
  libglib2.0-dev libpixman-1-dev libslirp-dev zlib1g-dev \
  liblzma-dev

# ── Build iPXE EFI ROM for PCI 10ec:8168 ────────────────────────
if [ ! -f /usr/local/share/qemu/efi-rtl8168.rom ]; then
  echo "--- Building iPXE EFI ROM ---"
  git clone --depth=1 https://github.com/ipxe/ipxe.git /tmp/ipxe
  make -C /tmp/ipxe/src -j"$(nproc)" bin-x86_64-efi/10ec8168.efirom
  sudo mkdir -p /usr/local/share/qemu
  sudo cp /tmp/ipxe/src/bin-x86_64-efi/10ec8168.efirom \
    /usr/local/share/qemu/efi-rtl8168.rom
  rm -rf /tmp/ipxe
fi

# ── Build QEMU with RTL8168 device ──────────────────────────────
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"
if [ ! -d "qemu-${QEMU_VERSION}" ]; then
  curl -sL "https://download.qemu.org/qemu-${QEMU_VERSION}.tar.xz" | tar xJ
fi
cd "qemu-${QEMU_VERSION}"

cp "$SCRIPT_DIR/rtl8168.c" hw/net/rtl8168.c

if ! grep -q 'rtl8168' hw/net/meson.build; then
  sed -i "/system_ss.add.*CONFIG_RTL8139_PCI/a system_ss.add(files('rtl8168.c'))" \
    hw/net/meson.build
fi

./configure \
  --target-list=x86_64-softmmu \
  --enable-kvm \
  --enable-slirp \
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

qemu-system-x86_64 --version
qemu-system-x86_64 -device help 2>&1 | grep -i rtl8168 || true
echo "iPXE ROM: $(ls -la /usr/local/share/qemu/efi-rtl8168.rom)"
