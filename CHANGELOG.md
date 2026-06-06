# Changelog

## Unreleased

- **BREAKING**: Restructure `BootConfig` spec into two mutually-exclusive
  sections, `netboot` and `iso`, and hoist kernel args to a shared top-level
  `kernelArgs`. Migration: `spec.kernel.ref` → `spec.netboot.kernelRef`,
  `spec.initrd.ref` → `spec.netboot.initrdRef`, `spec.firmware.ref` →
  `spec.netboot.firmwareRef`, `spec.kernel.args` → `spec.kernelArgs`.

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
