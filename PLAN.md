# isoboot v0.0.1 Plan

## Overview

A Kubernetes controller that manages PXE boot artifacts — downloads kernel, initrd, and optional firmware files, verifies their integrity, and assembles them into a directory structure ready for TFTP/HTTP serving.

## CRDs

### BootArtifact

A single downloadable file with integrity verification.

```yaml
apiVersion: boot.isoboot.io/v1alpha1
kind: BootArtifact
metadata:
  name: debian-13-kernel
spec:
  url: https://deb.debian.org/.../linux
  sha256: abc123...    # one of sha256 or sha512 required
  # sha512: def456... # mutually exclusive with sha256
```

**Status phases:** Pending → Downloading → Ready / Error

**Controller logic:**
- On create: set Pending, download file, verify hash
- Hash match → Ready
- Hash mismatch → delete file, Error
- Download failure → Error, retry with exponential backoff
- On restart: check etcd for existing resources
  - File exists on disk + hash matches → Ready
  - File exists + hash mismatch → delete, re-download
  - File missing → Pending → download

### BootConfig

Groups artifacts into a servable boot directory. Directory name = `metadata.name`.

```yaml
apiVersion: boot.isoboot.io/v1alpha1
kind: BootConfig
metadata:
  name: debian-13
spec:
  kernelRef: debian-13-kernel
  initrdRef: debian-13-initrd
  firmwareRef: debian-13-firmware   # optional
```

**Status phases:** Pending → Ready / Error

**Controller logic:**
- Watches referenced BootArtifacts
- All artifacts Ready → assemble directory → Ready
- Any artifact Pending/Downloading → Pending
- Any artifact Error → Error

**Directory layout — with firmwareRef:**
```
/data/boot/debian-13/
  vmlinuz
  no-firmware/initrd.gz            # raw initrd
  with-firmware/initrd.gz          # cat initrd.gz firmware.cpio.gz
```

**Directory layout — without firmwareRef (e.g. Rocky):**
```
/data/boot/rocky-9/
  vmlinuz
  initrd.img
```

No hash validation on the concatenated with-firmware/initrd.gz — individual artifacts are already verified.

## Project Setup

- **Framework:** kubebuilder v4
- **Domain:** isoboot.io
- **Group:** boot
- **Version:** v1alpha1
- **Repo:** github.com/isoboot/isoboot

## Implementation Steps

### Step 1: Scaffold
- `kubebuilder init`
- `kubebuilder create api` for BootArtifact (with controller)
- `kubebuilder create api` for BootConfig (with controller)

### Step 2: Define Types
- BootArtifact spec: `URL`, `SHA256`, `SHA512`
- BootArtifact status: `Phase`, `Message`, `LastChecked`, `FilePath`
- BootConfig spec: `KernelRef`, `InitrdRef`, `FirmwareRef`
- BootConfig status: `Phase`, `Message`

### Step 3: BootArtifact Controller
- Reconcile loop: check file on disk, download if missing, verify hash
- Set status based on outcome
- Exponential backoff on errors (use controller-runtime's built-in requeue)

### Step 4: BootConfig Controller
- Reconcile loop: look up referenced BootArtifacts
- If all Ready, assemble directory (copy/symlink files, concatenate firmware)
- If any not Ready, set Pending and requeue
- Watch BootArtifacts so changes trigger reconciliation

### Step 5: Tests
- Unit tests for hash verification
- Unit tests for firmware concatenation logic
- Controller tests with envtest

## v0.0.1 Scope

**In scope:**
- BootArtifact and BootConfig CRDs
- Download + hash verification
- Firmware concatenation
- Status reporting
- Exponential backoff on failures

**Out of scope (future):**
- TFTP/HTTP server integration
- Automatic mirror selection
- Update/re-download when URL or hash changes
- Cleanup of old files when resources are deleted
- Webhook validation
