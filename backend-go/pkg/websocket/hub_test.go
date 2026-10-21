package websocket_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"

	"air-traffic-control/backend/pkg/clusterapi"
	"air-traffic-control/backend/pkg/websocket"
)

func TestCheckOrigin(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		origin  string
		allowed bool
	}{
		{
			name:    "empty origin (CLI/tests)",
			host:    "localhost:8080",
			origin:  "",
			allowed: true,
		},
		{
			name:    "same-origin exact match",
			host:    "100.97.159.101:8080",
			origin:  "http://100.97.159.101:8080",
			allowed: true,
		},
		{
			name:    "tailscale domain .ts.net",
			host:    "100.97.159.101:8080",
			origin:  "https://dev-pc.leo-banjo.ts.net",
			allowed: true,
		},
		{
			name:    "tailscale subdomain with dev port",
			host:    "100.97.159.101:8080",
			origin:  "http://dev-pc.leo-banjo.ts.net:5173",
			allowed: true,
		},
		{
			name:    "localhost dev port 5173",
			host:    "localhost:8080",
			origin:  "http://localhost:5173",
			allowed: true,
		},
		{
			name:    "127.0.0.1 dev port 3000",
			host:    "127.0.0.1:8080",
			origin:  "http://127.0.0.1:3000",
			allowed: true,
		},
		{
			name:    "127.0.0.1 dev port 18090",
			host:    "127.0.0.1:18090",
			origin:  "http://127.0.0.1:18090",
			allowed: true,
		},
		{
			name:    "unauthorized external origin",
			host:    "localhost:8080",
			origin:  "http://evil-attacker.com",
			allowed: false,
		},
		{
			name:    "malformed origin",
			host:    "localhost:8080",
			origin:  "://bad",
			allowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "http://"+tt.host+"/ws", nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			got := websocket.CheckOrigin(req)
			if got != tt.allowed {
				t.Errorf("CheckOrigin() = %v, want %v (origin=%s, host=%s)", got, tt.allowed, tt.origin, tt.host)
			}
		})
	}
}

func TestHub_ConnectAndInitialState(t *testing.T) {
	connReg := clusterapi.NewConnectionRegistry()
	logStore := clusterapi.NewLogStore()
	logStore.Record(clusterapi.LogEntry{
		Kind:    clusterapi.KindApp,
		Level:   "info",
		Message: "test coordinator startup",
	})

	hub := websocket.NewHubWithOptions(websocket.HubOptions{
		ConnReg:  connReg,
		LogStore: logStore,
	})
	go hub.Run()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", "http://localhost:5173")

	ws, _, err := gorilla.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer ws.Close()

	// Wait and read messages sent upon connection
	receivedInit := false
	receivedInitialState := false
	receivedClusterLog := false
	receivedConns := false

	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 6; i++ {
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var frame map[string]any
		if err := json.Unmarshal(msgBytes, &frame); err == nil {
			msgType, _ := frame["type"].(string)
			switch msgType {
			case "init":
				receivedInit = true
			case "initial_state":
				receivedInitialState = true
			case "cluster_log_batch":
				receivedClusterLog = true
			case "connections_updated":
				receivedConns = true
			}
		}
		if receivedInit && receivedInitialState && receivedClusterLog && receivedConns {
			break
		}
	}

	if !receivedInit {
		t.Errorf("expected 'init' message on connection")
	}
	if !receivedInitialState {
		t.Errorf("expected 'initial_state' message on connection")
	}
	if !receivedClusterLog {
		t.Errorf("expected 'cluster_log_batch' message on connection")
	}
	if !receivedConns {
		t.Errorf("expected 'connections_updated' message on connection")
	}
}

func TestHub_TerminalPTYLifecycle(t *testing.T) {
	hub := websocket.NewHub()
	go hub.Run()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", "http://localhost:5173")

	ws, _, err := gorilla.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer ws.Close()

	// Drain init frames: read until we see initial_state
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var frame map[string]any
		if err := json.Unmarshal(msgBytes, &frame); err == nil {
			if frame["type"] == "init" || frame["type"] == "initial_state" {
				break
			}
		}
	}

	// 1. Send terminal_init / terminal_start
	sessionID := "test-session-1"
	initReq := map[string]any{
		"type":      "terminal_init",
		"sessionId": sessionID,
		"nodeId":    "dev-pc",
		"cols":      80,
		"rows":      24,
	}
	if err := ws.WriteJSON(initReq); err != nil {
		t.Fatalf("failed to send terminal_init: %v", err)
	}

	// 2. Read output from spawned PTY shell
	gotOutput := false
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 20; i++ {
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			t.Logf("ReadMessage err: %v", err)
			break
		}
		var frame map[string]any
		if err := json.Unmarshal(msgBytes, &frame); err == nil {
			t.Logf("Got frame: type=%v", frame["type"])
			if frame["type"] == "terminal_data" || frame["type"] == "terminal_output" {
				gotOutput = true
				break
			}
		}
	}
	if !gotOutput {
		t.Errorf("expected terminal_data or terminal_output frame from PTY")
	}

	// 3. Send terminal_resize
	resizeReq := map[string]any{
		"type":      "terminal_resize",
		"sessionId": sessionID,
		"cols":      100,
		"rows":      30,
	}
	if err := ws.WriteJSON(resizeReq); err != nil {
		t.Fatalf("failed to send terminal_resize: %v", err)
	}

	// 4. Send terminal_input
	inputReq := map[string]any{
		"type":      "terminal_input",
		"sessionId": sessionID,
		"data":      "echo test-input\n",
	}
	if err := ws.WriteJSON(inputReq); err != nil {
		t.Fatalf("failed to send terminal_input: %v", err)
	}

	// 5. Send terminal_close
	closeReq := map[string]any{
		"type":      "terminal_close",
		"sessionId": sessionID,
	}
	if err := ws.WriteJSON(closeReq); err != nil {
		t.Fatalf("failed to send terminal_close: %v", err)
	}
}

func TestHub_BroadcastStreams(t *testing.T) {
	hub := websocket.NewHub()
	go hub.Run()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", "http://localhost:5173")

	ws, _, err := gorilla.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer ws.Close()

	// Drain initial frames
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var frame map[string]any
		if err := json.Unmarshal(msgBytes, &frame); err == nil {
			if frame["type"] == "init" || frame["type"] == "initial_state" {
				break
			}
		}
	}

	// Broadcast tasks_update
	hub.BroadcastJSON(map[string]any{
		"type":   "tasks_update",
		"taskId": "task-test-1",
	})

	// Broadcast playbook_log
	hub.BroadcastJSON(map[string]any{
		"type":   "playbook_log",
		"taskId": "task-test-1",
		"output": "ok: [localhost]",
	})

	// Broadcast cluster_log
	hub.BroadcastJSON(map[string]any{
		"type":    "cluster_log",
		"message": "test cluster log",
	})

	// Broadcast connections_update
	hub.BroadcastJSON(map[string]any{
		"type":  "connections_update",
		"total": 5,
	})

	receivedTasks := false
	receivedPlaybook := false
	receivedClusterLog := false
	receivedConnections := false

	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 10; i++ {
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var frame map[string]any
		if err := json.Unmarshal(msgBytes, &frame); err == nil {
			switch frame["type"] {
			case "tasks_update":
				receivedTasks = true
			case "playbook_log":
				receivedPlaybook = true
			case "cluster_log":
				receivedClusterLog = true
			case "connections_update":
				receivedConnections = true
			}
		}
		if receivedTasks && receivedPlaybook && receivedClusterLog && receivedConnections {
			break
		}
	}

	if !receivedTasks {
		t.Errorf("expected tasks_update broadcast")
	}
	if !receivedPlaybook {
		t.Errorf("expected playbook_log broadcast")
	}
	if !receivedClusterLog {
		t.Errorf("expected cluster_log broadcast")
	}
	if !receivedConnections {
		t.Errorf("expected connections_update broadcast")
	}
}
