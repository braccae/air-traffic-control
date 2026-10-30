package collections

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCatalogCoversEveryAdvertisedCollection(t *testing.T) {
	required := []string{
		CommunityGeneral,
		AnsiblePosix,
		AnsibleWindows,
		CommunityDocker,
		ContainersPodman,
		CommunityPostgres,
		CommunityMySQL,
		CommunityMongoDB,
		CommunityProxmox,
	}
	names := map[string]bool{}
	for _, entry := range Catalog {
		if entry.Name == "" || entry.Description == "" {
			t.Errorf("catalog entry %q must carry a name and description", entry.Name)
		}
		names[entry.Name] = true
	}
	for _, name := range required {
		if !names[name] {
			t.Errorf("catalog is missing %s", name)
		}
	}
}

func TestCollectionsForPluginMapsDetectedEngines(t *testing.T) {
	cases := []struct {
		plugin  string
		details string
		want    []string
	}{
		{"database/sql", "Engines: postgres", []string{CommunityPostgres}},
		{"database/sql", "Engines: mariadb", []string{CommunityMySQL}},
		{"database/sql", "Engines: mysql, mssql", []string{CommunityMySQL, CommunityGeneral}},
		{"database/nosql", "Engines: mongodb", []string{CommunityMongoDB}},
		{"database/nosql", "Engines: valkey", []string{CommunityGeneral}},
		{"container/podman", "", []string{ContainersPodman}},
		{"container/docker", "", []string{CommunityDocker}},
		{"vm/proxmox", "", []string{CommunityProxmox}},
		{"system/service", "", nil},
		{"database/sql", "no engines here", nil},
	}

	for _, tc := range cases {
		got := CollectionsForPlugin(tc.plugin, tc.details)
		if len(got) != len(tc.want) {
			t.Fatalf("CollectionsForPlugin(%q, %q) = %v, want %v", tc.plugin, tc.details, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("CollectionsForPlugin(%q, %q) = %v, want %v", tc.plugin, tc.details, got, tc.want)
			}
		}
	}
}

func TestParseEnginesIgnoresTrailingContent(t *testing.T) {
	got := parseEngines("Engines: postgres, mariadb\nAdditional notes")
	if len(got) != 2 || got[0] != "postgres" || got[1] != "mariadb" {
		t.Fatalf("parseEngines = %v", got)
	}
	if got := parseEngines("no engine information"); got != nil {
		t.Fatalf("parseEngines must return nil without an Engines line, got %v", got)
	}
}

func TestCollectionsForPluginNeverDuplicatesBaseline(t *testing.T) {
	got := CollectionsForPlugin("database/sql", "Engines: postgres, postgresql")
	if len(got) != 1 || got[0] != CommunityPostgres {
		t.Fatalf("duplicate engines must collapse to one collection, got %v", got)
	}
}

// fakeStore is an in-memory StateStore.
type fakeStore struct {
	values map[string]string
}

func (f *fakeStore) Value(pageID, field string) (string, bool) {
	raw, ok := f.values[pageID+"."+field]
	if !ok {
		return "", false
	}
	// Mirror the real settings store: decode the stored JSON and render scalars.
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw, true
	}
	if str, ok := decoded.(string); ok {
		return str, true
	}
	return fmt.Sprint(decoded), true
}

func (f *fakeStore) Save(pageID string, values map[string]any) error {
	for field, raw := range values {
		b, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		f.values[pageID+"."+field] = string(b)
	}
	return nil
}

// fakeGalaxy answers the two galaxy invocations the manager makes.
func fakeGalaxy(installed []string, calls *[][]string) func(ctx context.Context, args ...string) (string, error) {
	return func(_ context.Context, args ...string) (string, error) {
		if calls != nil {
			*calls = append(*calls, args)
		}
		if len(args) >= 2 && args[0] == "collection" && args[1] == "list" {
			payload := map[string]map[string]struct {
				Version string `json:"version"`
			}{}
			for _, name := range installed {
				if payload["/tmp/collections/ansible_collections"] == nil {
					payload["/tmp/collections/ansible_collections"] = map[string]struct {
						Version string `json:"version"`
					}{}
				}
				payload["/tmp/collections/ansible_collections"][name] = struct {
					Version string `json:"version"`
				}{Version: "1.0.0"}
			}
			b, _ := json.Marshal(payload)
			return string(b), nil
		}
		return "installed", nil
	}
}

func TestEnsureSkipsDisabledCollections(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	m := New(t.TempDir())
	m.SetStateStore(store)

	var calls [][]string
	m.SetGalaxyRunner(fakeGalaxy([]string{CommunityGeneral}, &calls))

	if err := m.SetDisabled(CommunityMySQL, true); err != nil {
		t.Fatal(err)
	}
	installed := m.Ensure(context.Background(), []string{CommunityGeneral, CommunityMySQL}, "test")

	if len(installed) != 0 {
		t.Fatalf("nothing should be installed (general present, mysql disabled): %v", installed)
	}
	for _, args := range calls {
		if len(args) >= 3 && args[1] == "install" && args[2] == CommunityMySQL {
			t.Fatalf("disabled collection must never be installed, calls: %v", calls)
		}
	}
}

func TestEnsureCatalogHonoursOptOuts(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	m := New(t.TempDir())
	m.SetStateStore(store)

	var installs []string
	m.SetGalaxyRunner(func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 3 && args[0] == "collection" && args[1] == "install" {
			installs = append(installs, args[2])
		}
		if len(args) >= 2 && args[1] == "list" {
			return `{"path":{"ansible.builtin":{"version":"2.21.0"}}}`, nil
		}
		return "", nil
	})

	if err := m.SetDisabled(CommunityMongoDB, true); err != nil {
		t.Fatal(err)
	}
	m.EnsureCatalog(context.Background())

	for _, name := range installs {
		if name == CommunityMongoDB {
			t.Fatalf("opt-out was ignored by the catalog pass: %v", installs)
		}
	}
	if len(installs) == 0 {
		t.Fatal("expected the remaining catalog entries to be installed")
	}
}

func TestOptOutSurvivesANewManagerOverTheSameStore(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	first := New(t.TempDir())
	first.SetStateStore(store)
	if err := first.SetDisabled(CommunityDocker, true); err != nil {
		t.Fatal(err)
	}

	second := New(t.TempDir())
	second.SetStateStore(store)
	if !second.IsDisabled(CommunityDocker) {
		t.Fatal("the opt-out must be read back from the state store")
	}

	if err := second.SetDisabled(CommunityDocker, false); err != nil {
		t.Fatal(err)
	}
	if second.IsDisabled(CommunityDocker) {
		t.Fatal("enabling must clear the opt-out")
	}
}

func TestRequiredCollectionsGuarded(t *testing.T) {
	for _, name := range []string{CommunityGeneral, AnsiblePosix} {
		if !Required(name) {
			t.Errorf("%s must be treated as required", name)
		}
	}
	for _, name := range []string{CommunityMySQL, CommunityMongoDB, ContainersPodman} {
		if Required(name) {
			t.Errorf("%s must be removable", name)
		}
	}
}

func TestUpdateUsesUpgradeFlag(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	m := New(t.TempDir())
	m.SetStateStore(store)

	var installArgs []string
	m.SetGalaxyRunner(func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[1] == "list" {
			return `{"path":{"community.general":{"version":"13.4.0"}}}`, nil
		}
		installArgs = append(installArgs, strings.Join(args, " "))
		return "upgraded to 13.5.0", nil
	})

	results := m.Update(context.Background(), []string{CommunityGeneral})
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("unexpected update result: %+v", results)
	}
	joined := strings.Join(installArgs, " | ")
	if !strings.Contains(joined, "collection install --upgrade community.general") {
		t.Fatalf("update must use --upgrade, got %q", joined)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"13.5.0", "13.4.0", 1},
		{"13.4.0", "13.5.0", -1},
		{"13.5.0", "13.5.0", 0},
		{"v2.2.2", "2.2.2", 0},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0", "1.0.1", -1},
		{"1.10.0", "1.9.9", 1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestPinFreezesUpdates(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	m := New(t.TempDir())
	m.SetStateStore(store)

	var calls [][]string
	m.SetGalaxyRunner(func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, args)
		if len(args) >= 2 && args[1] == "list" {
			return `{"path":{"community.general":{"version":"13.5.0"}}}`, nil
		}
		return "ok", nil
	})

	version, err := m.Pin(context.Background(), CommunityGeneral, "")
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if version != "13.5.0" {
		t.Fatalf("Pin without a version must pin the installed version, got %q", version)
	}

	calls = nil
	results := m.Update(context.Background(), []string{CommunityGeneral})
	if len(results) != 1 || results[0].Skipped == "" {
		t.Fatalf("pinned collection must be skipped, got %+v", results)
	}
	for _, args := range calls {
		if len(args) >= 2 && args[1] == "install" {
			t.Fatalf("pinned collection must not be upgraded: %v", args)
		}
	}
}

func TestEnsureRestoresPinnedVersionOnDrift(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	m := New(t.TempDir())
	m.SetStateStore(store)

	var installed []string
	m.SetGalaxyRunner(func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[1] == "list" {
			return `{"path":{"community.general":{"version":"13.4.0"}}}`, nil
		}
		if len(args) >= 3 && args[1] == "install" {
			installed = append(installed, strings.Join(args, " "))
		}
		return "", nil
	})

	if _, err := m.Pin(context.Background(), CommunityGeneral, "13.5.0"); err != nil {
		t.Fatal(err)
	}
	m.Ensure(context.Background(), []string{CommunityGeneral}, "test")

	joined := strings.Join(installed, " | ")
	if !strings.Contains(joined, "install community.general:13.5.0 --force") {
		t.Fatalf("a drifted pin must be reinstalled exactly, got %q", joined)
	}
}

// seedState writes a persisted state the way the settings store does: encoded
// as a JSON string under the "state" field.
func seedState(t *testing.T, store *fakeStore, state persistedState) {
	t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := json.Marshal(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	store.values[statePage+".state"] = string(wrapped)
}

func TestAutoUpdateIsOptIn(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	m := New(t.TempDir())
	m.SetStateStore(store)
	m.SetGalaxyRunner(fakeGalaxy(nil, nil))

	if ran, _ := m.MaybeAutoUpdate(context.Background()); ran {
		t.Fatal("auto-update must stay off by default")
	}

	if err := m.SetAutoUpdate(true); err != nil {
		t.Fatal(err)
	}
	if ran, _ := m.MaybeAutoUpdate(context.Background()); ran {
		t.Fatal("enabling must schedule the first pass a week out, not run immediately")
	}

	// The opt-in survives a restart.
	restarted := New(t.TempDir())
	restarted.SetStateStore(store)
	if !restarted.AutoUpdateEnabled() {
		t.Fatal("auto-update setting must persist")
	}
}

func TestAutoUpdateRunsWhenDue(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	seedState(t, store, persistedState{
		AutoUpdate:        true,
		AutoUpdateLastRun: time.Now().Add(-8 * 24 * time.Hour).UnixMilli(),
	})

	m := New(t.TempDir())
	m.SetStateStore(store)
	m.SetGalaxyRunner(fakeGalaxy([]string{CommunityDocker}, nil))

	ran, results := m.MaybeAutoUpdate(context.Background())
	if !ran {
		t.Fatal("a due weekly pass must run")
	}
	if len(results) == 0 {
		t.Fatal("the pass must report what it checked")
	}
	if ranAgain, _ := m.MaybeAutoUpdate(context.Background()); ranAgain {
		t.Fatal("auto-update must not run twice inside the weekly window")
	}
}

func TestPinnedCollectionsAreExcludedFromAutoUpdate(t *testing.T) {
	store := &fakeStore{values: map[string]string{}}
	seedState(t, store, persistedState{
		AutoUpdate:        true,
		AutoUpdateLastRun: time.Now().Add(-8 * 24 * time.Hour).UnixMilli(),
		Pinned:            map[string]string{CommunityDocker: "5.4.0"},
	})

	m := New(t.TempDir())
	m.SetStateStore(store)

	var installs []string
	m.SetGalaxyRunner(func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[1] == "list" {
			return `{"path":{"community.docker":{"version":"5.4.0"},"community.mysql":{"version":"5.0.2"}}}`, nil
		}
		if len(args) >= 3 && args[1] == "install" {
			installs = append(installs, args[2])
		}
		return "", nil
	})

	m.MaybeAutoUpdate(context.Background())

	for _, name := range installs {
		if strings.HasPrefix(name, CommunityDocker) {
			t.Fatalf("pinned collection must not be auto-upgraded: %v", installs)
		}
	}
}
