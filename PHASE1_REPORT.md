# Phase 1 — Read-only API routes ported to pure Go

Branch: `feat/phase1-readonly-routes`
Worktree: `.worktrees/phase1-readonly`
Base: `main` tip `c197ee7` (branch merged `main` on top of the `bca56d2` base; 4 commits ahead of `main`)

## Summary

Ported the read-only Express routes from `server.ts` into the Go daemon as the
new package `backend-go/pkg/readonlyapi`, mounted from `backend-go/main.go`
(before the `/api` 404 catch-all). Each handler runs the **same shell command**
as its Express counterpart, over the same SSH runner, and returns the **same
JSON envelope** (`success`, domain keys, `fromCache`, `cachedAt`) with the
**same status codes** and the same `hostcache` TTL tiers.

## Routes ported (12)

| Express route | Go handler | File |
| --- | --- | --- |
| `GET /api/nodes/:id/storage` | `handleStorage` | `storage.go` |
| `GET /api/nodes/:id/shares` | `handleShares` | `shares.go` |
| `GET /api/nodes/:id/system-logs` | `handleSystemLogs` | `systemlogs.go` |
| `GET /api/system/logs` | `handleSystemLogs` | `systemlogs.go` |
| `GET /api/nodes/:id/interfaces` | `handleInterfaces` | `interfaces.go` |
| `GET /api/nodes/:id/disks` | `handleDisks` | `disks.go` |
| `GET /api/nodes/:id/vms` | `handleVMs` | `vms.go` |
| `GET /api/nodes/:id/firewall` | `handleFirewall` | `firewall.go` |
| `GET /api/nodes/:id/tailscale` | `handleNodeTailscale` | `tailscale.go` |
| `GET /api/tailscale/derp-map` | `handleDerpMap` | `tailscale.go` |
| `GET /api/podman/logs` | `handlePodmanLogs` | `podmanlogs.go` |
| `GET /api/nodes/:id/users` | `handleUsers` | `users.go` |

## Routes deferred to Phase 2

| Express route | Why deferred |
| --- | --- |
| `GET /api/telemetry` | Depends on `fetchLiveNodeTelemetry` (`src/utils/liveCluster.ts`) which merges `TelemetryData` into the client `NodeItem.specs` model and mutates `targetNode.uptime`/`osDescription`. |
| `GET /api/nodes/:id/telemetry/glances` | Depends on `fetchNodeTelemetryMultiTier` (`src/utils/telemetryCollector.ts`) — the glances/PCP/cockpit multi-tier fallback chain. |
| `GET /api/nodes/:id/containers` / `GET /api/podman/containers` | Depends on `probeNodePodmanAndIdentities` and the client container/identity model. `podman/logs` **is** ported; the container list needs its own task. |

**Plugins (scope item 12)** — list / per-node / modules / detail are already
served by the mounted `pkg/plugin` API (`pluginAPI.RegisterRoutes(mux)`), so no
re-port was needed.

## Verification — run #76

All four gates were run from the worktree, after `node_modules` was symlinked
from the main checkout.

### Gate 1 — `cd backend-go && TMPDIR=/tmp/gate1 go build ./... && TMPDIR=/tmp/gate1 go test ./...`

PASS. `BUILD_OK`; every package `ok` (including `pkg/readonlyapi`). The `pkg/ssh`
unix-socket-path failure seen on a long `TMPDIR` disappears with a short TMPDIR
(`/tmp/gate1`), confirming it is environmental, not a regression.

### Gate 2 — `npm test`

PASS. `tests 493 / suites 100 / pass 493 / fail 0 / cancelled 0`.

### Gate 3 — `npm run lint`

PASS. `tsc --noEmit` exit 0.

### Gate 4 — `npm run build`

PASS. Vite production build succeeded, 1731 modules, `dist/index-CFfbacbN.js`.

### Real-host route parity (Express vs Go, same host)

Method: start the Go daemon on `:8099` (`--port 8099 --db <repo>/data/hosts-cache.sqlite`),
start the untouched `server.ts` on `:8098` (`PORT=8098 HOST_CACHE_DB_PATH=... npx tsx server.ts`),
then diff each route's parsed JSON (volatile `cachedAt` excluded) with
`~/.hermes/profiles/cody-the-coder/cache/scratch/parity.py`. Host: `node-ping`.

Result — every route returned **HTTP 200 on both servers** and matched on status
code, key set, nesting, and value type. Residual differences are volatile by nature:

* `fromCache` on the cached sections flips false/true between the two servers
  depending on which one probed first (independent caches, same TTL tiers) — a
  re-run flipped it in both directions, and storage/interfaces/shares reported
  `PARITY OK` once both caches were warm. Cache-timing artifact, not a contract diff.
* `system-logs` / `system/logs` — only the live journal lines differ (time advances
  between the two sequential calls); shape `{success, lines, host, filter}` identical.
* `tailscale/derp-map` — only the `netcheck` stdout timing/portmap lines differ;
  shape `{success, netcheck}` identical.
* `podman/logs` — both fail loudly with `{success:false, lines:[], error, host}`
  because target `system` is not a container; the wrapped error text differs
  (`tailscale ssh …` vs local `ssh …` runner). Both honest, no synthetic data.

`disks`, `firewall`, `vms`, `users`, `tailscale` reported `PARITY OK` outright.

Cache semantics confirmed on `GET /api/nodes/node-ping/disks` (section TTL):
first call `fromCache:false` (live probe), second call `fromCache:true` with
unchanged `cachedAt`.

### Startup fix found during verification

`main.go` originally passed the raw `--db` flag value to `hostcache.Open`, so a
default (empty) flag disabled the read-only API at startup. Fixed by resolving
the path first via `plugin.ResolveDBPath(dbPath)`, matching how the plugin API
resolves the same file.

## Zero-mock

Every observation above was produced by real SSH against the real host `node-ping`
over the mesh. Nothing is stubbed or fabricated; unreachable hosts surface as
honest `{success:false,error}` / 502 responses.

## Unit tests added

`backend-go/pkg/readonlyapi/parsers_test.go` pins the ported pure functions:
`splitMarker`, `parseFirewallListAll`, `formatDiskSize`, `formatIfaceBytes`,
`splitFields`, and `lsblk` byte-size JSON decoding via `json.Number`.

## Exact commands run for each gate

```
cd backend-go && TMPDIR=/tmp/gate1 go build ./... && TMPDIR=/tmp/gate1 go test ./...
npm test
npm run lint
npm run build
```

Parity harness:

```
/tmp/atc-daemon --port 8099 --db <repo>/data/hosts-cache.sqlite
PORT=8098 HOST_CACHE_DB_PATH=<repo>/data/hosts-cache.sqlite npx tsx server.ts
python3 ~/.hermes/profiles/cody-the-coder/cache/scratch/parity.py
```
