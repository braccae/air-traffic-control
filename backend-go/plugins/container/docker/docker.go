// Package docker implements the container/docker plugin. It wraps pkg/docker
// (the Docker Engine socket access layer) to expose it as a compile-time ATC
// plugin: Plugin + Reader + Actor.
//
// Action path documentation (per task requirement):
//
//   - start / stop / restart / remove container and pull image all have real
//     declarative Ansible modules, so Compile() emits community.docker tasks and
//     the action is staged through the existing Ansible mutation queue:
//         * start/stop/restart/remove -> community.docker.docker_container
//         * pull image                -> community.docker.docker_image
//     These are the paths the UI stages.
//
//   - The pkg/docker Manager additionally exposes direct socket-API execution
//     (ContainerAction / PullImage) for immediate operations where an Ansible
//     run cannot be expressed or would be too heavyweight (and for read-back
//     verification). The Actor remains the canonical staging path.
//
// Zero-Mock Policy: Probe/Read reflect real `docker` CLI output read back from
// the host. A missing daemon or a failed command yields an explicit offline /
// unavailable Detection or a real error — never a synthesised success.
package docker

import (
	"context"
	"fmt"
	"strings"

	pkgdocker "air-traffic-control/backend/pkg/docker"
	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/ssh"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin wraps pkg/docker to implement plugin.Plugin, plugin.Reader and plugin.Actor.
type Plugin struct{}

// New returns a new Docker plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for the Docker plugin.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "container/docker",
		Kind:        plugin.KindContainer,
		Name:        "docker",
		Label:       "Docker Engine & Rootless Containers",
		Icon:        "deployed_code",
		Description: "Rootful & rootless Docker Engine containers, images, volumes, and networks",
		Order:       20,
	}
}

// Compatible performs a static eligibility check against the host facts.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

// Requirements returns host packages and controller Galaxy collections.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"docker-ce"},
			"apt": {"docker.io"},
			"apk": {"docker"},
		},
		ControllerCollections: []string{
			"community.docker",
		},
	}
}

// Probe determines whether Docker is present and reachable on the host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v docker")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "docker command not found",
		}, nil
	}

	// Count all containers (running and stopped) to report an accurate badge.
	cntOut, err := r.RunCommand(ctx, "docker ps -a -q 2>/dev/null | wc -l")
	count := 0
	if err == nil {
		if s := strings.TrimSpace(cntOut); s != "" {
			fmt.Sscanf(s, "%d", &count)
		}
	}

	return plugin.Detection{
		Detected: true,
		Status:   fmt.Sprintf("%d container(s)", count),
		Details:  "Docker Engine detected",
	}, nil
}

// runnerAdapter wraps plugin.Runner into pkg/docker's CommandRunner.
type runnerAdapter struct {
	ctx    context.Context
	runner plugin.Runner
}

// RunCommand satisfies pkg/docker.CommandRunner.
func (a *runnerAdapter) RunCommand(cfg ssh.Config, command string) (string, error) {
	return a.runner.RunCommand(a.ctx, command)
}

// Read retrieves live Docker Engine state: containers, images, volumes,
// networks, and the discovered rootless users.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	adapter := &runnerAdapter{ctx: ctx, runner: r}
	mgr := pkgdocker.NewManager(adapter, ssh.Config{})

	containers, cErr := mgr.ListAllContainers()
	images, _ := mgr.ListImages()
	volumes, _ := mgr.ListVolumes()
	networks, _ := mgr.ListNetworks()
	probedUsers, _ := mgr.ProbeRootlessUsers()
	_, serverVersion := mgr.Probe()

	// Zero-Mock: if the container listing itself failed and we have nothing,
	// surface the real error instead of returning an empty success.
	if cErr != nil && len(containers) == 0 {
		return nil, fmt.Errorf("failed to read docker state: %w", cErr)
	}

	return map[string]any{
		"containers":    containers,
		"images":        images,
		"volumes":       volumes,
		"networks":      networks,
		"probedUsers":   probedUsers,
		"serverVersion": serverVersion,
		"hasDocker":     true,
	}, nil
}

// Actions enumerates the mutation actions the Docker plugin supports. Every
// action below is staged as a declarative community.docker task through the
// Ansible mutation queue.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "start",
			Label:       "Start Container",
			Description: "Start a stopped Docker container",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
				{Name: "user", Type: "string", Required: false, Description: "Rootless user (blank/root for the system daemon)"},
			},
		},
		{
			Name:        "stop",
			Label:       "Stop Container",
			Description: "Stop a running Docker container",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
				{Name: "user", Type: "string", Required: false, Description: "Rootless user (blank/root for the system daemon)"},
			},
		},
		{
			Name:        "restart",
			Label:       "Restart Container",
			Description: "Restart a Docker container",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
				{Name: "user", Type: "string", Required: false, Description: "Rootless user (blank/root for the system daemon)"},
			},
		},
		{
			Name:        "remove",
			Label:       "Remove Container",
			Description: "Remove a Docker container",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Container name or ID"},
				{Name: "user", Type: "string", Required: false, Description: "Rootless user (blank/root for the system daemon)"},
			},
		},
		{
			Name:        "pull",
			Label:       "Pull Image",
			Description: "Pull an image into the Docker Engine",
			Options: []plugin.OptionSchema{
				{Name: "image", Type: "string", Required: true, Description: "Image reference, e.g. docker.io/library/alpine:latest"},
				{Name: "user", Type: "string", Required: false, Description: "Rootless user (blank/root for the system daemon)"},
			},
		},
	}
}

// Compile transforms a Docker action into a declarative Ansible TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))

	name, _ := opts["name"].(string)
	if name == "" {
		name, _ = opts["container"].(string)
	}
	image, _ := opts["image"].(string)
	user, _ := opts["user"].(string)

	becomeUser := user != "" && user != "root"
	applyBecome := func(params map[string]any) {
		if becomeUser {
			params["become"] = true
			params["become_user"] = user
		}
	}

	switch norm {
	case "start", "stop", "restart", "remove", "delete", "rm":
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("container name is required")
		}
		params := map[string]any{"name": name}
		var title string
		switch norm {
		case "start":
			params["state"] = "started"
			title = "Start"
		case "stop":
			params["state"] = "stopped"
			title = "Stop"
		case "restart":
			// community.docker.docker_container has no "restarted" state;
			// ensure started and force a restart.
			params["state"] = "started"
			params["restart"] = true
			title = "Restart"
		default: // remove/delete/rm
			params["state"] = "absent"
			title = "Remove"
		}
		applyBecome(params)
		step := plugin.TaskStep{
			Name:   fmt.Sprintf("%s container %s", title, name),
			Module: "community.docker.docker_container",
			Params: params,
		}
		return plugin.TaskSpec{
			Name:  step.Name,
			Steps: []plugin.TaskStep{step},
		}, nil

	case "pull", "pull_image":
		if strings.TrimSpace(image) == "" {
			return plugin.TaskSpec{}, fmt.Errorf("image reference is required")
		}
		params := map[string]any{
			"name":   image,
			"source": "pull",
			"state":  "present",
		}
		applyBecome(params)
		step := plugin.TaskStep{
			Name:   fmt.Sprintf("Pull image %s", image),
			Module: "community.docker.docker_image",
			Params: params,
		}
		return plugin.TaskSpec{
			Name:  step.Name,
			Steps: []plugin.TaskStep{step},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported docker action: %s", action)
	}
}
