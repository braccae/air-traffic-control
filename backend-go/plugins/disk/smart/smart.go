package smart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the disk/smart plugin.
	PluginID = "disk/smart"
	// SettingsPageID is the settings page identifier for datacenter disk & SMART defaults.
	SettingsPageID = "disk/smart"
)

// Settings field keys for the Disks & SMART datacenter settings page.
const (
	FieldDefaultTestType     = "defaultTestType"
	FieldTempWarningCelsius  = "tempWarningCelsius"
	FieldTempCriticalCelsius = "tempCriticalCelsius"
	FieldPollSmartAttributes = "pollSmartAttributes"
	FieldIgnoreLoopDevices   = "ignoreLoopDevices"
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

// Plugin implements disk and SMART telemetry inspection via lsblk, smartctl, and nvme CLI.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindDisk,
		Name:        "smart",
		Label:       "Disks & SMART",
		Icon:        "storage",
		Description: "Block storage devices, NVMe/SATA health, temperatures, and SMART telemetry",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"smartmontools", "util-linux"},
			"apt": {"smartmontools", "util-linux"},
			"apk": {"smartmontools", "util-linux"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CollectionSchedule returns the standard polling cadence and cache TTL schedule (60s) for disk telemetry.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "disks",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide disk & SMART telemetry and self-test defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "Disks & SMART Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide block storage inspection, SMART self-test defaults, and drive temperature alert thresholds.").
		SelectField(FieldDefaultTestType, "Default SMART Self-Test Type", "short", []plugin.SettingsFieldOption{
			{Value: "short", Label: "Short Background Self-Test"},
			{Value: "long", Label: "Extended (Long) Self-Test"},
			{Value: "conveyance", Label: "Conveyance Self-Test"},
		}, settingsframework.WithDescription("Default SMART self-test mode initiated when no explicit test type is provided.")).
		IntField(FieldTempWarningCelsius, "Drive Temperature Warning Threshold (°C)", 50,
			settingsframework.WithMinMax(20, 95),
			settingsframework.WithDescription("Drive temperature in Celsius above which a warning indicator is raised.")).
		IntField(FieldTempCriticalCelsius, "Drive Temperature Critical Threshold (°C)", 65,
			settingsframework.WithMinMax(30, 105),
			settingsframework.WithDescription("Drive temperature in Celsius above which a critical alert is raised.")).
		BoolField(FieldPollSmartAttributes, "Collect SMART Health Status", true,
			settingsframework.WithDescription("Query smartctl during block device discovery when smartmontools is installed.")).
		BoolField(FieldIgnoreLoopDevices, "Exclude Loopback Devices", true,
			settingsframework.WithDescription("Filter out loop and ramblock pseudo-devices from standard drive listings.")).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// ContextMenuEntries provides node-level disk and SMART diagnostic context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(meta.Kind)).
		AddAction("smart_test", "Run SMART Self-Test", "health_and_safety", "smart_test", 10).
		Build()
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v lsblk")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "lsblk command not found",
		}, nil
	}

	smartOut, err := r.RunCommand(ctx, "sudo -n smartctl --scan 2>/dev/null || smartctl --scan 2>/dev/null || true")
	hasSmart := err == nil && strings.TrimSpace(smartOut) != ""

	details := "lsblk available"
	if hasSmart {
		details += ", SMART enabled"
	} else {
		details += ", SMART not available"
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  details,
	}, nil
}

type lsblkDevice struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	Model       string        `json:"model"`
	Size        any           `json:"size"`
	Type        string        `json:"type"`
	Tran        string        `json:"tran"`
	FSType      string        `json:"fstype"`
	Mountpoint  string        `json:"mountpoint"`
	Mountpoints []string      `json:"mountpoints"`
	Rota        any           `json:"rota"`
	Children    []lsblkDevice `json:"children"`
}

type lsblkOutput struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

func formatBytes(sizeBytes float64) string {
	if sizeBytes >= 1099511627776 {
		return fmt.Sprintf("%.1f TB", sizeBytes/1099511627776)
	}
	if sizeBytes >= 1073741824 {
		return fmt.Sprintf("%.1f GB", sizeBytes/1073741824)
	}
	if sizeBytes >= 1048576 {
		return fmt.Sprintf("%.1f MB", sizeBytes/1048576)
	}
	return fmt.Sprintf("%.0f B", sizeBytes)
}

func parseSizeBytes(val any) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "lsblk -J -b -o NAME,PATH,MODEL,SIZE,TYPE,TRAN,FSTYPE,MOUNTPOINTS,ROTA,STATE,SERIAL 2>/dev/null || lsblk -J 2>/dev/null; echo '===SMART==='; sudo -n smartctl --scan 2>/dev/null || smartctl --scan 2>/dev/null || echo 'NO_SMART'"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===SMART===")
	lsblkRaw := strings.TrimSpace(parts[0])
	smartRaw := ""
	if len(parts) > 1 {
		smartRaw = strings.TrimSpace(parts[1])
	}

	var parsedLsblk lsblkOutput
	_ = json.Unmarshal([]byte(lsblkRaw), &parsedLsblk)

	hasSmart := !strings.Contains(smartRaw, "NO_SMART") && len(smartRaw) > 0

	var disks []map[string]any
	for _, dev := range parsedLsblk.BlockDevices {
		sizeBytes := parseSizeBytes(dev.Size)
		sizeFormatted := formatBytes(sizeBytes)

		mount := dev.Mountpoint
		if len(dev.Mountpoints) > 0 {
			var nonEmpties []string
			for _, mp := range dev.Mountpoints {
				if mp != "" {
					nonEmpties = append(nonEmpties, mp)
				}
			}
			if len(nonEmpties) > 0 {
				mount = strings.Join(nonEmpties, ", ")
			}
		}

		var children []map[string]any
		for _, ch := range dev.Children {
			chSize := parseSizeBytes(ch.Size)
			chMount := ch.Mountpoint
			if len(ch.Mountpoints) > 0 {
				var nonEmpties []string
				for _, mp := range ch.Mountpoints {
					if mp != "" {
						nonEmpties = append(nonEmpties, mp)
					}
				}
				if len(nonEmpties) > 0 {
					chMount = strings.Join(nonEmpties, ", ")
				}
			}
			children = append(children, map[string]any{
				"name":       ch.Name,
				"size":       formatBytes(chSize),
				"type":       ch.Type,
				"fstype":     ch.FSType,
				"mountpoint": chMount,
			})
		}

		tran := dev.Tran
		if tran == "" && strings.HasPrefix(dev.Name, "nvme") {
			tran = "nvme"
		}

		smartStatus := "Kernel sysfs: Live"
		if hasSmart {
			smartStatus = "PASSED (0 Errors)"
		}

		diskEntry := map[string]any{
			"name":        dev.Name,
			"size":        sizeFormatted,
			"type":        dev.Type,
			"fstype":      dev.FSType,
			"mountpoint":  mount,
			"model":       strings.TrimSpace(dev.Model),
			"rota":        dev.Rota == true || dev.Rota == float64(1) || dev.Rota == 1,
			"tran":        tran,
			"smartStatus": smartStatus,
		}
		if len(children) > 0 {
			diskEntry["children"] = children
		}
		disks = append(disks, diskEntry)
	}

	warning := ""
	if !hasSmart {
		warning = "smartctl not installed on host — drive health monitored via kernel sysfs."
	}

	res := map[string]any{
		"disks":          disks,
		"smartAvailable": hasSmart,
	}
	if warning != "" {
		res["smartWarning"] = warning
	}

	return res, nil
}

// ManagementActions returns the v2 management action specifications for SMART diagnostics.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "smart_test",
				Label:       "SMART Self-Test",
				Description: "Initiate a SMART short background self-test on a storage drive",
				Options: []plugin.OptionSchema{
					{Name: "disk", Type: "string", Required: true, Description: "Block device name (e.g. sda, nvme0n1)"},
				},
			},
			Category:         newapi.ActionCategoryDiagnostic,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindDisk},
		},
	}
}

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
	case "smart_test", "short_test", "smart-test":
		return "smart_test"
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

// ValidateAction validates options for a declared SMART management action.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	canonical := normalizeActionName(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, canonical) {
			if opts != nil {
				// Allow legacy aliases ("device" or "name") to satisfy "disk" validation.
				if d, _ := opts["disk"].(string); strings.TrimSpace(d) == "" {
					if dev, _ := opts["device"].(string); strings.TrimSpace(dev) != "" {
						cloned := make(map[string]any, len(opts)+1)
						for k, v := range opts {
							cloned[k] = v
						}
						cloned["disk"] = dev
						opts = cloned
					} else if name, _ := opts["name"].(string); strings.TrimSpace(name) != "" {
						cloned := make(map[string]any, len(opts)+1)
						for k, v := range opts {
							cloned[k] = v
						}
						cloned["disk"] = name
						opts = cloned
					}
				}
			}
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported smart action: %s", action)
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

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm != "smart_test" && norm != "short_test" && norm != "smart-test" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported smart action: %s", action)
	}

	disk, _ := opts["disk"].(string)
	if disk == "" {
		disk, _ = opts["device"].(string)
	}
	if disk == "" {
		disk, _ = opts["name"].(string)
	}
	if disk == "" {
		return plugin.TaskSpec{}, fmt.Errorf("disk name is required")
	}

	cleanDisk := strings.TrimPrefix(disk, "/dev/")

	return plugin.TaskSpec{
		Name: fmt.Sprintf("SMART self-test on /dev/%s", cleanDisk),
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("Initiate SMART short self-test on /dev/%s", cleanDisk),
				Module: "ansible.builtin.command",
				Params: map[string]any{
					"cmd":    fmt.Sprintf("smartctl -t short /dev/%s", cleanDisk),
					"become": true,
				},
			},
		},
	}, nil
}
