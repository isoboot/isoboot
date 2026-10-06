# Install Ubuntu Server

This page covers what is specific to Ubuntu Server 24.04.5 LTS, 26.04.1 LTS
and 26.10: the BootConfig built from the live-server ISO, which isoboot
unpacks and exports over NFS, its kernel arguments, and the autoinstall
answer file. The steps themselves (Machine, settings, Provision, boot) are
those of [Provision a machine](provision-a-machine.md), which installs
Ubuntu 26.04 as its example.

What each line of the answer file does, screen by screen with screenshots of
real boots, is in
[Ubuntu answer file, one screen at a time](../ubuntu-autoinstall-journey.md).
This page is the how-to; that page is the study behind it.

## Why NFS

Ubuntu's live server has no netboot kernel and initrd of its own, so the
BootConfig uses the ISO (`spec.iso`) instead of `spec.netboot`:

- The controller unpacks the whole ISO to `<dataDir>/nfs/<bootconfig>/` and
  copies the ISO's kernel and initrd to `<dataDir>/nginx/static/boot/<bootconfig>/`
  as `vmlinuz` and `initrd`.
- nginx serves the kernel and initrd to iPXE over HTTP.
- isoboot's `nfsd` exports the unpacked tree read-only over NFSv3 as
  `/<bootconfig>`, and the installer's initrd (casper) mounts it
  (`netboot=nfs nfsroot=...`).

Nothing is copied into the machine's RAM, so 2 GiB is enough: the E2E
installs every Ubuntu release in a VM with 2048 MB. The other way to boot
the live server, `url=<ISO>`, downloads the whole ISO into RAM first, and a
machine with 2 GiB cannot hold it.

## Before you start

- isoboot installed ([Install isoboot](install.md)) in the namespace
  `isoboot-system`, with `nfsd.enabled: true` (the default). Without nfsd
  the controller runs without `--nfs-dir`, and every `iso` BootConfig goes
  to `Error` with `iso mode needs the controller's --nfs-dir`.
- TCP 111 and 2049 free on the node (see
  [Install isoboot](install.md#what-you-need)). The installer's NFS client
  asks the port mapper on TCP 111, so that port cannot be changed.
- Room in `dataDir` for the ISO and a full unpacked copy of it, for each
  `iso` BootConfig (about 3 GiB each for these releases). Nothing checks the
  free space first.

nfsd accepts connections only from `nfsd.allowedCIDRs` (default: the PXE
subnet, `dnsmasq.subnet`) and the node itself.

## 1. Apply the BootConfig

```bash
kubectl -n isoboot-system apply -f examples/ubuntu-26.04.yaml
kubectl -n isoboot-system get bootartifacts,bootconfigs
```

| Release | Example | ISO |
|---|---|---|
| 24.04.5 LTS | [`ubuntu-24.04.yaml`](../../examples/ubuntu-24.04.yaml) | `ubuntu-24.04.5-live-server-amd64.iso` |
| 26.04.1 LTS | [`ubuntu-26.04.yaml`](../../examples/ubuntu-26.04.yaml) | `ubuntu-26.04.1-live-server-amd64.iso` |
| 26.10 (beta until its release) | [`ubuntu-26.10.yaml`](../../examples/ubuntu-26.10.yaml) | `ubuntu-26.10-beta-live-server-amd64.iso` |

Each example is one BootArtifact (the ISO with its sha256 from
`releases.ubuntu.com`) and one BootConfig:

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootConfig
metadata:
  name: ubuntu-26.04
spec:
  iso:
    artifactRef: ubuntu-26.04-iso
    kernelPath: casper/vmlinuz
    initrdPath: casper/initrd
  kernelArgs: "console=ttyS0,115200 ip=dhcp netboot=nfs nfsroot={{.NFSRoot}} fsck.mode=skip autoinstall ds=nocloud;s={{.ProvisionAutomationBaseURL}}/ ---"
```

The BootConfig stays `Pending` while the ISO downloads, then the controller
unpacks it. When the kernel and initrd are in place it is `Ready`, and
`/static/ubuntu-26.04/vmlinuz` and `/static/ubuntu-26.04/initrd` are served.
The ISO is unpacked again only when the ISO file changes. How unpacking,
re-unpacking and its retries work is in
[BootConfig lifecycle](../reference/custom-resources.md#bootconfig-lifecycle).

### Kernel arguments

| Argument | What it does |
|---|---|
| `console=ttyS0,115200` | A console on the first serial port, which the E2E reads (the installer's screens still show on the screen). |
| `ip=dhcp` | casper brings up the network with DHCP. |
| `netboot=nfs` | casper mounts its root over NFS. |
| `nfsroot={{.NFSRoot}}` | The export to mount. httpd fills in `<node IPv4>:/<bootconfig>`, for example `192.168.1.10:/ubuntu-26.04`, using the node address the machine used to reach isoboot. It must be an IPv4 address. |
| `fsck.mode=skip` | casper does not checksum the whole tree, which would read all of it over NFS. |
| `autoinstall` | The install starts without asking *Continue with autoinstall? (yes\|no)* on the console. This is a kernel argument, not a line of the answer file ([why](../ubuntu-autoinstall-journey.md#the-continue-with-autoinstall-question)). |
| `ds=nocloud;s={{.ProvisionAutomationBaseURL}}/` | cloud-init's NoCloud source: the installer reads `user-data` and `meta-data` from the Provision's ProvisionAutomation at `http://<node>:8080/dynamic/automation/<provision>/`. Keep the final `/`; the file names are added to it ([NoCloud seed](../reference/templates.md#ubuntu-the-nocloud-seed)). |
| `---` | Ends the arguments for the installer. Arguments after it would be passed on to the installed system's kernel command line; the examples put none there. |

Never add:

| Argument or setting | Why not |
|---|---|
| `url=`, `iso-url=` or `toram` | They copy the ISO into RAM. The machine then needs several GiB more, and the NFS tree is not used. The E2E fails a row whose kernel command line has any of them. |
| `nfsopts=` | casper ignores it. |
| a `network:` section in the answer file | The installer's root file system is on NFS over the boot NIC. Reconfiguring that NIC can freeze the install. Without the section, the installed system uses DHCP on the detected NIC. |

## The answer file

The answer file is the ProvisionAutomation's `user-data`; `meta-data` sits
beside it. Both are Go templates, rendered by httpd for each request.

### The minimal file

The ProvisionAutomation `ubuntu-autoinstall` in
[Provision a machine, step 3](provision-a-machine.md#3-write-the-install-files)
is the
[smallest answer file](../ubuntu-autoinstall-journey.md#the-final-minimal-answer-file)
that installs Ubuntu unattended onto the whole disk without LVM, with a
user and an SSH server (each of its lines was taken out once to show it is
needed), plus what isoboot and a key login need:

| Entry | Why it is there |
|---|---|
| `#cloud-config`, `autoinstall:`, `version: 1` | Without them there is no answer file and the installer asks every screen, or stops with an error. |
| `proxy:` | The installer and apt fetch packages through squid. The `if` leaves the line empty when there is no proxy. |
| `identity:` with `hostname`, `username`, `password` | The user has no default; a missing one stops the install. `password` is a SHA-512 crypt hash. |
| `ssh: install-server: true` | Without it the installed system has no SSH server. |
| `ssh: allow-pw: false`, `authorized-keys` | SSH accepts the key only; the password works on the console. Not in the study's minimal file. |
| `storage: layout: name: direct` | The whole disk, without LVM. Without it the installer uses its default layout, LVM. |
| `early-commands` | Posts `InProgress` before the install starts. |
| `late-commands` | Posts `Complete` at the end of the install. |
| `meta-data`: `instance-id` | cloud-init's instance ID. |

Everything else takes the installer's default: locale `en_US.UTF-8`,
keyboard `us`, network by DHCP, the default archive mirror.

### With host keys and a machine ID

The E2E installs every Ubuntu row with
[`test/e2e/provision/automation/user-data`](../../test/e2e/provision/automation/user-data).
On top of the minimal file it installs the SSH host keys and the machine ID
from [Provision a machine, step 4](provision-a-machine.md#4-create-the-settings-and-host-keys).
This is that file as a ProvisionAutomation, with the host name read from
the ConfigMap (the E2E writes it into the file) and without the E2E's GRUB
serial console:

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: ProvisionAutomation
metadata:
  name: ubuntu-autoinstall-with-host-keys
spec:
  files:
    user-data: |
      #cloud-config
      autoinstall:
        version: 1
        # No network: section. The installer runs from an NFS root over the boot
        # NIC; reconfiguring that NIC can freeze the install.
        {{if .ProxyURL}}proxy: {{.ProxyURL}}{{end}}
        identity:
          hostname: {{ required .ConfigMaps "hostname" }}
          username: {{ required .ConfigMaps "default_user.username" }}
          password: "{{ required .ConfigMaps "default_user.password" }}"
        ssh:
          install-server: true
          allow-pw: false
          authorized-keys:
            - "{{ required .ConfigMaps "default_user.ssh_public_key" }}"
        storage:
          layout:
            name: direct
        user-data:
          ssh_deletekeys: false
        early-commands:
          - curl -X POST -d 'provisionName={{.ProvisionName}}&phase=InProgress' {{.UpdatePhaseURL}}
        late-commands:
          - printf '%s' '{{ required .Secrets "ssh_host_ecdsa_key_b64" }}' | base64 -d > /target/etc/ssh/ssh_host_ecdsa_key
          - printf '%s' '{{ required .Secrets "ssh_host_ed25519_key_b64" }}' | base64 -d > /target/etc/ssh/ssh_host_ed25519_key
          - printf '%s' '{{ required .Secrets "ssh_host_rsa_key_b64" }}' | base64 -d > /target/etc/ssh/ssh_host_rsa_key
          - chmod 600 /target/etc/ssh/ssh_host_ecdsa_key /target/etc/ssh/ssh_host_ed25519_key /target/etc/ssh/ssh_host_rsa_key
          - curtin in-target --target=/target -- sh -c 'for t in ecdsa ed25519 rsa; do ssh-keygen -y -f /etc/ssh/ssh_host_${t}_key > /etc/ssh/ssh_host_${t}_key.pub; chmod 644 /etc/ssh/ssh_host_${t}_key.pub; done'
          - printf '%s\n' '{{ required .ConfigMaps "machine_id" }}' > /target/etc/machine-id
          - chmod 444 /target/etc/machine-id
          - curl -X POST -d 'provisionName={{.ProvisionName}}&phase=Complete' {{.UpdatePhaseURL}}
    meta-data: |
      instance-id: {{ required .ConfigMaps "hostname" }}
```

`user-data: ssh_deletekeys: false` keeps cloud-init from replacing the
installed host keys on the first boot. The host keys are read in base64
(the `_b64` keys), because a multi-line value inside a YAML list item would
break the file. To use this file, apply it and set the Provision's
`provisionAutomationRef` to `ubuntu-autoinstall-with-host-keys`.

## 2. Install

Follow [Provision a machine](provision-a-machine.md) from step 2. For 24.04
or 26.10, set the Provision's `bootConfigRef` to `ubuntu-24.04` or
`ubuntu-26.10`. What the boot looks like:

| Step | Where you see it |
|---|---|
| iPXE loads `/static/ubuntu-26.04/vmlinuz` and `/static/ubuntu-26.04/initrd`. | nginx access log |
| casper asks nfsd's port mapper (TCP 111) where NFS listens, then mounts `/ubuntu-26.04`. | nfsd log: `portmap getport`, then `msg=mount client=<ip>:<port> export=/ubuntu-26.04` |
| cloud-init fetches `user-data` and `meta-data`. | nginx access log |
| `early-commands` posts `InProgress`. | Provision `InProgress` |
| `late-commands` posts `Complete`; the installer reboots. | Provision `Complete` |
| The next PXE boot gets 404 (no `Pending` Provision) and the firmware boots the disk. | httpd log `no pending provision` |

```bash
kubectl -n isoboot-system logs deploy/isoboot-nfsd | grep -E 'getport|mount'
```

In the answer-file study, with 2 GiB of RAM, an install took about 6 to 8
minutes from power-on to the installer's reboot.

## GA or HWE kernel on 24.04

The 24.04.5 ISO has two kernels. The example boots the GA (general
availability) kernel, `casper/vmlinuz` with `casper/initrd`, which is the
one the E2E tests. For hardware that needs a newer kernel, use the HWE
(hardware enablement) pair from the same ISO, in the BootConfig
`ubuntu-24.04` of
[`examples/ubuntu-24.04.yaml`](../../examples/ubuntu-24.04.yaml):

<!-- docs-test: skip (part of a BootConfig spec) -->
```yaml
spec:
  iso:
    artifactRef: ubuntu-24.04-iso
    kernelPath: casper/hwe-vmlinuz
    initrdPath: casper/hwe-initrd
```

Changing only `kernelPath` and `initrdPath` does not unpack the ISO again:
the controller copies the other kernel and initrd from the tree it already
has. Two BootConfigs for the same ISO, one GA and one HWE, each unpack their
own copy.

## Known limitations

- **Changing the ISO under running installs breaks them.** When the ISO file
  of a BootConfig changes (a new URL or hash), the controller unpacks the
  new ISO beside the old tree, then puts it in place of the old one and
  deletes the old one. Machines installing from the old tree lose their
  files. Change the ISO only when no install from that BootConfig is
  running. While the new ISO downloads, the BootConfig is `Pending` and no
  machine is sent to the installer. If the new ISO lacks `kernelPath` or
  `initrdPath`, the old tree, kernel and initrd stay and the BootConfig goes
  to `Error`.
- **One tree per BootConfig.** Each `iso` BootConfig keeps its own unpacked
  copy, even when two use the same ISO, and nothing checks the free space
  first.
- **The apt proxy stays in the installed system.** Autoinstall's `proxy:`
  is written into the installed system's apt configuration. If the
  installed machine cannot reach squid later, remove it in a late command.
- **IPv4 only.** `{{.NFSRoot}}` must be an IPv4 address: the installer's
  NFS client (klibc `nfsmount`) takes nothing else. If a machine reaches
  isoboot by a host name or an IPv6 address, httpd answers 500 and logs
  `nfsroot needs an IPv4 address`.
- **Long names.** The BootConfig's name is the export and directory name.
  BootConfig names are limited to 200 characters.

If something does not work, see [Troubleshoot](troubleshoot.md).
