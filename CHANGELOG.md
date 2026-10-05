# Changelog

## Unreleased

- **BREAKING**: Restructure `BootConfig` spec into two mutually-exclusive
  sections, `netboot` and `iso`, and hoist kernel args to a shared top-level
  `kernelArgs`. Migration: `spec.kernel.ref` → `spec.netboot.kernelRef`,
  `spec.initrd.ref` → `spec.netboot.initrdRef`, `spec.firmware.ref` →
  `spec.netboot.firmwareRef`, `spec.kernel.args` → `spec.kernelArgs`.
- Add BootConfig Mode B: extract kernel and initrd from an ISO artifact
- **BREAKING**: Remove the `{{.ISOURL}}` kernel args template variable; the ISO
  is no longer served over HTTP. Use `netboot=nfs nfsroot={{.NFSRoot}}` instead
  of `url={{.ISOURL}}`.
- ISO mode now unpacks the whole ISO tree into `<dataDir>/nfs/<bootconfig>/`
  (new controller flag `--nfs-dir`) and exports it over NFS; kernel and initrd
  are still served over HTTP. The tree is re-extracted only when the ISO changes.
- Add the `{{.NFSRoot}}` kernel args template variable (`<IPv4>:/<bootconfig>`,
  ISO mode only)
- Add the `{{.ProxyURL}}` variable to install-file (automation) templates
- Add the `nfsd` component: a read-only NFSv3 server with a TCP port mapper,
  image `ghcr.io/isoboot/isoboot-nfsd`, Helm values `nfsd.*` (enabled by
  default; needs TCP 111 and 2049 free on the node)
- Add Ubuntu 26.04.1 and 26.10 (beta) live-server autoinstall examples over NFS;
  drop the Ubuntu 24.04 example
- Drop the AlmaLinux 9.8 and Rocky 9.8 examples (examples mirror the E2E matrix)
- Pin the Debian 13 netboot installer to the dated 13.7 build
  (`20250803+deb13u7`) instead of `current`; firmware bundle 13.7.0
- Provision E2E: steps moved into `test/e2e/provision/` scripts shared with the
  new local runner `hack/e2e-local.sh`; rows in `rows.json`; matrix is
  AlmaLinux 10.2, Rocky 10.2, Debian 13.7 (with and without firmware), Ubuntu
  26.04.1 and 26.10 over NFS at 2 GiB; the guest reboots by itself after the
  install instead of being powered off; the RTL8168 QEMU is built only for the
  Debian rows
- **BREAKING**: The chart's CRDs move from `templates/crds.yaml` to `crds/`,
  copied unchanged from `config/crd/bases` by `make manifests`. Helm now
  installs them before everything else, which fixes a race where the
  post-install iPXE `BootArtifact` could be created before its CRD was served.
  The `crds.enabled` value is removed: use `helm install --skip-crds` instead,
  and on upgrade apply `charts/isoboot/crds/` with kubectl first (Helm never
  upgrades CRDs).
- Build with Go 1.27 (was 1.26; the devcontainer was still on 1.25) and
  golangci-lint v2.14.0 (was v2.11.3, which cannot read Go 1.27 code).
  goconst keeps skipping test files, as the older linter did.
- Pin every tool version so builds are repeatable, and check each download
  against its project's published checksum. Devcontainer: image golang:1.27.1,
  pinned features, no package upgrade at build time, kind v0.33.0,
  kubebuilder v4.13.0 (the `PROJECT` scaffold version), kubectl v1.36.5. E2E:
  k3s v1.36.5+k3s1 and Helm v3.22.0 (`test/e2e/provision/lib.sh`). Lint plugin
  logtools v0.10.1. CI runners: ubuntu-24.04 instead of ubuntu-latest.
  Images: Go builder golang:1.27.1, runtime distroless/static pinned by
  digest, Alpine 3.24.2 (was 3.23) for dnsmasq, squid and init containers,
  nginx-unprivileged 1.31.6-alpine (was the floating 1.29-alpine). Alpine
  packages (`apk add`) still come from the 3.24 branch at build time.
- Fix the controller being OOM-killed at its 128Mi limit while downloading or
  unpacking a multi-GB ISO: large writes are flushed to disk every 32 MiB, so
  dirty page cache no longer counts against the pod
- Fix the ISO-mode kernel and initrd being copied again on every reconcile on
  Linux (the skip check compared coarse timestamps); they are now compared by
  content and keep their inode when unchanged

### From the adversarial review of PR #389

Security:
- `/automation` files are served only while the Provision is `Pending` or
  `InProgress` (404 otherwise), so SSH host keys and password hashes are no
  longer readable after an install. `/automation` and `/conditional-boot`
  answers carry `Cache-Control: no-store`, so squid no longer stores rendered
  install files on disk.
- **BREAKING (chart)**: the controller (new flag `--namespace`) and httpd see
  only the release namespace, with a Role and RoleBinding there instead of
  ClusterRoles. The controller no longer has any access to Secrets or
  ConfigMaps; only httpd reads them.
- nginx serves `/static/` and `/dynamic/` only to `dnsmasq.subnet`, localhost
  and the node's own addresses (squid, on the node, relays installers'
  requests from one of them; the first version of this rule returned 403 to
  every install file and status call sent through the proxy). squid serves only that subnet and refuses loopback
  (127.0.0.0/8), link-local addresses, `squid.blockedDestinationCIDRs`
  (default: the k3s pod and service networks), ports other than 80, 443 and
  nginx's port, and CONNECT except to 443.
- nfsd: new repeatable `--allow-cidr` (chart value `nfsd.allowedCIDRs`,
  default `dnsmasq.subnet`; the chart always adds the node's own address for
  the kubelet's probes); connections from other addresses are closed and
  logged, rate-limited. Link-local IPv6 clients match too.
- nfsd: requests are checked before go-nfs decodes them (at most 8 KiB;
  credentials at most 400 bytes; every declared length must fit), so a
  76-byte call can no longer make nfsd allocate gigabytes.
- nfsd: at most 8 NFS connections per client address and 512 in all, 4
  port-mapper connections per address and 1024 in all; port-mapper calls must
  arrive within 2 s and NFS requests within 10 s.
- nfsd: READs are capped at 1 MiB and share a 32 MiB in-flight budget; FSINFO
  advertises 1 MiB reads and 4 KiB writes instead of 1 GiB. `GOMEMLIMIT`
  (chart value `nfsd.goMemLimit`, 200MiB) keeps it under its memory limit.

Fixes:
- nfsd: no longer leaks the goroutines and replies of every connection whose
  reply write failed (a go-nfs bug, worked around in isoboot).
- `/conditional-boot` boots the installer only when the BootConfig is Ready;
  otherwise it answers 404 and logs why. MACs are compared in lower case.
- The BootConfig controller checks `kernelArgs` (rendered with sample values,
  one line; a final line break is allowed) and sets Error with
  "invalid kernelArgs: ..." instead of failing when a machine boots.
- A new ISO whose kernel or initrd path is missing no longer replaces the
  exported tree. A failed extraction is retried with backoff (10 s doubling to
  30 min), not every 10 s, and error messages no longer contain random temp
  names that made every status update trigger another reconcile.
- The extracted tree, its marker and the kernel/initrd copies are fsynced
  before the marker is written; editing only an ISO artifact's hash text no
  longer re-extracts it; switching a BootConfig between netboot and iso mode
  no longer leaves it in Error; trees and boot directories of BootConfigs
  deleted while the controller was down are removed at startup.
- IPv6 `X-Forwarded-Host` values no longer produce `[[addr]]` URLs.
- New `required` template function for install files
  (`{{ required .Secrets "key" }}`) fails the render when a key is missing.
- **BREAKING**: BootConfig names are limited to 200 characters.

Packaging and CI:
- **BREAKING (chart)**: one `isoboot` image holds `/manager`, `/httpd` and
  `/nfsd`; the `isoboot-httpd` and `isoboot-nfsd` images and the chart's
  `httpd.image` and `nfsd.image` values are gone. `nfsd.portmapPort` is
  removed (always 111). `dnsmasq.subnet` must be an IPv4 CIDR.
- The node-pinned Deployments use strategy `Recreate`, so `helm upgrade` no
  longer hangs; nginx restarts when its configuration changes; the metrics
  Service selects only the controller.
- Every PR lints and renders the chart, runs squid and nginx in Docker to check
  who they serve, checks the workflows (actionlint, permissions, pins), checks
  `rows.json`, self-tests the E2E scripts and runs shellcheck. Actions are
  pinned by commit SHA; tokens are read-only except in jobs that push; labels
  other than `e2e` no longer restart the provision E2E.
- Release: images are pushed with the version tag and tested before the chart
  and `latest` are published.

E2E:
- The scripts refuse to run on a host that is not a GitHub runner, a VM made
  by `hack/e2e-local.sh` or explicitly allowed (`E2E_ALLOW_THIS_HOST=1`), and
  always use k3s's own kubeconfig.
- Checks that could never fail now can: password login, the Debian
  no-firmware row (must stay Pending and never fetch its preseed), a missing
  host key. Ubuntu rows check the guest's real kernel command line
  (`netboot=nfs`, `nfsroot=`, no `url=`). A row fails if any isoboot container
  restarted.
- Waits follow download and unpack progress instead of a fixed 10 minutes.
  The local runner keeps downloads in its VM, never deletes a VM it did not
  create, and treats zero rows as an error.
- QEMU, iPXE and the k3s install script are pinned and checksummed. The Kind
  E2E pins the hash of its test file.
- No more `| head -1` or `| grep -q` at the end of a pipeline under
  `pipefail`: they could kill the writer with SIGPIPE and turn a success into
  a failure (seen as exit 141 installing k3s).
- actionlint checks workflow scripts with the pinned shellcheck v0.11.0, not
  whatever shellcheck is installed, so CI and a laptop give the same result.
- Docs: README describes the current system, its security defaults and
  limitations; AGENTS.md is specific to this repo; PLAN.md is removed;
  `config/samples` use the pinned Rocky 10.2 artifacts.

## v0.0.2-rc3

- Add `POST /dynamic/status` endpoint for provision phase updates
- Add `{{.UpdatePhaseURL}}` and `{{.ProvisionName}}` template variables
- Enforce phase transitions: Pending -> InProgress -> Complete
- Extract `resolveHost()` helper for X-Forwarded header resolution

## v0.0.2-rc2

- Split Squid access_log conditional to separate lines
- Keep Squid cache log always on
- Add Squid log toggle settings

## v0.0.2-rc1

- Log Squid access and cache to files
- Use HTTP repo URL for Squid caching

## v0.0.1

- Rocky Linux 10.1 fully automated installation tested with a 7-line
  kickstart file (lang, keyboard, timezone, autopart, clearpart, zerombr, user)
- Known limitations: no squid cache (#349), SSH host keys not tested (#350),
  hostname not tested (#351), SSH public key not tested (#352)

## v0.0.1-rc12

- Add httpd RBAC for automation endpoint
- Bypass cache for types without indexers (least privilege)
- Sync Helm manager-role with generated role.yaml

## v0.0.1-rc11

- Drop /dynamic prefix from Go httpd automation route

## v0.0.1-rc10

- Use X-Forwarded-Host/Port for kernel args base URL

## v0.0.1-rc9

- Add kernel args template rendering with `{{.ProvisionAutomationBaseURL}}`
- Add kernel args to rocky-10.1 example

## v0.0.1-rc8

- Default new Provision phase to Pending via reconciler

## v0.0.1-rc7

- Run squid as non-root (UID 31) with drop ALL capabilities
- Use lightweight alpine init container for directory permissions
- Consolidate alpine version into single `.alpine-version` file

## v0.0.1-rc6

- Migrate E2E from KinD to k3s
- Add squid image push to tag workflow
- Fix E2E host paths for Helm deploy

## v0.0.1-rc5

- Add automation file render endpoint
- Rename ProvisionAnswer to ProvisionAutomation
- Add squid caching proxy deployment
- Namespace dataDir by component
- Quote all interpolated Helm values
- Extract shared Helm templates
- Consolidate wait-for scripts
- Deduplicate Go code
- Remove scaffolding and dead code

## v0.0.1-rc4

- Implement /conditional-boot endpoint with E2E test
- Add PendingProvisionForMAC function
- Add Machine MAC address and Provision phase indexers
- Use dash-separated MAC addresses
- Add iPXE sanboot fallback on chain fail
- Restructure BootConfig spec, add firmware concat
- Add Provision, Machine, and ProvisionAnswer CRDs
- Add httpd Go server with nginx proxy and dnsmasq init container
- Add httpd build and dynamic boot E2E CI

## v0.0.1-rc3

- Add dnsmasq proxyDHCP + QEMU PXE boot E2E test
- Add dnsmasq Docker image
- Add Rocky Linux 10.1 example manifest
- Quote Helm template values
- Add Helm quoting rule to CLAUDE.md

## v0.0.1-rc2

- Add nginx PXE file server to Helm chart
- Add E2E download test for PXE file serving
- Add pod anti-affinity to controller manager
- Add tag release and E2E test workflow

## v0.0.1-rc1

- Controller manager can download basic boot artifacts
- Controller manager can create basic boot configs
- Tested: https://github.com/isoboot/isoboot/actions/runs/22932882510
