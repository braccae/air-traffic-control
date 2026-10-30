# Work State — SSH Onboarding, Vault Secrets & ATC Tailscale

_Last updated: 2026-10-25. This file is a continuity snapshot so the work survives context compression. Delete or archive once the remaining steps are done._

## 1. Objective

1. Make SSH onboarding work end-to-end with **vault-encrypted credentials** (no `/api/ssh-keys`, no plaintext keys in node metadata).
2. Eliminate the Add Node / Tailscale 502s.
3. Cover the flow with **containerized Playwright e2e** against real hosts.
4. Unblock the **internal Tailscale (tsnet) connection** and the **Tailscale auth key field that refuses input** (latest priority).
5. Verify the whole flow against the six real hosts/tags below.

## 2. Hard requirements & constraints

- **Zero-mock policy** (AGENTS.md): real hosts, real errors, no synthetic success/metrics.
- **All DB logic in the Go backend.** TypeScript is frontend-only. The old TS DB modules were deleted; Go owns hosts/vault/rclone/cron/queue/inventory.
- Only the vault-based credential flow remains: `/api/vault/*` + “SSH Vault Secrets”. `/api/ssh-keys` and the `ssh_keys` table are gone.
- Add Node modal: **no tab may require a Tailscale tag**; “Follow Tag” takes a plain tag (`pve`, not `tag:pve`).
- WinRM submission is **disabled**; do not save metadata or fake success.
- Secret naming is manual: not all secrets are SSH keys, but all SSH keys are secrets.
- The shared private key variable must be named **`ssh_key`**.
- Test vault master password: **`password`** (see §6 for the unattended source).
- **e2e runs against the real dev DB** (`data/hosts-cache.sqlite`) so the user can click around afterwards. Make only `e2e-<timestamp>` / `e2e_<timestamp>` records and clean up exactly those.
- Playwright runs in `mcr.microsoft.com/playwright:v1.64.0-noble`; **no browserless/CDP**, no host browsers (host lacks `libatk`).
- New nodes default honestly: `status: 'unmonitored'`, no invented IP/specs/capabilities.

## 3. Target hosts & tags

| Name | Address | User | Notes |
|---|---|---|---|
| `fred` | user-provided | — | real host |
| `shorts` | user-provided | — | real host |
| `openwrt` | **address still needed** | `root` | |
| `192.168.16.235` | as given | — | LAN |
| `192.168.8.249` | as given | — | LAN |
| `192.168.8.3` | as given | — | LAN |
| tag `pve` | **address still needed** | `root` | uses `ssh_key` |
| tag `host` | — | `core` | uses `ssh_key` |

`openwrt` and `pve` addresses are still missing.

## 4. Completed work (already in the tree, uncommitted)

### Go backend
- `backend-go/pkg/secrets`: vault secret resolver with content classification (PEM ⇒ private key), TTL cache, `NotFoundError`, audit logging, `ValidateRef`, unit tests.
- `backend-go/pkg/ssh/{client.go,pool.go}`: vault credentials actually reach SSH; loud no-auth errors; pool keys include a credential fingerprint.
- Node/tag validation: `422` for unknown `vaultSecretId`; `409` for duplicate vault names; `/api/ssh/test` returns real transport errors.
- Removed `/api/ssh-keys`, the `ssh_keys` table, and `ssh_key_id` from Go and TS.
- `/api/tailscale/peers` normalizes the host `tailscale status --json` output to `{success, self, peers[]}`.
- Go owns hosts/vault/rclone/cron/queue/inventory persistence.

### Frontend
- Add Node / Edit Node reworked: named vault secrets, plain tag input, WinRM disabled, honest defaults.
- Deleted dead TS DB modules and their tests (`hostCacheDb`, `vaultManager`, `inventoryManager`, `rcloneManager`, `ansibleQueueRunner`, cron stores/executors); added `src/types/cron.ts`.
- Fixed a real UI crash: node search called `toLowerCase()` on missing `tailscaleIp`/`group`/`osDescription` (`src/utils/navigationViewModel.ts`), with a regression unit test.

### Tests & tooling
- `tests/e2e/vault-ssh-onboarding.spec.ts` (14 tests), `tests/e2e/add-node-modal.spec.ts` (6), `tests/e2e/real-hosts.spec.ts` (7, skipped until `ssh_key` exists).
- `just e2e` recipe: Playwright image, `--userns=keep-id`, rootless `CONTAINER_HOST` detection, argument pass-through; `e2e-results/` git-ignored.
- Full e2e run: **32 passed, 7 skipped** (real hosts). DB left clean (`hermes (local)` only, 0 vault vars, 0 chunks).
- All four gates were green: `go test ./...`, `npm test` (536), `npm run lint`, `npm run build`.
- `npm run dev` delegates to `just dev-split`.

### Environment
- Unattended vault decryption: `data/.vault-password` (content `password`, mode `0600`) + `vault_password_file = ../data/.vault-password` in `ansible/ansible.cfg`.
- Vault master password for tests: `password`.

## 5. Root causes found and fixed (SSH auth)

### 5.1 “attempted methods [none]” — vault credential never reached the tsnet transport

`connection/ssh` resolves `h.Overrides["vaultSecretId"]`; `connection/tailscale` did **not**. Every
node whose route is a `100.x`/`.ts.net` address is pinned to `connection/tailscale`
(`DefaultRunnerResolver`), so a perfectly valid `vaultSecretId` was ignored and the handshake
offered zero methods. Tailscale SSH hosts still worked because `none` auth is legitimate for them.

Fixed in `backend-go/plugins/connection/tailscale/tailscale.go`:
- new `authMethodsForHost()` resolves the vault secret (private key or password), then legacy
  `private_key`/`password` overrides, then the ssh-agent;
- an unparseable/undecryptable reference now fails loudly instead of silently degrading;
- the pooled-client key includes a non-reversible credential fingerprint, so rotating a secret never
  reuses a connection authenticated with the old value;
- `tsnetDialer.ensureRunning()` calls `srv.Up(ctx)` before the first dial (removed the startup
  `tsnet: backend in state NoState` failures during cache warmup);
- handshake errors with zero auth methods now name the missing `vaultSecretId`.

`connection/ssh` also gained the zero-methods hint; `buildHostFacts` now exposes `authMethod` in
overrides for diagnostics.

### 5.2 `vault_secret_id` column was never written

`hostcache.upsertHostTx` omitted the column from INSERT/UPDATE, so a host edited in the UI kept
`vaultSecretId` only inside `node_json` while `GetHostByID` (and anything trusting the column) saw an
empty reference. Fixed:
- writes persist `vault_secret_id` (NULL when empty, record value wins when the blob omits it);
- `backfillVaultSecretIDs()` repairs existing rows from `node_json` on every writable open —
  it reported `backfilled vault_secret_id for 2 host(s)` on the real DB;
- `TestUpsertHostPersistsVaultSecretID`, `TestUpsertHostKeepsRecordVaultSecretIDWhenBlobOmitsIt`.

### 5.3 Verified live (after restart + `ssh_key` assigned to all key-based nodes)

`GET /api/nodes/:id/interfaces` — the real application path (plugin → tsnet → vault credential):

| Node | Transport / auth | Result |
|---|---|---|
| `likes-it-cold` (100.105.71.49) | tsnet + `ssh_key` | **200** real interfaces |
| `ping` (100.73.87.87) | tsnet + `ssh_key` | **200** real interfaces |
| `larry` (100.107.167.19) | tsnet + `ssh_key` | **200** real interfaces |
| `the-freezer` (100.115.75.78) | tsnet + `ssh_key` | **200** real interfaces |
| `fred` (100.100.173.57) | tsnet, keyless `tailscale-ssh` | **200** real interfaces |
| `dev-pc` (100.97.159.101) | tsnet + `ssh_key` | 502 — `attempted methods [none publickey]`: key not authorised for `core`/`root`/`admin`/`andrew`/`ubuntu`/`user`/`ansible`/`pi`/`dietpi` |
| `orangepizero3` (100.65.100.14) | tsnet + `ssh_key` | 502 — same `[none publickey]` rejection |
| `chuck` (100.114.97.98) | tsnet + `ssh_key` | TCP/22 timeout from ATC tsnet *and* the host tailscaled; peer online via DERP only |

`just e2e tests/e2e/real-hosts.spec.ts`: **5 passed, 2 failed, 1 skipped**.
- passed: `fred` inventory node, `likes-it-cold` inventory node, `the-freezer` inventory node, tag-chunk CRUD, ad-hoc credential plumbing
- failed: `dev-pc` inventory node, `shorts` (192.168.8.184, ad-hoc) — both host-side authorisation
- skipped: `openwrt` (hostname does not resolve from the test container)

Full `just e2e`: **42 passed, 2 failed, 1 skipped** — the only failures are the two above.

The shared vault key is:

```
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBa+WBLGQyn4d/0y0Z2YIB/C7FXO0b7lGveWSAA3A7Zj me@andrewscaccetti.com
```

`dev-pc` and `shorts` reject it for every plausible user (`core`, `root`, `admin`, `andrew`, `ubuntu`,
`user`, `ansible`, `pi`, `dietpi`), i.e. it is not in their `authorized_keys` (or those hosts
authenticate somewhere else entirely). `chuck` needs its firewall / `tailscale0` binding fixed before
any credential can be evaluated.

### 5.4 Stale e2e assertion fixed

`tests/e2e/atc-browser.spec.ts` asserted that the sidebar never contains `leo-banjo`. That was only
true while the DB was empty: with real inventory the tailnet group legitimately shows the operator's
own tailnet. Rewritten to be data-driven — every `*.ts.net` label rendered on screen must correspond
to a `tailscaleHost`/`datacenter` of a real node, so a hardcoded placeholder still fails the test.

`tests/e2e/real-hosts.spec.ts` was also restructured: inventory nodes are verified through the plugin
transport (`/api/nodes/:id/interfaces`, the path the app uses, and the only one that can verify
keyless Tailscale SSH), while ad-hoc hosts go through `/api/ssh/test` and may only claim
`vault-key` auth, because that endpoint performs a plain TCP SSH dial.


### 5.5 Vite dev reload churn — fixed

Playwright rewrites `e2e-results/.last-run.json`; Vite watched it and triggered full page reloads
mid-typing. `server.watch.ignored` now includes `**/e2e-results/**` and `**/test-results/**`.

## 6. Live dev stack (as of this snapshot)

- Go daemon: `:18090`, started as
  `cd backend-go && go run . -port 18090 -db ../data/hosts-cache.sqlite`
  (log `/tmp/atc-daemon.log`).
- Vite: `:5173` (`npx vite --host 0.0.0.0`, log `/tmp/atc-vite.log`); reachable at
  `http://127.0.0.1:5173`, `http://192.168.8.211:5173` and `http://100.70.87.47:5173`.
- Vault variables: `ssh_key` exists and decrypts (`data/.vault-password`, 0600). It is an ED25519 key
  commented `me@andrewscaccetti.com`.
- `hosts` table: 9 rows — 8 homelab hosts (`likes-it-cold`, `ping`, `dev-pc`, `fred`,
  `the-freezer`, `chuck`, `larry`, `orangepizero3`) + `hermes (local)`.
- `vault_secret_id` is populated for 7 rows (`ssh_key`); `fred` is keyless `tailscale-ssh`,
  `hermes (local)` is `local-socket`.
- `global_settings` (root DB): 5 `connection/tailscale` rows including a 63-char auth key,
  `hostname="air-traffic-control-tsnet"`, `stateDir="/tmp/tsnet"`, `ephemeral=true`,
  fallback disabled.
- `/api/tailscale/self` → `connected:true`, `air-traffic-control-tsnet`, `100.100.90.54`.
- Podman rootless needs `CONTAINER_HOST=unix:///run/user/1000/podman/podman.sock`; the host's
  global `CONTAINER_HOST` points at a broken rootful socket, which `just e2e` already overrides.


## 7. Next steps (in order)

1. ~~Implement the three fixes (§5.1, §5.2 status surface, §5.3 watch ignore) + tests.~~ **done**
2. ~~Run the gates.~~ **done — `go test ./...`, `npm test` (547), `npm run lint`, `npm run build` all green.**
3. ~~Make ATC's own tsnet transport use vault credentials (§5.1/§5.2).~~ **done — `the-freezer` returns 200 over tsnet.**
4. ~~Assign `ssh_key` to the nodes that reference no secret.~~ **done** for `likes-it-cold`, `ping`,
   `dev-pc`, `larry`, `orangepizero3`, `the-freezer`, `chuck` (`authMethod: vault-sshkey`).
5. ~~Correct the real-host e2e defaults / restructure the spec.~~ **done** (§5.3, §5.4).
6. **Operator action — authorise the shared key** on `dev-pc` (`100.97.159.101` /
   `192.168.16.235`) and `shorts` (`192.168.8.184`): append the public key from §5.3 to
   `~/.ssh/authorized_keys` for the user ATC should use. Nothing on the ATC side can fix this.
7. **`chuck` (100.114.97.98)**: peer is online via the custom DERP but TCP/22 is unreachable from
   both the host `tailscaled` and ATC's tsnet node — investigate the host firewall / `tailscale0`
   binding / sshd listen address.
8. **Provide the `openwrt` and `pve` addresses** so their e2e entries stop skipping, then re-run
   `just e2e`.


### Status since §5 (this round)

- `GlobalSettingsPanel`: secrets are write-only (blank draft, mask hint from server values, re-sync on page change/save) and **every** control is now labelled with `aria-label`, so settings are addressable and accessible.
- `DatacenterTailscalePage`: “ATC Mesh Identity” card (badge, transport, node, mesh name, addresses, backend state, state dir, ephemeral, error, authorize link) + Recheck.
- `GET /api/tailscale/self` (alias `/api/tailscale/status`) backed by `Plugin.SelfStatus`.
- `Plugin.SelfStatus(ctx)` in `backend-go/plugins/connection/tailscale/tailscale.go`; `TestSelfStatusReportsUnconfiguredNode` asserts the honest unconfigured payload.
- `tests/e2e/tailscale-settings.spec.ts` (6 tests, passing): typed input, non-secret prefill, mask-only secret, real status card, self/status alias parity, Recheck.
- Dev DB fork repaired: the daemon now runs `go run . -port 18090 -db ../data/hosts-cache.sqlite` from `backend-go` (`just dev-split`/`dev-backend` pass `-db` explicitly). Root DB serves **9 nodes** (8 homelab + `hermes (local)`) and holds the 5 `connection/tailscale` settings rows.
- `snapshotBeforeReset()` in `backend-go/pkg/hostcache/store.go` + `TestOpenSnapshotsLegacySchemaBeforeReset`: a legacy DB (no `schema_version`) is snapshotted to `<db>.pre-reset-<ts>.sqlite` before the destructive reset instead of being silently dropped.
- Vault SSH over tsnet + `vault_secret_id` persistence fixed (§5.1/§5.2) with unit tests and live verification.


## 8. Key files

| File | Why it matters |
|---|---|
| `src/components/GlobalSettingsPanel.tsx` | Broken secret input (`value=""`), state/save logic |
| `src/components/DatacenterTailscalePage.tsx` | Tailscale settings page; add tsnet status card |
| `src/components/DatacenterSettingsView.tsx` | `SSHVaultPage` (vault CRUD) and page routing |
| `backend-go/plugins/connection/tailscale/tailscale.go` | tsnet settings resolution, `ensureTsnet`, `Dial`, `GetClient` |
| `backend-go/main.go` | `/api/tailscale/peers`; add `/api/tailscale/self` + alias `/api/tailscale/status` |
| `backend-go/pkg/secrets/*` | Vault secret resolver |
| `backend-go/pkg/ssh/{client.go,pool.go}` | SSH credential plumbing |
| `vite.config.ts` | Add `e2e-results/**` to watch ignore |
| `tests/e2e/helpers.ts` | e2e fixtures + cleanup |
| `tests/e2e/vault-ssh-onboarding.spec.ts` | Vault/SSH/node/tag API coverage |
| `tests/e2e/add-node-modal.spec.ts` | Add Node UI coverage |
| `tests/e2e/real-hosts.spec.ts` | Real-host + tag-chunk verification (skipped until `ssh_key`) |
| `playwright.config.ts` | Container workflow, env-configurable URLs |
| `justfile` | `dev-split`, `e2e` recipes |
| `data/hosts-cache.sqlite` | Real dev DB — preserve |
| `ansible/ansible.cfg`, `data/.vault-password` | Unattended vault decryption |

## 9. Gotchas

- `just e2e` must run with the rootless Podman socket (handled by the recipe).
- e2e specs must never write to the real dev DB outside their own `e2e-*` rows.
- Do not persist a fake tsnet auth key into `global_settings` while testing; type-only tests, or restore prior values.
- `data/` and `*.sqlite*` are already ignored by Vite; `e2e-results/` is not yet.
- No commits made: the working tree contains many pre-existing uncommitted changes.
