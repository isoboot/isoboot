# CLAUDE.md

Go code repository for isoboot controller and HTTP server.

## Project Context

This repo works alongside `isoboot-chart` (Helm chart with CRDs). Together they provide PXE boot infrastructure on Kubernetes.

**Workflow**: Machine PXE boots → dnsmasq responds → iPXE loads → fetches boot script from isoboot-http → installer runs → fetches answer files from /answer/{provisionName}/{filename} → completes via /boot/done?id={machineName}

## Git Conventions

- **Never force push** - use squash merge at PR merge time
- PRs required for main branch

## Package Structure

```
cmd/
├── isoboot-controller/   # Kubernetes controller binary
└── isoboot-http/         # HTTP server binary

internal/
├── config/              # Configuration and hot-reload
├── controller/          # Controller reconciliation logic
├── controllerclient/    # gRPC client for HTTP→controller communication
├── handlers/            # HTTP handlers (boot, iso, answer)
├── iso/                 # ISO extraction utilities
└── k8s/                 # Kubernetes typed client (controller-runtime) and CRD types

api/
├── controllerpb/        # Generated protobuf code
└── proto/               # Proto definitions
```

## Key Patterns

### gRPC Communication
- Controller exposes gRPC on port 8081
- HTTP server connects as client
- Used for: GetPendingBoot, MarkBootStarted, GetRenderedTemplate, etc.

### Template Rendering
Templates use Go `text/template` with `missingkey=error`:
```go
tmpl, err := template.New("").Option("missingkey=error").Parse(content)
if err != nil {
    return err
}
```

Custom template functions (Helm/sprig-style):
- `b64enc` - base64 encode a string: `{{ .Password | b64enc }}`
- `hasKey` - check if key exists in map: `{{ if hasKey . "ssh_host_ed25519_key_pub" }}...{{ end }}`

Available variables in ResponseTemplate (preseed/answer files):
- `.Host` - HTTP server host IP
- `.Port` - HTTP server port
- `.Hostname` - machine reference from Provision
- `.Target` - boot target reference from Provision
- `.MAC` - machine MAC address (dash-separated, lowercase)
- `.MachineId` - systemd machine-id from Provision (use `hasKey` to check if set)
- `.key` - values merged from referenced ConfigMaps and Secrets (flat namespace)
- `.ssh_host_*_key_pub` - auto-derived public keys for SSH host keys in secrets

Available variables in BootTarget (iPXE scripts):
- `.Host` - HTTP server host IP
- `.Port` - HTTP server port
- `.MachineName` - full machine name (e.g., "vm-01.lan")
- `.Hostname` - first part before dot (e.g., "vm-01")
- `.Domain` - everything after first dot (e.g., "lan")
- `.BootTarget` - BootTarget resource name
- `.BootMedia` - BootMedia resource name (for static file paths, e.g., `/static/{{ .BootMedia }}/linux`)
- `.UseFirmware` - bool, whether to use firmware-combined initrd
- `.ProvisionName` - Provision resource name (use for answer file URLs)
- `.KernelFilename` - kernel filename (e.g., "linux", "vmlinuz") resolved from BootMedia
- `.InitrdFilename` - initrd filename (e.g., "initrd.gz") resolved from BootMedia
- `.HasFirmware` - bool, whether BootMedia has firmware defined

### CRD Architecture: BootMedia + BootTarget
- **BootMedia** owns file downloads via named fields: `kernel`, `initrd` (direct URLs), or `iso` (ISO download + extraction with `iso.kernel`/`iso.initrd` paths). Optional `firmware` for initrd concatenation. One per OS version. Names: `debian-12`, `debian-13`.
- **BootTarget** references a BootMedia via `bootMediaRef`. Adds `useFirmware: bool` and `template`. Multiple BootTargets can share one BootMedia. Names: `debian-12`, `debian-12-firmware`, `debian-13`, `debian-13-firmware`.
- Static files served at `/static/{bootMedia}/`.
- Provision still references `bootTargetRef`.

### BootMedia Directory Structure
Without firmware (flat layout):
```
debian-12/
  linux           ← kernel
  initrd.gz       ← initrd
```

With firmware (subdirectory layout):
```
debian-12/
  linux                    ← kernel (always top-level)
  no-firmware/
    initrd.gz              ← original initrd
  with-firmware/
    initrd.gz              ← initrd + firmware.cpio.gz concatenated
```

### Error Handling in HTTP Handlers
- Return 502 Bad Gateway for gRPC/transport errors
- Return 404 Not Found only for "resource not found" errors
- Always set Content-Length header

## Commands

```bash
# Run tests
go test ./...

# Build binaries
go build ./cmd/isoboot-controller
go build ./cmd/isoboot-http

# Generate protobuf (if proto files change)
protoc --go_out=. --go-grpc_out=. api/proto/controller.proto
```

## Testing Guidelines

- Unit tests alongside code: `foo_test.go`
- Use `httptest.NewRecorder()` for HTTP handler tests
- Use controller-runtime fake client (`sigs.k8s.io/controller-runtime/pkg/client/fake`) for k8s tests
- Mock gRPC client for HTTP handler tests

## PR Reviews

### Re-requesting Copilot review (without a new push)
```bash
gh api repos/isoboot/isoboot/pulls/{PR}/requested_reviewers \
  -X POST -f'reviewers[]=copilot-pull-request-reviewer[bot]'
```
The `[bot]` suffix is required — without it the API returns 422.

### Resolving review threads
```bash
gh api graphql -f query='mutation { resolveReviewThread(input: {threadId: "THREAD_ID"}) { thread { isResolved } } }'
```
Thread IDs look like `PRRT_kwDOQ_1gNM5r...` and can be found via:
```bash
gh api graphql -f query='query { repository(owner: "isoboot", name: "isoboot") { pullRequest(number: PR) { reviewThreads(first: 50) { nodes { id isResolved comments(first: 1) { nodes { body } } } } } } }'
```

## Before Committing

- Run `go test ./...`
- Keep commits focused on single logical changes
