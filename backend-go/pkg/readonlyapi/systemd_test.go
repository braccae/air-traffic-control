package readonlyapi_test

// End-to-end route tests for the systemd endpoints. The seeded host points at
// 127.0.0.1, so the resolver picks connection/local and the handlers really run
// systemctl/journalctl on this machine. They are read-only; the test skips when
// the host has no systemd.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/readonlyapi"
	_ "air-traffic-control/backend/plugins/all"
)

type unitListBody struct {
	NodeID  string `json:"nodeId"`
	Manager string `json:"manager"`
	Scope   string `json:"scope"`
	Units   []struct {
		Unit          string `json:"unit"`
		Active        string `json:"active"`
		Sub           string `json:"sub"`
		UnitFileState string `json:"unitFileState"`
		Enabled       bool   `json:"enabled"`
		Scope         string `json:"scope"`
	} `json:"units"`
	Warnings []string `json:"warnings"`
}

type unitDetailBody struct {
	NodeID string `json:"nodeId"`
	Scope  string `json:"scope"`
	State  struct {
		Unit          string `json:"unit"`
		LoadState     string `json:"loadState"`
		ActiveState   string `json:"activeState"`
		SubState      string `json:"subState"`
		UnitFileState string `json:"unitFileState"`
		Enabled       bool   `json:"enabled"`
	} `json:"state"`
}

type journalBody struct {
	NodeID  string `json:"nodeId"`
	Unit    string `json:"unit"`
	Count   int    `json:"count"`
	Entries []struct {
		Timestamp string `json:"timestamp"`
		Level     string `json:"level"`
		Unit      string `json:"unit"`
		Message   string `json:"message"`
	} `json:"entries"`
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl not available on this host")
	}

	dbPath := filepath.Join(t.TempDir(), "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.SeedHostsIfEmpty([]hostcache.NodeRecord{{
		ID: "local-node", Name: "local", ShortName: "local",
		Group: "test", IP: "127.0.0.1", Port: 22, SSHUser: "tester",
		Status: "online",
	}}); err != nil {
		t.Fatalf("seed host: %v", err)
	}

	api, err := readonlyapi.New(readonlyapi.Options{Store: store, DBPath: dbPath})
	if err != nil {
		t.Fatalf("readonlyapi.New: %v", err)
	}
	mux := http.NewServeMux()
	api.Register(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	if out != nil && res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return res.StatusCode
}

func TestSystemdRoutesUnitsAndState(t *testing.T) {
	srv := newTestServer(t)

	var list unitListBody
	if code := getJSON(t, srv.URL+"/api/nodes/local-node/systemd/units", &list); code != http.StatusOK {
		t.Fatalf("units status = %d", code)
	}
	if list.Manager != "systemd" || list.Scope != "system" {
		t.Errorf("unexpected envelope: %+v", list)
	}
	if len(list.Units) == 0 {
		t.Fatal("expected systemd service units from the real host")
	}
	for _, u := range list.Units {
		if u.Scope != "system" {
			t.Errorf("unit %s has scope %q", u.Unit, u.Scope)
		}
		if u.Active == "" {
			t.Errorf("unit %s has no active state", u.Unit)
		}
	}

	unit := ""
	for _, u := range list.Units {
		if u.Active == "active" {
			unit = u.Unit
			break
		}
	}
	if unit == "" {
		t.Skip("no active units to inspect")
	}

	var detail unitDetailBody
	if code := getJSON(t, srv.URL+"/api/nodes/local-node/systemd/unit?unit="+unit, &detail); code != http.StatusOK {
		t.Fatalf("unit status = %d", code)
	}
	if detail.State.Unit != unit {
		t.Errorf("unit detail mislabelled: %+v", detail.State)
	}
	if detail.State.ActiveState != "active" || detail.State.LoadState == "" {
		t.Errorf("unexpected unit detail state: %+v", detail.State)
	}
	t.Logf("%s: active=%s sub=%s unitFileState=%s", detail.State.Unit, detail.State.ActiveState, detail.State.SubState, detail.State.UnitFileState)
}

func TestSystemdRouteJournalForUnit(t *testing.T) {
	srv := newTestServer(t)

	// Find a unit that has journal records on this host.
	var all journalBody
	if code := getJSON(t, srv.URL+"/api/nodes/local-node/systemd/journal?lines=200", &all); code != http.StatusOK {
		t.Fatalf("journal status = %d", code)
	}
	unit := ""
	for _, e := range all.Entries {
		if e.Message != "" {
			unit = e.Unit
			break
		}
	}
	if unit == "" {
		t.Skip("host journal has no attributable records in the last 200 lines")
	}

	var body journalBody
	if code := getJSON(t, srv.URL+"/api/nodes/local-node/systemd/journal?unit="+unit+"&lines=10", &body); code != http.StatusOK {
		t.Fatalf("unit journal status = %d", code)
	}
	if body.Count == 0 || len(body.Entries) != body.Count {
		t.Fatalf("journal count mismatch: count=%d entries=%d", body.Count, len(body.Entries))
	}
	for _, e := range body.Entries {
		if e.Timestamp == "" {
			t.Errorf("journal entry without timestamp: %+v", e)
		}
	}
	t.Logf("journal for %s: %d entries, last=%q", unit, body.Count, body.Entries[len(body.Entries)-1].Message)
}

func TestSystemdRouteScopeAllMergesManagers(t *testing.T) {
	srv := newTestServer(t)

	var body unitListBody
	if code := getJSON(t, srv.URL+"/api/nodes/local-node/systemd/units?scope=all", &body); code != http.StatusOK {
		t.Fatalf("scope=all status = %d", code)
	}
	if body.Scope != "all" {
		t.Errorf("expected scope=all envelope, got %q", body.Scope)
	}
	if len(body.Units) == 0 {
		t.Fatal("expected units from the system manager")
	}
	seen := map[string]bool{}
	for _, u := range body.Units {
		seen[u.Scope] = true
	}
	if !seen["system"] {
		t.Error("scope=all must include system units")
	}
	if !seen["user"] {
		t.Log("no user units on this host (user manager may be empty); system units only")
	}
}

func TestSystemdRouteUserScope(t *testing.T) {
	srv := newTestServer(t)

	res, err := http.Get(srv.URL + "/api/nodes/local-node/systemd/units?scope=user")
	if err != nil {
		t.Fatalf("GET user units: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusBadGateway {
		var errBody map[string]any
		_ = json.NewDecoder(res.Body).Decode(&errBody)
		t.Skipf("no reachable user manager on this host: %v", errBody["error"])
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("user units status = %d", res.StatusCode)
	}
	var body unitListBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode user units: %v", err)
	}
	if body.Scope != "user" {
		t.Errorf("expected user scope, got %q", body.Scope)
	}
	for _, u := range body.Units {
		if u.Scope != "user" {
			t.Errorf("unit %s has scope %q", u.Unit, u.Scope)
		}
	}
}

func TestSystemdRouteRejectsBadInput(t *testing.T) {
	srv := newTestServer(t)

	for _, url := range []string{
		srv.URL + "/api/nodes/local-node/systemd/unit",
		srv.URL + "/api/nodes/local-node/systemd/unit?unit=sshd.service%3B%20reboot",
		srv.URL + "/api/nodes/local-node/systemd/units?scope=everything",
		srv.URL + "/api/nodes/local-node/systemd/journal?lines=0",
	} {
		if code := getJSON(t, url, nil); code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", url, code)
		}
	}
}
