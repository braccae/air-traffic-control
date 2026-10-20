package ansqueue

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestLiveQueueExecution is the only test that touches a real host. It is
// skipped unless ATC_ANSQUEUE_LIVE_HOST names a reachable SSH target, so the
// normal `go test ./...` run never depends on the network.
func TestLiveQueueExecution(t *testing.T) {
	host := os.Getenv("ATC_ANSQUEUE_LIVE_HOST")
	if host == "" {
		t.Skip("set ATC_ANSQUEUE_LIVE_HOST to run the live ansible queue test")
	}
	user := os.Getenv("ATC_ANSQUEUE_LIVE_USER")
	if user == "" {
		user = "core"
	}
	port := 22
	if raw := os.Getenv("ATC_ANSQUEUE_LIVE_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("ATC_ANSQUEUE_LIVE_PORT: %v", err)
		}
		port = p
	}

	store := newTestStore(t)
	workdir := t.TempDir()
	opts := Options{WorkDir: workdir, Timeout: 2 * time.Minute}

	ping := Task{
		Title:           "Live ping " + host,
		TargetNodeID:    "live-node",
		TargetNodeName:  "live-node",
		TargetHostRoute: host,
		TargetSSHUser:   user,
		TargetSSHPort:   port,
		Category:        "system",
		PrimaryModule:   "ansible.builtin.ping",
		Steps:           []Step{{Name: "Ping", Module: "ansible.builtin.ping", Parameters: map[string]any{}}},
		Become:          BoolPtr(false),
		BecomeMethod:    "run0",
		Status:          StatusStaged,
		Connection: &Connection{
			Plugin: "connection/ssh",
			Host:   host,
			Port:   port,
			User:   user,
		},
	}
	if _, err := store.Enqueue(ping); err != nil {
		t.Fatal(err)
	}

	outcome, err := store.ExecuteNextTask(context.Background(), opts)
	if err != nil {
		t.Fatalf("ExecuteNextTask: %v", err)
	}
	if outcome.Result == nil || !outcome.Result.Success {
		t.Fatalf("live ping failed: %+v", outcome.Result)
	}

	// Now stage a deliberately failing task followed by a third task: the
	// checkpoint halt must stop the queue before the third one runs.
	failing := ping
	failing.ID = ""
	failing.Title = "Live failing assert " + host
	failing.Steps = []Step{{
		Name:       "Assert false",
		Module:     "ansible.builtin.assert",
		Parameters: map[string]any{"that": []any{false}},
	}}
	if _, err := store.Enqueue(failing); err != nil {
		t.Fatal(err)
	}

	after := ping
	after.ID = ""
	after.Title = "Live follow-up " + host
	if _, err := store.Enqueue(after); err != nil {
		t.Fatal(err)
	}

	outcome, err = store.ExecuteNextTask(context.Background(), opts)
	if err != nil {
		t.Fatalf("ExecuteNextTask (failing): %v", err)
	}
	if outcome.Result == nil || outcome.Result.Success || !outcome.HaltedOnError {
		t.Fatalf("expected a halted failure, got %+v", outcome)
	}

	snap := store.Snapshot()
	var staged int
	for _, task := range snap.Tasks {
		if task.Title == after.Title && task.Status != StatusStaged {
			t.Fatalf("follow-up task ran despite the halt: %q", task.Status)
		}
		if task.Status == StatusStaged {
			staged++
		}
	}
	if staged != 1 || outcome.RemainingCount != 1 {
		t.Fatalf("staged=%d remainingCount=%d, want 1", staged, outcome.RemainingCount)
	}
}
