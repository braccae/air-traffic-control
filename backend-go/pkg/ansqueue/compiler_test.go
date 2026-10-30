package ansqueue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatYAMLParamValue(t *testing.T) {
	cases := []struct {
		name   string
		value  any
		indent int
		want   string
	}{
		{"bool true", true, 10, "true"},
		{"bool false", false, 10, "false"},
		{"int", 22, 10, "22"},
		{"float integral", float64(2), 10, "2"},
		{"float fractional", 1.5, 10, "1.5"},
		{"json number", json.Number("3"), 10, "3"},
		{"plain string", "nginx", 10, "nginx"},
		{"slash is safe", "80/tcp", 10, "80/tcp"},
		{"empty string quoted", "", 10, `""`},
		{"colon quoted", "a: b", 10, `"a: b"`},
		{"hash quoted", "a #b", 10, `"a #b"`},
		{"brace quoted", "a{b", 10, `"a{b"`},
		{"bracket quoted", "a]b", 10, `"a]b"`},
		{"ampersand quoted", "a&b", 10, `"a&b"`},
		{"percent quoted", "100%", 10, `"100%"`},
		{"backtick quoted", "a`b", 10, "\"a`b\""},
		{"keyword true", "true", 10, `"true"`},
		{"keyword No", "No", 10, `"No"`},
		{"keyword null", "null", 10, `"null"`},
		{
			"multiline block scalar",
			"line1\nline2",
			10,
			"|\n            line1\n            line2",
		},
		{
			"empty list",
			[]any{},
			8,
			"[]",
		},
		{
			"string list",
			[]any{"a", "b"},
			8,
			"\n        - a\n        - b",
		},
		{
			"empty map",
			map[string]any{},
			8,
			"{}",
		},
		{
			"flat map sorted",
			map[string]any{"state": "restarted", "name": "nginx"},
			8,
			"\n        name: nginx\n        state: restarted",
		},
		{
			"nested map with list",
			map[string]any{"ports": []any{"80", "443"}},
			8,
			"\n        ports: \n          - 80\n          - 443",
		},
		{
			"nil renders empty",
			nil,
			8,
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatYAMLParamValue(tc.value, tc.indent); got != tc.want {
				t.Fatalf("FormatYAMLParamValue(%#v) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestCompileOnTheFlyPlaybookExact(t *testing.T) {
	become := true
	task := Task{
		ID:              "task-1",
		Title:           `Deploy "web" <node>`,
		TargetHostRoute: "10.0.0.5",
		TargetSSHUser:   "core",
		TargetSSHPort:   22,
		Become:          &become,
		BecomeMethod:    "run0",
		Steps: []Step{
			{
				Name:   "Restart nginx",
				Module: "ansible.builtin.systemd_service",
				Parameters: map[string]any{
					"name":  "nginx",
					"state": "restarted",
				},
			},
			{
				Name:   "Allow port",
				Module: "ansible.posix.firewalld",
				Parameters: map[string]any{
					"port":      "80/tcp",
					"permanent": true,
				},
			},
		},
	}

	want := `---
- name: "Deploy \"web\" <node>"
  hosts: all
  gather_facts: false
  vars:
    ansible_host: "10.0.0.5"
    ansible_user: "core"
    ansible_port: 22
  become: true
  become_method: community.general.run0
  tasks:
    - name: "Restart nginx"
      ansible.builtin.systemd_service:
        name: nginx
        state: restarted
    - name: "Allow port"
      ansible.posix.firewalld:
        permanent: true
        port: 80/tcp
`

	if got := CompileOnTheFlyPlaybook(task); got != want {
		t.Fatalf("playbook mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCompileOnTheFlyPlaybookLocalAndProxy(t *testing.T) {
	become := false
	task := Task{
		Title:           "Local check",
		TargetHostRoute: "127.0.0.1",
		TargetSSHUser:   "root",
		TargetSSHPort:   2200,
		Connection: &Connection{
			Plugin:       "connection/local",
			ProxyCommand: "custom-proxy %h %p",
		},
		Become:       &become,
		BecomeMethod: "doas",
		Steps: []Step{{
			Name:       "Ping",
			Module:     "ansible.builtin.ping",
			Parameters: map[string]any{},
		}},
	}

	out := CompileOnTheFlyPlaybook(task)
	for _, want := range []string{
		`    ansible_connection: "local"`,
		`    ansible_ssh_common_args: "-o ProxyCommand=\"custom-proxy %h %p\""`,
		"  become: false",
		"  become_method: doas",
		"    - name: \"Ping\"",
		"      ansible.builtin.ping:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("playbook missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateTaskInventory(t *testing.T) {
	type hv struct {
		AnsibleHost         string `json:"ansible_host"`
		AnsibleUser         string `json:"ansible_user"`
		AnsiblePort         int    `json:"ansible_port"`
		AnsibleConnection   string `json:"ansible_connection"`
		AnsibleBecomeMethod string `json:"ansible_become_method"`
		AnsibleCommonArgs   string `json:"ansible_ssh_common_args"`
		AnsibleExtraArgs    string `json:"ansible_ssh_extra_args"`
	}

	// The document must be a static Ansible inventory (all.hosts is a map of
	// hostname -> vars); ansible rejects the _meta/dynamic-script shape here.
	parse := func(t *testing.T, out string) hv {
		t.Helper()
		var doc struct {
			All struct {
				Hosts map[string]hv `json:"hosts"`
			} `json:"all"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("inventory is not valid JSON: %v\n%s", err, out)
		}
		if len(doc.All.Hosts) != 1 {
			t.Fatalf("expected exactly one host, got %v", doc.All.Hosts)
		}
		for _, got := range doc.All.Hosts {
			return got
		}
		return hv{}
	}

	t.Run("ssh with extra args", func(t *testing.T) {
		task := Task{
			TargetNodeID:    "node-a",
			TargetNodeName:  "a",
			TargetHostRoute: "10.0.0.9",
			TargetSSHUser:   "fallback",
			TargetSSHPort:   22,
			BecomeMethod:    "run0",
			Connection: &Connection{
				Plugin:       "connection/ssh",
				Host:         "10.0.0.5",
				Port:         2222,
				User:         "deploy",
				BecomeMethod: "sudo",
				ExtraArgs:    []string{"-o", "StrictHostKeyChecking=no"},
			},
		}
		out, err := GenerateTaskInventory(task)
		if err != nil {
			t.Fatal(err)
		}
		got := parse(t, out)
		if got.AnsibleHost != "10.0.0.5" || got.AnsibleUser != "deploy" || got.AnsiblePort != 2222 {
			t.Fatalf("connection block not honoured: %+v", got)
		}
		if got.AnsibleConnection != "ssh" {
			t.Fatalf("connection = %q, want ssh", got.AnsibleConnection)
		}
		if got.AnsibleBecomeMethod != "sudo" {
			t.Fatalf("become method = %q, want sudo", got.AnsibleBecomeMethod)
		}
		if got.AnsibleExtraArgs != "-o StrictHostKeyChecking=no" {
			t.Fatalf("extra args = %q", got.AnsibleExtraArgs)
		}
	})

	t.Run("tailscale plugin", func(t *testing.T) {
		task := Task{
			TargetNodeID:    "node-b",
			TargetNodeName:  "b",
			TargetHostRoute: "b.leo-banjo.ts.net",
			Connection: &Connection{
				Plugin:       "connection/tailscale",
				ProxyCommand: "my-daemon proxy %h %p",
			},
		}
		out, err := GenerateTaskInventory(task)
		if err != nil {
			t.Fatal(err)
		}
		got := parse(t, out)
		if got.AnsibleConnection != "ssh" {
			t.Fatalf("connection = %q, want ssh", got.AnsibleConnection)
		}
		if got.AnsibleCommonArgs != `-o ProxyCommand="my-daemon proxy %h %p"` {
			t.Fatalf("common args = %q", got.AnsibleCommonArgs)
		}
		if got.AnsibleUser != "core" || got.AnsiblePort != 22 {
			t.Fatalf("defaults not applied: %+v", got)
		}
	})

	t.Run("local", func(t *testing.T) {
		task := Task{TargetNodeName: "self", Connection: &Connection{Plugin: "connection/local", Host: "127.0.0.1"}}
		out, err := GenerateTaskInventory(task)
		if err != nil {
			t.Fatal(err)
		}
		if got := parse(t, out); got.AnsibleConnection != "local" {
			t.Fatalf("connection = %q, want local", got.AnsibleConnection)
		}
	})

	t.Run("winrm", func(t *testing.T) {
		task := Task{TargetNodeName: "win", Connection: &Connection{Plugin: "connection/winrm", Host: "10.1.1.1", User: "Administrator", Port: 5986}}
		out, err := GenerateTaskInventory(task)
		if err != nil {
			t.Fatal(err)
		}
		got := parse(t, out)
		if got.AnsibleConnection != "winrm" || got.AnsiblePort != 5986 {
			t.Fatalf("winrm hostvars wrong: %+v", got)
		}
	})

	t.Run("no connection block", func(t *testing.T) {
		task := Task{TargetNodeID: "node-c", TargetNodeName: "c", TargetHostRoute: "c.example", TargetSSHUser: "ops", TargetSSHPort: 2022}
		out, err := GenerateTaskInventory(task)
		if err != nil {
			t.Fatal(err)
		}
		got := parse(t, out)
		if got.AnsibleHost != "c.example" || got.AnsibleUser != "ops" || got.AnsiblePort != 2022 {
			t.Fatalf("task defaults not used: %+v", got)
		}
		if got.AnsibleConnection != "" {
			t.Fatalf("unexpected explicit connection %q", got.AnsibleConnection)
		}
	})
}

func TestResolveProxyCommand(t *testing.T) {
	// Pin the "no system tailscaled" branch: the daemon proxy bridge is only
	// used when the host has no tailscaled socket, which a dev box usually has.
	forceNoTailscaled := func(t *testing.T) {
		t.Helper()
		original := isSystemTailscaledActive
		isSystemTailscaledActive = func() bool { return false }
		t.Cleanup(func() { isSystemTailscaledActive = original })
	}

	t.Run("system tailscaled suppresses the bridge", func(t *testing.T) {
		original := isSystemTailscaledActive
		isSystemTailscaledActive = func() bool { return true }
		t.Cleanup(func() { isSystemTailscaledActive = original })

		if got := ResolveProxyCommand(Task{TargetHostRoute: "100.64.0.1"}); got != "" {
			t.Fatalf("got %q, want empty while system tailscaled runs", got)
		}
	})

	t.Run("explicit proxyCommand wins", func(t *testing.T) {
		forceNoTailscaled(t)
		task := Task{TargetHostRoute: "100.64.0.1", Connection: &Connection{ProxyCommand: "explicit %h %p"}}
		if got := ResolveProxyCommand(task); got != "explicit %h %p" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("non-tailscale route has no proxy", func(t *testing.T) {
		forceNoTailscaled(t)
		task := Task{TargetHostRoute: "192.168.1.2"}
		if got := ResolveProxyCommand(task); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("finds daemon binary under cwd/backend-go", func(t *testing.T) {
		forceNoTailscaled(t)
		dir := t.TempDir()
		binDir := filepath.Join(dir, "backend-go")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(binDir, "air-traffic-control-daemon")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(dir)

		// Pin the candidate list to the one this test created: /tmp may hold a
		// real daemon binary on the build machine and would shadow it.
		originalCandidates := daemonProxyCandidates
		daemonProxyCandidates = func(cwd string) []string {
			return []string{
				filepath.Join(cwd, "backend-go", "air-traffic-control-daemon"),
				"/tmp/air-traffic-control-daemon",
				"/usr/local/bin/air-traffic-control-daemon",
			}
		}
		t.Cleanup(func() { daemonProxyCandidates = originalCandidates })

		task := Task{TargetHostRoute: "100.101.102.103"}
		if got, want := ResolveProxyCommand(task), bin+" proxy %h %p"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("tailscale hostname triggers resolution", func(t *testing.T) {
		forceNoTailscaled(t)
		dir := t.TempDir()
		t.Chdir(dir)
		task := Task{TargetHostRoute: "node.leo-banjo.ts.net"}
		got := ResolveProxyCommand(task)
		if !strings.HasSuffix(got, " proxy %h %p") {
			t.Fatalf("unexpected proxy command %q", got)
		}
	})
}

func TestSecretMasking(t *testing.T) {
	if got, want := MaskedSecretValue("smb_password"), "•••••••• (vault: smb_password)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := MaskedSecretValue(""); got != "••••••••" {
		t.Fatalf("empty var name: got %q", got)
	}

	task := CompileShareTask(Node{ID: "h1", Name: "host1"}, "media", "/srv/media", []string{"a", "b"}, "smb_vault")
	if task.SecretsMasked["smb_password"] != "•••••••• (vault: smb_vault)" {
		t.Fatalf("share task did not mask the secret: %+v", task.SecretsMasked)
	}

	redacted := RedactSecrets(`{"password": "hunter2", "token":"abc123", "ok":"keep"}`)
	for _, secret := range []string{"hunter2", "abc123"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret %q survived redaction: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "keep") {
		t.Fatalf("non-secret value was scrubbed: %s", redacted)
	}
	if got := RedactSecrets("password=s3cret rest"); strings.Contains(got, "s3cret") {
		t.Fatalf("unquoted secret survived: %s", got)
	}
}

func TestBuildersStayNativeFirst(t *testing.T) {
	node := Node{ID: "h1", Name: "host1", ShortName: "h1", IP: "10.0.0.2"}
	tasks := []Task{
		CompileServiceTask(node, "nginx", "restarted"),
		CompileFirewallTask(node, "443", "tcp", "public"),
		CompileShareTask(node, "media", "/srv/media", []string{"u"}, ""),
		CompileContainerTask(node, "web", "restart"),
		CompilePackageTask(node, "htop", "present"),
	}
	for _, task := range tasks {
		for _, step := range task.Steps {
			if step.Module == "ansible.builtin.shell" || step.Module == "ansible.builtin.command" {
				t.Fatalf("builder emitted shell/command: %+v", step)
			}
		}
		if task.ID == "" || task.Status != StatusStaged || task.CreatedAt == 0 {
			t.Fatalf("builder produced an incomplete task: %+v", task)
		}
	}

	svc := CompileServiceTask(node, "nginx", "restarted")
	if svc.Steps[0].Module != "ansible.builtin.systemd_service" || svc.PrimaryModule != "ansible.builtin.systemd_service" {
		t.Fatalf("service builder module drift: %+v", svc.Steps[0])
	}
	if svc.Title != "Restarted service nginx" {
		t.Fatalf("service title = %q", svc.Title)
	}
	if !svc.BecomeEnabled() {
		t.Fatal("service task must run with become")
	}

	ctr := CompileContainerTask(node, "web", "stop")
	if ctr.BecomeEnabled() {
		t.Fatal("container task must not become")
	}
	if ctr.Steps[0].Parameters["state"] != "stopped" {
		t.Fatalf("container state = %v", ctr.Steps[0].Parameters["state"])
	}
}

func TestRun0ElevationIsSkippedForRootLogins(t *testing.T) {
	rootTask := Task{
		Title:           "nft rule",
		TargetNodeName:  "fred",
		TargetHostRoute: "fred.leo-banjo.ts.net",
		TargetSSHUser:   "root",
		BecomeMethod:    "run0",
		Connection:      &Connection{Plugin: "connection/tailscale", Host: "fred.leo-banjo.ts.net", Port: 22, User: "root", BecomeMethod: "run0"},
		Steps:           []Step{{Name: "noop", Module: "ansible.builtin.command", Parameters: map[string]any{"cmd": "true"}}},
	}

	playbook := CompileOnTheFlyPlaybook(rootTask)
	if strings.Contains(playbook, "become: true") {
		t.Fatalf("root login must not enable become:\n%s", playbook)
	}

	inv, err := GenerateTaskInventory(rootTask)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inv, "community.general.run0") {
		t.Fatalf("root login must not carry a become method:\n%s", inv)
	}
	if strings.Contains(inv, "-tt") {
		t.Fatalf("root login must not force a TTY:\n%s", inv)
	}

	// A non-root login still elevates through run0 and needs the TTY flag.
	userTask := rootTask
	conn := *rootTask.Connection
	conn.User = "core"
	userTask.Connection = &conn
	userTask.TargetSSHUser = "core"
	userInv, err := GenerateTaskInventory(userTask)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(userInv, "community.general.run0") {
		t.Fatalf("non-root login must keep the run0 become method:\n%s", userInv)
	}
	if !strings.Contains(userInv, "-tt") {
		t.Fatalf("run0 requires -tt in the ssh args:\n%s", userInv)
	}
}
