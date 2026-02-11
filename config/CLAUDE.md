# config

Kubernetes manifests for CRD, RBAC, deployment, and kustomize overlays.

## What Is Generated (DO NOT EDIT)

- `crd/bases/*.yaml` — from `make manifests` (controller-gen reads markers in api/)
- `rbac/role.yaml` — from `make manifests` (controller-gen reads RBAC markers in internal/controller/)

## What Is Hand-Editable

- `default/kustomization.yaml` — main kustomize overlay (namespace, patches)
- `manager/manager.yaml` — controller deployment (resources, probes, security context)
- `rbac/kustomization.yaml` — which RBAC files to include
- `samples/*.yaml` — example CRs for users

## Adding Labels to Generated Files

Don't add labels directly to `rbac/role.yaml` — they'll be lost on `make manifests`. Instead use `commonLabels` in the relevant `kustomization.yaml`.
