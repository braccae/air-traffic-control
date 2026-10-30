package secrets

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
)

// fakeStore is an in-memory Store for resolver tests.
type fakeStore struct {
	mu       sync.Mutex
	vars     map[string]hostcache.VaultVariable
	audits   []hostcache.VaultAuditEvent
	getErr   error
	auditErr error
	getCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{vars: make(map[string]hostcache.VaultVariable)}
}

func (f *fakeStore) GetVaultVariable(name string) (hostcache.VaultVariable, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getErr != nil {
		return hostcache.VaultVariable{}, false, f.getErr
	}
	v, ok := f.vars[name]
	return v, ok, nil
}

func (f *fakeStore) RecordVaultAuditEvent(ev hostcache.VaultAuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, ev)
	return f.auditErr
}

func (f *fakeStore) set(name, block string, updatedAt int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vars[name] = hostcache.VaultVariable{Name: name, VaultBlock: block, UpdatedAt: updatedAt}
}

const (
	testPrivKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Kind
	}{
		{"openssh key", testPrivKey, KindPrivateKey},
		{"pem rsa key", "-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----", KindPrivateKey},
		{"password", "hunter2", KindPassword},
		{"empty", "", KindPassword},
		{"public key is not private", "ssh-ed25519 AAAAC3Nz user@host", KindPassword},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.in); got != tc.want {
				t.Errorf("Classify() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveClassifiesAndNormalizesPrivateKey(t *testing.T) {
	store := newFakeStore()
	store.set("ssh_key", "block", 10)
	decryptCalls := 0
	r := NewResolver(Options{
		Store:    store,
		RepoRoot: "/repo",
		Decrypt: func(context.Context, string, string) (string, error) {
			decryptCalls++
			return testPrivKey, nil // no trailing newline
		},
	})

	sec, err := r.Resolve(context.Background(), "ssh_key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sec.Kind != KindPrivateKey {
		t.Errorf("Kind = %q, want %q", sec.Kind, KindPrivateKey)
	}
	if !strings.HasSuffix(sec.Value, "-----END OPENSSH PRIVATE KEY-----\n") {
		t.Errorf("private key should end with a newline, got %q", sec.Value)
	}
	if decryptCalls != 1 {
		t.Errorf("decryptCalls = %d, want 1", decryptCalls)
	}
	if len(store.audits) != 1 || store.audits[0].VariableName != "ssh_key" {
		t.Errorf("expected one audit event for ssh_key, got %+v", store.audits)
	}
}

func TestResolvePasswordKeepsExactValue(t *testing.T) {
	store := newFakeStore()
	store.set("samba_pw", "block", 1)
	r := NewResolver(Options{
		Store: store,
		Decrypt: func(context.Context, string, string) (string, error) {
			return "  spaced password  ", nil
		},
	})
	sec, err := r.Resolve(context.Background(), "samba_pw")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sec.Kind != KindPassword {
		t.Errorf("Kind = %q, want %q", sec.Kind, KindPassword)
	}
	if sec.Value != "  spaced password  " {
		t.Errorf("password value = %q, want exact plaintext", sec.Value)
	}
}

func TestResolveNotFound(t *testing.T) {
	r := NewResolver(Options{Store: newFakeStore()})
	_, err := r.Resolve(context.Background(), "missing")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
	if nf.Name != "missing" {
		t.Errorf("NotFoundError.Name = %q, want missing", nf.Name)
	}
}

func TestResolveEmptyName(t *testing.T) {
	r := NewResolver(Options{Store: newFakeStore()})
	if _, err := r.Resolve(context.Background(), "   "); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestResolveCachesUntilUpdatedAtChanges(t *testing.T) {
	store := newFakeStore()
	store.set("ssh_key", "v1", 100)
	value := "password-one"
	decryptCalls := 0
	r := NewResolver(Options{
		Store: store,
		Decrypt: func(context.Context, string, string) (string, error) {
			decryptCalls++
			return value, nil
		},
	})

	if _, err := r.Resolve(context.Background(), "ssh_key"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := r.Resolve(context.Background(), "ssh_key"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decryptCalls != 1 {
		t.Errorf("decryptCalls = %d after two resolves, want 1 (cached)", decryptCalls)
	}

	// Updating the row (new UpdatedAt) must force a re-decrypt.
	value = "password-two"
	store.set("ssh_key", "v2", 200)
	sec, err := r.Resolve(context.Background(), "ssh_key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decryptCalls != 2 {
		t.Errorf("decryptCalls = %d after update, want 2", decryptCalls)
	}
	if sec.Value != "password-two" {
		t.Errorf("value = %q, want updated value", sec.Value)
	}
}

func TestResolveInvalidate(t *testing.T) {
	store := newFakeStore()
	store.set("ssh_key", "v1", 100)
	value := "one"
	decryptCalls := 0
	r := NewResolver(Options{
		Store: store,
		Decrypt: func(context.Context, string, string) (string, error) {
			decryptCalls++
			return value, nil
		},
	})
	_, _ = r.Resolve(context.Background(), "ssh_key")
	value = "two"
	r.Invalidate("ssh_key")
	sec, err := r.Resolve(context.Background(), "ssh_key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decryptCalls != 2 {
		t.Errorf("decryptCalls = %d after invalidate, want 2", decryptCalls)
	}
	if sec.Value != "two" {
		t.Errorf("value = %q, want two", sec.Value)
	}
}

func TestResolveCacheExpiresByTTL(t *testing.T) {
	store := newFakeStore()
	store.set("ssh_key", "v1", 100)
	now := time.Unix(0, 0)
	decryptCalls := 0
	r := NewResolver(Options{
		Store:    store,
		CacheTTL: 10 * time.Second,
		Now:      func() time.Time { return now },
		Decrypt: func(context.Context, string, string) (string, error) {
			decryptCalls++
			return "pw", nil
		},
	})
	_, _ = r.Resolve(context.Background(), "ssh_key")
	now = now.Add(11 * time.Second)
	_, _ = r.Resolve(context.Background(), "ssh_key")
	if decryptCalls != 2 {
		t.Errorf("decryptCalls = %d after TTL expiry, want 2", decryptCalls)
	}
}

func TestResolveMissingVaultPasswordFailsLoudly(t *testing.T) {
	store := newFakeStore()
	store.set("ssh_key", "block", 1)
	r := NewResolver(Options{
		Store: store,
		Decrypt: func(context.Context, string, string) (string, error) {
			return "", errors.New("exit status 5: PASSWORD_REQUIRED")
		},
	})
	_, err := r.Resolve(context.Background(), "ssh_key")
	if err == nil {
		t.Fatal("expected an error when no vault password source is configured")
	}
	if !strings.Contains(err.Error(), "no vault password source configured") {
		t.Errorf("error should explain the missing password source, got: %v", err)
	}
}

func TestResolveStoreReadError(t *testing.T) {
	store := newFakeStore()
	store.getErr = errors.New("db is locked")
	r := NewResolver(Options{Store: store, Decrypt: func(context.Context, string, string) (string, error) {
		return "pw", nil
	}})
	_, err := r.Resolve(context.Background(), "ssh_key")
	if err == nil || !strings.Contains(err.Error(), "db is locked") {
		t.Fatalf("expected wrapped store error, got %v", err)
	}
}

func TestNilResolver(t *testing.T) {
	var r *Resolver
	if _, err := r.Resolve(context.Background(), "x"); err == nil {
		t.Error("nil resolver should error on Resolve")
	}
	r.Invalidate("x") // must not panic
}

func TestDefaultResolverLifecycle(t *testing.T) {
	SetDefault(nil)
	defer SetDefault(nil)
	if Default() != nil {
		t.Fatal("expected nil default")
	}
	if _, err := Resolve(context.Background(), "x"); err == nil {
		t.Error("expected error when no default resolver is configured")
	}

	store := newFakeStore()
	store.set("ssh_key", "block", 1)
	SetDefault(NewResolver(Options{
		Store: store,
		Decrypt: func(context.Context, string, string) (string, error) {
			return "pw", nil
		},
	}))
	sec, err := Resolve(context.Background(), "ssh_key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sec.Value != "pw" {
		t.Errorf("value = %q, want pw", sec.Value)
	}
	PrefixTestHooks(t)
}

// PrefixTestHooks keeps the global default clean for other packages' tests.
func PrefixTestHooks(t *testing.T) {
	t.Helper()
	SetDefault(nil)
}
