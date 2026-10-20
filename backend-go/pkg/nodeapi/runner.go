package nodeapi

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

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

func (r *sshRunner) RunNodeCommand(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	ip, _ := node.GetString("ip")
	shortName, _ := node.GetString("shortName")
	tailscaleHost, _ := node.GetString("tailscaleHost")
	tailscaleIp, _ := node.GetString("tailscaleIp")
	sshUser, _ := node.GetString("sshUser")
	port := 22
	if rawPort, ok := node.Get("port"); ok {
		if p := parsePositiveInt(rawPort); p > 0 {
			port = p
		}
	}

	isLocal := ip == "127.0.0.1" ||
		ip == "localhost" ||
		shortName == "dev-pc" ||
		strings.HasPrefix(tailscaleHost, "dev-pc")

	if isLocal {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
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

	if sshUser == "" {
		sshUser = "core"
	}

	cfg := ssh.Config{
		Host:    targetHost,
		Port:    port,
		User:    sshUser,
		Timeout: timeout,
	}

	return r.pool.RunCommand(ctx, cfg, command)
}
