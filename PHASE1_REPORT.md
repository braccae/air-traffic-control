# Phase 1 — Read-only API routes ported to pure Go

Branch: `feat/phase1-readonly-routes`
Worktree: `.worktrees/phase1-readonly`

## Summary

Ported the read-only Express routes from `server.ts` into the Go daemon as the
new package `backend-go/pkg/readonlyapi`, mounted from `backend-go/main.go`.
Each handler runs the **same shell command** as its Express counterpart, over the
same SSH runner, and returns the **same JSON envelope** (`success`, domain keys,
`fromCache`, `cachedAt`) with the **same status codes**.

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
| `GET /api/nodes/:id/containers` | Depends on `probeNodePodmanAndIdentities` and the client container/identity model. The Go daemon already has `pkg/podman`; wiring it to this exact response shape needs its own task. |

## Verification

All four gates were run from the worktree, after `node_modules` was symlinked
from the main checkout.

### Gate 1 — `cd backend-go && go test ./...`

PASS. One pre-existing environmental failure in `pkg/ssh`
(`TestResolveSSHAgentAuthMethod_ConnectsToAgentSocket`) is caused by the long
`TMPDIR` under the Hermes profile scratch dir exceeding the unix-socket path
limit. Re-run with a short TMPDIR and it passes:

```
mkdir -p /tmp/gotest-short && TMPDIR=/tmp/gotest-short go test ./...
# ok  air-traffic-control/backend/pkg/ssh  1.325s  (and all other packages)
```

### Gate 2 — `npm test`

PASS. 20 tests, 0 failures.

### Gate 3 — `npm run lint`

PASS. Clean.

### Gate 4 — `npm run build`

PASS. Vite production build succeeded (1730 modules, `dist/index-*.js` emitted).

### Real-host route parity

The daemon was run against the real `data/hosts-cache.sqlite` and every ported
route was exercised against real hosts `node-ping` and `node-likes-it-cold`.
All returned **HTTP 200** with the expected domain keys:

```
node-ping:            storage shares interfaces disks vms firewall  -> 200
node-likes-it-cold:   storage shares interfaces disks vms firewall  -> 200
system-logs (ping)    -> 200  {lines:[...], host, filter}
podman/logs (ping)    -> 200  (target=system falls to podman branch, same as Express)
tailscale/derp-map    -> 200  {netcheck: "..."}
nodes/:id/tailscale   -> 200  {success:true, peer:{...}}   (node-ping)
```

Cache semantics confirmed on `GET /api/nodes/node-ping/disks`:

* first call  → `fromCache:false`, live probe
* second call → `fromCache:true`, `cachedAt` unchanged (15 s section TTL)
* `?refresh=true` → `fromCache:false`, live re-probe

### Startup fix found during verification

`main.go` originally passed the raw `--db` flag value to `hostcache.Open`, so a
default (empty) flag disabled the read-only API at startup. Fixed by resolving
the path first via `plugin.ResolveDBPath(dbPath)`, matching how the plugin API
resolves the same file.

## Unit tests added

`backend-go/pkg/readonlyapi/parsers_test.go` pins the ported pure functions:
`splitMarker`, `parseFirewallListAll`, `formatDiskSize`, `formatIfaceBytes`,
`splitFields`, and `lsblk` byte-size JSON decoding via `json.Number`.

## Exact commands run for each gate

```
cd backend-go && go build ./... && go test ./...
cd backend-go && mkdir -p /tmp/gotest-short && TMPDIR=/tmp/gotest-short go test ./...
npm test
npm run lint
npm run build
```
