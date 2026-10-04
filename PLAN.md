# isoboot v0.0.1 Plan

## Overview

A Kubernetes controller that manages PXE boot artifacts — downloads kernel, initrd, and optional firmware files, verifies their integrity, and assembles them into a directory structure ready for TFTP/HTTP serving.

## CRDs

### BootArtifact

A single downloadable file with integrity verification.

```yaml
apiVersion: isoboot.github.io/v1alpha1
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
Two mutually exclusive modes: direct refs or ISO extraction.

#### Mode A — Direct refs (Rocky, Debian)

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootConfig
metadata:
  name: debian-13
spec:
  kernelRef: debian-13-kernel
  initrdRef: debian-13-initrd
  firmwareRef: debian-13-firmware   # optional, mode A only
```

#### Mode B — ISO extraction (Ubuntu)

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootConfig
metadata:
  name: ubuntu-24
spec:
  iso:
    artifactRef: ubuntu-24-iso
    kernelPath: casper/vmlinuz
    initrdPath: casper/initrd
```

**CEL validation rules:**
- Mode A: `kernelRef` and `initrdRef` required, `iso` must not be set
- Mode B: `iso` required (with `artifactRef`, `kernelPath`, `initrdPath`), `kernelRef`, `initrdRef`, and `firmwareRef` must not be set
- Exactly one mode must be used

**Status phases:** Pending → Ready / Error

**Controller logic:**
- Watches referenced BootArtifacts
- Mode A:
  - All artifacts Ready → assemble directory → Ready
  - Any artifact Pending/Downloading → Pending
  - Any artifact Error → Error
- Mode B:
  - ISO artifact Ready → extract kernel and initrd from ISO → Ready
  - ISO artifact not Ready → Pending
  - Extraction failure → Error

**Directory layout — Mode A with firmwareRef:**
```
/data/boot/debian-13/
  vmlinuz
  no-firmware/initrd.gz            # raw initrd
  with-firmware/initrd.gz          # cat initrd.gz firmware.cpio.gz
```

**Directory layout — Mode A without firmwareRef (e.g. Rocky):**
```
/data/boot/rocky-9/
  vmlinuz
  initrd.img
```

**Directory layout — Mode B (ISO):**
```
/data/boot/ubuntu-24/
  vmlinuz                          # extracted from ISO at kernelPath
  initrd                           # extracted from ISO at initrdPath
```

No hash validation on the concatenated with-firmware/initrd.gz — individual artifacts are already verified.

## Project Setup

- **Framework:** kubebuilder v4
- **Domain:** isoboot.github.io
- **Group:** (none, flat API group)
- **Version:** v1alpha1
- **Repo:** github.com/isoboot/isoboot

## Implementation Steps

### Step 1: Scaffold ✅
- `kubebuilder init --domain isoboot.github.io --repo github.com/isoboot/isoboot`
- `kubebuilder create api --version v1alpha1 --kind BootArtifact --controller --resource`
- `kubebuilder create api --version v1alpha1 --kind BootConfig --controller --resource`

### Step 2: Define Types ✅ (BootArtifact) / In Progress (BootConfig)
- BootArtifact spec: `URL`, `SHA256`, `SHA512` with CEL validation
- BootArtifact status: `Phase`, `Message`, `LastChecked`
- BootConfig spec: Mode A (`KernelRef`, `InitrdRef`, `FirmwareRef`) or Mode B (`ISO`)
- BootConfig status: `Phase`, `Message`
- CEL rules to enforce exactly one mode

### Step 3: BootArtifact Controller
- Reconcile loop: check file on disk, download if missing, verify hash
- Set status based on outcome
- Exponential backoff on errors (use controller-runtime's built-in requeue)

### Step 4: BootConfig Controller
- Reconcile loop: look up referenced BootArtifacts
- Mode A: assemble directory (copy/symlink files, concatenate firmware)
- Mode B: extract kernel and initrd from ISO
- If any not Ready, set Pending and requeue
- Watch BootArtifacts so changes trigger reconciliation

### Step 5: Tests
- Unit tests for hash verification
- Unit tests for firmware concatenation logic
- Unit tests for ISO extraction logic
- Controller tests with envtest

## v0.0.1 Scope

**In scope:**
- BootArtifact and BootConfig CRDs
- Download + hash verification
- Firmware concatenation (Mode A)
- ISO extraction (Mode B)
- Status reporting
- Exponential backoff on failures

**Out of scope (future):**
- TFTP/HTTP server integration
- Automatic mirror selection
- Update/re-download when URL or hash changes
- Cleanup of old files when resources are deleted
- Webhook validation
