package tailscale_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	tsplugin "air-traffic-control/backend/plugins/connection/tailscale"
)

// startMockTailscaleSSHServer starts an in-memory SSH server with NoClientAuth simulating Tailscale SSH
func startMockTailscaleSSHServer(t *testing.T) (string, func()) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	srvConfig := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	srvConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	stopCh := make(chan struct{})
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				select {
				case <-stopCh:
					return
				default:
					return
				}
			}
			go func(conn net.Conn) {
				defer conn.Close()
				sConn, chans, reqs, err := ssh.NewServerConn(conn, srvConfig)
				if err != nil {
					return
				}
				defer sConn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					channel, reqs, err := ch.Accept()
					if err != nil {
						continue
					}
					go func() {
						for r := range reqs {
							if r.Type == "exec" {
								r.Reply(true, nil)
								channel.Write([]byte("tailscale-ssh-success\n"))
								channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
								channel.Close()
								return
							}
							r.Reply(false, nil)
						}
					}()
				}
			}(c)
		}
	}()

	addr := listener.Addr().String()
	cleanup := func() {
		close(stopCh)
		listener.Close()
	}
	return addr, cleanup
}

type mockDialer struct {
	dialFn func(ctx context.Context, network, address string) (net.Conn, error)
}

func (m *mockDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if m.dialFn != nil {
		return m.dialFn(ctx, network, address)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

func TestTailscalePlugin_MetaAndCompatibility(t *testing.T) {
	p := tsplugin.New()
	meta := p.Meta()

	if meta.ID != "connection/tailscale" {
		t.Errorf("expected ID 'connection/tailscale', got %q", meta.ID)
	}
	if meta.Kind != plugin.KindConnection {
		t.Errorf("expected Kind 'connection', got %q", meta.Kind)
	}

	// Compatible with 100.x.y.z addresses or .ts.net hostnames
	if !p.Compatible(plugin.HostFacts{HostRoute: "100.105.71.49"}) {
		t.Errorf("expected compatibility with 100.x.y.z Tailscale IP")
	}
	if !p.Compatible(plugin.HostFacts{HostRoute: "likes-it-cold.leo-banjo.ts.net"}) {
		t.Errorf("expected compatibility with .ts.net domain")
	}
	if !p.Compatible(plugin.HostFacts{ActiveConnection: "connection/tailscale"}) {
		t.Errorf("expected compatibility with activeConnection connection/tailscale")
	}
	if p.Compatible(plugin.HostFacts{HostRoute: "192.168.1.100"}) {
		t.Errorf("expected incompatible with non-Tailscale host by default")
	}
}

func TestTailscalePlugin_RunWithMockDialer(t *testing.T) {
	addr, cleanup := startMockTailscaleSSHServer(t)
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	p := tsplugin.New()
	p.SetDialer(&mockDialer{}) // uses standard dialer to reach 127.0.0.1 mock

	facts := plugin.HostFacts{
		NodeID:           "ts-test-node",
		OSFamily:         "linux",
		HostRoute:        host,
		ActiveConnection: "connection/tailscale",
		Overrides: map[string]string{
			"port": portStr,
			"user": "root",
		},
	}

	runner, err := p.NewRunner(facts)
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runner.RunCommand(ctx, "echo test")
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !strings.Contains(out, "tailscale-ssh-success") {
		t.Errorf("expected 'tailscale-ssh-success', got %q", out)
	}

	detection, err := p.Probe(ctx, runner, facts)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !detection.Detected || detection.Status != "ONLINE" {
		t.Errorf("expected probe ONLINE, got %+v", detection)
	}

	readData, err := p.Read(ctx, runner, facts)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	payload, ok := readData.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any from Read, got %T", readData)
	}
	connInfo, ok := payload["connection"].(map[string]any)
	if !ok || connInfo["plugin"] != "connection/tailscale" {
		t.Fatalf("unexpected Read connection payload: %+v", payload)
	}
}

func TestTailscalePlugin_V2NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("connection/tailscale")
	if !ok {
		t.Fatalf("expected connection/tailscale to be registered in default registry")
	}

	compatFacts := plugin.HostFacts{
		NodeID:           "node-likes-it-cold",
		OSFamily:         "linux",
		HostRoute:        "likes-it-cold.leo-banjo.ts.net",
		ActiveConnection: "connection/tailscale",
	}
	incompatFacts := plugin.HostFacts{
		NodeID:           "node-lan",
		OSFamily:         "linux",
		HostRoute:        "192.168.1.50",
		ActiveConnection: "connection/ssh",
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("expected connection/tailscale to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Fatalf("expected positive schedule Interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "connection" {
		t.Fatalf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("expected connection/tailscale to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 3 {
		t.Fatalf("expected 3 management actions, got %d", len(mgmtActions))
	}
	legacyActions := mgmt.Actions()
	if len(legacyActions) != len(mgmtActions) {
		t.Fatalf("Actions() length %d != ManagementActions() length %d", len(legacyActions), len(mgmtActions))
	}

	if err := mgmt.ValidateAction(compatFacts, "verify_connection", map[string]any{}); err != nil {
		t.Fatalf("expected valid verify_connection options, got: %v", err)
	}
	if err := mgmt.ValidateAction(compatFacts, "enable_tailscale_ssh", map[string]any{"enabled": true}); err != nil {
		t.Fatalf("expected valid enable_tailscale_ssh options, got: %v", err)
	}
	if err := mgmt.ValidateAction(compatFacts, "enable_tailscale_ssh", map[string]any{"enabled": "not-a-bool"}); err == nil {
		t.Fatalf("expected error for non-boolean enabled option")
	}
	if err := mgmt.ValidateAction(compatFacts, "restart_tailscaled", map[string]any{"service": "evil; rm -rf /"}); err == nil {
		t.Fatalf("expected error for invalid service name in restart_tailscaled")
	}
	if err := mgmt.ValidateAction(compatFacts, "unknown_action", map[string]any{}); err == nil {
		t.Fatalf("expected error for unknown action")
	}

	// 3. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("expected connection/tailscale to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(csp.SettingsPage()); err != nil {
		t.Fatalf("settings schema validation failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("categorized settings page validation failed: %v", err)
	}
	if catPage.ID != "connection/tailscale" || catPage.Category != "connection" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Fatalf("unexpected categorized settings page metadata: %+v", catPage)
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("expected connection/tailscale to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), compatFacts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible facts, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "connection/tailscale" || entry.Target != newapi.TargetNode || entry.Action == "" {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if gotIncompat := cmp.ContextMenuEntries(context.Background(), incompatFacts); len(gotIncompat) != 0 {
		t.Fatalf("expected no context menu entries for incompatible facts, got %+v", gotIncompat)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("expected connection/tailscale to implement newapi.TaskCompileProvider")
	}
	pingTask, err := tcp.CompileTask(context.Background(), compatFacts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "connection/tailscale",
		Action:   "verify_connection",
		NodeID:   compatFacts.NodeID,
	})
	if err != nil {
		t.Fatalf("CompileTask(verify_connection) failed: %v", err)
	}
	if len(pingTask.Steps) != 1 || pingTask.Steps[0].Module != "ansible.builtin.ping" {
		t.Fatalf("unexpected verify_connection TaskSpec: %+v", pingTask)
	}

	sshTask, err := tcp.CompileTask(context.Background(), compatFacts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "connection/tailscale",
		Action:   "enable_tailscale_ssh",
		NodeID:   compatFacts.NodeID,
		Params:   map[string]any{"enabled": true},
	})
	if err != nil {
		t.Fatalf("CompileTask(enable_tailscale_ssh) failed: %v", err)
	}
	if len(sshTask.Steps) != 1 || sshTask.Steps[0].Params["cmd"] != "tailscale set --ssh=true" {
		t.Fatalf("unexpected enable_tailscale_ssh TaskSpec: %+v", sshTask)
	}

	restartTask, err := tcp.CompileTask(context.Background(), compatFacts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "connection/tailscale",
		Action:   "restart_tailscaled",
		NodeID:   compatFacts.NodeID,
		Params:   map[string]any{"service": "tailscaled.service"},
	})
	if err != nil {
		t.Fatalf("CompileTask(restart_tailscaled) failed: %v", err)
	}
	if len(restartTask.Steps) != 1 || restartTask.Steps[0].Module != "ansible.builtin.systemd_service" {
		t.Fatalf("unexpected restart_tailscaled TaskSpec: %+v", restartTask)
	}
}
