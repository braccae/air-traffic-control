package rclone_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct{ responses map[string]string }

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestRclonePlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("mount/rclone")
	if !ok {
		t.Fatal("mount/rclone plugin not registered")
	}
	meta := p.Meta()
	if meta.Kind != plugin.KindMount || meta.Name != "rclone" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows")
	}
}

func TestRclonePlugin_ProbeAndRead(t *testing.T) {
	p, _ := plugin.Get("mount/rclone")
	runner := &mockRunner{responses: map[string]string{
		"command -v rclone":     "/usr/bin/rclone\n",
		"grep -c 'fuse.rclone'": "2\n",
		"rclone listremotes":    "hetzner:\nbackup:\n",
		"grep 'fuse.rclone'":    "/dev/fuse on /mnt/rclone/hetzner type fuse.rclone\n",
	}}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if !det.Detected || det.Status != "2 mount(s)" {
		t.Errorf("unexpected detection: %+v", det)
	}

	reader, ok := p.(plugin.Reader)
	if !ok {
		t.Fatal("expected Reader implementation")
	}
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	m := data.(map[string]any)
	remotes := m["remotes"].([]string)
	if len(remotes) != 2 || remotes[0] != "hetzner" {
		t.Errorf("unexpected remotes: %+v", remotes)
	}
}

func TestRclonePlugin_ProbeUnavailable(t *testing.T) {
	p, _ := plugin.Get("mount/rclone")
	det, err := p.Probe(context.Background(), &mockRunner{responses: map[string]string{}}, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if det.Detected {
		t.Error("expected not detected when rclone missing")
	}
}

func TestRclonePlugin_CompilePolicyVariants(t *testing.T) {
	p, _ := plugin.Get("mount/rclone")
	actor, ok := p.(plugin.Actor)
	if !ok {
		t.Fatal("expected Actor implementation")
	}

	base := map[string]any{
		"name":          "hetzner",
		"configContent": "[hetzner]\ntype = sftp\npass = obscuredvalue",
		"mountTarget":   "/mnt/rclone/hetzner",
	}

	modulesFor := func(policy string) []string {
		opts := map[string]any{}
		for k, v := range base {
			opts[k] = v
		}
		opts["policy"] = policy
		spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "install_remote", opts)
		if err != nil {
			t.Fatalf("compile %s: %v", policy, err)
		}
		var mods []string
		for _, s := range spec.Steps {
			mods = append(mods, s.Module)
		}
		return mods
	}

	if got := modulesFor("fstab"); !contains(got, "ansible.posix.mount") {
		t.Errorf("fstab policy should use ansible.posix.mount, got %v", got)
	}
	if got := modulesFor("systemd-mount"); !contains(got, "ansible.builtin.systemd") {
		t.Errorf("systemd-mount should use ansible.builtin.systemd, got %v", got)
	}
	if got := modulesFor("script"); !contains(got, "ansible.builtin.copy") {
		t.Errorf("script policy should copy a script, got %v", got)
	}

	// Measured difference: the three policies must not compile to the same task.
	a := strings.Join(modulesFor("systemd-mount"), ",")
	b := strings.Join(modulesFor("fstab"), ",")
	c := strings.Join(modulesFor("script"), ",")
	if a == b || b == c {
		t.Errorf("policies compiled identically: systemd=%s fstab=%s script=%s", a, b, c)
	}
}

func TestRclonePlugin_CompileMasksSecretsAndRejectsBadInput(t *testing.T) {
	p, _ := plugin.Get("mount/rclone")
	actor := p.(plugin.Actor)

	spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "install_remote", map[string]any{
		"name":          "hetzner",
		"configContent": "[hetzner]\ntype=sftp\npass=obscuredsecret",
		"mountTarget":   "/mnt/rclone/hetzner",
		"policy":        "systemd-mount",
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if spec.SecretsMasked["configContent"] == "" {
		t.Error("expected configContent to be masked in SecretsMasked")
	}
	// The obscured config is written with no_log so it never reaches Ansible logs.
	foundNoLog := false
	for _, s := range spec.Steps {
		if s.Module == "ansible.builtin.blockinfile" {
			if v, ok := s.Params["no_log"].(bool); !ok || !v {
				t.Error("config write step must set no_log: true")
			} else {
				foundNoLog = true
			}
		}
	}
	if !foundNoLog {
		t.Error("expected a blockinfile step writing the remote config")
	}

	// Unit content must never embed the obscured secret value.
	for _, s := range spec.Steps {
		if s.Module == "ansible.builtin.copy" {
			if content, ok := s.Params["content"].(string); ok {
				if strings.Contains(content, "obscuredsecret") {
					t.Error("unit/script content must not contain remote credentials")
				}
			}
		}
	}

	for _, bad := range []map[string]any{
		{"configContent": "x", "mountTarget": "/mnt/x"},                                 // missing name
		{"name": "bad name!", "configContent": "x", "mountTarget": "/mnt/x"},            // bad name
		{"name": "ok", "configContent": "x", "mountTarget": "/mnt/x", "policy": "nope"}, // bad policy
		{"name": "ok", "configContent": "x"},                                            // missing target
	} {
		if _, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "install_remote", bad); err == nil {
			t.Errorf("expected error for %+v", bad)
		}
	}
}

func contains(list []string, needle string) bool {
	for _, s := range list {
		if s == needle {
			return true
		}
	}
	return false
}
