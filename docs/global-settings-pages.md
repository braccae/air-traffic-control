# Addon-module Global Settings Pages

This is the reference for contributing a **datacenter-wide ("global") settings
page** from an addon module (a Go plugin registered in `backend-go/pkg/plugin`).
A module declares a page schema; the framework owns persistence, rendering, and
sidebar registration, so a module ships **no HTML of its own** for a settings
page.

## 1. The Go interface

A module contributes a settings page by implementing
`plugin.SettingsPageProvider` (in addition to `plugin.Plugin`):

```go
// backend-go/pkg/plugin/settings.go
type SettingsPageProvider interface {
    SettingsPage() SettingsPage
}

type SettingsPage struct {
    ID          string          // route slug, unique across modules (convention: "<kind>/<name>")
    Title       string
    Icon        string          // Material Symbols name
    Description string
    Order       int             // sidebar ordering (lower first)
    Fields      []SettingsField
}

type SettingsField struct {
    Name        string
    Label       string
    Type        SettingsFieldType // string | text | int | bool | select | secret
    Description string
    Placeholder string
    Default     any
    Required    bool
    Secret      bool
    Options     []SettingsFieldOption // for "select"
    Min, Max    *int                  // for "int"
}
```

Registering the plugin (`plugin.Register(New())`) is all that is required: the
`Registry` detects the interface and indexes the page. Pages whose `ID` is empty
or already taken are rejected at registration time.

## 2. Persistence and the HTTP API

Values are persisted by the framework in `data/hosts-cache.sqlite`
(table `global_settings`, `PRAGMA journal_mode = WAL`, `synchronous = NORMAL`).
The resident daemon mounts these endpoints:

| Method | Path | Purpose |
|---|---|---|
| `GET`  | `/api/settings/pages`      | list every registered page (schema + current values) |
| `GET`  | `/api/settings/pages/<id>` | one page (schema + current values); `<id>` may contain `/` |
| `PUT`  | `/api/settings/pages/<id>` | validate + persist `{ "values": { ... } }`, return the re-read page |

Incoming values are validated against the field schema: unknown fields,
missing required fields, non-integer `int` fields, and out-of-enum `select`
values are rejected with HTTP 400. This is the **Zero-Mock** surface: the API
only ever echoes values it has actually read back from SQLite, and a page never
fabricates state.

`NewAPI` auto-wires a `SettingsStore` (and publishes it process-wide via
`SetGlobalSettingsStore`) so a resident module can read its own persisted values
during `Read`/`Compile` via `plugin.GlobalSettingsStore().Value(pageID, field)`.

## 3. Frontend rendering

No per-page frontend code is needed:

- `src/components/GlobalSettingsPanel.tsx` renders any `GlobalSettingsPage` from
  its field schema (string/text/int/bool/select/secret) and PUTs on save.
- `src/components/ServerViewSidebar.tsx` fetches `/api/settings/pages` and lists
  each page under the **Module Settings** group in the Datacenter sidebar.
- `src/components/DatacenterGlobalView.tsx` renders each page as its own section
  (`id="datacenter-<sectionId>"`), so selecting the sidebar entry scrolls to it.
- Section ids are derived deterministically by
  `settingsSectionId(pageId)` in `src/utils/globalSettingsManager.ts`
  (`"backup/restic"` → `"settings-backup-restic"`).

## 4. Adding a settings page — three steps

1. Implement `plugin.Plugin` **and** `SettingsPageProvider` on your plugin type.
2. Ensure the plugin is blank-imported in `backend-go/plugins/all/all.go`.
3. That's it — the page appears in the Datacenter Settings sidebar at runtime.

Values are available to the module itself through
`plugin.GlobalSettingsStore().Value(<pageID>, "<field>")`.

## 5. Worked example

`backend-go/plugins/backup/restic/restic.go` is a real module using the
framework. It contributes the **Restic Backup Defaults** page
(`ID = "backup/restic"`, fields: default repository, daily/weekly/monthly
retention, and a secret vault-variable reference). Its `Read` method consults
the persisted `repoPath` default when the host has no per-host override, so the
setting has a real operational effect rather than being inert config.

## 6. Tests

- `backend-go/pkg/plugin/settings_test.go` — registry indexing, duplicate/empty
  page rejection, store round-trip, and the GET/PUT handlers (including
  validation failures).
- `src/utils/globalSettingsManager.test.ts` — section-id mapping and fetch/save
  behaviour.

Run the standard verification suite (see AGENTS.md §11) after touching either
side.
