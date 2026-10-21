package podman

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/types"
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

const (
	// RootSocketPath is the standard system/rootful Podman API socket.
	RootSocketPath = "/run/podman/podman.sock"
)

// RootlessSocketPath returns the rootless Podman API socket path for a given UID.
func RootlessSocketPath(uid int) string {
	return fmt.Sprintf("/run/user/%d/podman/podman.sock", uid)
}

// RootSocketOrCLICommand builds a command that queries the rootful Podman socket
// (/run/podman/podman.sock) first using the AGENTS.md §3 privilege escalation chain
// (run0 -> sudo -n -> doas -n -> none), falling back to the native podman CLI with
// the same escalation chain if the socket is unavailable.
func RootSocketOrCLICommand(socketEndpoint string, cliCmd string) string {
	sockURL := fmt.Sprintf("http://d%s", socketEndpoint)
	const curlBase = "curl -sf"
	const sockArg = "--unix-socket=" + RootSocketPath
	return fmt.Sprintf(
		"run0 --background=no %s %s %q 2>/dev/null || "+
			"sudo -n %s %s %q 2>/dev/null || "+
			"doas -n %s %s %q 2>/dev/null || "+
			"%s %s %q 2>/dev/null || "+
			"run0 --background=no %s 2>/dev/null || "+
			"sudo -n %s 2>/dev/null || "+
			"doas -n %s 2>/dev/null || "+
			"%s",
		curlBase, sockArg, sockURL,
		curlBase, sockArg, sockURL,
		curlBase, sockArg, sockURL,
		curlBase, sockArg, sockURL,
		cliCmd,
		cliCmd,
		cliCmd,
		cliCmd,
	)
}

// UserSocketOrCLICommand builds a command that queries a rootless user's Podman socket
// (/run/user/<uid>/podman/podman.sock) first using the AGENTS.md §3 privilege escalation chain
// (run0 -> sudo -n -> doas -n -> none), falling back to the native podman CLI with
// the same escalation chain if the socket is unavailable.
func UserSocketOrCLICommand(user string, uid int, socketEndpoint string, cliCmd string) string {
	sockPath := RootlessSocketPath(uid)
	sockURL := fmt.Sprintf("http://d%s", socketEndpoint)
	sockArg := "--unix-socket=" + sockPath
	const curlBase = "curl -sf"
	return fmt.Sprintf(
		"run0 --background=no %s %s %q 2>/dev/null || "+
			"run0 --background=no --user %s %s %s %q 2>/dev/null || "+
			"(cd / && sudo -n %s %s %q 2>/dev/null) || "+
			"(cd / && sudo -n -u %s %s %s %q 2>/dev/null) || "+
			"(cd / && doas -n %s %s %q 2>/dev/null) || "+
			"(cd / && doas -n --user %s %s %s %q 2>/dev/null) || "+
			"%s %s %q 2>/dev/null || "+
			"run0 --background=no --user %s %s 2>/dev/null || "+
			"(cd / && sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d %s 2>/dev/null) || "+
			"(cd / && doas -n --user %s XDG_RUNTIME_DIR=/run/user/%d %s 2>/dev/null) || "+
			"(cd / && doas -u %s XDG_RUNTIME_DIR=/run/user/%d %s 2>/dev/null) || "+
			"su - %s -s /bin/sh -c \"XDG_RUNTIME_DIR=/run/user/%d %s\" 2>/dev/null || "+
			"XDG_RUNTIME_DIR=/run/user/%d %s",
		curlBase, sockArg, sockURL,
		user, curlBase, sockArg, sockURL,
		curlBase, sockArg, sockURL,
		user, curlBase, sockArg, sockURL,
		curlBase, sockArg, sockURL,
		user, curlBase, sockArg, sockURL,
		curlBase, sockArg, sockURL,
		user, cliCmd,
		user, uid, cliCmd,
		user, uid, cliCmd,
		user, uid, cliCmd,
		user, uid, cliCmd,
		uid, cliCmd,
	)
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
	runUserOut, _ := m.sshMgr.RunCommand(m.cfg, "run0 --background=no sh -c 'ls -1d /run/user/*/podman/podman.sock /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null' 2>/dev/null || sudo -n sh -c 'ls -1d /run/user/*/podman/podman.sock /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null' 2>/dev/null || doas -n sh -c 'ls -1d /run/user/*/podman/podman.sock /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null' 2>/dev/null || ls -1d /run/user/*/podman/podman.sock /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null || true")

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

		// Parse /etc/subuid for rootless namespace mappings.
		// Note: we only store subuid mappings here to enrich users discovered via
		// active processes, /var/lib/systemd/linger, or /run/user/<uid>/* sockets/dirs,
		// avoiding expensive per-user podman spawns for inactive system accounts.
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
		if len(containers) > 0 {
			if stats, sErr := m.CollectStats(cand.User, cand.UID); sErr == nil && len(stats) > 0 {
				EnrichContainersWithStats(containers, stats)
			}
		}

		var pods []types.PodmanPod
		if len(containers) > 0 || len(cand.ActiveProcesses) > 0 {
			pods, _ = m.listUserPods(cand.User, cand.UID)
		}
		for pi := range pods {
			p := &pods[pi]
			var podContainers []types.PodmanContainer
			for _, c := range containers {
				if matchesPod(c, *p) {
					podContainers = append(podContainers, c)
				}
			}
			p.Containers = podContainers
			p.TotalCPU, p.TotalMemory = computePodTotals(podContainers)
		}

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
		ID      string      `json:"Id"`
		IdAlt   string      `json:"ID"`
		IdAlt2  string      `json:"id"`
		Names   interface{} `json:"Names"`
		Image   string      `json:"Image"`
		State   string      `json:"State"`
		Status  string      `json:"Status"`
		Pod     string      `json:"Pod"`
		PodName string      `json:"PodName"`
		PodId   string      `json:"PodId"`
		IsInfra bool        `json:"IsInfra"`
	}

	if err := json.Unmarshal([]byte(out), &rawContainers); err != nil {
		return nil, fmt.Errorf("failed to parse containers json for user %s: %w", user, err)
	}

	var containers []types.PodmanContainer
	for _, rc := range rawContainers {
		cid := rc.ID
		if cid == "" {
			cid = rc.IdAlt
		}
		if cid == "" {
			cid = rc.IdAlt2
		}

		name := cid
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
		} else if rc.State != "" && !strings.Contains(strings.ToLower(status), strings.ToLower(rc.State)) && !strings.HasPrefix(strings.ToLower(status), "up ") && !strings.HasPrefix(strings.ToLower(status), "exited") {
			// Libpod socket API sets State="running" and Status="healthy" (or empty); combine so status reflects running state.
			status = fmt.Sprintf("%s (%s)", rc.State, status)
		}

		nameLower := strings.ToLower(name)
		imgLower := strings.ToLower(rc.Image)
		if rc.IsInfra || strings.Contains(nameLower, "pause") || strings.HasSuffix(nameLower, "-infra") || strings.Contains(imgLower, "pause") || strings.Contains(imgLower, "podman-pause") {
			continue
		}

		podID := rc.Pod
		if podID == "" {
			podID = rc.PodId
		}
		podName := rc.PodName

		containers = append(containers, types.PodmanContainer{
			ID:       cid,
			PodID:    podID,
			PodName:  podName,
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

func matchesPod(c types.PodmanContainer, p types.PodmanPod) bool {
	podID := strings.TrimSpace(c.PodID)
	podName := strings.TrimSpace(c.PodName)
	pID := strings.TrimSpace(p.ID)
	pName := strings.TrimSpace(p.Name)

	if podID != "" {
		if podID == pID || podID == pName {
			return true
		}
		if pID != "" && (strings.HasPrefix(podID, pID) || strings.HasPrefix(pID, podID)) {
			return true
		}
	}
	if podName != "" {
		if podName == pName || podName == pID {
			return true
		}
		cleanName := strings.TrimPrefix(pName, "pod: ")
		if podName == cleanName {
			return true
		}
	}
	return false
}

func computePodTotals(containers []types.PodmanContainer) (string, string) {
	var totalCPU float64
	var totalMemBytes float64
	var hasCPU, hasMem bool

	for _, c := range containers {
		if c.CPU != "" {
			trimmed := strings.TrimSpace(strings.TrimSuffix(c.CPU, "%"))
			if val, err := strconv.ParseFloat(trimmed, 64); err == nil {
				totalCPU += val
				hasCPU = true
			}
		}
		if c.Memory != "" {
			bytes := parseMemStringToBytes(c.Memory)
			if bytes > 0 {
				totalMemBytes += bytes
				hasMem = true
			}
		}
	}

	cpuStr := ""
	if hasCPU {
		cpuStr = fmt.Sprintf("%.2f%%", totalCPU)
	}

	memStr := ""
	if hasMem {
		if totalMemBytes >= 1024*1024*1024 {
			memStr = fmt.Sprintf("%.2f GiB", totalMemBytes/(1024*1024*1024))
		} else if totalMemBytes >= 1024*1024 {
			memStr = fmt.Sprintf("%.1f MiB", totalMemBytes/(1024*1024))
		} else if totalMemBytes >= 1024 {
			memStr = fmt.Sprintf("%.0f KiB", totalMemBytes/1024)
		} else {
			memStr = fmt.Sprintf("%.0f B", totalMemBytes)
		}
	}

	return cpuStr, memStr
}

func parseMemStringToBytes(mem string) float64 {
	mem = strings.TrimSpace(mem)
	if idx := strings.Index(mem, "/"); idx != -1 {
		mem = strings.TrimSpace(mem[:idx])
	}
	var multiplier float64 = 1
	var numStr string

	for i, ch := range mem {
		if (ch >= '0' && ch <= '9') || ch == '.' {
			continue
		}
		numStr = strings.TrimSpace(mem[:i])
		unit := strings.ToLower(strings.TrimSpace(mem[i:]))
		switch {
		case strings.HasPrefix(unit, "g"):
			multiplier = 1024 * 1024 * 1024
		case strings.HasPrefix(unit, "m"):
			multiplier = 1024 * 1024
		case strings.HasPrefix(unit, "k"):
			multiplier = 1024
		case strings.HasPrefix(unit, "b"):
			multiplier = 1
		}
		break
	}
	if numStr == "" {
		numStr = mem
	}
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}
	return val * multiplier
}

func (m *Manager) listUserContainers(user string, uid int) ([]types.PodmanContainer, error) {
	cmd := UserSocketOrCLICommand(user, uid, "/v4.0.0/libpod/containers/json?all=true", "podman ps --format json")
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
	cmd := UserSocketOrCLICommand(user, uid, "/v4.0.0/libpod/pods/json", "podman pod ps --format json")
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
		IdAlt  string `json:"ID"`
		IdAlt2 string `json:"id"`
		Name   string `json:"Name"`
		Status string `json:"Status"`
	}

	if err := json.Unmarshal([]byte(out), &rawPods); err != nil {
		return nil, fmt.Errorf("failed to parse pods json for user %s: %w", user, err)
	}

	var pods []types.PodmanPod
	for _, rp := range rawPods {
		pid := rp.ID
		if pid == "" {
			pid = rp.IdAlt
		}
		if pid == "" {
			pid = rp.IdAlt2
		}
		pods = append(pods, types.PodmanPod{
			ID:       pid,
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

	rootCmd := RootSocketOrCLICommand("/v4.0.0/libpod/containers/json?all=true", "podman ps --all --format json")
	out, err := m.sshMgr.RunCommand(m.cfg, rootCmd)
	if err == nil {
		if rootContainers, parseErr := parseContainersJSON(out, "root", 0); parseErr == nil {
			if rootStats, sErr := m.CollectStats("root", 0); sErr == nil && len(rootStats) > 0 {
				EnrichContainersWithStats(rootContainers, rootStats)
			}
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

// ListPods queries podman pods via rootful/rootless sockets first, falling back to podman pod ps --format json
func (m *Manager) ListPods() ([]types.PodmanPod, error) {
	var pods []types.PodmanPod

	cmd := RootSocketOrCLICommand("/v4.0.0/libpod/pods/json", "podman pod ps --format json")
	out, rootErr := m.sshMgr.RunCommand(m.cfg, cmd)
	if rootErr == nil {
		var rawPods []struct {
			ID     string `json:"Id"`
			IdAlt  string `json:"ID"`
			IdAlt2 string `json:"id"`
			Name   string `json:"Name"`
			Status string `json:"Status"`
		}
		if parseErr := json.Unmarshal([]byte(out), &rawPods); parseErr != nil {
			rootErr = fmt.Errorf("failed to parse pods json: %w", parseErr)
		} else {
			rootContainersCmd := RootSocketOrCLICommand("/v4.0.0/libpod/containers/json?all=true", "podman ps --all --format json")
			rootContainersOut, _ := m.sshMgr.RunCommand(m.cfg, rootContainersCmd)
			rootContainers, _ := parseContainersJSON(rootContainersOut, "root", 0)
			if rootStats, sErr := m.CollectStats("root", 0); sErr == nil && len(rootStats) > 0 {
				EnrichContainersWithStats(rootContainers, rootStats)
			}

			for _, rp := range rawPods {
				pid := rp.ID
				if pid == "" {
					pid = rp.IdAlt
				}
				if pid == "" {
					pid = rp.IdAlt2
				}
				pod := types.PodmanPod{
					ID:       pid,
					Name:     rp.Name,
					Status:   rp.Status,
					User:     "root",
					UID:      0,
					Rootless: false,
				}
				var podContainers []types.PodmanContainer
				for _, c := range rootContainers {
					if matchesPod(c, pod) {
						podContainers = append(podContainers, c)
					}
				}
				pod.Containers = podContainers
				pod.TotalCPU, pod.TotalMemory = computePodTotals(podContainers)
				pods = append(pods, pod)
			}
		}
	}

	// Probe unprivileged users for rootless pods and containers
	unprivUsers, probeErr := m.ProbeUnprivilegedUsers()
	if probeErr == nil {
		for _, u := range unprivUsers {
			pods = append(pods, u.Pods...)
		}
	}

	if rootErr != nil && len(pods) == 0 {
		return nil, fmt.Errorf("failed to list pods: %w", rootErr)
	}

	return pods, nil
}

// RestartPod restarts a pod (querying rootful socket/CLI first, then searching unprivileged users if not found in root)
func (m *Manager) RestartPod(podName string) error {
	sockURL := fmt.Sprintf("http://d/v4.0.0/libpod/pods/%s/restart", podName)
	cmd := fmt.Sprintf(
		"run0 --background=no curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
			"sudo -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
			"doas -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
			"curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
			"run0 --background=no podman pod restart %s 2>/dev/null || "+
			"sudo -n podman pod restart %s 2>/dev/null || "+
			"doas -n podman pod restart %s 2>/dev/null || "+
			"podman pod restart %s",
		RootSocketPath, sockURL,
		RootSocketPath, sockURL,
		RootSocketPath, sockURL,
		RootSocketPath, sockURL,
		podName, podName, podName, podName,
	)
	_, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err == nil {
		return nil
	}

	if unprivUsers, probeErr := m.ProbeUnprivilegedUsers(); probeErr == nil {
		for _, u := range unprivUsers {
			for _, p := range u.Pods {
				if p.Name == podName || p.ID == podName {
					uSock := RootlessSocketPath(u.UID)
					userCmd := fmt.Sprintf(
						"run0 --background=no curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"run0 --background=no --user %s curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"sudo -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"doas -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"run0 --background=no --user %s podman pod restart %s 2>/dev/null || "+
							"sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman pod restart %s 2>/dev/null || "+
							"doas -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman pod restart %s 2>/dev/null || "+
							"XDG_RUNTIME_DIR=/run/user/%d podman pod restart %s",
						uSock, sockURL,
						u.User, uSock, sockURL,
						uSock, sockURL,
						uSock, sockURL,
						uSock, sockURL,
						u.User, podName,
						u.User, u.UID, podName,
						u.User, u.UID, podName,
						u.UID, podName,
					)
					_, userErr := m.sshMgr.RunCommand(m.cfg, userCmd)
					return userErr
				}
			}
		}
	}
	return err
}

// ContainerAction performs start, stop, restart, or inspect via Podman socket first (escalating with run0 -> sudo -n -> doas -n -> none), falling back to CLI and unprivileged users if needed.
func (m *Manager) ContainerAction(containerName, action string) error {
	sockURL := fmt.Sprintf("http://d/v4.0.0/libpod/containers/%s/%s", containerName, action)
	cmd := fmt.Sprintf(
		"run0 --background=no curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
			"sudo -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
			"doas -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
			"curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
			"run0 --background=no podman %s %s 2>/dev/null || "+
			"sudo -n podman %s %s 2>/dev/null || "+
			"doas -n podman %s %s 2>/dev/null || "+
			"podman %s %s",
		RootSocketPath, sockURL,
		RootSocketPath, sockURL,
		RootSocketPath, sockURL,
		RootSocketPath, sockURL,
		action, containerName,
		action, containerName,
		action, containerName,
		action, containerName,
	)
	_, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err == nil {
		return nil
	}

	if unprivUsers, probeErr := m.ProbeUnprivilegedUsers(); probeErr == nil {
		for _, u := range unprivUsers {
			for _, c := range u.Containers {
				if c.Name == containerName || c.ID == containerName {
					uSock := RootlessSocketPath(u.UID)
					userCmd := fmt.Sprintf(
						"run0 --background=no curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"run0 --background=no --user %s curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"sudo -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"doas -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
							"run0 --background=no --user %s podman %s %s 2>/dev/null || "+
							"sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman %s %s 2>/dev/null || "+
							"doas -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman %s %s 2>/dev/null || "+
							"XDG_RUNTIME_DIR=/run/user/%d podman %s %s",
						uSock, sockURL,
						u.User, uSock, sockURL,
						uSock, sockURL,
						uSock, sockURL,
						uSock, sockURL,
						u.User, action, containerName,
						u.User, u.UID, action, containerName,
						u.User, u.UID, action, containerName,
						u.UID, action, containerName,
					)
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
	cmd := fmt.Sprintf(
		"run0 --background=no podman logs --tail %d %s 2>&1 || "+
			"sudo -n podman logs --tail %d %s 2>&1 || "+
			"doas -n podman logs --tail %d %s 2>&1 || "+
			"podman logs --tail %d %s",
		tail, containerName, tail, containerName, tail, containerName, tail, containerName,
	)
	out, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err == nil {
		return out, nil
	}

	if unprivUsers, probeErr := m.ProbeUnprivilegedUsers(); probeErr == nil {
		for _, u := range unprivUsers {
			for _, c := range u.Containers {
				if c.Name == containerName || c.ID == containerName {
					userCmd := fmt.Sprintf(
						"run0 --background=no --user %s podman logs --tail %d %s 2>&1 || "+
							"sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman logs --tail %d %s 2>&1 || "+
							"doas -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman logs --tail %d %s 2>&1 || "+
							"XDG_RUNTIME_DIR=/run/user/%d podman logs --tail %d %s",
						u.User, tail, containerName,
						u.User, u.UID, tail, containerName,
						u.User, u.UID, tail, containerName,
						u.UID, tail, containerName,
					)
					return m.sshMgr.RunCommand(m.cfg, userCmd)
				}
			}
		}
	}
	return out, err
}
