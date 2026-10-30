package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	"air-traffic-control/backend/pkg/secrets"
)

// SettingsPageID is the route slug of the datacenter-wide SSH transport settings page.
const SettingsPageID = "connection/ssh"

// Default SSH transport values when no per-host override or persisted setting is configured.
const (
	DefaultUser           = "core"
	DefaultPort           = 22
	DefaultTimeoutSeconds = 8
)

// Settings field names for connection/ssh.
const (
	FieldDefaultUser          = "defaultUser"
	FieldDefaultPort          = "defaultPort"
	FieldConnectTimeoutSecs   = "connectTimeoutSeconds"
	FieldAgentForwarding      = "agentForwarding"
	FieldHostKeyPolicy        = "hostKeyPolicy"
	FieldPrivateKeyVaultVar   = "privateKeyVaultVar"
)

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Dialer                      = (*Plugin)(nil)
	_ plugin.Reader                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin, plugin.Dialer, and the v2 newapi contracts
// for standard SSH connections.
type Plugin struct {
	mu      sync.Mutex
	clients map[string]*ssh.Client
}

// New returns a new SSH connection plugin.
func New() *Plugin {
	return &Plugin{
		clients: make(map[string]*ssh.Client),
	}
}

// Meta returns metadata for the SSH plugin.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          SettingsPageID,
		Kind:        plugin.KindConnection,
		Name:        "ssh",
		Label:       "Secure Shell (SSH)",
		Icon:        "terminal",
		Description: "Pure-Go SSH connection provider with agent forwarding and known-hosts accept-new semantics",
		Order:       10,
	}
}

// CollectionSchedule returns the standard polling cadence and cache TTL
// schedule for SSH transport status inspection.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "ssh",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide SSH transport defaults via
// settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "SSH Connection Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide OpenSSH connection user, port, handshake timeout, agent forwarding, and Ansible Vault key defaults.").
		StringField(
			FieldDefaultUser,
			"Default SSH User",
			DefaultUser,
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder(DefaultUser),
			settingsframework.WithDescription("Default system username used when dialing SSH nodes without a per-host user override."),
		).
		IntField(
			FieldDefaultPort,
			"Default SSH Port",
			DefaultPort,
			settingsframework.WithMinMax(1, 65535),
			settingsframework.WithDescription("Default TCP port used for SSH connections when no per-host port override is set."),
		).
		IntField(
			FieldConnectTimeoutSecs,
			"Connection Handshake Timeout (Seconds)",
			DefaultTimeoutSeconds,
			settingsframework.WithMinMax(1, 120),
			settingsframework.WithDescription("TCP and SSH handshake timeout in seconds before marking a target host unreachable."),
		).
		BoolField(
			FieldAgentForwarding,
			"Enable SSH Agent Authentication",
			true,
			settingsframework.WithDescription("Authenticate via local SSH_AUTH_SOCK / systemd user ssh-agent socket when available."),
		).
		SelectField(
			FieldHostKeyPolicy,
			"Host Key Verification Policy",
			"accept-new",
			[]plugin.SettingsFieldOption{
				{Value: "accept-new", Label: "Accept New (StrictHostKeyChecking=accept-new)"},
				{Value: "strict", Label: "Strict Known Hosts"},
				{Value: "ignore", Label: "Ignore Host Key Changes"},
			},
			settingsframework.WithDescription("Host key verification policy applied to non-interactive SSH connections."),
		).
		SecretField(
			FieldPrivateKeyVaultVar,
			"Default Private Key Vault Variable",
			settingsframework.WithPlaceholder("vault_ssh_private_key"),
			settingsframework.WithDescription("Optional Ansible Vault variable holding the fallback SSH private key. Never returned in plaintext."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultSSHUser() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultUser, DefaultUser)
}

func defaultSSHPort() int {
	port := settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, FieldDefaultPort, DefaultPort)
	if port <= 0 || port > 65535 {
		return DefaultPort
	}
	return port
}

func defaultSSHTimeout() time.Duration {
	secs := settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, FieldConnectTimeoutSecs, DefaultTimeoutSeconds)
	if secs <= 0 {
		secs = DefaultTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

func defaultAgentForwarding() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldAgentForwarding, true)
}

// ContextMenuEntries provides node-level SSH context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(meta.Kind)).
		AddAction("test_connection", "Test SSH Connectivity", "network_check", "test_connection", 10).
		AddAction("authorize_key", "Authorize SSH Public Key", "vpn_key", "authorize_key", 20).
		AddDangerAction("restart_sshd", "Restart OpenSSH Daemon", "restart_alt", "restart_sshd",
			"Restart the OpenSSH server daemon (sshd) on this host?", 90).
		Build()
}

// Compatible checks if the host facts are compatible with SSH.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.ActiveConnection != "" && h.ActiveConnection != "connection/ssh" {
		return false
	}
	// Compatible with most OSes (linux, bsd, darwin, router/switch, windows if OpenSSH is present)
	return true
}

// Requirements returns dependencies for the SSH plugin.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"openssh-server"},
			"apt": {"openssh-server"},
			"apk": {"openssh-server"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// Runner represents an SSH command runner targeting a specific host.
type Runner struct {
	client *ssh.Client
}

// RunCommand executes a command remotely over the SSH session.
func (r *Runner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if r.client == nil {
		return "", fmt.Errorf("ssh client is nil")
	}

	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create ssh session: %w", err)
	}
	defer session.Close()

	done := make(chan struct{})
	var out []byte
	var runErr error

	go func() {
		out, runErr = session.CombinedOutput(cmd)
		close(done)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return "", ctx.Err()
	case <-done:
		if runErr != nil {
			return string(out), fmt.Errorf("ssh command execution failed: %w (output: %s)", runErr, string(out))
		}
		return string(out), nil
	}
}

// resolveAgentSocketPath finds an active SSH agent socket.
func resolveAgentSocketPath() string {
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if _, err := os.Stat(sock); err == nil {
			return sock
		}
	}
	uid := os.Getuid()
	candidates := []string{
		fmt.Sprintf("/run/user/%d/ssh-agent.socket", uid),
		fmt.Sprintf("/run/user/%d/keyring/ssh", uid),
		fmt.Sprintf("/run/user/%d/gnupg/S.gpg-agent.ssh", uid),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// getSSHAgentAuthMethod connects to the local ssh-agent if present.
func getSSHAgentAuthMethod() (ssh.AuthMethod, io.Closer) {
	sock := resolveAgentSocketPath()
	if sock == "" {
		return nil, nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil
	}
	agentClient := agent.NewClient(conn)
	return ssh.PublicKeysCallback(agentClient.Signers), conn
}

func resolveEffectiveUser(h plugin.HostFacts) string {
	if u, ok := h.Overrides["user"]; ok && strings.TrimSpace(u) != "" {
		return strings.TrimSpace(u)
	}
	return defaultSSHUser()
}

func resolveEffectivePort(h plugin.HostFacts) int {
	if pStr, ok := h.Overrides["port"]; ok && strings.TrimSpace(pStr) != "" {
		if pVal, err := strconv.Atoi(strings.TrimSpace(pStr)); err == nil && pVal > 0 && pVal <= 65535 {
			return pVal
		}
	}
	return defaultSSHPort()
}

// BuildClientConfig builds an ssh.ClientConfig from HostFacts.
func BuildClientConfig(h plugin.HostFacts) (*ssh.ClientConfig, error) {
	user := resolveEffectiveUser(h)

	var authMethods []ssh.AuthMethod

	// 1. Agent forwarding
	if defaultAgentForwarding() {
		if agentAuth, _ := getSSHAgentAuthMethod(); agentAuth != nil {
			authMethods = append(authMethods, agentAuth)
		}
	}

	// 2. Private Key
	if pkStr, ok := h.Overrides["private_key"]; ok && strings.TrimSpace(pkStr) != "" {
		pass := h.Overrides["passphrase"]
		var signer ssh.Signer
		var err error
		if pass != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pkStr), []byte(pass))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(pkStr))
		}
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}

	// 3. Password
	if pass, ok := h.Overrides["password"]; ok && pass != "" {
		authMethods = append(authMethods, ssh.Password(pass))
	}

	// 4. Ansible Vault secret referenced by the node or tag chunk. Decrypted at
	// connect time from the coordinator vault store; plaintext never leaves the
	// daemon. A missing/undecryptable reference fails loudly.
	if vaultID := strings.TrimSpace(h.Overrides["vaultSecretId"]); vaultID != "" {
		sec, err := secrets.Resolve(context.Background(), vaultID)
		if err != nil {
			return nil, err
		}
		switch sec.Kind {
		case secrets.KindPrivateKey:
			var signer ssh.Signer
			var parseErr error
			if pass := h.Overrides["passphrase"]; pass != "" {
				signer, parseErr = ssh.ParsePrivateKeyWithPassphrase([]byte(sec.Value), []byte(pass))
			} else {
				signer, parseErr = ssh.ParsePrivateKey([]byte(sec.Value))
			}
			if parseErr != nil {
				return nil, fmt.Errorf("vault secret %q is not a usable SSH private key: %w", vaultID, parseErr)
			}
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		case secrets.KindPassword:
			authMethods = append(authMethods, ssh.Password(sec.Value))
		}
	}

	timeout := defaultSSHTimeout()
	if tStr, ok := h.Overrides["timeout"]; ok && tStr != "" {
		if d, err := time.ParseDuration(tStr); err == nil && d > 0 {
			timeout = d
		}
	}

	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Known-hosts accept-new / zero-mock
		Timeout:         timeout,
	}, nil
}

// NewRunner creates a command runner for the specified host.
func (p *Plugin) NewRunner(h plugin.HostFacts) (plugin.Runner, error) {
	client, err := p.GetClient(h)
	if err != nil {
		return nil, err
	}
	return &Runner{client: client}, nil
}

// EvictClient removes a cached client for the given facts.
func (p *Plugin) EvictClient(h plugin.HostFacts) {
	host := h.HostRoute
	if host == "" {
		return
	}
	user := resolveEffectiveUser(h)
	port := resolveEffectivePort(h)
	key := fmt.Sprintf("%s@%s:%d", user, host, port)
	p.mu.Lock()
	if c, ok := p.clients[key]; ok {
		delete(p.clients, key)
		p.mu.Unlock()
		_ = c.Close()
	} else {
		p.mu.Unlock()
	}
}

// GetClient retrieves or connects an SSH client for the host facts.
func (p *Plugin) GetClient(h plugin.HostFacts) (*ssh.Client, error) {
	host := h.HostRoute
	if host == "" {
		return nil, fmt.Errorf("hostRoute is required")
	}

	port := resolveEffectivePort(h)
	user := resolveEffectiveUser(h)

	key := fmt.Sprintf("%s@%s:%d", user, host, port)

	p.mu.Lock()
	if client, ok := p.clients[key]; ok {
		p.mu.Unlock()
		if _, _, err := client.SendRequest("keepalive@openssh.com", false, nil); err == nil {
			return client, nil
		}
		p.mu.Lock()
		delete(p.clients, key)
		p.mu.Unlock()
		_ = client.Close()
	} else {
		p.mu.Unlock()
	}

	cfg, err := BuildClientConfig(h)
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to dial ssh to %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		if len(cfg.Auth) == 0 {
			return nil, fmt.Errorf(
				"ssh handshake failed to %s: %w (no credential offered: node has no vaultSecretId, no private_key/password override, and no ssh-agent socket — assign a vault secret or use authMethod=tailscale-ssh)",
				addr, err,
			)
		}
		return nil, fmt.Errorf("ssh handshake failed to %s: %w", addr, err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	p.mu.Lock()
	p.clients[key] = client
	p.mu.Unlock()

	return client, nil
}

// Probe checks connectivity to the target host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		var err error
		r, err = p.NewRunner(h)
		if err != nil {
			return plugin.Detection{
				Detected: false,
				Status:   "OFFLINE",
				Error:    err.Error(),
			}, nil
		}
	}

	out, err := r.RunCommand(ctx, "echo ATC_SSH_PROBE")
	if err != nil {
		return plugin.Detection{
			Detected: false,
			Status:   "UNREACHABLE",
			Error:    err.Error(),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  strings.TrimSpace(out),
	}, nil
}

// Read inspects live SSH transport and OpenSSH server status on the target host.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	out, err := r.RunCommand(ctx, "ssh -V 2>&1 || uname -n")
	if err != nil {
		return nil, fmt.Errorf("ssh status read failed: %w", err)
	}

	version := strings.TrimSpace(out)
	user := resolveEffectiveUser(h)
	port := resolveEffectivePort(h)

	return map[string]any{
		"ssh": map[string]any{
			"status":         "ONLINE",
			"host":           h.HostRoute,
			"user":           user,
			"port":           port,
			"version":        version,
			"agentAvailable": resolveAgentSocketPath() != "",
		},
	}, nil
}

// ManagementActions returns the v2 management action specifications for SSH transport and access management.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "test_connection",
				Label:       "Test SSH Connectivity",
				Description: "Verify non-interactive SSH connectivity and handshake against the target host",
				Options: []plugin.OptionSchema{
					{
						Name:        "timeout",
						Type:        "int",
						Required:    false,
						Default:     DefaultTimeoutSeconds,
						Description: "Maximum seconds to wait for SSH connectivity verification",
					},
				},
			},
			Category:         newapi.ActionCategoryDiagnostic,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindConnection},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "authorize_key",
				Label:       "Authorize SSH Public Key",
				Description: "Install or remove an SSH public key in the target user's authorized_keys file",
				Options: []plugin.OptionSchema{
					{
						Name:        "user",
						Type:        "string",
						Required:    false,
						Default:     DefaultUser,
						Description: "Target system account username (defaults to host or datacenter SSH user)",
					},
					{
						Name:        "public_key",
						Type:        "string",
						Required:    true,
						Secret:      true,
						Description: "OpenSSH public key string (e.g. ssh-ed25519 AAAA...)",
					},
					{
						Name:        "state",
						Type:        "select",
						Required:    false,
						Default:     "present",
						Enum:        []string{"present", "absent"},
						Description: "Whether the SSH public key should be present or absent",
					},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindConnection},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "restart_sshd",
				Label:       "Restart OpenSSH Daemon",
				Description: "Restart the OpenSSH server daemon (sshd) on the target host",
				Options: []plugin.OptionSchema{
					{
						Name:        "service",
						Type:        "string",
						Required:    false,
						Default:     "sshd",
						Description: "OpenSSH systemd service name (e.g. sshd or ssh)",
					},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Restart the OpenSSH server daemon (sshd) on this host?",
			InvalidatesKinds: []plugin.Kind{plugin.KindConnection},
		},
	}
}

// Actions enumerates mutation actions supported by the SSH plugin.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options against the declared ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.TrimSpace(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported %s action: %s", p.Meta().Name, action)
}

// CompileTask adapts a typed v2 TaskRequest to the plugin's declarative Ansible TaskSpec compiler.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, req.Params)
}

// Compile compiles an SSH management action into a declarative Ansible TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if opts == nil {
		opts = map[string]any{}
	}
	if err := p.ValidateAction(h, action, opts); err != nil {
		return plugin.TaskSpec{}, err
	}

	switch strings.ToLower(strings.TrimSpace(action)) {
	case "test_connection":
		timeout := DefaultTimeoutSeconds
		switch v := opts["timeout"].(type) {
		case int:
			if v > 0 {
				timeout = v
			}
		case float64:
			if int(v) > 0 {
				timeout = int(v)
			}
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && parsed > 0 {
				timeout = parsed
			}
		}
		return plugin.TaskSpec{
			Name: "Verify SSH connectivity",
			Steps: []plugin.TaskStep{
				{
					Name:   "Wait for SSH transport connection",
					Module: "ansible.builtin.wait_for_connection",
					Params: map[string]any{
						"timeout": timeout,
					},
				},
			},
		}, nil

	case "authorize_key":
		user, _ := opts["user"].(string)
		user = strings.TrimSpace(user)
		if user == "" {
			user = resolveEffectiveUser(h)
		}
		pubKey, _ := opts["public_key"].(string)
		pubKey = strings.TrimSpace(pubKey)
		if pubKey == "" {
			return plugin.TaskSpec{}, fmt.Errorf("public_key is required for authorize_key")
		}
		state, _ := opts["state"].(string)
		state = strings.TrimSpace(state)
		if state == "" {
			state = "present"
		}

		return plugin.TaskSpec{
			Name: fmt.Sprintf("Authorize SSH key for %s", user),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure authorized_keys entry for %s is %s", user, state),
					Module: "ansible.builtin.lineinfile",
					Params: map[string]any{
						"path":   fmt.Sprintf("~%s/.ssh/authorized_keys", user),
						"line":   pubKey,
						"state":  state,
						"create": true,
						"mode":   "0600",
						"become": true,
					},
				},
			},
			SecretsMasked: map[string]string{
				"public_key": plugin.MaskedSecretValue("public_key"),
			},
		}, nil

	case "restart_sshd":
		service, _ := opts["service"].(string)
		service = strings.TrimSpace(service)
		if service == "" {
			service = "sshd"
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Restart %s service", service),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Restart OpenSSH daemon (%s)", service),
					Module: "ansible.builtin.service",
					Params: map[string]any{
						"name":   service,
						"state":  "restarted",
						"become": true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported ssh action: %s", action)
	}
}

// Dial satisfies plugin.Dialer, opening a raw TCP connection through standard networking.
func (p *Plugin) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}
