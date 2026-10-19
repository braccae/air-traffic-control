package podman

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"prox-orch/backend/pkg/ssh"
	"prox-orch/backend/pkg/types"
)

// CommandRunner abstracts remote command execution (implemented by *ssh.ClientManager)
type CommandRunner interface {
	RunCommand(cfg ssh.Config, command string) (string, error)
}

type Manager struct {
	sshMgr CommandRunner
	cfg    ssh.Config
}

func NewManager(sshMgr CommandRunner, cfg ssh.Config) *Manager {
	return &Manager{
		sshMgr: sshMgr,
		cfg:    cfg,
	}
}

var podmanProcessSignatures = []string{
	"podman",
	"conmon",
	"catatonit",
	"pasta",
	"slirp4netns",
	"aardvark-dns",
	"rootlessport",
	"fuse-overlayfs",
	"crun",
}

func isPodmanProcess(cmdline string) bool {
	lower := strings.ToLower(cmdline)
	for _, sig := range podmanProcessSignatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// ProbeUnprivilegedUsers discovers unprivileged (non-root) users running Podman containers
func (m *Manager) ProbeUnprivilegedUsers() ([]types.UnprivilegedPodmanUser, error) {
	psOut, err := m.sshMgr.RunCommand(m.cfg, "ps -eo uid,user:32,args --no-headers")
	if err != nil {
		return nil, fmt.Errorf("failed to probe process table: %w", err)
	}

	passwdOut, _ := m.sshMgr.RunCommand(m.cfg, "cat /etc/passwd")
	subuidOut, _ := m.sshMgr.RunCommand(m.cfg, "cat /etc/subuid")
	lingerOut, _ := m.sshMgr.RunCommand(m.cfg, "ls -1 /var/lib/systemd/linger 2>/dev/null || true")
	runUserOut, _ := m.sshMgr.RunCommand(m.cfg, "ls -1d /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null || true")

	candidates := make(map[string]*types.UnprivilegedPodmanUser)

	// Parse /etc/passwd for UID/GID/HomeDir lookup
	passwdByUser := make(map[string]types.UnprivilegedPodmanUser)
	passwdByUID := make(map[int]types.UnprivilegedPodmanUser)
	for _, line := range strings.Split(passwdOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 6 {
			continue
		}
		user := parts[0]
		uid, err := strconv.Atoi(parts[2])
		if err != nil || uid == 0 {
			continue
		}
		gid, _ := strconv.Atoi(parts[3])
		home := parts[5]
		entry := types.UnprivilegedPodmanUser{
			User:    user,
			UID:     uid,
			GID:     gid,
			HomeDir: home,
		}
		passwdByUser[user] = entry
		passwdByUID[uid] = entry
	}

	// Parse /etc/subuid for rootless namespace mappings
	subuidByUser := make(map[string]string)
	for _, line := range strings.Split(subuidOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) >= 3 {
			user := parts[0]
			if user == "root" {
				continue
			}
			subuidByUser[user] = parts[1] + ":" + parts[2]
			if pw, ok := passwdByUser[user]; ok {
				u := pw
				u.SubUIDRange = subuidByUser[user]
				candidates[user] = &u
			} else {
				candidates[user] = &types.UnprivilegedPodmanUser{
					User:        user,
					SubUIDRange: subuidByUser[user],
				}
			}
		}
	}

	// Parse linger directory
	lingerUsers := make(map[string]bool)
	for _, line := range strings.Split(lingerOut, "\n") {
		user := strings.TrimSpace(line)
		if user != "" && user != "root" {
			lingerUsers[user] = true
			if c, ok := candidates[user]; ok {
				c.Linger = true
			} else if pw, ok := passwdByUser[user]; ok {
				u := pw
				u.Linger = true
				candidates[user] = &u
			}
		}
	}

	// Parse /run/user/<uid>/* runtime directories
	for _, line := range strings.Split(runUserOut, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "/run/user/") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(line, "/run/user/"), "/")
		if len(parts) == 0 {
			continue
		}
		uid, err := strconv.Atoi(parts[0])
		if err != nil || uid == 0 {
			continue
		}
		if pw, ok := passwdByUID[uid]; ok {
			if _, exists := candidates[pw.User]; !exists {
				u := pw
				u.SubUIDRange = subuidByUser[pw.User]
				u.Linger = lingerUsers[pw.User]
				candidates[pw.User] = &u
			}
		}
	}

	// Parse ps output for active Podman runtime processes owned by unprivileged users (uid > 0)
	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		uid, err := strconv.Atoi(fields[0])
		if err != nil || uid == 0 {
			continue
		}
		user := fields[1]
		// Resolve truncated usernames (e.g. "longusern+") or numeric user strings via passwdByUID
		if pw, ok := passwdByUID[uid]; ok {
			user = pw.User
		}
		if user == "root" {
			continue
		}
		args := strings.Join(fields[2:], " ")
		if !isPodmanProcess(args) {
			continue
		}

		c, exists := candidates[user]
		if !exists {
			if pw, ok := passwdByUser[user]; ok {
				u := pw
				c = &u
			} else {
				c = &types.UnprivilegedPodmanUser{
					User: user,
					UID:  uid,
				}
			}
			c.SubUIDRange = subuidByUser[user]
			c.Linger = lingerUsers[user]
			candidates[user] = c
		}
		if c.UID == 0 {
			c.UID = uid
		}
		c.ActiveProcesses = append(c.ActiveProcesses, args)
	}

	// Deterministic order of candidates
	var usernames []string
	for u := range candidates {
		usernames = append(usernames, u)
	}
	sort.Strings(usernames)

	var activeUsers []types.UnprivilegedPodmanUser
	for _, username := range usernames {
		cand := candidates[username]
		if cand.UID == 0 {
			if pw, ok := passwdByUser[username]; ok {
				cand.UID = pw.UID
				cand.GID = pw.GID
				cand.HomeDir = pw.HomeDir
			}
		}
		if cand.UID == 0 || cand.User == "root" {
			continue
		}
		cand.Linger = lingerUsers[username]
		if cand.SubUIDRange == "" {
			cand.SubUIDRange = subuidByUser[username]
		}

		containers, err := m.listUserContainers(cand.User, cand.UID)
		if err != nil || len(containers) == 0 {
			if fallback := extractContainersFromProcesses(cand.ActiveProcesses, cand.User, cand.UID); len(fallback) > 0 {
				containers = fallback
			}
		}
		pods, _ := m.listUserPods(cand.User, cand.UID)

		if len(containers) > 0 || len(pods) > 0 {
			cand.Containers = containers
			cand.Pods = pods
			activeUsers = append(activeUsers, *cand)
		}
	}

	return activeUsers, nil
}

func extractContainersFromProcesses(processes []string, user string, uid int) []types.PodmanContainer {
	var containers []types.PodmanContainer
	seen := make(map[string]bool)

	for _, proc := range processes {
		lower := strings.ToLower(proc)
		if !strings.Contains(lower, "conmon") {
			continue
		}
		tokens := strings.Fields(proc)
		var cid, cname string
		for i := 0; i < len(tokens); i++ {
			tok := tokens[i]
			switch {
			case (tok == "-c" || tok == "--cid") && i+1 < len(tokens):
				cid = tokens[i+1]
				i++
			case strings.HasPrefix(tok, "--cid="):
				cid = strings.TrimPrefix(tok, "--cid=")
			case strings.HasPrefix(tok, "-c") && len(tok) > 2 && !strings.HasPrefix(tok, "--"):
				cid = tok[2:]
			case (tok == "-n" || tok == "--name") && i+1 < len(tokens):
				cname = tokens[i+1]
				i++
			case strings.HasPrefix(tok, "--name="):
				cname = strings.TrimPrefix(tok, "--name=")
			case strings.HasPrefix(tok, "-n") && len(tok) > 2 && !strings.HasPrefix(tok, "--"):
				cname = tok[2:]
			}
		}
		if cid == "" && cname == "" {
			continue
		}
		if cid == "" {
			cid = cname
		}
		if cname == "" {
			cname = cid
		}
		if seen[cid] {
			continue
		}
		seen[cid] = true
		containers = append(containers, types.PodmanContainer{
			ID:       cid,
			Name:     cname,
			Image:    cname + ":latest",
			Status:   "Running",
			User:     user,
			UID:      uid,
			Rootless: uid != 0,
		})
	}
	// Do not synthesize podman-pause for idle namespaces
	return containers
}

func parseContainersJSON(out string, user string, uid int) ([]types.PodmanContainer, error) {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return nil, nil
	}

	var rawContainers []struct {
		ID     string      `json:"Id"`
		Names  interface{} `json:"Names"`
		Image  string      `json:"Image"`
		State  string      `json:"State"`
		Status string      `json:"Status"`
		Pod    string      `json:"Pod"`
	}

	if err := json.Unmarshal([]byte(out), &rawContainers); err != nil {
		return nil, fmt.Errorf("failed to parse containers json for user %s: %w", user, err)
	}

	var containers []types.PodmanContainer
	for _, rc := range rawContainers {
		name := rc.ID
		switch v := rc.Names.(type) {
		case []interface{}:
			if len(v) > 0 {
				if s, ok := v[0].(string); ok {
					name = s
				}
			}
		case string:
			if v != "" {
				name = v
			}
		}

		status := rc.Status
		if status == "" {
			status = rc.State
		}

		nameLower := strings.ToLower(name)
		imgLower := strings.ToLower(rc.Image)
		if strings.Contains(nameLower, "pause") || strings.HasSuffix(nameLower, "-infra") || strings.Contains(imgLower, "pause") || strings.Contains(imgLower, "podman-pause") {
			continue
		}

		containers = append(containers, types.PodmanContainer{
			ID:       rc.ID,
			PodID:    rc.Pod,
			Name:     name,
			Image:    rc.Image,
			Status:   status,
			User:     user,
			UID:      uid,
			Rootless: uid != 0,
		})
	}
	return containers, nil
}

func (m *Manager) listUserContainers(user string, uid int) ([]types.PodmanContainer, error) {
	cmd := fmt.Sprintf("run0 --background=no --user %s podman ps --format json 2>/dev/null || cd / && sudo -n -i -u %s XDG_RUNTIME_DIR=/run/user/%d podman ps --format json", user, user, uid)
	out, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err != nil {
		suCmd := fmt.Sprintf("su - %s -s /bin/sh -c \"XDG_RUNTIME_DIR=/run/user/%d podman ps --format json\"", user, uid)
		out, err = m.sshMgr.RunCommand(m.cfg, suCmd)
		if err != nil {
			directCmd := fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d podman ps --format json", uid)
			out, err = m.sshMgr.RunCommand(m.cfg, directCmd)
			if err != nil {
				return nil, err
			}
		}
	}
	return parseContainersJSON(out, user, uid)
}

func (m *Manager) listUserPods(user string, uid int) ([]types.PodmanPod, error) {
	cmd := fmt.Sprintf("run0 --background=no --user %s podman pod ps --format json 2>/dev/null || cd / && sudo -n -i -u %s XDG_RUNTIME_DIR=/run/user/%d podman pod ps --format json", user, user, uid)
	out, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err != nil {
		suCmd := fmt.Sprintf("su - %s -s /bin/sh -c \"XDG_RUNTIME_DIR=/run/user/%d podman pod ps --format json\"", user, uid)
		out, err = m.sshMgr.RunCommand(m.cfg, suCmd)
		if err != nil {
			directCmd := fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d podman pod ps --format json", uid)
			out, err = m.sshMgr.RunCommand(m.cfg, directCmd)
			if err != nil {
				return nil, err
			}
		}
	}

	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return nil, nil
	}

	var rawPods []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Status string `json:"Status"`
	}

	if err := json.Unmarshal([]byte(out), &rawPods); err != nil {
		return nil, fmt.Errorf("failed to parse pods json for user %s: %w", user, err)
	}

	var pods []types.PodmanPod
	for _, rp := range rawPods {
		pods = append(pods, types.PodmanPod{
			ID:       rp.ID,
			Name:     rp.Name,
			Status:   rp.Status,
			User:     user,
			UID:      uid,
			Rootless: uid != 0,
		})
	}
	return pods, nil
}

// ListAllContainers returns all containers across both root and unprivileged users
func (m *Manager) ListAllContainers() ([]types.PodmanContainer, error) {
	var all []types.PodmanContainer

	out, err := m.sshMgr.RunCommand(m.cfg, "run0 --background=no podman ps --all --format json 2>/dev/null || podman ps --all --format json")
	if err == nil {
		if rootContainers, parseErr := parseContainersJSON(out, "root", 0); parseErr == nil {
			all = append(all, rootContainers...)
		}
	}

	unprivUsers, probeErr := m.ProbeUnprivilegedUsers()
	if probeErr != nil && err != nil {
		return nil, fmt.Errorf("failed to list root and rootless containers: %w", err)
	}
	for _, u := range unprivUsers {
		all = append(all, u.Containers...)
	}

	return all, nil
}

// ListPods queries podman pod ps --format json for both root and unprivileged users
func (m *Manager) ListPods() ([]types.PodmanPod, error) {
	var pods []types.PodmanPod

	cmd := "run0 --background=no podman pod ps --format json 2>/dev/null || podman pod ps --format json"
	out, rootErr := m.sshMgr.RunCommand(m.cfg, cmd)
	if rootErr == nil {
		var rawPods []struct {
			ID     string `json:"Id"`
			Name   string `json:"Name"`
			Status string `json:"Status"`
		}
		if parseErr := json.Unmarshal([]byte(out), &rawPods); parseErr != nil {
			rootErr = fmt.Errorf("failed to parse pods json: %w", parseErr)
		} else {
			for _, rp := range rawPods {
				pods = append(pods, types.PodmanPod{
					ID:       rp.ID,
					Name:     rp.Name,
					Status:   rp.Status,
					User:     "root",
					UID:      0,
					Rootless: false,
				})
			}
		}
	}

	// Probe unprivileged users for rootless pods and containers
	unprivUsers, probeErr := m.ProbeUnprivilegedUsers()
	if probeErr == nil {
		for _, u := range unprivUsers {
			for _, p := range u.Pods {
				var podContainers []types.PodmanContainer
				for _, c := range u.Containers {
					if c.PodID == p.ID {
						podContainers = append(podContainers, c)
					}
				}
				p.Containers = podContainers
				pods = append(pods, p)
			}
		}
	}

	if rootErr != nil && len(pods) == 0 {
		return nil, fmt.Errorf("failed to list pods: %w", rootErr)
	}

	return pods, nil
}

// RestartPod restarts a pod (searching unprivileged users if not found in root)
func (m *Manager) RestartPod(podName string) error {
	cmd := fmt.Sprintf("run0 --background=no podman pod restart %s 2>/dev/null || podman pod restart %s", podName, podName)
	_, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err == nil {
		return nil
	}

	if unprivUsers, probeErr := m.ProbeUnprivilegedUsers(); probeErr == nil {
		for _, u := range unprivUsers {
			for _, p := range u.Pods {
				if p.Name == podName || p.ID == podName {
					userCmd := fmt.Sprintf("run0 --background=no --user %s podman pod restart %s 2>/dev/null || sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman pod restart %s", u.User, podName, u.User, u.UID, podName)
					_, userErr := m.sshMgr.RunCommand(m.cfg, userCmd)
					return userErr
				}
			}
		}
	}
	return err
}

// ContainerAction performs start, stop, restart, or inspect (falling back to unprivileged user if needed)
func (m *Manager) ContainerAction(containerName, action string) error {
	cmd := fmt.Sprintf("run0 --background=no podman %s %s 2>/dev/null || podman %s %s", action, containerName, action, containerName)
	_, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err == nil {
		return nil
	}

	if unprivUsers, probeErr := m.ProbeUnprivilegedUsers(); probeErr == nil {
		for _, u := range unprivUsers {
			for _, c := range u.Containers {
				if c.Name == containerName || c.ID == containerName {
					userCmd := fmt.Sprintf("run0 --background=no --user %s podman %s %s 2>/dev/null || sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman %s %s", u.User, action, containerName, u.User, u.UID, action, containerName)
					_, userErr := m.sshMgr.RunCommand(m.cfg, userCmd)
					return userErr
				}
			}
		}
	}
	return err
}

// GetContainerLogs returns recent logs (falling back to unprivileged user if needed)
func (m *Manager) GetContainerLogs(containerName string, tail int) (string, error) {
	cmd := fmt.Sprintf("run0 --background=no podman logs --tail %d %s 2>&1 || sudo -n podman logs --tail %d %s 2>&1 || podman logs --tail %d %s", tail, containerName, tail, containerName, tail, containerName)
	out, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err == nil {
		return out, nil
	}

	if unprivUsers, probeErr := m.ProbeUnprivilegedUsers(); probeErr == nil {
		for _, u := range unprivUsers {
			for _, c := range u.Containers {
				if c.Name == containerName || c.ID == containerName {
					userCmd := fmt.Sprintf("run0 --background=no --user %s podman logs --tail %d %s 2>&1 || sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman logs --tail %d %s", u.User, tail, containerName, u.User, u.UID, tail, containerName)
					return m.sshMgr.RunCommand(m.cfg, userCmd)
				}
			}
		}
	}
	return out, err
}
