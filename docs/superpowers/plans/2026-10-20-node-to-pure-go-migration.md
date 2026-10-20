# Migration Plan: Express Coordinator (`server.ts`) → Pure Go + Static Web Frontend

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Branch:** `feat/pure-go-migration`
**Goal:** Retire the Node/Express coordinator entirely. One Go binary serves the API, the WebSocket hub, and the built frontend. Node remains only as a *build-time* tool (Vite bundling), never as a runtime process.

**Status:** Planning. No production route has been cut over yet.

---

## 1. Measured current state

| Component | Location | Size | Notes |
| --- | --- | --- | --- |
| Express coordinator | `server.ts` | 2549 lines | 66 routes, `/ws` hub, Vite middleware in dev, static `dist/` in prod, binds `0.0.0.0:3000` |
| Shared TS logic imported by server | `src/utils/*.ts` | ~7970 lines across 17 modules | Parsing, SSH command builders, Ansible queue, vault, venv bootstrap |
| Go daemon | `backend-go/` | 16 plugin kinds, ~50 plugin packages | Plugin registry + `pkg/plugin/api.go` HTTP layer, ssh pool, websocket hub, podman, glances, restic, tailscale, cockpit bridge. Binds `:18090` (all interfaces) |
| Frontend | `src/` | React 19 + Vite + Tailwind v4 | Talks to `:3000` for API + WS |

### Routes already served by the Go daemon
`/ws`, `/api/health`, `/api/podman/probe`, `/api/nodes`, `/api/tailscale/peers`, `/api/restic/snapshots`, `/api/glances/telemetry` — **7 of 66**.

### Route inventory in `server.ts`, by domain
| Domain | Routes | Mutation? | Ansible queue? |
| --- | --- | --- | --- |
| Nodes CRUD + probe | 5 | yes | no (SSH probe) |
| Telemetry | 2 | no | no |
| Tasks / group-vars | 3 | yes | no |
| Podman (probe/containers/logs/action/identities) | 6 | yes | yes |
| Terminal exec | 1 | yes | no (PTY) |
| Tailscale (peers/ping/derp/node) | 4 | partly | no |
| Interfaces (list/toggle/connect) | 3 | yes | yes |
| VMs (list/action/create) | 3 | yes | yes |
| Firewall (list/add/delete) | 3 | yes | yes |
| Disks / SMART / ZFS scrub | 3 | yes | yes |
| Storage | 1 | no | no |
| Shares (list/reload/nfs/smb) | 4 | yes | yes |
| Users (list/create/password/smbpassword) | 4 | yes | yes |
| Reboot | 1 | yes | yes |
| System logs | 2 | no | no |
| Ansible config / environment / inventory / collections | 5 | yes | n/a |
| Playbook run / cancel | 2 | yes | yes |
| Queue (get/add/delete/clear/execute) | 5 | yes | yes |
| Plugins (list/per-node/modules/detail/action/refresh) | 6 | yes | yes |
| Restic snapshot | 1 | yes | no |
| Static `/ghostty-vt.wasm` | 1 | — | — |
| `/api/*` JSON 404 catch-all | 1 | — | — |
| `/ws` hub | 1 | — | — |

---

## 2. Security comparison: pure Go single binary vs. the current "separated model"

**Short answer: the current separation is not a security boundary, so pure Go is the more secure end state.**

Facts behind that:
- `server.ts` binds `0.0.0.0:3000`; the Go daemon binds `:18090` on all interfaces. Both run as the **same user on the same host**.
- `server.ts` imports `child_process` (`spawn`, `execSync`, `execFile`) and shells out to SSH, Ansible, podman, and system tools directly. It holds the **same host access** as the Go daemon, including `SSH_AUTH_SOCK`.
- Therefore "separation" buys zero privilege isolation. It doubles the code paths, the validation logic, and the auth surface instead.

Where the separated model *does* win — and only if made real:
- Node is a pure API/auth gateway that never touches a host; Go is the privileged executor reachable only over a **localhost unix socket** with file permissions `0600` and an owner-only group.
- That is a genuine least-privilege split. It is also more work than porting, and it leaves two languages and two auth paths to maintain forever. Recommend against for this project.

Security findings to fix regardless of path (both hubs):
- **WebSocket origin check is effectively disabled.** `backend-go/pkg/websocket/hub.go` has `CheckOrigin: func(r *http.Request) bool { return true }`. The Express `ws` hub sets no origin verification. Consequence: any page the operator visits can open `ws://<host>:3000/ws` (or `:18090`) and drive the homelab. Add an origin allowlist plus a per-session bearer token before exposing either port beyond localhost.
- **No TLS and no auth on either listener.** Neither the Node server nor the Go daemon terminates TLS or authenticates HTTP requests. Any LAN peer (or Tailscale peer, since the daemon binds all interfaces) has full API access. Terminate TLS and require auth at the Go layer, or bind both to `127.0.0.1` and front them with a reverse proxy.
- **systemd hardening is good and should be kept:** `User=core`, `ProtectSystem=full`, `NoNewPrivileges=true`, `PrivateTmp=true`, `ProtectHome=read-only`. Revisit `ProtectHome=read-only` only if the daemon must read an ssh-agent socket under `/run/user`.
- **Pure Go build advantages:** `modernc.org/sqlite` (already an indirect dependency) is a pure-Go driver, so the final binary needs no cgo and no libc coupling. Static binary, memory-safe, no `eval`, no npm runtime dependency tree. The npm supply-chain surface (`express`, `ws`, `dotenv`, `vite`, `@google/genai`) disappears from the runtime process.

---

## 3. Dev server workflow change

**Today:** `npm run dev` → `tsx watch server.ts` → Express creates Vite in `middlewareMode` → one process, one port (`:3000`), HMR included. API and UI share the origin.

**After migration:** two processes, two ports.

| Mode | Frontend | API/WS | External port |
| --- | --- | --- | --- |
| Dev | Vite dev server `:5173` | Go daemon `:18090` | `:5173` (Vite proxies `/api` + `/ws`) |
| Prod | static `dist/` embedded in the Go binary | same binary | `:3000` |

Vite config addition:

```ts
server: {
  hmr: process.env.DISABLE_HMR !== 'true',
  watch: process.env.DISABLE_HMR === 'true' ? null : {
    ignored: ['**/data/**', '**/*.sqlite*', '**/backend-go/**'],
  },
  proxy: {
    '/api': { target: 'http://127.0.0.1:18090', changeOrigin: true },
    '/ws':  { target: 'ws://127.0.0.1:18090', ws: true },
  },
}
```

Consequences and gotchas:
- `DISABLE_HMR` and the `watch.ignored` list must be preserved — both exist to stop agent edits from flickering the browser.
- The dev proxy changes the WS `Origin` header to `http://127.0.0.1:18090`. The hub origin allowlist must include the Vite dev origin (`http://localhost:5173`) or dev WS will be rejected once the check is tightened.
- `/ghostty-vt.wasm` is currently served by Express out of `node_modules/ghostty-web/`. In dev, Vite must serve it (add a `publicDir` copy or a small Vite plugin). In prod, the Go binary embeds it. Pick one mechanism and make dev match prod.
- `make dev` becomes two commands. Use a single foreground target that starts both and traps INT/TERM to kill both, or a supervisor such as `process-compose`. Do not leave a stray Go process bound to `:18090` after `Ctrl-C`.
- Production build order is now significant: `npm run build` first (emits `dist/`), then `go build` (embeds `dist/`). `make build` must encode that order.
- `npm run dev` no longer starts the API. Anyone running only `npm run dev` gets a Vite UI with `/api` returning proxy errors. Document this in `README.md` and `AGENTS.md`.

---

## 4. Phased plan

### Phase 0 — Foundation (no behavior change)
- [ ] Add `//go:embed dist` static asset serving plus SPA fallback to the Go daemon.
- [ ] Add the `/api/*` JSON 404 catch-all to the Go mux, mirroring `server.ts`.
- [ ] Add a Go cache/store package for `data/hosts-cache.sqlite` using `modernc.org/sqlite`. Schema, WAL pragmas, and TTL tiers must match `src/utils/hostCacheDb.ts` exactly so both processes can share the file during transition.
- [ ] Add the Vite `server.proxy` block above so dev runs without Express.
- [ ] Add `make dev-backend`, `make dev-frontend`, and a combined `make dev`.
- [ ] Fix the stale `/api/health` version string in `backend-go/main.go` (`"engine": "golang/1.22"` while `go.mod` requires 1.27.1).

### Phase 1 — Port read-only routes
Telemetry, node list, storage, disks, interfaces, firewall list, VMs list, shares list, users list, system logs, podman containers/logs, tailscale peers/derp-map, plugins list/modules/detail.
- These touch no Ansible queue and no mutation path. Lowest risk.
- Reuse existing plugin packages where present: `storage/zfs`, `storage/btrfs`, `storage/lvm`, `disk/smart`, `firewall/firewalld`, `network/interfaces`, `vm/libvirt`, `share/nfs`, `share/smb`, `logs/journald`, `telemetry/glances`, `container/podman`.
- Decide per TS module: pure client-side helper stays in `src/utils/`; host-touching parsing moves to Go. Do not maintain two copies of a parser.

### Phase 2 — Port mutation routes through the Ansible queue
Node CRUD/probe, users create/password/smbpassword, shares nfs/smb save, firewall add/delete, ZFS scrub, SMART test, interface toggle/connect, VM action/create, podman action, reboot.
- These must honor the existing invariant: stage into `ansible_queue_tasks`, execute one-by-one, checkpoint halt on first failure.
- Port `ansibleQueueCompiler`/`ansibleQueueRunner` semantics into Go. Keep `ANSIBLE_STDOUT_CALLBACK=json`.
- Every write must invalidate matching host cache keys.

### Phase 3 — Ansible config, playbooks, queue, vault
`/api/ansible/*`, `/api/playbook/*`, `/api/queue/*`.
- Vault password handling stays out of logs. Mask as `•••••••• (vault: <var>)`.
- The uv/venv bootstrap currently in `ansibleVenvManager.ts` runs as a Go-invoked script or a Go exec sequence.

### Phase 4 — Terminal and PTY
`/api/terminal/exec` and the WS PTY mux.
- Go already has an ssh pool and a websocket hub. Implement PTY-over-WS in Go.
- Embed or copy `ghostty-vt.wasm` for production.

### Phase 5 — Cutover
- [ ] Delete `server.ts`.
- [ ] Remove `express`, `ws`, `dotenv`, `@google/genai` from `package.json` dependencies. Vite stays as a devDependency.
- [ ] Production deploy: one Go binary, systemd unit updated to serve `:3000` and static assets. Node removed from the runtime path.
- [ ] Update `README.md`, `AGENTS.md`, `ARCHITECTURE.md`, and the `air-traffic-control-overview` skill to describe the single-binary architecture.

---

## 5. Acceptance gates (all four, zero errors)
1. `cd backend-go && go test -v ./... && cd ..`
2. `npm test`
3. `npm run lint`
4. `npm run build`

Plus, per phase: the affected routes must return identical JSON shape and status codes to the Express implementation, verified against a real host.
