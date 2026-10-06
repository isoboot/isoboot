# isoboot documentation

What isoboot is and how its parts fit together is in the
[project README](../README.md). Read that first, then:

1. [Install isoboot](how-to/install.md) on the node that serves PXE.
2. [Provision a machine](how-to/provision-a-machine.md): install Ubuntu
   26.04 on one machine, from the boot files to the first login.
3. The page for your release, for what differs from that guide.

## How-to guides

Step by step, for one task each.

| Page | What it does |
|---|---|
| [Install isoboot](how-to/install.md) | Requirements, the Helm install, the values to set, health checks, upgrade and uninstall. |
| [Provision a machine](how-to/provision-a-machine.md) | One install from start to finish, the settings every install file reads, reinstalling and stopping an install. |
| [Install Ubuntu Server](how-to/ubuntu.md) | Ubuntu 24.04, 26.04 and 26.10 over NFS: the ISO BootConfig, its kernel arguments and the answer file. |
| [Install Rocky Linux or AlmaLinux](how-to/rocky-and-alma.md) | The netboot BootConfig, its kernel arguments and the kickstart, with SELinux left enforcing. |
| [Install Debian](how-to/debian.md) | The netboot BootConfig, the preseed file, and firmware for network cards that need it. |
| [Troubleshoot](how-to/troubleshoot.md) | Symptoms, causes and fixes, and where each component logs. |
| [Run the E2E tests](how-to/run-the-e2e-tests.md) | The provision E2E: what it checks, how to run it in CI or locally, and its logs. |

## Reference

What each part does, field by field.

| Page | What it describes |
|---|---|
| [Custom resources](reference/custom-resources.md) | The five resources: fields, validation, status, phases, messages and lifecycle. |
| [Templates and the status callback](reference/templates.md) | `kernelArgs` and install-file templates: variables, functions, errors, and the `/dynamic/status` call. |

## Background

| Page | What it is |
|---|---|
| [Ubuntu answer file, one screen at a time](ubuntu-autoinstall-journey.md) | A study of what an Ubuntu autoinstall answer file must contain, with screenshots of every boot. |

## Conventions

- Every page assumes the Helm release `isoboot` in the namespace
  `isoboot-system`, so the Deployments are `isoboot-<component>`.
- The examples install one machine, `web-server-01`, with the ConfigMaps
  `default-user` and `web-server-01` and the Secret
  `web-server-01-host-keys` made in
  [Provision a machine, step 4](how-to/provision-a-machine.md#4-create-the-settings-and-host-keys).
  Every install file reads those keys.
- Each fact is in one place; other pages link to it.

## The docs are tested

`make test` runs [`test/docs/`](../test/docs/), which checks the pages:

- Every fenced `yaml` block that holds a Kubernetes object of an isoboot
  kind, a ConfigMap or a Secret is created in a scratch namespace of an
  envtest API server with the CRDs from `config/crd/bases/`, with strict
  field validation, so the schema and its CEL rules check it. A rejection
  fails the test with the file and the block number.
- Every relative link in `docs/` and the project README points to a file
  that exists, and every `#anchor` to a heading in it.

A block that is deliberately only part of an object (for example a `spec:`
fragment) must be marked, on the line right before its fence:

```markdown
<!-- docs-test: skip (part of a BootConfig spec) -->
```

The text after `skip` is free; say why. GitHub does not show the comment.
An unmarked block that looks like part of an object (it has `apiVersion`,
`kind`, `metadata` or `spec` at the top but not both `apiVersion` and
`kind`) fails the test. Blocks that are not objects at all, such as a
rendered answer file or a values file, are left alone.
