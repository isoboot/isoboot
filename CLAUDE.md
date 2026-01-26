# CLAUDE.md

Go code repository for isoboot controller and HTTP server.

## Project Context

This repo works alongside `isoboot-chart` (Helm chart with CRDs). Together they provide PXE boot infrastructure on Kubernetes.

**Workflow**: Machine PXE boots → dnsmasq responds → iPXE loads → fetches boot script from isoboot-http → installer runs → fetches answer files from /answer/{hostname}/{filename} → completes via /api/deploy/{hostname}/complete

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
└── k8s/                 # Kubernetes client and CR types

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

Available variables:
- From ConfigMaps/Secrets: any key-value pairs
- System vars: `Host`, `Port`, `Hostname`, `BootTarget`

### Error Handling in HTTP Handlers
- Return 502 Bad Gateway for gRPC/transport errors
- Return 404 Not Found only for "resource not found" errors
- Use sentinel errors (e.g., `controllerclient.ErrNotFound`) to distinguish error types
- Always set Content-Length header

### BootTarget and Firmware Merging
BootTarget CRD fields:
- `diskImageRef` (required): Reference to DiskImage resource
- `includeFirmwarePath` (optional): Path that triggers firmware merging (e.g., `/initrd.gz`)
- `template`: iPXE boot template content

Firmware merging behavior:
- Only occurs when `includeFirmwarePath` is set AND the requested path matches
- Concatenates initrd + firmware.cpio.gz for Debian netboot with non-free firmware
- If `includeFirmwarePath` is not set, serves files as-is (no merging)

## Commands

```bash
# Run tests
go test ./...

# Build binaries
go build ./cmd/isoboot-controller
go build ./cmd/isoboot-http

# Generate protobuf (if proto files change)
# Note: protoc may output to nested path like github.com/isoboot/isoboot/api/controllerpb/
# If so, copy files to api/controllerpb/ manually
protoc --go_out=. --go-grpc_out=. api/proto/controller.proto
```

## Testing Guidelines

- Unit tests alongside code: `foo_test.go`
- Use `httptest.NewRecorder()` for HTTP handler tests
- Mock external dependencies (k8s client, gRPC client)

## Before Committing

- Run `go test ./...`
- Keep commits focused on single logical changes
