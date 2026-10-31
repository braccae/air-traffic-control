package ansiblebin

import (
	"os"
	"path/filepath"
	"testing"
)

func writeScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestRunnableRejectsDanglingShebang(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ansible-vault")
	writeScript(t, script, "#!/nonexistent/venv/bin/python\nprint('hi')\n")

	if Runnable(script) {
		t.Fatalf("expected a script with a missing interpreter to be unusable")
	}
}

func TestRunnableAcceptsLiveShebang(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ansible-vault")
	writeScript(t, script, "#!/bin/sh\necho hi\n")

	if !Runnable(script) {
		t.Fatalf("expected a script with a live interpreter to be usable")
	}
}

func TestRunnableRejectsNonExecutableAndDirectory(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if Runnable(plain) {
		t.Fatalf("expected a non-executable file to be unusable")
	}
	if Runnable(dir) {
		t.Fatalf("expected a directory to be unusable")
	}
	if Runnable(filepath.Join(dir, "missing")) {
		t.Fatalf("expected a missing file to be unusable")
	}
}

func TestResolveSkipsBrokenVenvBinary(t *testing.T) {
	dir := t.TempDir()
	venvBin := filepath.Join(dir, "venv", "bin")
	writeScript(t, filepath.Join(venvBin, "ansible-galaxy"), "#!/var/home/core/venv/bin/python\n")

	if got := Resolve(venvBin, "ansible-galaxy"); got == filepath.Join(venvBin, "ansible-galaxy") {
		t.Fatalf("expected Resolve to skip the broken venv wrapper, got %q", got)
	}
}
