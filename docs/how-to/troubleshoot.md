# Troubleshoot

Find the symptom in the tables below, then confirm the cause where the table
says to look. The commands assume the namespace `isoboot-system` and the
Helm release name `isoboot`, so the Deployments are `isoboot-controller-manager`,
`isoboot-httpd`, `isoboot-nginx`, `isoboot-dnsmasq`, `isoboot-squid` and
`isoboot-nfsd`.

## Where to look

| What | Command | What it tells you |
|---|---|---|
| Every resource and its phase | `kubectl -n isoboot-system get bootartifacts,bootconfigs,machines,provisions` | Which step is stuck. BootArtifacts also show `Failures`, the number of failed downloads in a row. |
| Why a resource is in its phase | `kubectl -n isoboot-system get bootconfig ubuntu-26.04 -o jsonpath='{.status.phase}{": "}{.status.message}{"\n"}'` (or `describe`) | The controller's message, for example `waiting for iso artifact "ubuntu-26.04-iso" to be Ready` or `invalid kernelArgs: ...`. |
| The controller | `kubectl -n isoboot-system logs deploy/isoboot-controller-manager` | Downloads, hash checks, ISO unpacks (`Extracting ISO tree`, `ISO tree extracted`), errors. |
| httpd | `kubectl -n isoboot-system logs deploy/isoboot-httpd` | What it answered each machine and installer, and why ([below](#what-httpd-answers)). |
| dnsmasq | `kubectl -n isoboot-system logs deploy/isoboot-dnsmasq` | Its proxyDHCP and TFTP traffic (it runs with `--log-dhcp`). MACs are written with colons. |
| nginx | `kubectl -n isoboot-system exec deploy/isoboot-nginx -- cat /var/run/access.log` | Every HTTP request from machines and installers, with its status code and user agent (`iPXE/...` for iPXE). The access log is a file in the pod, not the pod's log. |
| nfsd | `kubectl -n isoboot-system logs deploy/isoboot-nfsd` | Exports, port-mapper lookups (`portmap getport`), mounts (`msg=mount ... export=/<bootconfig>`), and refusals. More with `--set nfsd.logLevel=debug` or `trace` (every request). |
| squid | `kubectl -n isoboot-system exec deploy/isoboot-squid -- cat /var/log/squid/access.log` | Package downloads. Off by default: install with `--set squid.log.access=true`. Both logs are also on the node under `<dataDir>/squid/logs/`; `cache.log` is always written. |
| Pods | `kubectl -n isoboot-system get pods`, `describe pod`, `logs --previous` | Restarts, OOM kills, failing init containers. |
| The machine | its screen or serial console | What the installer itself says. |

In the provision E2E, every row keeps all of this plus the guest's serial
console and screenshots; see [Run the E2E tests](run-the-e2e-tests.md#read-the-logs).

## Symptoms

### Downloads and BootConfigs

| Symptom | Cause | Fix |
|---|---|---|
| BootArtifact `Error`, message `hash mismatch: expected <a> got <b>` | The file at `url` is not the one the hash describes. Usually a moving URL (Debian's `current`, a "latest" link) whose file was replaced by a newer release, or a hash copied from another file. The downloaded file is thrown away. | Use a URL that never changes (Debian's dated installer directory, a versioned ISO) and take the hash from the publisher's checksum file. Edit the BootArtifact; the controller downloads again. |
| BootArtifact `Error`, message `download failed: HTTP <code>` | The URL is wrong or gone. | Fix the `url`. |
| BootArtifact `Error`, message `download failed: <network error>`, `writing file: ...` or `Content-Length mismatch: ...` | The controller cannot reach the server, or the connection broke. The controller downloads directly, not through squid. A download (including its body) that takes more than 30 minutes is cut off. | Check that the node reaches the URL (`curl -I <url>` on the node). On a slow link, fetch large ISOs from a closer mirror. |
| BootArtifact stays `Error` and `Failures` keeps growing | Every attempt fails. The controller tries again after 10 s, then twice as long each time, up to 320 s ([BootArtifact lifecycle](../reference/custom-resources.md#bootartifact-lifecycle)). `Failures` counts the failed attempts in a row and goes back to 0 once the file is `Ready`. | Fix the cause from the message. A corrected URL or hash is tried at once; a fix on the server or the network is picked up by the next attempt, at most 320 s later. |
| BootArtifact `Downloading` for a long time | A multi-GB ISO on a slow link. | Watch `<dataDir>/nginx/static/artifacts/<name>/` grow on the node. |
| BootConfig `Pending`, message `waiting for <kind> artifact "<name>" to be Ready` | That BootArtifact is not `Ready` yet. | Look at the BootArtifact. |
| BootConfig `Error`, message `<kind> artifact "<name>" not found` | No BootArtifact of that name in the BootConfig's namespace. | Create it, or fix the reference. All resources must be in the namespace isoboot is installed in. |
| BootConfig `Error`, message `invalid kernelArgs: ...` | `kernelArgs` is not a valid template, uses a variable that does not exist (for example the removed `{{.ISOURL}}`: `can't evaluate field ISOURL`), or renders to more than one line (`kernel args must be a single line`). | Use only `{{.ProvisionAutomationBaseURL}}`, `{{.ProxyURL}}`, `{{.UpdatePhaseURL}}`, `{{.ProvisionName}}` and, for `iso` BootConfigs, `{{.NFSRoot}}`. Write the arguments on one line. In YAML, a folded scalar (`kernelArgs: >`) joins its lines and is fine; a literal one (`|`) keeps its line breaks and fails as soon as it has more than one line. |
| BootConfig `Error`, message `extracting iso tree: "casper/vmlinuz" in iso: ...` or `extracting from iso: opening "casper/vmlinuz" in iso: ...` | `kernelPath` or `initrdPath` is not a file in this ISO. With an earlier tree in place, that tree, kernel and initrd are kept. | Correct the path. A failed unpack is retried after 10 s, then twice as long each time, up to 30 minutes; changing the paths retries at once. |
| BootConfig `Error`, message `iso mode needs the controller's --nfs-dir` | The chart was installed with `nfsd.enabled: false`. | Enable nfsd; Ubuntu installs need it. |
| BootConfig `Error`, message `invalid kernelPath "...": path traversal not allowed` | The path contains `..`. | Use the path inside the ISO, for example `casper/vmlinuz`. |

### PXE boot

| Symptom | Cause | Fix |
|---|---|---|
| The machine boots its disk instead of installing | httpd did not answer it with an iPXE script, so iPXE exits and the firmware boots the next device. | Follow [The machine boots its disk](#the-machine-boots-its-disk-instead-of-installing). |
| httpd answers 409, log `duplicate match ... multiple machines with MAC <mac>` | Two Machines have the same MAC (MACs are compared in lower case). | Delete one. |
| httpd answers 409, log `duplicate match ... multiple pending provisions for MAC <mac>` | The Machine has more than one `Pending` Provision; httpd cannot tell which to install. | Delete all but one. |
| httpd answers 500, log `nfsroot needs an IPv4 address, but the machine reached isoboot through another host` | An `iso` BootConfig, and the machine reached isoboot by a host name or an IPv6 address. The installer's NFS client takes only an IPv4 address. The `boot.ipxe` that dnsmasq hands out always uses the node's IPv4 address on the PXE subnet, so this happens only when something else chains to `/dynamic/conditional-boot`. | Chain with the node's IPv4 address. |
| No PXE answer at all; nothing about the MAC in the dnsmasq log | The machine is not on the PXE subnet's network segment, or it is not an x86 BIOS or x86-64 UEFI client (dnsmasq offers iPXE only to those), or its firmware does not try the network. | Put the machine on `dnsmasq.subnet`'s segment; enable network boot in its firmware. |
| dnsmasq pod stuck in `Init`, log (`kubectl -n isoboot-system logs deploy/isoboot-dnsmasq -c generate-boot-ipxe`) `FAIL: no interface matches subnet <subnet>` | The node has no address in `dnsmasq.subnet`. | Set `dnsmasq.subnet` to the node's PXE network. |
| dnsmasq pod stuck in `Init`, log (`-c extract-ipxe`) `FAIL: timed out waiting for .../ipxeboot.tar.gz` (it waits 300 seconds, then the pod restarts it) | The chart's iPXE BootArtifact (`isoboot-ipxe`) is not `Ready`. | Look at that BootArtifact's message. |
| Pods stay in `ContainerCreating`, `describe pod` shows `hostPath type check failed: <dataDir> is not a directory` | `dataDir` does not exist on the node. | `sudo mkdir -p /data/isoboot && sudo chown 65532:65532 /data/isoboot` (or your `dataDir`) |
| An init container fails with `cannot create <dataDir>/... — ensure dataDir is writable by UID 65532` | `dataDir` is not writable by UID 65532. | `sudo chown 65532:65532 /data/isoboot` |

### Install files and status calls

| Symptom | Cause | Fix |
|---|---|---|
| An install file answers 404, httpd log `automation file not served ... provision "<name>" is "Complete", files are served only while it is Pending or InProgress` | The Provision is no longer installing. Files can contain Secrets, so they are served only while the Provision is `Pending` or `InProgress`. | Delete and re-create the Provision to install again. |
| An install file answers 404, log `automation file not served ... file not found: "<file>" in provision automation "<name>"` | The kernel arguments ask for a file name the ProvisionAutomation does not have (for example `ks.cfg` against a file named `kickstart.cfg`). | Make the names match. |
| An install file answers 404, log `automation file not served ... getting configmap "<name>"` (or `secret`) | A ConfigMap or Secret listed in the Provision does not exist. | Create it, or remove it from the Provision. |
| An install file answers 500, log `automation render failed ... missing key "<key>"` | `{{ required ... "<key>" }}` found no such key in the merged ConfigMaps or Secrets. | Add the key, or fix its spelling. |
| An install file answers 500, log `automation render failed ... parsing template ...` or `executing template ...` | The file is not a valid Go template. | Fix the template. Literal `{{` in a file must be written as `{{"{{"}}`. |
| The Provision stays `InProgress` | The installer stopped or rebooted before it posted `Complete`, or its last command failed (Debian's late command is one `&&` chain). | Look at the machine's console. To install again, delete and re-create the Provision. |
| The Provision stays `Pending` after a finished install, and the machine installs again on every boot | The `InProgress` call never arrived, so `Complete` was refused with 409 (`Complete` is only accepted from `InProgress`). While the Provision is `Pending`, every PXE boot is sent to the installer. | Check the installer's early command; see [installer cannot reach nginx or squid](#the-installer-cannot-reach-nginx-or-squid). To stop the loop, delete the Provision. |
| `/dynamic/status` answers 409 `invalid phase transition: cannot transition from <a> to <b>` | A call out of order, or a second `InProgress` from an installer that started again. | Harmless if the install goes on; the order is `Pending` → `InProgress` → `Complete`. |
| Debian: the Provision stays `Pending`, and nginx never serves `preseed.cfg` | The network card needs firmware the netboot initrd lacks, so the installer has no network. | Use the `debian-13-firmware` BootConfig ([Debian](debian.md#network-cards-that-need-firmware)). |
| Ubuntu stops at `Continue with autoinstall? (yes\|no)` | `kernelArgs` lacks `autoinstall`. | Add `autoinstall` to `kernelArgs`. It cannot be set in the answer file. |
| Ubuntu shows its language screen and waits | There is no answer file: `user-data` was not served (check the nginx access log for `GET /dynamic/automation/<provision>/user-data`; a request for `.../<provision>user-data` means `ds=nocloud;s=` lost its final `/`), its first line is not `#cloud-config`, it is not valid YAML, or it has no `autoinstall:` section. | See the [answer-file study](../ubuntu-autoinstall-journey.md#every-line-is-needed-ablation) for what each missing line does. |
| Ubuntu freezes during the install | A `network:` section in the answer file reconfigured the NIC the installer's NFS root runs over. | Remove the `network:` section. |
| Rocky or AlmaLinux: the installed system drops into emergency mode on its first boot | Mislabelled SELinux files. | Use the relabel steps of the [kickstart](rocky-and-alma.md#selinux). |

### NFS (Ubuntu)

| Symptom | Cause | Fix |
|---|---|---|
| nfsd pod crash-loops, log `msg="nfsd failed" error="listen for port mapper: listen tcp :111: bind: address already in use"` | `rpcbind` (pulled in by `nfs-common`) holds TCP 111. | `sudo systemctl disable --now rpcbind.socket rpcbind.service nfs-server` on the node. `sudo ss -tlnp '( sport = :111 or sport = :2049 )'` shows what listens. |
| nfsd pod crash-loops, log `listen for NFS: ... address already in use` | A kernel NFS server, or another program, holds `nfsd.port` (2049). | Stop it, as above. |
| The installer stops before its first screen; nfsd logs `connection refused ... reason="address not allowed"` | The machine's address is outside `nfsd.allowedCIDRs` (default: `dnsmasq.subnet`). | Add the machine's network to `nfsd.allowedCIDRs`. |
| nfsd logs `mount refused ... reason="no such export"` | No unpacked tree with that name: the BootConfig is not `Ready`, or its name differs from the one in `nfsroot=`. | Wait for the BootConfig to be `Ready`; check `<dataDir>/nfs/<bootconfig>/` on the node. |
| nfsd logs `connection refused ... reason="too many connections from this address"` | More than 8 NFS (or 4 port-mapper) connections from one address. Machines behind one NAT address share that limit. | Do not install through NAT. |
| nfsd logs no `portmap getport` and no `mount` for the machine | The installer never got as far as NFS (no network, wrong kernel arguments), or a firewall between it and the node drops TCP 111 or 2049. | Look at the machine's console and the kernel arguments. |

nfsd writes at most one refusal warning every 5 seconds; the next one says
how many it left out (`suppressed=<n>`).

### The installer cannot reach nginx or squid

nginx (`/static/`, `/dynamic/`) answers only the PXE subnet
(`dnsmasq.subnet`), localhost and the node's own addresses; anything else
gets 403. squid answers only the PXE subnet and localhost, and refuses:

- destinations on the node itself (127.0.0.0/8) and link-local addresses;
- `squid.blockedDestinationCIDRs` (default: k3s's pod and service networks,
  10.42.0.0/16 and 10.43.0.0/16);
- ports other than 80, 443 and nginx's port, and `CONNECT` to anything but 443.

| Symptom | Cause | Fix |
|---|---|---|
| nginx access log: 403 for the machine's requests | The machine's address (as the installer got it from DHCP) is not in `dnsmasq.subnet`. | Install machines on the PXE subnet only. |
| squid access log: `TCP_DENIED/403` | The source is outside the PXE subnet, or the destination is one of those above, for example a package mirror inside the cluster's networks or on another port. | Use a mirror on port 80 or 443, outside the blocked networks; set `squid.blockedDestinationCIDRs` to your cluster's networks. |
| Packages download slowly every time, squid shows `TCP_TUNNEL` | The repository URL is `https://`: squid can only tunnel it, not cache it. | Use `http://` repository URLs, as the examples do. |
| The installed machine cannot run `apt` | The installer's proxy setting (Debian's `mirror/http/proxy`, Ubuntu's `proxy:`) stays in the installed system, and squid serves only the PXE subnet. | Remove the proxy from the installed system's apt configuration. |

### Pods

| Symptom | Cause | Fix |
|---|---|---|
| A host-network pod (nginx, dnsmasq, squid, nfsd) crash-loops with `address already in use` | Something else on the node uses UDP 67, 69 or 4011, or TCP 111, 2049, 3128 or 8080. | Stop it, or change `nginx.port` / `squid.port` / `nfsd.port`. TCP 111 cannot be changed. |
| A pod shows restarts with `OOMKilled` | It hit its memory limit. | `kubectl -n isoboot-system describe pod <pod>`; raise its limit in the chart values (`resources` for the controller, `<component>.resources` for the others; for nfsd keep `nfsd.goMemLimit` about 50 MiB below it). |

## The machine boots its disk instead of installing

dnsmasq hands iPXE a script that chains to
`http://<node>:8080/dynamic/conditional-boot?mac=<mac>`. If that request
gets anything but 200, iPXE exits and the firmware boots its next device,
normally the disk. Go through the path in order.

1. **Did dnsmasq answer?** Look for the MAC (with colons) in the dnsmasq
   log. Nothing there: see *No PXE answer* above.
2. **Did iPXE reach nginx?** Look for `GET /dynamic/conditional-boot?mac=`
   in the nginx access log, and its status code.
3. **What did httpd answer, and why?** Look for the MAC in the httpd log:

| httpd log | Status | Cause | Fix |
|---|---|---|---|
| `conditional-boot request mac=<mac>` | 200 | httpd sent the installer. If the machine still boots its disk, iPXE could not load the kernel or initrd: look for their `GET /static/...` lines in the nginx access log. | |
| `no pending provision mac=<mac>` | 404 | No Machine has this MAC in httpd's namespace, or the Machine has no Provision in phase `Pending`. A Provision is `InProgress` or `Complete` after an install; only `Pending` sends a machine to the installer. | Check the Machine's `mac` (hyphens) and namespace. To install again, delete and re-create the Provision. |
| `not booting the installer mac=<mac> reason="boot config not ready: \"<name>\" is \"<phase>\": <message>"` | 404 | The Provision's BootConfig is not `Ready`. | Fix the BootConfig; the message says why. |
| `duplicate match mac=<mac> ...` | 409 | Two Machines with the MAC, or two `Pending` Provisions for the Machine. | Delete the extra one. |
| `boot directive lookup failed mac=<mac> error="getting boot config ..."` | 500 | The Provision names a BootConfig that does not exist. | Fix `bootConfigRef`. |
| `kernel args template failed mac=<mac> ...` | 500 | `kernelArgs` did not render (rare: the controller checks it first). | See the BootConfig's message. |
| `nfsroot needs an IPv4 address ...` | 500 | See the table above. | |

4. **Is the network first in the boot order?** A machine that boots its disk
   first never asks isoboot once that disk holds a system.

## What httpd answers

httpd sits behind nginx's `/dynamic/`. Every answer to
`/conditional-boot` and `/automation` carries `Cache-Control: no-store`.

| Endpoint | 200 | 400 | 404 | 409 | 500 |
|---|---|---|---|---|---|
| `GET /dynamic/conditional-boot?mac=<mac>` | iPXE script: kernel, initrd, rendered kernel arguments | `mac` missing or not hyphen-separated hex | no `Pending` Provision for the MAC, or its BootConfig not `Ready` | duplicate Machine or `Pending` Provision | lookup failed, template failed, `nfsroot` not IPv4 |
| `GET /dynamic/automation/<provision>/<file>` | the rendered file | invalid Provision name | Provision, ProvisionAutomation, file, ConfigMap or Secret missing, or the Provision not `Pending`/`InProgress` | | the template did not render |
| `POST /dynamic/status` (`provisionName`, `phase`) | `OK`; phase changed | parameter missing or invalid; `phase` other than `InProgress` or `Complete` | no such Provision | transition not allowed, or a concurrent update | other errors |

httpd logs every 404, 409 and 500 of `/conditional-boot` and `/automation`
with its reason. For `/status` it logs each phase change
(`provision status updated`) and each 500, but not its 400, 404 and 409
answers: look for those in the nginx access log.
