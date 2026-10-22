package nfs

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements NFS exports table inspection and share management.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "share/nfs",
		Kind:        plugin.KindShare,
		Name:        "nfs",
		Label:       "NFS Shares",
		Icon:        "folder_shared",
		Description: "Kernel NFS server exports (/etc/exports) and client permissions",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"nfs-utils"},
			"apt": {"nfs-kernel-server"},
			"apk": {"nfs-utils"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "cat /var/lib/nfs/etab /etc/exports /etc/exports.d/*.exports 2>/dev/null || true")
	if err == nil && strings.TrimSpace(out) != "" {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		var valid int
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				valid++
			}
		}
		if valid > 0 {
			return plugin.Detection{
				Detected: true,
				Status:   fmt.Sprintf("%d export(s)", valid),
				Details:  "Active NFS exports found",
			}, nil
		}
	}

	svcOut, err := r.RunCommand(ctx, "systemctl is-active nfs-server 2>/dev/null || true")
	if err == nil && strings.TrimSpace(svcOut) == "active" {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "nfs-server active, no exports configured",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "UNAVAILABLE",
		Error:    "no active NFS exports or nfs-server service found",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "cat /var/lib/nfs/etab /etc/exports 2>/dev/null || echo 'NO_NFS'"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"nfs": strings.TrimSpace(out),
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "export",
			Label:       "Configure NFS Export",
			Description: "Add or update an NFS export line in /etc/exports and reload",
			Options: []plugin.OptionSchema{
				{Name: "path", Type: "string", Required: true, Description: "Directory path to export (e.g. /export/data)"},
				{Name: "client", Type: "string", Required: false, Default: "*", Description: "Client CIDR, hostname, or *"},
				{Name: "options", Type: "string", Required: false, Default: "rw,sync,no_subtree_check", Description: "Export options"},
				{Name: "state", Type: "select", Enum: []string{"present", "absent"}, Default: "present", Description: "Export presence state"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	if norm != "export" && norm != "nfs" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported nfs action: %s", action)
	}

	path, _ := opts["path"].(string)
	if path == "" {
		path = "/export"
	}
	client, _ := opts["client"].(string)
	if client == "" {
		client = "*"
	}
	options, _ := opts["options"].(string)
	if options == "" {
		options = "rw,sync,no_subtree_check"
	}
	state, _ := opts["state"].(string)
	if state == "" {
		state = "present"
	}

	exportLine := fmt.Sprintf("%s %s(%s)", path, client, options)

	steps := []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Configure NFS export for %s", path),
			Module: "ansible.builtin.lineinfile",
			Params: map[string]any{
				"path":   "/etc/exports",
				"line":   exportLine,
				"state":  state,
				"create": true,
				"mode":   "0644",
				"become": true,
			},
		},
		{
			Name:   "Reload NFS exports table",
			Module: "ansible.builtin.command",
			Params: map[string]any{
				"cmd":    "exportfs -ra",
				"become": true,
			},
		},
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("Configure NFS export at %s", path),
		Steps: steps,
	}, nil
}
