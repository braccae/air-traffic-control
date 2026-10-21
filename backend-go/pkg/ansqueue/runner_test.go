package ansqueue

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const okPayload = `{"plays":[{"play":{"name":"x"},"tasks":[{"task":{"name":"Ping"},"hosts":{"node-a":{"changed":true,"failed":false}}}]}],"stats":{"node-a":{"changed":1,"failures":0,"unreachable":0}}}`

const failingPayload = `{"plays":[{"play":{"name":"x"},"tasks":[{"task":{"name":"Assert"},"hosts":{"node-a":{"failed":true,"msg":"Assertion failed"}}}]}],"stats":{"node-a":{"changed":0,"failures":1,"unreachable":0}}}`

func staticExec(result ExecResult, err error) ExecFunc {
	return func(_ context.Context, _ string, _ []string, _ ExecOptions) (ExecResult, error) {
		return result, err
	}
}

func successExec() ExecFunc {
	return staticExec(ExecResult{Stdout: okPayload, ExitCode: 0}, nil)
}

func failureExec() ExecFunc {
	return staticExec(ExecResult{Stdout: failingPayload, ExitCode: 2}, nil)
}

func TestParseAnsibleJSONStdout(t *testing.T) {
	t.Run("clean json", func(t *testing.T) {
		got, err := ParseAnsibleJSONStdout(`{"plays":[]}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		obj, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("got %T, want map", got)
		}
		if plays, ok := obj["plays"].([]any); !ok || len(plays) != 0 {
			t.Fatalf("plays = %#v", obj["plays"])
		}
	})

	t.Run("json surrounded by warnings", func(t *testing.T) {
		stdout := "WARNING: ansible.cfg is world writable\n[DEPRECATION WARNING]: x\n" + okPayload + "\nPLAY RECAP *****\n"
		got, err := ParseAnsibleJSONStdout(stdout)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		obj := got.(map[string]any)
		if _, ok := obj["plays"].([]any); !ok {
			t.Fatalf("plays missing: %#v", obj)
		}
	})

	t.Run("nested braces and escaped quotes", func(t *testing.T) {
		stdout := `noise {"plays":[{"tasks":[{"hosts":{"h":{"msg":"a { b } \"quoted\" and } brace","failed":false}}}]}]} trailing noise`
		got, err := ParseAnsibleJSONStdout(stdout)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		obj := got.(map[string]any)
		plays := obj["plays"].([]any)
		tasks := plays[0].(map[string]any)["tasks"].([]any)
		hosts := tasks[0].(map[string]any)["hosts"].(map[string]any)
		msg := hosts["h"].(map[string]any)["msg"]
		if msg != `a { b } "quoted" and } brace` {
			t.Fatalf("msg = %q", msg)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		_, err := ParseAnsibleJSONStdout("")
		if err == nil || err.Error() != "Ansible stdout is empty or not a string" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("missing plays", func(t *testing.T) {
		_, err := ParseAnsibleJSONStdout("ERROR! the playbook could not be found")
		if err == nil || err.Error() != "No JSON object found in Ansible stdout (missing plays)" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("plays key but no object start", func(t *testing.T) {
		_, err := ParseAnsibleJSONStdout(`"plays" = []`)
		if err == nil || err.Error() != "No JSON object found in Ansible stdout" {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExecuteTaskOneByOneParsesAndCleansUp(t *testing.T) {
	workdir := t.TempDir()
	task := minimalTask()
	task.ID = "task/one by one"

	var captured []string
	opts := Options{
		WorkDir: workdir,
		Exec: func(_ context.Context, name string, args []string, opt ExecOptions) (ExecResult, error) {
			captured = append([]string{name}, args...)
			if opt.Env["ANSIBLE_STDOUT_CALLBACK"] != "json" {
				t.Fatalf("ANSIBLE_STDOUT_CALLBACK missing from child env: %#v", opt.Env)
			}
			if opt.Cwd != workdir {
				t.Fatalf("cwd = %q, want %q", opt.Cwd, workdir)
			}
			// The ephemeral playbook must exist while ansible runs.
			if _, err := os.Stat(args[len(args)-1]); err != nil {
				t.Fatalf("playbook missing at exec time: %v", err)
			}
			return ExecResult{Stdout: okPayload, ExitCode: 0}, nil
		},
	}

	res, err := ExecuteTaskOneByOne(context.Background(), task, opts)
	if err != nil {
		t.Fatalf("ExecuteTaskOneByOne: %v", err)
	}
	if !res.Success || !res.Changed || res.Failed {
		t.Fatalf("result = %+v", res)
	}
	if res.TaskID != task.ID {
		t.Fatalf("taskId = %q", res.TaskID)
	}
	if res.JSONOutput == nil {
		t.Fatal("jsonOutput was not captured")
	}
	if res.DurationMs < 0 {
		t.Fatalf("durationMs = %d", res.DurationMs)
	}

	if captured[0] != "ansible-playbook" {
		t.Fatalf("binary = %q", captured[0])
	}
	if captured[1] != "-i" {
		t.Fatalf("args = %v", captured)
	}
	// connection == nil -> "<route>," inventory, so no --limit.
	if !strings.HasSuffix(captured[2], ",") {
		t.Fatalf("inventory = %q, want the host-route fallback", captured[2])
	}
	if strings.Contains(strings.Join(captured, " "), "--limit") {
		t.Fatalf("--limit must not be passed with a trailing-comma inventory: %v", captured)
	}

	entries, err := os.ReadDir(filepath.Join(workdir, "ansible", "playbooks"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Fatalf("ephemeral playbook left behind: %s", e.Name())
	}

	invDir := filepath.Join(workdir, "ansible", "inventory")
	if entries, err := os.ReadDir(invDir); err == nil {
		for _, e := range entries {
			t.Fatalf("ephemeral inventory left behind: %s", e.Name())
		}
	}
}

func TestExecuteTaskOneByOneWritesInventoryAndLimit(t *testing.T) {
	workdir := t.TempDir()
	task := minimalTask()
	task.ID = "task-inv"
	task.Connection = &Connection{Plugin: "connection/ssh", Host: "10.0.0.5", Port: 2222, User: "deploy"}

	var invPath, limit string
	opts := Options{
		WorkDir: workdir,
		Exec: func(_ context.Context, _ string, args []string, _ ExecOptions) (ExecResult, error) {
			invPath = args[1]
			for i := range args {
				if args[i] == "--limit" && i+1 < len(args) {
					limit = args[i+1]
				}
			}
			data, err := os.ReadFile(invPath)
			if err != nil {
				t.Fatalf("inventory not written: %v", err)
			}
			if !strings.Contains(string(data), `"ansible_host": "10.0.0.5"`) {
				t.Fatalf("inventory content wrong: %s", data)
			}
			return ExecResult{Stdout: okPayload, ExitCode: 0}, nil
		},
	}

	if _, err := ExecuteTaskOneByOne(context.Background(), task, opts); err != nil {
		t.Fatal(err)
	}
	if limit != "a" {
		t.Fatalf("--limit = %q, want the target node name", limit)
	}
	if _, err := os.Stat(invPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral inventory %s not cleaned up", invPath)
	}
}

// TestExecuteTaskRealLocalAnsible exercises the PRODUCTION execution path — no
// Exec seam: it really spawns ansible-playbook with ANSIBLE_STDOUT_CALLBACK=json
// and parses the callback output — against this machine over
// ansible_connection=local. It skips when ansible-playbook is not installed.
func TestExecuteTaskRealLocalAnsible(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-playbook not installed")
	}

	task := minimalTask()
	task.ID = "task-real-local"
	task.TargetHostRoute = "127.0.0.1"
	task.TargetNodeName = "local-smoke"
	task.TargetSSHUser = "root"
	task.Connection = &Connection{Plugin: "connection/local", Host: "127.0.0.1", User: "root"}
	task.Steps = []Step{{Name: "Ping localhost", Module: "ansible.builtin.ping", Parameters: map[string]any{}}}
	task.Become = BoolPtr(false)

	res, err := ExecuteTaskOneByOne(context.Background(), task, Options{WorkDir: t.TempDir(), Timeout: 90 * time.Second})
	if err != nil {
		t.Fatalf("ExecuteTaskOneByOne: %v", err)
	}
	if !res.Success || res.Failed {
		t.Fatalf("real local ping failed: %+v (jsonOutput=%v)", res, res.JSONOutput)
	}
	obj, ok := res.JSONOutput.(map[string]any)
	if !ok {
		t.Fatalf("jsonOutput = %T, want a parsed object", res.JSONOutput)
	}
	if _, ok := obj["plays"].([]any); !ok {
		t.Fatalf("parsed output has no plays array: %#v", obj)
	}
}

func TestExecuteNextTaskHaltsOnFailure(t *testing.T) {
	store := newTestStore(t)
	workdir := t.TempDir()

	first := minimalTask()
	first.ID = "task-first"
	second := minimalTask()
	second.ID = "task-second"
	if _, err := store.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enqueue(second); err != nil {
		t.Fatal(err)
	}

	outcome, err := store.ExecuteNextTask(context.Background(), Options{WorkDir: workdir, Exec: failureExec()})
	if err != nil {
		t.Fatalf("ExecuteNextTask: %v", err)
	}
	if outcome.Result == nil || outcome.Result.Success {
		t.Fatalf("result = %+v", outcome.Result)
	}
	if !outcome.HaltedOnError {
		t.Fatal("haltedOnError must be true after a failure")
	}
	if outcome.Result.TaskID != "task-first" {
		t.Fatalf("executed %q, want task-first", outcome.Result.TaskID)
	}
	if outcome.Result.Error != "Assertion failed" {
		t.Fatalf("error = %q", outcome.Result.Error)
	}
	if outcome.RemainingCount != 1 {
		t.Fatalf("remainingCount = %d, want 1", outcome.RemainingCount)
	}

	snap := store.Snapshot()
	if snap.IsRunning {
		t.Fatal("isRunning must be cleared after a run")
	}
	if snap.Tasks[0].Status != StatusFailed || snap.Tasks[0].Error != "Assertion failed" {
		t.Fatalf("first task state = %+v", snap.Tasks[0])
	}
	if snap.Tasks[1].Status != StatusStaged {
		t.Fatalf("second task must stay staged, got %q", snap.Tasks[1].Status)
	}

	// A subsequent execute with a healthy ansible completes the next task.
	outcome, err = store.ExecuteNextTask(context.Background(), Options{WorkDir: workdir, Exec: successExec()})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Result == nil || !outcome.Result.Success || outcome.HaltedOnError {
		t.Fatalf("second run outcome = %+v", outcome)
	}
	if outcome.Result.TaskID != "task-second" {
		t.Fatalf("executed %q, want task-second", outcome.Result.TaskID)
	}
	if outcome.RemainingCount != 0 {
		t.Fatalf("remainingCount = %d", outcome.RemainingCount)
	}
	snap = store.Snapshot()
	if snap.Tasks[1].Status != StatusCompleted {
		t.Fatalf("second task status = %q", snap.Tasks[1].Status)
	}
}

func TestExecuteNextTaskHaltsOnParseFailure(t *testing.T) {
	store := newTestStore(t)

	task := minimalTask()
	task.ID = "task-garbage"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	outcome, err := store.ExecuteNextTask(context.Background(), Options{
		WorkDir: t.TempDir(),
		Exec:    staticExec(ExecResult{Stdout: "this is not json", ExitCode: 0}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Result == nil || !outcome.Result.Failed || !outcome.HaltedOnError {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !strings.Contains(outcome.Result.Error, "No JSON object found in Ansible stdout") {
		t.Fatalf("error = %q", outcome.Result.Error)
	}
	if got := store.Snapshot().Tasks[0]; got.Status != StatusFailed {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestExecuteNextTaskSpawnFailureIsReported(t *testing.T) {
	store := newTestStore(t)

	task := minimalTask()
	task.ID = "task-spawn"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	outcome, err := store.ExecuteNextTask(context.Background(), Options{
		WorkDir: t.TempDir(),
		Exec:    staticExec(ExecResult{}, errors.New("exec: \"ansible-playbook\": executable file not found in $PATH")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Result == nil || !outcome.Result.Failed || !outcome.HaltedOnError {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !strings.Contains(outcome.Result.Error, "executable file not found") {
		t.Fatalf("error = %q", outcome.Result.Error)
	}
}

func TestExecuteNextTaskNoStagedTask(t *testing.T) {
	store := newTestStore(t)

	outcome, err := store.ExecuteNextTask(context.Background(), Options{WorkDir: t.TempDir(), Exec: successExec()})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Result != nil || outcome.HaltedOnError || outcome.RemainingCount != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}

	// A specific task id that is not staged must not run anything.
	task := minimalTask()
	task.ID = "task-x"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}
	outcome, err = store.ExecuteNextTask(context.Background(), Options{WorkDir: t.TempDir(), TaskID: "nope", Exec: successExec()})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Result != nil {
		t.Fatalf("ran an unrequested task: %+v", outcome.Result)
	}
	if store.Snapshot().Tasks[0].Status != StatusStaged {
		t.Fatal("task must stay staged")
	}
}

func TestExecuteNextTaskInvalidatesOnSuccessOnly(t *testing.T) {
	store := newTestStore(t)

	counter := &countingInvalidator{}
	opts := Options{WorkDir: t.TempDir(), Exec: failureExec(), Invalidator: counter}

	task := minimalTask()
	task.ID = "task-inv-fail"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecuteNextTask(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if counter.calls != 0 {
		t.Fatalf("invalidator called on a failed run: %d", counter.calls)
	}

	opts.Exec = successExec()
	task2 := minimalTask()
	task2.ID = "task-inv-ok"
	if _, err := store.Enqueue(task2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecuteNextTask(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if counter.calls != 1 {
		t.Fatalf("invalidator calls = %d, want 1", counter.calls)
	}
}

type countingInvalidator struct{ calls int }

func (c *countingInvalidator) InvalidateForTask(Task) error {
	c.calls++
	return nil
}

func TestTaskJSONIsAcceptedByEncoder(t *testing.T) {
	task := minimalTask()
	task.Title = "A <b> & \"c\""
	blob, err := MarshalTaskJSON(task)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), "A <b> & \\\"c\\\"") {
		t.Fatalf("HTML escaping was not disabled: %s", blob)
	}
	var back Task
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	if back.Title != task.Title {
		t.Fatalf("round-trip changed the title: %q", back.Title)
	}
}
