package nodeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
)

// findNode resolves a target node matching query from persistence.
// Parity: matches server.ts findNode(query) fallback behavior.
func (a *API) findNode(query string) (*Object, error) {
	return a.FindNode(query)
}

// FindNode resolves a target node matching query from persistence.
// Parity: matches server.ts findNode(query) fallback behavior.
func (a *API) FindNode(query string) (*Object, error) {
	hosts, err := a.store.GetAllHosts()
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return nil, errors.New("no nodes available in inventory")
	}

	fallback, err := ParseObject(hosts[0].NodeJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to parse fallback node json: %w", err)
	}

	q := strings.TrimSpace(query)
	if q == "" {
		return fallback, nil
	}
	qLower := strings.ToLower(q)

	for _, h := range hosts {
		obj, err := ParseObject(h.NodeJSON)
		if err != nil {
			continue
		}
		id, _ := obj.GetString("id")
		name, _ := obj.GetString("name")
		shortName, _ := obj.GetString("shortName")
		tsHost, _ := obj.GetString("tailscaleHost")
		tsIP, _ := obj.GetString("tailscaleIp")
		ip, _ := obj.GetString("ip")

		if strings.ToLower(id) == qLower ||
			strings.ToLower(name) == qLower ||
			strings.ToLower(shortName) == qLower ||
			strings.ToLower(tsHost) == qLower ||
			tsIP == q ||
			ip == q {
			return obj, nil
		}
	}

	return fallback, nil
}

// FindNodeInSlice resolves a target node matching query from an in-memory slice
// of node objects (e.g. the baseline inventory used when no cache is available).
// It returns nil when nothing matches so callers can decide on their own
// fallback, unlike FindNode which always returns the first persisted host.
func FindNodeInSlice(nodes []*Object, query string) *Object {
	if len(nodes) == 0 {
		return nil
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nodes[0]
	}
	qLower := strings.ToLower(q)
	for _, obj := range nodes {
		if obj == nil {
			continue
		}
		id, _ := obj.GetString("id")
		name, _ := obj.GetString("name")
		shortName, _ := obj.GetString("shortName")
		tsHost, _ := obj.GetString("tailscaleHost")
		tsIP, _ := obj.GetString("tailscaleIp")
		ip, _ := obj.GetString("ip")

		if strings.ToLower(id) == qLower ||
			strings.ToLower(name) == qLower ||
			strings.ToLower(shortName) == qLower ||
			strings.ToLower(tsHost) == qLower ||
			tsIP == q ||
			ip == q {
			return obj
		}
	}
	return nil
}

func hostFactsFromNode(node *Object) plugin.HostFacts {
	id, _ := node.GetString("id")
	nodeType, _ := node.GetString("nodeType")
	osFamily := "linux"
	if strings.Contains(strings.ToLower(nodeType), "win") {
		osFamily = "windows"
	}
	tailscaleHost, _ := node.GetString("tailscaleHost")
	tailscaleIp, _ := node.GetString("tailscaleIp")
	ip, _ := node.GetString("ip")

	hostRoute := tailscaleIp
	if hostRoute == "" {
		hostRoute = ip
	}
	if hostRoute == "" {
		hostRoute = tailscaleHost
	}
	if hostRoute == "" {
		hostRoute = "localhost"
	}

	privMethod, _ := node.GetString("privilegeMethod")
	activeElev := "elevation/run0"
	if privMethod == "sudo" || privMethod == "doas" {
		activeElev = "elevation/" + privMethod
	}

	return plugin.HostFacts{
		NodeID:           id,
		OSFamily:         osFamily,
		HostRoute:        hostRoute,
		ActiveConnection: "connection/ssh",
		ActiveElevation:  activeElev,
	}
}

func ansNodeFromObject(node *Object) ansqueue.Node {
	id, _ := node.GetString("id")
	name, _ := node.GetString("name")
	shortName, _ := node.GetString("shortName")
	ip, _ := node.GetString("ip")
	tailscaleIP, _ := node.GetString("tailscaleIp")
	tailscaleHost, _ := node.GetString("tailscaleHost")
	sshUser, _ := node.GetString("sshUser")
	privMethod, _ := node.GetString("privilegeMethod")
	port := 22
	if rawPort, ok := node.Get("port"); ok {
		if p := parsePositiveInt(rawPort); p > 0 {
			port = p
		}
	}
	return ansqueue.Node{
		ID:              id,
		Name:            name,
		ShortName:       shortName,
		IP:              ip,
		Port:            port,
		SSHUser:         sshUser,
		TailscaleIP:     tailscaleIP,
		TailscaleHost:   tailscaleHost,
		PrivilegeMethod: privMethod,
	}
}

func (a *API) stagePluginTask(
	node *Object,
	category string,
	pluginID string,
	action string,
	opts map[string]any,
	fallbackTitle string,
	fallbackModule string,
	fallbackSteps []ansqueue.Step,
	become bool,
	secretsMasked map[string]string,
) {
	_, _ = a.stagePluginTaskID(node, category, pluginID, action, opts, fallbackTitle, fallbackModule, fallbackSteps, become, secretsMasked)
}

// stagePluginTaskID behaves like stagePluginTask but reports the queued task id
// so the caller can execute exactly that task.
func (a *API) stagePluginTaskID(
	node *Object,
	category string,
	pluginID string,
	action string,
	opts map[string]any,
	fallbackTitle string,
	fallbackModule string,
	fallbackSteps []ansqueue.Step,
	become bool,
	secretsMasked map[string]string,
) (string, error) {
	if a.queueStore == nil {
		return "", errors.New("ansible queue is not configured")
	}

	ansNode := ansNodeFromObject(node)
	facts := hostFactsFromNode(node)

	title := fallbackTitle
	primaryModule := fallbackModule
	steps := fallbackSteps

	if pluginID != "" {
		if p, ok := plugin.Get(pluginID); ok {
			if actor, ok := p.(plugin.Actor); ok {
				if spec, err := actor.Compile(facts, action, opts); err == nil {
					if spec.Name != "" {
						title = spec.Name
					}
					if len(spec.Steps) > 0 {
						primaryModule = spec.Steps[0].Module
						steps = make([]ansqueue.Step, len(spec.Steps))
						for i, s := range spec.Steps {
							params := make(map[string]any, len(s.Params))
							for k, v := range s.Params {
								if k == "become" || k == "become_user" {
									continue
								}
								params[k] = v
							}
							steps[i] = ansqueue.Step{
								Name:       s.Name,
								Module:     s.Module,
								Parameters: params,
							}
						}
					}
					if len(spec.SecretsMasked) > 0 {
						if secretsMasked == nil {
							secretsMasked = make(map[string]string)
						}
						for k, v := range spec.SecretsMasked {
							secretsMasked[k] = v
						}
					}
				}
			}
		}
	}

	sshUser := ansNode.SSHUser
	if sshUser == "" {
		sshUser = "core"
	}
	sshPort := ansNode.Port
	if sshPort == 0 {
		sshPort = 22
	}

	becomeMethod := ansNode.PrivilegeMethod
	if becomeMethod != "sudo" && becomeMethod != "doas" {
		becomeMethod = "run0"
	}

	nodeName := ansNode.ShortName
	if nodeName == "" {
		nodeName = ansNode.Name
	}
	if nodeName == "" {
		nodeName = ansNode.ID
	}

	task := ansqueue.Task{
		Title:           title,
		TargetNodeID:    ansNode.ID,
		TargetNodeName:  nodeName,
		TargetHostRoute: facts.HostRoute,
		TargetSSHUser:   sshUser,
		TargetSSHPort:   sshPort,
		Category:        category,
		PrimaryModule:   primaryModule,
		Steps:           steps,
		SecretsMasked:   secretsMasked,
		Become:          &become,
		BecomeMethod:    becomeMethod,
		Status:          ansqueue.StatusStaged,
		CreatedAt:       a.now().UnixMilli(),
		Connection: &ansqueue.Connection{
			Plugin: facts.ActiveConnection,
			Host:   facts.HostRoute,
			Port:   sshPort,
			User:   sshUser,
		},
	}

	enqueued, err := a.queueStore.Enqueue(task)
	a.emitQueueUpdated()
	if err != nil {
		return "", err
	}
	return enqueued.ID, nil
}

func (a *API) runCommand(ctx context.Context, targetNode *Object, cmd string, timeout time.Duration) (string, error) {
	if a.runner == nil {
		return "", errors.New("command runner is nil")
	}
	return a.runner.RunNodeCommand(ctx, targetNode, cmd, timeout)
}

// ---------------------------------------------------------------------------
// 1. POST /api/nodes/:id/interfaces/:name/toggle
// ---------------------------------------------------------------------------

func (a *API) handleInterfaceToggle(w http.ResponseWriter, r *http.Request, nodeID, iface string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var body struct {
		State string `json:"state"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	state := "up"
	if strings.ToLower(strings.TrimSpace(body.State)) == "down" {
		state = "down"
	}

	a.stagePluginTask(
		targetNode,
		"network",
		"network/interfaces",
		"toggle_interface",
		map[string]any{"name": iface, "interface": iface, "state": state},
		fmt.Sprintf("Set interface %s %s", iface, state),
		"ansible.builtin.command",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("Set interface %s %s", iface, state),
				Module:     "ansible.builtin.command",
				Parameters: map[string]any{"cmd": fmt.Sprintf("ip link set %s %s", iface, state)},
			},
		},
		true,
		nil,
	)

	cmd := fmt.Sprintf("sudo ip link set %s %s", iface, state)
	_, runErr := a.runCommand(r.Context(), targetNode, cmd, 6*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		errMsg := runErr.Error()
		if errMsg == "" {
			errMsg = fmt.Sprintf("Failed to set interface %s %s", iface, state)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": errMsg})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "interface": iface, "state": state})
}

// ---------------------------------------------------------------------------
// 2. POST /api/nodes/:id/interfaces/connect
// ---------------------------------------------------------------------------

func (a *API) handleInterfaceConnect(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var body ConnectNetworkInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	cmd, err := BuildConnectNetworkCommand(body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	opts := map[string]any{
		"name": body.Name,
		"type": body.Type,
	}
	if body.Cidr != "" {
		opts["cidr"] = body.Cidr
	}
	if body.Gateway != "" {
		opts["gateway"] = body.Gateway
	}
	if len(body.DNS) > 0 {
		opts["dns"] = strings.Join(body.DNS, " ")
	}

	a.stagePluginTask(
		targetNode,
		"network",
		"network/interfaces",
		"connect_interface",
		opts,
		fmt.Sprintf("Configure network interface %s", body.Name),
		"ansible.builtin.template",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("Configure network interface %s (%s)", body.Name, body.Type),
				Module:     "ansible.builtin.template",
				Parameters: opts,
			},
		},
		true,
		nil,
	)

	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 10*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "output": strings.TrimSpace(out)})
}

// firewallManagedByPve reports whether Proxmox's pve-firewall owns the host
// ruleset. ATC must not write into that ruleset: pve-firewall rewrites its
// chains on every reload, so mutations would appear to work and then vanish.
func (a *API) firewallManagedByPve(ctx context.Context, node *Object) bool {
	out, err := a.runCommand(ctx, node, `if [ -d /etc/pve ] && systemctl is-active --quiet pve-firewall 2>/dev/null; then echo PVE; fi`, 5*time.Second)
	return err == nil && strings.Contains(out, "PVE")
}

// rejectPveManagedFirewall writes the standard 409 for view-only hosts.
func rejectPveManagedFirewall(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"success":  false,
		"viewOnly": true,
		"error":    "Firewall is managed by pve-firewall; ATC mutations are not implemented yet",
	})
}

// ---------------------------------------------------------------------------
// 3. POST /api/nodes/:id/firewall/rules
// ---------------------------------------------------------------------------

func (a *API) handleFirewallAddRule(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if a.firewallManagedByPve(r.Context(), targetNode) {
		rejectPveManagedFirewall(w)
		return
	}

	var body FirewallRuleInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	flavour := normalizeFirewallFlavour(body.Plugin)
	action := "allow_port"
	opts := map[string]any{
		"port":  body.Port,
		"proto": body.Protocol,
		"zone":  body.Zone,
		"state": "enabled",
	}
	if body.Toport != "" {
		action = "forward_port"
		opts["toport"] = body.Toport
		opts["toaddr"] = body.Toaddr
	} else if body.Service != "" {
		action = "allow_service"
		opts["service"] = body.Service
	}

	// Every firewall mutation runs as an Ansible task. firewalld uses its native
	// module (permanent + immediate); the raw flavours are persisted through an
	// ATC-owned drop-in that the same play writes and activates.
	steps := []ansqueue.Step{}
	pluginID := "firewall/" + flavour
	if flavour == "firewalld" {
		steps = nil // compiled by the plugin Actor
	} else {
		targetID, _ := targetNode.GetString("id")
		rule := hostcache.FirewallRule{
			ID:        firewallRuleID(targetID, flavour, action, body),
			HostID:    targetID,
			Plugin:    flavour,
			Kind:      action,
			Params:    firewallRuleParams(action, body),
			Permanent: true,
		}
		if err := a.store.UpsertFirewallRule(rule); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		rules, err := a.store.ListFirewallRules(targetID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		steps = firewallPersistSteps(flavour, rules)
	}

	title := fmt.Sprintf("Configure %s firewall rule", flavour)
	out, runErr := a.applyFirewallTask(r.Context(), targetNode, title, pluginID, action, opts, steps)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}
	if runErr != nil {
		// The task is durably queued even when the play cannot reach the host.
		// Report the honest split instead of pretending it applied.
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false, "staged": true, "applied": false,
			"error": runErr.Error(), "output": strings.TrimSpace(out),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "staged": true, "applied": true, "output": strings.TrimSpace(out)})
}

// applyFirewallTask stages a firewall mutation, runs exactly that task through
// the Ansible queue, and returns its output. No direct SSH mutation remains.
func (a *API) applyFirewallTask(ctx context.Context, node *Object, title, pluginID, action string, opts map[string]any, steps []ansqueue.Step) (string, error) {
	id, err := a.stagePluginTaskID(node, "firewall", pluginID, action, opts, title, "ansible.builtin.command", steps, true, nil)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("firewall task could not be staged")
	}

	result, err := a.queueStore.ExecuteNextTask(ctx, ansqueue.Options{
		WorkDir:   ansqueue.ResolveWorkDir(),
		TaskID:    id,
		TaskStore: a.store,
	})
	if err != nil {
		return "", err
	}
	if result.Result != nil {
		if result.Result.Error != "" {
			return "", errors.New(result.Result.Error)
		}
		return fmt.Sprintf("task %s applied via ansible", result.Result.TaskID), nil
	}
	return "", nil
}

// firewallRuleID is a deterministic id, so re-applying the same rule updates it
// instead of stacking duplicates in the model.
func firewallRuleID(hostID, flavour, action string, body FirewallRuleInput) string {
	parts := []string{hostID, flavour, action, body.Port, body.Protocol, body.Toport, body.Toaddr, body.Service}
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		clean = append(clean, strings.NewReplacer("/", "-", ":", "-", " ", "").Replace(strings.TrimSpace(p)))
	}
	return "fw-" + strings.Join(clean, "-")
}

// firewallRuleParams maps the rule input onto the model parameters.
func firewallRuleParams(action string, body FirewallRuleInput) map[string]string {
	params := map[string]string{
		"port":  strings.TrimSpace(body.Port),
		"proto": strings.TrimSpace(body.Protocol),
	}
	if params["proto"] == "" {
		params["proto"] = "tcp"
	}
	if action == "forward_port" {
		params["toport"] = strings.TrimSpace(body.Toport)
		params["toaddr"] = strings.TrimSpace(body.Toaddr)
	}
	if action == "allow_service" {
		params["service"] = strings.TrimSpace(body.Service)
	}
	return params
}

// ---------------------------------------------------------------------------
// 4. DELETE /api/nodes/:id/firewall/rules/:ruleId
// ---------------------------------------------------------------------------

func (a *API) handleFirewallDeleteRule(w http.ResponseWriter, r *http.Request, nodeID, ruleID string) {
	if r.Method != http.MethodDelete {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if a.firewallManagedByPve(r.Context(), targetNode) {
		rejectPveManagedFirewall(w)
		return
	}

	zone := r.URL.Query().Get("zone")
	rawRule := r.URL.Query().Get("rawRule")

	var body struct {
		Zone    string `json:"zone"`
		RawRule string `json:"rawRule"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if zone == "" {
		zone = body.Zone
	}
	if zone == "" {
		zone = "public"
	}
	if rawRule == "" {
		rawRule = body.RawRule
	}

	// Keep the managed model in sync: a deleted rule must not be resurrected by
	// the next rendered drop-in. `ruleID` is "<port>/<proto>" for raw flavours.
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		if rules, listErr := a.store.ListFirewallRules(targetID); listErr == nil {
			port := strings.SplitN(ruleID, "/", 2)[0]
			for _, rule := range rules {
				if rule.Params["port"] == port {
					_ = a.store.DeleteFirewallRule(targetID, rule.ID)
				}
			}
		}
	}

	// Raw flavours are model-backed: re-render the ATC-owned drop-in instead of
	// poking the host directly, so persistence and runtime stay in sync.
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		if managed, listErr := a.store.ListFirewallRules(targetID); listErr == nil && len(managed) > 0 {
			flavour := managed[0].Plugin
			steps := firewallPersistSteps(flavour, managed)
			if len(steps) > 0 {
				out, runErr := a.applyFirewallTask(r.Context(), targetNode, "Remove "+flavour+" NAT rule", "firewall/"+flavour, "delete_rule", map[string]any{"rule": ruleID}, steps)
				_ = a.store.InvalidateHostSections(targetID)
				if runErr != nil {
					writeJSON(w, http.StatusOK, map[string]any{"success": false, "staged": true, "applied": false, "error": runErr.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"success": true, "staged": true, "applied": true, "output": strings.TrimSpace(out)})
				return
			}
		}
	}

	cmd, err := BuildFirewallDeleteCommand(FirewallDeleteInput{
		Zone:    zone,
		RuleID:  ruleID,
		RawRule: rawRule,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	a.stagePluginTask(
		targetNode,
		"firewall",
		"firewall/firewalld",
		"allow_port",
		map[string]any{
			"port":  ruleID,
			"zone":  zone,
			"state": "disabled",
		},
		fmt.Sprintf("Remove firewall rule %s", ruleID),
		"ansible.posix.firewalld",
		[]ansqueue.Step{
			{
				Name:   fmt.Sprintf("Remove firewall rule %s", ruleID),
				Module: "ansible.posix.firewalld",
				Parameters: map[string]any{
					"port":  ruleID,
					"zone":  zone,
					"state": "disabled",
				},
			},
		},
		true,
		nil,
	)

	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 8*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "output": strings.TrimSpace(out)})
}

// ---------------------------------------------------------------------------
// 5. POST /api/nodes/:id/disks/:name/smart-test
// ---------------------------------------------------------------------------

func (a *API) handleDiskSmartTest(w http.ResponseWriter, r *http.Request, nodeID, disk string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	cleanDisk := strings.TrimPrefix(disk, "/dev/")

	a.stagePluginTask(
		targetNode,
		"storage",
		"disk/smart",
		"smart_test",
		map[string]any{"disk": cleanDisk},
		fmt.Sprintf("SMART self-test on /dev/%s", cleanDisk),
		"ansible.builtin.command",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("SMART test /dev/%s", cleanDisk),
				Module:     "ansible.builtin.command",
				Parameters: map[string]any{"cmd": fmt.Sprintf("smartctl -t short /dev/%s", cleanDisk)},
			},
		},
		true,
		nil,
	)

	cmd := fmt.Sprintf("sudo smartctl -t short /dev/%s", cleanDisk)
	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 6*time.Second)
	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "disk": cleanDisk, "output": strings.TrimSpace(out)})
}

// ---------------------------------------------------------------------------
// 6. POST /api/nodes/:id/zfs/:pool/scrub
// ---------------------------------------------------------------------------

func (a *API) handleZfsScrub(w http.ResponseWriter, r *http.Request, nodeID, pool string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	a.stagePluginTask(
		targetNode,
		"storage",
		"storage/zfs",
		"scrub",
		map[string]any{"pool": pool, "state": "scrubbing"},
		fmt.Sprintf("Scrub ZFS pool %s", pool),
		"community.general.zpool",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("Scrub ZFS pool %s", pool),
				Module:     "community.general.zpool",
				Parameters: map[string]any{"name": pool, "state": "scrubbing"},
			},
		},
		true,
		nil,
	)

	cmd := fmt.Sprintf("sudo zpool scrub %s", pool)
	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 6*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "pool": pool, "output": strings.TrimSpace(out)})
}

// ---------------------------------------------------------------------------
// 7. POST /api/nodes/:id/vms/:name/action
// ---------------------------------------------------------------------------

func (a *API) handleVmAction(w http.ResponseWriter, r *http.Request, nodeID, vmName string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	cleanVmName := strings.TrimSpace(vmName)

	var body struct {
		Action string `json:"action"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	rawAction := strings.ToLower(strings.TrimSpace(body.Action))
	if rawAction == "" {
		rawAction = "start"
	}

	allowedActions := map[string]bool{
		"start": true, "shutdown": true, "destroy": true, "reboot": true,
		"reset": true, "resume": true, "suspend": true,
	}
	if !allowedActions[rawAction] {
		allowedList := "start, shutdown, destroy, reboot, reset, resume, suspend"
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Invalid VM action: %s. Allowed: %s", rawAction, allowedList),
		})
		return
	}

	if !regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`).MatchString(cleanVmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid VM name format"})
		return
	}

	a.stagePluginTask(
		targetNode,
		"vm",
		"vm/libvirt",
		rawAction,
		map[string]any{"name": cleanVmName},
		fmt.Sprintf("%s virtual machine %s", strings.Title(rawAction), cleanVmName),
		"community.general.virt",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("%s VM %s", strings.Title(rawAction), cleanVmName),
				Module:     "community.general.virt",
				Parameters: map[string]any{"name": cleanVmName, "state": rawAction},
			},
		},
		true,
		nil,
	)

	cmd := fmt.Sprintf("sudo virsh %s %s", rawAction, cleanVmName)
	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 8*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"vm":      cleanVmName,
		"action":  rawAction,
		"output":  strings.TrimSpace(out),
	})
}

// ---------------------------------------------------------------------------
// 8. POST /api/nodes/:id/vms/create
// ---------------------------------------------------------------------------

func (a *API) handleVmCreate(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var body struct {
		Name       string          `json:"name"`
		OsTemplate string          `json:"osTemplate"`
		Vcpus      json.RawMessage `json:"vcpus"`
		RamGiB     json.RawMessage `json:"ramGiB"`
		DiskGb     json.RawMessage `json:"diskGb"`
		Bridge     string          `json:"bridge"`
		Autostart  bool            `json:"autostart"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "VM name is required"})
		return
	}

	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "VM name is required"})
		return
	}

	osTemplate := "Ubuntu 24.04"
	if strings.TrimSpace(body.OsTemplate) != "" {
		osTemplate = strings.TrimSpace(body.OsTemplate)
	}
	bridge := "virbr0"
	if strings.TrimSpace(body.Bridge) != "" {
		bridge = strings.TrimSpace(body.Bridge)
	}
	vcpus := 2
	if v := parsePositiveInt(body.Vcpus); v > 0 {
		vcpus = v
	}
	ramGiB := 4
	if r := parsePositiveInt(body.RamGiB); r > 0 {
		ramGiB = r
	}
	diskGb := 20
	if d := parsePositiveInt(body.DiskGb); d > 0 {
		diskGb = d
	}

	cleanName := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(strings.TrimSpace(body.Name), "-")
	cleanBridge := regexp.MustCompile(`[^a-zA-Z0-9_.-]`).ReplaceAllString(bridge, "")
	ramMb := ramGiB * 1024
	numCpus := vcpus
	diskSize := diskGb

	cmd1 := fmt.Sprintf("virt-install --name %q --vcpus %d --memory %d --disk size=%d --network bridge=%s --noautoconsole --import --check path_in_use=off 2>&1 || virsh define /dev/stdin << 'EOF'\n<domain type='kvm'><name>%s</name><memory unit='MiB'>%d</memory><vcpu>%d</vcpu><os><type arch='x86_64'>hvm</type></os></domain>\nEOF 2>&1", cleanName, numCpus, ramMb, diskSize, cleanBridge, cleanName, ramMb, numCpus)
	cmd2 := "true"
	if body.Autostart {
		cmd2 = fmt.Sprintf("virsh autostart %q 2>&1 || true", cleanName)
	}
	cmd := cmd1 + " && " + cmd2

	a.stagePluginTask(
		targetNode,
		"vm",
		"vm/libvirt",
		"create",
		map[string]any{
			"name":      cleanName,
			"vcpus":     numCpus,
			"ramGiB":    ramGiB,
			"diskGb":    diskSize,
			"bridge":    cleanBridge,
			"autostart": body.Autostart,
		},
		fmt.Sprintf("Create virtual machine %s", cleanName),
		"ansible.builtin.command",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("Create VM %s", cleanName),
				Module:     "ansible.builtin.command",
				Parameters: map[string]any{"cmd": cmd},
			},
		},
		true,
		nil,
	)

	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 10*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		nodeName, _ := targetNode.GetString("name")
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Failed to create VM %s on %s: %s", cleanName, nodeName, runErr.Error()),
		})
		return
	}

	vmid := 200 + int(a.now().UnixMilli()%700)
	vmType := "linux"
	if strings.Contains(strings.ToLower(osTemplate), "windows") {
		vmType = "windows"
	}
	newVm := map[string]any{
		"vmid":    vmid,
		"name":    cleanName,
		"subtext": fmt.Sprintf("%s (%s)", osTemplate, bridge),
		"status":  "STOPPED",
		"vcpus":   numCpus,
		"ramGiB":  ramGiB,
		"type":    vmType,
	}

	nodeTarget, _ := targetNode.GetString("tailscaleHost")
	if nodeTarget == "" {
		nodeTarget, _ = targetNode.GetString("name")
	}
	sshUser, _ := targetNode.GetString("sshUser")
	if sshUser == "" {
		sshUser = "core"
	}

	vmTask := hostcache.Task{
		ID:          fmt.Sprintf("task-%d", a.now().UnixMilli()),
		StartTime:   a.now().UTC().Format("2006-01-02 15:04:05"),
		NodeTarget:  nodeTarget,
		User:        fmt.Sprintf("%s@ssh-agent", sshUser),
		Description: fmt.Sprintf("virt-install create VM: %s (%d vCPUs, %d GiB, %d GB)", cleanName, numCpus, ramGiB, diskSize),
		Progress:    100,
		ProgressEta: "VM created via KVM/QEMU",
		Status:      "OK",
		Category:    "active",
		Logs:        json.RawMessage(fmt.Sprintf("[%q]", strings.TrimSpace(out))),
	}
	_ = a.store.SaveTask(vmTask)
	a.emit(map[string]any{"type": "task_started", "task": vmTask})

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"vm":      newVm,
		"output":  strings.TrimSpace(out),
	})
}

// ---------------------------------------------------------------------------
// 9. POST /api/nodes/:id/shares/reload
// ---------------------------------------------------------------------------

func (a *API) handleSharesReload(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	a.stagePluginTask(
		targetNode,
		"shares",
		"share/smb",
		"reload",
		map[string]any{},
		"Reload network shares configuration",
		"ansible.builtin.command",
		[]ansqueue.Step{
			{
				Name:       "Reload network shares",
				Module:     "ansible.builtin.command",
				Parameters: map[string]any{"cmd": "smbcontrol all reload-config || exportfs -ra"},
			},
		},
		true,
		nil,
	)

	cmd := "sudo smbcontrol all reload-config 2>&1 || sudo exportfs -ra 2>&1"
	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 6*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "output": strings.TrimSpace(out)})
}

// ---------------------------------------------------------------------------
// 10. POST /api/nodes/:id/shares/nfs
// ---------------------------------------------------------------------------

func (a *API) handleSharesNfs(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var body struct {
		Path    string `json:"path"`
		Client  string `json:"client"`
		Options string `json:"options"`
		Name    string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Share directory path is required"})
		return
	}

	if strings.TrimSpace(body.Path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Share directory path is required"})
		return
	}

	saveCmd, err := BuildNfsSaveCommand(NfsShareConfig{
		Path:    body.Path,
		Client:  body.Client,
		Options: body.Options,
	}, body.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	a.stagePluginTask(
		targetNode,
		"shares",
		"share/nfs",
		"export",
		map[string]any{
			"path":    body.Path,
			"client":  body.Client,
			"options": body.Options,
		},
		fmt.Sprintf("Configure NFS export for %s", body.Path),
		"ansible.builtin.lineinfile",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("Configure NFS export for %s", body.Path),
				Module:     "ansible.builtin.lineinfile",
				Parameters: map[string]any{"path": body.Path},
			},
		},
		true,
		nil,
	)

	out, runErr := a.runCommand(r.Context(), targetNode, saveCmd, 10*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "NFS export configured successfully",
		"output":  strings.TrimSpace(out),
	})
}

// ---------------------------------------------------------------------------
// 11. POST /api/nodes/:id/shares/smb
// ---------------------------------------------------------------------------

func (a *API) handleSharesSmb(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var body struct {
		Name       string `json:"name"`
		Path       string `json:"path"`
		Comment    string `json:"comment"`
		ReadOnly   bool   `json:"readOnly"`
		ValidUsers string `json:"validUsers"`
		GuestOk    bool   `json:"guestOk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Share name and path are required"})
		return
	}

	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Share name and path are required"})
		return
	}

	saveCmd, err := BuildSmbSaveCommand(SmbShareConfig{
		Name:       body.Name,
		Path:       body.Path,
		Comment:    body.Comment,
		ReadOnly:   body.ReadOnly,
		ValidUsers: body.ValidUsers,
		GuestOk:    body.GuestOk,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	a.stagePluginTask(
		targetNode,
		"shares",
		"share/smb",
		"share",
		map[string]any{
			"name":      body.Name,
			"path":      body.Path,
			"comment":   body.Comment,
			"read_only": body.ReadOnly,
			"guest_ok":  body.GuestOk,
			"users":     body.ValidUsers,
		},
		fmt.Sprintf("Configure share %s", body.Name),
		"ansible.builtin.template",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("Configure share %s", body.Name),
				Module:     "ansible.builtin.template",
				Parameters: map[string]any{"name": body.Name, "path": body.Path},
			},
		},
		true,
		nil,
	)

	out, runErr := a.runCommand(r.Context(), targetNode, saveCmd, 12*time.Second)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}

	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "SMB share configured successfully",
		"output":  strings.TrimSpace(out),
	})
}

// ---------------------------------------------------------------------------
// 12. POST /api/nodes/:id/users/create
// ---------------------------------------------------------------------------

func (a *API) handleUsersCreate(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Username is required"})
		return
	}

	if strings.TrimSpace(body.Username) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Username is required"})
		return
	}

	cleanUser, err := SanitizeUsername(body.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	cmd, err := BuildUserCreateCommand(body.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	a.stagePluginTask(
		targetNode,
		"system",
		"",
		"",
		map[string]any{},
		fmt.Sprintf("Create user %s", cleanUser),
		"ansible.builtin.user",
		[]ansqueue.Step{
			{
				Name:   fmt.Sprintf("Create user %s", cleanUser),
				Module: "ansible.builtin.user",
				Parameters: map[string]any{
					"name":        cleanUser,
					"shell":       "/bin/bash",
					"create_home": true,
					"state":       "present",
				},
			},
		},
		true,
		nil,
	)

	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 8*time.Second)
	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": runErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"username": cleanUser,
		"output":   strings.TrimSpace(out),
	})
}

// ---------------------------------------------------------------------------
// 13. POST /api/nodes/:id/users/:user/password
// ---------------------------------------------------------------------------

func (a *API) handleUsersPassword(w http.ResponseWriter, r *http.Request, nodeID, user string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	cleanUser, err := SanitizeUsername(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var body struct {
		Password string `json:"password"`
		VaultKey string `json:"vaultKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Password is required"})
		return
	}

	if body.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Password is required"})
		return
	}

	cmd, err := BuildUserPasswdCommand(cleanUser, body.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	vaultVar := body.VaultKey
	if vaultVar == "" {
		vaultVar = "user_password_" + cleanUser
	}
	maskedSecret := ansqueue.MaskedSecretValue(vaultVar)

	a.stagePluginTask(
		targetNode,
		"system",
		"",
		"",
		map[string]any{},
		fmt.Sprintf("Update password for user %s", cleanUser),
		"ansible.builtin.user",
		[]ansqueue.Step{
			{
				Name:   fmt.Sprintf("Update password for user %s", cleanUser),
				Module: "ansible.builtin.user",
				Parameters: map[string]any{
					"name":     cleanUser,
					"password": maskedSecret,
				},
			},
		},
		true,
		map[string]string{"password": maskedSecret},
	)

	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 8*time.Second)
	if runErr != nil {
		redacted := ansqueue.RedactSecrets(strings.ReplaceAll(runErr.Error(), body.Password, "••••••••"))
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": redacted})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": fmt.Sprintf("Password updated for %s", user),
		"output":  strings.TrimSpace(out),
	})
}

// ---------------------------------------------------------------------------
// 14. POST /api/nodes/:id/users/:user/smbpassword
// ---------------------------------------------------------------------------

func (a *API) handleUsersSmbPassword(w http.ResponseWriter, r *http.Request, nodeID, user string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	cleanUser, err := SanitizeUsername(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var body struct {
		Password string `json:"password"`
		VaultKey string `json:"vaultKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Samba password is required"})
		return
	}

	if body.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Samba password is required"})
		return
	}

	cmd, err := BuildSmbPasswdCommand(cleanUser, body.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	vaultVar := body.VaultKey
	if vaultVar == "" {
		vaultVar = "smb_password_" + cleanUser
	}
	maskedSecret := ansqueue.MaskedSecretValue(vaultVar)

	a.stagePluginTask(
		targetNode,
		"shares",
		"",
		"",
		map[string]any{},
		fmt.Sprintf("Update Samba password for user %s", cleanUser),
		"ansible.builtin.command",
		[]ansqueue.Step{
			{
				Name:   fmt.Sprintf("Update Samba password for user %s", cleanUser),
				Module: "ansible.builtin.command",
				Parameters: map[string]any{
					"user":         cleanUser,
					"smb_password": maskedSecret,
				},
			},
		},
		true,
		map[string]string{"smb_password": maskedSecret},
	)

	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 8*time.Second)
	if runErr != nil {
		redacted := ansqueue.RedactSecrets(strings.ReplaceAll(runErr.Error(), body.Password, "••••••••"))
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": redacted})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": fmt.Sprintf("Samba password updated for %s", user),
		"output":  strings.TrimSpace(out),
	})
}

// ---------------------------------------------------------------------------
// 15. POST /api/nodes/:id/reboot
// ---------------------------------------------------------------------------

func (a *API) handleReboot(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	nodeName, _ := targetNode.GetString("name")
	tsIP, _ := targetNode.GetString("tailscaleIp")
	if tsIP == "" {
		tsIP, _ = targetNode.GetString("ip")
	}
	sshUser, _ := targetNode.GetString("sshUser")
	if sshUser == "" {
		sshUser = "core"
	}

	a.stagePluginTask(
		targetNode,
		"system",
		"system/service",
		"reboot",
		map[string]any{"timeout": 300, "msg": "Reboot initiated by Cockpit"},
		fmt.Sprintf("Reboot node %s", nodeName),
		"ansible.builtin.reboot",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("Reboot host (%s)", nodeName),
				Module:     "ansible.builtin.reboot",
				Parameters: map[string]any{"msg": "Reboot initiated by Cockpit", "reboot_timeout": 300},
			},
		},
		true,
		nil,
	)

	rebootTask := hostcache.Task{
		ID:          fmt.Sprintf("task-reboot-%d", a.now().UnixMilli()),
		StartTime:   a.now().UTC().Format("2006-01-02 15:04:05"),
		NodeTarget:  nodeName,
		User:        fmt.Sprintf("%s@ssh-agent", sshUser),
		Description: fmt.Sprintf("systemctl reboot on %s (%s)", nodeName, tsIP),
		Progress:    50,
		ProgressEta: "Sending reboot signal...",
		Status:      "RUNNING",
		Category:    "active",
		Logs:        json.RawMessage(fmt.Sprintf("[%q]", fmt.Sprintf("[reboot] Initiating graceful reboot sequence on %s", nodeName))),
	}

	_ = a.store.SaveTask(rebootTask)
	a.emit(map[string]any{"type": "task_started", "task": rebootTask})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, runErr := a.runCommand(ctx, targetNode, "sleep 1 && sudo systemctl reboot &", 3*time.Second)

		taskUpdate := rebootTask
		taskUpdate.Progress = 100
		if runErr == nil {
			taskUpdate.Status = "OK"
			taskUpdate.ProgressEta = "Reboot signal delivered"
			taskUpdate.Logs = json.RawMessage(fmt.Sprintf("[%q,%q]",
				fmt.Sprintf("[reboot] Initiating graceful reboot sequence on %s", nodeName),
				"[reboot] Reboot signal delivered",
			))
		} else {
			errMsg := strings.TrimSpace(out)
			if errMsg == "" {
				errMsg = runErr.Error()
			}
			taskUpdate.Status = "FAILED"
			taskUpdate.ProgressEta = fmt.Sprintf("Failed: %s", errMsg)
			taskUpdate.Logs = json.RawMessage(fmt.Sprintf("[%q,%q]",
				fmt.Sprintf("[reboot] Initiating graceful reboot sequence on %s", nodeName),
				fmt.Sprintf("[error] Reboot failed: %s", errMsg),
			))
		}
		_ = a.store.SaveTask(taskUpdate)
		a.emit(map[string]any{"type": "task_updated", "task": taskUpdate})
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": fmt.Sprintf("Reboot command sent to %s", nodeName),
		"task":    rebootTask,
	})
}

// ---------------------------------------------------------------------------
// 16. POST /api/podman/action
// ---------------------------------------------------------------------------

func (a *API) HandlePodmanAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}

	var body struct {
		Host      string `json:"host"`
		Container string `json:"container"`
		Action    string `json:"action"`
		User      string `json:"user"`
		UID       *int   `json:"uid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   "Container action failed",
		})
		return
	}

	targetNode, err := a.findNode(body.Host)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	a.stagePluginTask(
		targetNode,
		"container",
		"container/podman",
		body.Action,
		map[string]any{"name": body.Container, "user": body.User},
		fmt.Sprintf("%s container %s", strings.Title(body.Action), body.Container),
		"containers.podman.podman_container",
		[]ansqueue.Step{
			{
				Name:       fmt.Sprintf("%s container %s", strings.Title(body.Action), body.Container),
				Module:     "containers.podman.podman_container",
				Parameters: map[string]any{"name": body.Container, "state": body.Action},
			},
		},
		false,
		nil,
	)

	var cmd string
	sockAction := fmt.Sprintf("http://d/v4.0.0/libpod/containers/%s/%s", body.Container, body.Action)
	if body.User != "" && body.User != "root" {
		effectiveUID := 1000
		if body.UID != nil {
			effectiveUID = *body.UID
		}
		uSock := fmt.Sprintf("/run/user/%d/podman/podman.sock", effectiveUID)
		cmd = fmt.Sprintf(
			"run0 --background=no curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"run0 --background=no --user %s curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"sudo -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"doas -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"run0 --background=no --user %s podman %s %s 2>&1 || "+
				"sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman %s %s 2>&1 || "+
				"doas -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman %s %s 2>&1 || "+
				"podman %s %s 2>&1",
			uSock, sockAction,
			body.User, uSock, sockAction,
			uSock, sockAction,
			uSock, sockAction,
			uSock, sockAction,
			body.User, body.Action, body.Container,
			body.User, effectiveUID, body.Action, body.Container,
			body.User, effectiveUID, body.Action, body.Container,
			body.Action, body.Container,
		)
	} else {
		rootSock := "/run/podman/podman.sock"
		cmd = fmt.Sprintf(
			"run0 --background=no curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"sudo -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"doas -n curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"curl -sf -X POST --unix-socket %s %q 2>/dev/null || "+
				"run0 --background=no podman %s %s 2>&1 || "+
				"sudo -n podman %s %s 2>&1 || "+
				"doas -n podman %s %s 2>&1 || "+
				"sudo -n docker %s %s 2>&1 || "+
				"doas -n docker %s %s 2>&1 || "+
				"podman %s %s 2>&1",
			rootSock, sockAction,
			rootSock, sockAction,
			rootSock, sockAction,
			rootSock, sockAction,
			body.Action, body.Container,
			body.Action, body.Container,
			body.Action, body.Container,
			body.Action, body.Container,
			body.Action, body.Container,
			body.Action, body.Container,
		)
	}

	out, runErr := a.runCommand(r.Context(), targetNode, cmd, 8*time.Second)

	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.DeleteHostContainerCache(targetID)
		_ = a.store.DeleteHostTelemetry(targetID)
	}

	if runErr != nil {
		errMsg := runErr.Error()
		if errMsg == "" {
			errMsg = "Container action failed"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   errMsg,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"output":  out,
	})
}

// ---------------------------------------------------------------------------
// 17. POST /api/nodes/:id/identities
// ---------------------------------------------------------------------------

func (a *API) handleIdentities(w http.ResponseWriter, r *http.Request, nodeID string) {
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Node not found"})
		return
	}
	targetID, _ := targetNode.GetString("id")

	var req struct {
		User          string `json:"user"`
		UIDGid        string `json:"uidGid"`
		RolesGroups   string `json:"rolesGroups"`
		AuthMechanism string `json:"authMechanism"`
		LingerSubUID  string `json:"lingerSubUid"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	userVal := req.User
	if userVal == "" {
		userVal = "svc-app"
	}
	uidGidVal := req.UIDGid
	if uidGidVal == "" {
		uidGidVal = "2900:2900"
	}
	rolesVal := req.RolesGroups
	if rolesVal == "" {
		rolesVal = "podman, rootless-containers"
	}
	authVal := req.AuthMechanism
	if authVal == "" {
		authVal = "Rootless SubUID 655360:65536"
	}
	lingerVal := req.LingerSubUID
	if lingerVal == "" {
		lingerVal = "Linger: Enabled"
	}

	newIdentity := map[string]any{
		"user":          userVal,
		"uidGid":        uidGidVal,
		"rolesGroups":   rolesVal,
		"authMechanism": authVal,
		"lingerSubUid":  lingerVal,
		"icon":          "deployed_code",
	}

	idBytes, _ := json.Marshal(newIdentity)
	if a.store != nil {
		_ = a.store.AddCustomIdentity(targetID, idBytes)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"identity": newIdentity,
	})
}

// ---------------------------------------------------------------------------
// 18. Privilege Probe & Test
// ---------------------------------------------------------------------------

func (a *API) handlePrivilegeGet(w http.ResponseWriter, r *http.Request, nodeID string) {
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Node not found"})
		return
	}
	targetID, _ := targetNode.GetString("id")
	method, _ := targetNode.GetString("privilegeMethod")
	if method == "" {
		method = "run0"
	}
	sshUser, _ := targetNode.GetString("sshUser")
	if sshUser == "" {
		sshUser = "core"
	}

	cmd := "which run0 >/dev/null 2>&1 && run0 --background=no id -u 2>/dev/null || (sudo -n id -u 2>/dev/null) || echo 'NONE'"
	out, _ := a.runCommand(r.Context(), targetNode, cmd, 8*time.Second)

	active := false
	canElevate := false
	trimmed := strings.TrimSpace(out)
	if trimmed == "0" || strings.Contains(trimmed, "\n0") {
		active = true
		canElevate = true
	} else if trimmed != "NONE" && trimmed != "" {
		canElevate = true
	}

	resp := map[string]any{
		"method":     method,
		"active":     active,
		"user":       sshUser,
		"canElevate": canElevate,
		"details": map[string]any{
			"hasRun0":    true,
			"run0Active": method == "run0" && active,
			"hasSudo":    true,
			"sudoActive": method == "sudo" && active,
		},
	}
	if a.store != nil {
		b, _ := json.Marshal(resp)
		_ = a.store.UpsertHostSection(targetID, "privilege", b)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (a *API) handlePrivilegeTest(w http.ResponseWriter, r *http.Request, nodeID string) {
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Node not found"})
		return
	}

	var req struct {
		Method string `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	method := req.Method
	if method == "" {
		method, _ = targetNode.GetString("privilegeMethod")
		if method == "" {
			method = "run0"
		}
	}

	testCmd := "id -u"
	if method == "run0" {
		testCmd = "run0 --background=no id -u"
	} else if method == "sudo" {
		testCmd = "sudo -n id -u"
	} else if method == "doas" {
		testCmd = "doas -n id -u"
	}

	out, err := a.runCommand(r.Context(), targetNode, testCmd, 8*time.Second)
	success := err == nil && strings.TrimSpace(out) == "0"

	writeJSON(w, http.StatusOK, map[string]any{
		"success": success,
		"method":  method,
		"active":  success,
		"output":  strings.TrimSpace(out),
	})
}

// ---------------------------------------------------------------------------
// 19. Packages Check & Apply
// ---------------------------------------------------------------------------

// packageCheckTimeout bounds the whole package check so a hung registry, SSH
// session or container runtime cannot leave the UI spinning forever. Callers
// may override it with ?timeout=<seconds> (capped at 300s).
const packageCheckTimeout = 30 * time.Second

// pluginRunnerAdapter adapts the nodeapi CommandRunner to the plugin.Runner
// contract for a specific target node. The caller owns the deadline (timeout 0
// means "inherit the caller's context"), so no nested timeout is added.
type pluginRunnerAdapter struct {
	api  *API
	node *Object
}

func (r *pluginRunnerAdapter) RunCommand(ctx context.Context, cmd string) (string, error) {
	return r.api.runCommand(ctx, r.node, cmd, 0)
}

// writeOfflinePackageCheck returns a successful HTTP response carrying an
// explicit offline/unmonitored marker so the frontend can stop its spinner and
// show a host-unreachable state instead of waiting forever.
func (a *API) writeOfflinePackageCheck(w http.ResponseWriter, targetID, reason string) {
	result := map[string]any{
		"success":        false,
		"offline":        true,
		"unmonitored":    true,
		"manager":        "unknown",
		"isImmutable":    false,
		"updatesCount":   0,
		"updates":        []any{},
		"lastChecked":    time.Now().UTC().Format(time.RFC3339),
		"rebootRequired": false,
		"error":          reason,
	}
	if a.store != nil && targetID != "" {
		b, _ := json.Marshal(result)
		_ = a.store.UpsertHostSection(targetID, "packages", b)
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handlePackagesCheck(w http.ResponseWriter, r *http.Request, nodeID string) {
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Host not found"})
		return
	}
	targetID, _ := targetNode.GetString("id")

	timeout := packageCheckTimeout
	if raw := strings.TrimSpace(r.URL.Query().Get("timeout")); raw != "" {
		if secs, parseErr := strconv.Atoi(raw); parseErr == nil && secs > 0 && secs <= 300 {
			timeout = time.Duration(secs) * time.Second
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Fast probe package manager. The 8s inner bound keeps detection snappy;
	// the outer deadline bounds the whole request.
	out, probeErr := a.runCommand(ctx, targetNode, "which bootc 2>/dev/null || which rpm-ostree 2>/dev/null || which dnf 2>/dev/null || which apt-get 2>/dev/null || which apk 2>/dev/null || echo 'none'", 8*time.Second)
	if probeErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			a.writeOfflinePackageCheck(w, targetID, fmt.Sprintf("package manager probe timed out after %s", timeout))
			return
		}
		a.writeOfflinePackageCheck(w, targetID, fmt.Sprintf("package manager probe failed: %v", probeErr))
		return
	}
	trimmed := strings.TrimSpace(out)

	manager := "dnf"
	isImmutable := false
	if strings.Contains(trimmed, "bootc") {
		manager = "bootc"
		isImmutable = true
	} else if strings.Contains(trimmed, "rpm-ostree") {
		manager = "rpm-ostree"
		isImmutable = true
	} else if strings.Contains(trimmed, "apt-get") {
		manager = "apt"
	} else if strings.Contains(trimmed, "apk") {
		manager = "apk"
	}

	result := map[string]any{
		"success":        true,
		"manager":        manager,
		"isImmutable":    isImmutable,
		"updatesCount":   0,
		"updates":        []any{},
		"lastChecked":    time.Now().UTC().Format(time.RFC3339),
		"rebootRequired": false,
	}

	// Execute the real check through the registered plugin Reader. This restores
	// update checking that the Go port dropped, and gives every manager the same
	// 30s guard. A plugin read that exceeds the deadline is reported as
	// offline/unmonitored rather than an indefinite wait.
	pluginID := "package/" + manager
	if manager == "rpm-ostree" {
		pluginID = "package/rpmostree"
	}
	if p, ok := plugin.Get(pluginID); ok {
		if reader, ok := p.(plugin.Reader); ok {
			facts := plugin.HostFacts{
				NodeID:        targetID,
				OSFamily:      "linux",
				ActivePackage: pluginID,
			}
			data, readErr := reader.Read(ctx, &pluginRunnerAdapter{api: a, node: targetNode}, facts)
			if readErr != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(readErr, context.DeadlineExceeded) {
					a.writeOfflinePackageCheck(w, targetID, fmt.Sprintf("package check timed out after %s", timeout))
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"success":        false,
					"manager":        manager,
					"isImmutable":    isImmutable,
					"updates":        []any{},
					"updatesCount":   0,
					"lastChecked":    time.Now().UTC().Format(time.RFC3339),
					"rebootRequired": false,
					"error":          fmt.Sprintf("package check failed: %v", readErr),
				})
				return
			}
			if m, ok := data.(map[string]any); ok {
				// Pass every field the reader produced straight through. The
				// handler used to whitelist a handful of keys, which silently
				// dropped manager-specific data such as bootc's booted/rollback/
				// staged image references.
				for key, value := range m {
					if key == "success" {
						continue
					}
					result[key] = value
				}
				// rpm-ostree reports deployments instead of an updates array; surface
				// a staged deployment as a pending update when present.
				if manager == "rpm-ostree" {
					if pending, _ := m["pendingDeployment"].(bool); pending {
						if origin, _ := m["stagedOrigin"].(string); origin != "" {
							result["updates"] = []any{map[string]any{
								"name":       origin,
								"newVersion": m["stagedVersion"],
								"repository": "ostree",
								"security":   false,
							}}
							result["updatesCount"] = 1
							result["rebootRequired"] = true
						}
					}
				}
			}
		}
	}

	if a.store != nil {
		b, _ := json.Marshal(result)
		_ = a.store.UpsertHostSection(targetID, "packages", b)
	}

	writeJSON(w, http.StatusOK, result)
}

func (a *API) handlePackagesApply(w http.ResponseWriter, r *http.Request, nodeID string) {
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Host not found"})
		return
	}
	targetID, _ := targetNode.GetString("id")
	nodeName, _ := targetNode.GetString("name")
	sshUser, _ := targetNode.GetString("sshUser")
	if sshUser == "" {
		sshUser = "core"
	}

	var req struct {
		Manager  string   `json:"manager"`
		Packages []string `json:"packages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	mgr := strings.TrimSpace(req.Manager)
	if mgr == "" {
		out, _ := a.runCommand(r.Context(), targetNode, "which bootc 2>/dev/null || which rpm-ostree 2>/dev/null || which dnf 2>/dev/null || which apt-get 2>/dev/null || which apk 2>/dev/null || echo 'dnf'", 8*time.Second)
		trimmed := strings.TrimSpace(out)
		switch {
		case strings.Contains(trimmed, "bootc"):
			mgr = "bootc"
		case strings.Contains(trimmed, "rpm-ostree"):
			mgr = "rpm-ostree"
		case strings.Contains(trimmed, "apt-get"):
			mgr = "apt"
		case strings.Contains(trimmed, "apk"):
			mgr = "apk"
		default:
			mgr = "dnf"
		}
	}

	var applyCmd string
	switch mgr {
	case "bootc":
		applyCmd = "sudo bootc upgrade"
	case "rpm-ostree":
		applyCmd = "sudo rpm-ostree upgrade"
	case "apt":
		applyCmd = "sudo DEBIAN_FRONTEND=noninteractive apt-get upgrade -y"
	case "apk":
		applyCmd = "sudo apk upgrade"
	default:
		applyCmd = "sudo dnf upgrade -y"
	}

	taskID := fmt.Sprintf("task-pkg-%d", time.Now().UnixMilli())
	initLog := fmt.Sprintf("[packageManager] Starting updates via %s on %s: %s", mgr, nodeName, applyCmd)
	pkgTask := hostcache.Task{
		ID:          taskID,
		StartTime:   time.Now().Format("2006-01-02 15:04:05"),
		NodeTarget:  nodeName,
		User:        fmt.Sprintf("%s@ssh-agent", sshUser),
		Description: fmt.Sprintf("apply package updates on %s (%s)", nodeName, mgr),
		Progress:    20,
		ProgressEta: fmt.Sprintf("Executing %s...", applyCmd),
		Status:      "RUNNING",
		Category:    "active",
		Logs:        json.RawMessage(fmt.Sprintf("[%q]", initLog)),
	}

	if a.store != nil {
		_ = a.store.SaveTask(pkgTask)
		if targetID != "" {
			_ = a.store.InvalidateHostSections(targetID)
		}
	}
	a.emit(map[string]any{"type": "task_started", "task": pkgTask})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		out, runErr := a.runCommand(ctx, targetNode, applyCmd, 5*time.Minute)

		pkgTask.Progress = 100
		logLines := []string{initLog}
		if trimmedOut := strings.TrimSpace(out); trimmedOut != "" {
			for _, line := range strings.Split(trimmedOut, "\n") {
				if strings.TrimSpace(line) != "" {
					logLines = append(logLines, line)
				}
			}
		}

		if runErr != nil {
			errMsg := strings.TrimSpace(out)
			if errMsg == "" {
				errMsg = runErr.Error()
			}
			pkgTask.Status = "FAILED"
			pkgTask.ProgressEta = fmt.Sprintf("Failed: %s", errMsg)
			logLines = append(logLines, fmt.Sprintf("[error] Package upgrade failed: %s", errMsg))
		} else {
			pkgTask.Status = "OK"
			pkgTask.ProgressEta = "Updates complete"
			logLines = append(logLines, "[packageManager] Package upgrade finished successfully.")
		}
		if b, err := json.Marshal(logLines); err == nil {
			pkgTask.Logs = json.RawMessage(b)
		}
		if a.store != nil {
			_ = a.store.SaveTask(pkgTask)
		}
		a.emit(map[string]any{"type": "task_updated", "task": pkgTask})
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"taskId":  taskID,
		"task":    pkgTask,
		"message": fmt.Sprintf("Applying updates on %s via %s", nodeName, mgr),
	})
}

// POST /api/nodes/:id/firewall/masquerade
//
// firewalld uses the native ansible.posix.firewalld module (permanent +
// immediate). The raw flavours record the desired state and re-render the
// ATC-owned drop-in, which the same Ansible task writes and activates.
func (a *API) handleFirewallMasquerade(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if a.firewallManagedByPve(r.Context(), targetNode) {
		rejectPveManagedFirewall(w)
		return
	}

	var body struct {
		Enabled *bool  `json:"enabled"`
		Plugin  string `json:"plugin"`
		Zone    string `json:"zone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if body.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "enabled is required"})
		return
	}

	flavour := normalizeFirewallFlavour(body.Plugin)
	if flavour != "firewalld" && flavour != "nftables" && flavour != "iptables" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("masquerade is not supported for firewall plugin %q", flavour),
		})
		return
	}

	action := "masquerade"
	opts := map[string]any{"enabled": *body.Enabled, "plugin": flavour}
	if strings.TrimSpace(body.Zone) != "" {
		opts["zone"] = body.Zone
	}
	if strings.TrimSpace(body.Zone) != "" {
		opts["zone"] = body.Zone
	}

	steps := []ansqueue.Step{}
	pluginID := "firewall/" + flavour
	if flavour != "firewalld" {
		targetID, _ := targetNode.GetString("id")
		if *body.Enabled {
			if err := a.store.UpsertFirewallRule(hostcache.FirewallRule{
				ID:        "fw-" + targetID + "-" + flavour + "-masquerade",
				HostID:    targetID,
				Plugin:    flavour,
				Kind:      "masquerade",
				Params:    map[string]string{},
				Permanent: true,
			}); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}
		} else {
			existing, listErr := a.store.ListFirewallRules(targetID)
			if listErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": listErr.Error()})
				return
			}
			for _, rule := range existing {
				if rule.Kind == "masquerade" {
					_ = a.store.DeleteFirewallRule(targetID, rule.ID)
				}
			}
		}
		rules, listErr := a.store.ListFirewallRules(targetID)
		if listErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": listErr.Error()})
			return
		}
		steps = firewallPersistSteps(flavour, rules)
	}

	verb := "Enable"
	if !*body.Enabled {
		verb = "Disable"
	}
	title := fmt.Sprintf("%s masquerade for %s firewall", verb, flavour)

	out, runErr := a.applyFirewallTask(r.Context(), targetNode, title, pluginID, action, opts, steps)
	if targetID, ok := targetNode.GetString("id"); ok && targetID != "" {
		_ = a.store.InvalidateHostSections(targetID)
	}
	if runErr != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false, "staged": true, "applied": false, "enabled": *body.Enabled,
			"plugin": flavour, "error": runErr.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "staged": true, "applied": true, "enabled": *body.Enabled,
		"plugin": flavour, "output": strings.TrimSpace(out),
	})
}

// POST /api/nodes/:id/firewall/adopt
//
// Imports NAT state that already exists on the host (masquerade and DNAT rules
// created outside ATC) into the managed model, so the next render keeps them
// instead of silently dropping them.
func (a *API) handleFirewallAdopt(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}
	targetNode, err := a.findNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if a.firewallManagedByPve(r.Context(), targetNode) {
		rejectPveManagedFirewall(w)
		return
	}

	var body struct {
		Plugin string `json:"plugin"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	flavour := normalizeFirewallFlavour(body.Plugin)
	if flavour != "nftables" && flavour != "iptables" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "only nftables and iptables hosts have an importable NAT model",
		})
		return
	}

	p, ok := plugin.Get("firewall/" + flavour)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "firewall plugin not registered"})
		return
	}
	reader, ok := p.(plugin.Reader)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "firewall plugin cannot read host state"})
		return
	}

	targetID, _ := targetNode.GetString("id")
	data, readErr := reader.Read(r.Context(), &pluginRunnerAdapter{api: a, node: targetNode}, hostFactsFromNode(targetNode))
	if readErr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": readErr.Error()})
		return
	}
	state, _ := data.(map[string]any)

	adopted := 0
	if masq, ok := state["masquerade"].(bool); ok && masq {
		if err := a.store.UpsertFirewallRule(hostcache.FirewallRule{
			ID:        "fw-" + targetID + "-" + flavour + "-masquerade",
			HostID:    targetID,
			Plugin:    flavour,
			Kind:      "masquerade",
			Params:    map[string]string{},
			Permanent: true,
			Adopted:   true,
		}); err == nil {
			adopted++
		}
	}

	if raw, ok := state["forwardPorts"].([]any); ok {
		for _, entry := range raw {
			item, _ := entry.(map[string]any)
			port, _ := item["port"].(string)
			proto, _ := item["proto"].(string)
			toPort, _ := item["toport"].(string)
			toAddr, _ := item["toaddr"].(string)
			if strings.TrimSpace(port) == "" {
				continue
			}
			rule := hostcache.FirewallRule{
				ID:        "fw-" + targetID + "-" + flavour + "-forward-" + port + "-" + proto,
				HostID:    targetID,
				Plugin:    flavour,
				Kind:      "forward_port",
				Params:    map[string]string{"port": port, "proto": firstNonEmptyString(proto, "tcp"), "toport": toPort, "toaddr": toAddr},
				Permanent: true,
				Adopted:   true,
			}
			if err := a.store.UpsertFirewallRule(rule); err == nil {
				adopted++
			}
		}
	}

	rules, _ := a.store.ListFirewallRules(targetID)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"adopted": adopted,
		"managed": len(rules),
	})
}
