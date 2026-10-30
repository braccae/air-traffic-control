// Package secrets resolves Ansible Vault–encrypted secrets from the
// coordinator's SQLite vault_variables table into usable SSH credentials.
//
// This is the single runtime credential path for every host communication
// layer (command runner, interactive PTY, connection plugin). A node or tag
// chunk references a secret by NAME (vaultSecretId); this package is the only
// place that decrypts it. Plaintext never leaves the process and is never
// written to disk or exposed over the API.
//
// Zero-Mock Invariant: if a referenced vault variable is missing, cannot be
// decrypted, or the vault password source is unconfigured, resolution fails
// loudly. There is no silent fallback to an unauthenticated connection.
package secrets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/vaultapi"
)

// Kind classifies a resolved secret by its content, never by its name. A vault
// variable named ssh_key_webserver may legitimately hold a password, and a
// generically named variable may hold an SSH private key.
type Kind string

const (
	// KindPrivateKey is an OpenSSH/PEM private key block.
	KindPrivateKey Kind = "private_key"
	// KindPassword is any other secret (password, token, passphrase).
	KindPassword Kind = "password"
)

// DefaultCacheTTL bounds how long a decrypted value stays in memory before the
// store is re-read. Cache entries are also invalidated whenever the variable's
// updated_at changes, so an edit in the UI takes effect immediately.
const DefaultCacheTTL = 60 * time.Second

// Secret is a resolved credential. Value holds plaintext and must never be
// logged or serialized.
type Secret struct {
	Name  string
	Kind  Kind
	Value string
}

// Masked returns the audit-log-safe representation of a secret.
func (s Secret) Masked() string { return vaultapi.MaskVaultValue(s.Name) }

// NotFoundError reports a reference to a vault variable that does not exist.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf(
		"vault secret %q not found — create it under Settings → Variables → SSH Vault Secrets",
		e.Name,
	)
}

// Store is the subset of hostcache.Store required for secret resolution.
type Store interface {
	GetVaultVariable(name string) (hostcache.VaultVariable, bool, error)
	RecordVaultAuditEvent(ev hostcache.VaultAuditEvent) error
}

// DecryptFunc decrypts an encrypted vault block to plaintext. It exists so unit
// tests can run without the ansible-vault binary.
type DecryptFunc func(ctx context.Context, repoRoot, vaultBlock string) (string, error)

// Options configures a Resolver.
type Options struct {
	Store     Store
	RepoRoot  string
	CacheTTL  time.Duration
	Actor     string
	Host      string
	Decrypt   DecryptFunc
	Now       func() time.Time
}

// Resolver turns vault variable names into plaintext credentials.
type Resolver struct {
	store    Store
	repoRoot string
	ttl      time.Duration
	actor    string
	host     string
	decrypt  DecryptFunc
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	secret    Secret
	updatedAt int64
	storedAt  time.Time
}

// NewResolver builds a Resolver. A nil Store yields a resolver that reports
// every lookup as unconfigured.
func NewResolver(opts Options) *Resolver {
	ttl := opts.CacheTTL
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	actor := opts.Actor
	if actor == "" {
		actor = os.Getenv("USER")
		if actor == "" {
			actor = "atc-coordinator"
		}
	}
	host := opts.Host
	if host == "" {
		host = "n/a"
	}
	decrypt := opts.Decrypt
	if decrypt == nil {
		decrypt = func(ctx context.Context, repoRoot, vaultBlock string) (string, error) {
			return vaultapi.DecryptVaultString(ctx, repoRoot, vaultBlock, vaultapi.DecryptOptions{})
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Resolver{
		store:    opts.Store,
		repoRoot: opts.RepoRoot,
		ttl:      ttl,
		actor:    actor,
		host:     host,
		decrypt:  decrypt,
		now:      now,
		cache:    make(map[string]cacheEntry),
	}
}

// Classify inspects plaintext and reports whether it is a private key.
func Classify(value string) Kind {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "-----BEGIN ") && strings.Contains(trimmed, "PRIVATE KEY-----") {
		return KindPrivateKey
	}
	return KindPassword
}

// normalizeValue preserves the exact secret bytes except for private keys,
// where a missing trailing newline breaks OpenSSH parsing.
func normalizeValue(kind Kind, value string) string {
	if kind == KindPrivateKey && !strings.HasSuffix(value, "\n") {
		return value + "\n"
	}
	return value
}

// Exists reports whether a named vault variable is present.
func (r *Resolver) Exists(name string) (bool, error) {
	if r == nil || r.store == nil {
		return false, errors.New("vault secret store is not configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, nil
	}
	_, found, err := r.store.GetVaultVariable(name)
	return found, err
}

// Invalidate drops the cached plaintext for a variable.
func (r *Resolver) Invalidate(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.cache, strings.TrimSpace(name))
	r.mu.Unlock()
}

// Resolve decrypts a named vault variable into a credential.
func (r *Resolver) Resolve(ctx context.Context, name string) (Secret, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Secret{}, errors.New("vault secret name is required")
	}
	if r == nil || r.store == nil {
		return Secret{}, errors.New("vault secret store is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	v, found, err := r.store.GetVaultVariable(name)
	if err != nil {
		return Secret{}, fmt.Errorf("read vault secret %q: %w", name, err)
	}
	if !found {
		return Secret{}, &NotFoundError{Name: name}
	}

	r.mu.Lock()
	entry, cached := r.cache[name]
	r.mu.Unlock()
	if cached && entry.updatedAt == v.UpdatedAt && r.now().Sub(entry.storedAt) < r.ttl {
		return entry.secret, nil
	}

	plaintext, err := r.decrypt(ctx, r.repoRoot, v.VaultBlock)
	if err != nil {
		if strings.Contains(err.Error(), "PASSWORD_REQUIRED") {
			return Secret{}, fmt.Errorf(
				"cannot decrypt vault secret %q: no vault password source configured (set vault_password_file in ansible/ansible.cfg, or ANSIBLE_VAULT_PASSWORD/_FILE)",
				name,
			)
		}
		return Secret{}, fmt.Errorf("decrypt vault secret %q: %w", name, err)
	}
	kind := Classify(plaintext)
	secret := Secret{Name: name, Kind: kind, Value: normalizeValue(kind, plaintext)}

	r.mu.Lock()
	r.cache[name] = cacheEntry{secret: secret, updatedAt: v.UpdatedAt, storedAt: r.now()}
	r.mu.Unlock()

	r.audit(name, "resolve-ssh-credential")
	return secret, nil
}

func (r *Resolver) audit(variableName, action string) {
	if r == nil || r.store == nil {
		return
	}
	_ = r.store.RecordVaultAuditEvent(hostcache.VaultAuditEvent{
		ID:             newEventID(),
		Timestamp:      r.now().UnixMilli(),
		VariableName:   variableName,
		Actor:          r.actor,
		Host:           r.host,
		Action:         action,
		PasswordSource: vaultapi.ResolveVaultPasswordSource(r.repoRoot, vaultapi.ResolveOptions{}).Source,
	})
}

func newEventID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return "evt-" + hex.EncodeToString(buf)
}

// ---------------------------------------------------------------------------
// Process-wide default resolver
// ---------------------------------------------------------------------------

var (
	defaultMu       sync.RWMutex
	defaultResolver *Resolver
)

// SetDefault installs the process-wide resolver used by the SSH transport,
// connection plugin, and probe endpoints. Pass nil to clear (tests).
func SetDefault(r *Resolver) {
	defaultMu.Lock()
	defaultResolver = r
	defaultMu.Unlock()
}

// Default returns the process-wide resolver, which may be nil.
func Default() *Resolver {
	defaultMu.RLock()
	r := defaultResolver
	defaultMu.RUnlock()
	return r
}

// Resolve uses the process-wide resolver.
func Resolve(ctx context.Context, name string) (Secret, error) {
	r := Default()
	if r == nil {
		return Secret{}, errors.New("vault secret resolver is not configured")
	}
	return r.Resolve(ctx, name)
}

// Exists reports whether name is present in the process-wide resolver's store.
func Exists(name string) (bool, error) {
	r := Default()
	if r == nil {
		return false, errors.New("vault secret resolver is not configured")
	}
	return r.Exists(name)
}

// Invalidate drops the cached plaintext for name in the process-wide resolver.
func Invalidate(name string) {
	if r := Default(); r != nil {
		r.Invalidate(name)
	}
}

// ValidateRef rejects references to vault variables that do not exist. Manual
// secret naming means a typo is otherwise invisible until the first connection
// attempt, so API handlers call this before persisting a reference.
//
// When no resolver is installed (unit tests, read-only mode) validation is
// skipped rather than blocking writes.
func ValidateRef(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if Default() == nil {
		return nil
	}
	exists, err := Exists(name)
	if err != nil {
		return err
	}
	if !exists {
		return &NotFoundError{Name: name}
	}
	return nil
}
