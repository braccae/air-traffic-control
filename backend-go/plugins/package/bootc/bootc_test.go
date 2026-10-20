package bootc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
)

// fakeRunner records the command it received and returns canned output.
type fakeRunner struct {
	lastCmd string
	output  string
	err     error
}

func (f *fakeRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	f.lastCmd = cmd
	return f.output, f.err
}

// realHostStatusJSON is the verbatim `sudo -n bootc status --json` document
// captured from a real bootc host (core@likes-it-cold). It has a booted
// deployment, a rollback deployment and no staged deployment.
const realHostStatusJSON = `{"apiVersion":"org.containers.bootc/v1","kind":"BootcHost","metadata":{"name":"host"},"spec":{"bootOrder":"default","image":{"image":"ghcr.io/braccae/alma:dockerhost","transport":"registry"}},"status":{"booted":{"cachedUpdate":null,"composefs":null,"downloadOnly":false,"image":{"architecture":"amd64","image":{"image":"ghcr.io/braccae/alma:dockerhost","transport":"registry"},"imageDigest":"sha256:3d1444a737945b249cf7829adf6847cc929341445947dd14845dabaaea7974d9","timestamp":"2026-10-15T04:11:47Z","version":"dockerhost"},"incompatible":false,"ostree":{"checksum":"45415542e656ef50b5800571bcf9c1ff510a27d549e192d690e42ae699a102a9","deploySerial":0,"stateroot":"default"},"pinned":false,"softRebootCapable":true,"store":"ostreeContainer"},"rollback":{"cachedUpdate":{"architecture":"amd64","image":{"image":"ghcr.io/braccae/alma:dockerhost","transport":"registry"},"imageDigest":"sha256:3d1444a737945b249cf7829adf6847cc929341445947dd14845dabaaea7974d9","timestamp":"2026-10-15T04:11:47Z","version":"dockerhost"},"composefs":null,"downloadOnly":false,"image":{"architecture":"amd64","image":{"image":"ghcr.io/braccae/alma:dockerhost","transport":"registry"},"imageDigest":"sha256:9cb3a3941ee4fd1cf3b5cb837ddf9cb6009435dfd9c770b9a95e98ed0c4c38d3","timestamp":"2026-10-13T12:19:22Z","version":"dockerhost"},"incompatible":false,"ostree":{"checksum":"c1e5c41cb713a38e397e79a5f3a8ab48cf7160f53a7ceac93ea27217ee67984c","deploySerial":0,"stateroot":"default"},"pinned":false,"softRebootCapable":true,"store":"ostreeContainer"},"rollbackQueued":false,"staged":null,"type":"bootcHost","usrOverlay":null}}`

// stagedStatusJSON is a faithful document where a new deployment is staged for
// the next boot (staged non-null).
const stagedStatusJSON = `{"apiVersion":"org.containers.bootc/v1","kind":"BootcHost","metadata":{"name":"host"},"status":{"booted":{"image":{"image":{"image":"quay.io/centos-bootc/centos-bootc:stream9","transport":"registry"},"imageDigest":"sha256:aaaa","version":"9.20240315.0"}},"staged":{"image":{"image":{"image":"quay.io/centos-bootc/centos-bootc:stream9","transport":"registry"},"imageDigest":"sha256:bbbb","version":"9.20240320.0"}},"rollback":null}}`

// assertField extracts a string field from the Read result map.
func assertField(t *testing.T, got any, key, want string) {
	t.Helper()
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("Read returned %T, want map[string]any", got)
	}
	v, ok := m[key]
	if !ok {
		t.Fatalf("result missing key %q", key)
	}
	s, _ := v.(string)
	if s != want {
		t.Errorf("%s = %q, want %q", key, s, want)
	}
}

func TestRead_RealHost_BootedAndFallback(t *testing.T) {
	r := &fakeRunner{output: realHostStatusJSON}
	got, err := New().Read(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	assertField(t, got, "currentImage", "ghcr.io/braccae/alma:dockerhost")
	assertField(t, got, "currentImageVersion", "dockerhost")
	assertField(t, got, "currentImageDigest", "sha256:3d1444a737945b249cf7829adf6847cc929341445947dd14845dabaaea7974d9")
	// No deployment is staged on this host.
	assertField(t, got, "stagedImage", "none")
	// The previous deployment is available as a rollback/fallback.
	assertField(t, got, "fallbackImage", "ghcr.io/braccae/alma:dockerhost")
	assertField(t, got, "fallbackImageDigest", "sha256:9cb3a3941ee4fd1cf3b5cb837ddf9cb6009435dfd9c770b9a95e98ed0c4c38d3")

	// The plugin must query real bootc status, with a passwordless-sudo path.
	if !strings.Contains(r.lastCmd, "bootc status --json") {
		t.Errorf("Read did not run `bootc status --json`; ran %q", r.lastCmd)
	}
}

func TestRead_StagedImageReported(t *testing.T) {
	r := &fakeRunner{output: stagedStatusJSON}
	got, err := New().Read(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	assertField(t, got, "currentImage", "quay.io/centos-bootc/centos-bootc:stream9")
	assertField(t, got, "stagedImage", "quay.io/centos-bootc/centos-bootc:stream9")
	assertField(t, got, "stagedImageVersion", "9.20240320.0")
	assertField(t, got, "stagedImageDigest", "sha256:bbbb")
	assertField(t, got, "fallbackImage", "none")
}

func TestRead_NoStagedOrRollback_ReportsNone(t *testing.T) {
	r := &fakeRunner{output: `{"kind":"BootcHost","status":{"booted":{"image":{"image":{"image":"quay.io/org/os:latest"}}},"staged":null,"rollback":null}}`}
	got, err := New().Read(context.Background(), r, plugin.HostFacts{})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	assertField(t, got, "currentImage", "quay.io/org/os:latest")
	assertField(t, got, "stagedImage", "none")
	assertField(t, got, "fallbackImage", "none")
}

func TestRead_RunnerNil_Errors(t *testing.T) {
	_, err := New().Read(context.Background(), nil, plugin.HostFacts{})
	if err == nil {
		t.Fatal("expected error for nil runner, got nil")
	}
}

func TestRead_CommandError_Propagates(t *testing.T) {
	r := &fakeRunner{err: errors.New("ssh command execution failed")}
	_, err := New().Read(context.Background(), r, plugin.HostFacts{})
	if err == nil {
		t.Fatal("expected error when bootc status fails, got nil")
	}
}

func TestRead_MalformedJSON_Errors(t *testing.T) {
	r := &fakeRunner{output: "not json at all"}
	_, err := New().Read(context.Background(), r, plugin.HostFacts{})
	if err == nil {
		t.Fatal("expected error for malformed bootc status JSON, got nil")
	}
}

func TestRead_EmptyOutput_Errors(t *testing.T) {
	r := &fakeRunner{output: "   "}
	_, err := New().Read(context.Background(), r, plugin.HostFacts{})
	if err == nil {
		t.Fatal("expected error for empty bootc status output, got nil")
	}
}

// TestPlugin_ImplementsReader asserts the optional Reader interface is satisfied.
func TestPlugin_ImplementsReader(t *testing.T) {
	var _ plugin.Reader = New()
}

// TestProbe_Unavailable is a regression guard for the reporting path.
func TestProbe_Unavailable(t *testing.T) {
	r := &fakeRunner{err: fmt.Errorf("command not found")}
	d, err := New().Probe(context.Background(), r, plugin.HostFacts{})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if d.Detected || d.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE detection, got %+v", d)
	}
}
