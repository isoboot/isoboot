# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

isoboot is a Kubernetes operator (Kubebuilder v4, Go 1.25) managing `NetworkBoot` custom resources for HTTPS-based PXE booting. Single CRD: `NetworkBoot` (group `boot.isoboot.github.io`, version `v1alpha1`, namespaced).

## Common Commands

```bash
# After editing api/v1alpha1/*_types.go (markers or structs):
make manifests generate    # Regenerate CRD, RBAC, and DeepCopy

# Before committing:
gofmt -w .
make lint                  # golangci-lint v2.7.2

# Testing:
make test                  # Unit/integration tests (envtest — real K8s API + etcd)
make test-e2e              # E2E tests (spins up Kind cluster, tears it down after)

# Note: make test automatically runs manifests, generate, fmt, vet as prerequisites

# Build:
make build                 # Builds bin/manager
make docker-build IMG=<registry>/<image>:<tag>
```

## Architecture

```
api/v1alpha1/
  networkboot_types.go       # CRD spec — URL type, BinaryHashPair, ISOSpec, FirmwareSpec
  groupversion_info.go       # API group registration
  zz_generated.deepcopy.go   # DO NOT EDIT — from make generate

internal/controller/
  networkboot_controller.go  # Reconciler (stub — TODO)
  suite_test.go              # envtest setup (loads CRDs from config/crd/bases/)
  networkboot_validation_test.go  # Integration tests for CRD validation rules

cmd/main.go                  # Manager entrypoint (leader election, metrics, webhooks, probes)

config/
  crd/bases/                 # DO NOT EDIT — generated CRD YAML
  rbac/role.yaml             # DO NOT EDIT — generated from RBAC markers
  default/                   # Kustomize overlay (namespace: isoboot-system)
  manager/                   # Deployment manifest (restricted pod security)
  samples/                   # Example CRs
```

## CRD Design

Two mutually exclusive boot modes enforced via CEL XOR:
- **Direct boot**: `spec.kernel` + `spec.initrd` (each a `BinaryHashPair` with binary/hash URLs)
- **ISO boot**: `spec.iso` (BinaryHashPair + kernel/initrd paths within the ISO)

Optional: `spec.firmware` (BinaryHashPair + prefix, defaults to `/with-firmware`)

Validation layers: `URL` custom type (HTTPS-only, no `@`, MaxLength=2048), CEL hostname matching between binary and hash, path traversal protection on ISO paths and firmware prefix.

## PR Workflow

- After pushing new commits to a PR, always update the PR description to reflect the full scope of changes. Do not wait to be asked.
- Always run `gofmt -w .`, `make lint`, and `make test` locally before pushing.

## Key Patterns

- **Never edit** auto-generated files: `config/crd/bases/`, `config/rbac/role.yaml`, `zz_generated.*.go`, `PROJECT`
- **Never remove** `// +kubebuilder:scaffold:*` markers — CLI injects code at these
- **Tests use Ginkgo/Gomega** BDD style with `DescribeTable`/`Entry` for table-driven tests
- **envtest** provides a real API server + etcd for integration tests (CRDs loaded from `config/crd/bases/`)
- **CI runs** three workflows: Lint, Tests, E2E Tests (Kind cluster)

## Tool Versions

- controller-gen v0.20.0 (via `make controller-gen`)
- golangci-lint v2.7.2 (via `make golangci-lint`)
- kustomize v5.7.1 (via `make kustomize`)
