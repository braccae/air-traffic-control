# Ansible Mutation Queue & Telemetry Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform the Air Traffic Control platform into an Ansible-driven mutation architecture where all system changes are staged into an on-the-fly playbook queue, executed step-by-step with structured JSON output, paired with a Datacenter `ansible.cfg` manager, Console accordion drill-downs, and a conservative 30–60s multi-tier Glances telemetry engine.

**Architecture:** 
1. **On-the-Fly Playbook Queue:** Mutations (services, shares, firewall, containers, packages) are compiled into atomic `QueuedAnsibleTask` items with native modules (`ansible.builtin.*`), connection routing injection (`ansible_host`, `ansible_user`, `ansible_port`), and masked Vault secrets.
2. **Step-by-Step Execution Runner:** Tasks execute one-by-one with `ANSIBLE_STDOUT_CALLBACK=json`, parsing structured JSON results and halting at failure checkpoints to prevent cascading state corruption.
3. **Console Task Queue (Option A):** Staged tasks are presented in an accordion drawer tab with live drill-down into parameters, secrets, and module provenance.
4. **Decoupled Drivers:** Pluggable task categories (`system`, `container`, `storage`, `network`, `package`) prepared for future database (`mongodb`, `postgresql`, `mysql`) and Kubernetes (`k8s`/`k3s`) modules, with non-systemd and Cisco switch fallbacks.
5. **Conservative Telemetry Engine:** Polling relaxed to 30–60 seconds, querying Glances HTTP REST (`:61208`) -> Glances over SSH localhost curl -> CLI `glances --stdout-json now,cpu,mem,load` -> PCP/sysfs.

**Tech Stack:** Express, Node.js, React 19, TypeScript, Ansible CLI (`ansible-playbook`, `ansible-config`, `ansible-galaxy`), libghostty WASM, SQLite, Go.

**Spec:** [`docs/superpowers/specs/2026-10-19-ansible-mutation-queue-design.md`](file:///home/pants/Projects/go_projects/air-traffic-control/docs/superpowers/specs/2026-10-19-ansible-mutation-queue-design.md)

---

## Global Constraints

- **Zero-Mock Policy:** All playbook runs, queue executions, telemetry queries, and tests must execute against real local files or live host daemons. No fake timers or synthetic container lists.
- **Strict JSON Stdout Callback:** All Ansible queue runs enforce `ANSIBLE_STDOUT_CALLBACK=json` for machine-parseable UI streaming.
- **Native Module Preference:** Core modules (`ansible.builtin.*`, `ansible.posix.*`) preferred. Prompt at staging time before installing Galaxy collections. Relegate `command`/`shell` strictly to last resorts.
- **Secret Protection Invariant:** Passwords and Ansible Vault tokens are masked (`••••••••`) in UI models, never output in plaintext in task descriptions, and securely passed via extra-vars or vault encryption.
- **4-Gate Verification:** Every task must pass:
  1. `cd backend-go && go test -v ./... && cd ..`
  2. `npm test`
  3. `npm run lint` (`tsc --noEmit`)
  4. `npm run build` (`vite build`)

---

## Review Focus

1. **Staged Queue Crash Recovery:** If the Express backend restarts while tasks are staged in the queue, tasks persisted in SQLite must reload without corrupted state or ghost executions.
2. **Step-by-Step Failure Checkpointing:** When task #2 in a 4-task queue fails, execution must halt immediately without running tasks #3 and #4, keeping them staged and reporting the exact JSON error from Ansible.
3. **Secret Masking Leak Prevention:** Parameters with sensitive keys (`password`, `token`, `secret`, `vault_`) must never be serialized in cleartext into the Console DOM or broadcast unmasked over WebSockets.
4. **Dynamic Host Routing Invariance:** Injected `ansible_host`, `ansible_user`, and `ansible_port` must accurately reflect the node's active failover route (Tailscale vs LAN IP) regardless of static inventory entries.
5. **Glances Probe Degradation:** If Glances REST API is blocked by a firewall, the poller must seamlessly try SSH localhost curl, then CLI `--stdout-json`, without throwing unhandled exceptions or timing out the UI.

---

### Task 1: `ansible.cfg` Settings Manager & Datacenter Configuration View

**Files:**
- Create: `src/utils/ansibleConfigManager.ts`
- Create: `src/utils/ansibleConfigManager.test.ts`
- Modify: `server.ts`
- Modify: `src/components/DatacenterGlobalView.tsx`

**Interfaces:**
- Produces:
  ```ts
  export interface AnsibleConfigSettings {
    stdoutCallback: 'json' | 'yaml' | 'default';
    forks: number;
    timeout: number;
    hostKeyChecking: boolean;
    pipelining: boolean;
    becomeMethod: 'run0' | 'sudo' | 'doas';
    vaultPasswordFile?: string;
  }
  export function parseAnsibleCfg(iniContent: string): AnsibleConfigSettings;
  export function serializeAnsibleCfg(settings: AnsibleConfigSettings, baseContent?: string): string;
  export function loadAnsibleConfig(cfgPath?: string): Promise<AnsibleConfigSettings>;
  export function saveAnsibleConfig(settings: AnsibleConfigSettings, cfgPath?: string): Promise<boolean>;
  ```

- [ ] **Step 1: Write failing unit test for `ansibleConfigManager.ts`**

```ts
// src/utils/ansibleConfigManager.test.ts
import { describe, it, expect } from 'vitest';
import { parseAnsibleCfg, serializeAnsibleCfg, type AnsibleConfigSettings } from './ansibleConfigManager.ts';

describe('ansibleConfigManager', () => {
  const sampleIni = `
[defaults]
stdout_callback = json
forks = 15
timeout = 45
host_key_checking = False
pipelining = True

[privilege_escalation]
become_method = run0
`;

  it('parses INI content into typed AnsibleConfigSettings', () => {
    const settings = parseAnsibleCfg(sampleIni);
    expect(settings.stdoutCallback).toBe('json');
    expect(settings.forks).toBe(15);
    expect(settings.timeout).toBe(45);
    expect(settings.hostKeyChecking).toBe(false);
    expect(settings.pipelining).toBe(true);
    expect(settings.becomeMethod).toBe('run0');
  });

  it('serializes updated settings back into compliant ansible.cfg INI format', () => {
    const updated: AnsibleConfigSettings = {
      stdoutCallback: 'json',
      forks: 20,
      timeout: 60,
      hostKeyChecking: false,
      pipelining: true,
      becomeMethod: 'sudo',
    };
    const out = serializeAnsibleCfg(updated, sampleIni);
    expect(out).toContain('stdout_callback = json');
    expect(out).toContain('forks = 20');
    expect(out).toContain('become_method = sudo');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/utils/ansibleConfigManager.test.ts`  
Expected: FAIL with module not found.

- [ ] **Step 3: Implement `ansibleConfigManager.ts`**

Implement `parseAnsibleCfg` and `serializeAnsibleCfg` using safe INI key-value parsing, with defaults: `stdoutCallback: 'json'`, `forks: 10`, `timeout: 30`, `hostKeyChecking: false`, `pipelining: true`, `becomeMethod: 'run0'`.

- [ ] **Step 4: Mount `GET /api/ansible/config` and `PUT /api/ansible/config` in `server.ts`**

Add endpoints to read and safely write `ansible/ansible.cfg`, invalidating cache and returning updated settings.

- [ ] **Step 5: Add Ansible Engine Configuration Card in `DatacenterGlobalView.tsx`**

Add a dedicated settings card in Datacenter settings displaying dropdowns for `stdout_callback`, `become_method`, steppers for `forks`, and toggles for `pipelining` and `host_key_checking`, with a live "Save & Sync `ansible.cfg`" action.

- [ ] **Step 6: Run verification gates and commit**

Run: `npm test && npm run lint && npm run build`  
Commit: `feat(ansible): add ansible.cfg settings manager and Datacenter UI card`

---

### Task 2: Ansible Mutation Task Models & On-the-Fly Playbook Compiler

**Files:**
- Create: `src/utils/ansibleQueueCompiler.ts`
- Create: `src/utils/ansibleQueueCompiler.test.ts`
- Modify: `src/types/index.ts`

**Interfaces:**
- Produces:
  ```ts
  export interface QueuedAnsibleTask {
    id: string;
    title: string;
    targetNodeId: string;
    targetNodeName: string;
    targetHostRoute: string;
    targetSshUser: string;
    targetSshPort: number;
    category: AnsibleModuleCategory;
    primaryModule: string;
    steps: AnsibleTaskStep[];
    secretsMasked?: Record<string, string>;
    become: boolean;
    becomeMethod: 'run0' | 'sudo' | 'doas';
    status: 'staged' | 'running' | 'completed' | 'failed' | 'cancelled';
    createdAt: number;
  }
  export function compileServiceTask(node: NodeItem, serviceName: string, action: 'restarted' | 'started' | 'stopped' | 'reloaded'): QueuedAnsibleTask;
  export function compileFirewallTask(node: NodeItem, port: string, proto: string, zone?: string): QueuedAnsibleTask;
  export function compileShareTask(node: NodeItem, shareName: string, path: string, users: string[], passwordVaultKey?: string): QueuedAnsibleTask;
  export function compileOnTheFlyPlaybook(task: QueuedAnsibleTask): string;
  ```

- [ ] **Step 1: Write failing unit tests for `ansibleQueueCompiler.ts`**

```ts
// src/utils/ansibleQueueCompiler.test.ts
import { describe, it, expect } from 'vitest';
import { compileServiceTask, compileOnTheFlyPlaybook, compileShareTask } from './ansibleQueueCompiler.ts';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-1',
  name: 'likes-it-cold',
  shortName: 'likes-it-cold',
  ip: '192.168.16.50',
  tailscaleIp: '100.115.92.19',
  tailscaleHost: 'likes-it-cold.ts.net',
  sshUser: 'core',
  port: 22,
  status: 'online',
  group: 'tag:host',
};

describe('ansibleQueueCompiler', () => {
  it('compiles service mutation using native ansible.builtin.systemd with run0', () => {
    const task = compileServiceTask(mockNode, 'caddy', 'restarted');
    expect(task.primaryModule).toBe('ansible.builtin.systemd');
    expect(task.becomeMethod).toBe('run0');
    expect(task.steps[0].module).toBe('ansible.builtin.systemd');
    expect(task.steps[0].parameters.name).toBe('caddy');
    expect(task.steps[0].parameters.state).toBe('restarted');

    const yaml = compileOnTheFlyPlaybook(task);
    expect(yaml).toContain('ansible.builtin.systemd:');
    expect(yaml).toContain('ansible_host: "likes-it-cold.ts.net"');
    expect(yaml).toContain('ansible_user: "core"');
    expect(yaml).toContain('become_method: run0');
  });

  it('compiles share mutation with masked secrets in task metadata', () => {
    const task = compileShareTask(mockNode, 'media_vault', '/mnt/media', ['core', 'media'], 'vault_smb_media');
    expect(task.primaryModule).toBe('ansible.builtin.template');
    expect(task.secretsMasked?.smb_password).toBe('•••••••• (vault: vault_smb_media)');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/utils/ansibleQueueCompiler.test.ts`  
Expected: FAIL with missing module.

- [ ] **Step 3: Implement `ansibleQueueCompiler.ts`**

Implement task generation functions and YAML serializer. Enforce connection parameter injection (`ansible_host`, `ansible_user`, `ansible_port`) and `run0` elevation preference.

- [ ] **Step 4: Run tests and commit**

Run: `npm test && npm run lint && npm run build`  
Commit: `feat(queue): implement on-the-fly playbook compiler with connection injection and secret masking`

---

### Task 3: In-Memory / SQLite Queue State & Backend Step-by-Step Execution Runner

**Files:**
- Create: `src/utils/ansibleQueueRunner.ts`
- Create: `src/utils/ansibleQueueRunner.test.ts`
- Modify: `server.ts`

**Interfaces:**
- Produces:
  ```ts
  export interface QueueExecutionResult {
    success: boolean;
    taskId: string;
    changed: boolean;
    failed: boolean;
    jsonOutput: any;
    error?: string;
  }
  export function executeQueueTaskOneByOne(task: QueuedAnsibleTask, inventoryPath: string): Promise<QueueExecutionResult>;
  ```

- [ ] **Step 1: Write failing unit test for `ansibleQueueRunner.ts`**

Test task state transitions (`staged` -> `running` -> `completed` / `failed`), JSON result extraction from `stdout_callback=json`, and checkpoint halting when a task fails.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/utils/ansibleQueueRunner.test.ts`  
Expected: FAIL.

- [ ] **Step 3: Implement `ansibleQueueRunner.ts`**

Write on-the-fly playbook to `ansible/playbooks/.queue-<id>.yml`, execute `ansible-playbook` with `ANSIBLE_STDOUT_CALLBACK=json`, parse stdout JSON (`res.plays[0].tasks`), clean up ephemeral playbook, and return typed `QueueExecutionResult`.

- [ ] **Step 4: Mount Queue API routes in `server.ts`**

- `GET /api/queue`: Returns current queue state (`{ tasks: QueuedAnsibleTask[], isRunning: boolean }`).
- `POST /api/queue/task`: Staged an incoming `QueuedAnsibleTask`, persisting in SQLite and broadcasting over WebSocket.
- `DELETE /api/queue/task/:id`: Deletes a staged task.
- `DELETE /api/queue`: Clears all staged tasks.
- `POST /api/queue/execute`: Runs next task or single task by ID, returning structured result and stopping on error.

- [ ] **Step 5: Run tests and commit**

Run: `npm test && npm run lint && npm run build`  
Commit: `feat(queue): implement step-by-step queue execution runner with checkpoint halting`

---

### Task 4: Pluggable Driver Architecture & Staging Contract across UI Modals

**Files:**
- Create: `src/utils/drivers/index.ts`
- Create: `src/utils/drivers/systemDriver.ts`
- Create: `src/utils/drivers/storageDriver.ts`
- Create: `src/utils/drivers/networkDriver.ts`
- Modify: `src/components/modals/RebootConfirmModal.tsx`
- Modify: `src/components/modals/ShareEditorModal.tsx`
- Modify: `src/components/modals/ConnectNetworkModal.tsx`
- Modify: `src/components/PackageUpdatesView.tsx`

**Interfaces:**
- Produces:
  ```ts
  export interface MutationDriver {
    category: AnsibleModuleCategory;
    supports(node: NodeItem): boolean;
    compileMutation(node: NodeItem, action: string, payload: any): QueuedAnsibleTask;
  }
  ```

- [ ] **Step 1: Write failing unit tests for pluggable drivers**

Test driver registration, module selection (`ansible.builtin.systemd` vs `ansible.builtin.service` for non-systemd), and staging payload formatting.

- [ ] **Step 2: Run tests to verify failure**

Run: `npx vitest run src/utils/drivers/systemDriver.test.ts`  
Expected: FAIL.

- [ ] **Step 3: Implement pluggable drivers and Galaxy collection guard**

Implement `SystemDriver`, `StorageDriver`, and `NetworkDriver`. Add `POST /api/ansible/collections/install` in `server.ts` to allow staging-time collection installations when needed.

- [ ] **Step 4: Update UI Modals to stage tasks into the Queue**

Update action buttons in `ShareEditorModal`, `RebootConfirmModal`, `ConnectNetworkModal`, and `PackageUpdatesView` to dispatch to `/api/queue/task` and trigger a toast `"Staged in Task Queue (1 pending)"` linking to the Console.

- [ ] **Step 5: Run verification gates and commit**

Run: `npm test && npm run lint && npm run build`  
Commit: `feat(drivers): add pluggable mutation drivers and stage actions from UI modals`

---

### Task 5: Console "Task Queue" Tab & Accordion Drill-Down (Option A)

**Files:**
- Create: `src/components/TaskQueueTab.tsx`
- Create: `src/components/TaskQueueTab.test.ts`
- Modify: `src/components/GlobalTasksExecutionLog.tsx`
- Modify: `src/utils/navigationViewModel.ts`

**Interfaces:**
- Consumes: `QueuedAnsibleTask`, `PlaybookQueueState` from Task 2 & Task 3.
- Produces:
  ```tsx
  export interface TaskQueueTabProps {
    queue: QueuedAnsibleTask[];
    isRunning: boolean;
    onRunNextTask: () => void;
    onExecuteAll: () => void;
    onDeleteTask: (id: string) => void;
    onClearQueue: () => void;
  }
  export const TaskQueueTab: React.FC<TaskQueueTabProps>;
  ```

- [ ] **Step 1: Write failing component tests for `TaskQueueTab.test.tsx`**

Test rendering of staged tasks, accordion toggle, module pill (`ansible.builtin.*`), masked secrets (`••••••••`), and single-step run button.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/components/TaskQueueTab.test.ts`  
Expected: FAIL.

- [ ] **Step 3: Implement `TaskQueueTab.tsx`**

Build the Option A interactive inline accordion with:
- Target node badge, intent title, module badge (`ansible.builtin.*`).
- Expanded card: module parameters table, privilege escalation badge (`run0`), Vault secret badge (`••••••••`).
- Toolbar: `[▶ Run Next Task]`, `[▶ Execute Queue (One-by-One)]`, `[Clear Queue]`.
- Live checkpoint failure banners showing structured Ansible error messages.

- [ ] **Step 4: Integrate into `GlobalTasksExecutionLog.tsx`**

Add `Task Queue (${stagedCount})` tab to the pinned left section of the Console drawer header.

- [ ] **Step 5: Run verification gates and commit**

Run: `npm test && npm run lint && npm run build`  
Commit: `feat(console): add Task Queue tab with Option A inline accordion drill-down`

---

### Task 6: Conservative 30–60s Telemetry Poller & Glances Multi-Tier API Engine

**Files:**
- Create: `src/utils/telemetryCollector.ts`
- Create: `src/utils/telemetryCollector.test.ts`
- Modify: `server.ts`
- Modify: `src/hooks/useHostPolling.ts`

**Interfaces:**
- Produces:
  ```ts
  export interface GlancesMetricPayload {
    cpu: { total: number };
    mem: { percent: number; used: number; total: number };
    load: { min1: number; min5: number; min15: number };
    source: 'http_rest' | 'ssh_localhost_curl' | 'cli_stdout_json' | 'pcp_fallback';
  }
  export function fetchNodeTelemetryMultiTier(node: NodeItem): Promise<GlancesMetricPayload>;
  ```

- [ ] **Step 1: Write failing unit test for `telemetryCollector.ts`**

Test probe sequence: (1) direct HTTP REST, (2) SSH localhost curl, (3) CLI `glances --stdout-json`, (4) graceful PCP/sysfs degradation.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/utils/telemetryCollector.test.ts`  
Expected: FAIL.

- [ ] **Step 3: Implement `telemetryCollector.ts` & update `server.ts`**

Implement multi-tier probe logic with timeout limits. In `server.ts` and `useHostPolling.ts`, adjust default polling cadence to a conservative 45 seconds (within 30–60s range).

- [ ] **Step 4: Run tests and commit**

Run: `npm test && npm run lint && npm run build`  
Commit: `feat(telemetry): implement conservative 30-60s multi-tier Glances telemetry collector`

---

### Task 7: Full System Integration & 4-Gate Verification

**Files:**
- Modify: `src/App.tsx`
- Modify: `AGENTS.md`
- Modify: `ARCHITECTURE.md`

- [ ] **Step 1: Wire queue state and WebSocket updates into `App.tsx`**

Ensure that changes triggered across all views update the Console `Task Queue` badge reactively and execute cleanly one-by-one.

- [ ] **Step 2: Update architecture and agent guidelines**

Document the On-the-Fly Playbook Queue, native module prioritization, one-by-one checkpoint execution, and conservative Glances polling in `ARCHITECTURE.md` and `AGENTS.md`.

- [ ] **Step 3: Run the 4 verification gates**

```bash
# 1. Run all Go tests
cd backend-go && go test -v ./... && cd ..

# 2. Run all frontend Vitest unit tests
npm test

# 3. Verify TypeScript compilation
npm run lint

# 4. Verify Vite production build
npm run build
```
Ensure 100% pass rate with zero errors.

- [ ] **Step 4: Commit**

Commit: `chore(arch): finalize Ansible mutation queue and conservative telemetry integration`
