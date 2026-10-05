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

In `iso` mode (Ubuntu live-server) the controller unpacks the whole ISO into `<dataDir>/nfs/<bootconfig>/`, the `nfsd` component exports it read-only over NFSv3, and only the kernel and initrd go over HTTP. Kernel arguments use `netboot=nfs nfsroot={{.NFSRoot}}`, so the installer never downloads the ISO into RAM (see `examples/ubuntu-26.04.yaml`). nfsd runs on the host network and needs TCP 111 and 2049 free on the node (no `rpcbind` or kernel NFS server).
