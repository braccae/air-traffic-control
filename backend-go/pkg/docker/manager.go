// Package docker inspects and mutates a Docker Engine instance over a command
// transport. It follows the socket-first HTTP API architecture while elevated
// to root:
//
//   - Rootful operations talk to the system Docker socket (/var/run/docker.sock)
//     via `curl -s --unix-socket /var/run/docker.sock http://localhost/...`
//     and are escalated in strict AGENTS.md §3 order:
//     run0 --background=no -> sudo -n -> doas -n -> direct curl -> CLI fallback.
//   - Rootless operations talk directly to per-user sockets
//     (/run/user/<uid>/docker.sock) while elevated as root first (since root
//     can read/write all user sockets), falling back to user-session curl/CLI.
//
// Zero-Mock Policy: every method reflects real Docker socket HTTP API or CLI
// output read back from the target host. Nothing is synthesised. On failure the
// underlying error is returned so the caller can surface an accurate
// offline/unavailable status.
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

const (
	// RootSocketPath is the standard system/rootful Docker Engine Unix socket.
	RootSocketPath = "/var/run/docker.sock"
)

// RootlessSocketPath returns the rootless Docker Engine Unix socket path for a given UID.
func RootlessSocketPath(uid int) string {
	return fmt.Sprintf("/run/user/%d/docker.sock", uid)
}

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

// ---- Docker Engine HTTP API & CLI JSON output shapes ----

// DockerImage is one Docker image record (from /images/json or `docker images --format '{{json .}}'`).
type DockerImage struct {
	ID           string `json:"id"`
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
	Digest       string `json:"digest,omitempty"`
	Size         string `json:"size,omitempty"`
	CreatedSince string `json:"createdSince,omitempty"`
	Containers   string `json:"containers,omitempty"`
}

// DockerVolume is one Docker volume record (from /volumes or `docker volume ls --format '{{json .}}'`).
type DockerVolume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Labels     string `json:"labels,omitempty"`
}

// DockerNetwork is one Docker network record (from /networks or `docker network ls --format '{{json .}}'`).
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

// RootSocketOrCLICommand builds a command that queries the rootful Docker socket
// (/var/run/docker.sock) via curl while elevated to root in strict AGENTS.md §3
// order (run0 --background=no -> sudo -n -> doas -n -> direct), falling back to
// the native docker CLI with the same escalation chain only if curl or socket is unavailable.
func RootSocketOrCLICommand(socketEndpoint string, cliArgs string) string {
	sockURL := fmt.Sprintf("http://localhost%s", socketEndpoint)
	const curlBase = "curl -s --unix-socket " + RootSocketPath
	return fmt.Sprintf(
		"run0 --background=no %s %s 2>/dev/null || "+
			"sudo -n %s %s 2>/dev/null || "+
			"doas -n %s %s 2>/dev/null || "+
			"%s %s 2>/dev/null || "+
			"run0 --background=no docker %s 2>/dev/null || "+
			"sudo -n docker %s 2>/dev/null || "+
			"doas -n docker %s 2>/dev/null || "+
			"docker %s 2>/dev/null",
		curlBase, sockURL,
		curlBase, sockURL,
		curlBase, sockURL,
		curlBase, sockURL,
		cliArgs,
		cliArgs,
		cliArgs,
		cliArgs,
	)
}

// UserSocketOrCLICommand builds a command that queries a rootless user's Docker socket
// (/run/user/<uid>/docker.sock) while elevated to root first (run0 -> sudo -n -> doas -n),
// then user-level curl, falling back to the native docker CLI in the user session.
func UserSocketOrCLICommand(user string, uid int, socketEndpoint string, cliArgs string) string {
	sockPath := RootlessSocketPath(uid)
	sockURL := fmt.Sprintf("http://localhost%s", socketEndpoint)
	curlCmd := fmt.Sprintf("curl -s --unix-socket %s %s", sockPath, sockURL)
	return fmt.Sprintf(
		"run0 --background=no %s 2>/dev/null || "+
			"sudo -n %s 2>/dev/null || "+
			"doas -n %s 2>/dev/null || "+
			"run0 --background=no --user %s %s 2>/dev/null || "+
			"(cd / && sudo -n -u %s %s 2>/dev/null) || "+
			"(cd / && doas -n -u %s %s 2>/dev/null) || "+
			"%s 2>/dev/null || "+
			"run0 --background=no --user %s docker %s 2>/dev/null || "+
			"(cd / && sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d docker %s 2>/dev/null) || "+
			"(cd / && doas -n -u %s XDG_RUNTIME_DIR=/run/user/%d docker %s 2>/dev/null) || "+
			"XDG_RUNTIME_DIR=/run/user/%d docker %s 2>/dev/null",
		curlCmd,
		curlCmd,
		curlCmd,
		user, curlCmd,
		user, curlCmd,
		user, curlCmd,
		curlCmd,
		user, cliArgs,
		user, uid, cliArgs,
		user, uid, cliArgs,
		uid, cliArgs,
	)
}

// rootfulCmd builds a command that talks to the rootful (system) Docker CLI,
// preferring run0 escalation, then passwordless sudo, then doas, then a direct call.
func rootfulCmd(args string) string {
	return fmt.Sprintf(
		"run0 --background=no docker %s 2>/dev/null || sudo -n docker %s 2>/dev/null || doas -n docker %s 2>/dev/null || docker %s 2>/dev/null",
		args, args, args, args,
	)
}

// userCmd builds a command that talks to a rootless user's Docker CLI,
// preferring run0 --user, then sudo -u, then doas -u, then a direct call in the user session.
func userCmd(user string, uid int, args string) string {
	return fmt.Sprintf(
		"run0 --background=no --user %s docker %s 2>/dev/null || (cd / && sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d docker %s 2>/dev/null) || (cd / && doas -n -u %s XDG_RUNTIME_DIR=/run/user/%d docker %s 2>/dev/null) || XDG_RUNTIME_DIR=/run/user/%d docker %s 2>/dev/null",
		user, args, user, uid, args, user, uid, args, uid, args,
	)
}

// IsPodmanVersionJSON inspects a `/version` HTTP response or CLI output and
// returns true if it indicates Podman / Libpod emulation rather than genuine
// Docker Engine.
func IsPodmanVersionJSON(raw string) bool {
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "podman") || strings.Contains(lower, "libpod") {
		return true
	}
	return false
}

// ParseVersionJSON extracts the Docker Engine ServerVersion from `/version` JSON
// (or plain text fallback) and reports whether it is genuine Docker (not Podman).
func ParseVersionJSON(raw string) (validDocker bool, version string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || IsPodmanVersionJSON(trimmed) {
		return false, ""
	}
	if strings.HasPrefix(trimmed, "{") {
		var ver struct {
			Version    string `json:"Version"`
			ApiVersion string `json:"ApiVersion"`
			Platform   struct {
				Name string `json:"Name"`
			} `json:"Platform"`
			Components []struct {
				Name    string `json:"Name"`
				Version string `json:"Version"`
			} `json:"Components"`
		}
		if err := json.Unmarshal([]byte(trimmed), &ver); err == nil {
			if strings.Contains(strings.ToLower(ver.Platform.Name), "podman") ||
				strings.Contains(strings.ToLower(ver.Platform.Name), "libpod") {
				return false, ""
			}
			for _, comp := range ver.Components {
				cn := strings.ToLower(comp.Name)
				if strings.Contains(cn, "podman") || strings.Contains(cn, "libpod") {
					return false, ""
				}
			}
			if ver.Version != "" {
				return true, ver.Version
			}
			if len(ver.Components) > 0 && ver.Components[0].Version != "" {
				return true, ver.Components[0].Version
			}
			if ver.ApiVersion != "" {
				return true, ver.ApiVersion
			}
		}
		return false, ""
	}
	// Plain-text version string from `docker info --format '{{.ServerVersion}}'`
	return true, trimmed
}

// Probe reports whether the Docker Engine socket (or CLI) is present, reachable,
// and NOT a podman-docker emulation symlink. It returns the server version when known.
func (m *Manager) Probe() (bool, string) {
	verCheck, _ := m.run("(docker --version 2>&1; readlink /var/run/docker.sock /run/docker.sock 2>/dev/null) || true")
	if IsPodmanVersionJSON(verCheck) {
		return false, ""
	}

	// 1. Try querying /version over /var/run/docker.sock elevated to root.
	vOut, vErr := m.run(RootSocketOrCLICommand("/version", "info --format '{{.ServerVersion}}'"))
	if vErr == nil && strings.TrimSpace(vOut) != "" {
		if IsPodmanVersionJSON(vOut) {
			return false, ""
		}
		if ok, ver := ParseVersionJSON(vOut); ok {
			return true, ver
		}
	}

	// 2. Fallback: check `command -v docker`.
	out, err := m.run("command -v docker")
	if err != nil || strings.TrimSpace(out) == "" {
		return false, ""
	}
	return true, ""
}

// ---- containers ----

// ListRootContainers lists every container (running and stopped) from the
// rootful Docker Engine via `curl -s --unix-socket /var/run/docker.sock http://localhost/containers/json?all=1`
// while elevated to root (falling back to CLI if curl/socket is unavailable).
func (m *Manager) ListRootContainers() ([]types.PodmanContainer, error) {
	out, err := m.run(RootSocketOrCLICommand("/containers/json?all=1", "ps -a --format '{{json .}}'"))
	if err != nil {
		return nil, err
	}
	return ParseDockerContainersJSON(out, "root", 0), nil
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

// dockerAPIPort represents one entry in the `Ports` array returned by the
// Docker Engine HTTP API (`/containers/json?all=1`).
type dockerAPIPort struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

func formatDockerAPIPorts(rawPorts any) string {
	switch v := rawPorts.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		seen := make(map[string]bool)
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			ip, _ := m["IP"].(string)
			proto, _ := m["Type"].(string)
			if proto == "" {
				proto = "tcp"
			}
			priv := toInt(m["PrivatePort"])
			pub := toInt(m["PublicPort"])
			var formatted string
			if pub > 0 {
				if ip != "" {
					formatted = fmt.Sprintf("%s:%d->%d/%s", ip, pub, priv, proto)
				} else {
					formatted = fmt.Sprintf("%d->%d/%s", pub, priv, proto)
				}
			} else if priv > 0 {
				formatted = fmt.Sprintf("%d/%s", priv, proto)
			}
			if formatted != "" && !seen[formatted] {
				seen[formatted] = true
				parts = append(parts, formatted)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

// ParseDockerContainersJSON parses either:
//  1. A JSON array from the Docker Engine socket API (`GET /containers/json?all=1`),
//  2. Newline-delimited JSON objects from `docker ps -a --format '{{json .}}'`, or
//  3. `:::` delimited fallback lines.
func ParseDockerContainersJSON(out, user string, uid int) []types.PodmanContainer {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "null" || strings.Contains(trimmed, "NO_DOCKER") {
		return nil
	}

	// 1. Docker Engine HTTP API JSON array (`/containers/json?all=1`)
	if strings.HasPrefix(trimmed, "[") {
		var rawList []struct {
			Id      string            `json:"Id"`
			ID      string            `json:"ID"`
			Names   any               `json:"Names"`
			Image   string            `json:"Image"`
			State   string            `json:"State"`
			Status  string            `json:"Status"`
			Ports   any               `json:"Ports"`
			Labels  map[string]string `json:"Labels"`
			Created int64             `json:"Created"`
		}
		if err := json.Unmarshal([]byte(trimmed), &rawList); err == nil {
			var containers []types.PodmanContainer
			for _, rc := range rawList {
				cid := rc.Id
				if cid == "" {
					cid = rc.ID
				}
				if len(cid) > 12 {
					cid = cid[:12]
				}

				name := ""
				switch nv := rc.Names.(type) {
				case []any:
					if len(nv) > 0 {
						if s, ok := nv[0].(string); ok {
							name = strings.TrimPrefix(s, "/")
						}
					}
				case string:
					name = strings.TrimPrefix(nv, "/")
				}
				if name == "" {
					name = cid
				}
				if cid == "" && name == "" {
					continue
				}

				nameLower := strings.ToLower(name)
				imgLower := strings.ToLower(rc.Image)
				if strings.Contains(nameLower, "pause") ||
					strings.HasSuffix(nameLower, "-infra") ||
					strings.Contains(imgLower, "pause") ||
					strings.Contains(imgLower, "podman-pause") {
					continue
				}

				status := rc.Status
				if status == "" {
					status = rc.State
				}

				var composeProject, composeService, composeConfigFiles string
				if rc.Labels != nil {
					composeProject = strings.TrimSpace(rc.Labels["com.docker.compose.project"])
					composeService = strings.TrimSpace(rc.Labels["com.docker.compose.service"])
					composeConfigFiles = strings.TrimSpace(rc.Labels["com.docker.compose.project.config_files"])
				}

				containers = append(containers, types.PodmanContainer{
					ID:                 cid,
					Name:               name,
					Image:              rc.Image,
					Status:             status,
					Ports:              formatDockerAPIPorts(rc.Ports),
					User:               user,
					UID:                uid,
					Rootless:           uid != 0,
					ComposeProject:     composeProject,
					ComposeService:     composeService,
					ComposeConfigFiles: composeConfigFiles,
				})
			}
			return containers
		}
	}

	// 2. Fallback: newline-delimited CLI JSON (`docker ps -a --format '{{json .}}'`) or `:::` lines
	return parseContainerLines(out, user, uid)
}

// parseContainerLines parses newline-delimited `docker ps --format '{{json .}}'`
// records or `:::` delimited records into container items. Malformed lines are
// skipped rather than fabricated.
func parseContainerLines(out, user string, uid int) []types.PodmanContainer {
	var containers []types.PodmanContainer
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ":::") && !strings.Contains(line, "{{") {
			parts := strings.Split(line, ":::")
			if len(parts) >= 4 {
				id, name, image, status := strings.TrimSpace(parts[0]), strings.TrimPrefix(strings.TrimSpace(parts[1]), "/"), strings.TrimSpace(parts[2]), strings.TrimSpace(parts[3])
				if id == "" || name == "" {
					continue
				}
				nameLower := strings.ToLower(name)
				imgLower := strings.ToLower(image)
				if strings.Contains(nameLower, "pause") ||
					strings.HasSuffix(nameLower, "-infra") ||
					strings.Contains(imgLower, "pause") ||
					strings.Contains(imgLower, "podman-pause") {
					continue
				}
				if len(id) > 12 {
					id = id[:12]
				}
				var composeProject, composeService, composeConfigFiles string
				if len(parts) > 4 {
					composeProject = strings.TrimSpace(parts[4])
				}
				if len(parts) > 5 {
					composeService = strings.TrimSpace(parts[5])
				}
				if len(parts) > 6 {
					composeConfigFiles = strings.TrimSpace(parts[6])
				}
				containers = append(containers, types.PodmanContainer{
					ID:                 id,
					Name:               name,
					Image:              image,
					Status:             status,
					User:               user,
					UID:                uid,
					Rootless:           uid != 0,
					ComposeProject:     composeProject,
					ComposeService:     composeService,
					ComposeConfigFiles: composeConfigFiles,
				})
			}
			continue
		}
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var raw struct {
			ID     string `json:"ID"`
			Id     string `json:"Id"`
			Names  any    `json:"Names"`
			Image  string `json:"Image"`
			State  string `json:"State"`
			Status string `json:"Status"`
			Ports  any    `json:"Ports"`
			Labels any    `json:"Labels"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		cid := raw.ID
		if cid == "" {
			cid = raw.Id
		}
		if len(cid) > 12 {
			cid = cid[:12]
		}
		var name string
		switch nv := raw.Names.(type) {
		case string:
			name = strings.TrimPrefix(nv, "/")
		case []any:
			if len(nv) > 0 {
				if s, ok := nv[0].(string); ok {
					name = strings.TrimPrefix(s, "/")
				}
			}
		}
		if cid == "" && name == "" {
			continue
		}
		status := raw.Status
		if status == "" {
			status = raw.State
		}
		var composeProject, composeService, composeConfigFiles string
		switch lv := raw.Labels.(type) {
		case map[string]any:
			if v, ok := lv["com.docker.compose.project"].(string); ok {
				composeProject = strings.TrimSpace(v)
			}
			if v, ok := lv["com.docker.compose.service"].(string); ok {
				composeService = strings.TrimSpace(v)
			}
			if v, ok := lv["com.docker.compose.project.config_files"].(string); ok {
				composeConfigFiles = strings.TrimSpace(v)
			}
		case string:
			for _, kv := range strings.Split(lv, ",") {
				if k, v, ok := strings.Cut(kv, "="); ok {
					switch strings.TrimSpace(k) {
					case "com.docker.compose.project":
						composeProject = strings.TrimSpace(v)
					case "com.docker.compose.service":
						composeService = strings.TrimSpace(v)
					case "com.docker.compose.project.config_files":
						composeConfigFiles = strings.TrimSpace(v)
					}
				}
			}
		}
		containers = append(containers, types.PodmanContainer{
			ID:                 cid,
			Name:               name,
			Image:              raw.Image,
			Status:             status,
			Ports:              formatDockerAPIPorts(raw.Ports),
			User:               user,
			UID:                uid,
			Rootless:           uid != 0,
			ComposeProject:     composeProject,
			ComposeService:     composeService,
			ComposeConfigFiles: composeConfigFiles,
		})
	}
	return containers
}

// ---- images ----

func formatImageBytes(size int64) string {
	if size <= 0 {
		return ""
	}
	const (
		kb = 1000
		mb = 1000 * kb
		gb = 1000 * mb
	)
	switch {
	case size >= gb:
		return fmt.Sprintf("%.2fGB", float64(size)/float64(gb))
	case size >= mb:
		return fmt.Sprintf("%.1fMB", float64(size)/float64(mb))
	case size >= kb:
		return fmt.Sprintf("%.1fkB", float64(size)/float64(kb))
	default:
		return fmt.Sprintf("%dB", size)
	}
}

// ListImages lists images from the rootful daemon via `/images/json` over the
// Docker socket (falling back to CLI `docker images --format '{{json .}}'`).
func (m *Manager) ListImages() ([]DockerImage, error) {
	out, err := m.run(RootSocketOrCLICommand("/images/json", "images --format '{{json .}}'"))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(out)
	if strings.HasPrefix(trimmed, "[") {
		var apiImages []struct {
			ID          string   `json:"Id"`
			RepoTags    []string `json:"RepoTags"`
			RepoDigests []string `json:"RepoDigests"`
			Size        int64    `json:"Size"`
			Containers  int64    `json:"Containers"`
		}
		if err := json.Unmarshal([]byte(trimmed), &apiImages); err == nil {
			var images []DockerImage
			for _, img := range apiImages {
				shortID := strings.TrimPrefix(img.ID, "sha256:")
				if len(shortID) > 12 {
					shortID = shortID[:12]
				}
				repo := "<none>"
				tag := "<none>"
				if len(img.RepoTags) > 0 && img.RepoTags[0] != "" {
					if r, t, ok := strings.Cut(img.RepoTags[0], ":"); ok {
						repo, tag = r, t
					} else {
						repo = img.RepoTags[0]
					}
				}
				digest := ""
				if len(img.RepoDigests) > 0 {
					if _, d, ok := strings.Cut(img.RepoDigests[0], "@"); ok {
						digest = d
					} else {
						digest = img.RepoDigests[0]
					}
				}
				containersStr := ""
				if img.Containers >= 0 {
					containersStr = strconv.FormatInt(img.Containers, 10)
				}
				images = append(images, DockerImage{
					ID:         shortID,
					Repository: repo,
					Tag:        tag,
					Digest:     digest,
					Size:       formatImageBytes(img.Size),
					Containers: containersStr,
				})
			}
			return images, nil
		}
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

func formatLabelsMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, m[k]))
	}
	return strings.Join(parts, ",")
}

// ListVolumes lists volumes from the rootful daemon via `/volumes` over the
// Docker socket (falling back to CLI `docker volume ls --format '{{json .}}'`).
func (m *Manager) ListVolumes() ([]DockerVolume, error) {
	out, err := m.run(RootSocketOrCLICommand("/volumes", "volume ls --format '{{json .}}'"))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(out)
	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "\"Volumes\"") {
		var env struct {
			Volumes []struct {
				Name       string            `json:"Name"`
				Driver     string            `json:"Driver"`
				Mountpoint string            `json:"Mountpoint"`
				Scope      string            `json:"Scope"`
				Labels     map[string]string `json:"Labels"`
			} `json:"Volumes"`
		}
		if err := json.Unmarshal([]byte(trimmed), &env); err == nil {
			var volumes []DockerVolume
			for _, v := range env.Volumes {
				volumes = append(volumes, DockerVolume{
					Name:       v.Name,
					Driver:     v.Driver,
					Mountpoint: v.Mountpoint,
					Scope:      v.Scope,
					Labels:     formatLabelsMap(v.Labels),
				})
			}
			return volumes, nil
		}
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

// ListNetworks lists networks from the rootful daemon via `/networks` over the
// Docker socket (falling back to CLI `docker network ls --format '{{json .}}'`).
func (m *Manager) ListNetworks() ([]DockerNetwork, error) {
	out, err := m.run(RootSocketOrCLICommand("/networks", "network ls --format '{{json .}}'"))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(out)
	if strings.HasPrefix(trimmed, "[") {
		var apiNets []struct {
			ID         string            `json:"Id"`
			Name       string            `json:"Name"`
			Driver     string            `json:"Driver"`
			Scope      string            `json:"Scope"`
			Internal   bool              `json:"Internal"`
			EnableIPv6 bool              `json:"EnableIPv6"`
			Labels     map[string]string `json:"Labels"`
		}
		if err := json.Unmarshal([]byte(trimmed), &apiNets); err == nil {
			var networks []DockerNetwork
			for _, n := range apiNets {
				shortID := n.ID
				if len(shortID) > 12 {
					shortID = shortID[:12]
				}
				networks = append(networks, DockerNetwork{
					ID:       shortID,
					Name:     n.Name,
					Driver:   n.Driver,
					Scope:    n.Scope,
					Internal: strconv.FormatBool(n.Internal),
					IPv6:     strconv.FormatBool(n.EnableIPv6),
					Labels:   formatLabelsMap(n.Labels),
				})
			}
			return networks, nil
		}
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
// Engine, either via a per-user socket (/run/user/<uid>/docker.sock) probed
// while elevated to root or via a live dockerd/rootlesskit process. It returns
// an UnprivilegedPodmanUser entry per user that owns at least one container.
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

	// 1. Per-user Docker sockets (probed with root elevation first so 0700 /run/user/<uid> dirs are visible).
	sockOut, _ := m.run("run0 --background=no sh -c 'ls -1 /run/user/*/docker.sock 2>/dev/null' 2>/dev/null || sudo -n sh -c 'ls -1 /run/user/*/docker.sock 2>/dev/null' 2>/dev/null || doas -n sh -c 'ls -1 /run/user/*/docker.sock 2>/dev/null' 2>/dev/null || ls -1 /run/user/*/docker.sock 2>/dev/null || true")
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
		out, err := m.run(UserSocketOrCLICommand(user, uid, "/containers/json?all=1", "ps -a --format '{{json .}}'"))
		if err != nil {
			continue
		}
		containers := ParseDockerContainersJSON(out, user, uid)
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

func buildDockerSocketActionCmd(sockPath, name, verb string) string {
	method := "POST"
	endpoint := fmt.Sprintf("http://localhost/containers/%s/%s", name, verb)
	if verb == "rm" {
		method = "DELETE"
		endpoint = fmt.Sprintf("http://localhost/containers/%s?force=true", name)
	}
	curlCmd := fmt.Sprintf("curl -sf -X %s --unix-socket %s %s", method, sockPath, shellQuote(endpoint))
	cliCmd := fmt.Sprintf("docker %s %s", verb, shellQuote(name))
	return fmt.Sprintf(
		"run0 --background=no %s 2>/dev/null || "+
			"sudo -n %s 2>/dev/null || "+
			"doas -n %s 2>/dev/null || "+
			"%s 2>/dev/null || "+
			"run0 --background=no %s 2>/dev/null || "+
			"sudo -n %s 2>/dev/null || "+
			"doas -n %s 2>/dev/null || "+
			"%s 2>/dev/null",
		curlCmd, curlCmd, curlCmd, curlCmd,
		cliCmd, cliCmd, cliCmd, cliCmd,
	)
}

// ContainerAction performs a container lifecycle action against the rootful
// Docker socket via `curl -s -X POST --unix-socket /var/run/docker.sock http://localhost/containers/<id>/<action>`
// (or DELETE for remove), falling back to the rootless user socket that owns the container.
func (m *Manager) ContainerAction(name, action string) error {
	verb, ok := dockerActionVerbs[strings.ToLower(action)]
	if !ok {
		return fmt.Errorf("docker: unsupported container action %q", action)
	}
	if _, err := m.run(buildDockerSocketActionCmd(RootSocketPath, name, verb)); err == nil {
		return nil
	}

	users, _ := m.ProbeRootlessUsers()
	for _, u := range users {
		for _, c := range u.Containers {
			if c.Name == name || c.ID == name || strings.HasPrefix(c.ID, name) {
				uSock := RootlessSocketPath(u.UID)
				method := "POST"
				endpoint := fmt.Sprintf("http://localhost/containers/%s/%s", name, verb)
				if verb == "rm" {
					method = "DELETE"
					endpoint = fmt.Sprintf("http://localhost/containers/%s?force=true", name)
				}
				curlCmd := fmt.Sprintf("curl -sf -X %s --unix-socket %s %s", method, uSock, shellQuote(endpoint))
				cliArgs := fmt.Sprintf("%s %s", verb, shellQuote(name))
				cmd := fmt.Sprintf(
					"run0 --background=no %s 2>/dev/null || "+
						"sudo -n %s 2>/dev/null || "+
						"doas -n %s 2>/dev/null || "+
						"%s 2>/dev/null || "+
						"%s",
					curlCmd, curlCmd, curlCmd, curlCmd,
					userCmd(u.User, u.UID, cliArgs),
				)
				_, uerr := m.run(cmd)
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
