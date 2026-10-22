package glances

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

// SettingsPageID is the route slug of the datacenter-wide Glances telemetry settings page.
const SettingsPageID = "telemetry/glances"

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Reader                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements 4-tier telemetry collection (Glances REST API -> loopback -> CLI -> proc/sysfs)
// plus declarative Glances daemon lifecycle management.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          SettingsPageID,
		Kind:        plugin.KindTelemetry,
		Name:        "glances",
		Label:       "Glances Telemetry & Metrics",
		Icon:        "monitoring",
		Description: "Multi-tier real-time host metrics: CPU, memory, load averages, sensors, and thermal stats",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"glances", "python3-psutil"},
			"apt": {"glances", "python3-psutil"},
			"apk": {"glances", "py3-psutil"},
		},
		ControllerCollections: []string{"ansible.builtin"},
	}
}

// CollectionSchedule returns the standard telemetry polling and cache TTL schedule (15s, realtime).
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "cpu",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide Glances telemetry defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(meta.ID, "Glances Telemetry Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide Glances REST daemon port, API version, polling cadence, and /proc fallback settings.").
		IntField(
			"apiPort",
			"Glances REST API Port",
			61208,
			settingsframework.WithMinMax(1, 65535),
			settingsframework.WithDescription("Default TCP port on which the Glances web/REST server listens."),
		).
		SelectField(
			"apiVersion",
			"Preferred Glances API Version",
			"4",
			[]plugin.SettingsFieldOption{
				{Value: "4", Label: "API v4 (Glances 4.x+)"},
				{Value: "3", Label: "API v3 (Glances 3.x)"},
			},
			settingsframework.WithDescription("Preferred Glances REST API version before falling back."),
		).
		IntField(
			"pollIntervalSeconds",
			"Telemetry Poll Interval (Seconds)",
			45,
			settingsframework.WithMinMax(5, 300),
			settingsframework.WithDescription("Conservative telemetry polling interval in seconds (AGENTS.md §7 recommends 30-60s)."),
		).
		BoolField(
			"enableProcFallback",
			"Enable /proc & sysfs Fallback",
			true,
			settingsframework.WithDescription("Allow Tier 4 /proc/loadavg and /proc/meminfo fallback when Glances is unavailable."),
		).
		SecretField(
			"apiPasswordVaultVar",
			"Glances API Password Vault Variable",
			settingsframework.WithPlaceholder("vault_glances_api_password"),
			settingsframework.WithDescription("Optional Ansible Vault variable name holding the Glances HTTP basic-auth password."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// ContextMenuEntries provides telemetry context menu actions for compatible nodes.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(meta.Kind)).
		AddAction("restart_daemon", "Restart Glances Daemon", "restart_alt", "restart_daemon", 10).
		AddAction("enable_daemon", "Enable Glances Daemon", "play_circle", "enable_daemon", 20).
		AddDangerAction("stop_daemon", "Stop Glances Daemon", "stop_circle", "stop_daemon",
			"Stop the Glances telemetry daemon on this node?", 90).
		Build()
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	// Tier 1 / 2 check: curl glances daemon port 61208
	out, err := r.RunCommand(ctx, "curl -s -m 2 http://127.0.0.1:61208/api/4/status 2>/dev/null || curl -s -m 2 http://127.0.0.1:61208/api/3/status 2>/dev/null || true")
	if err == nil && strings.TrimSpace(out) != "" {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "Glances REST daemon active (port 61208)",
		}, nil
	}

	// Tier 3 check: glances CLI
	cliOut, err := r.RunCommand(ctx, "command -v glances")
	if err == nil && strings.TrimSpace(cliOut) != "" {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "Glances CLI available",
		}, nil
	}

	// Tier 4 check: /proc/stat fallback
	procOut, err := r.RunCommand(ctx, "test -r /proc/stat && echo 'PROC_OK' || true")
	if err == nil && strings.Contains(procOut, "PROC_OK") {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "Proc / sysfs fallback telemetry active",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "UNAVAILABLE",
		Error:    "neither glances nor readable /proc/stat found",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	// Tier 1/2: Query local Glances REST API
	restCmd := "curl -s -m 2 http://127.0.0.1:61208/api/4/all 2>/dev/null || curl -s -m 2 http://127.0.0.1:61208/api/3/all 2>/dev/null || true"
	restOut, err := r.RunCommand(ctx, restCmd)
	if err == nil && strings.TrimSpace(restOut) != "" && strings.HasPrefix(strings.TrimSpace(restOut), "{") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(restOut), &payload); err == nil {
			return payload, nil
		}
	}

	// Tier 3: Execute glances CLI with stdout JSON
	cliCmd := "glances --stdout-json cpu,mem,load,sensors 2>/dev/null || true"
	cliOut, err := r.RunCommand(ctx, cliCmd)
	if err == nil && strings.TrimSpace(cliOut) != "" && strings.HasPrefix(strings.TrimSpace(cliOut), "{") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(cliOut), &payload); err == nil {
			return payload, nil
		}
	}

	// Tier 4: /proc/stat and /proc/meminfo fallback
	procCmd := "cat /proc/loadavg /proc/meminfo 2>/dev/null || true"
	procOut, err := r.RunCommand(ctx, procCmd)
	if err != nil {
		return nil, err
	}

	var load1, load5, load15 float64
	var memTotalKb, memAvailKb float64

	lines := strings.Split(procOut, "\n")
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) >= 3 && load1 == 0 && strings.Contains(fields[0], ".") {
			load1, _ = strconv.ParseFloat(fields[0], 64)
			load5, _ = strconv.ParseFloat(fields[1], 64)
			load15, _ = strconv.ParseFloat(fields[2], 64)
		} else if strings.HasPrefix(l, "MemTotal:") && len(fields) >= 2 {
			memTotalKb, _ = strconv.ParseFloat(fields[1], 64)
		} else if strings.HasPrefix(l, "MemAvailable:") && len(fields) >= 2 {
			memAvailKb, _ = strconv.ParseFloat(fields[1], 64)
		}
	}

	memUsedKb := memTotalKb - memAvailKb
	memPercent := 0.0
	if memTotalKb > 0 {
		memPercent = (memUsedKb / memTotalKb) * 100.0
	}

	return map[string]any{
		"load": map[string]any{
			"min1":  load1,
			"min5":  load5,
			"min15": load15,
		},
		"mem": map[string]any{
			"total":   memTotalKb * 1024,
			"used":    memUsedKb * 1024,
			"free":    memAvailKb * 1024,
			"percent": memPercent,
		},
		"fallback": true,
	}, nil
}

// ManagementActions returns rich v2 action specifications for Glances daemon management.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	serviceOpt := plugin.OptionSchema{
		Name:        "service",
		Type:        "string",
		Required:    false,
		Default:     "glances.service",
		Description: "Systemd service unit name for Glances",
	}

	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "restart_daemon",
				Label:       "Restart Glances Daemon",
				Description: "Restart the Glances telemetry systemd service on the host",
				Options:     []plugin.OptionSchema{serviceOpt},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindTelemetry},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "enable_daemon",
				Label:       "Enable Glances Daemon",
				Description: "Enable and start the Glances telemetry systemd service on the host",
				Options:     []plugin.OptionSchema{serviceOpt},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindTelemetry},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "stop_daemon",
				Label:       "Stop Glances Daemon",
				Description: "Stop the Glances telemetry systemd service on the host",
				Options:     []plugin.OptionSchema{serviceOpt},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Stop the Glances telemetry daemon on this node?",
			InvalidatesKinds: []plugin.Kind{plugin.KindTelemetry},
		},
	}
}

// Actions derives legacy plugin.ActionSpec items from ManagementActions().
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options against the declared ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.TrimSpace(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			if err := newapi.ValidateManagementOptions(spec, opts); err != nil {
				return err
			}
			if rawSvc, ok := opts["service"].(string); ok && strings.TrimSpace(rawSvc) != "" {
				if err := validateServiceName(strings.TrimSpace(rawSvc)); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return fmt.Errorf("unsupported glances action: %s", action)
}

// CompileTask adapts a typed v2 TaskRequest to the plugin's declarative Ansible TaskSpec compiler.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, req.Params)
}

// Compile produces a declarative ansible.builtin.systemd_service TaskSpec for Glances daemon lifecycle actions.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if err := p.ValidateAction(h, action, opts); err != nil {
		return plugin.TaskSpec{}, err
	}

	svc := "glances.service"
	if opts != nil {
		if raw, ok := opts["service"].(string); ok && strings.TrimSpace(raw) != "" {
			svc = strings.TrimSpace(raw)
		}
	}

	norm := strings.ToLower(strings.TrimSpace(action))
	params := map[string]any{
		"name":   svc,
		"become": true,
	}
	var label string

	switch norm {
	case "restart_daemon":
		params["state"] = "restarted"
		label = fmt.Sprintf("Restart %s", svc)
	case "enable_daemon":
		params["state"] = "started"
		params["enabled"] = true
		label = fmt.Sprintf("Enable and start %s", svc)
	case "stop_daemon":
		params["state"] = "stopped"
		label = fmt.Sprintf("Stop %s", svc)
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported glances action: %s", action)
	}

	return plugin.TaskSpec{
		Name: label,
		Steps: []plugin.TaskStep{
			{
				Name:   label,
				Module: "ansible.builtin.systemd_service",
				Params: params,
			},
		},
	}, nil
}

func validateServiceName(name string) error {
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("service name must not start with '-'")
	}
	if strings.ContainsAny(name, " \t\r\n;|&$`\"'\\/") {
		return fmt.Errorf("service name contains disallowed characters")
	}
	return nil
}
