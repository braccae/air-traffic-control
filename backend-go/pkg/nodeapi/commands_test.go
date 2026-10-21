package nodeapi

import (
	"strings"
	"testing"
)

func TestPortValidation(t *testing.T) {
	validPorts := []struct {
		in   string
		norm string
	}{
		{"80", "80"},
		{"443", "443"},
		{"1", "1"},
		{"65535", "65535"},
		{"  8080  ", "8080"},
		{"8000-8080", "8000-8080"},
		{"30000-32767", "30000-32767"},
		{"1-1024", "1-1024"},
	}
	for _, tc := range validPorts {
		res := validatePortOrRange(tc.in)
		if !res.Valid || res.Normalized != tc.norm {
			t.Errorf("validatePortOrRange(%q) = %+v; want norm %q", tc.in, res, tc.norm)
		}
	}

	invalidPorts := []string{
		"0", "65536", "-1", "http", "", "8080-8000", "80-80", "0-100", "100-70000", "8000-", "-8080", "8000-8080-9000",
	}
	for _, in := range invalidPorts {
		res := validatePortOrRange(in)
		if res.Valid {
			t.Errorf("validatePortOrRange(%q) should be invalid, got valid", in)
		}
	}
}

func TestProtocolValidation(t *testing.T) {
	for _, proto := range []string{"tcp", "udp", "sctp", "icmp", "all", "TCP", "Udp"} {
		res := validateProtocol(proto)
		if !res.Valid {
			t.Errorf("validateProtocol(%q) should be valid", proto)
		}
	}
	for _, proto := range []string{"http", "ftp", "ip", ""} {
		res := validateProtocol(proto)
		if res.Valid {
			t.Errorf("validateProtocol(%q) should be invalid", proto)
		}
	}
}

func TestCidrOrIpValidation(t *testing.T) {
	valid := []string{"192.168.1.1", "10.0.0.0/8", "172.16.0.0/16", "any", "0.0.0.0/0", ""}
	for _, in := range valid {
		res := validateCidrOrIp(in)
		if !res.Valid {
			t.Errorf("validateCidrOrIp(%q) = invalid; want valid", in)
		}
	}
	invalid := []string{"256.0.0.1", "10.0.0.0/33", "foo.bar", "1.2.3"}
	for _, in := range invalid {
		res := validateCidrOrIp(in)
		if res.Valid {
			t.Errorf("validateCidrOrIp(%q) = valid; want invalid", in)
		}
	}
}

func TestInterfaceNameValidation(t *testing.T) {
	valid := []string{"eth0", "wlan0", "tailscale0", "br-lan", "vlan.100", "any", ""}
	for _, in := range valid {
		res := validateInterfaceName(in)
		if !res.Valid {
			t.Errorf("validateInterfaceName(%q) = invalid", in)
		}
	}
	invalid := []string{"-eth0", "interface_name_that_is_far_too_long_for_linux", "eth 0"}
	for _, in := range invalid {
		res := validateInterfaceName(in)
		if res.Valid {
			t.Errorf("validateInterfaceName(%q) = valid; want invalid", in)
		}
	}
}

func TestZoneValidation(t *testing.T) {
	res := validateZone("public")
	if !res.Valid || res.Normalized != "public" {
		t.Fatalf("expected public zone")
	}
	res2 := validateZone("INVALID_ZONE")
	if res2.Valid {
		t.Fatalf("expected invalid zone")
	}
	res3 := validateZone("")
	if !res3.Valid || res3.Normalized != "public" {
		t.Fatalf("expected public default for empty zone")
	}
}

func TestBuildConnectNetworkCommand(t *testing.T) {
	// wireguard
	cmd, err := BuildConnectNetworkCommand(ConnectNetworkInput{
		Type:    "wireguard",
		Name:    "wg0",
		Cidr:    "10.0.0.1/24",
		Gateway: "10.0.0.254",
	})
	if err != nil {
		t.Fatalf("wireguard connect failed: %v", err)
	}
	if !strings.Contains(cmd, "ip link add dev wg0 type wireguard") ||
		!strings.Contains(cmd, "ip addr add 10.0.0.1/24 dev wg0") ||
		!strings.Contains(cmd, "ip route replace default via 10.0.0.254 dev wg0") {
		t.Errorf("unexpected wireguard cmd: %s", cmd)
	}

	// bridge
	cmdBridge, err := BuildConnectNetworkCommand(ConnectNetworkInput{
		Type:   "bridge",
		Name:   "br0",
		Parent: "eth0",
		Cidr:   "192.168.1.1/24",
	})
	if err != nil {
		t.Fatalf("bridge connect failed: %v", err)
	}
	if !strings.Contains(cmdBridge, "ip link add name br0 type bridge") ||
		!strings.Contains(cmdBridge, "ip link set eth0 master br0") {
		t.Errorf("unexpected bridge cmd: %s", cmdBridge)
	}

	// vlan
	vlanID := 50
	cmdVlan, err := BuildConnectNetworkCommand(ConnectNetworkInput{
		Type:   "vlan",
		Name:   "vlan50",
		Parent: "eth0",
		VlanID: &vlanID,
	})
	if err != nil {
		t.Fatalf("vlan connect failed: %v", err)
	}
	if !strings.Contains(cmdVlan, "link eth0 name vlan50 type vlan id 50") {
		t.Errorf("unexpected vlan cmd: %s", cmdVlan)
	}

	// static
	cmdStatic, err := BuildConnectNetworkCommand(ConnectNetworkInput{
		Type: "static",
		Name: "eth1",
		Cidr: "192.168.10.5/24",
	})
	if err != nil {
		t.Fatalf("static connect failed: %v", err)
	}
	if !strings.Contains(cmdStatic, "ip addr add 192.168.10.5/24 dev eth1") {
		t.Errorf("unexpected static cmd: %s", cmdStatic)
	}

	// dhcp
	cmdDhcp, err := BuildConnectNetworkCommand(ConnectNetworkInput{
		Type: "dhcp",
		Name: "eth2",
	})
	if err != nil {
		t.Fatalf("dhcp connect failed: %v", err)
	}
	if !strings.Contains(cmdDhcp, "dhclient -v eth2") {
		t.Errorf("unexpected dhcp cmd: %s", cmdDhcp)
	}

	// unsupported
	_, err = BuildConnectNetworkCommand(ConnectNetworkInput{
		Type: "infiniband",
		Name: "ib0",
	})
	if err == nil {
		t.Errorf("expected error for unsupported type")
	}
}

func TestBuildFirewallAddCommand(t *testing.T) {
	// Simple port
	cmd, err := BuildFirewallAddCommand(FirewallRuleInput{
		Port:     "8080",
		Protocol: "tcp",
		Zone:     "public",
	})
	if err != nil {
		t.Fatalf("simple port rule failed: %v", err)
	}
	if !strings.Contains(cmd, "--add-port=8080/tcp") {
		t.Errorf("unexpected cmd: %s", cmd)
	}

	// Service rule
	cmdSvc, err := BuildFirewallAddCommand(FirewallRuleInput{
		Service: "cockpit",
		Zone:    "public",
	})
	if err != nil {
		t.Fatalf("service rule failed: %v", err)
	}
	if !strings.Contains(cmdSvc, "--add-service=cockpit") {
		t.Errorf("unexpected service cmd: %s", cmdSvc)
	}

	// Rich rule with source
	cmdRich, err := BuildFirewallAddCommand(FirewallRuleInput{
		Port:     "22",
		Protocol: "tcp",
		Source:   "192.168.1.0/24",
		Zone:     "trusted",
		Policy:   "ACCEPT",
	})
	if err != nil {
		t.Fatalf("rich rule failed: %v", err)
	}
	if !strings.Contains(cmdRich, "--add-rich-rule=") || !strings.Contains(cmdRich, "source address=\"192.168.1.0/24\"") {
		t.Errorf("unexpected rich rule cmd: %s", cmdRich)
	}

	// Forward port
	cmdFwd, err := BuildFirewallAddCommand(FirewallRuleInput{
		Port:     "8080",
		Toport:   "80",
		Protocol: "tcp",
		Zone:     "public",
	})
	if err != nil {
		t.Fatalf("forward port rule failed: %v", err)
	}
	if !strings.Contains(cmdFwd, "--add-forward-port=port=8080:proto=tcp:toport=80") {
		t.Errorf("unexpected forward port cmd: %s", cmdFwd)
	}
}

func TestBuildFirewallDeleteCommand(t *testing.T) {
	// Simple port
	cmd, err := BuildFirewallDeleteCommand(FirewallDeleteInput{
		RuleID:   "8080/tcp",
		Zone:     "public",
		Protocol: "tcp",
	})
	if err != nil {
		t.Fatalf("delete port failed: %v", err)
	}
	if !strings.Contains(cmd, "--remove-port=8080/tcp") {
		t.Errorf("unexpected cmd: %s", cmd)
	}

	// Service
	cmdSvc, err := BuildFirewallDeleteCommand(FirewallDeleteInput{
		RuleID: "svc:cockpit",
		Zone:   "public",
	})
	if err != nil {
		t.Fatalf("delete service failed: %v", err)
	}
	if !strings.Contains(cmdSvc, "--remove-service=cockpit") {
		t.Errorf("unexpected svc delete cmd: %s", cmdSvc)
	}
}

func TestSanitizeUsernameAndPath(t *testing.T) {
	validUsers := []string{"core", "john_doe", "admin1", "_svc"}
	for _, u := range validUsers {
		clean, err := SanitizeUsername(u)
		if err != nil || clean != u {
			t.Errorf("SanitizeUsername(%q) failed: %v", u, err)
		}
	}
	invalidUsers := []string{"", "123user", "-bad", "user;rm -rf", "a very long username that definitely exceeds 32 chars in total length"}
	for _, u := range invalidUsers {
		if _, err := SanitizeUsername(u); err == nil {
			t.Errorf("SanitizeUsername(%q) should fail", u)
		}
	}

	cleanPath, err := SanitizePath("/srv/data/shares//media")
	if err != nil || cleanPath != "/srv/data/shares/media" {
		t.Errorf("SanitizePath failed: got %q, err %v", cleanPath, err)
	}
	if _, err := SanitizePath("relative/path"); err == nil {
		t.Errorf("SanitizePath with relative path should fail")
	}
	if _, err := SanitizePath("/path;rm -rf"); err == nil {
		t.Errorf("SanitizePath with shell metacharacters should fail")
	}
}

func TestShareCommandBuilders(t *testing.T) {
	// NFS
	nfsCmd, err := BuildNfsSaveCommand(NfsShareConfig{
		Path:    "/srv/nfs",
		Client:  "192.168.1.0/24",
		Options: "rw,sync,no_subtree_check",
	}, "nfs_share")
	if err != nil {
		t.Fatalf("BuildNfsSaveCommand failed: %v", err)
	}
	if !strings.Contains(nfsCmd, "/etc/exports.d/cockpit-nfs_share.exports") ||
		!strings.Contains(nfsCmd, "exportfs -ra") {
		t.Errorf("unexpected nfs save cmd: %s", nfsCmd)
	}

	// SMB
	smbCmd, err := BuildSmbSaveCommand(SmbShareConfig{
		Name:       "media",
		Path:       "/srv/media",
		Comment:    "Media library",
		ReadOnly:   false,
		ValidUsers: "core @media_group",
		GuestOk:    false,
	})
	if err != nil {
		t.Fatalf("BuildSmbSaveCommand failed: %v", err)
	}
	if !strings.Contains(smbCmd, "/etc/samba/smb.conf") ||
		!strings.Contains(smbCmd, "smbcontrol all reload-config") {
		t.Errorf("unexpected smb save cmd: %s", smbCmd)
	}
}

func TestUserCommands(t *testing.T) {
	cmdCreate, err := BuildUserCreateCommand("testuser")
	if err != nil {
		t.Fatalf("BuildUserCreateCommand failed: %v", err)
	}
	if !strings.Contains(cmdCreate, "sudo useradd -m -s /bin/bash 'testuser'") {
		t.Errorf("unexpected create user cmd: %s", cmdCreate)
	}

	cmdPasswd, err := BuildUserPasswdCommand("testuser", "secret123")
	if err != nil {
		t.Fatalf("BuildUserPasswdCommand failed: %v", err)
	}
	if !strings.Contains(cmdPasswd, "sudo chpasswd") || !strings.Contains(cmdPasswd, "'testuser'") || !strings.Contains(cmdPasswd, "'secret123'") {
		t.Errorf("unexpected passwd cmd: %s", cmdPasswd)
	}

	cmdSmbPasswd, err := BuildSmbPasswdCommand("testuser", "secret123")
	if err != nil {
		t.Fatalf("BuildSmbPasswdCommand failed: %v", err)
	}
	if !strings.Contains(cmdSmbPasswd, "sudo smbpasswd -s -a 'testuser'") {
		t.Errorf("unexpected smb passwd cmd: %s", cmdSmbPasswd)
	}
}
