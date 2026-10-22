package ssh_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	sshplugin "air-traffic-control/backend/plugins/connection/ssh"
)

type fakeSSHRunner struct {
	output string
	err    error
}

func (f *fakeSSHRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.output, nil
}

// startMockSSHServer starts an in-memory SSH server for testing connection plugin.
func startMockSSHServer(t *testing.T, noClientAuth bool) (string, func()) {
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
		NoClientAuth: noClientAuth,
	}
	if !noClientAuth {
		srvConfig.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("invalid password")
		}
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
								cmd := string(r.Payload[4:]) // 4 bytes uint32 length prefix
								r.Reply(true, nil)
								if strings.Contains(cmd, "echo") {
									channel.Write([]byte("echo-output\n"))
								} else if strings.Contains(cmd, "ssh -V") {
									channel.Write([]byte("OpenSSH_9.6p1, OpenSSL 3.2.1\n"))
								} else {
									channel.Write([]byte("mock-output\n"))
								}
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

func TestSSHPlugin_MetaAndCompatibility(t *testing.T) {
	p := sshplugin.New()

	meta := p.Meta()
	if meta.ID != "connection/ssh" {
		t.Errorf("expected ID 'connection/ssh', got %q", meta.ID)
	}
	if meta.Kind != plugin.KindConnection {
		t.Errorf("expected Kind 'connection', got %q", meta.Kind)
	}

	// Should be compatible with linux, freebsd, etc. when ActiveConnection matches or not restricted
	linuxFacts := plugin.HostFacts{OSFamily: "linux", HostRoute: "192.168.1.100"}
	if !p.Compatible(linuxFacts) {
		t.Errorf("expected SSH plugin to be compatible with linux facts")
	}

	// Incompatible if ActiveConnection explicitly set to something else e.g. "connection/winrm"
	winrmFacts := plugin.HostFacts{OSFamily: "windows", ActiveConnection: "connection/winrm"}
	if p.Compatible(winrmFacts) {
		t.Errorf("expected SSH plugin to be incompatible when ActiveConnection is connection/winrm")
	}
}

func TestSSHPlugin_ProbeAndRunCommand(t *testing.T) {
	addr, cleanup := startMockSSHServer(t, false)
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	p := sshplugin.New()

	facts := plugin.HostFacts{
		NodeID:    "test-node",
		OSFamily:  "linux",
		HostRoute: host,
		Overrides: map[string]string{
			"port":     portStr,
			"user":     "testuser",
			"password": "secret",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Direct runner retrieval or through probe
	runner, err := p.NewRunner(facts)
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}

	out, err := runner.RunCommand(ctx, "echo hello")
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !strings.Contains(out, "echo-output") {
		t.Errorf("expected 'echo-output', got %q", out)
	}

	// Probe
	detection, err := p.Probe(ctx, runner, facts)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !detection.Detected {
		t.Errorf("expected probe to detect SSH connection, got %+v", detection)
	}
}

func TestSSHPlugin_Dialer(t *testing.T) {
	addr, cleanup := startMockSSHServer(t, true)
	defer cleanup()

	p := sshplugin.New()
	dialer, ok := any(p).(plugin.Dialer)
	if !ok {
		t.Fatalf("expected SSH plugin to implement plugin.Dialer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dialer.Dial(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	if conn.RemoteAddr().String() != addr {
		t.Errorf("expected remote addr %q, got %q", addr, conn.RemoteAddr().String())
	}
}

func TestSSHPlugin_ReadAndCompile(t *testing.T) {
	p := sshplugin.New()
	facts := plugin.HostFacts{
		NodeID:    "node-ssh-1",
		OSFamily:  "linux",
		HostRoute: "192.168.1.50",
		Overrides: map[string]string{
			"user": "deploy",
			"port": "2222",
		},
	}

	// Nil runner -> error
	if _, err := p.Read(context.Background(), nil, facts); err == nil {
		t.Fatal("expected error when Read is called with nil runner")
	}

	// Failing runner -> error
	if _, err := p.Read(context.Background(), &fakeSSHRunner{err: errors.New("connection reset")}, facts); err == nil {
		t.Fatal("expected error when runner fails")
	}

	// Successful Read
	readData, err := p.Read(context.Background(), &fakeSSHRunner{output: "OpenSSH_9.6p1\n"}, facts)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	payload, ok := readData.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any from Read, got %T", readData)
	}
	sshInfo, ok := payload["ssh"].(map[string]any)
	if !ok {
		t.Fatalf("expected ssh map in payload, got %+v", payload)
	}
	if sshInfo["status"] != "ONLINE" || sshInfo["host"] != "192.168.1.50" || sshInfo["user"] != "deploy" || sshInfo["port"] != 2222 || sshInfo["version"] != "OpenSSH_9.6p1" {
		t.Errorf("unexpected ssh read info: %+v", sshInfo)
	}

	// Compile test_connection
	testSpec, err := p.Compile(facts, "test_connection", map[string]any{"timeout": 15})
	if err != nil {
		t.Fatalf("Compile test_connection failed: %v", err)
	}
	if len(testSpec.Steps) != 1 || testSpec.Steps[0].Module != "ansible.builtin.wait_for_connection" || testSpec.Steps[0].Params["timeout"] != 15 {
		t.Errorf("unexpected test_connection TaskSpec: %+v", testSpec)
	}

	// Compile authorize_key (with secret masking)
	authSpec, err := p.Compile(facts, "authorize_key", map[string]any{
		"user":       "deploy",
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey deploy@atc",
		"state":      "present",
	})
	if err != nil {
		t.Fatalf("Compile authorize_key failed: %v", err)
	}
	if len(authSpec.Steps) != 1 || authSpec.Steps[0].Module != "ansible.builtin.lineinfile" {
		t.Errorf("unexpected authorize_key TaskSpec: %+v", authSpec)
	}
	if authSpec.SecretsMasked["public_key"] == "" {
		t.Errorf("expected public_key to be masked in TaskSpec.SecretsMasked, got %+v", authSpec.SecretsMasked)
	}

	// Compile restart_sshd
	restartSpec, err := p.Compile(facts, "restart_sshd", map[string]any{"service": "sshd"})
	if err != nil {
		t.Fatalf("Compile restart_sshd failed: %v", err)
	}
	if len(restartSpec.Steps) != 1 || restartSpec.Steps[0].Module != "ansible.builtin.service" || restartSpec.Steps[0].Params["name"] != "sshd" {
		t.Errorf("unexpected restart_sshd TaskSpec: %+v", restartSpec)
	}
}

func TestSSHPlugin_NewAPICompliance(t *testing.T) {
	p, ok := plugin.Get("connection/ssh")
	if !ok {
		t.Fatalf("connection/ssh not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("connection/ssh does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("unexpected collection schedule for connection/ssh: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "ssh" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("connection/ssh does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 3 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 3 management actions matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	for i, ma := range mgmtActions {
		if ma.Name != legacyActions[i].Name {
			t.Errorf("action[%d] name mismatch: %q vs %q", i, ma.Name, legacyActions[i].Name)
		}
		if !ma.Idempotent || len(ma.InvalidatesKinds) == 0 || ma.InvalidatesKinds[0] != plugin.KindConnection {
			t.Errorf("unexpected management metadata for %q: %+v", ma.Name, ma)
		}
		if ma.Name == "restart_sshd" && (!ma.Dangerous || ma.ConfirmPrompt == "") {
			t.Errorf("expected restart_sshd to be marked dangerous with a confirmation prompt: %+v", ma)
		}
	}

	facts := plugin.HostFacts{NodeID: "node-ssh-1", OSFamily: "linux", HostRoute: "192.168.1.50"}
	if err := mgmt.ValidateAction(facts, "test_connection", map[string]any{"timeout": 10}); err != nil {
		t.Errorf("expected valid test_connection options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "authorize_key", map[string]any{
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey",
		"state":      "present",
	}); err != nil {
		t.Errorf("expected valid authorize_key options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "authorize_key", map[string]any{}); err == nil {
		t.Errorf("expected missing required public_key option for authorize_key to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "authorize_key", map[string]any{
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey",
		"state":      "invalid",
	}); err == nil {
		t.Errorf("expected invalid enum state option for authorize_key to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("connection/ssh does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for connection/ssh settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for connection/ssh: %v", err)
	}
	if catPage.ID != sshplugin.SettingsPageID || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "connection" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.GetCategorizedSettingsPage(sshplugin.SettingsPageID); !ok {
		t.Errorf("expected connection/ssh categorized settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("connection/ssh does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible host, got %d (%+v)", len(entries), entries)
	}
	for _, entry := range entries {
		if entry.PluginID != "connection/ssh" || entry.Target != newapi.TargetNode || entry.Action == "" {
			t.Errorf("unexpected context menu entry metadata: %+v", entry)
		}
	}
	if winrmEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{
		OSFamily:         "windows",
		ActiveConnection: "connection/winrm",
	}); len(winrmEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries when ActiveConnection is connection/winrm, got %+v", winrmEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("connection/ssh does not implement newapi.TaskCompileProvider")
	}
	compiledTest, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "connection/ssh",
		Action:   "test_connection",
		NodeID:   "node-ssh-1",
		Params: map[string]any{
			"timeout": 12,
		},
	})
	if err != nil {
		t.Fatalf("CompileTask test_connection failed: %v", err)
	}
	if len(compiledTest.Steps) != 1 || compiledTest.Steps[0].Module != "ansible.builtin.wait_for_connection" || compiledTest.Steps[0].Params["timeout"] != 12 {
		t.Errorf("unexpected CompileTask test_connection result: %+v", compiledTest)
	}

	compiledViaParam, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "connection/ssh",
		NodeID:   "node-ssh-1",
		Params: map[string]any{
			"action":  "restart_sshd",
			"service": "ssh",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask with action in Params failed: %v", err)
	}
	if len(compiledViaParam.Steps) != 1 || compiledViaParam.Steps[0].Params["name"] != "ssh" {
		t.Errorf("unexpected CompileTask restart_sshd result: %+v", compiledViaParam)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{NodeID: "node-ssh-1"}); err == nil {
		t.Errorf("expected error when CompileTask is called without an action")
	}
}
