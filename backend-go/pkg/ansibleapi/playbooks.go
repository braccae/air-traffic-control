package ansibleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
	"gopkg.in/yaml.v3"
)

// ProtectedPlaybooks defines playbooks that cannot be deleted.
var ProtectedPlaybooks = map[string]bool{
	"site.yml":  true,
	"site.yaml": true,
}

// PlaybookValidation represents validation outcome.
type PlaybookValidation struct {
	Valid bool   `json:"valid"`
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
}

// PlaybookMeta represents metadata for a playbook file.
type PlaybookMeta struct {
	Name        string `json:"name"`
	Filename    string `json:"filename"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Hosts       string `json:"hosts"`
	TasksCount  int    `json:"tasksCount"`
	Protected   bool   `json:"protected"`
	UpdatedAt   int64  `json:"updatedAt"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// PlaybookItemResult represents a single playbook file's name and YAML content.
type PlaybookItemResult struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

var safePlaybookRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`)

// ValidatePlaybookName guards against path traversal and ensures .yml/.yaml.
func ValidatePlaybookName(name string) PlaybookValidation {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return PlaybookValidation{Valid: false, Name: name, Error: "Playbook name must be a non-empty string"}
	}
	if filepath.Base(trimmed) != trimmed || strings.Contains(trimmed, "..") || strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return PlaybookValidation{Valid: false, Name: trimmed, Error: "Directory traversal and path separators are not permitted in playbook name"}
	}
	if !strings.HasSuffix(trimmed, ".yml") && !strings.HasSuffix(trimmed, ".yaml") {
		return PlaybookValidation{Valid: false, Name: trimmed, Error: "Playbook filename must have a .yml or .yaml extension"}
	}
	baseWithoutExt := strings.TrimSuffix(strings.TrimSuffix(trimmed, ".yaml"), ".yml")
	if baseWithoutExt == "" {
		return PlaybookValidation{Valid: false, Name: trimmed, Error: "Playbook filename must have a name before the extension"}
	}
	if !safePlaybookRegex.MatchString(trimmed) {
		return PlaybookValidation{Valid: false, Name: trimmed, Error: "Playbook filename contains invalid characters. Use letters, numbers, hyphens, and underscores."}
	}
	return PlaybookValidation{Valid: true, Name: trimmed}
}

// NormalizePlaybookName appends .yml if missing.
func NormalizePlaybookName(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.HasSuffix(trimmed, ".yml") || strings.HasSuffix(trimmed, ".yaml") {
		return trimmed
	}
	return trimmed + ".yml"
}

// IsProtectedPlaybook checks if name is protected.
func IsProtectedPlaybook(name string) bool {
	return ProtectedPlaybooks[filepath.Base(name)]
}

// StarterPlaybookTemplate generates basic template for new playbooks.
func StarterPlaybookTemplate(playbookName string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(playbookName, ".yaml"), ".yml")
	base = strings.ReplaceAll(strings.ReplaceAll(base, "-", " "), "_", " ")
	words := strings.Fields(base)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	title := strings.Join(words, " ")
	if title == "" {
		title = "New Playbook"
	}

	return fmt.Sprintf(`---
- name: %s
  hosts: tag:host
  gather_facts: false
  tasks:
    - name: Verify host reachability
      ping:

    - name: Check system uptime
      command: uptime -p
      register: host_uptime
      changed_when: false

    - name: Report host status
      debug:
        msg: "{{ inventory_hostname }} uptime is {{ host_uptime.stdout }}."
`, title)
}

func resolveSafePlaybookPath(playbooksDir, name string) (string, error) {
	val := ValidatePlaybookName(name)
	if !val.Valid {
		return "", fmt.Errorf("%s", val.Error)
	}
	absDir, err := filepath.Abs(playbooksDir)
	if err != nil {
		return "", err
	}
	targetPath := filepath.Join(absDir, val.Name)
	if !strings.HasPrefix(targetPath, absDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected")
	}
	return targetPath, nil
}

// InspectPlaybookYaml inspects YAML structure to extract metadata (description, hosts, tasksCount).
func InspectPlaybookYaml(content string) (desc, hosts string, tasksCount int) {
	var plays []map[string]any
	if err := yaml.Unmarshal([]byte(content), &plays); err != nil {
		return "", "", 0
	}
	if len(plays) == 0 {
		return "", "", 0
	}
	firstPlay := plays[0]
	if name, ok := firstPlay["name"].(string); ok {
		desc = name
	}
	if h, ok := firstPlay["hosts"].(string); ok {
		hosts = h
	}
	for _, play := range plays {
		if tasks, ok := play["tasks"].([]any); ok {
			tasksCount += len(tasks)
		}
	}
	return desc, hosts, tasksCount
}

// ListPlaybooksMeta returns metadata for each playbook in playbooksDir.
func ListPlaybooksMeta(playbooksDir string) ([]PlaybookMeta, error) {
	if _, err := os.Stat(playbooksDir); os.IsNotExist(err) {
		return []PlaybookMeta{}, nil
	}

	entries, err := os.ReadDir(playbooksDir)
	if err != nil {
		return nil, err
	}

	var results []PlaybookMeta
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}

		fullPath := filepath.Join(playbooksDir, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}

		content, _ := os.ReadFile(fullPath)
		desc, hosts, tasksCount := InspectPlaybookYaml(string(content))

		meta := PlaybookMeta{
			Name:        e.Name(),
			Filename:    e.Name(),
			Path:        fullPath,
			Description: desc,
			Hosts:       hosts,
			TasksCount:  tasksCount,
			Protected:   IsProtectedPlaybook(e.Name()),
			UpdatedAt:   fi.ModTime().UnixMilli(),
			SizeBytes:   fi.Size(),
		}
		results = append(results, meta)
	}

	return results, nil
}

// ReadPlaybook returns the file's content.
func ReadPlaybook(playbooksDir, name string) (PlaybookItemResult, error) {
	path, err := resolveSafePlaybookPath(playbooksDir, name)
	if err != nil {
		return PlaybookItemResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PlaybookItemResult{}, err
	}
	return PlaybookItemResult{Name: filepath.Base(path), Content: string(data)}, nil
}

// CreatePlaybook creates a new playbook file.
func CreatePlaybook(playbooksDir, name, content string) (PlaybookItemResult, error) {
	path, err := resolveSafePlaybookPath(playbooksDir, name)
	if err != nil {
		return PlaybookItemResult{}, err
	}
	if err := os.MkdirAll(playbooksDir, 0o755); err != nil {
		return PlaybookItemResult{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return PlaybookItemResult{}, fmt.Errorf("playbook %s already exists", name)
	}
	finalContent := content
	if strings.TrimSpace(finalContent) == "" {
		finalContent = StarterPlaybookTemplate(name)
	}
	if err := os.WriteFile(path, []byte(finalContent), 0o644); err != nil {
		return PlaybookItemResult{}, err
	}
	return PlaybookItemResult{Name: filepath.Base(path), Content: finalContent}, nil
}

// UpdatePlaybook updates an existing playbook file.
func UpdatePlaybook(playbooksDir, name, content string) (PlaybookItemResult, error) {
	path, err := resolveSafePlaybookPath(playbooksDir, name)
	if err != nil {
		return PlaybookItemResult{}, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return PlaybookItemResult{}, fmt.Errorf("playbook %s not found", name)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return PlaybookItemResult{}, err
	}
	return PlaybookItemResult{Name: filepath.Base(path), Content: content}, nil
}

// DeletePlaybook removes a playbook file unless protected.
func DeletePlaybook(playbooksDir, name string) (string, error) {
	if IsProtectedPlaybook(name) {
		return "", fmt.Errorf("cannot delete protected baseline playbook %s", name)
	}
	path, err := resolveSafePlaybookPath(playbooksDir, name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("playbook %s not found", name)
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return filepath.Base(path), nil
}

// CheckPlaybookSyntax runs ansible-playbook --syntax-check.
func CheckPlaybookSyntax(repoRoot, playbooksDir, playbookName, content string) (string, error) {
	val := ValidatePlaybookName(playbookName)
	if !val.Valid {
		return "", fmt.Errorf("%s", val.Error)
	}

	targetPath := filepath.Join(playbooksDir, val.Name)
	var tempFile string
	if content != "" {
		tmp, err := os.CreateTemp(playbooksDir, ".tmp_syntax_*.yml")
		if err != nil {
			return "", fmt.Errorf("failed to create temp file for syntax check: %w", err)
		}
		tempFile = tmp.Name()
		if _, err := tmp.WriteString(content); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tempFile)
			return "", err
		}
		_ = tmp.Close()
		targetPath = tempFile
		defer os.Remove(tempFile)
	}

	venvDir := GetAnsibleVenvPath(repoRoot)
	playbookBin := filepath.Join(venvDir, "bin", "ansible-playbook")
	if _, err := exec.LookPath(playbookBin); err != nil {
		p, err := exec.LookPath("ansible-playbook")
		if err != nil {
			return "", fmt.Errorf("ansible-playbook executable not found")
		}
		playbookBin = p
	}

	inventoryScript := filepath.Join(repoRoot, "inventory", "tailscale_inventory.py")
	args := []string{"-i", inventoryScript, targetPath, "--syntax-check"}
	cmd := exec.Command(playbookBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// ParsePlaybookYamlDocument parses YAML content into a structured document or reports syntax error.
func ParsePlaybookYamlDocument(content string) (any, map[string]any, error) {
	var doc any
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(content)))
	if err := decoder.Decode(&doc); err != nil {
		errMap := map[string]any{
			"success": false,
			"error":   err.Error(),
		}
		return nil, errMap, err
	}
	return doc, nil, nil
}

// PlaybookRunnerState manages active executions.
type PlaybookRunnerState struct {
	mu        sync.Mutex
	processes map[string]*exec.Cmd
	store     *hostcache.Store
	broadcast func(map[string]any)
	repoRoot  string
}

// NewPlaybookRunner creates a PlaybookRunnerState.
func NewPlaybookRunner(repoRoot string, store *hostcache.Store, broadcast func(map[string]any)) *PlaybookRunnerState {
	return &PlaybookRunnerState{
		processes: make(map[string]*exec.Cmd),
		store:     store,
		broadcast: broadcast,
		repoRoot:  repoRoot,
	}
}

// RunRequest encapsulates playbook execution arguments.
type RunRequest struct {
	Playbook  string `json:"playbook"`
	Target    string `json:"target"`
	CheckMode bool   `json:"checkMode"`
	Tags      string `json:"tags"`
	ExtraVars any    `json:"extraVars"`
	TaskId    string `json:"taskId"`
	Source    string `json:"source"`
}

// Run executes ansible-playbook with ANSIBLE_STDOUT_CALLBACK=json.
func (r *PlaybookRunnerState) Run(ctx context.Context, req RunRequest) (string, hostcache.Task, error) {
	if req.Playbook == "" {
		req.Playbook = "site.yml"
	}
	if req.Target == "" {
		req.Target = "all"
	}

	val := ValidatePlaybookName(req.Playbook)
	if !val.Valid {
		return "", hostcache.Task{}, fmt.Errorf("%s", val.Error)
	}

	playbooksDir := filepath.Join(r.repoRoot, "ansible", "playbooks")
	playbookPath := filepath.Join(playbooksDir, val.Name)
	if _, err := os.Stat(playbookPath); os.IsNotExist(err) {
		return "", hostcache.Task{}, fmt.Errorf("playbook %s not found on filesystem", req.Playbook)
	}

	taskId := req.TaskId
	if taskId == "" {
		taskId = fmt.Sprintf("task-ansible-%d", time.Now().UnixMilli())
	}

	startTime := time.Now().Format("2006-01-02 15:04:05")
	desc := fmt.Sprintf("ansible-playbook %s --limit %s", req.Playbook, req.Target)
	if req.CheckMode {
		desc += " (--check)"
	}
	if req.Source != "" {
		desc += fmt.Sprintf(" [%s]", req.Source)
	}

	task := hostcache.Task{
		ID:          taskId,
		StartTime:   startTime,
		NodeTarget:  req.Target,
		User:        "core@ansible",
		Description: desc,
		Progress:    10,
		ProgressEta: "Spawning Ansible runner...",
		Status:      "RUNNING",
		Category:    "ansible",
		Logs:        json.RawMessage(fmt.Sprintf(`["[ansible] Starting playbook %s against %s"]`, req.Playbook, req.Target)),
		UpdatedAt:   time.Now().UnixMilli(),
	}

	if r.store != nil {
		_ = r.store.SaveTask(task)
	}
	if r.broadcast != nil {
		r.broadcast(map[string]any{
			"type": "task_started",
			"task": task,
		})
	}

	venvDir := GetAnsibleVenvPath(r.repoRoot)
	playbookBin := filepath.Join(venvDir, "bin", "ansible-playbook")
	if _, err := exec.LookPath(playbookBin); err != nil {
		p, err := exec.LookPath("ansible-playbook")
		if err != nil {
			playbookBin = "ansible-playbook"
		} else {
			playbookBin = p
		}
	}

	inventoryScript := filepath.Join(r.repoRoot, "inventory", "tailscale_inventory.py")
	args := []string{"-i", inventoryScript, playbookPath}
	if req.Target != "all" && req.Target != "" {
		args = append(args, "--limit", req.Target)
	}
	if req.CheckMode {
		args = append(args, "--check")
	}
	if req.Tags != "" {
		args = append(args, "--tags", req.Tags)
	}
	if req.ExtraVars != nil {
		var evStr string
		switch v := req.ExtraVars.(type) {
		case string:
			evStr = v
		default:
			b, _ := json.Marshal(v)
			evStr = string(b)
		}
		args = append(args, "--extra-vars", evStr)
	}

	cmd := exec.CommandContext(ctx, playbookBin, args...)
	cmd.Dir = r.repoRoot
	cmd.Env = append(os.Environ(), "ANSIBLE_STDOUT_CALLBACK=json", "ANSIBLE_FORCE_COLOR=0")

	r.mu.Lock()
	r.processes[taskId] = cmd
	r.mu.Unlock()

	go func() {
		out, err := cmd.CombinedOutput()
		r.mu.Lock()
		delete(r.processes, taskId)
		r.mu.Unlock()

		task.Progress = 100
		task.UpdatedAt = time.Now().UnixMilli()

		logLines := []string{fmt.Sprintf("[ansible] Starting playbook %s against %s", req.Playbook, req.Target)}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) != "" {
				logLines = append(logLines, line)
			}
		}
		if len(logLines) > 300 {
			logLines = logLines[len(logLines)-300:]
		}
		logBytes, _ := json.Marshal(logLines)
		task.Logs = json.RawMessage(logBytes)

		if err != nil {
			task.Status = "FAILED"
			task.ProgressEta = fmt.Sprintf("Failed: %v", err)
		} else {
			task.Status = "OK"
			if req.CheckMode {
				task.ProgressEta = "Completed dry-run (0 failed)"
			} else {
				task.ProgressEta = "Completed successfully"
			}
		}

		if r.store != nil {
			_ = r.store.SaveTask(task)
		}
		if r.broadcast != nil {
			r.broadcast(map[string]any{
				"type": "task_updated",
				"task": task,
			})
			r.broadcast(map[string]any{
				"type":        "playbook_log",
				"taskId":      taskId,
				"output":      string(out),
				"status":      task.Status,
				"progressEta": task.ProgressEta,
			})
			r.broadcast(map[string]any{
				"type":   "tasks_update",
				"taskId": taskId,
				"task":   task,
			})
		}
	}()

	return taskId, task, nil
}

// Cancel terminates a running playbook.
func (r *PlaybookRunnerState) Cancel(taskId string) bool {
	r.mu.Lock()
	cmd, exists := r.processes[taskId]
	if exists && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		delete(r.processes, taskId)
		r.mu.Unlock()

		task := hostcache.Task{
			ID:          taskId,
			Status:      "FAILED",
			ProgressEta: "Cancelled by user",
			Progress:    100,
			UpdatedAt:   time.Now().UnixMilli(),
		}
		if r.store != nil {
			_ = r.store.SaveTask(task)
		}
		if r.broadcast != nil {
			r.broadcast(map[string]any{"type": "task_updated", "task": task})
		}
		return true
	}
	r.mu.Unlock()
	return false
}
