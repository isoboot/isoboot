# Changelog

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
