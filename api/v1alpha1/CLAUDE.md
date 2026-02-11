# api/v1alpha1

CRD type definitions. After any change here, run `make manifests generate`.

## Type Hierarchy

```
URL (string)                    # HTTPS-only, no @, MaxLength=2048
  └─ BinaryHashPair             # binary + hash URLs, CEL hostname match
       ├─ NetworkBootSpec.Kernel (direct boot)
       ├─ NetworkBootSpec.Initrd (direct boot)
       ├─ ISOSpec (embedded inline) + kernel/initrd paths
       └─ FirmwareSpec (embedded inline) + prefix

ISOPath (string)                # absolute path, no traversal, MaxLength=1024
  ├─ ISOSpec.Kernel
  └─ ISOSpec.Initrd

NetworkBootSpec                  # XOR: (kernel+initrd) or iso
  ├─ Kernel *BinaryHashPair     # optional (required with initrd)
  ├─ Initrd *BinaryHashPair     # optional (required with kernel)
  ├─ ISO *ISOSpec               # optional (mutually exclusive with kernel/initrd)
  └─ Firmware *FirmwareSpec     # optional (either mode)
```

## Validation Patterns Used

- **Type-level markers** on `URL`: propagate to all fields of that type (MaxLength, Pattern)
- **Type-level markers** on `ISOPath`: propagate to all fields of that type (MaxLength, Pattern, CEL path traversal)
- **Struct-level CEL** on `BinaryHashPair`: `self.binary.split('/')[2] == self.hash.split('/')[2]` — propagates through `json:",inline"` embedding
- **Spec-level CEL** on `NetworkBootSpec`: XOR via two rules (`has(self.kernel) == has(self.initrd)` and `has(self.iso) != has(self.kernel)`)

## Adding a New Field

1. Add the field to the appropriate struct with json tag and kubebuilder markers
2. Run `make manifests generate`
3. Add positive and negative test entries in `internal/controller/networkboot_validation_test.go`
4. Run `make test`
