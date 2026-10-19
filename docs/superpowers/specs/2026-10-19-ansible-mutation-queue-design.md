# Technical Design Specification: On-the-Fly Ansible Playbook Mutation Queue & Telemetry Engine

**Document Path:** `docs/superpowers/specs/2026-10-19-ansible-mutation-queue-design.md`  
**Date:** 2026-10-19  
**Status:** DRAFT (Under Review)  
**Author:** AI Pair Programmer & System Architect  

---

## 1. Intent & Context (The "Why")

In Air Traffic Control (`air-traffic-control`), managing 47+ physical and virtual machines across Tailscale and local network meshes requires maximum predictability, structured auditability, and consistent execution tracking. 

Previously, certain operations executed direct, ad-hoc CLI commands over non-interactive SSH (e.g. `systemctl restart`, `podman restart`, `iptables`). While effective, direct CLI commands lack uniform schema output, structured rollback semantics, and seamless integration with the homelab's Ansible inventory, secrets, and vault infrastructure.

This project refactors the mutation and telemetry architecture:
1. **Every change is executed via an Ansible playbook created on the fly known as the "Queue".**
2. **Execution runs one task at a time with structured JSON output**, halting at checkpoints if a failure occurs to prevent cascading errors.
3. **Ansible configuration (`ansible.cfg`) settings are directly modifiable from the Datacenter settings UI.**
4. **Tasks prioritize native Ansible modules** (`ansible.builtin.*`, `ansible.posix.*`), prompt before installing community collections from Ansible Galaxy, and relegate `command`/`shell` strictly to documented last resorts.
5. **Data collection is relaxed to a conservative 30–60 second interval**, sourcing metrics directly from REST/WS APIs, localhost APIs accessed via SSH port-forwarding/curl, or Ansible actions.
6. **Glances metrics prioritize the Glances HTTP REST API**, falling back to SSH localhost curl, then CLI `--stdout-json now,cpu,mem,load`, and finally PCP/sysfs.

---

## 2. Goals, Non-Goals, and Anti-Goals

### Goals
- **On-the-Fly Mutation Queue:** All mutations (services, firewall, shares, packages, container lifecycle) compile into discrete `QueuedAnsibleTask` items held in an active execution queue.
- **Console "Task Queue" Tab (Option A):** An interactive tab in the bottom Console drawer displaying staged tasks with accordion drill-downs showing the target node, native module, parameter table, privilege escalation (`run0` / `sudo`), and masked Vault secrets.
- **Step-by-Step Checkpointed Execution:** Tasks execute one-by-one via `ansible-playbook` with `ANSIBLE_STDOUT_CALLBACK=json`, parsing structured JSON results and halting on failure while keeping subsequent tasks staged.
- **Datacenter `ansible.cfg` Manager:** A visual editor in the Datacenter global options view for viewing and modifying key `ansible.cfg` properties (`stdout_callback`, `forks`, `timeout`, `host_key_checking`, `pipelining`, `become_method`, `vault_password_file`).
- **Strict Native Module Priority:** Codify and enforce module selection:
  1. `ansible.builtin.*` (e.g. `systemd`, `package`, `user`, `template`, `copy`, `cron`, `reboot`)
  2. `ansible.posix.*` (e.g. `firewalld`, `sysctl`, `mount`, `acl`)
  3. Pre-installed homelab collections (`containers.podman`, `community.general.zfs`)
  4. Galaxy Prompt: If an uninstalled collection is needed, prompt the operator with module details before installing.
  5. Last Resort: `ansible.builtin.command` or `ansible.builtin.shell`.
- **Conservative API Telemetry (30–60s):** Telemetry intervals set to 30–60 seconds, preferring Glances REST API (`:61208`), then SSH-forwarded localhost API, then CLI `glances --stdout-json now,cpu,mem,load`.

### Non-Goals
- **No Background Agent Requirement:** Remains strictly agentless over universal OpenSSH. No background daemons need to be installed on target nodes.
- **Not Replacing Fast Read-Only Commands with Heavy Ansible Runs:** Telemetry polling does not run heavy Ansible playbooks every 30s. Telemetry uses direct HTTP APIs or lightweight CLI calls; mutations use the Ansible Queue.

### Anti-Goals
- **No Plaintext Secret Exposure:** Ansible Vault variables, passwords, and tokens must never appear in plaintext in the UI, Console logs, or on-disk unencrypted queue artifacts.
- **No Uncheckpointed Batch Runs:** The queue will not execute all tasks blindly without stopping if an intermediate task fails.
- **No Fake/Mocked Outputs:** Adheres strictly to the Zero-Mock policy; all playbook executions invoke genuine `ansible-playbook` binaries against real targets.

---

## 3. Data Structures & Schema Design

### A. Queued Task Model (`src/types/index.ts`)
```ts
export type AnsibleModuleCategory = 'system' | 'container' | 'storage' | 'network' | 'package' | 'custom';

export interface AnsibleTaskStep {
  name: string;
  module: string;                  // E.g., "ansible.builtin.template"
  moduleType: 'native' | 'posix' | 'community' | 'command_fallback';
  parameters: Record<string, any>;
  notify?: string;
  register?: string;
}

export interface QueuedAnsibleTask {
  id: string;                      // Unique ID: `qtask-<timestamp>-<hash>`
  title: string;                   // E.g., "Configure Samba Share: media_vault"
  targetNodeId: string;            // Node ID
  targetNodeName: string;          // E.g., "likes-it-cold"
  targetHostRoute: string;         // Primary or failover IP/hostname
  targetSshUser: string;           // E.g., "core", "root"
  targetSshPort: number;           // E.g., 22
  category: AnsibleModuleCategory; // Category classification
  primaryModule: string;           // Primary module for badge display (e.g. "ansible.builtin.template")
  steps: AnsibleTaskStep[];        // 1 or more atomic Ansible task steps forming this mutation
  secretsMasked?: Record<string, string>; // Keys that contain vault references
  become: boolean;                 // Whether elevation is required
  becomeMethod: 'run0' | 'sudo' | 'doas'; // Elevation method
  status: 'staged' | 'running' | 'completed' | 'failed' | 'cancelled';
  createdAt: number;
  startedAt?: number;
  completedAt?: number;
  resultJson?: any;                // Structured JSON result from ansible-playbook
  error?: string;
}

export interface PlaybookQueueState {
  playbookId: string;
  tasks: QueuedAnsibleTask[];
  activeTaskId: string | null;
  isRunning: boolean;
  ansibleCfgPath: string;
}
```

### B. `ansible.cfg` Settings Model (`src/utils/ansibleConfigManager.ts`)
```ts
export interface AnsibleConfigSettings {
  stdoutCallback: 'json' | 'yaml' | 'default';
  forks: number;
  timeout: number;
  hostKeyChecking: boolean;
  pipelining: boolean;
  becomeMethod: 'run0' | 'sudo' | 'doas';
  vaultPasswordFile?: string;
  rolesPath?: string;
  collectionsPath?: string;
}
```

---

## 4. Architecture & Component Workflow

```
[ Cockpit User Action ] (e.g. Restart Service, Add Share, Update Rule)
          │
          ▼
[ Action Compiler ] (builds QueuedAnsibleTask with native module & masked parameters)
          │
          ▼
[ POST /api/queue/task ] (adds to Queue in server.ts / SQLite)
          │
          ├────────────────────────────────────────────────┐
          ▼                                                ▼
[ Console: Task Queue Tab ]                      [ Operator Drill-Down ]
(displays staged pills & accordions)              (inspects native module, params, secrets)
          │
          ▼
[ Operator clicks "Execute Queue (One-by-One)" ]
          │
          ▼
[ Queue Execution Runner ] ───────────────────────────────────────────┐
  1. Compiles single-task on-the-fly playbook (.queue-<id>.yml)       │
  2. Injects ANSIBLE_STDOUT_CALLBACK=json                             │
  3. Executes `ansible-playbook -i ansible/inventory.ini ...`        │
  4. Parses structured JSON output                                    │
  5. Checkpoint Evaluation:                                           │
     - If SUCCESS: marks completed, advances to next task in queue.   │
     - If FAILURE: marks failed, stops queue, alerts operator. ◄──────┘
```

### A. On-the-Fly Playbook Generation & Connection Injection
When a task executes, the backend generates an ephemeral, self-contained playbook file in `ansible/playbooks/.queue-<taskId>.yml`. To guarantee flawless routing across Tailscale mesh, LAN fallbacks, and non-standard SSH users without modifying the static inventory file, host connection parameters are explicitly injected into the playbook play header:

```yaml
---
- name: "Homelab Cockpit Queued Mutation: Configure Samba Share"
  hosts: likes-it-cold
  gather_facts: false
  become: true
  become_method: run0
  vars:
    ansible_host: "100.115.92.19"
    ansible_user: "core"
    ansible_port: 22
    ansible_ssh_common_args: "-F /dev/null -o StrictHostKeyChecking=accept-new -o BatchMode=yes"
  tasks:
    - name: "Deploy share configuration template"
      ansible.builtin.template:
        src: "templates/smb_share.conf.j2"
        dest: "/etc/samba/conf.d/media_vault.conf"
        mode: "0644"
      vars:
        share_path: "/mnt/storage/media"
        smb_password: "{{ vault_smb_media }}"
    - name: "Reload smbd service"
      ansible.builtin.systemd:
        name: "smb"
        state: "reloaded"
```

The command is executed one task at a time via:
```bash
ANSIBLE_STDOUT_CALLBACK=json ansible-playbook -i ansible/inventory.ini ansible/playbooks/.queue-<taskId>.yml
```
Because `ANSIBLE_STDOUT_CALLBACK=json` is enforced, `ansible-playbook` returns structured JSON output. If a task fails, execution halts immediately; subsequent tasks remain staged in the queue for operator remediation.

### B. REST Mutation Endpoint Staging Contract
Cockpit mutation endpoints (e.g. `POST /api/nodes/:id/services/:name/action`, `POST /api/nodes/:id/shares/smb`, `POST /api/nodes/:id/firewall/rules`) stage their corresponding Ansible tasks into the queue by default, returning:
```json
{
  "success": true,
  "queued": true,
  "taskId": "qtask-1792400000-a1b2",
  "message": "Action staged in Ansible Queue: Restart caddy.service"
}
```
Originating UI dialogs present a toast: `"Staged in Task Queue (1 pending)"` with a quick link to `[View in Console]`. Modals close cleanly without artificial delays.

### C. Sensitive Input & Vault Secret Ingestion
1. **Existing Vault Variable References:** In share/user modals, operators can reference pre-existing vault keys (e.g. `{{ vault_media_password }}`). These are passed directly as variable identifiers and rendered masked (`••••••••`) in the UI.
2. **New Plaintext Credentials:** If an operator enters a new password/secret in the UI:
   - If an active `vault_password_file` is configured, the backend automatically encrypts the string using `ansible-vault encrypt_string` before staging it in the task payload.
   - If no vault password file is configured, the secret is stored in an ephemeral, memory-backed variable passed via `--extra-vars` at runtime, ensuring plaintext secrets are never written to unencrypted `.yml` files on disk.

### D. Galaxy Collection Guard (Staging-Time Check)
If a user action requires a module outside `ansible.builtin` or `ansible.posix` (e.g. `containers.podman` or `community.general.zfs`), the action compiler checks whether the collection is installed locally. If not installed:
- The modal displays an inline dependency prompt: `"Requires Galaxy collection community.general. Install now?"`.
- Clicking `[Install]` calls `POST /api/ansible/collections/install` running `ansible-galaxy collection install community.general`.
- Once installed, the task is staged into the queue, ensuring subsequent batch queue runs never hang waiting for dependencies.

### C. Glances API & Telemetry Poller (Conservative 30–60s)
1. In `server.ts` and `src/utils/telemetryCollector.ts`:
   - Set poll interval to 30–60 seconds (configurable, default 45s).
   - Telemetry probe sequence for node:
     1. **Glances HTTP REST API**: Query `http://<tailscale_or_lan_ip>:61208/api/4/quicklook` and `/api/4/cpu`.
     2. **Glances over SSH Localhost**: If port 61208 is refused externally, run non-interactive SSH: `curl -s http://127.0.0.1:61208/api/4/quicklook`.
     3. **Glances CLI JSON Fallback**: If REST is inactive, run non-interactive SSH: `glances --stdout-json now,cpu,mem,load`.
     4. **PCP / sysfs Fallback**: If Glances is absent, query PCP (`pmcd`) or `/proc`/`/sys` reads.

---

## 5. User Interface Specifications

### A. Console "Task Queue" Tab (`src/components/GlobalTasksExecutionLog.tsx`)
- Placed alongside `Active Tasks`, `Ansible Stdout`, `Cluster Syslog`, and `Vault Audit`.
- Badge indicates staged task count: `Task Queue (3 Staged)`.
- Global Action Toolbar:
  - `[▶ Run Next Task]` / `[▶ Execute Queue (One-by-One)]`
  - `[Clear Queue]` (with confirmation dialog)
- Task Accordion List:
  - Staged row: `#1 | <target-node> | <intent-title> | <module-badge>` with `[Expand ▼]`.
  - Expanded Card:
    - **Native Module Pill**: E.g. `ansible.builtin.systemd` (green border "Builtin Native").
    - **Target Route**: E.g. `likes-it-cold (100.115.92.19 via Tailscale)`.
    - **Parameters Table**: Key-value display of module arguments.
    - **Vault Protection Badge**: Displays masked secret variables with vault ID references.
    - **Privilege Badge**: `run0` (systemd Polkit transient session).
    - **Row Controls**: `[Run This Task Now]`, `[Remove from Queue]`.

### B. Datacenter `ansible.cfg` Settings Card (`src/components/DatacenterGlobalView.tsx`)
- Added to Datacenter Global View under "Ansible Engine Configuration (`ansible.cfg`)".
- Interactive settings form:
  - `stdout_callback`: Dropdown (`json` [Recommended], `yaml`, `default`).
  - `forks`: Numeric stepper (default `10`).
  - `timeout`: Numeric input (default `30` seconds).
  - `host_key_checking`: Boolean toggle (default `false`).
  - `pipelining`: Boolean toggle (default `true`).
  - `become_method`: Dropdown (`run0`, `sudo`, `doas`).
  - `vault_password_file`: Text input with file presence indicator (`.vault-pass`).
- "Save & Sync `ansible.cfg`" button writing to disk and synchronizing active runner.

---

---

## 7. Extensibility & Future Modularization Architecture

To prepare the codebase for upcoming modularization efforts, all components designed in this project follow a decoupled, driver-oriented architecture:

### A. Pluggable Task & Mutation Drivers
The queue compiler delegates module generation through typed driver interfaces:
- **Core Drivers (Target for current phase):**
  - `SystemDriver`: systemd unit management (`ansible.builtin.systemd`), journalctl logs, host reboots (`ansible.builtin.reboot`).
  - `ContainerDriver`: Podman rootful and rootless management (`containers.podman.podman_container`, `podman_pod`).
  - `StorageDriver`: Samba template management, NFS exports, and ZFS/Btrfs datasets.
  - `NetworkDriver`: Firewalld rules (`ansible.posix.firewalld`), network interfaces.
  - `PackageDriver`: Package upgrades (`ansible.builtin.package`, `dnf`, `apt`, `apk`).
- **Future Extension Points (Prepared interfaces):**
  - `DatabaseDriver`: Database schema migration, user creation, and service control for MongoDB (`community.mongodb.*`) and SQL flavors (PostgreSQL `community.postgresql.*`, MySQL `community.mysql.*`).
  - `KubernetesDriver`: Manifest application and pod controls for K3s / vanilla Kubernetes (`kubernetes.core.*`).
  - `NetworkSwitchDriver`: Network device management for Cisco switches (`cisco.ios.*`) and VyOS router nodes.

### B. Target Stack Alignment & Graceful Degradation
- **Primary Target Stack:** Systemd-based Linux (Fedora, Debian, Ubuntu, Silverblue, Rocky/RHEL) with OpenSSH, Tailscale, and Glances.
- **Fallback Paths (Non-Systemd / Minimal Hosts / Network Gear):**
  - **Non-Systemd Linux (Alpine / OpenRC / SysVinit):** Detect service manager via facts/autodetection; if systemd is absent, map service tasks to `ansible.builtin.service` (`use: openrc` or `use: sysvinit`) and privilege escalation to `doas -n` or `sudo -n`.
  - **Hosts without Glances / Tailscale:** If Glances API is unreachable and CLI is missing, fallback to PCP (`pmcd`) or sysfs reads without throwing exceptions; if Tailscale is absent, route through the configured fallback LAN IP.
  - **Cisco & Network Switches:** Handled via connection plugins (`ansible_connection: network_cli` or direct management port `22`/`443`) rather than POSIX shell executors.
