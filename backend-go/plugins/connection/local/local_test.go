package local_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	localplugin "air-traffic-control/backend/plugins/connection/local"
)

type fakeRunner struct {
	lastCmd string
	output  string
	err     error
}

func (f *fakeRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	f.lastCmd = cmd
	return f.output, f.err
}

func TestLocalPlugin_MetaAndCompatibility(t *testing.T) {
	p, ok := plugin.Get("connection/local")
	if !ok {
		t.Fatal("expected connection/local to be registered in default registry")
	}
	meta := p.Meta()

	if meta.ID != "connection/local" {
		t.Errorf("expected ID 'connection/local', got %q", meta.ID)
	}
	if meta.Kind != plugin.KindConnection {
		t.Errorf("expected Kind 'connection', got %q", meta.Kind)
	}
	if meta.Name != "local" || meta.Order != 1 {
		t.Errorf("unexpected meta: %+v", meta)
	}

	// Should be compatible when HostRoute is 127.0.0.1, localhost, ::1, empty, or ActiveConnection is local
	if !p.Compatible(plugin.HostFacts{HostRoute: "127.0.0.1"}) {
		t.Errorf("expected local plugin to be compatible with 127.0.0.1")
	}
	if !p.Compatible(plugin.HostFacts{HostRoute: "localhost"}) {
		t.Errorf("expected local plugin to be compatible with localhost")
	}
	if !p.Compatible(plugin.HostFacts{HostRoute: "::1"}) {
		t.Errorf("expected local plugin to be compatible with ::1")
	}
	if !p.Compatible(plugin.HostFacts{HostRoute: ""}) {
		t.Errorf("expected local plugin to be compatible with empty hostRoute")
	}
	if !p.Compatible(plugin.HostFacts{ActiveConnection: "connection/local", HostRoute: "192.168.1.50"}) {
		t.Errorf("expected local plugin to be compatible with activeConnection connection/local")
	}
	if p.Compatible(plugin.HostFacts{HostRoute: "192.168.1.50"}) {
		t.Errorf("expected local plugin to be incompatible with remote host without explicit connection/local")
	}
	if p.Compatible(plugin.HostFacts{HostRoute: "127.0.0.1", ActiveConnection: "connection/ssh"}) {
		t.Errorf("expected local plugin to be incompatible when ActiveConnection is connection/ssh")
	}

	reqs := p.Requirements()
	if len(reqs.ControllerCollections) == 0 || reqs.ControllerCollections[0] != "ansible.builtin" {
		t.Errorf("unexpected requirements: %+v", reqs)
	}
}

func TestLocalPlugin_RunCommandProbeAndDialer(t *testing.T) {
	p := localplugin.New()
	facts := plugin.HostFacts{
		NodeID:    "controller",
		OSFamily:  "linux",
		HostRoute: "127.0.0.1",
	}

	runner, err := p.NewRunner(facts)
	if err != nil {
		t.Fatalf("failed to create local runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runner.RunCommand(ctx, "echo test-local-output")
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !strings.Contains(out, "test-local-output") {
		t.Errorf("unexpected output: %q", out)
	}

	// Probe success
	detection, err := p.Probe(ctx, runner, facts)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !detection.Detected || detection.Status != "ONLINE" || detection.Details == "" {
		t.Errorf("expected detected and ONLINE, got %+v", detection)
	}

	// Probe failure on fake runner error
	errRunner := &fakeRunner{err: errors.New("exec failure")}
	detErr, err := p.Probe(ctx, errRunner, facts)
	if err != nil {
		t.Fatalf("unexpected error from Probe: %v", err)
	}
	if detErr.Detected || detErr.Status != "ERROR" {
		t.Errorf("expected ERROR detection on runner failure, got %+v", detErr)
	}

	// Dialer
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	conn, err := p.Dial(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	_ = conn.Close()
}

func TestLocalPlugin_Read(t *testing.T) {
	p := localplugin.New()
	ctx := context.Background()
	facts := plugin.HostFacts{
		NodeID:    "controller",
		OSFamily:  "linux",
		HostRoute: "127.0.0.1",
	}

	r := &fakeRunner{output: "Linux controller 6.18.0 x86_64\n"}
	got, err := p.Read(ctx, r, facts)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !strings.Contains(r.lastCmd, "uname") {
		t.Errorf("expected uname command, got %q", r.lastCmd)
	}

	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if m["plugin"] != "connection/local" || m["transport"] != "local" || m["status"] != "ONLINE" {
		t.Errorf("unexpected read payload: %+v", m)
	}
	connMap, ok := m["connection"].(map[string]any)
	if !ok || connMap["uname"] != "Linux controller 6.18.0 x86_64" {
		t.Errorf("unexpected connection map in read payload: %+v", m["connection"])
	}

	// Error path
	rErr := &fakeRunner{err: errors.New("uname error")}
	if _, err := p.Read(ctx, rErr, facts); err == nil {
		t.Fatal("expected error when runner fails during Read")
	}
}

func TestLocalPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("connection/local")
	if !ok {
		t.Fatal("connection/local not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("connection/local does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("expected 60s collection schedule interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "connection" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("connection/local does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "connection/local" || catPage.Category != "connection" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("connection/local"); !ok {
		t.Error("expected connection/local categorized settings page to be registered in global registry")
	}
	if got := localplugin.EffectiveShell(); got != "sh" {
		t.Errorf("EffectiveShell() = %q, want %q", got, "sh")
	}
	if got := localplugin.EffectiveTimeout(); got != 30*time.Second {
		t.Errorf("EffectiveTimeout() = %v, want 30s", got)
	}

	// 3. ManagementPlugin
	mp, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("connection/local does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mp.ManagementActions()
	if len(mgmtActions) != 2 {
		t.Fatalf("expected 2 management actions, got %d", len(mgmtActions))
	}
	if len(mp.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d != ManagementActions() length %d", len(mp.Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "controller", OSFamily: "linux", HostRoute: "127.0.0.1"}
	if err := mp.ValidateAction(facts, "ping", map[string]any{"data": "pong"}); err != nil {
		t.Errorf("expected valid ping action, got: %v", err)
	}
	if err := mp.ValidateAction(facts, "gather_facts", map[string]any{"filter": "ansible_os_family"}); err != nil {
		t.Errorf("expected valid gather_facts action, got: %v", err)
	}
	if err := mp.ValidateAction(facts, "unknown", map[string]any{}); err == nil {
		t.Error("expected error for unsupported action")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("connection/local does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 2 {
		t.Fatalf("expected 2 context menu entries for compatible local host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "connection/local" || entry.Target != newapi.TargetNode {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if incompat := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{HostRoute: "192.168.1.50"}); len(incompat) != 0 {
		t.Errorf("expected no context menu entries for incompatible remote host, got %+v", incompat)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("connection/local does not implement newapi.TaskCompileProvider")
	}
	spec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-local-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "controller",
		PluginID: "connection/local",
		Action:   "ping",
		Params: map[string]any{
			"data":                          "pong",
			localplugin.FieldEnvironmentVaultVar: "vault_local_env",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask ping failed: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "ansible.builtin.ping" {
		t.Fatalf("unexpected compiled steps: %+v", spec.Steps)
	}
	if spec.Steps[0].Params["data"] != "pong" || spec.Steps[0].Params["connection"] != "local" {
		t.Errorf("unexpected ping step params: %+v", spec.Steps[0].Params)
	}
	if spec.SecretsMasked[localplugin.FieldEnvironmentVaultVar] != plugin.MaskedSecretValue("vault_local_env") {
		t.Errorf("expected masked secret in TaskSpec.SecretsMasked, got %+v", spec.SecretsMasked)
	}

	factsSpec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-local-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "controller",
		PluginID: "connection/local",
		Params:   map[string]any{"action": "gather_facts", "filter": "ansible_distribution*"},
	})
	if err != nil {
		t.Fatalf("CompileTask gather_facts failed: %v", err)
	}
	if len(factsSpec.Steps) != 1 || factsSpec.Steps[0].Module != "ansible.builtin.setup" {
		t.Fatalf("unexpected gather_facts steps: %+v", factsSpec.Steps)
	}
	if factsSpec.Steps[0].Params["filter"] != "ansible_distribution*" {
		t.Errorf("unexpected gather_facts filter: %+v", factsSpec.Steps[0].Params)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{}); err == nil {
		t.Error("expected error when action is empty in CompileTask")
	}
}
