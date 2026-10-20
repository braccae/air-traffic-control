# vm/lxc — Linux Containers (LXC) via libvirt

> **STATUS: UNTESTED STUB.** This plugin has **never been run against a real
> libvirt LXC host**. It was authored without access to a machine running
> `libvirtd` with the `lxc` driver enabled. Everything below the unit tests is a
> best-effort implementation based on documented `virsh` behaviour and the
> existing `vm/libvirt` plugin precedent. Treat all container data and all
> mutations as unverified until the checklist at the bottom is completed.

## What this plugin is

A compile-time Go plugin (`backend-go/plugins/vm/lxc`) that inspects and drives
LXC **system containers** through the libvirt LXC driver (`lxc:///`). It is
registered as `vm/lxc` (Kind `vm`) and renders as the **Linux Containers (LXC)**
tab in the host sub-sidebar (`src/components/LxcContainers.tsx`).

## Read paths (implemented, unverified)

`Read` runs real commands over the host runner — no fabricated data:

| Step | Command |
| --- | --- |
| Binary probe | `command -v virsh` |
| Container list | `virsh -c lxc:/// list --all` |
| Per-container specs | `virsh -c lxc:/// dumpxml '<name>'` |

`<vcpu>` and `<memory unit='KiB'>` are parsed out of the domain XML. When a value
cannot be read it stays `-1` (**unknown**) rather than being invented.

### Honest-failure contract (Zero-Mock Policy)

When `virsh` is missing or the `lxc:///` driver is unreachable, `Read` returns a
`ReadResult` with `hasLibvirt: false`, `containers: []`, and a human-readable
`libvirtError`. It never returns fabricated containers. The LXC tab renders this
as an explicit "no libvirt LXC host available" panel.

## Actions (implemented, unverified)

Registered actions compile to declarative `community.general.virt` tasks with
`uri: lxc:///`:

| Action | `state` |
| --- | --- |
| `start` | `running` |
| `shutdown` | `shutdown` |
| `destroy` | `destroyed` |
| `suspend` | `paused` |

## Unimplemented operations (deliberate)

These are **not** registered as actions and are surfaced in the Read payload
(`unimplemented`) so the UI lists them as explicitly unavailable:

- `create` — container provisioning
- `undefine` — destroy + undefine a domain
- `autostart` — boot autostart toggle
- `console` — interactive console attach
- `snapshot` — snapshot / clone

## Verified against unit tests only

`lxc_test.go` covers registration, both honest-failure probe paths, the
honest-failure read path (no fabricated containers), real `virsh` list/`dumpxml`
parsing, and action compilation. These exercise mock runners, **not** a real
libvirt host.

---

## Checklist — verify against a real libvirt LXC host

Re-open the card and work this list top to bottom on a host with
`libvirtd` + the `lxc` driver. Record the command output next to each item.

- [ ] **1. Driver reachable.** `virsh -c lxc:/// version` prints a version on the
      target host (proves the lxc driver, not just QEMU, is up).
- [ ] **2. Probe true.** With a real LXC container defined, `vm/lxc` shows
      `Detected: true` / status `LXC driver active` (not `UNAVAILABLE`).
- [ ] **3. List parsing.** Define at least one running and one stopped container.
      Confirm the tab lists both with correct `RUNNING` / `STOPPED` states and
      that the `Id` column matches `virsh -c lxc:/// list --all`.
- [ ] **4. Spec parsing.** Confirm parsed `vcpus` and `ramGiB` match
      `<vcpu>` / `<memory unit='KiB'>` in `virsh -c lxc:/// dumpxml <name>`.
      Fix the regexes in `lxc.go` if the LXC XML differs from QEMU XML.
- [ ] **5. Host without lxc driver.** On a QEMU-only libvirt host, confirm the
      tab reports "no libvirt LXC host available" and shows **zero** containers.
- [ ] **6. Host without virsh.** Confirm the tab reports `UNAVAILABLE` (virsh not
      found) and shows **zero** containers.
- [ ] **7. start.** Stage `start` and confirm the container transitions to
      running. Verify `community.general.virt` honours `uri: lxc:///` and whether
      `become: true` is required or harmful.
- [ ] **8. shutdown.** Confirm graceful shutdown succeeds.
- [ ] **9. destroy.** Confirm force-stop succeeds on a running container.
- [ ] **10. suspend.** Confirm the container reports `paused` in `virsh list`.
- [ ] **11. Registry + view.** Confirm `vm/lxc` appears in `GET /api/plugins`,
      `GET /api/nodes/:id/plugins`, and that the LXC tab renders (not the KVM
      view) with the UNTESTED banner present.
- [ ] **12. Flip the flag.** Once 1–11 pass, set `Verified: true` and update the
      `VerifiedNote` in `lxc.go`, and replace this README's status header.
