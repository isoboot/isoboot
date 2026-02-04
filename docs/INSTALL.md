# Installation Guide

## Install via Helm

```bash
helm install isoboot oci://ghcr.io/isoboot/charts/isoboot
```

Or from source:

```bash
git clone https://github.com/isoboot/isoboot.git
helm install isoboot ./isoboot/chart/isoboot
```

## Quick Start (Multipass + MicroK8s)

This guide walks through installing the BootSource CRD on a fresh VM.

### 1. Launch VM

```bash
multipass launch --name isoboot --cpus 2 --memory 2G --disk 10G
multipass shell isoboot
```

### 2. Install MicroK8s

```bash
sudo snap install microk8s --classic
sudo usermod -aG microk8s $USER
newgrp microk8s

# Wait for microk8s to be ready
microk8s status --wait-ready

# Alias kubectl (optional)
sudo snap alias microk8s.kubectl kubectl
```

### 3. Install BootSource CRD

**Option A: Helm**
```bash
microk8s enable helm3
microk8s helm3 install isoboot oci://ghcr.io/isoboot/charts/isoboot
```

**Option B: kubectl**
```bash
microk8s kubectl apply -f https://raw.githubusercontent.com/isoboot/isoboot/main/config/crd/bases/isoboot.github.io_bootsources.yaml
```

Verify:
```bash
microk8s kubectl get crd bootsources.isoboot.github.io
```

### 4. Create a BootSource

```bash
cat <<EOF | microk8s kubectl apply -f -
apiVersion: isoboot.github.io/v1alpha1
kind: BootSource
metadata:
  name: ubuntu-24-04
spec:
  iso:
    url:
      binary: "https://releases.ubuntu.com/noble/ubuntu-24.04.3-live-server-amd64.iso"
      shasum: "https://releases.ubuntu.com/noble/SHA256SUMS"
    path:
      kernel: "/casper/vmlinuz"
      initrd: "/casper/initrd"
EOF
```

Verify:
```bash
microk8s kubectl get bootsources
microk8s kubectl describe bootsource ubuntu-24-04
```

### 5. Test Validation

The CRD enforces validation rules. Try creating an invalid BootSource:

```bash
# This will fail - binary and shasum must be on the same server
cat <<EOF | microk8s kubectl apply -f -
apiVersion: isoboot.github.io/v1alpha1
kind: BootSource
metadata:
  name: invalid-different-servers
spec:
  kernel:
    url:
      binary: "https://server1.com/vmlinuz"
      shasum: "https://server2.com/vmlinuz.sha256"
  initrd:
    url:
      binary: "https://server1.com/initrd"
      shasum: "https://server1.com/initrd.sha256"
EOF
```

Expected error:
```
binary and shasum URLs must be on the same server
```

### Cleanup

```bash
# Exit VM
exit

# Delete VM
multipass delete isoboot && multipass purge
```

## Alternative: Kind (Docker)

If you prefer Kind over Multipass:

```bash
# Install kind
go install sigs.k8s.io/kind@latest

# Create cluster
kind create cluster --name isoboot

# Install CRD
kubectl apply -f https://raw.githubusercontent.com/isoboot/isoboot/main/config/crd/bases/isoboot.github.io_bootsources.yaml

# Cleanup
kind delete cluster --name isoboot
```

## Validation Rules

The BootSource CRD enforces these rules:

| Rule | Description |
|------|-------------|
| HTTPS required | `binary` and `shasum` URLs must use `https://` |
| Same server | `binary` and `shasum` must be on the same host |
| Either/or | Must specify `(kernel + initrd)` OR `iso`, not both |
| ISO paths | If using `iso`, must specify `path.kernel` and `path.initrd` |
