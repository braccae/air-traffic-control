package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/clusterapi"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/nodeapi"
	"air-traffic-control/backend/pkg/plugin"
	sshclient "air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/terminal"
)

// isLocalNode checks if a node represents the local machine (localhost).
// Mirrors the logic in terminal.OpenSSHPTY for consistency.
func isLocalNode(node *nodeapi.Object) bool {
	if node == nil {
		return false
	}
	ip, _ := node.GetString("ip")
	shortName, _ := node.GetString("shortName")
	name, _ := node.GetString("name")
	return ip == "127.0.0.1" ||
		ip == "localhost" ||
		ip == "::1" ||
		shortName == "localhost" ||
		name == "localhost"
}

// isLocalQuery checks if a query string (host/nodeId) explicitly refers to localhost.
func isLocalQuery(query string) bool {
	return query == "localhost" || query == "127.0.0.1" || query == "::1"
}

// ConnectionMetrics holds connection statistics for monitoring
type ConnectionMetrics struct {
	TotalConnections    atomic.Int64
	ActiveConnections   atomic.Int64
	TotalPTYSessions    atomic.Int64
	ActivePTYSessions   atomic.Int64
	FailedUpgrades      atomic.Int64
	FailedPTYSpawns     atomic.Int64
	BytesReceived       atomic.Int64
	BytesSent           atomic.Int64
	ConnectionErrors    atomic.Int64
	LastConnectionTime  atomic.Int64 // Unix timestamp
	LastDisconnectTime  atomic.Int64 // Unix timestamp
}

// GetSnapshot returns a copy of current metrics
func (m *ConnectionMetrics) GetSnapshot() map[string]any {
	return map[string]any{
		"total_connections":   m.TotalConnections.Load(),
		"active_connections":  m.ActiveConnections.Load(),
		"total_pty_sessions":  m.TotalPTYSessions.Load(),
		"active_pty_sessions": m.ActivePTYSessions.Load(),
		"failed_upgrades":     m.FailedUpgrades.Load(),
		"failed_pty_spawns":   m.FailedPTYSpawns.Load(),
		"bytes_received":      m.BytesReceived.Load(),
		"bytes_sent":          m.BytesSent.Load(),
		"connection_errors":   m.ConnectionErrors.Load(),
		"last_connection":     m.LastConnectionTime.Load(),
		"last_disconnect":     m.LastDisconnectTime.Load(),
	}
}

// AllowedPort checks if a port is in the allowed dev ports list.
var allowedDevPorts = map[string]bool{
	"5173":  true,
	"3000":  true,
	"18090": true,
	"8080":  true,
}

// CheckOrigin validates WebSocket request origins according to security requirements:
// allows same-origin (r.Host), localhost / 127.0.0.1 / [::1] dev origins (:5173, :3000, :18090, :8080),
// .ts.net tailnet origins, or requests without an Origin header (CLI/testing).
func CheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // CLI / non-browser testing
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	hostName := u.Hostname()
	port := u.Port()

	// Same-origin check
	reqHost := r.Host
	if rHost, _, err := net.SplitHostPort(r.Host); err == nil {
		reqHost = rHost
	}
	if strings.EqualFold(hostName, reqHost) {
		return true
	}

	// Tailscale mesh check (.ts.net)
	if strings.HasSuffix(strings.ToLower(hostName), ".ts.net") {
		return true
	}

	// Dev host check (localhost / 127.0.0.1 / ::1)
	if hostName == "localhost" || hostName == "127.0.0.1" || hostName == "::1" {
		if port == "" || allowedDevPorts[port] {
			return true
		}
		// Also allow if port matches request host port
		if _, rPort, err := net.SplitHostPort(r.Host); err == nil && rPort == port {
			return true
		}
	}

	return false
}

var upgrader = websocket.Upgrader{
	CheckOrigin: CheckOrigin,
}

type Client struct {
	hub         *Hub
	conn        *websocket.Conn
	send        chan []byte
	ptySessions map[string]terminal.PTYSession
	closed      bool
	mu          sync.Mutex
}

// HubOptions configures data dependencies for initial state & streaming.
type HubOptions struct {
	Store      *hostcache.Store
	QueueStore *ansqueue.Store
	ConnReg    *clusterapi.ConnectionRegistry
	LogStore   *clusterapi.LogStore
	SSHManager *sshclient.ClientManager
	Resolver   terminal.NodeResolver
	// FactsResolver resolves inventory nodes to plugin HostFacts so interactive
	// PTY sessions can dial through the same connection-plugin transport
	// (tsnet mesh) as command runners instead of bare TCP.
	FactsResolver plugin.NodeResolver
}

type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.Mutex

	store      *hostcache.Store
	queueStore *ansqueue.Store
	connReg    *clusterapi.ConnectionRegistry
	logStore   *clusterapi.LogStore
	sshManager    *sshclient.ClientManager
	resolver      terminal.NodeResolver
	factsResolver plugin.NodeResolver

	connBroadcastTimer *time.Timer
	connTimerMu        sync.Mutex

	metrics ConnectionMetrics
}

func NewHub() *Hub {
	return NewHubWithOptions(HubOptions{})
}

func NewHubWithOptions(opts HubOptions) *Hub {
	return &Hub{
		broadcast:  make(chan []byte, 1024),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
		store:      opts.Store,
		queueStore: opts.QueueStore,
		connReg:    opts.ConnReg,
		logStore:   opts.LogStore,
		sshManager:    opts.SSHManager,
		resolver:      opts.Resolver,
		factsResolver: opts.FactsResolver,
	}
}

func (h *Hub) SetOptions(opts HubOptions) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if opts.Store != nil {
		h.store = opts.Store
	}
	if opts.QueueStore != nil {
		h.queueStore = opts.QueueStore
	}
	if opts.ConnReg != nil {
		h.connReg = opts.ConnReg
	}
	if opts.LogStore != nil {
		h.logStore = opts.LogStore
	}
	if opts.SSHManager != nil {
		h.sshManager = opts.SSHManager
	}
	if opts.Resolver != nil {
		h.resolver = opts.Resolver
	}
	if opts.FactsResolver != nil {
		h.factsResolver = opts.FactsResolver
	}
}

// GetMetrics returns a snapshot of current connection metrics
func (h *Hub) GetMetrics() map[string]any {
	return h.metrics.GetSnapshot()
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			h.RecordClusterLog(clusterapi.LogEntry{
				Kind:    clusterapi.KindApp,
				Level:   "info",
				Source:  "atc/coordinator",
				Message: fmt.Sprintf("websocket client connected (%s)", client.conn.RemoteAddr().String()),
			})
			h.sendInitialState(client)

		case client := <-h.unregister:
				h.mu.Lock()
				if _, ok := h.clients[client]; ok {
					delete(h.clients, client)
					client.mu.Lock()
					if !client.closed {
						client.closed = true
						close(client.send)
					}
					client.mu.Unlock()
				}
				h.mu.Unlock()
				h.metrics.ActiveConnections.Add(-1)
				h.metrics.LastDisconnectTime.Store(time.Now().Unix())
				log.Printf("websocket disconnected: active=%d", h.metrics.ActiveConnections.Load())
				h.RecordClusterLog(clusterapi.LogEntry{
					Kind:    clusterapi.KindApp,
					Level:   "info",
					Source:  "atc/coordinator",
					Message: fmt.Sprintf("websocket client disconnected (%s)", client.conn.RemoteAddr().String()),
				})
				client.closeAllPTYSessions()

		case message := <-h.broadcast:
			h.mu.Lock()
			for client := range h.clients {
				client.mu.Lock()
				if !client.closed {
					select {
					case client.send <- message:
					default:
						client.closed = true
						close(client.send)
						delete(h.clients, client)
					}
				}
				client.mu.Unlock()
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) BroadcastJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err == nil {
		select {
		case h.broadcast <- data:
		default:
			log.Println("websocket hub broadcast channel full, dropping message")
		}
	}
}

// boolPtr returns a pointer to b, for LogEntry.OK fields.
func boolPtr(b bool) *bool { return &b }

// RecordClusterLog persists an entry to the cluster log store and broadcasts it
// to connected clients so the Cluster Syslog tab stays live.
func (h *Hub) RecordClusterLog(entry clusterapi.LogEntry) {
	if h.logStore == nil {
		return
	}
	e := h.logStore.Record(entry)
	h.BroadcastJSON(map[string]any{
		"type":    "cluster_log_batch",
		"entries": []clusterapi.LogEntry{e},
	})
}

// BroadcastConnectionUpdate debounces and broadcasts connection snapshot.
func (h *Hub) BroadcastConnectionUpdate() {
	h.connTimerMu.Lock()
	defer h.connTimerMu.Unlock()

	if h.connReg == nil {
		return
	}

	if h.connBroadcastTimer != nil {
		return
	}

	h.connBroadcastTimer = time.AfterFunc(250*time.Millisecond, func() {
		h.connTimerMu.Lock()
		h.connBroadcastTimer = nil
		h.connTimerMu.Unlock()

		snap := h.connReg.Snapshot()
		h.BroadcastJSON(map[string]any{
			"type":        "connections_updated",
			"connections": snap,
		})
	})
}

func (h *Hub) sendInitialState(c *Client) {
	// 1. Initial State (tasks, connections) + full init payload matching server.ts lines 2613-2615
	var tasks []hostcache.Task
	if h.store != nil {
		if t, err := h.store.GetAllTasks(100); err == nil {
			tasks = t
		}
	}

	var queue any
	if h.queueStore != nil {
		queue = h.queueStore.Snapshot()
	}

	var nodes any = nodeapi.BaselineNodeObjects()
	if h.store != nil {
		if hosts, err := h.store.GetAllHosts(); err == nil && len(hosts) > 0 {
			var parsedNodes []any
			for _, host := range hosts {
				var obj map[string]any
				if err := json.Unmarshal(host.NodeJSON, &obj); err == nil {
					parsedNodes = append(parsedNodes, obj)
				}
			}
			if len(parsedNodes) > 0 {
				nodes = parsedNodes
			}
		}
	}

	var connSnap any
	if h.connReg != nil {
		connSnap = h.connReg.Snapshot()
	}

	initPayload := map[string]any{
		"type":        "init",
		"tasks":       tasks,
		"nodes":       nodes,
		"queue":       queue,
		"connections": connSnap,
	}
	data, err := json.Marshal(initPayload)
	if err == nil {
		select {
		case c.send <- data:
		default:
		}
	}

	// 2. Also send "initial_state" for requirements compatibility
	initialStatePayload := map[string]any{
		"type":        "initial_state",
		"tasks":       tasks,
		"connections": connSnap,
	}
	if b, err := json.Marshal(initialStatePayload); err == nil {
		select {
		case c.send <- b:
		default:
		}
	}

	// 3. Cluster logs batch
	if h.logStore != nil {
		entries := h.logStore.List(200, "", "", 0)
		logBatch := map[string]any{
			"type":    "cluster_log_batch",
			"entries": entries,
		}
		if b, err := json.Marshal(logBatch); err == nil {
			select {
			case c.send <- b:
			default:
			}
		}
	}

	// 4. Connections updated
	if connSnap != nil {
		connsPayload := map[string]any{
			"type":        "connections_updated",
			"connections": connSnap,
		}
		if b, err := json.Marshal(connsPayload); err == nil {
			select {
			case c.send <- b:
			default:
			}
		}
	}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.metrics.FailedUpgrades.Add(1)
		h.metrics.ConnectionErrors.Add(1)
		log.Printf("websocket upgrade error: %v (remote=%s, origin=%s)", err, r.RemoteAddr, r.Header.Get("Origin"))
		return
	}

	h.metrics.TotalConnections.Add(1)
	h.metrics.ActiveConnections.Add(1)
	h.metrics.LastConnectionTime.Store(time.Now().Unix())
	log.Printf("websocket connected: remote=%s origin=%s active=%d", r.RemoteAddr, r.Header.Get("Origin"), h.metrics.ActiveConnections.Load())

	var releaseConn func()
	if h.connReg != nil {
		releaseConn = h.connReg.Begin("websocket", "ws /ws")
		h.BroadcastConnectionUpdate()
	}

	client := &Client{
		hub:         h,
		conn:        conn,
		send:        make(chan []byte, 256),
		ptySessions: make(map[string]terminal.PTYSession),
	}
	h.register <- client

	go func() {
		defer func() {
			if releaseConn != nil {
				releaseConn()
				h.BroadcastConnectionUpdate()
			}
		}()
		client.writePump()
	}()

	go client.readPump()
}

// ClientWSMessage unmarshals incoming JSON frames from WebSocket clients.
type ClientWSMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	NodeID    string `json:"nodeId"`
	Host      string `json:"host"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Data      string `json:"data"`
	Command   string `json:"command"`
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if err != io.EOF && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.hub.metrics.ConnectionErrors.Add(1)
				log.Printf("websocket read error: %v", err)
			}
			break
		}
		c.hub.metrics.BytesReceived.Add(int64(len(message)))
		c.handleMessage(message)
	}
}

func (c *Client) handleMessage(data []byte) {
	var msg ClientWSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	switch msg.Type {
	case "terminal_start", "terminal_init":
		c.handleTerminalStart(msg)

	case "terminal_data":
		c.handleTerminalData(msg)

	case "terminal_input":
		c.handleTerminalInput(msg)

	case "terminal_resize":
		c.handleTerminalResize(msg)

	case "terminal_close":
		c.handleTerminalClose(msg)
	}
}

func (c *Client) handleTerminalStart(msg ClientWSMessage) {
	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("pty-%d", time.Now().UnixMilli())
	}

	targetQuery := msg.Host
	if targetQuery == "" {
		targetQuery = msg.NodeID
	}

	log.Printf("pty spawn requested: session=%s target=%s cols=%d rows=%d", sessionID, targetQuery, msg.Cols, msg.Rows)

	// Explicitly handle localhost/127.0.0.1/::1 in query to use local PTY
	isLocalHost := isLocalQuery(targetQuery)

	var targetNode *nodeapi.Object
	if c.hub.resolver != nil && !isLocalHost {
		targetNode, _ = c.hub.resolver.FindNode(targetQuery)
		// If resolved node represents localhost (by IP, shortName, or tailscaleHost), treat as local
		if targetNode != nil {
			if isLocalNode(targetNode) {
				isLocalHost = true
				shortName, _ := targetNode.GetString("shortName")
			ip, _ := targetNode.GetString("ip")
			log.Printf("pty target resolved to local node: session=%s target=%s shortName=%s ip=%s", sessionID, targetQuery, shortName, ip)
			}
		}
	} else if c.hub.resolver != nil && isLocalHost {
		// Still try to resolve for metadata even if query is explicitly local
		targetNode, _ = c.hub.resolver.FindNode(targetQuery)
	}

	cols := msg.Cols
	if cols <= 0 {
		cols = 80
	}
	rows := msg.Rows
	if rows <= 0 {
		rows = 24
	}

	var pool *sshclient.ClientPool
	if c.hub.sshManager != nil {
		pool = c.hub.sshManager.Pool()
	}

	var ptySession terminal.PTYSession
	var err error

	if isLocalHost {
		log.Printf("pty opening local session: session=%s cols=%d rows=%d", sessionID, cols, rows)
		ptySession, err = terminal.OpenLocalPTY(cols, rows)
	} else if targetNode != nil && c.hub.factsResolver != nil {
		// Interactive PTYs must ride the same transport as command runners:
		// tailnet nodes go over ATC's own tsnet node via the connection plugin.
		// The legacy ssh pool dials bare TCP, which cannot reach tailnet-only
		// addresses (100.x) and produced the "dial tcp 100.x: i/o timeout"
		// terminal failures.
		nodeID, _ := targetNode.GetString("id")
		log.Printf("pty opening SSH session via connection plugin: session=%s target=%s node=%s", sessionID, targetQuery, nodeID)
		facts, found, factsErr := c.hub.factsResolver.ResolveNode(nodeID)
		switch {
		case factsErr != nil:
			err = fmt.Errorf("pty: failed to resolve facts for node %s: %w", nodeID, factsErr)
		case !found:
			err = fmt.Errorf("pty: node %s not found in inventory", nodeID)
		default:
			client, clientErr := plugin.ResolveSSHClient(facts)
			if clientErr != nil {
				err = fmt.Errorf("pty: %w", clientErr)
			} else {
				// The client is pooled by the connection plugin: only the
				// session is ours to close, never the client.
				ptySession, err = terminal.OpenSSHPTYWithClient(client, cols, rows)
				if err != nil {
					// The pooled client may be stale (e.g. after a tsnet
					// re-registration): evict and retry once with a fresh dial.
					plugin.EvictSSHClient(facts)
					if c2, e2 := plugin.ResolveSSHClient(facts); e2 == nil {
						ptySession, err = terminal.OpenSSHPTYWithClient(c2, cols, rows)
					} else {
						err = fmt.Errorf("pty: %w", e2)
					}
				}
			}
		}
	} else if pool != nil && targetNode != nil {
		log.Printf("pty opening SSH session: session=%s target=%s", sessionID, targetQuery)
		ptySession, err = terminal.OpenSSHPTY(context.Background(), pool, targetNode, cols, rows)
	} else {
		// Zero-mock invariant: never substitute a local shell for a remote
		// target that failed to resolve — that would silently show the
		// operator a shell on the wrong machine.
		err = fmt.Errorf("pty: target %q did not resolve to an inventory node; refusing to substitute a local shell", targetQuery)
	}

	if err != nil {
		c.hub.metrics.FailedPTYSpawns.Add(1)
		c.hub.metrics.ConnectionErrors.Add(1)
		log.Printf("pty spawn error: session=%s target=%s local=%v err=%v", sessionID, targetQuery, isLocalHost, err)
		c.hub.RecordClusterLog(clusterapi.LogEntry{
			Kind:    clusterapi.KindApp,
			Level:   "error",
			Source:  "atc/coordinator",
			Host:    targetQuery,
			Message: fmt.Sprintf("PTY spawn failed for %s: %v", targetQuery, err),
			OK:      boolPtr(false),
		})
		c.sendJSON(map[string]any{
			"type":      "terminal_data",
			"sessionId": sessionID,
			"data":      fmt.Sprintf("\r\n[pty spawn error: %v]\r\n", err),
		})
		c.sendJSON(map[string]any{
			"type":      "terminal_output",
			"nodeId":    msg.NodeID,
			"sessionId": sessionID,
			"data":      fmt.Sprintf("\r\n[pty spawn error: %v]\r\n", err),
		})
		c.sendJSON(map[string]any{
			"type":      "terminal_exit",
			"sessionId": sessionID,
			"code":      1,
		})
		return
	}

	c.hub.metrics.TotalPTYSessions.Add(1)
	c.hub.metrics.ActivePTYSessions.Add(1)
	log.Printf("pty session started: session=%s target=%s local=%v active_pty=%d", sessionID, targetQuery, isLocalHost, c.hub.metrics.ActivePTYSessions.Load())
	c.hub.RecordClusterLog(clusterapi.LogEntry{
		Kind:    clusterapi.KindApp,
		Level:   "info",
		Source:  "atc/coordinator",
		Host:    targetQuery,
		Message: fmt.Sprintf("PTY session started for %s", targetQuery),
		OK:      boolPtr(true),
	})

	c.mu.Lock()
	// Close any previous session under this ID
	if prev, ok := c.ptySessions[sessionID]; ok {
		_ = prev.Close()
	}
	c.ptySessions[sessionID] = ptySession
	c.mu.Unlock()

	var releaseSSHConn func()
	if c.hub.connReg != nil {
		targetName := targetQuery
		if targetNode != nil {
			if name, ok := targetNode.GetString("shortName"); ok && name != "" {
				targetName = name
			}
		}
		releaseSSHConn = c.hub.connReg.Begin("ssh", fmt.Sprintf("pty %s", targetName))
		c.hub.BroadcastConnectionUpdate()
	}

	// Stream stdout from PTY to WebSocket client
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.ptySessions, sessionID)
			c.mu.Unlock()
			_ = ptySession.Close()
			c.hub.metrics.ActivePTYSessions.Add(-1)
			log.Printf("pty session closed: session=%s active_pty=%d", sessionID, c.hub.metrics.ActivePTYSessions.Load())

			if releaseSSHConn != nil {
				releaseSSHConn()
				c.hub.BroadcastConnectionUpdate()
			}

			c.sendJSON(map[string]any{
				"type":      "terminal_exit",
				"sessionId": sessionID,
				"code":      0,
			})
		}()

		buf := make([]byte, 4096)
		for {
			n, rErr := ptySession.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				// Send terminal_data (matching ghostty-web TerminalWindow.tsx)
				c.sendJSON(map[string]any{
					"type":      "terminal_data",
					"sessionId": sessionID,
					"data":      chunk,
				})
			}
			if rErr != nil {
				if rErr != io.EOF {
					log.Printf("pty read error: session=%s err=%v", sessionID, rErr)
				}
				break
			}
		}
	}()
}

func (c *Client) handleTerminalData(msg ClientWSMessage) {
	c.mu.Lock()
	session := c.ptySessions[msg.SessionID]
	c.mu.Unlock()

	if session != nil {
		_, _ = session.Write([]byte(msg.Data))
	}
}

func (c *Client) handleTerminalInput(msg ClientWSMessage) {
	// If message targets an active PTY session, write bytes to stdin
	c.mu.Lock()
	session := c.ptySessions[msg.SessionID]
	if session == nil && len(c.ptySessions) == 1 {
		// Single active session fallback
		for _, s := range c.ptySessions {
			session = s
			break
		}
	}
	c.mu.Unlock()

	if session != nil && msg.Data != "" {
		_, _ = session.Write([]byte(msg.Data))
		return
	}

	// Otherwise, handle legacy non-interactive command execution matching server.ts lines 2706-2714
	if msg.Command != "" {
		targetQuery := msg.Host
		if targetQuery == "" {
			targetQuery = msg.NodeID
		}
		isLocalHost := isLocalQuery(targetQuery)

		var targetNode *nodeapi.Object
		if c.hub.resolver != nil && !isLocalHost {
			targetNode, _ = c.hub.resolver.FindNode(targetQuery)
			// If resolved node represents localhost (by IP, shortName, or tailscaleHost), treat as local
			if targetNode != nil {
				if isLocalNode(targetNode) {
					isLocalHost = true
				}
			}
		}
		out, exitCode := terminal.ParseTerminalCommand(msg.Command, targetNode)
		_ = exitCode
		c.sendJSON(map[string]any{
			"type": "terminal_output",
			"data": out,
		})
	}
}

func (c *Client) handleTerminalResize(msg ClientWSMessage) {
	c.mu.Lock()
	session := c.ptySessions[msg.SessionID]
	if session == nil && len(c.ptySessions) == 1 {
		for _, s := range c.ptySessions {
			session = s
			break
		}
	}
	c.mu.Unlock()

	if session != nil && msg.Cols > 0 && msg.Rows > 0 {
		_ = session.Resize(msg.Cols, msg.Rows)
	}
}

func (c *Client) handleTerminalClose(msg ClientWSMessage) {
	c.mu.Lock()
	session, ok := c.ptySessions[msg.SessionID]
	if ok {
		delete(c.ptySessions, msg.SessionID)
	}
	c.mu.Unlock()

	if session != nil {
		_ = session.Close()
	}
}

func (c *Client) closeAllPTYSessions() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, s := range c.ptySessions {
		_ = s.Close()
		delete(c.ptySessions, id)
	}
}

func (c *Client) sendJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

func (c *Client) writePump() {
	defer func() {
		c.conn.Close()
	}()

	for message := range c.send {
		w, err := c.conn.NextWriter(websocket.TextMessage)
		if err != nil {
			c.hub.metrics.ConnectionErrors.Add(1)
			log.Printf("websocket write error (next writer): %v", err)
			return
		}
		n, err := w.Write(message)
		if err != nil {
			c.hub.metrics.ConnectionErrors.Add(1)
			log.Printf("websocket write error: %v", err)
			return
		}
		c.hub.metrics.BytesSent.Add(int64(n))
		if err := w.Close(); err != nil {
			c.hub.metrics.ConnectionErrors.Add(1)
			log.Printf("websocket write close error: %v", err)
			return
		}
	}
}
