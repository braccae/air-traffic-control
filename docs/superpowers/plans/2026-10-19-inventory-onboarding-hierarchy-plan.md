# Multi-Path Node Onboarding, Tiered Ansible Vault, & Hierarchical Inventory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a four-path node onboarding workflow (Tailscale manual, manual SSH with Ansible Vault encryption, WinRM, and dynamic Tailscale tag following), backed by an automated `uv` bootstrapper managing a persistent container virtualenv (`ansible-core`, `pywinrm`), a tiered Vault credential engine, and a hierarchical inventory sidebar distinguishing Tailscale tag chunks from non-Tailscale custom folders.

**Architecture:** 
1. **Automated `uv` Bootstrapper & Persistent Venv:** Probes for `uv` on `PATH` or downloads the standalone binary to `data/bin/uv`. Uses `uv` to provision a persistent virtual environment at `data/ansible-venv` (mounted container volume) populated with `ansible-core`, `pywinrm`, `requests`, `jmespath`, `cryptography`, and required plugin packages.
2. **Tiered Vault Credential Engine:** A modular service resolving vault passphrases via `vault_password_file` > environment variable > interactive `ask_password` prompt, encrypting and decrypting secrets via `ansible-vault` from the managed venv.
3. **Dynamic Inventory Chunks & Custom Folders:** Tag-following dynamic inventory chunks and non-Tailscale folder models persisted in SQLite, with an automated sync engine pulling matching peers from the Tailscale mesh.
4. **Four-Path Add Node Modal & Hierarchical Sidebar:** A redesigned onboarding modal with distinct workflows for Tailscale, Vault-encrypted SSH, WinRM, and tag-following, paired with an inventory sidebar organized into `<ts-shortname>` tag chunks and non-Tailscale folders.

**Tech Stack:** React 19, Vite, Tailwind CSS v4, Express, SQLite (`better-sqlite3`), `uv` (Astral), Python 3 virtualenv, `ansible-core`, `pywinrm`, Tailscale CLI.

**Spec:** `docs/superpowers/specs/2026-10-19-inventory-onboarding-hierarchy-design.md`

## Global Constraints

- Zero-Mock Policy: All node queries, hardware probes, and Tailscale syncs must target live hosts, real CLI outputs, or return real error payloads.
- Secret Protection Invariant: Cleartext passwords and private keys must never be saved in SQLite or exposed in REST responses. Stored secrets must use encrypted Ansible Vault blocks (`$ANSIBLE_VAULT;1.1;AES256`).
- Persistent Container Venv: All Ansible CLI tools (`ansible-playbook`, `ansible-vault`, `ansible-galaxy`) must run from the persistent venv located at `data/ansible-venv` (or `process.env.ANSIBLE_VENV_PATH`), ensuring zero package loss across container restarts.
- Automated `uv` Pull: If `uv` is not present on `PATH`, the application automatically downloads the official standalone `uv` binary into `data/bin/uv`.
- Tiered Vault Resolution: Strictly follow the order: `vault_password_file` -> `ANSIBLE_VAULT_PASSWORD` / `ANSIBLE_VAULT_PASSWORD_FILE` -> interactive `ask_password`.
- Dynamic Tag Following: If credentials are blank when following a tag, assume `tailscale-ssh` (keyless PAM); all hosts in a tag share those login details.
- Local Testing Guard: `ssh-agent forwarding` is preserved strictly for local development and testing; production defaults to Tailscale SSH or Vault-secured credentials.
- 4-Gate Verification: Every task must verify `cd backend-go && go test -v ./... && cd ..`, `npm test`, `npm run lint`, and `npm run build`.

## Review Focus

- Missing `uv` on host or container triggers automatic binary download into `data/bin/uv` without manual operator intervention.
- Missing vault password source triggers `ask_password` prompt instead of silent failure or cleartext fallback.
- Dynamic tag chunk sync handles disappearing or newly tagged Tailscale peers gracefully without dropping manual node settings.
- WinRM onboarding sets `nodeType = 'windows'`, default port 5985/5986, and appropriate connection parameters.
- Empty credentials on followed tag automatically default to `tailscale-ssh`.
- Custom folders for non-Tailscale hosts persist across page reloads and allow assigning/unassigning hosts cleanly.

---

### Task 1: Automated `uv` Bootstrapper & Persistent Ansible/WinRM Virtualenv Manager

**Files:**
- Create: `src/utils/ansibleVenvManager.ts`
- Create: `src/utils/ansibleVenvManager.test.ts`
- Modify: `server.ts`

**Interfaces:**
- Consumes: `process.env.ANSIBLE_VENV_PATH`, `data/` directory
- Produces: `findOrBootstrapUv(): Promise<string>`, `ensureAnsibleVenv(): Promise<AnsibleVenvInfo>`, `getAnsibleExecutionEnv(): Record<string, string>`

- [ ] **Step 1: Write failing unit test in `src/utils/ansibleVenvManager.test.ts`**

```typescript
import { describe, it, expect, beforeEach } from 'vitest';
import {
  findOrBootstrapUv,
  getAnsibleVenvPath,
  getAnsibleExecutionEnv,
  REQUIRED_VENV_PACKAGES,
} from './ansibleVenvManager.ts';
import fs from 'fs';
import path from 'path';

describe('Ansible Venv Manager with uv bootstrapper', () => {
  it('identifies uv binary path if available or points to data/bin/uv fallback', async () => {
    const uvPath = await findOrBootstrapUv();
    expect(typeof uvPath).toBe('string');
    expect(uvPath.length).toBeGreaterThan(0);
  });

  it('determines persistent container venv path under data/ansible-venv', () => {
    const venvPath = getAnsibleVenvPath();
    expect(venvPath).toContain('data');
    expect(venvPath).toContain('ansible-venv');
  });

  it('includes required core packages: ansible-core and pywinrm for WinRM support', () => {
    expect(REQUIRED_VENV_PACKAGES).toContain('ansible-core');
    expect(REQUIRED_VENV_PACKAGES).toContain('pywinrm');
    expect(REQUIRED_VENV_PACKAGES).toContain('requests');
    expect(REQUIRED_VENV_PACKAGES).toContain('jmespath');
    expect(REQUIRED_VENV_PACKAGES).toContain('cryptography');
  });

  it('injects venv bin directory into PATH in getAnsibleExecutionEnv', () => {
    const env = getAnsibleExecutionEnv();
    const venvBin = path.join(getAnsibleVenvPath(), 'bin');
    expect(env.PATH).toContain(venvBin);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/utils/ansibleVenvManager.test.ts`
Expected: FAIL with "Cannot find module './ansibleVenvManager.ts'"

- [ ] **Step 3: Implement `src/utils/ansibleVenvManager.ts`**

Implement:
- `findOrBootstrapUv(options?: { customDataDir?: string; downloadIfMissing?: boolean }): Promise<string>`
  - Searches standard locations (`which uv`, `process.env.PATH`, `~/.local/bin/uv`, `~/.cargo/bin/uv`, `data/bin/uv`).
  - If missing and `downloadIfMissing = true`, downloads standalone `uv` binary (or runs installer) to `data/bin/uv` and makes executable (`chmod 755`).
- `getAnsibleVenvPath(): string`: returns `process.env.ANSIBLE_VENV_PATH || path.resolve(process.cwd(), 'data', 'ansible-venv')`.
- `ensureAnsibleVenv(options?: { uvPath?: string; venvPath?: string }): Promise<AnsibleVenvInfo>`:
  - If `data/ansible-venv/bin/ansible` does not exist:
    - Runs `${uv} venv ${venvPath}`.
    - Runs `${uv} pip install --python ${venvPath}/bin/python ansible-core pywinrm requests jmespath cryptography netaddr`.
  - Returns paths to `python`, `ansible`, `ansiblePlaybook`, `ansibleVault`.
- `getAnsibleExecutionEnv(): Record<string, string>`: prepends `data/ansible-venv/bin` to `process.env.PATH`.

- [ ] **Step 4: Mount status endpoint in `server.ts` & initialize on startup**

Mount:
- `GET /api/ansible/environment`: returns `{ success: true, venvPath, uvPath, installed: boolean, packages: string[] }`
- Call `ensureAnsibleVenv()` during `startServer()` in background so environment is ready for queue and vault operations.

- [ ] **Step 5: Run tests and 4 verification gates**

Run:
```bash
npx vitest run src/utils/ansibleVenvManager.test.ts
cd backend-go && go test -v ./... && cd ..
npm test
npm run lint
npm run build
```
Expected: PASS with 0 errors.

- [ ] **Step 6: Commit**

```bash
git add src/utils/ansibleVenvManager.ts src/utils/ansibleVenvManager.test.ts server.ts
git commit -m "feat(ansible): add uv bootstrapper and persistent container virtualenv manager"
```

---

### Task 2: Tiered Vault Credential Engine (`vaultManager.ts`) & REST API

**Files:**
- Create: `src/utils/vaultManager.ts`
- Create: `src/utils/vaultManager.test.ts`
- Modify: `server.ts`

**Interfaces:**
- Consumes: `loadAnsibleConfig()` from `src/utils/ansibleConfigManager.ts`, `getAnsibleExecutionEnv()` from `src/utils/ansibleVenvManager.ts`
- Produces: `resolveVaultPasswordSource()`, `encryptVaultString()`, `decryptVaultString()`

- [ ] **Step 1: Write failing unit test in `src/utils/vaultManager.test.ts`**

```typescript
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import {
  resolveVaultPasswordSource,
  encryptVaultString,
  decryptVaultString,
} from './vaultManager.ts';
import fs from 'fs';
import path from 'path';

describe('Tiered Vault Credential Engine', () => {
  const tmpPassFile = path.resolve('/tmp/test-vault-pass.txt');

  beforeEach(() => {
    delete process.env.ANSIBLE_VAULT_PASSWORD;
    delete process.env.ANSIBLE_VAULT_PASSWORD_FILE;
    if (fs.existsSync(tmpPassFile)) fs.unlinkSync(tmpPassFile);
  });

  afterEach(() => {
    delete process.env.ANSIBLE_VAULT_PASSWORD;
    delete process.env.ANSIBLE_VAULT_PASSWORD_FILE;
    if (fs.existsSync(tmpPassFile)) fs.unlinkSync(tmpPassFile);
  });

  it('Tier 1: resolves password from vault_password_file when file exists', () => {
    fs.writeFileSync(tmpPassFile, 'file-secret-pass\n', 'utf8');
    const source = resolveVaultPasswordSource({ vaultPasswordFile: tmpPassFile });
    expect(source.source).toBe('file');
    expect(source.password).toBe('file-secret-pass');
  });

  it('Tier 2: resolves password from ANSIBLE_VAULT_PASSWORD env var when file not set', () => {
    process.env.ANSIBLE_VAULT_PASSWORD = 'env-secret-pass';
    const source = resolveVaultPasswordSource({});
    expect(source.source).toBe('env');
    expect(source.password).toBe('env-secret-pass');
  });

  it('Tier 3: falls back to ask_password when neither file nor env var exists', () => {
    const source = resolveVaultPasswordSource({});
    expect(source.source).toBe('ask_password');
    expect(source.hasPassword).toBe(false);
  });

  it('encrypts plaintext into valid $ANSIBLE_VAULT;1.1;AES256 block and decrypts it', async () => {
    const secret = 'super-secret-ssh-key-or-pass';
    const encrypted = await encryptVaultString(secret, 'ssh_password', { password: 'master-key-123' });
    expect(encrypted).toContain('!vault |');
    expect(encrypted).toContain('$ANSIBLE_VAULT;1.1;AES256');

    const decrypted = await decryptVaultString(encrypted, { password: 'master-key-123' });
    expect(decrypted).toBe(secret);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/utils/vaultManager.test.ts`
Expected: FAIL with "Cannot find module './vaultManager.ts'"

- [ ] **Step 3: Implement `src/utils/vaultManager.ts`**

Implement:
- `resolveVaultPasswordSource(options?: { vaultPasswordFile?: string; env?: NodeJS.ProcessEnv })`
- `encryptVaultString(plaintext: string, varName?: string, options?: { password?: string; passwordFile?: string; env?: Record<string, string> }): Promise<string>` using `ansible-vault encrypt_string --vault-password-file /dev/stdin <plaintext> --name <varName>` with `getAnsibleExecutionEnv()`
- `decryptVaultString(vaultPayload: string, options?: { password?: string; passwordFile?: string; env?: Record<string, string> }): Promise<string>` using `ansible-vault decrypt --vault-password-file /dev/stdin <tmpFile>` with `getAnsibleExecutionEnv()`

- [ ] **Step 4: Mount endpoints in `server.ts`**

Mount:
- `GET /api/vault/status`: returns `{ success: true, hasPasswordSource: boolean, source: 'file' | 'env' | 'ask_password' }`
- `POST /api/vault/encrypt`: validates body, runs `encryptVaultString`, returns `{ success: true, vaultBlock }`
- `POST /api/vault/decrypt`: validates body, runs `decryptVaultString`, returns `{ success: true, plaintext }`

- [ ] **Step 5: Run tests and 4 verification gates**

Run:
```bash
npx vitest run src/utils/vaultManager.test.ts
cd backend-go && go test -v ./... && cd ..
npm test
npm run lint
npm run build
```
Expected: PASS with 0 errors.

- [ ] **Step 6: Commit**

```bash
git add src/utils/vaultManager.ts src/utils/vaultManager.test.ts server.ts
git commit -m "feat(vault): implement tiered Ansible Vault credential engine and REST endpoints"
```

---

### Task 3: Dynamic Tag Inventory Chunk & Custom Folder SQLite Models

**Files:**
- Modify: `src/types/index.ts`
- Modify: `src/utils/hostCacheDb.ts`
- Create: `src/utils/inventoryManager.test.ts`
- Modify: `server.ts`

**Interfaces:**
- Consumes: `createHostCacheStore()` from `src/utils/hostCacheDb.ts`
- Produces: `TagInventoryChunk`, `CustomFolder`, DB methods `upsertTagChunk`, `getAllTagChunks`, `deleteTagChunk`, `upsertCustomFolder`, `getAllCustomFolders`, `deleteCustomFolder`

- [ ] **Step 1: Update `src/types/index.ts`**

Export:
```typescript
export interface TagInventoryChunk {
  id: string;
  tag: string;
  sshUser?: string;
  vaultSecretId?: string;
  authMethod: 'tailscale-ssh' | 'vault-password' | 'vault-sshkey' | 'ssh-agent';
  createdAt: number;
}

export interface CustomFolder {
  id: string;
  name: string;
  createdAt: number;
}
```
Add to `NodeItem`:
```typescript
connectionProtocol?: 'ssh' | 'winrm' | 'tailscale_ssh';
vaultSecretId?: string;
folder?: string;
isDynamicTagMember?: boolean;
followedTag?: string;
```

- [ ] **Step 2: Write failing unit test in `src/utils/inventoryManager.test.ts`**

```typescript
import { describe, it, expect } from 'vitest';
import { createHostCacheStore } from './hostCacheDb.ts';

describe('Inventory Chunks & Folders Store', () => {
  it('persists and retrieves custom folders and dynamic tag chunks', () => {
    const store = createHostCacheStore(':memory:');
    
    store.upsertCustomFolder({ id: 'folder-lab', name: 'Lab Hardware', createdAt: Date.now() });
    const folders = store.getAllCustomFolders();
    expect(folders.length).toBe(1);
    expect(folders[0].name).toBe('Lab Hardware');

    store.upsertTagChunk({
      id: 'chunk-tag:worker',
      tag: 'tag:worker',
      sshUser: 'core',
      authMethod: 'tailscale-ssh',
      createdAt: Date.now(),
    });
    const chunks = store.getAllTagChunks();
    expect(chunks.length).toBe(1);
    expect(chunks[0].tag).toBe('tag:worker');

    store.deleteTagChunk('chunk-tag:worker');
    expect(store.getAllTagChunks().length).toBe(0);
  });
});
```

- [ ] **Step 3: Implement SQLite tables and methods in `src/utils/hostCacheDb.ts`**

In `src/utils/hostCacheDb.ts`:
- Create tables `inventory_folders` and `tag_inventory_chunks`.
- Implement `upsertCustomFolder`, `getAllCustomFolders`, `deleteCustomFolder`, `upsertTagChunk`, `getAllTagChunks`, `deleteTagChunk`.

- [ ] **Step 4: Mount inventory chunk and folder REST endpoints in `server.ts`**

Mount:
- `GET /api/inventory/chunks`
- `POST /api/inventory/chunks`
- `DELETE /api/inventory/chunks/:id`
- `GET /api/inventory/folders`
- `POST /api/inventory/folders`
- `DELETE /api/inventory/folders/:id`

- [ ] **Step 5: Run tests and 4 verification gates**

Run:
```bash
npx vitest run src/utils/inventoryManager.test.ts
cd backend-go && go test -v ./... && cd ..
npm test
npm run lint
npm run build
```
Expected: PASS with 0 errors.

- [ ] **Step 6: Commit**

```bash
git add src/types/index.ts src/utils/hostCacheDb.ts src/utils/inventoryManager.test.ts server.ts
git commit -m "feat(inventory): add dynamic tag chunks and custom folder persistence in SQLite"
```

---

### Task 4: Dynamic Tailscale Tag Synchronization Engine

**Files:**
- Create: `src/utils/tailscaleSync.ts`
- Create: `src/utils/tailscaleSync.test.ts`
- Modify: `server.ts`

**Interfaces:**
- Consumes: `TagInventoryChunk` from `src/types/index.ts`, Tailscale peer response from `/api/tailscale/peers`
- Produces: `syncTagInventoryChunks(chunks: TagInventoryChunk[], peers: any[], existingNodes: NodeItem[]): { updatedNodes: NodeItem[]; addedCount: number; removedCount: number }`

- [ ] **Step 1: Write failing unit test in `src/utils/tailscaleSync.test.ts`**

```typescript
import { describe, it, expect } from 'vitest';
import { syncTagInventoryChunks } from './tailscaleSync.ts';
import type { TagInventoryChunk, NodeItem } from '../types/index.ts';

describe('Tailscale Tag Synchronization Engine', () => {
  it('discovers nodes matching a tag chunk and configures default tailscale-ssh', () => {
    const chunk: TagInventoryChunk = {
      id: 'chunk-tag:worker',
      tag: 'tag:worker',
      authMethod: 'tailscale-ssh',
      createdAt: Date.now(),
    };

    const peers = [
      {
        HostName: 'k8s-w1',
        DNSName: 'k8s-w1.leo-banjo.ts.net.',
        TailscaleIPs: ['100.100.1.1'],
        Tags: ['tag:worker'],
        OS: 'linux',
        Online: true,
      },
      {
        HostName: 'unrelated',
        DNSName: 'unrelated.leo-banjo.ts.net.',
        TailscaleIPs: ['100.100.1.2'],
        Tags: ['tag:storage'],
        OS: 'linux',
        Online: true,
      },
    ];

    const result = syncTagInventoryChunks([chunk], peers, []);
    expect(result.updatedNodes.length).toBe(1);
    expect(result.updatedNodes[0].name).toBe('k8s-w1');
    expect(result.updatedNodes[0].authMethod).toBe('tailscale-ssh');
    expect(result.updatedNodes[0].isDynamicTagMember).toBe(true);
    expect(result.updatedNodes[0].followedTag).toBe('tag:worker');
  });

  it('inherits shared vault credentials when configured on the tag chunk', () => {
    const chunk: TagInventoryChunk = {
      id: 'chunk-tag:pve',
      tag: 'tag:pve',
      sshUser: 'root',
      authMethod: 'vault-password',
      vaultSecretId: 'vault_pve_pass',
      createdAt: Date.now(),
    };

    const peers = [
      {
        HostName: 'pve-node-1',
        DNSName: 'pve-node-1.leo-banjo.ts.net.',
        TailscaleIPs: ['100.100.2.1'],
        Tags: ['tag:pve'],
        OS: 'linux',
        Online: true,
      },
    ];

    const result = syncTagInventoryChunks([chunk], peers, []);
    expect(result.updatedNodes[0].sshUser).toBe('root');
    expect(result.updatedNodes[0].vaultSecretId).toBe('vault_pve_pass');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/utils/tailscaleSync.test.ts`
Expected: FAIL with "Cannot find module './tailscaleSync.ts'"

- [ ] **Step 3: Implement `src/utils/tailscaleSync.ts`**

Implement:
- `syncTagInventoryChunks(chunks: TagInventoryChunk[], peers: any[], existingNodes: NodeItem[])`:
  - Matches peers by `peer.Tags.includes(chunk.tag)`.
  - Preserves existing nodes that are manually added or from other sources.
  - Automatically generates `NodeItem` records with `isDynamicTagMember = true`, `followedTag = chunk.tag`.
  - If `authMethod === 'tailscale-ssh'` or credentials empty, sets keyless Tailscale SSH.
- Integrate into `refreshTailscaleNodes()` in `server.ts` so dynamic chunk nodes are synchronized on startup and refresh.

- [ ] **Step 4: Run tests and 4 verification gates**

Run:
```bash
npx vitest run src/utils/tailscaleSync.test.ts
cd backend-go && go test -v ./... && cd ..
npm test
npm run lint
npm run build
```
Expected: PASS with 0 errors.

- [ ] **Step 5: Commit**

```bash
git add src/utils/tailscaleSync.ts src/utils/tailscaleSync.test.ts server.ts
git commit -m "feat(sync): implement dynamic Tailscale tag inventory synchronization engine"
```

---

### Task 5: Add Node Modal Multi-Path Redesign (`AddNodeModal.tsx`)

**Files:**
- Modify: `src/components/modals/AddNodeModal.tsx`
- Create: `src/components/modals/AddNodeModal.test.tsx`

**Interfaces:**
- Consumes: `TagInventoryChunk` from `src/types/index.ts`, `/api/vault/status`, `/api/vault/encrypt`, `/api/tailscale/peers`
- Produces: Redesigned modal with 4 tabs: `Tailscale Manual`, `Manual SSH`, `WinRM`, `Follow Tag (Dynamic)`

- [ ] **Step 1: Write failing unit test in `src/components/modals/AddNodeModal.test.tsx`**

```tsx
import React from 'react';
import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { AddNodeModal } from './AddNodeModal.tsx';

describe('AddNodeModal Multi-Path Provisioning', () => {
  it('renders the 4 distinct onboarding tabs', () => {
    render(
      <AddNodeModal
        isOpen={true}
        onClose={() => {}}
        onAddNode={() => {}}
      />
    );

    expect(screen.getByText(/Tailscale Mesh/i)).toBeInTheDocument();
    expect(screen.getByText(/Manual SSH/i)).toBeInTheDocument();
    expect(screen.getByText(/WinRM/i)).toBeInTheDocument();
    expect(screen.getByText(/Follow Tag/i)).toBeInTheDocument();
  });

  it('switches to WinRM tab and displays Windows Remote Management port 5985', () => {
    render(
      <AddNodeModal
        isOpen={true}
        onClose={() => {}}
        onAddNode={() => {}}
      />
    );

    fireEvent.click(screen.getByText(/WinRM/i));
    expect(screen.getByText(/WinRM Port/i)).toBeInTheDocument();
    expect(screen.getByDisplayValue('5985')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/components/modals/AddNodeModal.test.tsx`
Expected: FAIL as current modal does not have the 4 tabs.

- [ ] **Step 3: Redesign `AddNodeModal.tsx` with the 4 Onboarding Tabs**

Implement:
1. **Tabs Navigation Bar**:
   - `Tailscale Mesh (Manual)`
   - `Manual SSH (Vault-Encrypted)`
   - `WinRM (Windows)`
   - `Follow Tag (Dynamic Chunk)`
2. **Tab 1: Tailscale Manual**:
   - Unmanaged peer picker, auto-fill IP/DNS/OS, autodetect Linux vs Windows, defaults to Tailscale SSH.
3. **Tab 2: Manual SSH**:
   - Host/IP, port (default 22), user.
   - Password or Private Key input with inline "Encrypt with Ansible Vault" option.
   - If Vault requires password and none set, prompts for master key.
   - Clear checkbox: `Use ssh-agent forwarding (Local testing only)`.
4. **Tab 3: WinRM**:
   - Host/IP, WinRM port (default 5985 HTTP / 5986 HTTPS), transport (NTLM / Kerberos / Basic).
   - User (e.g. `Administrator`), password encrypted in Vault.
5. **Tab 4: Follow Tag (Dynamic Chunk)**:
   - Tag picker/input (e.g. `tag:worker`).
   - Shared username, password/SSH key (or left blank for Tailscale SSH).
   - Displays live preview count of matching peers in the mesh.
   - "Follow Tag & Sync Nodes" button calling `POST /api/inventory/chunks`.

- [ ] **Step 4: Run tests and 4 verification gates**

Run:
```bash
npx vitest run src/components/modals/AddNodeModal.test.tsx
cd backend-go && go test -v ./... && cd ..
npm test
npm run lint
npm run build
```
Expected: PASS with 0 errors.

- [ ] **Step 5: Commit**

```bash
git add src/components/modals/AddNodeModal.tsx src/components/modals/AddNodeModal.test.tsx
git commit -m "feat(ui): redesign AddNodeModal with Tailscale, Vault SSH, WinRM, and Tag Following tabs"
```

---

### Task 6: Hierarchical Inventory Sidebar (`ServerViewSidebar.tsx`)

**Files:**
- Modify: `src/utils/navigationViewModel.ts`
- Modify: `src/utils/navigationViewModel.test.ts`
- Modify: `src/components/ServerViewSidebar.tsx`

**Interfaces:**
- Consumes: `NodeItem`, `TagInventoryChunk`, `CustomFolder`
- Produces: `buildInventoryTree(nodes, chunks, folders)` and rendered tree matching sketch

- [ ] **Step 1: Write failing unit test in `src/utils/navigationViewModel.test.ts`**

```typescript
import { describe, it, expect } from 'vitest';
import { buildInventoryTree } from './navigationViewModel.ts';
import type { NodeItem, TagInventoryChunk, CustomFolder } from '../types/index.ts';

describe('buildInventoryTree', () => {
  it('partitions nodes into Tailscale tag chunks and non-Tailscale folders matching the operator sketch', () => {
    const nodes: NodeItem[] = [
      { id: 'n1', name: 'host-1', tailscaleIp: '100.1.1.1', tags: ['tag:prod'], group: 'tag:prod' } as any,
      { id: 'n2', name: 'host-2', tailscaleIp: '100.1.1.2', tags: ['tag:prod'], group: 'tag:prod' } as any,
      { id: 'n3', name: 'host-3', tailscaleIp: '100.1.1.3', tags: ['tag:stage'], group: 'tag:stage' } as any,
      { id: 'n4', name: 'non-ts-1', tailscaleIp: '', ip: '192.168.1.10', group: 'lan' } as any,
      { id: 'n5', name: 'non-ts-2', tailscaleIp: '', ip: '192.168.1.11', folder: 'f-storage', group: 'lan' } as any,
    ];

    const folders: CustomFolder[] = [{ id: 'f-storage', name: 'Storage Vault', createdAt: 1 }];
    const chunks: TagInventoryChunk[] = [{ id: 'c1', tag: 'tag:prod', authMethod: 'tailscale-ssh', createdAt: 1 }];

    const tree = buildInventoryTree(nodes, chunks, folders, 'leo-banjo.ts.net');
    expect(tree.tailscale.shortName).toBe('leo-banjo.ts.net');
    expect(tree.tailscale.tags.length).toBeGreaterThanOrEqual(1);
    expect(tree.nonTailscale.standalone.length).toBe(1);
    expect(tree.nonTailscale.folders.length).toBe(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/utils/navigationViewModel.test.ts`
Expected: FAIL with "buildInventoryTree is not defined"

- [ ] **Step 3: Implement `buildInventoryTree` in `src/utils/navigationViewModel.ts`**

Implement `buildInventoryTree(nodes, chunks, folders, defaultTailnet)`:
- Partition into Tailscale section (`<ts-shortname>` & tag groups) and Non-Tailscale section (standalone & folders).

- [ ] **Step 4: Update `ServerViewSidebar.tsx` to render the hierarchical tree**

In `src/components/ServerViewSidebar.tsx`:
- Render "Inventory" header.
- Tailscale section:
  - Header: `<ts-shortname>` (e.g. `leo-banjo.ts.net`).
  - Accordion for each `<tag>`, showing count of members and dynamic chunk indicator.
  - Member hosts indented underneath.
- Non-Tailscale section:
  - Standalone non-TS hosts.
  - Collapsible `<Folder/>` items with folder name, "+ Add Folder" button, and assigned hosts.

- [ ] **Step 5: Run tests and 4 verification gates**

Run:
```bash
npx vitest run src/utils/navigationViewModel.test.ts
cd backend-go && go test -v ./... && cd ..
npm test
npm run lint
npm run build
```
Expected: PASS with 0 errors.

- [ ] **Step 6: Commit**

```bash
git add src/utils/navigationViewModel.ts src/utils/navigationViewModel.test.ts src/components/ServerViewSidebar.tsx
git commit -m "feat(sidebar): render hierarchical inventory matching operator sketch"
```

---

### Task 7: Full System Integration & 4-Gate Verification

**Files:**
- Modify: `src/App.tsx`
- Modify: `AGENTS.md`
- Modify: `ARCHITECTURE.md`

- [ ] **Step 1: Wire tag chunk and folder state in `src/App.tsx`**

- Fetch chunks (`GET /api/inventory/chunks`) and folders (`GET /api/inventory/folders`) on mount.
- Provide callback handlers for adding folders, following tags, and updating node assignments.
- Listen for WebSocket inventory update broadcasts.

- [ ] **Step 2: Update `AGENTS.md` and `ARCHITECTURE.md`**

Document:
- Automated `uv` bootstrapper and persistent venv architecture (`data/ansible-venv`).
- The four onboarding pathways and WinRM support.
- The tiered Vault password resolution hierarchy (`vault_password_file` > env var > `ask_password`).
- Dynamic tag inventory chunks and non-Tailscale organizer folders.
- Invariant on `ssh-agent forwarding` strictly for local testing.

- [ ] **Step 3: Run all 4 verification gates**

Run:
```bash
cd backend-go && go test -v ./... && cd ..
npm test
npm run lint
npm run build
```
All 4 must pass with zero errors!

- [ ] **Step 4: Commit**

```bash
git add src/App.tsx AGENTS.md ARCHITECTURE.md
git commit -m "chore(integration): complete multi-path onboarding and hierarchical inventory integration"
```

