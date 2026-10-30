package tailscale

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/secrets"
)

type fakeVaultStore struct {
	vars map[string]hostcache.VaultVariable
}

func (f *fakeVaultStore) GetVaultVariable(name string) (hostcache.VaultVariable, bool, error) {
	v, ok := f.vars[name]
	return v, ok, nil
}

func (f *fakeVaultStore) RecordVaultAuditEvent(hostcache.VaultAuditEvent) error { return nil }

// generatePrivateKeyPEM returns a real OpenSSH private key so the resolver
// classifies the secret as a private key and ssh.ParsePrivateKey succeeds.
func generatePrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

// withVault installs a process-wide resolver whose single variable decrypts to
// value. It returns the resolver so callers can assert on invalidation.
func withVault(t *testing.T, name, value string) *secrets.Resolver {
	t.Helper()
	store := &fakeVaultStore{vars: map[string]hostcache.VaultVariable{
		name: {Name: name, VaultBlock: "ENCRYPTED", UpdatedAt: 1},
	}}
	r := secrets.NewResolver(secrets.Options{
		Store:    store,
		RepoRoot: t.TempDir(),
		Decrypt: func(context.Context, string, string) (string, error) {
			return value, nil
		},
	})
	secrets.SetDefault(r)
	t.Cleanup(func() { secrets.SetDefault(nil) })
	return r
}

// A node that references a vault variable must actually offer that credential
// over the tsnet transport. Before this was fixed the transport only looked at
// private_key/password overrides and the ssh-agent, so a node with a perfectly
// valid vaultSecretId connected with zero auth methods and the server replied
// "attempted methods [none]".
func TestAuthMethodsForHostUsesVaultSecret(t *testing.T) {
	key := generatePrivateKeyPEM(t)
	withVault(t, "ssh_key", key)

	methods, fingerprint, err := authMethodsForHost(plugin.HostFacts{
		Overrides: map[string]string{"vaultSecretId": "ssh_key"},
	})
	if err != nil {
		t.Fatalf("authMethodsForHost: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("got %d auth methods, want 1 (the vault key)", len(methods))
	}
	if !strings.HasPrefix(fingerprint, "vault:ssh_key:") {
		t.Fatalf("fingerprint = %q, want the vault-key identity", fingerprint)
	}
}

// The fingerprint must change when the secret value changes, otherwise the
// plugin would reuse a pooled connection authenticated with the old key.
func TestAuthMethodsFingerprintTracksSecretValue(t *testing.T) {
	first := generatePrivateKeyPEM(t)
	withVault(t, "ssh_key", first)

	_, fp1, err := authMethodsForHost(plugin.HostFacts{
		Overrides: map[string]string{"vaultSecretId": "ssh_key"},
	})
	if err != nil {
		t.Fatalf("first authMethodsForHost: %v", err)
	}

	// Simulate an edited vault variable: new ciphertext, new updated_at.
	secrets.SetDefault(secrets.NewResolver(secrets.Options{
		Store: &fakeVaultStore{vars: map[string]hostcache.VaultVariable{
			"ssh_key": {Name: "ssh_key", VaultBlock: "ENCRYPTED-2", UpdatedAt: 2},
		}},
		RepoRoot: t.TempDir(),
		Decrypt: func(context.Context, string, string) (string, error) {
			return generatePrivateKeyPEM(t), nil
		},
	}))

	_, fp2, err := authMethodsForHost(plugin.HostFacts{
		Overrides: map[string]string{"vaultSecretId": "ssh_key"},
	})
	if err != nil {
		t.Fatalf("second authMethodsForHost: %v", err)
	}
	if fp1 == fp2 {
		t.Fatalf("fingerprint unchanged after the secret was replaced: %q", fp1)
	}
}

// A dangling reference must fail before dialing, naming the variable, instead
// of silently offering no credentials.
func TestAuthMethodsForHostFailsOnMissingVaultSecret(t *testing.T) {
	withVault(t, "ssh_key", generatePrivateKeyPEM(t))

	_, _, err := authMethodsForHost(plugin.HostFacts{
		Overrides: map[string]string{"vaultSecretId": "does_not_exist"},
	})
	if err == nil {
		t.Fatal("expected an error for a missing vault secret")
	}
	if !strings.Contains(err.Error(), "does_not_exist") {
		t.Fatalf("error %q does not name the missing variable", err)
	}
}

func TestAuthMethodsForHostWithoutCredentialsIsEmpty(t *testing.T) {
	secrets.SetDefault(nil)
	t.Setenv("SSH_AUTH_SOCK", "")

	methods, fingerprint, err := authMethodsForHost(plugin.HostFacts{
		Overrides: map[string]string{"authMethod": "ssh-ed25519 (ssh-agent)"},
	})
	if err != nil {
		t.Fatalf("authMethodsForHost: %v", err)
	}
	if fingerprint != "none" {
		t.Fatalf("fingerprint = %q, want none", fingerprint)
	}
	// A missing agent socket on the test host means nothing to offer; if one
	// exists, exactly the agent method is offered.
	if len(methods) > 1 {
		t.Fatalf("got %d auth methods, want at most the ssh-agent", len(methods))
	}
}
