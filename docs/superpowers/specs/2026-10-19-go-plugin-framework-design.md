# Design: Go Plugin Framework & Connection Layer (Project 1 of 3)

**Date:** 2026-10-19
**Status:** DRAFT — awaiting operator approval

Decisions are tagged:
- **[operator]** — decided by the operator in conversation.
- **[builder's call — chosen]** — a call made while writing this doc; the operator may overturn it.
- **[left to builder]** — free for whoever plans/implements.

---

## 1. Intent

Air Traffic Control will grow Windows and Cisco IOS support plus many more features. Today every capability is hardcoded:

- `src/utils/drivers/*.ts` "drivers" are single `if/else` blocks on action strings (e.g. `StorageDriver` treats SMB as the default branch, NFS as a special case).
- `QueuedAnsibleTask` carries SSH-only fields (`targetSshUser`, `targetSshPort`, `becomeMethod: run0|sudo|doas`), so WinRM / `network_cli` cannot be expressed.
- `backend-go` wires `podman`, `restic`, `ssh` packages directly in `main.go`; there is no registry.
- `CockpitSubSidebar.tsx` hardcodes tabs and picks **one** storage engine, defaulting to `'zfs'` when nothing is detected (a zero-mock violation).

The goal: **everything that has options is a plugin.** Porting a new capability that already has an Ansible collection and/or a Go library should be trivial: one small Go file, an Ansible task template or two, and optionally one React view. **[operator]**

### The larger roadmap **[operator]**

1. **This project:** Go plugin framework + connection layer (embedded SSH + embedded Tailscale), data-driven inner sidebar, Modules tab.
2. Port `server.ts` (≈2,400 lines: routes, SQLite, queue runner, vault, WebSockets, terminal) to Go, route by route, onto this framework.
3. Go serves the built UI (`embed.FS`); Node removed from production (kept only for Vite in dev).

End state: a container with **one Go binary + uv/Python (Ansible venv) + openssh-client**. **[operator]** (openssh-client is required by Ansible's `ssh` connection plugin.)

---

## 2. Goals, non-goals, anti-goals

### Goals
- A compile-time plugin registry in the Go daemon. Plugins self-register in `init()`. **[operator: "write a small Go file and recompile"]**
- The Go daemon owns the registry and all drivers; the TS coordinator calls it over HTTP. **[operator]**
- Connection methods are plugins: SSH (pure Go, `golang.org/x/crypto/ssh` — already a dependency), Tailscale (embedded via `tailscale.com/tsnet`), local exec.
- Ansible subprocesses can reach tailnet hosts through the binary's embedded Tailscale node (proxy bridge, §5.3).
- Existing TS drivers (system / storage+shares / network) are re-expressed as Go plugins and the TS versions deleted.
- Inner sidebar tabs are generated from plugins **detected** on the host. **[operator]**
- A **Modules** tab lists every plugin of every kind compatible with the host, its state, and what it needs. **[operator]**
- A shared plugin UI kit so all plugin tabs look cohesive. **[operator: "all menus should look cohesive"]**

### Non-goals (later projects or later in the roadmap)
- Porting the rest of `server.ts`, the queue runner, SQLite ownership or vault to Go (project 2).
- Serving the UI from Go / removing Node (project 3).
- WinRM and Cisco IOS connection drivers, and Windows/IOS plugins. The framework must *fit* them (§4.2, §5) but does not ship them.
- The backup hook pipeline (before / after / on success / on failure / running). The action model must fit it (§4.5) but it is not built here.
- Runtime-loaded plugins (`.so`, manifests, WASM). **[operator]**

### Anti-goals (would be failure even if it "works")
- A tab appearing for a tool that is not detected on the host. No default engine. **[operator]**
- Any synthetic data (per AGENTS.md zero-mock policy) — an undetectable plugin is simply not shown as active.
- Adding a plugin requiring edits to switch statements or core files beyond a single registration/import line.
- Plugin tabs that look visibly different from each other.
- Go libraries used to *mutate* hosts. Mutations stay Ansible (§3).

---

## 3. The Go / Ansible split **[operator]**

- **Go** is for collecting data (probes, listing resources, telemetry), connection protocols, and core features.
- **Ansible** performs host mutations via templates and wrappers, always through the existing queue with checkpoint halting.

So a typical plugin has two halves: a Go **probe/read** half and an Ansible **action** half. Either half may be absent (an SSH connection plugin has no Ansible half).

---

## 4. Plugin model

### 4.1 Identity
- ID is `<kind>/<name>`, e.g. `storage/zfs`, `share/nfs`, `container/podman`, `connection/tailscale`.

### 4.2 Kinds **[builder's call — chosen]**
Initial kinds (adding a kind should be cheap — a constant plus a sidebar section label):

| Kind | Examples (now / later) |
|------|------------------------|
| `connection` | ssh, tailscale, local / winrm, ios-network-cli |
| `elevation` | run0, sudo, doas, none / windows-runas, ios-enable |
| `package` | dnf, apt, apk, rpm-ostree, bootc / winget, choco |
| `container` | podman / docker, nspawn, lxc, incus |
| `storage` | zfs, btrfs, lvm (+thin), bare |
| `share` | nfs, smb (exports) |
| `mount` | network-mounted drives: nfs/smb/s3/iscsi clients |
| `backup` | restic / borg, rclone, rsync, systemd-timer |
| `logs` | journald, log files, container logs / windows-eventlog, ios-logging **[operator: logs pluggable]** |
| `network` | interfaces, routes |
| `firewall` | firewalld / nftables, windows-firewall |
| `telemetry` | glances tiers, proc/sysfs |
| `disk` | smart / nvme-cli |
| `vm` | libvirt / proxmox, hyper-v |

`elevation` and `telemetry` as kinds follow the operator's "everything with options is pluggable" rule; today's run0→sudo→doas order and the Glances 4-tier hierarchy become ordered plugin chains.

Whether `share` vs `mount` are two kinds or one kind with direction is **[left to builder]**, as long as each export/mount type gets its own tab.

### 4.3 What a plugin declares **[builder's call — chosen]**

Sketch (names illustrative, shape is the point):

```go
type Plugin interface {
    Meta() Meta                    // ID, kind, display label, icon, sidebar order
    Compatible(h HostFacts) bool   // static check: OS family, connection type
    Requirements() Requirements    // host packages + controller collections
    Probe(ctx context.Context, r Runner, h HostFacts) (Detection, error)
}

// Optional interfaces, discovered by type assertion:
type Reader interface { // data shown in the tab
    Read(ctx context.Context, r Runner, h HostFacts) (any, error)
}
type Actor interface {   // mutations via Ansible
    Actions() []ActionSpec // name, label, option schema, template ref
    Compile(h HostFacts, action string, opts map[string]any) (TaskSpec, error)
}
```

- `HostFacts`: node identity, OS family (`linux` / `windows` / `ios`), active connection, active elevation, active package manager.
- `Detection`: `Detected bool`, a short badge (`ONLINE`, `3 pools`), optional detail. Probe errors are reported, never converted into fake success.
- `Requirements`: host packages **keyed by package-manager plugin** (`dnf: [targetcli]`, `apt: [targetcli-fb]`) and controller-side Ansible collections (`community.general`).
- Option schema: a small typed field list (name, type, required, default, enum, description, **`secret` flag**). The secret flag drives masking (`•••••••• (vault: <var>)`) per the AGENTS.md secret invariant. A full JSON Schema is not required.

### 4.4 Ansible templates **[builder's call — chosen]**
- Templates are **static Ansible task YAML embedded next to the plugin** (`embed.FS`), using Ansible's own Jinja `{{ var }}` syntax.
- The plugin validates operator options against its schema and passes them as task **vars**, never by string-splicing into YAML or shell. This avoids injection and keeps templates readable as plain Ansible.
- Native-module priority, Galaxy guard, and shell-as-last-resort rules from AGENTS.md §5 still apply to every template.

### 4.5 Fit for the backup hook pipeline (not built here)
`Compile` returns a `TaskSpec` that is a list of steps, not a single module call, and the queue task format must allow ordered step groups. That is enough for a later backup plugin to emit before / core / on-success (`block`) / on-failure (`rescue`) / after (`always`) groups. No hook UI or storage in this project.

### 4.6 Registration and layout **[builder's call — chosen]**
- `backend-go/pkg/plugin` — registry, interfaces, schema types.
- `backend-go/plugins/<kind>/<name>/` — one package per plugin, with its `templates/*.yml`.
- `backend-go/plugins/all/all.go` — blank-imports every plugin. **This one import line is the only edit outside the plugin's own folder.**
- Frontend: `src/plugins/views/index.ts` maps plugin ID → custom view; one line per custom view.

---

## 5. Connection layer

### 5.1 Runner abstraction
Every probe and reader receives a `Runner` (run command → stdout/stderr/exit code; optionally dial a `net.Conn`). Probes never know whether the link is Tailscale, LAN or local. **[builder's call — chosen]**

### 5.2 Connection plugins shipped in this project
- `connection/ssh`: `golang.org/x/crypto/ssh` with agent forwarding (existing `pkg/ssh` logic moves here), known-hosts accept-new semantics matching today's `StrictHostKeyChecking=accept-new`.
- `connection/tailscale`: embedded `tsnet.Server`; dials `100.x` / MagicDNS hosts and runs the SSH client over that `net.Conn`. Against Tailscale SSH servers the client relies on `none` auth (x/crypto tries it first). **This must be verified with a short spike before building on it.**
- `connection/local`: local exec for the controller host itself.
- The AGENTS.md failover order (tailnet → LAN → loopback) becomes an ordered chain of connection plugins. **[builder's call — chosen]**

tsnet state lives in `data/tsnet/`; auth key from `TS_AUTHKEY` env on first start. The controller's tailnet tag and the ACL SSH rules allowing it are operator-managed outside the app. **[builder's call — chosen]**

Go toolchain must be bumped from 1.22 to whatever current `tsnet` requires.

**Spike first, with a fallback [operator].** The tsnet + `none`-auth spike runs before `connection/tailscale` or the proxy bridge are built. If it fails, the controller's tsnet node falls back to **regular SSH over the tailnet**: the controller holds a real SSH key (vault-protected, under `data/`), and targets authorize it in `authorized_keys`. Embedded Tailscale stays, and so does the one-binary goal.

### 5.2.1 Choosing the active connection / elevation / package plugin **[operator]**
- By default the active plugin is **auto-detected** from the ordered probe chain (connection: tailnet → LAN → loopback; elevation: run0 → sudo → doas → none; package: bootc/rpm-ostree → dnf/apt/apk).
- The operator can **override** each one per host in the Edit Node modal. This is a new saved node field (e.g. `pluginOverrides: { connection?, elevation?, package? }`). TS adds the column and writes it, since TS is the only SQLite writer in project 1; the daemon reads it.
- An override always wins. If the overridden plugin fails, the host reports that error loudly; it does not quietly fall back to auto-detection. **[builder's call — chosen]**
- Detected results are cached (§6), so compiling an action uses cached `HostFacts` and doesn't wait on a fresh probe.

### 5.3 Ansible proxy bridge **[builder's call — chosen]**
tsnet's network exists only inside the Go process, so `ansible-playbook` cannot reach tailnet hosts on its own. The binary provides `air-traffic-control-daemon proxy <host> <port>`; inventories for Tailscale-connected hosts set `ansible_ssh_common_args: -o ProxyCommand="<binary> proxy %h %p"`. The subcommand forwards stdio through the running daemon's tsnet (via a local socket or tsnet's `Loopback()` SOCKS5 — **[left to builder]**). When a system `tailscaled` is present, the bridge is unnecessary and may be skipped.

### 5.4 Task format change
`QueuedAnsibleTask` replaces the SSH-only fields with a generic connection block produced by the connection/elevation plugins (e.g. `ansible_connection`, `ansible_host`, `ansible_port`, `ansible_user`, `ansible_become_method`, extra args). `becomeMethod` is no longer a closed union. Existing queued rows must still load (migration or compat read) — **[left to builder]**.

### 5.5 Credentials
Project 1 supports: SSH agent, key files, Tailscale SSH (no credential). Vault-encrypted credentials are decrypted by invoking `ansible-vault` from `data/ansible-venv`, using the existing tiered password resolution. **[builder's call — chosen]**

---

## 6. Daemon API & data ownership (interim, until project 2)

**[builder's call — chosen]** During project 1 the TS server still owns SQLite, the queue runner and the HTTP surface the browser talks to.

- The Go daemon **reads** node records from `data/hosts-cache.sqlite` (WAL permits concurrent readers); TS remains the only writer.
- The Go daemon caches probe results in memory using AGENTS.md TTLs (containers 20s, network 30s, firewall 45s, storage/shares 60s; other kinds default 60s).
- TS proxies the browser to these daemon endpoints:

| Endpoint | Purpose |
|---|---|
| `GET /api/plugins` | all registered plugins + metadata, schemas, requirements |
| `GET /api/nodes/:id/plugins` | detected plugins for the sidebar (label, icon, section, badge) |
| `GET /api/nodes/:id/modules` | every plugin with state: active / available / unsupported (+reason) |
| `GET /api/nodes/:id/plugins/:kind/:name` | the plugin's `Read` data for its tab |
| `POST /api/nodes/:id/plugins/:kind/:name/actions/:action` | returns a compiled `QueuedAnsibleTask`; TS stages it in the existing queue |
| `POST /api/nodes/:id/plugins/refresh` | invalidate + re-probe |

Mutation endpoints must invalidate the relevant probe cache (AGENTS.md §4).

### 6.1 Interim rules **[builder's call — chosen]**
- **Controller-only daemon.** The daemon runs only on the controller; targets stay agentless (AGENTS.md §3). All probes go through a `Runner`. The existing `?host=` daemon endpoints (`/api/podman/probe` etc.) are retired once their plugin exists.
- **Go `Read` replaces TS probes.** A ported view's data comes from `GET /api/nodes/:id/plugins/:kind/:name`. The Go reader returns the same data shape the TS endpoint returns today, so the view port is mostly a URL change. The superseded TS probe code and its `host_section_cache` entries are deleted in this project, so there is never a second probe path or cache.
- **No TS fallback when the daemon is down.** TS returns a loud error (`{ success: false, error: "air-traffic-control-daemon unreachable at 127.0.0.1:18090" }`), and the sidebar shows an offline state. The existing direct-SSH fallbacks in `server.ts` are removed for anything a plugin covers. The dev setup (`Makefile` / `npm run dev`) starts the daemon alongside Vite.
- **How the connection block reaches Ansible.** The queue runner writes a temporary per-task inventory from the task's connection block, including the `ProxyCommand` for bridge-routed hosts. The dynamic inventory script `inventory/tailscale_inventory.py` also emits the bridge `ansible_ssh_common_args` for Tailscale hosts when no system `tailscaled` is present, so ad-hoc and non-queue playbook runs reach tailnet hosts too.

---

## 7. Frontend

### 7.1 Inner sidebar **[operator]**
- Overview and Terminal stay hardcoded.
- All other tabs come from `GET /api/nodes/:id/plugins`, grouped into sections by kind. A host with SMB, NFS, iSCSI, LVM, Btrfs, ZFS and network mounts shows all of them as separate tabs.
- Nothing detected → no tab.
- **Modules** tab pinned at the bottom.

### 7.2 Modules tab **[operator: covers every plugin kind]**
- Lists every plugin compatible with the host's OS family, grouped by kind, with state **Active** (detected, has a tab), **Available** (compatible, requirements missing), **Unsupported** (greyed, with reason). **[states: builder's call — chosen]**
- Shows host packages (resolved for the host's active package manager) and controller collections required.
- **Install** stages an Ansible package-install task via the active `package` plugin; missing collections go through the existing Galaxy install guard. After a successful run + re-probe, the plugin's tab appears. **[builder's call — chosen]**
- Connection plugins show whether they currently work against this host.

### 7.3 Plugin UI kit and views **[operator: templates first, custom views where options differ, cohesive]**
- `src/plugins/kit/`: resource table, schema-driven option form (honours `secret`), action buttons that stage to the queue, status badge, empty/error/offline states — styled from the current views' Tailwind palette.
- A plugin with no registered custom view gets a **generic view** built entirely from the kit (Read data as table + one form per action).
- Custom views are registered by plugin ID and must compose kit parts; bespoke styling is discouraged.
- Existing views are ported as the first custom views: `ZfsManagementView` → `storage/zfs`, `NetworkSharesView` → split into `share/nfs` and `share/smb`, `SystemLogsView` → `logs/journald`, `PodmanManager` → `container/podman`. Port, don't rewrite.

---

## 8. Initial plugin set for this project **[builder's call — chosen; operator may trim]**

- `connection/ssh`, `connection/tailscale`, `connection/local`
- `elevation/run0`, `elevation/sudo`, `elevation/doas`, `elevation/none`
- `package/dnf`, `package/apt`, `package/apk`, `package/rpm-ostree`, `package/bootc` (needed by Modules installs)
- `storage/zfs`, `storage/btrfs`, `storage/lvm`
- `share/nfs`, `share/smb`
- `logs/journald`
- `container/podman` (wraps existing `pkg/podman`)
- `backup/restic` (wraps existing `pkg/restic`, read-only probe; no hooks)
- `system/*` and `network/*` equivalents of today's TS `SystemDriver` / `NetworkDriver` actions

### 8.1 Every existing tab becomes a plugin **[operator]**
No tab from today's sidebar may disappear. All 13 current tabs ship as plugins in this project. Overview stays hardcoded.

| Current tab | Plugin | View ported from |
|---|---|---|
| logs | `logs/journald` | `SystemLogsView` |
| metrics | `telemetry/glances` (the 4-tier chain; proc/sysfs is the last tier) | `SystemHealthTelemetry` / metrics view |
| updates | updates view of the active `package/*` plugin | `PackageUpdatesView` |
| zfs | `storage/zfs` | `ZfsManagementView` |
| shares | `share/nfs`, `share/smb` (two tabs) | `NetworkSharesView` |
| smart | `disk/smart` (new kind `disk`) | `DisksSmartView` |
| interfaces | `network/interfaces` | `NetworkInterfacesView` |
| firewall | `firewall/firewalld` | `FirewallView` |
| tailscale | `network/tailscale` (the host's own tailnet status) | `TailscaleMeshCard` / `TailscaleExplorerView` |
| pods | `container/podman` | `PodmanManager` |
| vms | `vm/libvirt` (new kind `vm`) | `VirtualMachinesKVM` |
| restic | `backup/restic` | `ResticBackupPolicy` |

How the updates tab hangs off the package plugin (its own tab per package manager vs one shared "Updates" tab) is **[left to builder]**.

---

## 9. Success criteria
1. Adding a new plugin (e.g. `storage/lvm-thin`) requires: one plugin folder (Go + templates), one import line, and optionally one view file + one registry line. Nothing else.
2. A host without ZFS shows no ZFS tab; a host with ZFS, Btrfs and NFS shows three tabs.
3. The Modules tab on any host lists every compatible plugin with an accurate state and requirements.
4. With no system `tailscaled` on the controller, both a Go probe and an `ansible-playbook` run reach a tailnet host.
5. `src/utils/drivers/*.ts` compilation logic is deleted; all mutation compilation comes from the daemon.
6. All four AGENTS.md verification commands pass. Go plugin tests use fake `Runner`s (test doubles are fine in tests; zero-mock applies to production paths).
7. AGENTS.md §6 (driver architecture) is updated to describe the plugin framework.
8. Every tab in today's sidebar is still reachable on a host where its tool is detected (§8.1).

---

## 10. Open items left to the builder
- Exact Go interface/method names and package boundaries.
- `share` vs `mount` as separate kinds.
- Proxy bridge transport (local socket vs tsnet `Loopback()` SOCKS5).
- Task-row migration strategy for the new connection block.
- Sidebar ordering within a section.
