#!/bin/bash
set -euo pipefail

# Tool versions, pinned so every devcontainer build gets the same tools.
# Bump them here on purpose. Each download is checked against the checksum
# its project publishes for that release.
KIND_VERSION="v0.33.0"
# Matches cliVersion in PROJECT, the version this project was scaffolded with.
KUBEBUILDER_VERSION="v4.13.0"
# Same Kubernetes minor as the k3s the E2E runs (K3S_VERSION in
# test/e2e/provision/lib.sh).
KUBECTL_VERSION="v1.36.5"

echo "===================================="
echo "Kubebuilder DevContainer Setup"
echo "===================================="

# Verify running as root (required for installing to /usr/local/bin and /etc)
if [ "$(id -u)" -ne 0 ]; then
  echo "ERROR: This script must be run as root"
  exit 1
fi

echo ""
echo "Detecting system architecture..."
# Detect architecture using uname
MACHINE=$(uname -m)
case "${MACHINE}" in
  x86_64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    echo "WARNING: Unsupported architecture ${MACHINE}, defaulting to amd64"
    ARCH="amd64"
    ;;
esac
echo "Architecture: ${ARCH}"

# install_binary <name> <url> <sha256>: download to a temporary file, check
# its checksum, then install it as /usr/local/bin/<name>.
install_binary() {
  local name=$1 url=$2 sum=$3 tmp
  if [ -z "${sum}" ]; then
    echo "ERROR: no published checksum found for ${name}"
    exit 1
  fi
  tmp=$(mktemp)
  curl -fsSLo "${tmp}" "${url}"
  if ! echo "${sum}  ${tmp}" | sha256sum -c --quiet -; then
    rm -f "${tmp}"
    echo "ERROR: checksum mismatch for ${name} (${url})"
    exit 1
  fi
  install -m 0755 "${tmp}" "/usr/local/bin/${name}"
  rm -f "${tmp}"
}

echo ""
echo "------------------------------------"
echo "Setting up bash completion..."
echo "------------------------------------"

BASH_COMPLETIONS_DIR="/usr/share/bash-completion/completions"

# Enable bash-completion in root's .bashrc (devcontainer runs as root)
if ! grep -q "source /usr/share/bash-completion/bash_completion" ~/.bashrc 2>/dev/null; then
  echo 'source /usr/share/bash-completion/bash_completion' >> ~/.bashrc
  echo "Added bash-completion to .bashrc"
fi

echo ""
echo "------------------------------------"
echo "Installing development tools..."
echo "------------------------------------"

# Install kind
if ! command -v kind &> /dev/null; then
  echo "Installing kind ${KIND_VERSION}..."
  base="https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}"
  sum=$(curl -fsSL "${base}/kind-linux-${ARCH}.sha256sum" | awk '{print $1}')
  install_binary kind "${base}/kind-linux-${ARCH}" "${sum}"
  echo "kind installed successfully"
fi

# Generate kind bash completion
if command -v kind &> /dev/null; then
  if kind completion bash > "${BASH_COMPLETIONS_DIR}/kind" 2>/dev/null; then
    echo "kind completion installed"
  else
    echo "WARNING: Failed to generate kind completion"
  fi
fi

# Install kubebuilder
if ! command -v kubebuilder &> /dev/null; then
  echo "Installing kubebuilder ${KUBEBUILDER_VERSION}..."
  base="https://github.com/kubernetes-sigs/kubebuilder/releases/download/${KUBEBUILDER_VERSION}"
  sum=$(curl -fsSL "${base}/checksums.txt" | awk -v f="kubebuilder_linux_${ARCH}" '$2 == f {print $1}')
  install_binary kubebuilder "${base}/kubebuilder_linux_${ARCH}" "${sum}"
  echo "kubebuilder installed successfully"
fi

# Generate kubebuilder bash completion
if command -v kubebuilder &> /dev/null; then
  if kubebuilder completion bash > "${BASH_COMPLETIONS_DIR}/kubebuilder" 2>/dev/null; then
    echo "kubebuilder completion installed"
  else
    echo "WARNING: Failed to generate kubebuilder completion"
  fi
fi

# Install kubectl
if ! command -v kubectl &> /dev/null; then
  echo "Installing kubectl ${KUBECTL_VERSION}..."
  base="https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}"
  sum=$(curl -fsSL "${base}/kubectl.sha256")
  install_binary kubectl "${base}/kubectl" "${sum}"
  echo "kubectl installed successfully"
fi

# Generate kubectl bash completion
if command -v kubectl &> /dev/null; then
  if kubectl completion bash > "${BASH_COMPLETIONS_DIR}/kubectl" 2>/dev/null; then
    echo "kubectl completion installed"
  else
    echo "WARNING: Failed to generate kubectl completion"
  fi
fi

# Generate Docker bash completion
if command -v docker &> /dev/null; then
  if docker completion bash > "${BASH_COMPLETIONS_DIR}/docker" 2>/dev/null; then
    echo "docker completion installed"
  else
    echo "WARNING: Failed to generate docker completion"
  fi
fi

echo ""
echo "------------------------------------"
echo "Configuring Docker environment..."
echo "------------------------------------"

# Wait for Docker to be ready
echo "Waiting for Docker to be ready..."
for i in {1..30}; do
  if docker info >/dev/null 2>&1; then
    echo "Docker is ready"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "WARNING: Docker not ready after 30s"
  fi
  sleep 1
done

# Create kind network (ignore if already exists)
if ! docker network inspect kind >/dev/null 2>&1; then
  if docker network create kind >/dev/null 2>&1; then
    echo "Created kind network"
  else
    echo "WARNING: Failed to create kind network (may already exist)"
  fi
fi

echo ""
echo "------------------------------------"
echo "Verifying installations..."
echo "------------------------------------"
kind version
kubebuilder version
kubectl version --client
docker --version
go version

echo ""
echo "===================================="
echo "DevContainer ready!"
echo "===================================="
echo "All development tools installed successfully."
echo "You can now start building Kubernetes operators."
