# AGENTS.md

Guidance for coding agents working in this repository. `README.md` explains
what isoboot does and how it is installed; `CLAUDE.md` has the commit, review
and E2E rules, which apply to every agent.

## Layout

| Path | What |
|---|---|
| `api/v1alpha1/` | The five CRDs: BootArtifact, BootConfig, Machine, ProvisionAutomation, Provision |
| `cmd/main.go`, `internal/controller/` | Controller manager: downloads, boot directories, ISO unpacking (`isotree.go`), Provision bookkeeping |
| `cmd/httpd/`, `internal/httpd/` | `/conditional-boot`, `/automation/<provision>/<file>`, `/status` |
| `cmd/nfsd/`, `internal/nfsd/` | Read-only NFSv3 + MOUNT + port mapper for ISO-mode BootConfigs |
| `internal/urlutil/`, `internal/envtestutil/` | Shared helpers |
| `Dockerfile` | The one Go image: `/manager`, `/httpd`, `/nfsd` |
| `Dockerfile.dnsmasq`, `Dockerfile.squid` | The other two images |
| `charts/isoboot/` | The Helm chart, the supported way to install |
| `config/` | Kustomize manifests from the kubebuilder scaffold, used by the Kind E2E tests |
| `examples/` | One tested BootConfig and its BootArtifacts per supported release |
| `test/e2e/` | Kind E2E (Ginkgo, build tag `e2e`) and `wait-for-resource.sh` |
| `test/e2e/provision/` | Provision E2E: real installs in QEMU/KVM; rows in `rows.json` |
| `test/qemu/` | QEMU build with the emulated RTL8168 NIC for the Debian firmware rows |
| `hack/e2e-local.sh` | Runs the provision E2E in a throwaway multipass VM |
| `docs/` | User documentation: how-to guides and reference (index: `docs/README.md`) |
| `test/docs/` | Keeps `docs/` honest: its YAML objects are created against the CRDs, its links must resolve (`make test`) |

## Generated files: never edit by hand

- `config/crd/bases/*.yaml` and `charts/isoboot/crds/*.yaml` (a copy): `make manifests`
- `config/rbac/role.yaml`: `make manifests`, from the `+kubebuilder:rbac` markers
- `api/**/zz_generated.deepcopy.go`: `make generate`
- `PROJECT`: kubebuilder

Change the Go types or markers, then run `make manifests generate` and commit
the result; the Verify Manifests workflow fails on any drift. Keep the
`// +kubebuilder:scaffold:*` markers.

## Conventions

- Status is a `.status.phase` string plus `.status.message`, not
  `metav1.Condition`. httpd, the chart and both E2E suites read the phase
  strings, so keep them.
- Secure defaults: least privilege (namespaced RBAC, read-only mounts,
  dropped capabilities), access limited to the PXE subnet, no secrets in
  logs. Never weaken a default or a test to get a build green.
- Every version is pinned in a file (images by tag or digest, tools,
  downloads), and every download is checked against a published or pinned
  checksum.
- Descriptive names, no cryptic abbreviations; simple code that matches the
  code around it.
- A behaviour change comes with a test that fails without it.
- Helm templates quote interpolated values: `"{{ .Values.foo }}"`.

## Checks

```bash
make test        # unit tests with envtest
make lint        # golangci-lint
make manifests generate && git diff --exit-code
test/e2e/provision/check-rows.sh   # rows.json and the examples it uses
test/e2e/provision/selftest.sh     # the provision E2E harness (needs docker)
```

`make test-e2e` runs the Kind E2E against a dedicated Kind cluster it creates.

The provision E2E changes the host it runs on (it uninstalls k3s, deletes
`/data/isoboot`, adds a bridge and iptables rules). Run it only through
`hack/e2e-local.sh`, which uses a throwaway VM, or in CI (label a PR `e2e`).
The scripts refuse any other host unless `E2E_ALLOW_THIS_HOST=1` is set.
