# isoboot

Kubernetes operator for managing network boot sources.

## BootSource CRD

Define boot sources (kernel, initrd, firmware, ISO) with URL validation:

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootSource
metadata:
  name: ubuntu-24-04
spec:
  kernel:
    url:
      binary: "https://example.com/vmlinuz"
      shasum: "https://example.com/vmlinuz.sha256"
  initrd:
    url:
      binary: "https://example.com/initrd.img"
      shasum: "https://example.com/initrd.img.sha256"
```

Or use an ISO with embedded kernel/initrd paths:

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootSource
metadata:
  name: debian-iso
spec:
  iso:
    url:
      binary: "https://example.com/debian.iso"
      shasum: "https://example.com/debian.iso.sha256"
    path:
      kernel: "/install/vmlinuz"
      initrd: "/install/initrd.gz"
```

## Quick Install

```bash
kubectl apply -f https://raw.githubusercontent.com/isoboot/isoboot/main/config/crd/bases/isoboot.github.io_bootsources.yaml
```

See [docs/INSTALL.md](docs/INSTALL.md) for detailed installation instructions.

## Validation

The CRD enforces:
- HTTPS required for all URLs
- `binary` and `shasum` must be on the same server
- Must specify `(kernel + initrd)` OR `iso`, not both
- ISO requires `path.kernel` and `path.initrd`
