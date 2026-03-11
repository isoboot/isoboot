# Changelog

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
