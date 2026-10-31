package ansqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/ansiblebin"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
)

// ExecOptions is the process shape handed to an ExecFunc seam.
type ExecOptions struct {
	Cwd string
	Env map[string]string
}

// ExecResult is the captured result of an ansible-playbook invocation.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ExecFunc is the command execution seam. It is nil in production, where the
// runner really spawns ansible-playbook; tests inject a fake to exercise the
// parsing, change detection and checkpoint-halt logic without a live host.
type ExecFunc func(ctx context.Context, name string, args []string, opts ExecOptions) (ExecResult, error)

// TaskRecorder persists background tasks into the shared tasks table (`GET /api/tasks`).
type TaskRecorder interface {
	SaveTask(t hostcache.Task) error
}

// Options configures one playbook run or one ExecuteNextTask call.
type Options struct {
	// AnsibleBinary is the ansible-playbook executable (default: PATH lookup).
	AnsibleBinary string
	// WorkDir is the repo root holding ansible/ (default: ResolveWorkDir()).
	WorkDir string
	// Env is overlaid on the parent environment for the child process.
	Env map[string]string
	// VaultVarsFile is passed as `--extra-vars @<path>` when set.
	VaultVarsFile string
	// TaskID forces ExecuteNextTask to run that specific staged task.
	TaskID string
	// InventoryPath overrides inventory resolution for this run.
	InventoryPath string
	// Exec is the test seam; nil means really exec ansible-playbook.
	Exec ExecFunc
	// Invalidator is called after a successful run (nil-safe).
	Invalidator Invalidator
	// TaskStore optionally records queue executions in the background tasks table (`tasks`).
	TaskStore TaskRecorder
	// Broadcast optionally emits real-time task lifecycle events (`task_started`, `task_updated`).
	Broadcast func(event map[string]any)
	// Timeout bounds one playbook run (default 10m).
	Timeout time.Duration
}

// DefaultTimeout bounds a single playbook run.
const DefaultTimeout = 10 * time.Minute

// ResolveWorkDir derives the repo root: the nearest ancestor of the current
// working directory that contains ansible/playbooks, else the working directory
// itself.
func ResolveWorkDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := wd
	for {
		if st, err := os.Stat(filepath.Join(dir, "ansible", "playbooks")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return wd
}

var (
	safeIDPattern       = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	safePlaybookPattern = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`)
)

// safeID normalises a task id for use in an ephemeral filename.
func safeID(id string) string { return safeIDPattern.ReplaceAllString(id, "_") }

func validateQueuePlaybookName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("Playbook name must be a non-empty string")
	}
	if filepath.Base(trimmed) != trimmed || strings.Contains(trimmed, "..") || strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return "", errors.New("Directory traversal and path separators are not permitted in playbook name")
	}
	if !strings.HasSuffix(trimmed, ".yml") && !strings.HasSuffix(trimmed, ".yaml") {
		return "", errors.New("Playbook filename must have a .yml or .yaml extension")
	}
	if !safePlaybookPattern.MatchString(trimmed) {
		return "", errors.New("Playbook filename contains invalid characters")
	}
	return trimmed, nil
}

// ExecuteTaskOneByOne compiles the task to an ephemeral playbook (or runs the
// specified PlaybookRun file), runs it with ANSIBLE_STDOUT_CALLBACK=json,
// parses the callback JSON and derives changed/failed/error.
func ExecuteTaskOneByOne(ctx context.Context, task Task, opts Options) (ExecutionResult, error) {
	startTime := time.Now()

	workdir := opts.WorkDir
	if workdir == "" {
		workdir = ResolveWorkDir()
	}
	playbooksDir := filepath.Join(workdir, "ansible", "playbooks")
	if err := os.MkdirAll(playbooksDir, 0o755); err != nil {
		return ExecutionResult{}, fmt.Errorf("ansqueue: create playbooks dir %s: %w", playbooksDir, err)
	}

	id := safeID(task.ID)
	var ephemeralFile string
	var runPlaybookPath string

	// Single-host mutations over a tailnet route are bridged through ATC's own
	// transport: ansible-playbook then talks plain TCP to 127.0.0.1. The
	// rewritten task feeds both the generated playbook and its inventory so the
	// play's `ansible_host` var cannot override the loopback target.
	bridged, stopForward, bridgeErr := bridgeTaskToLoopback(task)
	if bridgeErr != nil {
		return ExecutionResult{}, fmt.Errorf("ansqueue: open transport bridge: %w", bridgeErr)
	}
	if stopForward != nil {
		defer stopForward()
	}
	task = bridged

	if task.PlaybookRun != nil {
		validName, err := validateQueuePlaybookName(task.PlaybookRun.Playbook)
		if err != nil {
			return ExecutionResult{
				Success:    false,
				TaskID:     task.ID,
				Changed:    false,
				Failed:     true,
				JSONOutput: nil,
				Error:      err.Error(),
				DurationMs: time.Since(startTime).Milliseconds(),
			}, nil
		}
		absDir, _ := filepath.Abs(playbooksDir)
		candidate := filepath.Join(absDir, validName)
		if !strings.HasPrefix(candidate, absDir+string(filepath.Separator)) {
			return ExecutionResult{
				Success:    false,
				TaskID:     task.ID,
				Changed:    false,
				Failed:     true,
				JSONOutput: nil,
				Error:      "Path traversal detected in playbook name",
				DurationMs: time.Since(startTime).Milliseconds(),
			}, nil
		}
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return ExecutionResult{
				Success:    false,
				TaskID:     task.ID,
				Changed:    false,
				Failed:     true,
				JSONOutput: nil,
				Error:      fmt.Sprintf("Playbook %s not found in %s", validName, playbooksDir),
				DurationMs: time.Since(startTime).Milliseconds(),
			}, nil
		}
		runPlaybookPath = candidate
	} else {
		ephemeralFile = filepath.Join(playbooksDir, ".queue-"+id+".yml")
		if err := os.WriteFile(ephemeralFile, []byte(CompileOnTheFlyPlaybook(task)), 0o600); err != nil {
			return ExecutionResult{}, fmt.Errorf("ansqueue: write playbook %s: %w", ephemeralFile, err)
		}
		defer os.Remove(ephemeralFile)
		runPlaybookPath = ephemeralFile
	}

	ephemeralInventory := ""
	defer func() {
		if ephemeralInventory != "" {
			os.Remove(ephemeralInventory)
		}
	}()

	// A single-host mutation always uses its own generated inventory: the host
	// name in that file is the task's node name, so `--limit <node>` resolves.
	// The shared tailnet inventory names hosts by MagicDNS name instead, which
	// is why limiting a mutation against it failed with "no hosts to target".
	inventoryPath := opts.InventoryPath
	if task.PlaybookRun == nil && task.Connection != nil {
		inventoryDir := filepath.Join(workdir, "ansible", "inventory")
		if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
			return ExecutionResult{}, fmt.Errorf("ansqueue: create inventory dir %s: %w", inventoryDir, err)
		}
		content, err := GenerateTaskInventory(task)
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("ansqueue: generate inventory: %w", err)
		}
		ephemeralInventory = filepath.Join(inventoryDir, ".inv-"+id+".json")
		if err := os.WriteFile(ephemeralInventory, []byte(content), 0o600); err != nil {
			return ExecutionResult{}, fmt.Errorf("ansqueue: write inventory %s: %w", ephemeralInventory, err)
		}
		inventoryPath = ephemeralInventory
	} else if inventoryPath == "" {
		if task.Connection != nil {
			inventoryDir := filepath.Join(workdir, "ansible", "inventory")
			if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
				return ExecutionResult{}, fmt.Errorf("ansqueue: create inventory dir %s: %w", inventoryDir, err)
			}
			content, err := GenerateTaskInventory(task)
			if err != nil {
				return ExecutionResult{}, fmt.Errorf("ansqueue: generate inventory: %w", err)
			}
			ephemeralInventory = filepath.Join(inventoryDir, ".inv-"+id+".json")
			if err := os.WriteFile(ephemeralInventory, []byte(content), 0o600); err != nil {
				return ExecutionResult{}, fmt.Errorf("ansqueue: write inventory %s: %w", ephemeralInventory, err)
			}
			inventoryPath = ephemeralInventory
		} else {
			defaultInventory := filepath.Join(workdir, "inventory", "tailscale_inventory.py")
			if _, err := os.Stat(defaultInventory); err == nil {
				inventoryPath = defaultInventory
			} else {
				inventoryPath = task.TargetHostRoute + ","
			}
		}
	}

	args := []string{"-i", inventoryPath}
	if task.PlaybookRun != nil {
		runTarget := strings.TrimSpace(task.PlaybookRun.Target)
		if runTarget != "" && runTarget != "all" && !strings.HasSuffix(inventoryPath, ",") {
			args = append(args, "--limit", runTarget)
		}
		if task.PlaybookRun.CheckMode {
			args = append(args, "--check")
		}
		if strings.TrimSpace(task.PlaybookRun.Tags) != "" {
			args = append(args, "--tags", strings.TrimSpace(task.PlaybookRun.Tags))
		}
		if task.PlaybookRun.ExtraVars != nil {
			switch v := task.PlaybookRun.ExtraVars.(type) {
			case string:
				if strings.TrimSpace(v) != "" {
					args = append(args, "--extra-vars", v)
				}
			default:
				if b, err := json.Marshal(v); err == nil {
					args = append(args, "--extra-vars", string(b))
				}
			}
		}
	} else if !strings.HasSuffix(inventoryPath, ",") {
		if limit := firstNonEmpty(task.TargetNodeName, task.TargetNodeID); limit != "" {
			args = append(args, "--limit", limit)
		}
	}
	args = append(args, runPlaybookPath)
	if opts.VaultVarsFile != "" {
		args = append(args, "--extra-vars", "@"+opts.VaultVarsFile)
	}

	env := buildEnv(workdir, opts)
	// ansible only reports has_tty when -tt is present in the ssh args, and the
	// host-var form is not visible to the pre-connect TTY check. Export it for
	// run0 tasks so community.general.run0 (require_tty) can actually run.
	if !taskRunsAsRoot(task) && strings.Contains(becomeMethodFor(task), "run0") {
		if strings.TrimSpace(env["ANSIBLE_SSH_COMMON_ARGS"]) == "" {
			env["ANSIBLE_SSH_COMMON_ARGS"] = "-tt"
		}
	}
	ansibleBin := opts.AnsibleBinary
	if ansibleBin == "" {
		venvDir := os.Getenv("ANSIBLE_VENV_PATH")
		if venvDir == "" {
			venvDir = filepath.Join(workdir, "data", "ansible-venv")
		}
		// Prefer a venv binary that actually execs. A venv copied from another
		// host keeps absolute interpreter paths, so a plain existence check
		// would select a wrapper that fails with ENOENT.
		candidate := filepath.Join(venvDir, "bin", "ansible-playbook")
		if ansiblebin.Runnable(candidate) {
			ansibleBin = candidate
		} else {
			ansibleBin = "ansible-playbook"
		}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var res ExecResult
	if opts.Exec != nil {
		out, err := opts.Exec(runCtx, ansibleBin, args, ExecOptions{Cwd: workdir, Env: env})
		if err != nil {
			// A seam error is a spawn-class failure, never a silent success.
			res = ExecResult{Stdout: out.Stdout, Stderr: firstNonEmpty(out.Stderr, err.Error()), ExitCode: 1}
		} else {
			res = out
		}
	} else {
		res = runAnsible(runCtx, ansibleBin, args, workdir, env)
	}

	duration := time.Since(startTime).Milliseconds()

	jsonOutput, parseErr := ParseAnsibleJSONStdout(res.Stdout)
	if parseErr != nil {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = parseErr.Error()
		}
		if msg == "" {
			msg = "Failed to parse Ansible JSON output"
		}
		return ExecutionResult{
			Success:    false,
			TaskID:     task.ID,
			Changed:    false,
			Failed:     true,
			JSONOutput: nil,
			Error:      RedactSecrets(msg),
			DurationMs: duration,
		}, nil
	}

	changed, failed, errText := deriveOutcome(jsonOutput, res.ExitCode, res.Stderr)
	success := !failed && res.ExitCode == 0

	return ExecutionResult{
		Success:    success,
		TaskID:     task.ID,
		Changed:    changed,
		Failed:     failed,
		JSONOutput: jsonOutput,
		Error:      RedactSecrets(errText),
		DurationMs: duration,
	}, nil
}

// runAnsible really spawns ansible-playbook. A spawn failure is reported as
// exit code 1 with the error as stderr, matching spawn()'s `error` event.
func runAnsible(ctx context.Context, bin string, args []string, dir string, env map[string]string) ExecResult {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = envSlice(env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			out.ExitCode = exitErr.ExitCode()
		} else {
			out.ExitCode = 1
			if strings.TrimSpace(out.Stderr) == "" {
				out.Stderr = err.Error()
			}
		}
	}
	return out
}

// buildEnv assembles the child environment: parent env, then
// ANSIBLE_STDOUT_CALLBACK=json (AGENTS.md §5 structured callback requirement),
// then opts.Env, then ANSIBLE_CONFIG when the repo ships one.
func buildEnv(workdir string, opts Options) map[string]string {
	env := make(map[string]string, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	// ansible-core 2.21+ makes a missing vault password file fatal, so a stale
	// ANSIBLE_VAULT_PASSWORD_FILE inherited from the operator's shell or an old
	// unit file would abort every playbook run. The coordinator resolves vault
	// passwords itself and passes them explicitly, so drop a pointer that no
	// longer resolves rather than poisoning unrelated tasks.
	if p := strings.TrimSpace(env["ANSIBLE_VAULT_PASSWORD_FILE"]); p != "" {
		if _, err := os.Stat(p); err != nil {
			delete(env, "ANSIBLE_VAULT_PASSWORD_FILE")
		}
	}

	env["ANSIBLE_STDOUT_CALLBACK"] = "json"
	// ansible-core no longer ships the `json` callback (gone in 2.19+), so the
	// repo carries its own implementation and points ansible at it.
	callbackDir := filepath.Join(workdir, "ansible", "plugins", "callback")
	if _, err := os.Stat(callbackDir); err == nil {
		existing := strings.TrimSpace(env["ANSIBLE_CALLBACK_PLUGINS"])
		if existing == "" {
			env["ANSIBLE_CALLBACK_PLUGINS"] = callbackDir
		} else {
			env["ANSIBLE_CALLBACK_PLUGINS"] = callbackDir + string(os.PathListSeparator) + existing
		}
	}
	for k, v := range opts.Env {
		env[k] = v
	}
	for _, cfg := range []string{
		filepath.Join(workdir, "ansible", "ansible.cfg"),
		filepath.Join(workdir, "ansible.cfg"),
	} {
		if _, err := os.Stat(cfg); err == nil {
			env["ANSIBLE_CONFIG"] = cfg
			break
		}
	}
	return env
}

func envSlice(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + env[k]
	}
	return out
}

// deriveOutcome walks the callback JSON the way the TS executor does: stats
// first, then per-play/per-task host results, then the process exit code.
func deriveOutcome(jsonOutput any, exitCode int, stderr string) (changed, failed bool, errText string) {
	obj, _ := jsonOutput.(map[string]any)

	if stats, ok := obj["stats"].(map[string]any); ok {
		for _, raw := range stats {
			stat, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if numericField(stat, "failures") > 0 || numericField(stat, "unreachable") > 0 {
				failed = true
			}
			if numericField(stat, "changed") > 0 {
				changed = true
			}
		}
	}

	if plays, ok := obj["plays"].([]any); ok {
		for _, rawPlay := range plays {
			play, ok := rawPlay.(map[string]any)
			if !ok {
				continue
			}
			tasks, ok := play["tasks"].([]any)
			if !ok {
				continue
			}
			for _, rawTask := range tasks {
				stepTask, ok := rawTask.(map[string]any)
				if !ok {
					continue
				}
				hosts, ok := stepTask["hosts"].(map[string]any)
				if !ok {
					continue
				}
				for _, rawHost := range hosts {
					if hostRes, ok := rawHost.(map[string]any); ok {
						hostFailed, _ := hostRes["failed"].(bool)
						hostUnreachable, _ := hostRes["unreachable"].(bool)
						if ignored, _ := hostRes["ignored"].(bool); ignored {
							// Deliberately non-halting failure (ignore_errors).
							hostFailed = false
							hostUnreachable = false
						}
						if hostFailed || hostUnreachable {
							failed = true
							if errText == "" {
								errText = firstNonEmpty(
									stringField(hostRes, "msg"),
									stringField(hostRes, "stderr"),
									stringField(hostRes, "exception"),
								)
							}
						}
						if hostChanged, _ := hostRes["changed"].(bool); hostChanged {
							changed = true
						}
					}
				}
			}
		}
	}

	if exitCode != 0 {
		failed = true
		if errText == "" {
			errText = strings.TrimSpace(stderr)
			if errText == "" {
				errText = formatExitError(exitCode)
			}
		}
	}

	return changed, failed, strings.TrimSpace(errText)
}

func numericField(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// ParseAnsibleJSONStdout extracts the ansible JSON callback object from stdout,
// tolerating warnings, deprecation notices and terminal noise around it. The
// scan mirrors the TS parser character for character.
func ParseAnsibleJSONStdout(stdout string) (any, error) {
	if stdout == "" {
		return nil, errors.New("Ansible stdout is empty or not a string")
	}

	trimmed := strings.TrimSpace(stdout)
	var direct any
	if err := json.Unmarshal([]byte(trimmed), &direct); err == nil {
		if obj, ok := direct.(map[string]any); ok {
			if _, hasPlays := obj["plays"].([]any); hasPlays {
				return obj, nil
			}
		}
	}

	playsIndex := strings.Index(stdout, `"plays"`)
	if playsIndex == -1 {
		return nil, errors.New("No JSON object found in Ansible stdout (missing plays)")
	}

	openBrace := strings.LastIndex(stdout[:playsIndex], "{")
	if openBrace == -1 {
		return nil, errors.New("No JSON object found in Ansible stdout")
	}

	depth := 0
	inString := false
	escape := false

	for i := openBrace; i < len(stdout); i++ {
		ch := stdout[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				slice := stdout[openBrace : i+1]
				var parsed any
				if err := json.Unmarshal([]byte(slice), &parsed); err != nil {
					return nil, fmt.Errorf("Failed to parse Ansible JSON output: %v", err)
				}
				if obj, ok := parsed.(map[string]any); ok {
					if _, hasPlays := obj["plays"].([]any); hasPlays {
						return obj, nil
					}
				}
			}
		}
	}

	return nil, errors.New("Failed to parse Ansible JSON output")
}

// claimNext atomically picks the next runnable task and flips the run flag, so
// two concurrent /api/queue/execute calls can never run the same task twice.
func (s *Store) claimNext(taskID string) (Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return Task{}, false, s.initErr
	}
	if s.isRunning {
		return Task{}, false, nil
	}

	idx := -1
	for i := range s.tasks {
		if s.tasks[i].Status != StatusStaged {
			continue
		}
		if taskID == "" || s.tasks[i].ID == taskID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Task{}, false, nil
	}

	task := s.tasks[idx]
	task.Status = StatusRunning
	task.ExecutedAt = time.Now().UnixMilli()
	blob, err := MarshalTaskJSON(task)
	if err != nil {
		return Task{}, false, fmt.Errorf("ansqueue: encode task: %w", err)
	}
	if _, err := s.db.Exec(`
		UPDATE ansible_queue_tasks
		   SET status = ?, task_json = ?, updated_at = ?
		 WHERE id = ?`,
		string(StatusRunning), string(blob), time.Now().UnixMilli(), task.ID,
	); err != nil {
		return Task{}, false, fmt.Errorf("ansqueue: mark task %s running: %w", task.ID, err)
	}
	s.tasks[idx] = task
	s.isRunning = true
	return task, true, nil
}

// finishRun records the terminal status and always clears the run flag.
func (s *Store) finishRun(id string, status Status, errMsg *string) {
	_, _, _ = s.UpdateStatus(id, status, errMsg)

	s.mu.Lock()
	s.isRunning = false
	s.mu.Unlock()
}

// ExecuteNextTask runs exactly one staged task. It enforces checkpoint halting:
// on any failure the task is marked failed, the error recorded, and no
// subsequent task is executed.
func (s *Store) ExecuteNextTask(ctx context.Context, opts Options) (Outcome, error) {
	task, ok, err := s.claimNext(opts.TaskID)
	if err != nil {
		return Outcome{}, err
	}
	if !ok {
		return Outcome{RemainingCount: s.StagedCount(), HaltedOnError: false}, nil
	}

	var outcome Outcome
	bgTask := hostcache.Task{
		ID:          task.ID,
		StartTime:   time.Now().Format("2006-01-02 15:04:05"),
		NodeTarget:  firstNonEmpty(task.TargetNodeName, task.TargetNodeID, task.TargetHostRoute, "all"),
		User:        firstNonEmpty(task.TargetSSHUser, "core") + "@ansible",
		Description: task.Title,
		Progress:    20,
		ProgressEta: fmt.Sprintf("Executing %s...", firstNonEmpty(task.PrimaryModule, "ansible-playbook")),
		Status:      "RUNNING",
		Category:    "ansible",
		Logs:        json.RawMessage(fmt.Sprintf("[%q]", fmt.Sprintf("[queue] Starting %s on %s", task.Title, firstNonEmpty(task.TargetNodeName, task.TargetHostRoute)))),
		UpdatedAt:   time.Now().UnixMilli(),
	}
	if opts.TaskStore != nil {
		_ = opts.TaskStore.SaveTask(bgTask)
	}
	if opts.Broadcast != nil {
		opts.Broadcast(map[string]any{"type": "task_started", "task": bgTask})
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				msg := RedactSecrets(fmt.Sprintf("ansible task panicked: %v", r))
				res := ExecutionResult{
					Success: false,
					TaskID:  task.ID,
					Changed: false,
					Failed:  true,
					Error:   msg,
				}
				s.finishRun(task.ID, StatusFailed, &msg)
				bgTask.Progress = 100
				bgTask.Status = "FAILED"
				bgTask.ProgressEta = fmt.Sprintf("Failed: %s", msg)
				bgTask.Logs = json.RawMessage(fmt.Sprintf("[%q,%q]", fmt.Sprintf("[queue] Starting %s", task.Title), fmt.Sprintf("[error] %s", msg)))
				if opts.TaskStore != nil {
					_ = opts.TaskStore.SaveTask(bgTask)
				}
				if opts.Broadcast != nil {
					opts.Broadcast(map[string]any{"type": "task_updated", "task": bgTask})
				}
				outcome = Outcome{Result: &res, HaltedOnError: true}
			}
		}()

		result, runErr := ExecuteTaskOneByOne(ctx, task, opts)
		if runErr != nil {
			msg := RedactSecrets(runErr.Error())
			result = ExecutionResult{
				Success: false,
				TaskID:  task.ID,
				Changed: false,
				Failed:  true,
				Error:   msg,
			}
		}

		if result.Success && !result.Failed {
			s.finishRun(task.ID, StatusCompleted, nil)
			if opts.Invalidator != nil {
				// Invalidation is a cache hint: a failure there must never fail the
				// task that really succeeded on the host.
				_ = opts.Invalidator.InvalidateForTask(task)
			}
			bgTask.Progress = 100
			bgTask.Status = "OK"
			if result.Changed {
				bgTask.ProgressEta = "Completed (changed)"
			} else {
				bgTask.ProgressEta = "Completed (ok)"
			}
			logLines := []string{fmt.Sprintf("[queue] Starting %s on %s", task.Title, bgTask.NodeTarget)}
			if result.JSONOutput != nil {
				if b, err := json.Marshal(result.JSONOutput); err == nil {
					logLines = append(logLines, RedactSecrets(string(b)))
				}
			}
			logLines = append(logLines, fmt.Sprintf("[queue] Completed in %dms", result.DurationMs))
			if b, err := json.Marshal(logLines); err == nil {
				bgTask.Logs = json.RawMessage(b)
			}
			if opts.TaskStore != nil {
				_ = opts.TaskStore.SaveTask(bgTask)
			}
			if opts.Broadcast != nil {
				opts.Broadcast(map[string]any{"type": "task_updated", "task": bgTask})
			}
			outcome = Outcome{Result: &result, HaltedOnError: false}
			return
		}

		msg := result.Error
		if msg == "" {
			msg = "Ansible task execution failed"
		}
		result.Error = msg
		s.finishRun(task.ID, StatusFailed, &msg)
		bgTask.Progress = 100
		bgTask.Status = "FAILED"
		bgTask.ProgressEta = fmt.Sprintf("Failed: %s", msg)
		logLines := []string{
			fmt.Sprintf("[queue] Starting %s on %s", task.Title, bgTask.NodeTarget),
			fmt.Sprintf("[error] %s", msg),
		}
		if b, err := json.Marshal(logLines); err == nil {
			bgTask.Logs = json.RawMessage(b)
		}
		if opts.TaskStore != nil {
			_ = opts.TaskStore.SaveTask(bgTask)
		}
		if opts.Broadcast != nil {
			opts.Broadcast(map[string]any{"type": "task_updated", "task": bgTask})
		}
		outcome = Outcome{Result: &result, HaltedOnError: true}
	}()

	outcome.RemainingCount = s.StagedCount()
	return outcome, nil
}

// ExecuteNext is a convenience alias for ExecuteNextTask.
func (s *Store) ExecuteNext(ctx context.Context, opts Options) (Outcome, error) {
	return s.ExecuteNextTask(ctx, opts)
}

// formatExitError is used by tests and callers that need the TS-compatible
// "exited with code N" text.
func formatExitError(code int) string {
	return "Ansible playbook exited with code " + strconv.Itoa(code)
}

// taskRunsAsRoot reports whether the task logs in as root, in which case become
// is pointless and run0's TTY requirement must not be forced.
func taskRunsAsRoot(task Task) bool {
	user := ""
	if task.Connection != nil {
		user = strings.TrimSpace(task.Connection.User)
	}
	if user == "" {
		user = strings.TrimSpace(task.TargetSSHUser)
	}
	return strings.EqualFold(user, "root")
}

// becomeMethodFor resolves the become method a task will run with.
func becomeMethodFor(task Task) string {
	if task.Connection != nil {
		if m := strings.TrimSpace(task.Connection.BecomeMethod); m != "" {
			return qualifyBecomeMethod(m)
		}
	}
	return qualifyBecomeMethod(task.BecomeMethod)
}

// bridgeTaskToLoopback rewrites a single-host task so ansible connects to a
// loopback port served by ATC, when the target is only reachable through a
// plugin transport (tsnet). Without this, ansible's ssh client dials the
// tailnet address from the controller's host stack, which has no route to it.
//
// The returned stop function must be called once the run finishes.
func bridgeTaskToLoopback(task Task) (Task, func(), error) {
	if task.Connection == nil || task.PlaybookRun != nil {
		return task, nil, nil
	}
	route := strings.TrimSpace(task.Connection.Host)
	if route == "" {
		route = strings.TrimSpace(task.TargetHostRoute)
	}
	if !isTailnetAddress(route) {
		return task, nil, nil
	}

	port := task.Connection.Port
	if port == 0 {
		port = task.TargetSSHPort
	}
	if port == 0 {
		port = 22
	}

	dialer, err := plugin.ResolveDialer(plugin.HostFacts{NodeID: task.TargetNodeID, HostRoute: route})
	if err != nil {
		// No transport plugin can be resolved: leave the task untouched so the
		// failure is reported by ansible against the real route.
		return task, nil, nil
	}

	forwarder, err := startLoopbackForwarder(dialer, net.JoinHostPort(route, strconv.Itoa(port)))
	if err != nil {
		return task, nil, err
	}

	rewritten := task
	conn := *task.Connection
	conn.Host = "127.0.0.1"
	conn.Port = forwarder.port
	conn.ProxyCommand = ""
	rewritten.Connection = &conn
	rewritten.TargetHostRoute = "127.0.0.1"

	return rewritten, forwarder.Close, nil
}
