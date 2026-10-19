# Cockpit Enhancements & Bugfixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the ZFS pools output parsing, implement full system logs filtering by unit/user/priority, add CRUD operations for Ansible playbooks, build a typed UI for variables and secrets, add a Catppuccin Latte light theme toggle, and make the Cockpit sub-sidebar static with its own scrollbar.

**Architecture:**
- Frontend: React 19 + TypeScript + Tailwind CSS v4.
- Backend: Express coordinator (`server.ts`) with SSH multiplexing (`runNodeCommand`) and Go daemon (`backend-go`).
- Theme: Dual theme support (Dark and Catppuccin Latte) via CSS variables and `data-theme` attribute on root.
- Persistence: `data/hosts-cache.sqlite` for host state, local file system for playbooks in `ansible/playbooks/`.

**Tech Stack:** React 19, Vite, Tailwind CSS v4, Express, SQLite, Go, Ansible, Catppuccin Latte.

---

## Global Constraints
- Zero-Mock Policy: All actions, playbooks, logs, and pools must run against real hosts or local backend files. No random noise or synthetic pools.
- Clickable markdown links for all modified files.
- Preserve all existing comments and docstrings.
- All 4 verification checks (`go test ./...`, `npm test`, `npm run lint`, `npm run build`) must pass with 0 errors.

---

## Tasks

### Task 1: Fix ZFS Pools Data Parsing in `ZfsManagementView.tsx` and `server.ts`

**Files:**
- Modify: `server.ts:930-970`
- Modify: `src/components/ZfsManagementView.tsx:50-135`
- Test: `src/components/ZfsManagementView.test.tsx` or `src/utils/orchestrator.test.ts`

**Interfaces:**
- Consumes: `/api/nodes/:id/storage` response containing `zpoolList` and `zpoolStatus`.
- Produces: Correctly parsed array of ZFS pools (`name`, `size`, `allocated`, `free`, `frag`, `health`, `vdevType`, `scrubStatus`) ignoring prose notices like `status:` and `action:`.

- [ ] **Step 1: Write unit tests verifying ZFS parser against prose output**
  Create test cases with actual output from `zpool list -H` and `zpool status` (including feature upgrade warnings) to ensure prose sentences are never treated as pool names.

- [ ] **Step 2: Run test to verify it fails on old parsing logic**
  Confirm the old parser extracted `status:`, `The`, `action:`.

- [ ] **Step 3: Update `src/components/ZfsManagementView.tsx`**
  - Parse `data.zpoolList` (`NAME SIZE ALLOC FREE CKPOINT EXPANDSZ FRAG CAP DEDUP HEALTH ALTROOT`) for pool rows.
  - Parse `data.zpoolStatus` for scan/scrub status and errors, keyed by pool name.
  - Show actual vdevs and disk devices (`sdb`, `sda cache`, etc.).

- [ ] **Step 4: Verify test passes**
  Run `npm test` and verify clean parse.

---

### Task 2: Implement Static Sub-Sidebar with Independent Scrollbar

**Files:**
- Modify: `src/App.tsx:965-995`
- Modify: `src/components/CockpitSubSidebar.tsx:1-35`

**Interfaces:**
- Consumes: Selected node and active cockpit item.
- Produces: Sticky positioned sub-sidebar with `h-[calc(100vh-140px)]`, `overflow-y-auto`, and dedicated custom scrollbar.

- [ ] **Step 1: Remove redundant outer column classes in `CockpitSubSidebar.tsx`**
  Remove `<aside className="col-span-12 lg:col-span-3 xl:col-span-2 flex flex-col gap-2">` and replace with a clean flex container with styled scrollbar.

- [ ] **Step 2: Update `App.tsx` layout**
  Add `sticky top-2 h-[calc(100vh-140px)] overflow-y-auto shrink-0 pr-1` to the sub-sidebar wrapper div so it stays static and scrolls independently of the main workspace.

- [ ] **Step 3: Test responsive resizing and scrolling**
  Verify that scrolling long menus or switching between wide/compact modes keeps the sidebar pinned on screen.

---

### Task 3: Deep System Logs Filtering (Backend & `SystemLogsView.tsx`)

**Files:**
- Modify: `server.ts` (Add route `GET /api/nodes/:id/system-logs`)
- Create: `src/components/SystemLogsView.tsx`
- Modify: `src/App.tsx:975-1035`

**Interfaces:**
- Backend: `GET /api/nodes/:id/system-logs?unit=&user=&priority=&since=&grep=&lines=&source=`
  Returns: `{ success: true, lines: string[], host: string, filter: object }`
- Frontend: `SystemLogsView` renders in main operational grid when `activeCockpitItem === 'logs'`, with full controls for unit, user, priority level, time range, text search, and real-time refresh.

- [ ] **Step 1: Add backend route in `server.ts`**
  Implement `GET /api/nodes/:id/system-logs` constructing real `journalctl` flags (`-u`, `-p`, `--since`, `-g`, `-n`) or `dmesg` commands.

- [ ] **Step 2: Create `src/components/SystemLogsView.tsx`**
  Build component with:
  - Unit input with quick badges (`sshd`, `tailscaled`, `podman`, `NetworkManager`, `firewalld`)
  - User filter input (`root`, `core`, `apps`, etc.)
  - Priority dropdown (`Emergency`, `Alert`, `Critical`, `Error`, `Warning`, `Notice`, `Info`, `Debug`)
  - Time range selector (`15m`, `1h`, `6h`, `24h`, `7d`, `All`)
  - Regex / text search input with instant highlight
  - Auto-refresh toggle and manual refresh button
  - Color-coded log lines by severity (red for error, yellow for warn, blue/teal for info).

- [ ] **Step 3: Wire `SystemLogsView` into `App.tsx`**
  When `activeCockpitItem === 'logs'` or `activeOpTab === 'logs'`, display `SystemLogsView` in the main workspace.

---

### Task 4: Ansible Playbook CRUD (Add, Remove, Edit)

**Files:**
- Modify: `server.ts` (Add `GET /api/playbooks/:name`, `POST /api/playbooks`, `PUT /api/playbooks/:name`, `DELETE /api/playbooks/:name`)
- Create: `src/components/modals/PlaybookEditorModal.tsx`
- Modify: `src/components/modals/RunPlaybookModal.tsx`
- Modify: `src/App.tsx`

**Interfaces:**
- Backend: Full CRUD on files in `ansible/playbooks/` with validation that file paths remain within the directory.
- Frontend: Modal or tab allowing users to create a new playbook, edit YAML with syntax checks, delete playbooks, and run them directly.

- [ ] **Step 1: Add CRUD endpoints in `server.ts`**
  - `GET /api/playbooks/:name`: return file content.
  - `POST /api/playbooks`: create new `.yml` file with initial template.
  - `PUT /api/playbooks/:name`: update file content.
  - `DELETE /api/playbooks/:name`: delete file.

- [ ] **Step 2: Create `PlaybookEditorModal.tsx`**
  Build editor with syntax check trigger (`/api/playbook/syntax-check`), save button, delete confirmation, and run button.

- [ ] **Step 3: Add "Add / Edit Playbook" buttons to `RunPlaybookModal.tsx` and Header**
  Enable seamless navigation between running playbooks and editing them.

---

### Task 5: Structured UI for Vars & Secrets

**Files:**
- Modify: `src/components/modals/GroupVarsModal.tsx`
- Modify: `src/components/modals/RunPlaybookModal.tsx`
- Create: `src/components/VarsSecretsEditor.tsx`

**Interfaces:**
- Component `VarsSecretsEditor`:
  Props: `value: Record<string, any>`, `onChange: (val: Record<string, any>) => void`, `onSave: () => void`.
  Features: Table rows for each variable with key name, type selector (`string`, `number`, `boolean`, `secret`), value editor (masked input with reveal eye icon for secret, toggle for boolean, number/text input), add/delete row, plus raw YAML mode toggle.

- [ ] **Step 1: Create `src/components/VarsSecretsEditor.tsx`**
  Implement typed row editor with:
  - Add variable row (`+ Add Variable`)
  - Key name input
  - Type dropdown (`String`, `Number`, `Boolean`, `Secret / Vault`)
  - Value input with password masking and eye reveal icon
  - Delete row button (`🗑️`)
  - Mode toggle: "Structured UI" vs "Raw YAML" with bidirectional synchronization.

- [ ] **Step 2: Integrate into `GroupVarsModal.tsx`**
  Replace plain `<textarea>` with `VarsSecretsEditor`.

- [ ] **Step 3: Integrate into `RunPlaybookModal.tsx`**
  Replace raw extra-vars text box with structured key-value inputs.

---

### Task 6: Catppuccin Latte Light Mode

**Files:**
- Modify: `src/index.css`
- Modify: `src/components/Header.tsx`
- Modify: `src/App.tsx`

**Interfaces:**
- Theme state: `'dark' | 'latte'` stored in `localStorage` and applied via `data-theme="latte"` on `<html>` or `<body>`.
- Palette: Catppuccin Latte colors:
  Base `#eff1f5`, Mantle `#e6e9ef`, Crust `#dce0e8`, Surface 0 `#ccd0da`, Surface 1 `#bcc0cc`, Surface 2 `#acb0be`, Text `#4c4f69`, Subtext 0 `#6c6f85`, Blue `#1e66f5`, Mauve `#8839ef`, Teal `#179299`, Red `#d20f39`, Green `#40a02b`.

- [ ] **Step 1: Add Catppuccin Latte CSS variables and style overrides in `src/index.css`**
  Map `--color-surface-*`, background classes, border classes, and text colors under `[data-theme="latte"]` or `.theme-latte`.

- [ ] **Step 2: Add theme toggle button in `src/components/Header.tsx`**
  Add a theme switcher button with sun/moon icon and tooltip.

- [ ] **Step 3: Wire theme state in `App.tsx`**
  Persist theme selection in `localStorage` and apply attribute on document root.

---

### Task 7: Full System Verification

**Files:**
- Run test commands across the entire stack.

- [ ] **Step 1: Run Go tests**
  `cd backend-go && go test -v ./... && cd ..`
- [ ] **Step 2: Run Vitest unit tests**
  `npm test`
- [ ] **Step 3: Run TypeScript linter**
  `npm run lint`
- [ ] **Step 4: Run Vite production build**
  `npm run build`
- [ ] **Step 5: Smoke test all new endpoints and views**
  Verify ZFS view, System Logs filtering, Playbook CRUD, Vars editor, and Theme toggle in browser.
