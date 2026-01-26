# isoboot TODO

Project tracking for isoboot - PXE boot infrastructure on Kubernetes.

## Milestone 1: Basic PXE Boot ✅

**Status: Complete**

- [x] HTTP server (`isoboot-http`) for serving boot files
- [x] ISO9660 parsing to extract kernel/initrd from netboot ISOs
- [x] On-demand ISO download with caching
- [x] Boot handler for iPXE scripts (MAC-based matching)
- [x] Machine CRD for hardware inventory
- [x] Deploy CRD for deployment requests
- [x] Request logging middleware

## Milestone 2: Controller Architecture ✅

**Status: Complete**

- [x] Split architecture: controller (k8s access) + HTTP server (no k8s access)
- [x] gRPC communication between controller and HTTP server
- [x] Controller manages Deploy lifecycle (Pending → InProgress → Complete)
- [x] Lazy gRPC connection (HTTP starts before controller)
- [x] BootTarget CRD for boot configurations

## Milestone 3: Template Rendering ✅

**Status: Complete**

- [x] ResponseTemplate CRD for preseed/kickstart/autoinstall files
- [x] Template rendering with Go text/template
- [x] ConfigMap/Secret value injection
- [x] System variables (Host, Port, Hostname, Target)
- [x] Answer file serving via `/answer/{hostname}/{filename}`
- [x] Deploy completion endpoint `/api/deploy/{hostname}/complete`

## Milestone 4: DiskImage Management ✅

**Status: Complete (PR #10)**

- [x] DiskImage CRD for ISO/firmware references
- [x] Controller-based download (not on-demand in HTTP)
- [x] Checksum discovery (SHA512SUMS, SHA256SUMS)
- [x] Checksum verification (SHA512, SHA256)
- [x] File size verification
- [x] Existing file verification (skip download if valid)
- [x] DiskImage status tracking (Pending → Downloading → Complete/Failed)
- [x] Deploy WaitingForDiskImage status
- [x] Firmware merging with initrd (Debian netboot firmware)

## Milestone 5: Deploy Validation ✅

**Status: Complete**

- [x] Validate machineRef exists
- [x] Validate bootTargetRef exists
- [x] Validate responseTemplateRef exists
- [x] Validate configMaps exist
- [x] Validate secrets exist
- [x] ConfigError status for missing references
- [x] Self-healing on reference creation

## Milestone 6: Production Readiness 🚧

**Status: In Progress**

- [ ] Prometheus metrics endpoint
- [ ] Health check endpoints (liveness, readiness)
- [ ] Graceful shutdown handling
- [ ] Resource limits and requests in Helm chart
- [ ] Pod disruption budgets
- [ ] Network policies
- [ ] RBAC fine-tuning

## Milestone 7: Additional OS Support 📋

**Status: Planned**

- [ ] Ubuntu 24.04 (autoinstall)
- [ ] Rocky Linux 9 (kickstart)
- [ ] Fedora CoreOS (Ignition)
- [ ] Talos Linux
- [ ] Flatcar Container Linux

## Milestone 8: Advanced Features 📋

**Status: Planned**

- [ ] UEFI Secure Boot support
- [ ] UEFI HTTP Boot (not just PXE)
- [ ] Multi-architecture support (amd64, arm64)
- [ ] Webhook validation for CRDs
- [ ] Deploy scheduling (maintenance windows)
- [ ] Deploy dependencies (order multiple machines)
- [ ] Retry logic for failed deploys
- [ ] Deploy timeout handling

## Milestone 9: Observability 📋

**Status: Planned**

- [ ] Structured logging (JSON format)
- [ ] OpenTelemetry tracing
- [ ] Grafana dashboard
- [ ] Alerting rules
- [ ] Event recording to Kubernetes events

## Milestone 10: High Availability 📋

**Status: Planned**

- [ ] Controller leader election
- [ ] Multiple HTTP server replicas
- [ ] Shared storage for ISOs (PVC or object storage)
- [ ] Cache synchronization

---

## Current Work

### PR #10: DiskImage Download in Controller
- [x] Implementation complete
- [x] Manual tests passed (6/6)
- [x] Unit tests added
- [x] Code review feedback addressed
- [ ] Merge to main

---

## Legend

- ✅ Complete
- 🚧 In Progress
- 📋 Planned
- [ ] Not started
- [x] Done
