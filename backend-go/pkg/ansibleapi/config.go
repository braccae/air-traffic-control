package ansibleapi

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ConfigSettings represents parsed ansible.cfg values.
type ConfigSettings struct {
	StdoutCallback    string `json:"stdoutCallback"`
	Forks             int    `json:"forks"`
	Timeout           int    `json:"timeout"`
	HostKeyChecking   bool   `json:"hostKeyChecking"`
	Pipelining        bool   `json:"pipelining"`
	BecomeMethod      string `json:"becomeMethod"`
	VaultPasswordFile string `json:"vaultPasswordFile,omitempty"`
}

// DefaultConfigSettings returns baseline ansible.cfg configuration.
func DefaultConfigSettings() ConfigSettings {
	return ConfigSettings{
		StdoutCallback:  "json",
		Forks:           10,
		Timeout:         30,
		HostKeyChecking: false,
		Pipelining:      true,
		BecomeMethod:    "run0",
	}
}

var invalidVaultChars = regexp.MustCompile(`[\r\n\x00-\x1F\x7F]`)

// ValidateVaultPasswordFile guards against INI injection via newlines and control characters.
func ValidateVaultPasswordFile(p string) bool {
	if p == "" {
		return true
	}
	return !invalidVaultChars.MatchString(p)
}

func parseBool(val string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	switch v {
	case "true", "yes", "1", "on":
		return true
	case "false", "no", "0", "off":
		return false
	default:
		return fallback
	}
}

func parseInt(val string, fallback, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return fallback
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func parseStdoutCallback(val string) string {
	v := strings.ToLower(strings.TrimSpace(val))
	switch v {
	case "json", "yaml", "default":
		return v
	default:
		return "json"
	}
}

func parseBecomeMethod(val string) string {
	v := strings.ToLower(strings.TrimSpace(val))
	switch v {
	case "run0", "community.general.run0":
		return "run0"
	case "sudo", "ansible.builtin.sudo":
		return "sudo"
	case "doas", "community.general.doas":
		return "doas"
	default:
		return "run0"
	}
}

// ParseAnsibleCfg parses INI content into ConfigSettings.
func ParseAnsibleCfg(iniContent string) ConfigSettings {
	settings := DefaultConfigSettings()
	if strings.TrimSpace(iniContent) == "" {
		return settings
	}

	lines := strings.Split(iniContent, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		eqIdx := strings.Index(line, "=")
		if eqIdx == -1 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(line[:eqIdx]))
		rawValue := line[eqIdx+1:]
		val := rawValue
		for _, commentChar := range []string{"#", ";"} {
			if idx := strings.Index(val, commentChar); idx != -1 {
				val = val[:idx]
			}
		}
		val = strings.TrimSpace(val)

		switch key {
		case "stdout_callback":
			settings.StdoutCallback = parseStdoutCallback(val)
		case "forks":
			settings.Forks = parseInt(val, 10, 1, 500)
		case "timeout":
			settings.Timeout = parseInt(val, 30, 1, 600)
		case "host_key_checking":
			settings.HostKeyChecking = parseBool(val, false)
		case "pipelining":
			settings.Pipelining = parseBool(val, true)
		case "become_method":
			settings.BecomeMethod = parseBecomeMethod(val)
		case "vault_password_file":
			if ValidateVaultPasswordFile(val) {
				settings.VaultPasswordFile = val
			}
		}
	}

	return settings
}

func boolToStr(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// SerializeAnsibleCfg generates compliant INI format preserving baseContent when available.
func SerializeAnsibleCfg(settings ConfigSettings, baseContent string) (string, error) {
	if settings.VaultPasswordFile != "" && !ValidateVaultPasswordFile(settings.VaultPasswordFile) {
		return "", fmt.Errorf("invalid vaultPasswordFile: newlines and control characters are prohibited to prevent INI injection")
	}

	if strings.TrimSpace(baseContent) == "" {
		lines := []string{
			"[defaults]",
			fmt.Sprintf("stdout_callback = %s", settings.StdoutCallback),
			fmt.Sprintf("forks = %d", settings.Forks),
			fmt.Sprintf("timeout = %d", settings.Timeout),
			fmt.Sprintf("host_key_checking = %s", boolToStr(settings.HostKeyChecking)),
			fmt.Sprintf("pipelining = %s", boolToStr(settings.Pipelining)),
		}
		if settings.VaultPasswordFile != "" {
			lines = append(lines, fmt.Sprintf("vault_password_file = %s", settings.VaultPasswordFile))
		}
		lines = append(lines,
			"",
			"[privilege_escalation]",
			fmt.Sprintf("become_method = %s", settings.BecomeMethod),
			"",
		)
		return strings.Join(lines, "\n"), nil
	}

	lines := strings.Split(baseContent, "\n")
	var updatedLines []string
	currentSection := ""
	handledKeys := make(map[string]bool)
	seenSections := make(map[string]bool)

	appendDefaults := func() {
		if !handledKeys["stdout_callback"] {
			updatedLines = append(updatedLines, fmt.Sprintf("stdout_callback = %s", settings.StdoutCallback))
			handledKeys["stdout_callback"] = true
		}
		if !handledKeys["forks"] {
			updatedLines = append(updatedLines, fmt.Sprintf("forks = %d", settings.Forks))
			handledKeys["forks"] = true
		}
		if !handledKeys["timeout"] {
			updatedLines = append(updatedLines, fmt.Sprintf("timeout = %d", settings.Timeout))
			handledKeys["timeout"] = true
		}
		if !handledKeys["host_key_checking"] {
			updatedLines = append(updatedLines, fmt.Sprintf("host_key_checking = %s", boolToStr(settings.HostKeyChecking)))
			handledKeys["host_key_checking"] = true
		}
		if !handledKeys["pipelining"] {
			updatedLines = append(updatedLines, fmt.Sprintf("pipelining = %s", boolToStr(settings.Pipelining)))
			handledKeys["pipelining"] = true
		}
		if settings.VaultPasswordFile != "" && !handledKeys["vault_password_file"] {
			updatedLines = append(updatedLines, fmt.Sprintf("vault_password_file = %s", settings.VaultPasswordFile))
			handledKeys["vault_password_file"] = true
		}
	}

	appendPrivEsc := func() {
		if !handledKeys["become_method"] {
			updatedLines = append(updatedLines, fmt.Sprintf("become_method = %s", settings.BecomeMethod))
			handledKeys["become_method"] = true
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if currentSection == "defaults" {
				appendDefaults()
			} else if currentSection == "privilege_escalation" {
				appendPrivEsc()
			}
			currentSection = strings.ToLower(strings.Trim(trimmed, "[]"))
			seenSections[currentSection] = true
			updatedLines = append(updatedLines, line)
			continue
		}

		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") || trimmed == "" {
			updatedLines = append(updatedLines, line)
			continue
		}

		eqIdx := strings.Index(trimmed, "=")
		if eqIdx != -1 {
			key := strings.ToLower(strings.TrimSpace(trimmed[:eqIdx]))
			switch key {
			case "stdout_callback":
				updatedLines = append(updatedLines, fmt.Sprintf("stdout_callback = %s", settings.StdoutCallback))
				handledKeys["stdout_callback"] = true
				continue
			case "forks":
				updatedLines = append(updatedLines, fmt.Sprintf("forks = %d", settings.Forks))
				handledKeys["forks"] = true
				continue
			case "timeout":
				updatedLines = append(updatedLines, fmt.Sprintf("timeout = %d", settings.Timeout))
				handledKeys["timeout"] = true
				continue
			case "host_key_checking":
				updatedLines = append(updatedLines, fmt.Sprintf("host_key_checking = %s", boolToStr(settings.HostKeyChecking)))
				handledKeys["host_key_checking"] = true
				continue
			case "pipelining":
				updatedLines = append(updatedLines, fmt.Sprintf("pipelining = %s", boolToStr(settings.Pipelining)))
				handledKeys["pipelining"] = true
				continue
			case "become_method":
				updatedLines = append(updatedLines, fmt.Sprintf("become_method = %s", settings.BecomeMethod))
				handledKeys["become_method"] = true
				continue
			case "vault_password_file":
				if settings.VaultPasswordFile != "" {
					updatedLines = append(updatedLines, fmt.Sprintf("vault_password_file = %s", settings.VaultPasswordFile))
				}
				handledKeys["vault_password_file"] = true
				continue
			}
		}

		updatedLines = append(updatedLines, line)
	}

	if currentSection == "defaults" {
		appendDefaults()
	} else if currentSection == "privilege_escalation" {
		appendPrivEsc()
	}

	if !seenSections["defaults"] {
		updatedLines = append(updatedLines, "", "[defaults]")
		appendDefaults()
	}
	if !seenSections["privilege_escalation"] {
		updatedLines = append(updatedLines, "", "[privilege_escalation]")
		appendPrivEsc()
	}

	return strings.Join(updatedLines, "\n"), nil
}

// LoadAnsibleConfig reads and parses ansible.cfg, initializing default if absent.
func LoadAnsibleConfig(cfgPath string) (ConfigSettings, string, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			defaults := DefaultConfigSettings()
			serialized, serErr := SerializeAnsibleCfg(defaults, "")
			if serErr == nil {
				_ = os.MkdirAll(filepath.Dir(cfgPath), 0o755)
				_ = os.WriteFile(cfgPath, []byte(serialized), 0o644)
			}
			return defaults, serialized, nil
		}
		return DefaultConfigSettings(), "", err
	}
	raw := string(data)
	return ParseAnsibleCfg(raw), raw, nil
}

// SaveAnsibleConfig writes the updated configuration atomically.
func SaveAnsibleConfig(cfgPath string, settings ConfigSettings) (string, error) {
	var baseContent string
	if data, err := os.ReadFile(cfgPath); err == nil {
		baseContent = string(data)
	}

	serialized, err := SerializeAnsibleCfg(settings, baseContent)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	tmpFile := filepath.Join(dir, fmt.Sprintf(".tmp_cfg_%d", os.Getpid()))
	if err := os.WriteFile(tmpFile, []byte(serialized), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpFile, cfgPath); err != nil {
		_ = os.Remove(tmpFile)
		return "", err
	}
	return serialized, nil
}
