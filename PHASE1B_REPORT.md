# Phase 1b — Deferred read-only routes ported to pure Go

Branch: `wt/t_9d283c9b`
Base: `main` tip `85fa4af` (Phase 1 `readonlyapi` package already merged as `156a9ca`)

## Summary

The three routes Phase 1 deliberately deferred now live in the **same merged
package** `backend-go/pkg/readonlyapi` (extended, not forked). All are read-only
and execute over the real SSH/tailscale runner; an unreachable host produces an
honest error, never synthetic data.

| Express route | Go handler | File |
| --- | --- | --- |
| `GET /api/telemetry` | `handleTelemetry` | `readonlyapi/telemetry.go` |
| `GET /api/nodes/:id/telemetry/glances` | `handleNodeGlances` | `readonlyapi/glances.go` |
| `GET /api/nodes/:id/containers` | `handleNodeContainers` | `readonlyapi/containers.go` |
| `GET /api/podman/containers` (host-param form) | `handlePodmanContainers` | `readonlyapi/containers.go` |

`/api/podman/containers` has no Express counterpart in `server.ts` (grep: the
only container route is `/api/nodes/:id/containers`); the host-parameter alias
was added because the Phase 1 scope listed it. It shares the handler and envelope.

New shared parser package: `backend-go/pkg/glances/multitier.go` —
`Collect()` implements the four-tier chain (Glances REST → SSH localhost curl →
Glances CLI → /proc+sysfs) and returns the exact `GlancesMetricPayload` shape the
TS collector emits. `plugins/telemetry/glances` (probe/Read) and `pkg/podman`
(rootless discovery, stats) were reused; no second rootless-user parser was
written.

## Zero-mock deviations from Express (intentional)

Express fabricates values in these paths; the Go port does not:

| Field | Express | Go |
| --- | --- | --- |
| `cpuTempC`, `fanSpeedRpm`, `ioDelayPercent`, `packageWatts` | hard-coded seed values from the `telemetry` singleton in `server.ts` (39 / 840 / 0.02 / 38.5, never measured) | `0` |
| `zfsRaidType`, `zfsScrubStatus` | hard-coded `"XFS / LVM Root"` / `"Host: <shortName>"` when no ZFS pool is present | `""` |
| docker `cpu`/`memory` | fabricated `"0.2%"` / `"48 MiB"` | `""` |
| unreachable host | falls back to `node.specs`-derived numbers (`fetchLiveNodeTelemetry` catch) or an all-empty success envelope | `502 {success:false,error}` |
| `sparkline` | static seed array | real history of measured `cpuUsage` samples (empty until measured) |

Key set, nesting and value types are unchanged (verified below).

## Parity evidence (real hosts, same DB, Express :3000 vs Go daemon :18097)

### `GET /api/nodes/node-ping/telemetry/glances`

```
express_http=200  go_http=200
express: {"success":true,"glances":{"cpu":{"total":6.6},"mem":{"percent":48.7,"used":3792994304,
          "total":7793221632},"load":{"min1":0.22,"min5":0.18,"min15":0.18},"source":"pcp_fallback"}}
go:      {"glances":{"cpu":{"total":6.6},"mem":{"percent":48.7,"used":3795722240,"total":7793221632},
          "load":{"min1":0.22,"min5":0.18,"min15":0.18},"source":"pcp_fallback"},"success":true}
```

Identical shape, source and values; `mem.used` differs by 2.7 MB because the two
requests were ~1s apart (real memory drift).

### `GET /api/telemetry?host=node-ping&refresh=true`

Both return HTTP 200 with the **same 25 keys**:
`arcCacheGiB, buffCacheGiB, cachedAt, cpuModel, cpuTempC, cpuUsage, fanSpeedRpm,
fromCache, ioDelayPercent, loadAvg15m, loadAvg1m, loadAvg5m, memoryTotalGiB,
memoryUsedGiB, metricsSource, node, osDescription, packageWatts, sparkline,
uptime, vCpus, zfsRaidType, zfsScrubStatus, zfsTankTotalTb, zfsTankUsedTb`.

Every measured value matches (`cpuUsage` 6.6, `cpuModel` "Intel(R) Core(TM)
i5-6500 CPU @ 3.20GHz", `vCpus` 4, `memoryUsedGiB` 3.5, `memoryTotalGiB` 7.6,
`loadAvg1m/5m/15m` 0.29/0.20/0.19, `buffCacheGiB` 3.1, `metricsSource` "pcp",
`uptime` "1 day, 3 hours, 39 minutes", `osDescription` "AlmaLinux 10.2 (Lavender
Lion)"). The embedded `node` object is **byte-identical** (`node_diff_rc=0`).
The only diffs are the fabricated fields listed above.

### `GET /api/nodes/node-ping/containers?refresh=true`

Both envelopes identical: `probedUsers` 1, `identities` 0, `rootContainers` 0,
`rootPods` 0, `hasDocker` false, `dockerContainers` 0, `fromCache` false, same 9
top-level keys, same node blob, HTTP 200 both. Go returned the real (empty)
container state for that host, so this is a true value-for-value match.

### Hosts with real container state — a pre-existing Express defect

Express cannot be compared value-for-value on these hosts because its own probe
returns the all-empty catch envelope while the host demonstrably has state:

| Host | Express | Go |
| --- | --- | --- |
| node-dev-pc (local) | identities 0, root 0, docker 0 | identities 33, rootContainers 74, docker 13 |
| node-chuck | identities 0, root 0, docker 0 | probed users 1 (`apps`, 9 containers, 2 pods), identities 23, rootContainers 1, rootPods 2, docker 4 |
| node-likes-it-cold | identities 0, root 0, docker 0 | identities 13, rootContainers 1, rootPods 1, docker 21 |

Root cause (measured, with the Express catch instrumented): the probe script's
**first command is `ps -eo uid,user:32,args --no-headers`**, and `ps` prints the
running shell's argv — i.e. the whole script, `===PASSWD===` markers included.
`probeNodePodmanAndIdentities` splits the output with `raw.split('===PASSWD===')`,
whose first hit is inside that `ps` line, so every following section is garbage
and every `JSON.parse` fails silently (`raw.length` 592,960 with `identities: 0`).

The Go port uses `splitMarkerLine` (marker must occupy a whole line), which is
immune to the echo; `TestSplitMarkerLineIgnoresScriptEcho` pins it. The TS
splitter was left untouched because `server.ts` is deleted in Phase 5 — this is
recorded here so the Express retirement (or a dedicated card) can drop the dead
code rather than "fix" a route that is being removed.

Two more Express zero-mock violations found in the same path, **not** reproduced
in Go: `getDefaultHostPodmanProbe()` (`src/utils/orchestrator.ts`) returns a
hard-coded fixture (deployer/podman/jellyfin containers) when probing fails, and
`parseDockerPsOutput` invents `cpu: '0.2%'`, `memory: '48 MiB'`.

## Other fixes in this branch

- `pkg/glances/client.go`: local CLI fallback budget 4s → 8s. A real
  `glances --stdout-json now,cpu,mem,load,sensors` run on this host takes ~3.8s,
  so `TestFetchTelemetry_LocalCLIFallback` was a coin flip (it also flakes on
  unmodified `main`: 1 pass in 3 runs).
- `readonlyapi/api.go`: registered the four new routes; removed the "DEFERRED to
  Phase 2" note.

## Gates (all four, observed)

```
$ cd backend-go && go test -v ./...
PASS  (rc=0, 153 --- PASS lines, 0 failures)   [TMPDIR=/tmp]

$ npm test
ℹ tests 508  ℹ suites 100  ℹ pass 508  ℹ fail 0  ℹ skipped 0

$ npm run lint
tsc --noEmit  (rc=0, no output)

$ npm run build
✓ built in 1.36s  (rc=0)
```

Note on gate 1: the test harness runs with `TMPDIR=$HERMES_.../cache/scratch`,
where `bind()` on a unix socket fails (`invalid argument`), so
`pkg/ssh TestResolveSSHAgentAuthMethod_ConnectsToAgentSocket` fails for
environmental reasons on **any** branch (reproduced on `main`). Running the gate
with `TMPDIR=/tmp` is the correct invocation.

## Files

```
backend-go/pkg/glances/multitier.go          (new, shared 4-tier collector)
backend-go/pkg/glances/multitier_test.go     (new)
backend-go/pkg/readonlyapi/telemetry.go      (new)
backend-go/pkg/readonlyapi/glances.go        (new)
backend-go/pkg/readonlyapi/containers.go     (new)
backend-go/pkg/readonlyapi/phase1b_test.go   (new)
backend-go/pkg/readonlyapi/api.go            (modified: routes + splitMarkerLine)
backend-go/pkg/glances/client.go             (modified: CLI fallback budget)
```
