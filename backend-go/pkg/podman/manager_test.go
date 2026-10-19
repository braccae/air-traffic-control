package podman

import (
	"fmt"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/ssh"
)

type mockRunner struct {
	handler func(cfg ssh.Config, cmd string) (string, error)
	calls   []string
}

func (m *mockRunner) RunCommand(cfg ssh.Config, cmd string) (string, error) {
	m.calls = append(m.calls, cmd)
	if m.handler != nil {
		return m.handler(cfg, cmd)
	}
	return "", fmt.Errorf("unexpected command: %s", cmd)
}

func TestProbeUnprivilegedUsers_FindsRootlessPodmanUsers(t *testing.T) {
	runner := &mockRunner{
		handler: func(cfg ssh.Config, cmd string) (string, error) {
			switch {
			case strings.Contains(cmd, "ps -eo"):
				// Process list includes root (0), deployer (1000) running conmon/catatonit,
				// podman (1001) running podman/pasta, and www-data (33) running nginx (not podman).
				return strings.Join([]string{
					"0 root /usr/lib/systemd/systemd --system",
					"0 root /usr/bin/conmon --api-version 1 -c root_container",
					"33 www-data nginx: worker process",
					"1000 deployer /usr/bin/conmon --api-version 1 -c d9841af024ea -u d9841af024ea",
					"1000 deployer /usr/libexec/podman/catatonit -P",
					"1001 podman /usr/bin/pasta --config-net",
					"1001 podman /usr/bin/conmon --api-version 1 -c 7e4b9019a101",
					"1002 alice -bash",
				}, "\n"), nil

			case strings.Contains(cmd, "/etc/passwd"):
				return strings.Join([]string{
					"root:x:0:0:root:/root:/bin/bash",
					"www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin",
					"deployer:x:1000:1000:Deployer:/home/deployer:/bin/bash",
					"podman:x:1001:1001:Podman Rootless:/home/podman:/bin/bash",
					"alice:x:1002:1002:Alice:/home/alice:/bin/bash",
					"svc-media:x:995:995:Media Service:/var/lib/svc-media:/sbin/nologin",
				}, "\n"), nil

			case strings.Contains(cmd, "/etc/subuid"):
				return strings.Join([]string{
					"deployer:100000:65536",
					"podman:165536:65536",
					"alice:231072:65536",
					"svc-media:296608:65536",
				}, "\n"), nil

			case strings.Contains(cmd, "/var/lib/systemd/linger"):
				return "deployer\npodman\nsvc-media\n", nil

			case strings.Contains(cmd, "deployer") && strings.Contains(cmd, "podman ps"):
				return `[
					{
						"Id": "3f0a9918bc22",
						"Names": ["jellyfin-media-server"],
						"Image": "jellyfin/jellyfin:10.8.13",
						"State": "running",
						"Status": "Up 2 days",
						"Pod": "a98c091f09e1"
					},
					{
						"Id": "c810d48a1239",
						"Names": ["caddy-gateway"],
						"Image": "caddy:2.7.6-alpine",
						"State": "running",
						"Status": "Up 2 days",
						"Pod": "a98c091f09e1"
					}
				]`, nil

			case strings.Contains(cmd, "deployer") && strings.Contains(cmd, "podman pod ps"):
				return `[
					{
						"Id": "a98c091f09e1",
						"Name": "media-stack",
						"Status": "Running"
					}
				]`, nil

			case strings.Contains(cmd, "podman") && strings.Contains(cmd, "-u podman") && strings.Contains(cmd, "podman ps"):
				return `[
					{
						"Id": "7e4b9019a101",
						"Names": ["ollama-vllm-engine"],
						"Image": "ollama/ollama:latest",
						"State": "running",
						"Status": "Up 12 hours",
						"Pod": "bb09817adfe2"
					}
				]`, nil

			case strings.Contains(cmd, "-u podman") && strings.Contains(cmd, "podman pod ps"):
				return `[
					{
						"Id": "bb09817adfe2",
						"Name": "ai-inference",
						"Status": "Running"
					}
				]`, nil

			case strings.Contains(cmd, "svc-media") && strings.Contains(cmd, "podman ps"):
				// Even though svc-media wasn't in the initial ps snapshot, it has subuid/linger
				// and is running a rootless container!
				return `[
					{
						"Id": "99aa11bb22cc",
						"Names": ["transcode-worker"],
						"Image": "handbrake/worker:latest",
						"State": "running",
						"Status": "Up 1 hour",
						"Pod": ""
					}
				]`, nil

			case strings.Contains(cmd, "svc-media") && strings.Contains(cmd, "podman pod ps"):
				return `[]`, nil

			case strings.Contains(cmd, "alice") && strings.Contains(cmd, "podman ps"):
				// Alice has subuid but is NOT running any podman containers
				return `[]`, nil

			case strings.Contains(cmd, "alice") && strings.Contains(cmd, "podman pod ps"):
				return `[]`, nil

			default:
				return `[]`, nil
			}
		},
	}

	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})
	users, err := mgr.ProbeUnprivilegedUsers()
	if err != nil {
		t.Fatalf("expected no error from ProbeUnprivilegedUsers, got: %v", err)
	}

	// Should find deployer (1000), podman (1001), and svc-media (995),
	// and MUST NOT include root (0), www-data (33), or alice (1002, no running containers).
	if len(users) != 3 {
		t.Fatalf("expected 3 unprivileged users running podman containers, got %d: %+v", len(users), users)
	}

	byName := make(map[string]int)
	for i, u := range users {
		if u.UID == 0 || u.User == "root" {
			t.Errorf("probe must not return privileged root user, got: %+v", u)
		}
		byName[u.User] = i
	}

	depIdx, ok := byName["deployer"]
	if !ok {
		t.Fatalf("expected deployer user to be discovered")
	}
	deployer := users[depIdx]
	if deployer.UID != 1000 {
		t.Errorf("expected deployer UID 1000, got %d", deployer.UID)
	}
	if deployer.SubUIDRange != "100000:65536" {
		t.Errorf("expected deployer SubUIDRange 100000:65536, got %q", deployer.SubUIDRange)
	}
	if !deployer.Linger {
		t.Errorf("expected deployer Linger to be true")
	}
	if len(deployer.Containers) != 2 {
		t.Fatalf("expected deployer to have 2 running containers, got %d", len(deployer.Containers))
	}
	if !deployer.Containers[0].Rootless || deployer.Containers[0].User != "deployer" || deployer.Containers[0].UID != 1000 {
		t.Errorf("expected container to be marked Rootless with user=deployer, uid=1000, got %+v", deployer.Containers[0])
	}

	podIdx, ok := byName["podman"]
	if !ok {
		t.Fatalf("expected podman user to be discovered")
	}
	podmanUser := users[podIdx]
	if podmanUser.UID != 1001 || len(podmanUser.Containers) != 1 || len(podmanUser.Pods) != 1 {
		t.Errorf("unexpected podman user probe data: %+v", podmanUser)
	}

	svcIdx, ok := byName["svc-media"]
	if !ok {
		t.Fatalf("expected unprivileged system service user svc-media (UID 995) to be discovered")
	}
	svcUser := users[svcIdx]
	if svcUser.UID != 995 || len(svcUser.Containers) != 1 {
		t.Errorf("unexpected svc-media user probe data: %+v", svcUser)
	}
}

func TestListPods_IncludesRootAndUnprivilegedUserPods(t *testing.T) {
	runner := &mockRunner{
		handler: func(cfg ssh.Config, cmd string) (string, error) {
			switch {
			case strings.Contains(cmd, "podman pod ps --format json") && !strings.Contains(cmd, "-u"):
				return `[{"Id": "root-pod-1", "Name": "system-ingress", "Status": "Running"}]`, nil
			case strings.Contains(cmd, "ps -eo"):
				return "1001 podman /usr/bin/conmon --api-version 1 -c 7e4b9019a101\n", nil
			case strings.Contains(cmd, "/etc/passwd"):
				return "root:x:0:0:root:/root:/bin/bash\npodman:x:1001:1001:Podman:/home/podman:/bin/bash\n", nil
			case strings.Contains(cmd, "/etc/subuid"):
				return "podman:165536:65536\n", nil
			case strings.Contains(cmd, "/var/lib/systemd/linger"):
				return "podman\n", nil
			case strings.Contains(cmd, "-u podman") && strings.Contains(cmd, "podman ps"):
				return `[{"Id": "7e4b9019a101", "Names": ["ollama"], "Image": "ollama/ollama:latest", "State": "running", "Status": "Up 2h", "Pod": "bb09817adfe2"}]`, nil
			case strings.Contains(cmd, "-u podman") && strings.Contains(cmd, "podman pod ps"):
				return `[{"Id": "bb09817adfe2", "Name": "ai-inference", "Status": "Running"}]`, nil
			default:
				return `[]`, nil
			}
		},
	}

	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.153", Port: 22, User: "root"})
	pods, err := mgr.ListPods()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("expected 2 pods (1 root + 1 rootless), got %d: %+v", len(pods), pods)
	}

	var foundRoot, foundRootless bool
	for _, p := range pods {
		if p.Name == "system-ingress" && !p.Rootless && p.User == "root" {
			foundRoot = true
		}
		if p.Name == "ai-inference" && p.Rootless && p.User == "podman" && p.UID == 1001 {
			foundRootless = true
		}
	}
	if !foundRoot || !foundRootless {
		t.Errorf("expected both root and rootless pods, got: %+v", pods)
	}
}

func TestProbeUnprivilegedUsers_DiscoversFromRunUserDirAndTruncatedPS(t *testing.T) {
	runner := &mockRunner{
		handler: func(cfg ssh.Config, cmd string) (string, error) {
			switch {
			case strings.Contains(cmd, "ps -eo"):
				// Long username truncated with '+' in standard ps, or crun/fuse-overlayfs process
				return "1055 longusern+ /usr/bin/fuse-overlayfs -o lowerdir=/home/longusername/.local/share/containers\n", nil
			case strings.Contains(cmd, "/etc/passwd"):
				return strings.Join([]string{
					"root:x:0:0:root:/root:/bin/bash",
					"longusername:x:1055:1055:Long User:/home/longusername:/bin/bash",
					"runtimeuser:x:1060:1060:Runtime User:/home/runtimeuser:/bin/bash",
				}, "\n"), nil
			case strings.Contains(cmd, "/etc/subuid"):
				return "", nil
			case strings.Contains(cmd, "/var/lib/systemd/linger"):
				return "", nil
			case strings.Contains(cmd, "ls -1d /run/user"):
				return "/run/user/1060/libpod\n/run/user/0/libpod\n", nil
			case strings.Contains(cmd, "-u longusername") && strings.Contains(cmd, "podman ps"):
				return `[{"Id": "c1", "Names": ["web-app"], "Image": "nginx:alpine", "State": "running", "Status": "Up 5m"}]`, nil
			case strings.Contains(cmd, "-u runtimeuser") && strings.Contains(cmd, "podman ps"):
				return `[{"Id": "c2", "Names": ["redis-cache"], "Image": "redis:7", "State": "running", "Status": "Up 10m"}]`, nil
			default:
				return `[]`, nil
			}
		},
	}

	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})
	users, err := mgr.ProbeUnprivilegedUsers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 unprivileged users (longusername and runtimeuser), got %d: %+v", len(users), users)
	}
	if users[0].User != "longusername" || users[0].UID != 1055 {
		t.Errorf("expected longusername with UID 1055, got %+v", users[0])
	}
	if users[1].User != "runtimeuser" || users[1].UID != 1060 {
		t.Errorf("expected runtimeuser with UID 1060, got %+v", users[1])
	}
}

func TestListAllContainers_CombinesRootAndUnprivilegedContainers(t *testing.T) {
	runner := &mockRunner{
		handler: func(cfg ssh.Config, cmd string) (string, error) {
			switch {
			case strings.Contains(cmd, "podman ps --all --format json") && !strings.Contains(cmd, "-u"):
				return `[{"Id": "root-c1", "Names": ["traefik-proxy"], "Image": "traefik:v3", "State": "running", "Status": "Up 3d"}]`, nil
			case strings.Contains(cmd, "ps -eo"):
				return "1000 deployer /usr/bin/conmon -c rootless-c1\n", nil
			case strings.Contains(cmd, "/etc/passwd"):
				return "deployer:x:1000:1000::/home/deployer:/bin/bash\n", nil
			case strings.Contains(cmd, "-u deployer") && strings.Contains(cmd, "podman ps"):
				return `[{"Id": "rootless-c1", "Names": ["jellyfin"], "Image": "jellyfin/jellyfin", "State": "running", "Status": "Up 1d"}]`, nil
			default:
				return `[]`, nil
			}
		},
	}

	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})
	containers, err := mgr.ListAllContainers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d: %+v", len(containers), containers)
	}
	if containers[0].Name != "traefik-proxy" || containers[0].Rootless || containers[0].User != "root" {
		t.Errorf("unexpected root container: %+v", containers[0])
	}
	if containers[1].Name != "jellyfin" || !containers[1].Rootless || containers[1].User != "deployer" || containers[1].UID != 1000 {
		t.Errorf("unexpected rootless container: %+v", containers[1])
	}
}

func TestRootlessFallback_ContainerLogsAndActions(t *testing.T) {
	var executedCmds []string
	runner := &mockRunner{
		handler: func(cfg ssh.Config, cmd string) (string, error) {
			executedCmds = append(executedCmds, cmd)
			switch {
			case strings.Contains(cmd, "podman logs --tail 50 jellyfin") && !strings.Contains(cmd, "-u"):
				return "", fmt.Errorf("Error: no container with name or ID \"jellyfin\" found")
			case strings.Contains(cmd, "podman restart jellyfin") && !strings.Contains(cmd, "-u"):
				return "", fmt.Errorf("Error: no container with name or ID \"jellyfin\" found")
			case strings.Contains(cmd, "podman pod restart media-stack") && !strings.Contains(cmd, "-u"):
				return "", fmt.Errorf("Error: no pod with name or ID \"media-stack\" found")
			case strings.Contains(cmd, "ps -eo"):
				return "1000 deployer /usr/bin/conmon -c d9841af024ea\n", nil
			case strings.Contains(cmd, "/etc/passwd"):
				return "deployer:x:1000:1000::/home/deployer:/bin/bash\n", nil
			case strings.Contains(cmd, "-u deployer") && strings.Contains(cmd, "podman ps"):
				return `[{"Id": "3f0a9918bc22", "Names": ["jellyfin"], "Image": "jellyfin/jellyfin", "State": "running", "Status": "Up 1d"}]`, nil
			case strings.Contains(cmd, "-u deployer") && strings.Contains(cmd, "podman pod ps"):
				return `[{"Id": "a98c091f09e1", "Name": "media-stack", "Status": "Running"}]`, nil
			case strings.Contains(cmd, "-u deployer") && strings.Contains(cmd, "podman logs --tail 50 jellyfin"):
				return "INFO [jellyfin] Rootless server ready", nil
			case strings.Contains(cmd, "-u deployer") && strings.Contains(cmd, "podman restart jellyfin"):
				return "jellyfin", nil
			case strings.Contains(cmd, "-u deployer") && strings.Contains(cmd, "podman pod restart media-stack"):
				return "a98c091f09e1", nil
			default:
				return `[]`, nil
			}
		},
	}

	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})

	logs, err := mgr.GetContainerLogs("jellyfin", 50)
	if err != nil {
		t.Fatalf("expected fallback to unprivileged user for GetContainerLogs, got error: %v", err)
	}
	if !strings.Contains(logs, "Rootless server ready") {
		t.Errorf("unexpected logs output: %q", logs)
	}

	if err := mgr.ContainerAction("jellyfin", "restart"); err != nil {
		t.Fatalf("expected fallback to unprivileged user for ContainerAction, got error: %v", err)
	}

	if err := mgr.RestartPod("media-stack"); err != nil {
		t.Fatalf("expected fallback to unprivileged user for RestartPod, got error: %v", err)
	}
}

func TestProbeUnprivilegedUsers_FallbackToSuAndConmonArgs(t *testing.T) {
	runner := &mockRunner{
		handler: func(cfg ssh.Config, cmd string) (string, error) {
			switch {
			case strings.Contains(cmd, "ps -eo"):
				return strings.Join([]string{
					"1005media-user /usr/bin/conmon", // malformed line without space should be ignored safely
					"1005 mediauser /usr/bin/conmon --api-version 1 --cid=88ab99cd00ef -nplex-server -u 88ab99cd00ef",
					"1006 suonly /usr/bin/conmon --api-version 1 -c 112233445566 -n navidrome",
				}, "\n"), nil
			case strings.Contains(cmd, "/etc/passwd"):
				return strings.Join([]string{
					"mediauser:x:1005:1005::/home/mediauser:/sbin/nologin",
					"suonly:x:1006:1006::/home/suonly:/bin/bash",
				}, "\n"), nil
			case strings.Contains(cmd, "sudo ") && strings.Contains(cmd, "suonly"):
				return "", fmt.Errorf("sudo: command not found")
			case strings.HasPrefix(cmd, "su - suonly") && strings.Contains(cmd, "podman ps"):
				return `[{"Id": "112233445566", "Names": ["navidrome"], "Image": "deluan/navidrome:latest", "State": "running", "Status": "Up 4h"}]`, nil
			case strings.Contains(cmd, "mediauser") && strings.Contains(cmd, "podman ps"):
				// Both sudo and su fail for mediauser (e.g. nologin / restricted environment)
				return "", fmt.Errorf("This account is currently not available")
			default:
				return `[]`, nil
			}
		},
	}

	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})
	users, err := mgr.ProbeUnprivilegedUsers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 unprivileged users (mediauser via conmon fallback, suonly via su fallback), got %d: %+v", len(users), users)
	}

	if users[0].User != "mediauser" || users[0].UID != 1005 || len(users[0].Containers) != 1 {
		t.Fatalf("expected mediauser with 1 container extracted from conmon args, got %+v", users[0])
	}
	if users[0].Containers[0].ID != "88ab99cd00ef" || users[0].Containers[0].Name != "plex-server" || !users[0].Containers[0].Rootless {
		t.Errorf("unexpected conmon-extracted container: %+v", users[0].Containers[0])
	}

	if users[1].User != "suonly" || users[1].UID != 1006 || len(users[1].Containers) != 1 || users[1].Containers[0].Name != "navidrome" {
		t.Errorf("unexpected su-fallback user: %+v", users[1])
	}
}

func TestListPods_SucceedsWhenRootPodmanFailsButUnprivilegedSucceeds(t *testing.T) {
	runner := &mockRunner{
		handler: func(cfg ssh.Config, cmd string) (string, error) {
			switch {
			case cmd == "podman pod ps --format json":
				return "", fmt.Errorf("root podman socket not active")
			case strings.Contains(cmd, "ps -eo"):
				return "1001 podman /usr/bin/conmon -c c1\n", nil
			case strings.Contains(cmd, "/etc/passwd"):
				return "podman:x:1001:1001::/home/podman:/bin/bash\n", nil
			case strings.Contains(cmd, "-u podman") && strings.Contains(cmd, "podman ps"):
				return `[{"Id": "c1", "Names": ["app"], "Image": "alpine", "State": "running", "Pod": "p1"}]`, nil
			case strings.Contains(cmd, "-u podman") && strings.Contains(cmd, "podman pod ps"):
				return `[{"Id": "p1", "Name": "rootless-pod", "Status": "Running"}]`, nil
			default:
				return `[]`, nil
			}
		},
	}

	mgr := NewManager(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})
	pods, err := mgr.ListPods()
	if err != nil {
		t.Fatalf("expected ListPods to succeed using unprivileged user pods, got error: %v", err)
	}
	if len(pods) != 1 || pods[0].Name != "rootless-pod" || !pods[0].Rootless || pods[0].User != "podman" {
		t.Errorf("unexpected pods: %+v", pods)
	}
}

func TestListUserContainers_UsesLoginShellOrMachinectlWhenHomeDirRestricted(t *testing.T) {
	runner := &mockRunner{
		handler: func(cfg ssh.Config, cmd string) (string, error) {
			switch {
			case strings.Contains(cmd, "ps -eo"):
				return "1001 uosserver /usr/libexec/podman/catatonit -P\n1001 uosserver /usr/bin/conmon --api-version 1 -c 417811aa3ef5 -u 417811aa3ef5\n", nil
			case strings.Contains(cmd, "/etc/passwd"):
				return "core:x:1000:1000:Core:/var/home/core:/bin/bash\nuosserver:x:1001:1001:UOS:/var/home/uosserver:/bin/bash\n", nil
			case strings.Contains(cmd, "/etc/subuid"):
				return "uosserver:589824:65536\n", nil
			case strings.Contains(cmd, "/var/lib/systemd/linger"):
				return "uosserver\n", nil
			case strings.Contains(cmd, "sudo -n -u uosserver") && !strings.Contains(cmd, "cd /") && !strings.Contains(cmd, "-i"):
				// Simulates AlmaLinux 10.2 /var/home/core permission denied when running sudo -n -u without cd / or -i
				return "", fmt.Errorf("sudo: cannot chdir to /var/home/core: Permission denied")
			case strings.Contains(cmd, "cd /") && strings.Contains(cmd, "uosserver") && strings.Contains(cmd, "podman ps"):
				return `[{"Id": "417811aa3ef5", "Names": ["uosserver"], "Image": "docker.io/library/uosserver:c9603dec9010", "State": "running", "Status": "Up 3 days (healthy)"}]`, nil
			default:
				return `[]`, nil
			}
		},
	}

	mgr := NewManager(runner, ssh.Config{Host: "ping.leo-banjo.ts.net", Port: 22, User: "core"})
	users, err := mgr.ProbeUnprivilegedUsers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 1 || users[0].User != "uosserver" || len(users[0].Containers) != 1 {
		t.Fatalf("expected uosserver with 1 container from AlmaLinux sudo login shell, got: %+v", users)
	}
	if users[0].Containers[0].Name != "uosserver" || users[0].Containers[0].Image != "docker.io/library/uosserver:c9603dec9010" {
		t.Errorf("expected full container metadata from podman ps json, got: %+v", users[0].Containers[0])
	}
}



