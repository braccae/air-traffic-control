package ansqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
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

var safeIDPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// safeID normalises a task id for use in an ephemeral filename.
func safeID(id string) string { return safeIDPattern.ReplaceAllString(id, "_") }

// ExecuteTaskOneByOne compiles the task to an ephemeral playbook, runs it with
// ANSIBLE_STDOUT_CALLBACK=json, parses the callback JSON and derives
// changed/failed/error exactly like executeQueueTaskOneByOne.
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
	ephemeralFile := filepath.Join(playbooksDir, ".queue-"+id+".yml")
	if err := os.WriteFile(ephemeralFile, []byte(CompileOnTheFlyPlaybook(task)), 0o600); err != nil {
		return ExecutionResult{}, fmt.Errorf("ansqueue: write playbook %s: %w", ephemeralFile, err)
	}
	defer os.Remove(ephemeralFile)

	ephemeralInventory := ""
	defer func() {
		if ephemeralInventory != "" {
			os.Remove(ephemeralInventory)
		}
	}()

	inventoryPath := opts.InventoryPath
	if inventoryPath == "" {
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
	if !strings.HasSuffix(inventoryPath, ",") {
		if limit := firstNonEmpty(task.TargetNodeName, task.TargetNodeID); limit != "" {
			args = append(args, "--limit", limit)
		}
	}
	args = append(args, ephemeralFile)
	if opts.VaultVarsFile != "" {
		args = append(args, "--extra-vars", "@"+opts.VaultVarsFile)
	}

	env := buildEnv(workdir, opts)
	ansibleBin := opts.AnsibleBinary
	if ansibleBin == "" {
		ansibleBin = "ansible-playbook"
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
	env["ANSIBLE_STDOUT_CALLBACK"] = "json"
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
			outcome = Outcome{Result: &result, HaltedOnError: false}
			return
		}

		msg := result.Error
		if msg == "" {
			msg = "Ansible task execution failed"
		}
		result.Error = msg
		s.finishRun(task.ID, StatusFailed, &msg)
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
