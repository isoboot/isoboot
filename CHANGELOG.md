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
- Fix the controller being OOM-killed at its 128Mi limit while downloading or
  unpacking a multi-GB ISO: large writes are flushed to disk every 32 MiB, so
  dirty page cache no longer counts against the pod
- Fix the ISO-mode kernel and initrd being copied again on every reconcile on
  Linux (the skip check compared coarse timestamps); they are now compared by
  content and keep their inode when unchanged

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
