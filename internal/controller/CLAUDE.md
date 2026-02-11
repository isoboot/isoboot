# internal/controller

Controller reconciliation logic and integration tests.

## Test Setup

Tests use **envtest** (real K8s API server + etcd) via `suite_test.go`. The CRD is loaded from `config/crd/bases/`, so `make manifests` must have been run first. `make test` handles this automatically.

Shared variables available across test files (same package):
- `k8sClient` — controller-runtime client for creating/getting/deleting resources
- `ctx`, `cancel` — context for the test suite
- `cfg` — rest.Config for the envtest API server
- `testEnv` — the envtest.Environment

## Test Helpers (defined in networkboot_validation_test.go)

```go
pair(binary, hash URL) BinaryHashPair          // shorthand constructor
directBootSpec(kernel, initrd BinaryHashPair)   // direct boot mode spec
isoBootSpec(isoPair BinaryHashPair, kernel, initrd string)  // ISO boot mode spec
withFirmware(spec, fwPair BinaryHashPair, prefix *string)   // adds firmware to any spec
createNetworkBoot(name string, spec) error      // creates resource, returns error
deleteNetworkBoot(name string)                  // best-effort cleanup
ptr(s string) *string                           // pointer helper

// Pre-built valid pairs:
validPair     // https://example.com/artifact
validFWPair   // https://fw.example.com/fw.bin
validISOPair  // https://releases.ubuntu.com/noble.iso
```

## Adding a New Validation Test

Use `DescribeTable`/`Entry` for table-driven tests. Negative tests don't need cleanup (resource was never created). Positive tests need `deleteNetworkBoot` in `AfterEach` or inline.

```go
Entry("description of what is being tested",
    directBootSpec(pair("https://...", "https://..."), validPair)),
```
