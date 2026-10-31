package plugin_test

import (
	"os"
	"path/filepath"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
)

// Running the daemon from backend-go/ used to resolve backend-go/data/
// hosts-cache.sqlite, silently forking the inventory. Resolution must prefer
// the repository-root database even when the working directory is the Go module.
func TestResolveDBPathPrefersRepoRootOverModuleLocalDB(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "backend-go"),
		filepath.Join(root, "data"),
		filepath.Join(root, "backend-go", "data"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	rootDB := filepath.Join(root, "data", "hosts-cache.sqlite")
	localDB := filepath.Join(root, "backend-go", "data", "hosts-cache.sqlite")
	for _, db := range []string{rootDB, localDB, filepath.Join(root, "backend-go", "go.mod")} {
		if err := os.WriteFile(db, []byte(""), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", db, err)
		}
	}

	t.Setenv("ATC_DB_PATH", "")
	t.Chdir(filepath.Join(root, "backend-go"))

	if got := plugin.ResolveDBPath(""); got != rootDB {
		t.Fatalf("ResolveDBPath() = %q, want the repo-root inventory %q", got, rootDB)
	}
}

func TestResolveDBPathHonorsExplicitAndEnv(t *testing.T) {
	t.Setenv("ATC_DB_PATH", "")
	if got := plugin.ResolveDBPath("/tmp/atc-explicit.sqlite"); got != "/tmp/atc-explicit.sqlite" {
		t.Fatalf("explicit path not honored: %q", got)
	}
	t.Setenv("ATC_DB_PATH", "/tmp/atc-env.sqlite")
	if got := plugin.ResolveDBPath(""); got != "/tmp/atc-env.sqlite" {
		t.Fatalf("ATC_DB_PATH not honored: %q", got)
	}
}
