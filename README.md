# isoboot

A Kubernetes controller that manages PXE boot artifacts.

## Development

### Scaffold

Project was scaffolded with kubebuilder using a flat API group:

```bash
kubebuilder init --domain isoboot.github.io --repo github.com/isoboot/isoboot
# APIs created with --group "" so apiVersion is isoboot.github.io/v1alpha1
```
