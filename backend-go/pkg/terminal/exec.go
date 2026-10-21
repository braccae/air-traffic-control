package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/nodeapi"
)

// NodeResolver resolves node details by query.
type NodeResolver interface {
	FindNode(query string) (*nodeapi.Object, error)
}

// CommandRunner dispatches commands to a node.
type CommandRunner interface {
	RunNodeCommand(ctx context.Context, node *nodeapi.Object, command string, timeout time.Duration) (string, error)
}

// Store allows saving tasks into persistence.
type Store interface {
	SaveTask(t hostcache.Task) error
}

// API handles /api/terminal/exec requests.
type API struct {
	resolver NodeResolver
	runner   CommandRunner
	store    Store
}

// NewAPI returns a new terminal API instance.
func NewAPI(resolver NodeResolver, runner CommandRunner, store Store) *API {
	return &API{
		resolver: resolver,
		runner:   runner,
		store:    store,
	}
}

// ExecRequestBody matches the input body for /api/terminal/exec.
type ExecRequestBody struct {
	Command string `json:"command"`
	Host    string `json:"host"`
	NodeID  string `json:"nodeId"`
	User    string `json:"user"`
}

// Register mounts the /api/terminal/exec endpoint on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/terminal/exec", a.HandleExec)
}

// HandleExec handles POST /api/terminal/exec.
func (a *API) HandleExec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(ExecResult{
			Success: false,
			Error:   "method not allowed",
		})
		return
	}

	var req ExecRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ExecResult{
			Success: false,
			Error:   fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	cmd := strings.TrimSpace(req.Command)
	targetQuery := req.Host
	if targetQuery == "" {
		targetQuery = req.NodeID
	}

	var targetNode *nodeapi.Object
	if a.resolver != nil {
		targetNode, _ = a.resolver.FindNode(targetQuery)
	}

	nodeID := targetQuery
	if targetNode != nil {
		if id, ok := targetNode.GetString("id"); ok && id != "" {
			nodeID = id
		}
	}

	now := time.Now().UnixMilli()

	if cmd == "" {
		_ = json.NewEncoder(w).Encode(ExecResult{
			Output:    "",
			ExitCode:  0,
			Stdout:    "",
			Stderr:    "",
			NodeID:    nodeID,
			Success:   true,
			Timestamp: now,
		})
		return
	}

	if cmd == "help" {
		out, exitCode := ParseTerminalCommand(cmd, targetNode)
		_ = json.NewEncoder(w).Encode(ExecResult{
			Output:    out,
			ExitCode:  exitCode,
			Stdout:    out,
			NodeID:    nodeID,
			Success:   exitCode == 0,
			Timestamp: now,
		})
		return
	}

	// Record task in hostcache if store is configured
	taskID := fmt.Sprintf("term-exec-%d", now)
	startTimeStr := time.Now().UTC().Format(time.RFC3339)
	if a.store != nil {
		_ = a.store.SaveTask(hostcache.Task{
			ID:          taskID,
			NodeTarget:  nodeID,
			Description: fmt.Sprintf("Terminal: %s", cmd),
			Status:      "RUNNING",
			StartTime:   startTimeStr,
			UpdatedAt:   now,
		})
	}

	var output string
	var runErr error
	if a.runner != nil && targetNode != nil {
		output, runErr = a.runner.RunNodeCommand(r.Context(), targetNode, fmt.Sprintf("%s 2>&1", cmd), 8*time.Second)
	} else {
		runErr = fmt.Errorf("no runner or node available")
	}

	if runErr == nil {
		cleanOut := strings.TrimRight(output, "\r\n ")
		if cleanOut == "" {
			cleanOut = "(command completed with exit code 0)"
		}
      execLogJSON, _ := json.Marshal([]string{cleanOut})
		if a.store != nil {
			_ = a.store.SaveTask(hostcache.Task{
				ID:          taskID,
				NodeTarget:  nodeID,
				Description: fmt.Sprintf("Terminal: %s", cmd),
				Status:      "OK",
				Progress:    100,
				ProgressEta: "Completed (exit code 0)",
				StartTime:   startTimeStr,
				Logs:        json.RawMessage(execLogJSON),
				UpdatedAt:   time.Now().UnixMilli(),
			})
		}
		_ = json.NewEncoder(w).Encode(ExecResult{
			Output:    cleanOut,
			Stdout:    cleanOut,
			ExitCode:  0,
			NodeID:    nodeID,
			Success:   true,
			Timestamp: now,
		})
		return
	}

	// Check if runErr produced output
	outStr := strings.TrimRight(output, "\r\n ")
	if outStr != "" {
		errLogJSON, _ := json.Marshal([]string{outStr})
		if a.store != nil {
			_ = a.store.SaveTask(hostcache.Task{
				ID:          taskID,
				NodeTarget:  nodeID,
				Description: fmt.Sprintf("Terminal: %s", cmd),
				Status:      "FAILED",
				Progress:    100,
				ProgressEta: "Failed (exit code 1)",
				StartTime:   startTimeStr,
				Logs:        json.RawMessage(errLogJSON),
				UpdatedAt:   time.Now().UnixMilli(),
			})
		}
		_ = json.NewEncoder(w).Encode(ExecResult{
			Output:    outStr,
			Stderr:    outStr,
			ExitCode:  1,
			NodeID:    nodeID,
			Success:   false,
			Error:     outStr,
			Timestamp: now,
		})
		return
	}

	// Fallback to parseTerminalCommand matching server.ts lines 1409-1410
	fallbackOut, fallbackCode := ParseTerminalCommand(cmd, targetNode)
	status := "FAILED"
	if fallbackCode == 0 {
		status = "OK"
	}
	fbLogJSON, _ := json.Marshal([]string{fallbackOut})
	if a.store != nil {
		_ = a.store.SaveTask(hostcache.Task{
			ID:          taskID,
			NodeTarget:  nodeID,
			Description: fmt.Sprintf("Terminal: %s", cmd),
			Status:      status,
			Progress:    100,
			StartTime:   startTimeStr,
			Logs:        json.RawMessage(fbLogJSON),
			UpdatedAt:   time.Now().UnixMilli(),
		})
	}

	_ = json.NewEncoder(w).Encode(ExecResult{
		Output:    fallbackOut,
		Stdout:    fallbackOut,
		ExitCode:  fallbackCode,
		NodeID:    nodeID,
		Success:   fallbackCode == 0,
		Timestamp: now,
	})
}
