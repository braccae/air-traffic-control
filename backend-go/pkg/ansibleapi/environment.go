package ansibleapi

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/ansiblebin"
)

// RequiredVenvPackages lists packages that must be present in the virtualenv.
var RequiredVenvPackages = []string{
	"ansible-core",
	"pywinrm",
	"requests",
	"jmespath",
	"cryptography",
	"netaddr",
}

// RequiredGalaxyCollections lists Ansible Galaxy collections that must be present
// (e.g. community.general provides the run0 and doas become plugins).
var RequiredGalaxyCollections = []string{
	"community.general",
	"ansible.posix",
}

// EnvironmentInfo describes the Ansible runtime environment.
type EnvironmentInfo struct {
	Success          bool     `json:"success"`
	VenvPath         string   `json:"venvPath"`
	UvPath           string   `json:"uvPath"`
	Installed        bool     `json:"installed"`
	Packages         []string `json:"packages"`
	PythonVersion    string   `json:"pythonVersion,omitempty"`
	AnsibleVersion   string   `json:"ansibleVersion,omitempty"`
	Collections      []string `json:"collections,omitempty"`
	AnsibleBinary    string   `json:"ansibleBinary,omitempty"`
	PlaybookBinary   string   `json:"ansiblePlaybookBinary,omitempty"`
	VaultBinary      string   `json:"ansibleVaultBinary,omitempty"`
}

// GetAnsibleVenvPath returns the path to the persistent Ansible virtualenv.
func GetAnsibleVenvPath(repoRoot string) string {
	if custom := os.Getenv("ANSIBLE_VENV_PATH"); custom != "" {
		return custom
	}
	return filepath.Join(repoRoot, "data", "ansible-venv")
}

// FindUv locates the uv binary on disk without downloading.
func FindUv(repoRoot string) string {
	// 1. Check data/bin/uv
	localUv := filepath.Join(repoRoot, "data", "bin", "uv")
	if fi, err := os.Stat(localUv); err == nil && !fi.IsDir() {
		return localUv
	}

	// 2. Look in PATH
	if p, err := exec.LookPath("uv"); err == nil {
		return p
	}

	// 3. Fallback to standard locations
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "uv"),
		filepath.Join(home, ".cargo", "bin", "uv"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// ResolveAnsibleBinary returns the runnable path for an Ansible executable,
// preferring the managed virtualenv and falling back to PATH. It returns ""
// when neither location has a usable binary.
func ResolveAnsibleBinary(repoRoot, name string) string {
	venvBinDir := filepath.Join(GetAnsibleVenvPath(repoRoot), "bin")
	return ansiblebin.Resolve(venvBinDir, name)
}

// venvHealthy reports whether the virtualenv is usable on this host. Presence
// is not enough: a venv copied from another machine keeps absolute interpreter
// paths, so the wrappers exist but exec fails with ENOENT. An --version probe
// is the only reliable check, and it also catches missing or broken packages.
func venvHealthy(venvDir string) bool {
	pythonBin := filepath.Join(venvDir, "bin", "python")
	if !ansiblebin.Runnable(pythonBin) {
		return false
	}
	ansibleBin := filepath.Join(venvDir, "bin", "ansible")
	if !ansiblebin.Runnable(ansibleBin) {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, ansibleBin, "--version").Run() == nil
}

// EnsureAnsibleVenv checks if the virtualenv exists and is runnable; if missing
// or corrupt it rebuilds it in place with uv, then ensures required Galaxy
// collections (such as community.general for run0) are installed.
func EnsureAnsibleVenv(repoRoot string) (string, error) {
	venvDir := GetAnsibleVenvPath(repoRoot)
	if venvHealthy(venvDir) {
		ensureRequiredCollections(repoRoot)
		return venvDir, nil
	}

	uvPath := FindUv(repoRoot)
	if uvPath == "" {
		return "", fmt.Errorf("ansible-venv at %s is unusable and no uv binary was found to rebuild it", venvDir)
	}

	// --clear rebuilds in place, which is what heals a stale venv: the old
	// bin/python symlink and script shebangs are replaced with this host's paths.
	cmd := exec.Command(uvPath, "venv", "--clear", venvDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("uv venv failed (%w): %s", err, string(out))
	}

	// Install required packages
	pythonBin := filepath.Join(venvDir, "bin", "python")
	args := append([]string{"pip", "install", "--python", pythonBin}, RequiredVenvPackages...)
	installCmd := exec.Command(uvPath, args...)
	if out, err := installCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("uv pip install failed (%w): %s", err, string(out))
	}

	ensureRequiredCollections(repoRoot)
	return venvDir, nil
}

func ensureRequiredCollections(repoRoot string) {
	installed, _ := ListGalaxyCollections(repoRoot)
	have := make(map[string]bool, len(installed))
	for _, c := range installed {
		have[c.Name] = true
	}
	for _, req := range RequiredGalaxyCollections {
		if !have[req] {
			_, _ = InstallGalaxyCollection(repoRoot, req)
		}
	}
}

// InspectEnvironment probes the environment for JSON reporting.
func InspectEnvironment(repoRoot string) EnvironmentInfo {
	venvPath := GetAnsibleVenvPath(repoRoot)
	binDir := filepath.Join(venvPath, "bin")
	ansibleBinary := filepath.Join(binDir, "ansible")
	playbookBinary := filepath.Join(binDir, "ansible-playbook")
	vaultBinary := filepath.Join(binDir, "ansible-vault")
	pythonBinary := filepath.Join(binDir, "python")

	// Runnable, not merely present: a venv copied from another host keeps
	// absolute interpreter paths that no longer resolve on this machine.
	installed := false
	if ansiblebin.Runnable(ansibleBinary) {
		installed = true
	} else if ansiblebin.Runnable(playbookBinary) {
		installed = true
	} else if p, err := exec.LookPath("ansible-playbook"); err == nil {
		installed = true
		playbookBinary = p
		ansibleBinary = p
	}

	uvPath := FindUv(repoRoot)

	info := EnvironmentInfo{
		Success:        true,
		VenvPath:       venvPath,
		UvPath:         uvPath,
		Installed:      installed,
		Packages:       RequiredVenvPackages,
		AnsibleBinary:  ansibleBinary,
		PlaybookBinary: playbookBinary,
		VaultBinary:    vaultBinary,
	}

	// Python version probe
	pyToRun := pythonBinary
	if _, err := os.Stat(pyToRun); err != nil {
		if p, err := exec.LookPath("python3"); err == nil {
			pyToRun = p
		}
	}
	if out, err := exec.Command(pyToRun, "--version").CombinedOutput(); err == nil {
		info.PythonVersion = strings.TrimSpace(string(out))
	}

	// Ansible version probe
	ansToRun := playbookBinary
	if !ansiblebin.Runnable(ansToRun) {
		ansToRun, _ = exec.LookPath("ansible-playbook")
	}
	if ansToRun != "" {
		if out, err := exec.Command(ansToRun, "--version").CombinedOutput(); err == nil {
			firstLine := strings.Split(string(out), "\n")[0]
			info.AnsibleVersion = strings.TrimSpace(firstLine)
		}
	}

	// Galaxy collections list
	galaxyToRun := filepath.Join(binDir, "ansible-galaxy")
	if !ansiblebin.Runnable(galaxyToRun) {
		galaxyToRun, _ = exec.LookPath("ansible-galaxy")
	}
	if galaxyToRun != "" {
		if out, err := exec.Command(galaxyToRun, "collection", "list").CombinedOutput(); err == nil {
			var collections []string
			for _, line := range strings.Split(string(out), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.Contains(trimmed, ".") && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "Collection") {
					parts := strings.Fields(trimmed)
					if len(parts) >= 1 && strings.Contains(parts[0], ".") {
						collections = append(collections, parts[0])
					}
				}
			}
			info.Collections = collections
		}
	}

	return info
}
