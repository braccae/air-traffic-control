package share_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct {
	responses map[string]string
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestNFSPlugin_ProbeAndCompile(t *testing.T) {
	p, ok := plugin.Get("share/nfs")
	if !ok {
		t.Fatalf("share/nfs not found")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"cat /var/lib/nfs/etab /etc/exports": "/export/media *(rw,sync)\n/export/data 100.64.0.0/10(rw)",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Status, "2 export") {
		t.Errorf("unexpected nfs probe: %+v", det)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "export", map[string]any{
		"path":   "/srv/nfs",
		"client": "192.168.1.0/24",
	})
	if err != nil {
		t.Fatalf("nfs compile failed: %v", err)
	}
	if len(task.Steps) != 2 || task.Steps[0].Module != "ansible.builtin.lineinfile" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
}

func TestSMBPlugin_ProbeAndCompile(t *testing.T) {
	p, ok := plugin.Get("share/smb")
	if !ok {
		t.Fatalf("share/smb not found")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"testparm": "[global]\nworkgroup = SAMBA\n[media]\npath = /srv/media",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected detected smb")
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "share", map[string]any{
		"name":             "backups",
		"path":             "/mnt/backups",
		"passwordVaultKey": "vault_smb_pwd",
	})
	if err != nil {
		t.Fatalf("smb compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.builtin.template" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.SecretsMasked["smb_password"] != "•••••••• (vault: vault_smb_pwd)" {
		t.Errorf("expected masked secret in task: %+v", task.SecretsMasked)
	}
}
