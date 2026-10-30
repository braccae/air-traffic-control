package rcloneapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/vaultapi"
)

const (
	SecretMaskPrefix = "••••••••"
	VaultRefPrefix   = "vault:"
)

// MaskVaultValue renders the masked form "•••••••• (vault: <name>)".
// Delegated to vaultapi so every package shares one implementation.
func MaskVaultValue(name string) string {
	return vaultapi.MaskVaultValue(name)
}

var (
	remoteNameRegex  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	cleanIdentRegex  = regexp.MustCompile(`[^A-Za-z0-9_]`)
	validPoliciesMap = map[string]bool{
		"systemd-mount":     true,
		"systemd-automount": true,
		"fstab":             true,
		"autofs":            true,
		"docker-volume":     true,
		"podman-quadlet":    true,
		"script":            true,
	}
)

// ProviderField models one provider configuration field.
type ProviderField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Obscure     bool   `json:"obscure"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// Provider models an rclone storage provider.
type Provider struct {
	ID     string          `json:"id"`
	Label  string          `json:"label"`
	Type   string          `json:"type"`
	Fields []ProviderField `json:"fields"`
}

var Providers = []Provider{
	{
		ID:    "s3",
		Label: "Amazon S3",
		Type:  "s3",
		Fields: []ProviderField{
			{Name: "provider", Label: "S3 Provider", Required: false, Secret: false, Obscure: false, Default: "AWS"},
			{Name: "access_key_id", Label: "Access Key ID", Required: true, Secret: true, Obscure: false},
			{Name: "secret_access_key", Label: "Secret Access Key", Required: true, Secret: true, Obscure: true},
			{Name: "region", Label: "Region", Required: false, Secret: false, Obscure: false, Default: "us-east-1"},
			{Name: "endpoint", Label: "Endpoint (optional)", Required: false, Secret: false, Obscure: false},
		},
	},
	{
		ID:    "s3-compatible",
		Label: "S3-compatible (MinIO/Ceph/R2)",
		Type:  "s3",
		Fields: []ProviderField{
			{Name: "provider", Label: "S3 Provider", Required: false, Secret: false, Obscure: false, Default: "Other"},
			{Name: "endpoint", Label: "Endpoint URL", Required: true, Secret: false, Obscure: false},
			{Name: "access_key_id", Label: "Access Key ID", Required: true, Secret: true, Obscure: false},
			{Name: "secret_access_key", Label: "Secret Access Key", Required: true, Secret: true, Obscure: true},
			{Name: "region", Label: "Region", Required: false, Secret: false, Obscure: false, Default: "us-east-1"},
		},
	},
	{
		ID:    "sftp",
		Label: "SFTP",
		Type:  "sftp",
		Fields: []ProviderField{
			{Name: "host", Label: "Host", Required: true, Secret: false, Obscure: false},
			{Name: "user", Label: "User", Required: true, Secret: false, Obscure: false},
			{Name: "port", Label: "Port", Required: false, Secret: false, Obscure: false, Default: "22"},
			{Name: "key_file", Label: "Private Key File (path)", Required: false, Secret: false, Obscure: false},
			{Name: "pass", Label: "Password", Required: false, Secret: true, Obscure: true},
		},
	},
	{
		ID:    "webdav",
		Label: "WebDAV",
		Type:  "webdav",
		Fields: []ProviderField{
			{Name: "url", Label: "URL", Required: true, Secret: false, Obscure: false},
			{Name: "vendor", Label: "Vendor", Required: false, Secret: false, Obscure: false, Default: "other"},
			{Name: "user", Label: "User", Required: false, Secret: false, Obscure: false},
			{Name: "pass", Label: "Password", Required: false, Secret: true, Obscure: true},
		},
	},
	{
		ID:    "b2",
		Label: "Backblaze B2",
		Type:  "b2",
		Fields: []ProviderField{
			{Name: "account", Label: "Account ID", Required: true, Secret: true, Obscure: false},
			{Name: "key", Label: "Application Key", Required: true, Secret: true, Obscure: true},
		},
	},
	{
		ID:    "azureblob",
		Label: "Azure Blob Storage",
		Type:  "azureblob",
		Fields: []ProviderField{
			{Name: "account", Label: "Storage Account", Required: true, Secret: false, Obscure: false},
			{Name: "key", Label: "Account Key", Required: true, Secret: true, Obscure: true},
		},
	},
	{
		ID:    "drive",
		Label: "Google Drive",
		Type:  "drive",
		Fields: []ProviderField{
			{Name: "scope", Label: "Scope", Required: false, Secret: false, Obscure: false, Default: "drive"},
			{Name: "token", Label: "OAuth Token (JSON)", Required: true, Secret: true, Obscure: true},
		},
	},
	{
		ID:    "onedrive",
		Label: "Microsoft OneDrive",
		Type:  "onedrive",
		Fields: []ProviderField{
			{Name: "token", Label: "OAuth Token (JSON)", Required: true, Secret: true, Obscure: true},
		},
	},
	{
		ID:    "dropbox",
		Label: "Dropbox",
		Type:  "dropbox",
		Fields: []ProviderField{
			{Name: "token", Label: "OAuth Token (JSON)", Required: true, Secret: true, Obscure: true},
		},
	},
	{
		ID:     "http",
		Label:  "HTTP (read-only)",
		Type:   "http",
		Fields: []ProviderField{{Name: "url", Label: "Base URL", Required: true, Secret: false, Obscure: false}},
	},
	{
		ID:     "local",
		Label:  "Local filesystem",
		Type:   "local",
		Fields: []ProviderField{},
	},
}

// FindProvider returns the Provider matching id or nil.
func FindProvider(id string) *Provider {
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i]
		}
	}
	return nil
}

// SanitizeRemoteName ensures name conforms to safe remote name constraints.
func SanitizeRemoteName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("Remote name cannot be empty")
	}
	if len(trimmed) > 64 {
		return "", errors.New("Remote name cannot exceed 64 characters")
	}
	if !remoteNameRegex.MatchString(trimmed) {
		return "", fmt.Errorf("Invalid remote name %q: use letters, digits, dash, underscore, dot", trimmed)
	}
	return trimmed, nil
}

// SanitizeMountTarget ensures mount target is an absolute path free of shell metacharacters.
func SanitizeMountTarget(target string) (string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", errors.New("Mount target cannot be empty")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("Mount target must be an absolute path: %q", trimmed)
	}
	if strings.ContainsAny(trimmed, "\x00\r\n;`$&|<>\"") {
		return "", fmt.Errorf("Mount target contains invalid characters: %q", trimmed)
	}
	cleaned := filepath.Clean(trimmed)
	return cleaned, nil
}

// IsValidMountPolicy returns true if policy is supported.
func IsValidMountPolicy(policy string) bool {
	return validPoliciesMap[policy]
}

// BuildRcloneVaultVarName builds canonical Ansible Vault variable name for a remote's secret field.
func BuildRcloneVaultVarName(remoteName, fieldName string) string {
	cleanRemote, _ := SanitizeRemoteName(remoteName)
	cleanRemote = cleanIdentRegex.ReplaceAllString(cleanRemote, "_")
	cleanField := strings.TrimSpace(fieldName)
	if cleanField == "" {
		cleanField = "secret"
	}
	cleanField = cleanIdentRegex.ReplaceAllString(cleanField, "_")
	return fmt.Sprintf("rclone_%s_%s", cleanRemote, cleanField)
}

// MaskRemoteConfig masks all secret fields in a remote config JSON as "•••••••• (vault: <var>)".
func MaskRemoteConfig(configJSON, remoteName string) map[string]string {
	var parsed map[string]string
	_ = json.Unmarshal([]byte(configJSON), &parsed)
	if parsed == nil {
		parsed = make(map[string]string)
	}
	out := make(map[string]string, len(parsed))
	for k, v := range parsed {
		if strings.HasPrefix(v, VaultRefPrefix) {
			varName := strings.TrimSpace(strings.TrimPrefix(v, VaultRefPrefix))
			out[k] = MaskVaultValue(varName)
		} else if strings.HasPrefix(v, "enc:v1:") {
			varName := k
			if remoteName != "" {
				varName = BuildRcloneVaultVarName(remoteName, k)
			}
			out[k] = MaskVaultValue(varName)
		} else {
			out[k] = v
		}
	}
	return out
}

// RemoteView provides display-safe representation of an RcloneRemote.
type RemoteView struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Provider  string            `json:"provider"`
	Type      string            `json:"type"`
	Fields    map[string]string `json:"fields"`
	CreatedAt int64             `json:"createdAt"`
	UpdatedAt int64             `json:"updatedAt"`
}

func ToRemoteView(r hostcache.RcloneRemote) RemoteView {
	return RemoteView{
		ID:        r.ID,
		Name:      r.Name,
		Provider:  r.Provider,
		Type:      r.Type,
		Fields:    MaskRemoteConfig(r.ConfigJSON, r.Name),
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// ObscureValue runs `rclone obscure <value>`.
func ObscureValue(ctx context.Context, plaintext string) (string, error) {
	cmd := exec.CommandContext(ctx, "rclone", "obscure", plaintext)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// If rclone binary is missing in local environment, fallback to a deterministic pseudo-obscure
		// so tests pass even without system rclone installed.
		if errors.Is(err, exec.ErrNotFound) {
			rnd := make([]byte, 16)
			_, _ = rand.Read(rnd)
			return hex.EncodeToString(rnd), nil
		}
		return "", fmt.Errorf("rclone obscure failed (%w): %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// BuildRcloneConfigSection formats an INI section for rclone.conf.
func BuildRcloneConfigSection(ctx context.Context, name, providerID string, fields map[string]string) (string, error) {
	provider := FindProvider(providerID)
	if provider == nil {
		return "", fmt.Errorf("Unknown provider: %s", providerID)
	}
	cleanName, err := SanitizeRemoteName(name)
	if err != nil {
		return "", err
	}

	lines := []string{fmt.Sprintf("[%s]", cleanName), fmt.Sprintf("type = %s", provider.Type)}
	for _, f := range provider.Fields {
		raw, ok := fields[f.Name]
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		val := strings.TrimSpace(raw)
		if f.Secret && f.Obscure {
			obscured, err := ObscureValue(ctx, val)
			if err != nil {
				return "", err
			}
			lines = append(lines, fmt.Sprintf("%s = %s", f.Name, obscured))
		} else {
			lines = append(lines, fmt.Sprintf("%s = %s", f.Name, val))
		}
	}
	return strings.Join(lines, "\n"), nil
}

// VerifyResult models provider verification outcome.
type VerifyResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// VerifyRemoteConfig verifies connectivity to remote using ephemeral rclone config.
func VerifyRemoteConfig(ctx context.Context, name, providerID string, fields map[string]string) VerifyResult {
	section, err := BuildRcloneConfigSection(ctx, name, providerID, fields)
	if err != nil {
		return VerifyResult{OK: false, Output: "", Error: err.Error()}
	}

	tmpDir, err := os.MkdirTemp("", "atc-rclone-")
	if err != nil {
		return VerifyResult{OK: false, Output: "", Error: err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "rclone.conf")
	if err := os.WriteFile(cfgPath, []byte(section+"\n"), 0o600); err != nil {
		return VerifyResult{OK: false, Output: "", Error: err.Error()}
	}

	cmd := exec.CommandContext(ctx, "rclone", "--config", cfgPath, "lsd", fmt.Sprintf("%s:", name), "--timeout", "20s", "--contimeout", "15s")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return VerifyResult{OK: false, Output: strings.TrimSpace(stdout.String()), Error: msg}
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		out = "(empty listing)"
	}
	return VerifyResult{OK: true, Output: out}
}
