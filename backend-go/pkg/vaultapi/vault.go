package vaultapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"air-traffic-control/backend/pkg/ansibleapi"
	"air-traffic-control/backend/pkg/hostcache"
)

const (
	VaultValueMask = "••••••••"
)

var vaultVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// IsValidVaultVariableName validates that name conforms to Ansible identifier syntax.
func IsValidVaultVariableName(name string) bool {
	return vaultVarNamePattern.MatchString(name)
}

// MaskVaultValue renders the masked form "•••••••• (vault: <name>)".
func MaskVaultValue(name string) string {
	return fmt.Sprintf("%s (vault: %s)", VaultValueMask, name)
}

// PasswordSource represents the tier that resolved the master vault password.
type PasswordSource struct {
	Source      string `json:"source"`
	HasPassword bool   `json:"hasPassword"`
	Password    string `json:"-"`
	FilePath    string `json:"filePath,omitempty"`
}

// ResolveOptions configures password source resolution.
type ResolveOptions struct {
	VaultPasswordFile string
	AnsibleCfgPath    string
	Env               map[string]string
}

// ResolveVaultPasswordSource resolves the vault password in strict precedence:
// 1. vault_password_file (explicit option or parsed from ansible.cfg)
// 2. ANSIBLE_VAULT_PASSWORD or ANSIBLE_VAULT_PASSWORD_FILE environment variable
// 3. Interactive ask_password prompt fallback
func ResolveVaultPasswordSource(repoRoot string, opts ResolveOptions) PasswordSource {
	getEnv := func(k string) string {
		if opts.Env != nil {
			if v, ok := opts.Env[k]; ok {
				return v
			}
		}
		return os.Getenv(k)
	}

	// Tier 1: Check vaultPasswordFile
	candidate := opts.VaultPasswordFile
	if candidate == "" {
		cfgPath := opts.AnsibleCfgPath
		if cfgPath == "" && repoRoot != "" {
			cfgPath = filepath.Join(repoRoot, "ansible", "ansible.cfg")
		}
		if cfgPath != "" {
			if cfg, _, err := ansibleapi.LoadAnsibleConfig(cfgPath); err == nil && cfg.VaultPasswordFile != "" {
				candidate = cfg.VaultPasswordFile
			}
		}
	}

	if candidate != "" {
		resolved := candidate
		if !filepath.IsAbs(resolved) {
			if repoRoot != "" {
				resolved = filepath.Join(repoRoot, candidate)
				if _, err := os.Stat(resolved); err != nil {
					ansibleSub := filepath.Join(repoRoot, "ansible", candidate)
					if _, errSub := os.Stat(ansibleSub); errSub == nil {
						resolved = ansibleSub
					}
				}
			}
		}
		if data, err := os.ReadFile(resolved); err == nil {
			pass := strings.TrimSpace(string(data))
			return PasswordSource{
				Source:      "file",
				HasPassword: true,
				Password:    pass,
				FilePath:    resolved,
			}
		}
	}

	// Tier 2: Check environment variables
	if pass := getEnv("ANSIBLE_VAULT_PASSWORD"); pass != "" {
		return PasswordSource{
			Source:      "env",
			HasPassword: true,
			Password:    pass,
		}
	}

	if passFile := getEnv("ANSIBLE_VAULT_PASSWORD_FILE"); passFile != "" {
		resolved := passFile
		if !filepath.IsAbs(resolved) && repoRoot != "" {
			resolved = filepath.Join(repoRoot, passFile)
		}
		if data, err := os.ReadFile(resolved); err == nil {
			return PasswordSource{
				Source:      "file",
				HasPassword: true,
				Password:    strings.TrimSpace(string(data)),
				FilePath:    resolved,
			}
		}
	}

	// Tier 3: Fall back to ask_password
	return PasswordSource{
		Source:      "ask_password",
		HasPassword: false,
	}
}

// GetVaultExecutionEnv returns clean environment variables with conflicting
// Ansible vault variables stripped out to prevent exit code 5.
func GetVaultExecutionEnv(repoRoot string, baseEnv map[string]string) []string {
	envMap := make(map[string]string)
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	if baseEnv != nil {
		for k, v := range baseEnv {
			envMap[k] = v
		}
	}

	// Strip conflicting Ansible vault environment variables
	delete(envMap, "ANSIBLE_VAULT_PASSWORD_FILE")
	delete(envMap, "ANSIBLE_VAULT_IDENTITY_LIST")
	delete(envMap, "DEFAULT_VAULT_PASSWORD_FILE")
	delete(envMap, "ANSIBLE_CONFIG")

	if repoRoot != "" {
		venvDir := ansibleapi.GetAnsibleVenvPath(repoRoot)
		binDir := filepath.Join(venvDir, "bin")
		if oldPath, ok := envMap["PATH"]; ok && oldPath != "" {
			envMap["PATH"] = binDir + string(os.PathListSeparator) + oldPath
		} else {
			envMap["PATH"] = binDir
		}
		envMap["VIRTUAL_ENV"] = venvDir
	}

	res := make([]string, 0, len(envMap))
	for k, v := range envMap {
		res = append(res, k+"="+v)
	}
	return res
}

// ExtractRawVaultPayload extracts raw $ANSIBLE_VAULT payload lines, stripping YAML tags.
func ExtractRawVaultPayload(input string) (string, error) {
	vaultIdx := strings.Index(input, "$ANSIBLE_VAULT;")
	if vaultIdx == -1 {
		return "", errors.New("INVALID_VAULT_PAYLOAD: Missing $ANSIBLE_VAULT header")
	}

	remaining := input[vaultIdx:]
	lines := strings.Split(remaining, "\n")
	var resultLines []string

	hexPattern := regexp.MustCompile(`^[0-9a-fA-F]+$`)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i == 0 {
			resultLines = append(resultLines, trimmed)
			continue
		}
		if hexPattern.MatchString(trimmed) {
			resultLines = append(resultLines, trimmed)
		} else {
			break
		}
	}

	return strings.Join(resultLines, "\n"), nil
}

// FormatVaultEntry renders one stored entry as a YAML `name: !vault |\n  <payload>`.
func FormatVaultEntry(name, payload string) string {
	lines := strings.Split(payload, "\n")
	var indented []string
	for _, l := range lines {
		indented = append(indented, "  "+l)
	}
	return fmt.Sprintf("%s: !vault |\n%s", name, strings.Join(indented, "\n"))
}

// ExtractVaultVariableName extracts the variable name from a YAML `name: !vault |` block.
func ExtractVaultVariableName(vaultBlock string) string {
	re := regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_.\-]+)\s*:\s*!vault\s*\|`)
	m := re.FindStringSubmatch(vaultBlock)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// EncryptOptions configures encryption.
type EncryptOptions struct {
	Password       string
	PasswordFile   string
	AnsibleCfgPath string
	Env            map[string]string
}

// EncryptVaultString encrypts plaintext using ansible-vault encrypt_string.
func EncryptVaultString(ctx context.Context, repoRoot, plaintext, varName string, opts EncryptOptions) (string, error) {
	password := opts.Password
	if password == "" {
		res := ResolveVaultPasswordSource(repoRoot, ResolveOptions{
			VaultPasswordFile: opts.PasswordFile,
			AnsibleCfgPath:    opts.AnsibleCfgPath,
			Env:               opts.Env,
		})
		if res.HasPassword {
			password = res.Password
		}
	}
	if password == "" {
		return "", errors.New("PASSWORD_REQUIRED: Vault password source required for encryption")
	}

	if varName == "" {
		varName = "secret"
	}

	rnd := make([]byte, 8)
	_, _ = rand.Read(rnd)
	id := hex.EncodeToString(rnd)
	tempPassFile := filepath.Join(os.TempDir(), fmt.Sprintf(".vault-pass-%s", id))
	if err := os.WriteFile(tempPassFile, []byte(password), 0o600); err != nil {
		return "", err
	}
	defer os.Remove(tempPassFile)

	vaultBin := "ansible-vault"
	if repoRoot != "" {
		venvBin := filepath.Join(ansibleapi.GetAnsibleVenvPath(repoRoot), "bin", "ansible-vault")
		if _, err := os.Stat(venvBin); err == nil {
			vaultBin = venvBin
		}
	}

	cmd := exec.CommandContext(ctx, vaultBin, "encrypt_string", "--vault-password-file", tempPassFile, "--stdin-name", varName)
	cmd.Env = GetVaultExecutionEnv(repoRoot, opts.Env)
	cmd.Stdin = strings.NewReader(plaintext)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ansible-vault encrypt_string failed (%w): %s", err, strings.TrimSpace(stderr.String()))
	}

	outStr := stdout.String()
	vaultIdx := strings.Index(outStr, "!vault |")
	if vaultIdx == -1 {
		return "", fmt.Errorf("failed to parse encrypted vault block: %s", outStr)
	}

	before := outStr[:vaultIdx]
	lastNewline := strings.LastIndex(before, "\n")
	start := 0
	if lastNewline != -1 {
		start = lastNewline + 1
	}
	return strings.TrimSpace(outStr[start:]), nil
}

// DecryptOptions configures decryption.
type DecryptOptions struct {
	Password       string
	PasswordFile   string
	AnsibleCfgPath string
	Env            map[string]string
}

// DecryptVaultString decrypts an encrypted vault block to plaintext.
func DecryptVaultString(ctx context.Context, repoRoot, vaultPayload string, opts DecryptOptions) (string, error) {
	rawPayload, err := ExtractRawVaultPayload(vaultPayload)
	if err != nil {
		return "", err
	}

	password := opts.Password
	if password == "" {
		res := ResolveVaultPasswordSource(repoRoot, ResolveOptions{
			VaultPasswordFile: opts.PasswordFile,
			AnsibleCfgPath:    opts.AnsibleCfgPath,
			Env:               opts.Env,
		})
		if res.HasPassword {
			password = res.Password
		}
	}
	if password == "" {
		return "", errors.New("PASSWORD_REQUIRED: Vault password source required for decryption")
	}

	rnd := make([]byte, 8)
	_, _ = rand.Read(rnd)
	id := hex.EncodeToString(rnd)
	tempPassFile := filepath.Join(os.TempDir(), fmt.Sprintf(".vault-pass-%s", id))
	if err := os.WriteFile(tempPassFile, []byte(password), 0o600); err != nil {
		return "", err
	}
	defer os.Remove(tempPassFile)

	tempPayloadFile := filepath.Join(os.TempDir(), fmt.Sprintf(".vault-payload-%s", id))
	if err := os.WriteFile(tempPayloadFile, []byte(rawPayload), 0o600); err != nil {
		return "", err
	}
	defer os.Remove(tempPayloadFile)

	vaultBin := "ansible-vault"
	if repoRoot != "" {
		venvBin := filepath.Join(ansibleapi.GetAnsibleVenvPath(repoRoot), "bin", "ansible-vault")
		if _, err := os.Stat(venvBin); err == nil {
			vaultBin = venvBin
		}
	}

	cmd := exec.CommandContext(ctx, vaultBin, "decrypt", "--vault-password-file", tempPassFile, tempPayloadFile)
	cmd.Env = GetVaultExecutionEnv(repoRoot, opts.Env)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ansible-vault decrypt failed (%w): %s", err, strings.TrimSpace(stderr.String()))
	}

	decryptedBytes, err := os.ReadFile(tempPayloadFile)
	if err != nil {
		return "", err
	}
	return string(decryptedBytes), nil
}

// MaterializedFile holds the path and cleanup func for extra-vars.
type MaterializedFile struct {
	FilePath string
	Cleanup  func()
}

// MaterializeVaultVarsFile dumps all vault variables to an ephemeral 0600 file.
// Returns nil if store is empty or has no variables.
func MaterializeVaultVarsFile(store *hostcache.Store) (*MaterializedFile, error) {
	if store == nil {
		return nil, nil
	}
	vars, err := store.ListVaultVariables()
	if err != nil || len(vars) == 0 {
		return nil, err
	}

	var entries []string
	for _, v := range vars {
		raw, err := ExtractRawVaultPayload(v.VaultBlock)
		if err != nil {
			continue
		}
		entries = append(entries, FormatVaultEntry(v.Name, raw))
	}
	if len(entries) == 0 {
		return nil, nil
	}

	tmpDir, err := os.MkdirTemp("", "atc-vault-vars-")
	if err != nil {
		return nil, err
	}
	filePath := filepath.Join(tmpDir, "vault.yml")
	header := "# Generated by Air Traffic Control from the SQLite vault store.\n# Every value below is ansible-vault AES256 ciphertext.\n"
	content := fmt.Sprintf("---\n%s%s\n", header, strings.Join(entries, "\n"))

	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, err
	}

	return &MaterializedFile{
		FilePath: filePath,
		Cleanup: func() {
			_ = os.RemoveAll(tmpDir)
		},
	}, nil
}
