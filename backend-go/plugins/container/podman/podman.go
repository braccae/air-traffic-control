package podman

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	pkgpodman "air-traffic-control/backend/pkg/podman"
	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/types"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin wraps pkg/podman to implement plugin.Plugin, plugin.Reader, and plugin.Actor.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "container/podman",
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

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "start",
			Label:       "Start Container",
			Description: "Start a stopped container",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
			},
		},
		{
			Name:        "stop",
			Label:       "Stop Container",
			Description: "Stop a running container",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
			},
		},
		{
			Name:        "restart",
			Label:       "Restart Container",
			Description: "Restart a container",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
			},
		},
		{
			Name:        "delete",
			Label:       "Delete Container",
			Description: "Remove a container",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
			},
		},
		{
			Name:        "restart_pod",
			Label:       "Restart Pod",
			Description: "Restart a podman pod and all contained containers",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Pod name or ID"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
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
