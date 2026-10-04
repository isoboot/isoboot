# isoboot

A Kubernetes controller that manages PXE boot artifacts.

## Development

### Scaffold

Project was scaffolded with kubebuilder using a flat API group:

```bash
kubebuilder init --domain isoboot.github.io --repo github.com/isoboot/isoboot
# APIs created without --group so apiVersion is isoboot.github.io/v1alpha1
kubebuilder create api --version v1alpha1 --kind BootArtifact --controller --resource
kubebuilder create api --version v1alpha1 --kind BootConfig --controller --resource
```

### CRDs

**BootArtifact** — a single downloadable file (kernel, initrd, or firmware) with URL and hash verification.

**BootConfig** — groups BootArtifacts into a servable PXE boot directory. If firmware is present, creates `no-firmware/` and `with-firmware/` subdirectories.
