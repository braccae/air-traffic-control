package nodeapi

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/cockpit"
	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/ssh"
)

// CommandRunner defines the execution contract for dispatching commands to nodes.
type CommandRunner interface {
	RunNodeCommand(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error)
}

type sshRunner struct {
	pool *ssh.ClientPool
}

// NewSSHRunner builds a CommandRunner backed by a persistent SSH connection pool,
// with a local execution shortcut matching TypeScript liveCluster.ts.
func NewSSHRunner(pool *ssh.ClientPool) CommandRunner {
	return &sshRunner{pool: pool}
}

// IsLocalNode reports whether the given node targets the controller host itself,
// in which case remote protocols (SSH) must be bypassed in favor of the local
// execution provider.
func IsLocalNode(node *Object) bool {
	if node == nil {
		return true
	}
	ip, _ := node.GetString("ip")
	tailscaleIp, _ := node.GetString("tailscaleIp")
	tailscaleHost, _ := node.GetString("tailscaleHost")
	shortName, _ := node.GetString("shortName")
	name, _ := node.GetString("name")
	route := ip
	if route == "" {
		route = tailscaleIp
	}
	if route == "" {
		route = tailscaleHost
	}
	switch route {
	case "127.0.0.1", "localhost", "::1", "":
		return true
	}
	if shortName == "localhost" || name == "localhost" {
		return true
	}
	return false
}

// LocalRunner is a CommandRunner that executes commands directly on the
// controller host via exec.CommandContext, bypassing SSH entirely. It
// implements the standard node management interface (exec) for localhost.
type LocalRunner struct {
	Shell      string
	WorkingDir string
}

// NewLocalRunner builds a CommandRunner that always runs locally.
func NewLocalRunner(shell, workingDir string) CommandRunner {
	return &LocalRunner{Shell: shell, WorkingDir: workingDir}
}

func (r *LocalRunner) RunNodeCommand(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	shell := "sh"
	if r != nil && strings.TrimSpace(r.Shell) != "" {
		shell = strings.TrimSpace(r.Shell)
	}
	effectiveCommand := command
	cmd := exec.CommandContext(ctx, shell, "-c", effectiveCommand)
	if r != nil && strings.TrimSpace(r.WorkingDir) != "" {
		cmd.Dir = strings.TrimSpace(r.WorkingDir)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("local execution failed (%w): %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// CompositeRunner selects the local execution provider for localhost nodes and
// falls back to the SSH runner for remote nodes, preserving the standard node
// management interface across both.
type CompositeRunner struct {
	Local CommandRunner
	SSH   CommandRunner
}

// NewCompositeRunner builds a CommandRunner that routes localhost nodes to the
// local driver and remote nodes to SSH.
func NewCompositeRunner(local, ssh CommandRunner) CommandRunner {
	return &CompositeRunner{Local: local, SSH: ssh}
}

func (r *CompositeRunner) RunNodeCommand(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
	if IsLocalNode(node) && r.Local != nil {
		return r.Local.RunNodeCommand(ctx, node, command, timeout)
	}
	if r.SSH != nil {
		return r.SSH.RunNodeCommand(ctx, node, command, timeout)
	}
	return "", errors.New("no command runner available")
}

func (r *sshRunner) RunNodeCommand(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	ip, _ := node.GetString("ip")
	nodeID, _ := node.GetString("id")
	tailscaleHost, _ := node.GetString("tailscaleHost")
	tailscaleIp, _ := node.GetString("tailscaleIp")
	sshUser, _ := node.GetString("sshUser")
	vaultSecretID, _ := node.GetString("vaultSecretId")
	authMethod, _ := node.GetString("authMethod")
	privMethod, _ := node.GetString("privilegeMethod")
	port := 22
	if rawPort, ok := node.Get("port"); ok {
		if p := parsePositiveInt(rawPort); p > 0 {
			port = p
		}
	}

	effectiveUser := sshUser
	if effectiveUser == "" {
		effectiveUser = "core"
	}
	effectiveCommand := command
	if (privMethod == "run0" || privMethod == "doas") && (strings.HasPrefix(command, "sudo ") || strings.HasPrefix(command, "sudo -n ")) {
		effectiveCommand = cockpit.WrapElevatedCommand(command, cockpit.ElevationMethod(privMethod), effectiveUser)
	}

	isLocal := ip == "127.0.0.1" ||
		ip == "localhost" ||
		ip == "::1"

	if isLocal {
		cmd := exec.CommandContext(ctx, "sh", "-c", effectiveCommand)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("local command failed (%w): %s", err, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}

	if r.pool == nil {
		return "", errors.New("ssh client pool is nil")
	}

	targetHost := tailscaleIp
	if targetHost == "" {
		targetHost = ip
	}
	if targetHost == "" {
		targetHost = tailscaleHost
	}
	if targetHost == "" {
		return "", errors.New("no host address available for node")
	}

	cfg := ssh.Config{
		Host:          targetHost,
		Port:          port,
		User:          effectiveUser,
		VaultSecretID: vaultSecretID,
		AuthMethod:    authMethod,
		Timeout:       timeout,
	}

	// Tailnet peers are reached over ATC's own tsnet node: the controller has no
	// host-level tailscaled route, so a bare TCP dial to 100.64.0.0/10 times out.
	if isTailnetRoute(targetHost) {
		facts := plugin.HostFacts{NodeID: nodeID, HostRoute: targetHost}
		if d, derr := plugin.ResolveDialer(facts); derr == nil {
			cfg.Dialer = d.Dial
		}
	}

	return r.pool.RunCommand(ctx, cfg, effectiveCommand)
}

// isTailnetRoute reports whether the address is a Tailscale CGNAT IP or a
// MagicDNS name, i.e. something that must be dialed through tsnet.
func isTailnetRoute(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.HasSuffix(host, ".ts.net") {
		return true
	}
	parts := strings.Split(host, ".")
	if len(parts) != 4 || parts[0] != "100" {
		return false
	}
	second, err := strconv.Atoi(parts[1])
	return err == nil && second >= 64 && second <= 127
}
