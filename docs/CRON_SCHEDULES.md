# Cron Schedules — scheduled playbooks and mutations

ATC can run work on a schedule: an Ansible playbook, or a set of declarative
Ansible steps (the same steps the mutation queue accepts).

## Design decision: the ATC scheduler drives runs — no host-side cron

Schedules are executed **by the coordinator**, in-process
(`src/utils/cronScheduler.ts`). ATC does **not** install a systemd timer or a
crontab entry on any target host, and it does not do both.

Why:

- **Agentless fleet.** Everything else in ATC reaches hosts over non-interactive
  SSH with zero custom agent software (`AGENTS.md` §3). Writing timers onto
  targets would mean persistent host-side state, per-distro unit files, and a
  second source of truth for when a job runs.
- **Survives target rebuilds.** A host rebuilt from a fresh image keeps its
  schedules because they live in the coordinator's SQLite store, not on the host.
- **One visible execution path.** A cron run goes through the exact same runner
  as an interactive run, so it lands in the console's task stream
  (`tasks` table / `GlobalTaskItem`) or the Ansible mutation queue
  (`ansible_queue_tasks`) with captured output — no separate log silo.
- **Single clock.** Expressions are evaluated against the coordinator host's
  local clock. There is no per-target timezone reconciliation.

Consequence to be aware of: if the coordinator is down at the scheduled minute,
that occurrence is skipped (see *Missed runs* below). If the coordinator is down
for a long time, schedules do not "catch up" by replaying every missed slot.

## Data model

Table `cron_schedules` in `data/hosts-cache.sqlite` (`src/utils/cronScheduleStore.ts`):

| column | meaning |
| --- | --- |
| `id` | `cron-<epoch>-<rand>` |
| `name` | display name |
| `cron_expression` | canonical 5-field expression (`minute hour day-of-month month day-of-week`) |
| `enabled` | 1 = armed |
| `scope_kind` | `host` \| `group` \| `datacenter` |
| `scope_target` | host id/short name; tag (with or without `tag:`); `all`/label for datacenter |
| `task_kind` | `playbook` \| `ansible_steps` |
| `payload_json` | per-kind payload (see below) |
| `next_run_at` | epoch ms of the next fire, `NULL` when disabled or exhausted |
| `last_run_at`, `last_run_status`, `last_run_task_id`, `last_run_error` | last run outcome |

Expressions support `*`, `n`, `a-b`, `*/step`, `a-b/step`, comma lists, month and
day-of-week names (`jan`, `mon`), and the `@hourly`/`@daily`/`@weekly`/
`@monthly`/`@yearly` macros. Standard cron semantics apply: when both
day-of-month and day-of-week are restricted, a day matches if **either** matches.

## Task kinds

### `playbook`

```json
{ "playbook": "site.yml", "checkMode": false, "tags": "baseline",
  "extraVars": { "k": "v" }, "limit": "optional-override" }
```

Spawns a real `ansible-playbook -i <inventory> <playbook> [--limit ...]` through
the shared runner (`src/utils/playbookRunner.ts`) — the same code path as
`POST /api/playbook/run`. The run appears as a `GlobalTaskItem` in the console
task stream with the playbook's stdout/stderr captured.

`--limit` comes from the scope unless `payload.limit` overrides it:

| scope | limit |
| --- | --- |
| `host` | the host name/id |
| `group` | `tag:<tag>` |
| `datacenter` | `all` (or the literal target when it is not `all`) |

### `ansible_steps`

```json
{ "title": "Restart podman socket",
  "steps": [ { "name": "Restart", "module": "ansible.builtin.systemd",
               "parameters": { "name": "podman.socket", "state": "restarted" } } ],
  "become": true, "becomeMethod": "run0" }
```

For every node matched by the scope, ATC builds a real `QueuedAnsibleTask`
(node connection resolved from the live node record), enqueues it into the
Ansible mutation queue and executes it one-by-one with checkpoint halting —
the same runner used by `POST /api/queue/execute`. Results and per-step recap
lines are also mirrored into a `GlobalTaskItem` so the console shows them.

If the scope matches **zero** nodes the run fails loudly
(`No target nodes matched scope ...`). Nothing synthetic is produced.

## Scheduler behaviour

`createCronScheduler` ticks every 15 s (configurable):

1. `listDueSchedules(now)` — enabled rows with `next_run_at <= now`.
2. **Advance `next_run_at` first**, then execute. A crash or restart can never
   re-fire the same slot; a row whose expression can never fire again is parked
   with `next_run_at = NULL`.
3. Record `last_run_at` / `last_run_status` (`RUNNING` → `OK` | `FAILED`),
   `last_run_task_id` and `last_run_error`.
4. Per-schedule in-flight guard prevents overlapping runs of the same schedule.

### Missed runs

Because the next fire time is advanced before execution, a coordinator that was
down across a scheduled minute simply skips it: the next tick sees
`next_run_at <= now`, fires once, and advances to the following slot. Schedule
state is never fabricated.

## HTTP API

| method | path | purpose |
| --- | --- | --- |
| GET | `/api/cron/schedules` | list schedules + `schedulerRunning` |
| GET | `/api/cron/schedules/:id` | one schedule |
| POST | `/api/cron/schedules` | create |
| PUT | `/api/cron/schedules/:id` | edit / enable / disable |
| DELETE | `/api/cron/schedules/:id` | delete |
| POST | `/api/cron/schedules/:id/run` | fire now (does not move the next-run clock) |
| GET | `/api/cron/preview?expression=...&count=5` | next fire timestamps |

Validation errors return HTTP 400 with `{ success: false, error }`.

## UI

`src/components/CronSchedulesPanel.tsx` renders in the datacenter view
(section id `datacenter-schedules`, sidebar entry *Scheduled Jobs*):

- create / edit / enable / disable / delete
- run now
- last-run time + status (+ error tooltip) and next-run time
- scope picker fed from the live node list (hosts and tags)

## Verification

Deterministic unit tests cover the expression engine, the store (including a
restart by re-binding the same SQLite file), scope resolution, the scheduler
tick semantics, and the executor for both task kinds:

```
node --import tsx --test src/utils/cronExpression.test.ts \
  src/utils/cronScheduleStore.test.ts src/utils/cronScheduler.test.ts \
  src/utils/cronScheduleExecutor.test.ts src/components/CronSchedulesPanel.test.tsx
```

End-to-end acceptance (real `ansible-playbook`, `* * * * *` schedule): the
schedule fires at the minute boundary, the run is recorded `OK` with the
playbook's real output captured, disabling clears `next_run_at` and stops
further fires, and re-enabling recomputes a future `next_run_at`.
