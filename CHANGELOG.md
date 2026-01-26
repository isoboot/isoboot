# Changelog

## [0.3.0] - 2026-01-24

### Added
- `isoboot-controller` gRPC server for Kubernetes API access
- Controller manages Deploy lifecycle (Pending → InProgress → Complete)
- gRPC service for HTTP pod to query deploys and templates

### Changed
- Split architecture: controller has k8s access, HTTP server does not
- HTTP server connects to controller via gRPC instead of direct k8s API
- Lazy gRPC connection allows HTTP to start before controller is ready

### Tested With
- Debian 13 (trixie) netboot - boots to installer screen
- QEMU VM with UEFI PXE boot
- Machine/Deploy CRDs with controller reconciliation

## [0.2.0] - 2026-01-24

### Added
- `isoboot-http` server for Debian 13 netboot
- Kubernetes CRD support (Machine, Deploy)
- On-demand ISO download with caching and lock coordination
- ISO9660 parsing to extract kernel/initrd from netboot ISOs
- Boot handler for iPXE scripts based on MAC/Deploy matching
- Dynamic handler for preseed configs and deployment completion
- Request logging middleware with method, path, status, duration
- Config hot-reload from file

### Security
- Validate ISO filename against config to prevent disk exhaustion
- Filter deploys by phase (Pending/InProgress) to prevent wrong deploy updates

### Changed
- MAC addresses standardized to dash-separated format (iPXE compatible)
- Reject colon-separated MAC addresses
- Content-Length required on all 200 responses
