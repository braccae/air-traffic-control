// Package ansiblebin locates Ansible executables, preferring the coordinator's
// managed virtualenv and falling back to PATH.
//
// A virtualenv is not relocatable: its scripts carry an absolute interpreter
// path in the shebang and bin/python is an absolute symlink. When data/ansible-venv
// is copied from another machine, every file still exists, so a plain os.Stat
// check says "installed" while exec fails with ENOENT ("no such file or
// directory"). Runnable closes that gap by verifying the interpreter as well.
package ansiblebin

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const shebangProbeBytes = 512

// Runnable reports whether path is an executable file that the kernel can
// actually exec. For script wrappers it also verifies the shebang interpreter
// exists, so a venv built on a different host is reported as unusable rather
// than silently preferred over a working PATH install.
func Runnable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return false
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	head := make([]byte, shebangProbeBytes)
	n, _ := f.Read(head)
	head = head[:n]
	if !bytes.HasPrefix(head, []byte("#!")) {
		return true
	}

	line := string(head[2:])
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}

	interp := fields[0]
	if interp == "/usr/bin/env" {
		if len(fields) < 2 {
			return false
		}
		_, err := exec.LookPath(fields[1])
		return err == nil
	}
	interpInfo, err := os.Stat(interp)
	return err == nil && !interpInfo.IsDir()
}

// Resolve returns the runnable binary named name inside venvBinDir, falling
// back to PATH. It returns "" when neither location has a usable executable.
func Resolve(venvBinDir, name string) string {
	if venvBinDir != "" {
		candidate := filepath.Join(venvBinDir, name)
		if Runnable(candidate) {
			return candidate
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}
