# Custom resources

isoboot is driven by five custom resources in the API group
`isoboot.github.io`, version `v1alpha1`. This page describes each one: what
it is for, its fields and validation, its status, what `kubectl get` shows,
and what the controller and httpd do with it.

- [How they fit together](#how-they-fit-together)
- [BootArtifact](#bootartifact): one file to download and verify
- [BootConfig](#bootconfig): what a machine boots
- [Machine](#machine): a machine, by MAC address
- [ProvisionAutomation](#provisionautomation): the install files
- [Provision](#provision): one install of one machine
- [What happens when ...](#what-happens-when-)

## How they fit together

```text
                              Provision
                   one install of one machine
                   status.phase: Pending -> InProgress -> Complete
        |                  |                      |                   |
   machineRef        bootConfigRef      provisionAutomationRef   configMaps, secrets
        v                  v                      v                   v
     Machine           BootConfig         ProvisionAutomation     ConfigMap, Secret
   (spec.mac)       (netboot or iso,      (spec.files: install    (data for the
                     kernelArgs)           file templates)          templates)
                           |
       netboot: kernelRef, initrdRef, firmwareRef
       iso:     artifactRef
                           v
                      BootArtifact
                 (url + sha256 or sha512)
```

Every reference is a plain name. It always points to an object **in the same
namespace** as the object that holds it. There are no owner references and no
finalizers: deleting an object never deletes the objects it points to, and
nothing stops you deleting an object that others still point to.

| Kind | You write | Status written by | Read by |
|---|---|---|---|
| BootArtifact | `spec` (the chart also creates one, `<release>-ipxe`, for iPXE) | controller | controller; httpd (the file names, in netboot mode) |
| BootConfig | `spec` | controller | controller; httpd |
| Machine | `spec` | (no status) | httpd |
| ProvisionAutomation | `spec` | (no status) | httpd |
| Provision | `spec` | controller (sets `Pending`); httpd (sets `InProgress`, `Complete`) | controller; httpd |

What happens at boot time:

1. iPXE asks nginx for `/dynamic/conditional-boot?mac=<mac>`, and nginx
   passes the request to httpd.
2. httpd finds the one **Machine** with that MAC, then the one **Provision**
   for that Machine whose phase is `Pending`.
3. If the Provision's **BootConfig** is `Ready`, httpd answers an iPXE script
   that loads the BootConfig's kernel and initrd, with `kernelArgs` rendered.
   Otherwise it answers 404 and the machine boots its local disk.
4. The installer fetches its files from the **ProvisionAutomation**, rendered
   with the data of the Provision's ConfigMaps and Secrets, and reports
   `InProgress` and `Complete` back to httpd.

### Common facts

| | |
|---|---|
| Scope | All five kinds are namespaced. |
| Namespace | The chart starts the controller and httpd with `--namespace=<release namespace>`. They see only that namespace. Objects in any other namespace are ignored: they get no status and are never served. Create everything in the release namespace. |
| One at a time | The controller handles one BootArtifact and one BootConfig at a time. A long download or ISO unpack delays the others of its kind. |
| Where files live | `dataDir` is a chart value (default `/data/isoboot`) on the node. Downloads go to `<dataDir>/nginx/static/artifacts/<bootartifact>/<file>`, boot directories to `<dataDir>/nginx/static/boot/<bootconfig>/`, unpacked ISOs to `<dataDir>/nfs/<bootconfig>/`. nginx serves `<dataDir>/nginx/static/boot/` as `/static/`. |

| Kind | Plural | Short name | Status subresource | Printer columns |
|---|---|---|---|---|
| BootArtifact | `bootartifacts` | none | yes | Phase, Failures, Age |
| BootConfig | `bootconfigs` | none | yes | Phase, Age |
| Machine | `machines` | `mach` | no status | MAC, Age |
| ProvisionAutomation | `provisionautomations` | `pa` | no status | Age |
| Provision | `provisions` | `prov` | yes | Machine, Phase, Age |

No printer column shows `status.message`. To see why something is not
`Ready`, for example:

```bash
kubectl -n isoboot-system get bootconfigs \
  -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,MESSAGE:.status.message
```

The examples on this page assume the chart was installed in the namespace
`isoboot-system`. Apply them with `kubectl apply -n isoboot-system -f <file>`.

---

## BootArtifact

One file to download over HTTPS and check against a SHA-256 or SHA-512 hash:
a kernel, an initrd, a firmware archive, an installer ISO, or the iPXE
tarball. BootConfigs point to BootArtifacts by name. The controller downloads
each file once and keeps it on the node.

### BootArtifact example

From [`examples/debian-13.yaml`](../../examples/debian-13.yaml):

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootArtifact
metadata:
  name: debian-13-kernel
spec:
  url: https://deb.debian.org/debian/dists/trixie/main/installer-amd64/20250803+deb13u7/images/netboot/debian-installer/amd64/linux
  sha256: "2b2358b37674d2505350528875bb17afae2a36522a9e8a9417eaca65a7da0e08"
```

### BootArtifact spec

| Field | Type | Required | Default | Validation | Meaning |
|---|---|---|---|---|---|
| `spec.url` | string | yes | | Must start with `https://`. Must not contain `/..` | Where to download the file. Redirects are followed. The last element of the URL path is the file name on disk and in the served path (`artifact` when the URL has no file name). |
| `spec.sha256` | string | one of `sha256`, `sha512` | | Exactly 64 hex digits, upper or lower case | Expected SHA-256 of the file. |
| `spec.sha512` | string | one of `sha256`, `sha512` | | Exactly 128 hex digits, upper or lower case | Expected SHA-512 of the file. |

Rules on the whole `spec`, checked by the API server:

- One of `sha256` or `sha512` must be set ("one of sha256 or sha512 is required").
- Not both ("sha256 and sha512 are mutually exclusive").

The hash is compared without regard to case.

### BootArtifact status

Written by the controller only.

| Field | Type | Meaning |
|---|---|---|
| `status.phase` | `Downloading`, `Ready`, `Error` | See below. Empty until the controller first sees the object. (`Pending` is also allowed by the schema, but nothing sets it.) |
| `status.message` | string | Empty when `Ready`, `Downloading` while downloading, the reason when `Error`. |
| `status.failureCount` | integer | Failures in a row. Reset to 0 when the file becomes `Ready`. |
| `status.lastFailureTime` | timestamp | Time of the last failure. Cleared when `Ready`. |
| `status.lastChecked` | timestamp | When the file was last verified or downloaded and the phase set to `Ready`. It does not change while the artifact stays `Ready`. |

| Phase | What moves the BootArtifact there |
|---|---|
| `Downloading` | The file is not on disk, or it was on disk with the wrong hash and has been deleted. The download has started. |
| `Ready` | The file is on disk and its hash matches `spec`. |
| `Error` | The download or the check failed. `failureCount` goes up by one. |

`Error` messages you may see:

| Message | Cause |
|---|---|
| `download failed: HTTP 404` (or another code) | The server did not answer 200. |
| `download failed: <error>` | Connection error, TLS error, DNS error, or the 30-minute limit ran out before the server answered. |
| `writing file: ... (Client.Timeout or context cancellation while reading body)` | The 30-minute limit for one download ran out while the file was still coming in. |
| `writing file: ...` (other) | The connection broke during the download, or the node's disk is full. |
| `hash mismatch: expected <hash> got <hash>` | The file downloaded completely but its hash is not the one in `spec`. Nothing is kept. |
| `Content-Length mismatch: expected <n> bytes, got <m>` | The connection ended early. |
| `creating request: ...` | The URL cannot be parsed. |
| `creating directory: ...`, `creating temp file: ...`, `syncing temp file: ...`, `closing temp file: ...`, `renaming file: ...`, `syncing directory: ...`, `stat file: ...` | A problem with the node's disk, such as a full disk or wrong owner of `dataDir`. |

### BootArtifact printer columns

| Column | Source |
|---|---|
| Phase | `status.phase` |
| Failures | `status.failureCount` (empty while it is 0, because the field is then left out) |
| Age | `metadata.creationTimestamp` |

Phase is also empty until the controller first sees the BootArtifact.

### BootArtifact lifecycle

- **Download.** The file is written to `<name>.tmp` beside its final place,
  hashed while it downloads, and renamed into place only when the hash
  matches. A failed download leaves nothing behind. One download may take at
  most 30 minutes, including the body: on a slow link a multi-GB ISO can fail
  with `writing file: ... (Client.Timeout ...)` for this reason, and every
  retry starts again from the first byte.
- **Retry.** After a failure the controller tries again after a wait that
  doubles: 10 s, 20 s, 40 s, 80 s, 160 s, then 320 s at most. The wait
  follows `failureCount`, which is kept in the status, so a controller
  restart tries once straight away and then waits as long as before. The
  controller's own status updates (`Downloading`, `Error`) do not start an
  attempt. A spec change, such as a corrected URL or hash, is tried at once;
  a fix on the server or the network is picked up by the next attempt.
- **Already on disk.** When the file is already there (after a controller
  restart, or when you re-create a deleted BootArtifact), the controller hashes
  it. If it matches, the BootArtifact is `Ready` without a download. If it does
  not, the file is deleted, a `Warning` event `HashMismatch` is recorded, and
  the file is downloaded again.
- **Controller restart.** Every BootArtifact's file is hashed again when the
  controller starts, and also after any change to the BootArtifact object
  (its labels, or its own status update once a download is done). Large ISOs
  take a while, and other BootArtifacts wait meanwhile.
- **Spec changes.**
  - New hash: the file on disk no longer matches, so it is deleted and
    downloaded again. BootConfigs that use it go to `Pending` until it is
    `Ready`.
  - Same hash in other letter case, or the same file's `sha512` instead of its
    `sha256`: the file still matches. Nothing is downloaded, and an ISO is not
    unpacked again.
  - New URL with a different file name: the new file is downloaded beside the
    old one. The old file stays on disk.
  - New URL with the same file name and the same hash: nothing happens. The
    file on disk already matches.
- **Deletion.** The file stays on disk under
  `<dataDir>/nginx/static/artifacts/<name>/`. BootConfigs that use the
  BootArtifact go to `Error` (`kernel artifact "<name>" not found` and so on).
  Re-creating a BootArtifact with the same name and hash makes it `Ready`
  again without a download.
- **The chart's iPXE artifact.** The chart creates the BootArtifact
  `<release>-ipxe` (for the release `isoboot`: `isoboot-ipxe`) from
  `dnsmasq.ipxe.url` and `dnsmasq.ipxe.sha512`. dnsmasq waits for its file
  when it starts. Leave it alone.

---

## BootConfig

What a machine boots: a kernel, an initrd and the kernel arguments. A
BootConfig has one of two modes:

- **netboot**: the kernel and initrd are BootArtifacts of their own
  (AlmaLinux, Rocky, Debian). With `firmwareRef`, the served initrd is the
  initrd with a firmware archive appended (how Debian's installer finds
  non-free NIC firmware).
- **iso**: one BootArtifact is an installer ISO (Ubuntu live-server). The
  controller unpacks the whole ISO to `<dataDir>/nfs/<bootconfig>/`, nfsd
  exports that tree read-only over NFSv3 as `/<bootconfig>`, and the
  kernel and initrd are copied out of it and served over HTTP. The ISO
  itself is not served. This mode needs nfsd (`nfsd.enabled: true`, the
  default).

### BootConfig examples

netboot, from [`examples/debian-13.yaml`](../../examples/debian-13.yaml):

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootConfig
metadata:
  name: debian-13
spec:
  netboot:
    kernelRef: debian-13-kernel
    initrdRef: debian-13-initrd
  kernelArgs: "console=ttyS0,115200 auto=true priority=critical {{if .ProxyURL}}mirror/http/proxy={{.ProxyURL}} {{end}}preseed/url={{.ProvisionAutomationBaseURL}}/preseed.cfg"
```

netboot with firmware, from
[`examples/debian-13-firmware.yaml`](../../examples/debian-13-firmware.yaml)
(it uses the kernel and initrd BootArtifacts of `examples/debian-13.yaml`):

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootConfig
metadata:
  name: debian-13-firmware
spec:
  netboot:
    kernelRef: debian-13-kernel
    initrdRef: debian-13-initrd
    firmwareRef: debian-13-firmware
  kernelArgs: "console=ttyS0,115200 auto=true priority=critical {{if .ProxyURL}}mirror/http/proxy={{.ProxyURL}} {{end}}preseed/url={{.ProvisionAutomationBaseURL}}/preseed.cfg"
```

iso, from [`examples/ubuntu-26.04.yaml`](../../examples/ubuntu-26.04.yaml):

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootArtifact
metadata:
  name: ubuntu-26.04-iso
spec:
  url: https://releases.ubuntu.com/26.04/ubuntu-26.04.1-live-server-amd64.iso
  sha256: "cc8a95cde20f6ced61a322420de00f10cc3c90ced545daa46cb9c1a117f1d927"
---
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

`examples/` has a tested BootConfig for every supported release. What an
Ubuntu answer file must contain is in
[Ubuntu answer file, one screen at a time](../ubuntu-autoinstall-journey.md).

### BootConfig spec

| Field | Type | Required | Default | Validation | Meaning |
|---|---|---|---|---|---|
| `metadata.name` | string | yes | | At most 200 characters | Names the boot directory, the NFS tree and the NFS export `/<name>`. |
| `spec.netboot` | object | one of `netboot`, `iso` | | | netboot mode. |
| `spec.netboot.kernelRef` | string | yes, in netboot mode | | Not empty | Name of the kernel's BootArtifact. |
| `spec.netboot.initrdRef` | string | yes, in netboot mode | | Not empty | Name of the initrd's BootArtifact. |
| `spec.netboot.firmwareRef` | string | no | | | Name of a firmware archive's BootArtifact. The served initrd is the initrd followed by this file. |
| `spec.iso` | object | one of `netboot`, `iso` | | | iso mode. |
| `spec.iso.artifactRef` | string | yes, in iso mode | | Not empty | Name of the ISO's BootArtifact. |
| `spec.iso.kernelPath` | string | yes, in iso mode | | Not empty. The controller also refuses any `..` path element | Path of the kernel inside the ISO, for example `casper/vmlinuz`. A leading `/` is allowed. |
| `spec.iso.initrdPath` | string | yes, in iso mode | | As `kernelPath` | Path of the initrd inside the ISO, for example `casper/initrd`. |
| `spec.kernelArgs` | string | no | empty | The controller renders it with sample values; if that fails, the BootConfig goes to `Error` | Kernel command line, a Go template. See [Kernel arguments](#kernel-arguments). |

Rules checked by the API server:

- Exactly one of `netboot` and `iso` ("must set exactly one of netboot or iso").
- `metadata.name` is at most 200 characters ("BootConfig names are limited to
  200 characters (used as NFS export and directory names)"). The name is also
  used, with a prefix, for temporary files beside the tree, and the whole must
  fit the 255-byte file name limit.

Do not name a BootConfig `boot.ipxe`. That is the file dnsmasq writes beside
the boot directories and every machine fetches first. Such a BootConfig goes
to `Error`, and deleting it deletes that file, so no machine can PXE-boot
until the dnsmasq pod restarts and writes it again.

### Kernel arguments

`kernelArgs` is a Go [`text/template`](https://pkg.go.dev/text/template)
that httpd renders each time a machine boots and appends to the iPXE
`kernel` line. It can use `{{.ProvisionAutomationBaseURL}}`,
`{{.UpdatePhaseURL}}`, `{{.ProvisionName}}`, `{{.ProxyURL}}` and, for iso
mode, `{{.NFSRoot}}`, and it must render to one line. The controller
renders it with sample values whenever the BootConfig changes; a template
that fails sets the BootConfig to `Error` with `invalid kernelArgs:
<reason>`, so you see the mistake before a machine boots. The values, the
rules and the errors at boot are in
[Templates: kernelArgs](templates.md#bootconfigspeckernelargs).

For Ubuntu, never add `url=`, `iso-url=` or `toram`: they copy the ISO into
RAM.

### BootConfig status

Written by the controller only.

| Field | Type | Meaning |
|---|---|---|
| `status.phase` | `Pending`, `Ready`, `Error` | See below. |
| `status.message` | string | Empty when `Ready`, otherwise why not. |

| Phase | What moves the BootConfig there | Retried |
|---|---|---|
| `Pending` | A BootArtifact it uses exists but is not `Ready`. Message: `waiting for kernel artifact "<name>" to be Ready` (or `initrd`, `firmware`, `iso`). | Every 5 s, and at once when the BootArtifact changes. |
| `Ready` | All files are in place. **Only a `Ready` BootConfig is ever booted.** | |
| `Error` | Something must be fixed. See the messages below. | Every 10 s, and at once when the spec or a BootArtifact it uses changes. A failed ISO unpack waits longer (see [BootConfig lifecycle](#bootconfig-lifecycle)). |

`Error` messages you may see:

| Message | Cause |
|---|---|
| `invalid kernelArgs: ...` | The template does not parse, uses an unknown variable, or renders to more than one line. |
| `kernel artifact "<name>" not found` (also `initrd`, `firmware`, `iso`) | No BootArtifact of that name in the namespace. |
| `iso mode needs the controller's --nfs-dir` | The chart was installed with `nfsd.enabled: false`. |
| `invalid kernelPath "<path>": path traversal not allowed` (also `initrdPath`) | The path has a `..` element. |
| `extracting iso tree: "<path>" in iso: ...` | `kernelPath` or `initrdPath` is not in the ISO (found while unpacking it). |
| `extracting from iso: opening "<path>" in iso: ...` | `kernelPath` or `initrdPath` was changed to a path that is not in the tree already unpacked. |
| `extracting iso tree: ...` (other) | The ISO could not be unpacked: an unsupported or damaged ISO, or a full disk. |
| `stat iso "<path>": ...` | The ISO BootArtifact is `Ready` but its file is gone from the node. |
| `creating kernel dir: ...`, `creating initrd dir: ...`, `creating kernel symlink: ...`, `creating initrd symlink: ...`, `concatenating initrd + firmware: ...`, `creating boot dir: ...`, `extracting from iso: ...` (other) | A problem with the node's disk. |

### BootConfig printer columns

| Column | Source |
|---|---|
| Phase | `status.phase` |
| Age | `metadata.creationTimestamp` |

### What is served

| Mode | Over HTTP (nginx) | Over NFS (nfsd) |
|---|---|---|
| netboot | `/static/<bootconfig>/kernel/<kernel file>` and `/static/<bootconfig>/initrd/<initrd file>`: links to the BootArtifacts' files. With `firmwareRef`, the initrd is a new read-only file (initrd then firmware) with the initrd's file name. | nothing |
| iso | `/static/<bootconfig>/vmlinuz` and `/static/<bootconfig>/initrd`: read-only copies from the unpacked tree | `/<bootconfig>`: the whole unpacked ISO, read-only |

The file names come from the BootArtifacts' URLs. For example, `debian-13`
serves `/static/debian-13/kernel/linux` and `/static/debian-13/initrd/initrd.gz`.

### BootConfig lifecycle

- **Ready gate.** httpd sends a machine to the installer only while its
  BootConfig is `Ready`. While a BootArtifact is downloaded again or an ISO
  is unpacked again, the BootConfig is not `Ready` and machines boot their
  local disk.
- **Unpacking an ISO.** The controller unpacks every directory, file and
  symlink of the ISO (Rock Ridge names kept). Directories get mode 0755 and
  files 0644. Symlinks that would point outside the tree are skipped, and
  so are special files. An ISO with extended attribute records, directories
  nested more than 64 deep, or files that add up to more than the ISO's own
  size is refused. Each iso-mode BootConfig keeps its own full copy, even
  when two use the same ISO, and nothing checks free space first.
- **While unpacking**, the BootConfig keeps the status it had before: no
  phase yet for a new BootConfig, or `Pending` with
  `waiting for iso artifact "<name>" to be Ready` after a download. The
  status changes when the unpack ends. For a 3 GB ISO that can take minutes.
- **When an ISO is unpacked again.** The controller records which ISO file
  (path, size, modification time) each tree came from. It unpacks again only
  when that file changes (the BootArtifact downloaded a new file) or the tree
  is missing. It does **not** unpack again when:
  - `kernelPath` or `initrdPath` changes: the kernel and initrd are copied
    again from the existing tree (a path that is not in it gives `Error`
    `extracting from iso: opening ...`, and the old copies stay);
  - only the BootArtifact's hash text changes (case, or `sha256` to `sha512`
    for the same file);
  - the controller restarts.
- **Replacing a tree.** A new tree is unpacked beside the old one, checked to
  contain `kernelPath` and `initrdPath`, and then swapped in. If the check
  fails, the old tree and its kernel and initrd stay as they were, but the
  BootConfig is `Error`, so no new machine boots from it. A successful swap
  removes the old tree, which **breaks installs that are running from it**:
  do not change the ISO while machines are installing.
- **Failed unpack backoff.** After a failed unpack the controller waits 10 s
  before it tries the same ISO with the same paths again, then 20 s, 40 s,
  and so on up to 30 minutes. The BootConfig stays `Error` with the same
  message meanwhile. Changing the ISO or the paths starts a new attempt at
  once. The backoff is kept in memory: a controller restart tries again at
  once.
- **Kernel and initrd copies (iso mode)** are compared with the tree by
  content and copied again only when they differ.
- **Firmware initrd (netboot)** is built again when the initrd or the
  firmware file is newer than it.
- **Switching mode.** Changing a BootConfig from `iso` to `netboot` removes
  its NFS tree and the `vmlinuz` and `initrd` copies. Changing from
  `netboot` to `iso` removes the `kernel` and `initrd` directories.
- **Spec changes** are picked up at once. A label or annotation change alone
  does not start a reconcile. A new `kernelArgs` is used from the next boot.
- **Deletion.** The boot directory `<dataDir>/nginx/static/boot/<name>/` and
  the NFS tree `<dataDir>/nfs/<name>/` (with its marker and any temporary
  files) are removed at once. If the controller was not running when the
  BootConfig was deleted, they are removed when it starts. The BootArtifacts
  and their files are kept.
- **Namespace.** Boot directories and trees are named after the BootConfig
  alone, so the controller acts only on BootConfigs in its own namespace.

---

## Machine

A machine to be installed, known by the MAC address of the NIC it PXE-boots
from. httpd looks up Machines by MAC when iPXE asks what to boot. A Machine
does nothing on its own: a [Provision](#provision) says what to install on
it.

### Machine example

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: Machine
metadata:
  name: web-server-01
spec:
  mac: 52-54-00-12-34-56
```

### Machine spec

| Field | Type | Required | Default | Validation | Meaning |
|---|---|---|---|---|---|
| `spec.mac` | string | yes | | Six pairs of hex digits separated by hyphens (`^([0-9A-Fa-f]{2}-){5}([0-9A-Fa-f]{2})$`). Colons are refused | MAC address of the NIC that PXE-boots. Upper or lower case. |

A Machine has no status.

### Machine printer columns

| Column | Source |
|---|---|
| MAC | `spec.mac` (as written) |
| Age | `metadata.creationTimestamp` |

### Machine lifecycle

- **Case.** MACs are compared in lower case. `52-54-00-AB-CD-08` and
  `52-54-00-ab-cd-08` are the same machine.
- **Duplicates.** If two Machines have the same MAC (even in different case),
  httpd answers 409 to that MAC, logs `duplicate match`, and the machine boots
  its local disk. Delete one of them.
- **Changes** to `spec.mac` take effect at the next PXE boot.
- **Deletion** has no effect on Provisions that name it, except that they can
  no longer be booted.

---

## ProvisionAutomation

The install files for an installer: a kickstart file, a Debian preseed, or
Ubuntu's autoinstall `user-data` and `meta-data`. Each file is a Go template.
httpd renders a file each time an installer fetches it, with the data of the
Provision that asked. One ProvisionAutomation can serve many Provisions.

### ProvisionAutomation example

The Ubuntu autoinstall files of
[Provision a machine](../how-to/provision-a-machine.md#3-write-the-install-files).
They go with the `ubuntu-26.04` BootConfig above, whose `kernelArgs` point
cloud-init at `{{.ProvisionAutomationBaseURL}}/`, so it fetches `user-data`
and `meta-data`. The ConfigMap keys it reads are created in
[step 4](../how-to/provision-a-machine.md#4-create-the-settings-and-host-keys)
of that guide.

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: ProvisionAutomation
metadata:
  name: ubuntu-autoinstall
spec:
  files:
    user-data: |
      #cloud-config
      autoinstall:
        version: 1
        # No network: section. The installer runs from an NFS root over the
        # boot NIC; reconfiguring that NIC can freeze the install.
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
        early-commands:
          - curl -X POST -d 'provisionName={{.ProvisionName}}&phase=InProgress' {{.UpdatePhaseURL}}
        late-commands:
          - curl -X POST -d 'provisionName={{.ProvisionName}}&phase=Complete' {{.UpdatePhaseURL}}
    meta-data: |
      instance-id: {{ required .ConfigMaps "hostname" }}
```

The tested kickstart (AlmaLinux, Rocky) and preseed (Debian) files are in
[`test/e2e/provision/automation/`](../../test/e2e/provision/automation/), and
as ProvisionAutomations in [Rocky Linux and AlmaLinux](../how-to/rocky-and-alma.md)
and [Debian](../how-to/debian.md). The kickstart is served as `ks.cfg` and
the preseed as `preseed.cfg`, the names the example BootConfigs'
`kernelArgs` ask for.

### ProvisionAutomation spec

| Field | Type | Required | Default | Validation | Meaning |
|---|---|---|---|---|---|
| `spec.files` | map of string to string | yes | | At least one entry. Every key must start with a letter or digit and contain only letters, digits, `-`, `_` and `.` (`^[A-Za-z0-9][-A-Za-z0-9_.]*$`), so no `/`, and no name starting with `.` (which rules out `.` and `..`) ("File names must be valid path components (no slashes or path traversal)") | File name to file content. The content is a Go template. |

A ProvisionAutomation has no status.

### Template data and serving

httpd renders a file each time an installer fetches
`GET /dynamic/automation/<provision>/<file>`, with the merged data of the
Provision's ConfigMaps (`.ConfigMaps`) and Secrets (`.Secrets`),
`{{.ProvisionName}}`, `{{.UpdatePhaseURL}}` and `{{.ProxyURL}}`. It serves
files only while the Provision is `Pending` or `InProgress`, since they
can hold rendered Secrets. The data, the functions, what a missing key
does, and every answer are in
[Templates: ProvisionAutomation files](templates.md#provisionautomation-files).

---

## Provision

One install of one machine: which Machine, which BootConfig to boot, which
ProvisionAutomation holds its install files, and which ConfigMaps and Secrets
fill in those files. Its phase says where the install is, and it is what
makes httpd answer a machine's PXE boot.

### Provision example

The Provision of
[Provision a machine](../how-to/provision-a-machine.md#5-create-the-provision):
it installs Ubuntu 26.04 on the Machine `web-server-01` with the BootConfig
`ubuntu-26.04` and the ProvisionAutomation `ubuntu-autoinstall` above. The
ConfigMaps and the Secret it lists are made in
[step 4](../how-to/provision-a-machine.md#4-create-the-settings-and-host-keys)
of that guide.

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: Provision
metadata:
  name: web-server-01-install
spec:
  machineRef: web-server-01
  bootConfigRef: ubuntu-26.04
  provisionAutomationRef: ubuntu-autoinstall
  configMaps:
  - default-user
  - web-server-01
  secrets:
  - web-server-01-host-keys
```

Watch it:

```bash
kubectl -n isoboot-system get provisions --watch
```

### Provision spec

| Field | Type | Required | Default | Validation | Meaning |
|---|---|---|---|---|---|
| `spec.machineRef` | string | yes | | Not empty | Name of the Machine to install. |
| `spec.bootConfigRef` | string | yes | | Not empty | Name of the BootConfig the machine boots. |
| `spec.provisionAutomationRef` | string | yes | | Not empty | Name of the ProvisionAutomation that holds the install files. |
| `spec.configMaps` | list of strings | no | | | ConfigMap names. Their `data` is merged into `.ConfigMaps` for the install files; a later ConfigMap wins over an earlier one for the same key. Nothing is mounted. |
| `spec.secrets` | list of strings | no | | | Secret names. Their data is merged into `.Secrets` in the same way. |

Nothing checks that the names exist when you create the Provision. A missing
object shows up at boot time (see [What happens when ...](#what-happens-when-)).

### Provision status

| Field | Type | Written by | Meaning |
|---|---|---|---|
| `status.phase` | see below | controller, httpd | Where the install is. |
| `status.message` | string | httpd | `Installation in progress` or `Installation complete`. |
| `status.lastUpdated` | timestamp | httpd | When httpd last changed the phase. |
| `status.ip` | string | nothing | Reserved. Never set. |

| Phase | What moves the Provision there | What httpd does for it |
|---|---|---|
| (empty) | A new Provision. The API server drops any `status` sent on create, so the schema default `Pending` does not apply. The controller sets `Pending` within a second or so. | Nothing: 404 to the PXE boot and to the install files. |
| `Pending` | The controller sets it on a Provision whose phase is empty. | Answers the machine's PXE boot with the installer, if the BootConfig is `Ready`. Serves the install files. |
| `InProgress` | The installer posts `phase=InProgress` to `/dynamic/status`. Allowed only from `Pending`. | Answers the PXE boot with 404, so a reboot during the install boots the local disk. Serves the install files. |
| `Complete` | The installer posts `phase=Complete`. Allowed only from `InProgress`. | 404 to the PXE boot and to the install files. |
| `WaitingForBootSource`, `Failed`, `ConfigError` | Nothing in isoboot sets them; the schema accepts them. | Treated like `Complete`. |

A Provision never goes back to `Pending` by itself. A machine that keeps
PXE-booting while its Provision is `Complete` boots its local disk.

### Provision printer columns

| Column | Source |
|---|---|
| Machine | `spec.machineRef` |
| Phase | `status.phase` |
| Age | `metadata.creationTimestamp` |

### Reporting the phase

Installers move the Provision forward themselves, with
`POST /dynamic/status` and the form fields `provisionName` and `phase`
(`InProgress`, then `Complete`). The request, the allowed transitions and
every answer are in [Templates: the status callback](templates.md#the-status-callback).

### Provision lifecycle

- **One Pending Provision per Machine.** If a Machine has two Provisions in
  phase `Pending`, httpd answers 409 to its MAC and the machine boots its
  local disk. Provisions in other phases do not count, so old `Complete`
  Provisions can stay.
- **Install again.** Delete the Provision and create it again, or create a
  new one for the Machine. The controller sets it to `Pending`. Or set the
  old one back to `Pending` by hand:

  ```bash
  kubectl -n isoboot-system patch provision web-server-01-install \
    --subresource=status --type=merge -p '{"status":{"phase":"Pending","message":""}}'
  ```
- **Spec changes** take effect at the next PXE boot or file fetch. Changing
  `bootConfigRef` during an install changes nothing for the running
  installer, which already has its kernel.
- **Deletion** does not touch the Machine, BootConfig, ProvisionAutomation,
  ConfigMaps or Secrets. An installer still running can no longer fetch files
  or report its phase (404).

---

## What happens when ...

| Situation | Outcome |
|---|---|
| A machine PXE-boots and no Machine has its MAC | 404. It boots its local disk. |
| The Machine has no Provision in phase `Pending` | 404. It boots its local disk. |
| The Pending Provision's BootConfig is not `Ready` (`Pending`, `Error`, or no phase yet) | 404, and httpd logs why. It boots its local disk. The Provision stays `Pending`: reboot the machine once the BootConfig is `Ready`. |
| The Pending Provision names a BootConfig that does not exist | 500. It boots its local disk. |
| Two Machines share a MAC, or a Machine has two Pending Provisions | 409. It boots its local disk. |
| The Debian firmware archive is missing for a NIC that needs it | The installer cannot fetch its preseed. The Provision stays `Pending`. Use a `firmwareRef` BootConfig such as `debian-13-firmware`. |
| The Provision names a ConfigMap or Secret that does not exist | The machine still boots the installer, but every install file answers 404, so the installer stops at its first fetch. Create the object; the next fetch uses it. |
| A BootArtifact's URL is wrong or answers an error, or its hash is wrong | `Error`. The controller tries again after 10 s, then twice as long each time, up to 320 s (see [BootArtifact lifecycle](#bootartifact-lifecycle)); with a wrong hash, each attempt downloads the whole file again. `failureCount` climbs. Fixing the spec retries at once. |
| You change a BootArtifact's hash | Its file is deleted and downloaded again. BootConfigs using it are `Pending` until it is `Ready`. An ISO is then unpacked again, which breaks installs running from the old tree. |
| You delete a BootArtifact | Its file stays on disk. BootConfigs using it go to `Error`. |
| You delete a BootConfig | Its boot directory and NFS tree are removed at once. |
| The controller was down while a BootConfig was deleted | Its files are removed when the controller starts. |
| You switch a BootConfig between `netboot` and `iso` | The other mode's files are removed. It goes to `Ready` when the new files are in place. |
| An installer reboots into PXE after reporting `InProgress` | 404. It boots its local disk and is not installed twice. |
| You create objects in a namespace other than the release namespace | Nothing happens. The controller and httpd do not see them. |
