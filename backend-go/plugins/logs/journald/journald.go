package journald

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	"air-traffic-control/backend/pkg/systemd"
)

// PluginID and SettingsPageID identify the systemd journald log collector plugin.
const (
	PluginID       = "logs/journald"
	SettingsPageID = "logs/journald"
)

// Default settings values for journald log collection and maintenance.
const (
	DefaultLinesCount  = 100
	DefaultVacuumSize  = "500M"
	DefaultVacuumTime  = "30d"
	DefaultStorageMode = "persistent"
)

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

var (
	// reJournalUnit matches a unit filter with or without its type suffix.
	reJournalUnit = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.@-]*$`)
	// reJournalPriority matches a syslog priority name or numeric level.
	reJournalPriority = regexp.MustCompile(`^[0-7]$|^(emerg|emergency|alert|crit|critical|err|error|warn|warning|notice|info|debug)$`)
	// reJournalLines matches a plain positive line count.
	reJournalLines = regexp.MustCompile(`^[0-9]{1,5}$`)
	// reVacuumSize matches a journalctl --vacuum-size specification (e.g. 500M, 2G, 1024K).
	reVacuumSize = regexp.MustCompile(`^[1-9][0-9]*[BKMGTP]?$`)
	// reVacuumTime matches a journalctl --vacuum-time specification (e.g. 7d, 2weeks, 30d, 1month, 1y).
	reVacuumTime = regexp.MustCompile(`^[1-9][0-9]*(s|sec|m|min|h|hr|d|day|days|w|week|weeks|M|month|months|y|year|years)?$`)
)

// shellQuote single-quotes a value for the SSH command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Plugin implements systemd journald log retrieval, inspection, and maintenance.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindLogs,
		Name:        "journald",
		Label:       "System & Journal Logs",
		Icon:        "description",
		Description: "Systemd journald log collector with priority, unit, and service filtering",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"systemd"},
			"apt": {"systemd"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CollectionSchedule returns the standard polling cadence and cache TTL schedule for journald logs.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "lines",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide journald query and retention defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "System & Journal Logs Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide systemd-journald log tailing, priority filtering, storage persistence, and vacuum retention defaults.").
		IntField("defaultLines", "Default Log Line Count", DefaultLinesCount,
			settingsframework.WithMinMax(10, 10000),
			settingsframework.WithDescription("Default number of journal lines retrieved when no per-request line count is specified.")).
		SelectField("defaultPriority", "Default Minimum Priority Filter", "", []plugin.SettingsFieldOption{
			{Value: "", Label: "All Priorities (No Filter)"},
			{Value: "emerg", Label: "0 - Emergency (emerg)"},
			{Value: "alert", Label: "1 - Alert (alert)"},
			{Value: "crit", Label: "2 - Critical (crit)"},
			{Value: "err", Label: "3 - Error (err)"},
			{Value: "warning", Label: "4 - Warning (warning)"},
			{Value: "notice", Label: "5 - Notice (notice)"},
			{Value: "info", Label: "6 - Informational (info)"},
			{Value: "debug", Label: "7 - Debug (debug)"},
		}, settingsframework.WithDescription("Default syslog priority level filter applied to journal queries when not overridden.")).
		SelectField("defaultScope", "Default Systemd Manager Scope", "system", []plugin.SettingsFieldOption{
			{Value: "system", Label: "System Manager (system)"},
			{Value: "user", Label: "User Session Manager (user)"},
		}, settingsframework.WithDescription("Default systemd journal scope inspected when no explicit scope is provided.")).
		SelectField("storageMode", "Journal Storage Persistence", DefaultStorageMode, []plugin.SettingsFieldOption{
			{Value: "persistent", Label: "Persistent (/var/log/journal)"},
			{Value: "volatile", Label: "Volatile (/run/log/journal in RAM)"},
			{Value: "auto", Label: "Auto (Persistent if /var/log/journal exists)"},
		}, settingsframework.WithDescription("Preferred Storage= setting in /etc/systemd/journald.conf across managed Linux hosts.")).
		StringField("defaultVacuumSize", "Default Vacuum Size Cap", DefaultVacuumSize,
			settingsframework.WithPlaceholder(DefaultVacuumSize),
			settingsframework.WithDescription("Default maximum disk space retained when running journal vacuum by size (e.g. 500M, 2G).")).
		StringField("defaultVacuumTime", "Default Vacuum Retention Window", DefaultVacuumTime,
			settingsframework.WithPlaceholder(DefaultVacuumTime),
			settingsframework.WithDescription("Default retention window when vacuuming archived journal files by age (e.g. 7d, 30d).")).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultLinesString() string {
	n := settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, "defaultLines", DefaultLinesCount)
	if n < 1 || n > 99999 {
		n = DefaultLinesCount
	}
	return strconv.Itoa(n)
}

func defaultPriorityFilter() string {
	return strings.ToLower(strings.TrimSpace(
		settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultPriority", ""),
	))
}

func defaultVacuumSize() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultVacuumSize", DefaultVacuumSize)
}

func defaultVacuumTime() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultVacuumTime", DefaultVacuumTime)
}

// ContextMenuEntries provides journald log maintenance context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(meta.Kind)).
		AddAction("rotate", "Rotate System Journal", "Autorenew", "rotate", 10).
		AddAction("flush", "Flush Journal to Persistent Storage", "save", "flush", 20).
		AddDangerAction("vacuum_size", "Vacuum Journal by Size", "cleaning_services", "vacuum_size",
			"Prune archived systemd journal files to enforce the target disk size cap?", 30).
		AddDangerAction("vacuum_time", "Vacuum Journal by Age", "history_toggle_off", "vacuum_time",
			"Delete archived systemd journal files older than the target retention window?", 40).
		Build()
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v journalctl")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "journalctl command not found",
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  "systemd journald active",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	unit := ""
	priority := defaultPriorityFilter()
	linesCount := defaultLinesString()
	scope := ""
	if h.Overrides != nil {
		if u, ok := h.Overrides["unit"]; ok && u != "" {
			unit = strings.TrimSpace(u)
		}
		if pr, ok := h.Overrides["priority"]; ok && pr != "" {
			priority = strings.ToLower(strings.TrimSpace(pr))
		}
		if l, ok := h.Overrides["lines"]; ok && l != "" {
			linesCount = strings.TrimSpace(l)
		}
		if s, ok := h.Overrides["scope"]; ok {
			parsed, err := systemd.ParseScope(s)
			if err != nil {
				return nil, err
			}
			if parsed == systemd.ScopeUser {
				scope = " --user"
			}
		}
	}

	// Filter values are interpolated into the SSH command string, so they are
	// validated against allow-lists rather than trusted verbatim.
	if unit != "" && !reJournalUnit.MatchString(unit) {
		return nil, fmt.Errorf("invalid journal unit filter %q", unit)
	}
	if priority != "" && !reJournalPriority.MatchString(priority) {
		return nil, fmt.Errorf("invalid journal priority %q", priority)
	}
	if !reJournalLines.MatchString(linesCount) {
		return nil, fmt.Errorf("invalid journal line count %q", linesCount)
	}

	cmd := fmt.Sprintf("journalctl%s -n %s --no-pager", scope, linesCount)
	if unit != "" {
		cmd += " -u " + shellQuote(unit)
	}
	if priority != "" {
		cmd += " -p " + priority
	}

	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	rawLines := strings.Split(out, "\n")
	var lines []string
	for _, l := range rawLines {
		t := strings.TrimRight(l, "\r\n ")
		if t != "" {
			lines = append(lines, t)
		}
	}

	return map[string]any{
		"lines": lines,
		"unit":  unit,
		"count": len(lines),
	}, nil
}

// ManagementActions returns the v2 management action specifications for systemd-journald.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "rotate",
				Label:       "Rotate Journal Files",
				Description: "Mark active systemd journal files as archived and start writing to new journal files",
				Options:     []plugin.OptionSchema{},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindLogs},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "flush",
				Label:       "Flush Journal to Persistent Storage",
				Description: "Flush volatile journal log data from /run/log/journal into persistent /var/log/journal",
				Options:     []plugin.OptionSchema{},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindLogs},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "vacuum_size",
				Label:       "Vacuum Journal by Size",
				Description: "Remove oldest archived journal files until total disk usage falls below the specified size",
				Options: []plugin.OptionSchema{
					{
						Name:        "size",
						Type:        "string",
						Required:    false,
						Default:     DefaultVacuumSize,
						Description: "Target maximum disk usage (e.g. 500M, 2G, 1024K)",
					},
				},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Prune archived systemd journal files to enforce the target disk size cap?",
			InvalidatesKinds: []plugin.Kind{plugin.KindLogs},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "vacuum_time",
				Label:       "Vacuum Journal by Age",
				Description: "Remove archived journal files older than the specified retention time window",
				Options: []plugin.OptionSchema{
					{
						Name:        "time",
						Type:        "string",
						Required:    false,
						Default:     DefaultVacuumTime,
						Description: "Target retention duration (e.g. 7d, 30d, 2weeks)",
					},
				},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Delete archived systemd journal files older than the target retention window?",
			InvalidatesKinds: []plugin.Kind{plugin.KindLogs},
		},
	}
}

// Actions enumerates mutation actions supported by the journald plugin.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

func normalizeActionName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "rotate", "journal-rotate":
		return "rotate"
	case "flush", "journal-flush":
		return "flush"
	case "vacuum_size", "vacuum-size":
		return "vacuum_size"
	case "vacuum_time", "vacuum-time":
		return "vacuum_time"
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

// ValidateAction validates options for a journald management action against its schema and format rules.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	canonical := normalizeActionName(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, canonical) {
			if err := newapi.ValidateManagementOptions(spec, opts); err != nil {
				return err
			}
			switch canonical {
			case "vacuum_size":
				if opts != nil {
					if raw, ok := opts["size"].(string); ok && strings.TrimSpace(raw) != "" {
						if !reVacuumSize.MatchString(strings.TrimSpace(raw)) {
							return fmt.Errorf("invalid journal vacuum size %q (expected e.g. 500M, 2G)", raw)
						}
					}
				}
			case "vacuum_time":
				if opts != nil {
					if raw, ok := opts["time"].(string); ok && strings.TrimSpace(raw) != "" {
						if !reVacuumTime.MatchString(strings.TrimSpace(raw)) {
							return fmt.Errorf("invalid journal vacuum time %q (expected e.g. 7d, 30d, 2weeks)", raw)
						}
					}
				}
			}
			return nil
		}
	}
	return fmt.Errorf("unsupported journald action: %s", action)
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

// Compile generates a declarative Ansible TaskSpec for journald maintenance operations.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if err := p.ValidateAction(h, action, opts); err != nil {
		return plugin.TaskSpec{}, err
	}

	canonical := normalizeActionName(action)
	var cmd string
	var stepName string
	var taskName string

	switch canonical {
	case "rotate":
		cmd = "journalctl --rotate"
		stepName = "Rotate systemd journal files"
		taskName = "Rotate systemd journal"
	case "flush":
		cmd = "journalctl --flush"
		stepName = "Flush volatile systemd journal to persistent storage"
		taskName = "Flush systemd journal"
	case "vacuum_size":
		size := ""
		if opts != nil {
			size, _ = opts["size"].(string)
		}
		size = strings.TrimSpace(size)
		if size == "" {
			size = strings.TrimSpace(defaultVacuumSize())
		}
		if !reVacuumSize.MatchString(size) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid journal vacuum size %q", size)
		}
		cmd = fmt.Sprintf("journalctl --vacuum-size=%s", size)
		stepName = fmt.Sprintf("Vacuum systemd journal to %s", size)
		taskName = fmt.Sprintf("Vacuum systemd journal by size (%s)", size)
	case "vacuum_time":
		duration := ""
		if opts != nil {
			duration, _ = opts["time"].(string)
		}
		duration = strings.TrimSpace(duration)
		if duration == "" {
			duration = strings.TrimSpace(defaultVacuumTime())
		}
		if !reVacuumTime.MatchString(duration) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid journal vacuum time %q", duration)
		}
		cmd = fmt.Sprintf("journalctl --vacuum-time=%s", duration)
		stepName = fmt.Sprintf("Vacuum systemd journal older than %s", duration)
		taskName = fmt.Sprintf("Vacuum systemd journal by age (%s)", duration)
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported journald action: %s", action)
	}

	return plugin.TaskSpec{
		Name: taskName,
		Steps: []plugin.TaskStep{
			{
				Name:   stepName,
				Module: "ansible.builtin.command",
				Params: map[string]any{
					"cmd":    cmd,
					"become": true,
				},
			},
		},
	}, nil
}
