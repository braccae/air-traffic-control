package ansqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// APIOptions configures the queue HTTP surface.
type APIOptions struct {
	// Store is the queue store (required).
	Store *Store
	// RunOptions is the template for every /api/queue/execute call.
	RunOptions Options
	// Broadcast is the optional websocket hook. It is nil-safe.
	Broadcast func(event map[string]any)
}

// API mounts the five /api/queue* routes with the Express wire contract.
type API struct {
	store     *Store
	runOpts   Options
	broadcast func(event map[string]any)
}

// NewAPI validates its inputs and builds the queue API.
func NewAPI(opts APIOptions) (*API, error) {
	if opts.Store == nil {
		return nil, errors.New("ansqueue: store is required")
	}
	if err := opts.Store.Err(); err != nil {
		return nil, err
	}
	return &API{store: opts.Store, runOpts: opts.RunOptions, broadcast: opts.Broadcast}, nil
}

// Register mounts every queue route. Wrong methods fall through to the daemon's
// JSON 404 catch-all (Express has no route for them either).
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/queue", a.handleQueue)
	mux.HandleFunc("/api/queue/task", a.handleTask)
	mux.HandleFunc("/api/queue/tasks", a.handleTask)
	mux.HandleFunc("/api/queue/task/", a.handleTaskByID)
	mux.HandleFunc("/api/queue/execute", a.handleExecute)
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

func (a *API) handleQueue(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := a.store.Snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"success":   true,
			"tasks":     q.Tasks,
			"isRunning": q.IsRunning,
		})
	case http.MethodDelete:
		if err := a.store.Clear(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		a.emitQueueUpdated()
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		a.notFound(w, r)
	}
}

func (a *API) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}

	rawBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid task payload: title, targetNodeId, targetHostRoute, and non-empty steps required",
		})
		return
	}

	var task Task
	if err := json.Unmarshal(rawBytes, &task); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid task payload: title, targetNodeId, targetHostRoute, and non-empty steps required",
		})
		return
	}

	// Support wrapped plugin TaskSpec payloads (`{"nodeId": "...", "task": {"name": "...", "steps": [...]}}`).
	if task.Title == "" && task.TargetNodeID == "" {
		var wrapped struct {
			NodeID string `json:"nodeId"`
			Task   struct {
				ID            string            `json:"id"`
				Name          string            `json:"name"`
				Title         string            `json:"title"`
				Category      string            `json:"category"`
				PrimaryModule string            `json:"primaryModule"`
				Become        *bool             `json:"become"`
				SecretsMasked map[string]string `json:"secretsMasked"`
				Steps         []struct {
					Name       string         `json:"name"`
					Module     string         `json:"module"`
					Params     map[string]any `json:"params"`
					Parameters map[string]any `json:"parameters"`
				} `json:"steps"`
			} `json:"task"`
		}
		if err := json.Unmarshal(rawBytes, &wrapped); err == nil && wrapped.NodeID != "" && (wrapped.Task.Name != "" || wrapped.Task.Title != "") {
			title := firstNonEmpty(wrapped.Task.Title, wrapped.Task.Name)
			steps := make([]Step, len(wrapped.Task.Steps))
			for i, s := range wrapped.Task.Steps {
				params := s.Parameters
				if len(params) == 0 {
					params = s.Params
				}
				if params == nil {
					params = map[string]any{}
				}
				steps[i] = Step{
					Name:       s.Name,
					Module:     s.Module,
					Parameters: params,
				}
			}
			primaryMod := wrapped.Task.PrimaryModule
			if primaryMod == "" && len(steps) > 0 {
				primaryMod = steps[0].Module
			}
			task = Task{
				ID:              wrapped.Task.ID,
				Title:           title,
				TargetNodeID:    wrapped.NodeID,
				TargetNodeName:  wrapped.NodeID,
				TargetHostRoute: wrapped.NodeID,
				Category:        firstNonEmpty(wrapped.Task.Category, "database"),
				PrimaryModule:   firstNonEmpty(primaryMod, "ansible.builtin.command"),
				Steps:           steps,
				SecretsMasked:   wrapped.Task.SecretsMasked,
				Become:          wrapped.Task.Become,
				Status:          StatusStaged,
			}
		}
	}

	if task.Title == "" || task.TargetNodeID == "" || task.TargetHostRoute == "" || (len(task.Steps) == 0 && task.PlaybookRun == nil) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid task payload: title, targetNodeId, targetHostRoute, and non-empty steps required",
		})
		return
	}

	enqueued, err := a.store.Enqueue(task)
	if err != nil {
		msg := err.Error()
		if msg == "" {
			msg = "Failed to enqueue task"
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": msg})
		return
	}

	a.emitQueueUpdated()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "task": enqueued})
}

func (a *API) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		a.notFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/queue/task/")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		a.notFound(w, r)
		return
	}

	removed, err := a.store.Remove(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if !removed {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Task %s not found in queue", id),
		})
		return
	}

	a.emitQueueUpdated()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (a *API) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}

	var body struct {
		TaskID        string `json:"taskId"`
		InventoryPath string `json:"inventoryPath"`
	}
	if r.Body != nil {
		// An absent body is `{}` (Express `req.body || {}`), malformed JSON is a 400.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid JSON body"})
			return
		}
	}

	inventoryPath := body.InventoryPath
	if inventoryPath == "" {
		workdir := a.runOpts.WorkDir
		if workdir == "" {
			workdir = ResolveWorkDir()
		}
		candidate := filepath.Join(workdir, "inventory", "tailscale_inventory.py")
		if _, err := os.Stat(candidate); err == nil {
			inventoryPath = candidate
		}
		// When the default inventory is absent, leave it empty so the runner
		// applies its own "<targetHostRoute>," fallback.
	}

	if a.broadcast != nil {
		event := map[string]any{
			"type":  "queue_execution_started",
			"queue": a.store.Snapshot(),
		}
		if body.TaskID != "" {
			event["taskId"] = body.TaskID
		}
		a.broadcast(event)
	}

	opts := a.runOpts
	opts.TaskID = body.TaskID
	opts.InventoryPath = inventoryPath

	// Detach from the request context: a client that hangs up must not kill a
	// mutation already running on a host (Express keeps the promise alive). The
	// runner's own per-playbook Timeout still bounds the run.
	outcome, err := a.store.ExecuteNextTask(context.WithoutCancel(r.Context()), opts)

	if a.broadcast != nil {
		event := map[string]any{
			"type":           "queue_execution_finished",
			"result":         outcome.Result,
			"remainingCount": outcome.RemainingCount,
			"haltedOnError":  outcome.HaltedOnError,
			"queue":          a.store.Snapshot(),
		}
		if outcome.Result != nil {
			event["taskId"] = outcome.Result.TaskID
		}
		a.broadcast(event)
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success":        false,
			"result":         nil,
			"remainingCount": a.store.StagedCount(),
			"haltedOnError":  false,
			"error":          RedactSecrets(err.Error()),
		})
		return
	}

	success := outcome.Result != nil && outcome.Result.Success
	writeJSON(w, http.StatusOK, map[string]any{
		"success":        success,
		"result":         outcome.Result,
		"remainingCount": outcome.RemainingCount,
		"haltedOnError":  outcome.HaltedOnError,
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (a *API) emitQueueUpdated() {
	if a.broadcast == nil {
		return
	}
	a.broadcast(map[string]any{
		"type":  "queue_updated",
		"queue": a.store.Snapshot(),
	})
}

func (a *API) notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"success": false,
		"error":   fmt.Sprintf("API route not found: %s %s", r.Method, r.URL.Path),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
