package podman

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	pkgpodman "air-traffic-control/backend/pkg/podman"
	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/types"
)

const SettingsPageID = "container/podman"

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

// Plugin wraps pkg/podman to implement the v2 container plugin contracts.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          SettingsPageID,
		Kind:        plugin.KindContainer,
		Name:        "podman",
		Label:       "Podman & Rootless Containers",
		Icon:        "deployed_code",
		Description: "Rootless & system podman containers, pods, user session namespaces, and live stats",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"podman"},
			"apt": {"podman"},
			"apk": {"podman"},
		},
		ControllerCollections: []string{
			"containers.podman",
		},
	}
}

// CollectionSchedule returns the standard polling and cache TTL schedule for container telemetry.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the payload returned by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "containers",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide Podman container runtime defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(meta.ID, "Podman Container Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide Podman rootful/rootless socket probing, quadlet systemd integration, and container lifecycle defaults.").
		StringField(
			"rootfulSocketPath",
			"Rootful Socket Path",
			"/run/podman/podman.sock",
			settingsframework.WithPlaceholder("/run/podman/podman.sock"),
			settingsframework.WithDescription("Default unix socket path for system-wide rootful Podman API inspection."),
		).
		BoolField(
			"probeRootlessUsers",
			"Probe Unprivileged Rootless Users",
			true,
			settingsframework.WithDescription("Discover and inspect rootless Podman user sessions under /run/user/<uid>/podman/podman.sock."),
		).
		IntField(
			"stopTimeoutSeconds",
			"Default Stop Timeout (Seconds)",
			10,
			settingsframework.WithMinMax(1, 300),
			settingsframework.WithDescription("Graceful SIGTERM timeout in seconds before SIGKILL when stopping containers."),
		).
		SelectField(
			"defaultRestartPolicy",
			"Default Restart Policy",
			"on-failure",
			[]plugin.SettingsFieldOption{
				{Value: "no", Label: "No"},
				{Value: "on-failure", Label: "On Failure"},
				{Value: "always", Label: "Always"},
				{Value: "unless-stopped", Label: "Unless Stopped"},
			},
			settingsframework.WithDescription("Default restart policy for managed Podman containers."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying global SettingsPage schema.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v podman")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "podman command not found",
		}, nil
	}

	// Probe both rootful (/run/podman/podman.sock) and rootless (/run/user/<uid>/podman/podman.sock)
	// with AGENTS.md §3 privilege escalation (run0 -> sudo -n -> doas -n -> none), falling back to native CLI.
	cntCmd := "(" + pkgpodman.RootSocketOrCLICommand("/v4.0.0/libpod/containers/json?all=true", "podman ps -q") + " 2>/dev/null || true)"
	cntOut, err := r.RunCommand(ctx, cntCmd)
	count := 0
	trimmed := strings.TrimSpace(cntOut)
	if err == nil && trimmed != "" {
		if strings.HasPrefix(trimmed, "[") {
			// Count non-infra containers in socket JSON
			adapter := &runnerAdapter{ctx: ctx, runner: r}
			mgr := pkgpodman.NewManager(adapter, ssh.Config{})
			if all, listErr := mgr.ListAllContainers(); listErr == nil {
				count = len(all)
			}
		} else {
			count = len(strings.Fields(trimmed))
		}
	}

	return plugin.Detection{
		Detected: true,
		Status:   fmt.Sprintf("%d container(s)", count),
		Details:  "Podman engine detected (rootful & rootless sockets probed)",
	}, nil
}

// runnerAdapter wraps plugin.Runner into pkg/podman's CommandRunner
type runnerAdapter struct {
	ctx    context.Context
	runner plugin.Runner
}

func (a *runnerAdapter) RunCommand(cfg ssh.Config, command string) (string, error) {
	return a.runner.RunCommand(a.ctx, command)
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	adapter := &runnerAdapter{ctx: ctx, runner: r}
	mgr := pkgpodman.NewManager(adapter, ssh.Config{})

	// List containers and pods across root and unprivileged users
	containers, err := mgr.ListAllContainers()
	if err != nil {
		// Even if error, attempt to return partial or fallback
		containers = nil
	}

	var rootContainers []types.PodmanContainer
	for _, c := range containers {
		if !c.Rootless && (c.User == "root" || c.UID == 0) {
			rootContainers = append(rootContainers, c)
		}
	}

	pods, _ := mgr.ListPods()
	var rootPods []types.PodmanPod
	for _, pod := range pods {
		if !pod.Rootless && (pod.User == "root" || pod.UID == 0) {
			rootPods = append(rootPods, pod)
		}
	}
	unprivUsers, _ := mgr.ProbeUnprivilegedUsers()

	return map[string]any{
		"containers":     containers,
		"rootContainers": rootContainers,
		"pods":           pods,
		"rootPods":       rootPods,
		"probedUsers":    unprivUsers,
	}, nil
}

// ManagementActions returns rich action specifications for Podman container and pod lifecycle mutations.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "start",
				Label:       "Start Container",
				Description: "Start a stopped container",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindContainer},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "stop",
				Label:       "Stop Container",
				Description: "Stop a running container",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindContainer},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "restart",
				Label:       "Restart Container",
				Description: "Restart a container",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindContainer},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "delete",
				Label:       "Delete Container",
				Description: "Remove a container",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Permanently remove this Podman container?",
			InvalidatesKinds: []plugin.Kind{plugin.KindContainer},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "restart_pod",
				Label:       "Restart Pod",
				Description: "Restart a podman pod and all contained containers",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Pod name or ID"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindContainer},
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

// ValidateAction validates options against the declared ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm == "rm" || norm == "remove" {
		norm = "delete"
	}

	normalizedOpts := make(map[string]any, len(opts)+1)
	for k, v := range opts {
		normalizedOpts[k] = v
	}
	if name, _ := normalizedOpts["name"].(string); strings.TrimSpace(name) == "" {
		if c, ok := normalizedOpts["container"].(string); ok && strings.TrimSpace(c) != "" {
			normalizedOpts["name"] = strings.TrimSpace(c)
		} else if pod, ok := normalizedOpts["pod"].(string); ok && strings.TrimSpace(pod) != "" {
			normalizedOpts["name"] = strings.TrimSpace(pod)
		}
	}

	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, normalizedOpts)
		}
	}
	return fmt.Errorf("unsupported podman action: %s", action)
}

// ContextMenuEntries provides container context menu items for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(p.Meta().ID).
		WithTarget(newapi.TargetContainer).
		WithGroup(string(p.Meta().Kind)).
		AddAction("podman-start", "Start Container", "play_arrow", "start", 10).
		AddAction("podman-stop", "Stop Container", "stop", "stop", 20).
		AddAction("podman-restart", "Restart Container", "restart_alt", "restart", 30).
		AddAction("podman-restart-pod", "Restart Pod", "workspaces", "restart_pod", 40).
		AddDangerAction(
			"podman-delete",
			"Delete Container",
			"delete_forever",
			"delete",
			"Permanently remove this Podman container?",
			90,
		).
		Build()
}

// CompileTask compiles a typed TaskRequest into a declarative Ansible TaskSpec.
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
	name, _ := opts["name"].(string)
	if name == "" {
		name, _ = opts["container"].(string)
	}
	if name == "" {
		name, _ = opts["pod"].(string)
	}
	if name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("container or pod name is required")
	}

	user, _ := opts["user"].(string)
	becomeUser := false
	if user != "" && user != "root" {
		becomeUser = true
	}

	switch norm {
	case "start", "stop", "restart":
		stateMap := map[string]string{
			"start":   "started",
			"stop":    "stopped",
			"restart": "restarted",
		}
		params := map[string]any{
			"name":  name,
			"state": stateMap[norm],
		}
		if becomeUser {
			params["become"] = true
			params["become_user"] = user
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("%s container %s", strings.Title(norm), name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("%s container %s", strings.Title(norm), name),
					Module: "containers.podman.podman_container",
					Params: params,
				},
			},
		}, nil

	case "delete", "rm", "remove":
		params := map[string]any{
			"name":  name,
			"state": "absent",
		}
		if becomeUser {
			params["become"] = true
			params["become_user"] = user
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Remove container %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Remove container %s", name),
					Module: "containers.podman.podman_container",
					Params: params,
				},
			},
		}, nil

	case "restart_pod":
		params := map[string]any{
			"name":  name,
			"state": "restarted",
		}
		if becomeUser {
			params["become"] = true
			params["become_user"] = user
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Restart pod %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Restart pod %s", name),
					Module: "containers.podman.podman_pod",
					Params: params,
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported podman action: %s", action)
	}
}

// UnprivilegedUserHelper converts UID int if needed
func parseUID(val any) int {
	if i, ok := val.(int); ok {
		return i
	}
	if s, ok := val.(string); ok {
		if i, err := strconv.Atoi(s); err == nil {
			return i
		}
	}
	return 0
}
