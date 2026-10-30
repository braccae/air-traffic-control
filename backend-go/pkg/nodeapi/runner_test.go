package nodeapi

import "testing"

func TestIsTailnetRoute(t *testing.T) {
	cases := map[string]bool{
		"100.115.75.78":                true,
		"100.64.0.1":                   true,
		"100.127.255.254":              true,
		"the-freezer.leo-banjo.ts.net": true,
		"100.63.0.1":                   false,
		"100.128.0.1":                  false,
		"192.168.8.3":                  false,
		"10.0.0.5":                     false,
		"127.0.0.1":                    false,
		"":                             false,
	}
	for host, want := range cases {
		if got := isTailnetRoute(host); got != want {
			t.Errorf("isTailnetRoute(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestIsLocalNodeHonorsConnectionOverride(t *testing.T) {
	remote := NewObject()
	remote.SetString("id", "node-cold-war")
	remote.SetString("name", "cold-war")
	remote.SetString("ip", "100.118.44.7")
	if IsLocalNode(remote) {
		t.Fatalf("expected a tailnet-addressed node to be remote")
	}

	// An explicit local connection plugin wins over the remote address.
	forcedLocal := NewObject()
	forcedLocal.SetString("id", "node-cold-war")
	forcedLocal.SetString("ip", "100.118.44.7")
	if err := forcedLocal.SetJSON("pluginOverrides", map[string]string{"connection": "connection/local"}); err != nil {
		t.Fatalf("SetJSON(pluginOverrides): %v", err)
	}
	if !IsLocalNode(forcedLocal) {
		t.Fatalf("expected pluginOverrides.connection=connection/local to select the local runner")
	}

	// And an explicit ssh plugin wins over a loopback address.
	forcedSSH := NewObject()
	forcedSSH.SetString("id", "node-dev-pc")
	forcedSSH.SetString("ip", "127.0.0.1")
	if err := forcedSSH.SetJSON("pluginOverrides", map[string]string{"connection": "connection/ssh"}); err != nil {
		t.Fatalf("SetJSON(pluginOverrides): %v", err)
	}
	if IsLocalNode(forcedSSH) {
		t.Fatalf("expected pluginOverrides.connection=connection/ssh to select the SSH runner")
	}
}

func TestIsLocalNodeRecognizesLocalBadgeAndLoopback(t *testing.T) {
	badged := NewObject()
	badged.SetString("id", "node-controller")
	badged.SetString("badge", "LOCAL")
	badged.SetString("ip", "100.118.44.7")
	if !IsLocalNode(badged) {
		t.Fatalf("expected badge=LOCAL to be treated as local")
	}

	loopback := NewObject()
	loopback.SetString("id", "node-loopback")
	loopback.SetString("ip", "127.0.0.1")
	if !IsLocalNode(loopback) {
		t.Fatalf("expected 127.0.0.1 to be treated as local")
	}
}

func TestResolveTargetHost(t *testing.T) {
	local := NewObject()
	local.SetString("id", "node-controller")
	local.SetString("ip", "127.0.0.1")
	if got := ResolveTargetHost(local); got != "127.0.0.1" {
		t.Fatalf("ResolveTargetHost(local) = %q, want 127.0.0.1", got)
	}

	remote := NewObject()
	remote.SetString("id", "node-cold-war")
	remote.SetString("ip", "192.168.8.20")
	remote.SetString("tailscaleIp", "100.118.44.7")
	if got := ResolveTargetHost(remote); got != "100.118.44.7" {
		t.Fatalf("ResolveTargetHost(remote) = %q, want the tailnet address", got)
	}
}
