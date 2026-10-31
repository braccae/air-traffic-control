package ansibleapi

import (
	"os"
	"path/filepath"
	"testing"
)

func writeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, "bin", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
	return path
}

// A venv copied from another host keeps absolute interpreter paths, so its
// wrappers exist but exec fails with ENOENT. venvHealthy must reject it so
// EnsureAnsibleVenv rebuilds instead of trusting the files.
func TestVenvHealthyRejectsStaleInterpreter(t *testing.T) {
	venvDir := t.TempDir()
	writeExecutable(t, venvDir, "python", "#!/var/home/core/uv/python/bin/python3.14\n")
	writeExecutable(t, venvDir, "ansible", "#!/var/home/core/Projects/atc/data/ansible-venv/bin/python\n")

	if venvHealthy(venvDir) {
		t.Fatal("venvHealthy accepted a venv whose interpreter does not exist on this host")
	}
}

func TestVenvHealthyAcceptsWorkingVenv(t *testing.T) {
	venvDir := t.TempDir()
	writeExecutable(t, venvDir, "python", "#!/bin/sh\nexit 0\n")
	writeExecutable(t, venvDir, "ansible", "#!/bin/sh\necho \"ansible [core 2.21.5]\"\n")

	if !venvHealthy(venvDir) {
		t.Fatal("venvHealthy rejected a working venv")
	}
}

func TestVenvHealthyRejectsMissingBinary(t *testing.T) {
	venvDir := t.TempDir()
	writeExecutable(t, venvDir, "python", "#!/bin/sh\nexit 0\n")

	if venvHealthy(venvDir) {
		t.Fatal("venvHealthy accepted a venv without an ansible launcher")
	}
}

func TestResolveAnsibleBinaryFallsBackWhenVenvIsStale(t *testing.T) {
	venvDir := t.TempDir()
	t.Setenv("ANSIBLE_VENV_PATH", venvDir)
	writeExecutable(t, venvDir, "ansible-vault", "#!/var/home/core/venv/bin/python\n")

	got := ResolveAnsibleBinary(t.TempDir(), "ansible-vault")
	if got == filepath.Join(venvDir, "bin", "ansible-vault") {
		t.Fatalf("ResolveAnsibleBinary selected the broken venv wrapper: %q", got)
	}
}
