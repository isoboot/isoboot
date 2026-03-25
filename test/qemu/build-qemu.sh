#!/usr/bin/env bash
# Build QEMU from source with the custom RTL8168 device.
# Usage: ./build-qemu.sh [qemu-version]
#   Produces: /usr/local/bin/qemu-system-x86_64
set -euo pipefail

QEMU_VERSION="${1:-8.2.2}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="/tmp/qemu-build"

echo "=== Building QEMU ${QEMU_VERSION} with RTL8168 device ==="

# Install build dependencies
sudo apt-get update -qq
sudo apt-get install -y -qq \
  build-essential ninja-build python3-venv pkg-config \
  libglib2.0-dev libpixman-1-dev libslirp-dev zlib1g-dev

# Download and extract QEMU source
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"
if [ ! -d "qemu-${QEMU_VERSION}" ]; then
  curl -sL "https://download.qemu.org/qemu-${QEMU_VERSION}.tar.xz" \
    | tar xJ
fi
cd "qemu-${QEMU_VERSION}"

# Copy our RTL8168 device into the QEMU source tree
cp "$SCRIPT_DIR/rtl8168.c" hw/net/rtl8168.c

# Patch meson.build to include our device
if ! grep -q 'rtl8168' hw/net/meson.build; then
  # Add rtl8168.c to the system softmmu network devices
  sed -i "/^softmmu_ss.add.*eepro100/a softmmu_ss.add(files('rtl8168.c'))" \
    hw/net/meson.build
  echo "Patched hw/net/meson.build"
fi

# Configure — x86_64 softmmu only, minimal features for speed
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

# Build (parallel)
ninja -C build -j"$(nproc)"

# Install
sudo cp build/qemu-system-x86_64 /usr/local/bin/qemu-system-x86_64
sudo cp -r build/pc-bios /usr/local/share/qemu 2>/dev/null || true

echo "=== QEMU ${QEMU_VERSION} with RTL8168 installed ==="
qemu-system-x86_64 --version
echo "=== Available devices ==="
qemu-system-x86_64 -device help 2>&1 | grep -i rtl || echo "RTL8168 device check: run with -device rtl8168,help"
