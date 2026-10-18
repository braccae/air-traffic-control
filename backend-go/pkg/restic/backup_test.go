package restic

import (
	"strings"
	"testing"

	"prox-orch/backend/pkg/ssh"
)

type mockSSHRunner struct {
	lastCmd string
	output  string
	err     error
}

func (m *mockSSHRunner) RunCommand(cfg ssh.Config, cmd string) (string, error) {
	m.lastCmd = cmd
	return m.output, m.err
}

func TestListSnapshots_ParsesResticJSON(t *testing.T) {
	runner := &mockSSHRunner{
		output: `[{"time":"2025-05-14T10:04:12Z","id":"f7a91b2e00112233","short_id":"f7a91b2e","hostname":"the-freezer.homelab","paths":["/etc","/tank0/appdata"],"tags":["gold","daily"]}]`,
	}
	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"}, "s3://restic-homelab-vault/the-freezer")
	snaps, err := mgr.ListSnapshots("secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snaps) != 1 || snaps[0].ShortID != "f7a91b2e" || snaps[0].Hostname != "the-freezer.homelab" {
		t.Errorf("unexpected snapshots: %+v", snaps)
	}
}

func TestCreateSnapshotAndIntegrityCheck(t *testing.T) {
	runner := &mockSSHRunner{output: `{"message_type":"summary","snapshot_id":"a492f1b0"}`}
	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"}, "s3://restic-homelab-vault/the-freezer")
	out, err := mgr.CreateSnapshot("secret", []string{"/etc"}, []string{"gold"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "a492f1b0") || !strings.Contains(runner.lastCmd, "--tag gold") {
		t.Errorf("unexpected create snapshot result: cmd=%q out=%q", runner.lastCmd, out)
	}

	runner.output = "no errors were found"
	checkOut, err := mgr.IntegrityCheck("secret")
	if err != nil || checkOut != "no errors were found" {
		t.Errorf("unexpected integrity check result: %v, %q", err, checkOut)
	}
}

