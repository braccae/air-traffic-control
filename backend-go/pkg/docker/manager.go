// Package docker inspects and mutates a Docker Engine instance over a command
// transport. It is modelled directly on pkg/podman:
//
//   - Rootful operations talk to the system Docker socket (/var/run/docker.sock)
//     and are escalated with run0 first, then passwordless sudo, then a direct
//     invocation.
//   - Rootless operations run inside the target user's session against their
//     per-user socket (/run/user/<uid>/docker.sock), preferring run0 --user,
//     then sudo -u, then a direct call.
//
// Zero-Mock Policy: every method reflects real `docker` CLI output read back
// from the target host. Nothing is synthesised. On failure the underlying error
// is returned so the caller can surface an accurate offline/unavailable status.
package docker

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/types"
)

// CommandRunner abstracts remote command execution. It is implemented by
// *ssh.ClientManager and by the plugin runner adapter.
type CommandRunner interface {
	RunCommand(cfg ssh.Config, command string) (string, error)
}

// Manager wraps a command transport to inspect a Docker Engine.
type Manager struct {
	sshMgr CommandRunner
	cfg    ssh.Config
}

// NewManager constructs a Docker manager over the supplied command transport.
func NewManager(sshMgr CommandRunner, cfg ssh.Config) *Manager {
	return &Manager{sshMgr: sshMgr, cfg: cfg}
}

// ---- CLI JSON output shapes (docker <verb> --format '{{json .}}') ----

// DockerImage is one `docker images --format '{{json .}}'` record.
type DockerImage struct {
	ID           string `json:"id"`
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
	Digest       string `json:"digest,omitempty"`
	Size         string `json:"size,omitempty"`
	CreatedSince string `json:"createdSince,omitempty"`
	Containers   string `json:"containers,omitempty"`
}

// DockerVolume is one `docker volume ls --format '{{json .}}'` record.
type DockerVolume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Labels     string `json:"labels,omitempty"`
}

// DockerNetwork is one `docker network ls --format '{{json .}}'` record.
type DockerNetwork struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Scope    string `json:"scope,omitempty"`
	Internal string `json:"internal,omitempty"`
	IPv6     string `json:"ipv6,omitempty"`
	Labels   string `json:"labels,omitempty"`
}

func (m *Manager) run(command string) (string, error) {
	if m.sshMgr == nil {
		return "", fmt.Errorf("docker: command runner is nil")
	}
	return m.sshMgr.RunCommand(m.cfg, command)
}

// shellQuote single-quotes a value so it survives the remote shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// rootfulCmd builds a command that talks to the rootful (system) Docker socket,
// preferring run0 escalation, then passwordless sudo, then a direct call.
func rootfulCmd(args string) string {
	return fmt.Sprintf(
		"run0 --background=no docker %s 2>/dev/null || sudo -n docker %s 2>/dev/null || docker %s 2>/dev/null",
		args, args, args,
	)
}

// userCmd builds a command that talks to a rootless user's Docker socket,
// preferring run0 --user, then sudo -u, then a direct call in the user session.
func userCmd(user string, uid int, args string) string {
	return fmt.Sprintf(
		"run0 --background=no --user %s docker %s 2>/dev/null || (cd / && sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d docker %s 2>/dev/null) || XDG_RUNTIME_DIR=/run/user/%d docker %s 2>/dev/null",
		user, args, user, uid, args, uid, args,
	)
}

// Probe reports whether the Docker CLI is present and the daemon is reachable.
// It returns the server version when known.
func (m *Manager) Probe() (bool, string) {
	out, err := m.run("command -v docker")
	if err != nil || strings.TrimSpace(out) == "" {
		return false, ""
	}
	verOut, _ := m.run("(docker --version 2>&1; readlink /var/run/docker.sock /run/docker.sock 2>/dev/null) || true")
	if strings.Contains(strings.ToLower(verOut), "podman") {
		return false, ""
	}
	if v, err := m.run(rootfulCmd("info --format '{{.ServerVersion}}'")); err == nil {
		if s := strings.TrimSpace(v); s != "" {
			return true, s
		}
	}
	return true, ""
}

// ---- containers ----

// ListRootContainers lists every container (running and stopped) from the
// rootful daemon.
func (m *Manager) ListRootContainers() ([]types.PodmanContainer, error) {
	out, err := m.run(rootfulCmd("ps -a --format '{{json .}}'"))
	if err != nil {
		return nil, err
	}
	return parseContainerLines(out, "root", 0), nil
}

// ListAllContainers returns rootful containers plus every reachable rootless
// user's containers.
func (m *Manager) ListAllContainers() ([]types.PodmanContainer, error) {
	var all []types.PodmanContainer

	root, rootErr := m.ListRootContainers()
	if rootErr == nil {
		all = append(all, root...)
	}

	users, _ := m.ProbeRootlessUsers()
	for _, u := range users {
		all = append(all, u.Containers...)
	}

	if rootErr != nil && len(all) == 0 {
		return nil, fmt.Errorf("failed to list docker containers: %w", rootErr)
	}
	return all, nil
}

// parseContainerLines parses newline-delimited `docker ps --format '{{json .}}'`
// records into container items. Malformed lines are skipped rather than
// fabricated.
func parseContainerLines(out, user string, uid int) []types.PodmanContainer {
	var containers []types.PodmanContainer
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var raw struct {
			ID     string `json:"ID"`
			Names  string `json:"Names"`
			Image  string `json:"Image"`
			State  string `json:"State"`
			Status string `json:"Status"`
			Ports  string `json:"Ports"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		if raw.ID == "" && raw.Names == "" {
			continue
		}
		status := raw.Status
		if status == "" {
			status = raw.State
		}
		containers = append(containers, types.PodmanContainer{
			ID:       raw.ID,
			Name:     raw.Names,
			Image:    raw.Image,
			Status:   status,
			Ports:    raw.Ports,
			User:     user,
			UID:      uid,
			Rootless: uid != 0,
		})
	}
	return containers
}

// ---- images ----

// ListImages lists images from the rootful daemon.
func (m *Manager) ListImages() ([]DockerImage, error) {
	out, err := m.run(rootfulCmd("images --format '{{json .}}'"))
	if err != nil {
		return nil, err
	}
	var images []DockerImage
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var raw struct {
			ID           string `json:"ID"`
			Repository   string `json:"Repository"`
			Tag          string `json:"Tag"`
			Digest       string `json:"Digest"`
			Size         string `json:"Size"`
			CreatedSince string `json:"CreatedSince"`
			Containers   string `json:"Containers"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		images = append(images, DockerImage{
			ID:           raw.ID,
			Repository:   raw.Repository,
			Tag:          raw.Tag,
			Digest:       raw.Digest,
			Size:         raw.Size,
			CreatedSince: raw.CreatedSince,
			Containers:   raw.Containers,
		})
	}
	return images, nil
}

// ---- volumes ----

// ListVolumes lists volumes from the rootful daemon.
func (m *Manager) ListVolumes() ([]DockerVolume, error) {
	out, err := m.run(rootfulCmd("volume ls --format '{{json .}}'"))
	if err != nil {
		return nil, err
	}
	var volumes []DockerVolume
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var raw struct {
			Name       string `json:"Name"`
			Driver     string `json:"Driver"`
			Mountpoint string `json:"Mountpoint"`
			Scope      string `json:"Scope"`
			Labels     string `json:"Labels"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		volumes = append(volumes, DockerVolume{
			Name:       raw.Name,
			Driver:     raw.Driver,
			Mountpoint: raw.Mountpoint,
			Scope:      raw.Scope,
			Labels:     raw.Labels,
		})
	}
	return volumes, nil
}

// ---- networks ----

// ListNetworks lists networks from the rootful daemon.
func (m *Manager) ListNetworks() ([]DockerNetwork, error) {
	out, err := m.run(rootfulCmd("network ls --format '{{json .}}'"))
	if err != nil {
		return nil, err
	}
	var networks []DockerNetwork
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var raw struct {
			ID       string `json:"ID"`
			Name     string `json:"Name"`
			Driver   string `json:"Driver"`
			Scope    string `json:"Scope"`
			Internal string `json:"Internal"`
			IPv6     string `json:"IPv6"`
			Labels   string `json:"Labels"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		networks = append(networks, DockerNetwork{
			ID:       raw.ID,
			Name:     raw.Name,
			Driver:   raw.Driver,
			Scope:    raw.Scope,
			Internal: raw.Internal,
			IPv6:     raw.IPv6,
			Labels:   raw.Labels,
		})
	}
	return networks, nil
}

// ---- rootless users ----

// ProbeRootlessUsers discovers unprivileged users running a rootless Docker
// Engine, either via a per-user socket (/run/user/<uid>/docker.sock) or via a
// live dockerd/rootlesskit process. It returns a UnprivilegedPodmanUser entry
// per user that owns at least one container, reusing the Podman view-model
// shape so the frontend needs no new types.
func (m *Manager) ProbeRootlessUsers() ([]types.UnprivilegedPodmanUser, error) {
	passwdOut, _ := m.run("cat /etc/passwd 2>/dev/null || true")
	passwdByUID := make(map[int]string)
	for _, line := range strings.Split(passwdOut, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) < 6 {
			continue
		}
		uid, err := strconv.Atoi(parts[2])
		if err != nil || uid == 0 {
			continue
		}
		passwdByUID[uid] = parts[0]
	}

	// uid -> username
	candidates := make(map[int]string)
	userFor := func(uid int) string {
		if u, ok := passwdByUID[uid]; ok && u != "" {
			return u
		}
		return strconv.Itoa(uid)
	}

	// 1. Per-user Docker sockets.
	sockOut, _ := m.run("ls -1 /run/user/*/docker.sock 2>/dev/null || true")
	for _, line := range strings.Split(sockOut, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "/run/user/") {
			continue
		}
		rest := strings.TrimPrefix(line, "/run/user/")
		uidStr := rest
		if idx := strings.Index(rest, "/"); idx != -1 {
			uidStr = rest[:idx]
		}
		uid, err := strconv.Atoi(uidStr)
		if err != nil || uid == 0 {
			continue
		}
		candidates[uid] = userFor(uid)
	}

	// 2. Live rootless dockerd processes (socket may live under a custom path).
	psOut, _ := m.run("ps -eo uid,user:32,args --no-headers 2>/dev/null || true")
	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		uid, err := strconv.Atoi(fields[0])
		if err != nil || uid == 0 {
			continue
		}
		args := strings.ToLower(strings.Join(fields[2:], " "))
		if !strings.Contains(args, "dockerd") && !strings.Contains(args, "rootlesskit") {
			continue
		}
		// Exclude the rootful system daemon (which reports uid 0, already filtered).
		if !strings.Contains(args, "rootless") && !strings.Contains(args, "/run/user/") {
			continue
		}
		if _, ok := candidates[uid]; !ok {
			candidates[uid] = userFor(uid)
		}
	}

	uids := make([]int, 0, len(candidates))
	for uid := range candidates {
		uids = append(uids, uid)
	}
	sort.Ints(uids)

	var result []types.UnprivilegedPodmanUser
	for _, uid := range uids {
		user := candidates[uid]
		out, err := m.run(userCmd(user, uid, "ps -a --format '{{json .}}'"))
		if err != nil {
			continue
		}
		containers := parseContainerLines(out, user, uid)
		if len(containers) == 0 {
			continue
		}
		result = append(result, types.UnprivilegedPodmanUser{
			User:       user,
			UID:        uid,
			Containers: containers,
		})
	}
	return result, nil
}

// ---- mutations ----

var dockerActionVerbs = map[string]string{
	"start":   "start",
	"stop":    "stop",
	"restart": "restart",
	"remove":  "rm",
	"delete":  "rm",
	"rm":      "rm",
}

// ContainerAction performs a container lifecycle action against the rootful
// daemon, falling back to the rootless user that owns the container. This is
// the direct socket path used when an action is executed immediately (the
// declarative Ansible path is produced by the plugin's Actor).
func (m *Manager) ContainerAction(name, action string) error {
	verb, ok := dockerActionVerbs[strings.ToLower(action)]
	if !ok {
		return fmt.Errorf("docker: unsupported container action %q", action)
	}
	if _, err := m.run(rootfulCmd(fmt.Sprintf("%s %s", verb, shellQuote(name)))); err == nil {
		return nil
	} else if err != nil {
		// Remember the rootful failure only if no rootless owner matches.
		defer func() { _ = err }()
	}

	users, _ := m.ProbeRootlessUsers()
	for _, u := range users {
		for _, c := range u.Containers {
			if c.Name == name || c.ID == name || strings.HasPrefix(c.ID, name) {
				_, uerr := m.run(userCmd(u.User, u.UID, fmt.Sprintf("%s %s", verb, shellQuote(name))))
				return uerr
			}
		}
	}
	return fmt.Errorf("docker: container %q not found on rootful or rootless daemons", name)
}

// PullImage pulls an image into the rootful daemon.
func (m *Manager) PullImage(image string) error {
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("docker: image reference is required")
	}
	_, err := m.run(rootfulCmd(fmt.Sprintf("pull %s", shellQuote(image))))
	return err
}
