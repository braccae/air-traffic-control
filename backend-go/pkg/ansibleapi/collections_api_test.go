package ansibleapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/collections"
)

// memoryState is an in-memory collections.StateStore.
type memoryState struct{ values map[string]string }

func (m *memoryState) Value(pageID, field string) (string, bool) {
	raw, ok := m.values[pageID+"."+field]
	if !ok {
		return "", false
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw, true
	}
	if str, ok := decoded.(string); ok {
		return str, true
	}
	return fmt.Sprint(decoded), true
}

func (m *memoryState) Save(pageID string, values map[string]any) error {
	if m.values == nil {
		m.values = map[string]string{}
	}
	for field, raw := range values {
		b, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		m.values[pageID+"."+field] = string(b)
	}
	return nil
}

func wireCollectionManager(t *testing.T, env *testEnv) *collections.Manager {
	t.Helper()
	mgr := collections.New(env.tempDir)
	mgr.SetStateStore(&memoryState{values: map[string]string{}})
	mgr.SetGalaxyRunner(func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[1] == "list" {
			return `{"path":{"community.general":{"version":"13.5.0"}}}`, nil
		}
		return "ok", nil
	})
	env.api.SetCollectionsManager(mgr)
	return mgr
}

func TestRemovingPipelineCriticalCollectionIsBlocked(t *testing.T) {
	env := setupAnsibleTestEnv(t)
	mgr := wireCollectionManager(t, env)

	status, body := doJSON(t, env.server, "POST", "/api/ansible/collections/remove",
		map[string]any{"collection": collections.CommunityGeneral})

	if status != 409 {
		t.Fatalf("status = %d, want 409 (body %v)", status, body)
	}
	if body["required"] != true {
		t.Fatalf("response must flag the collection as required: %v", body)
	}
	if mgr.IsDisabled(collections.CommunityGeneral) {
		t.Fatal("a refused removal must not record an opt-out")
	}
}

func TestUpdateRejectsCoreCollection(t *testing.T) {
	env := setupAnsibleTestEnv(t)
	wireCollectionManager(t, env)

	status, body := doJSON(t, env.server, "POST", "/api/ansible/collections/update",
		map[string]any{"collection": "ansible.builtin"})
	if status != 400 {
		t.Fatalf("status = %d, want 400 (body %v)", status, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "ansible-core") {
		t.Fatalf("unexpected error message: %v", body["error"])
	}
}

func TestUpdateRequiresTarget(t *testing.T) {
	env := setupAnsibleTestEnv(t)
	wireCollectionManager(t, env)

	status, _ := doJSON(t, env.server, "POST", "/api/ansible/collections/update", map[string]any{})
	if status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestEnableRejectsInvalidName(t *testing.T) {
	env := setupAnsibleTestEnv(t)
	wireCollectionManager(t, env)

	status, _ := doJSON(t, env.server, "POST", "/api/ansible/collections/enable",
		map[string]any{"collection": "not a collection"})
	if status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestCollectionsListReportsDisabledState(t *testing.T) {
	env := setupAnsibleTestEnv(t)
	mgr := wireCollectionManager(t, env)
	if err := mgr.SetDisabled(collections.CommunityMongoDB, true); err != nil {
		t.Fatal(err)
	}

	status, body := doJSON(t, env.server, "GET", "/api/ansible/collections", nil)
	if status != 200 {
		t.Fatalf("status = %d", status)
	}

	raw, ok := body["disabled"].([]any)
	if !ok {
		t.Fatalf("response must carry the disabled list: %v", body)
	}
	found := false
	for _, entry := range raw {
		if entry == collections.CommunityMongoDB {
			found = true
		}
	}
	if !found {
		t.Fatalf("disabled collection missing from response: %v", raw)
	}
}

func TestPinRejectsCoreCollection(t *testing.T) {
	env := setupAnsibleTestEnv(t)
	wireCollectionManager(t, env)

	status, _ := doJSON(t, env.server, "POST", "/api/ansible/collections/pin",
		map[string]any{"collection": "ansible.builtin"})
	if status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestPinAndUnpinRoundTrip(t *testing.T) {
	env := setupAnsibleTestEnv(t)
	mgr := wireCollectionManager(t, env)

	status, body := doJSON(t, env.server, "POST", "/api/ansible/collections/pin",
		map[string]any{"collection": collections.CommunityGeneral})
	if status != 200 || body["success"] != true {
		t.Fatalf("pin failed: %d %v", status, body)
	}
	if version, ok := mgr.PinnedVersion(collections.CommunityGeneral); !ok || version != "13.5.0" {
		t.Fatalf("pin should capture the installed version, got %q (ok=%v)", version, ok)
	}

	status, body = doJSON(t, env.server, "POST", "/api/ansible/collections/unpin",
		map[string]any{"collection": collections.CommunityGeneral})
	if status != 200 || body["success"] != true {
		t.Fatalf("unpin failed: %d %v", status, body)
	}
	if _, ok := mgr.PinnedVersion(collections.CommunityGeneral); ok {
		t.Fatal("unpin must clear the pin")
	}
}

func TestAutoUpdateTogglePersists(t *testing.T) {
	env := setupAnsibleTestEnv(t)
	mgr := wireCollectionManager(t, env)

	status, body := doJSON(t, env.server, "POST", "/api/ansible/collections/auto-update",
		map[string]any{"enabled": true})
	if status != 200 || body["autoUpdate"] != true {
		t.Fatalf("toggle failed: %d %v", status, body)
	}
	if !mgr.AutoUpdateEnabled() {
		t.Fatal("manager must report the setting as enabled")
	}

	status, body = doJSON(t, env.server, "GET", "/api/ansible/collections", nil)
	if status != 200 || body["autoUpdate"] != true {
		t.Fatalf("list endpoint must report autoUpdate, got %v", body)
	}
}
