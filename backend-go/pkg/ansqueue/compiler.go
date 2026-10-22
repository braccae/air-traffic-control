package ansqueue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Node is the subset of the TS NodeItem the task builders read. It is a plain
// data carrier: the daemon resolves it from the hosts table, nothing is
// synthesised here.
type Node struct {
	ID              string
	Name            string
	ShortName       string
	IP              string
	Port            int
	SSHUser         string
	TailscaleIP     string
	TailscaleHost   string
	FallbackHost    string
	FallbackIP      string
	PrivilegeMethod string
}

// ---------------------------------------------------------------------------
// small JSON helpers
// ---------------------------------------------------------------------------

// marshalNoEscape serialises v like JSON.stringify: no HTML escaping, no
// trailing newline. Task titles and share paths contain <, > and & and must
// survive a round-trip through SQLite unchanged.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// jsonQuote is JSON.stringify(string) — the exact quoting formatYamlParamValue
// and the playbook generator use for YAML scalars.
func jsonQuote(s string) string {
	b, err := marshalNoEscape(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// connection resolution
// ---------------------------------------------------------------------------

// resolveBecomeMethod mirrors the TS helper: sudo/doas are honoured, everything
// else (including an unset value) prefers systemd 256+ run0.
func resolveBecomeMethod(node Node) string {
	switch node.PrivilegeMethod {
	case "sudo", "doas":
		return node.PrivilegeMethod
	default:
		return "run0"
	}
}

// resolveConnectionRoute is the Go port of hardwareProbe.ts
// resolveConnectionRoute: tailscale host, then tailscale IP, then LAN IP, then
// localhost.
func resolveConnectionRoute(node Node) (primary, fallback string) {
	tailscaleHost := strings.TrimSpace(node.TailscaleHost)
	tailscaleIP := strings.TrimSpace(node.TailscaleIP)
	lanIP := strings.TrimSpace(node.IP)
	fallbackHost := strings.TrimSpace(node.FallbackHost)
	fallbackIP := strings.TrimSpace(node.FallbackIP)

	primary = firstNonEmpty(tailscaleHost, tailscaleIP, lanIP, "localhost")
	switch {
	case fallbackHost != "" && fallbackHost != primary:
		fallback = fallbackHost
	case fallbackIP != "" && fallbackIP != primary:
		fallback = fallbackIP
	case lanIP != "" && lanIP != primary:
		fallback = lanIP
	}
	return primary, fallback
}

// resolveNodeConnection ports resolveNodeConnection, including the
// resolveConnectionRoute fallback chain and the core/22 defaults.
func resolveNodeConnection(node Node) (hostRoute, sshUser string, sshPort int) {
	primary, _ := resolveConnectionRoute(node)
	hostRoute = primary
	if hostRoute == "" {
		hostRoute = firstNonEmpty(node.TailscaleHost, node.TailscaleIP, node.IP, "localhost")
	}
	sshUser = firstNonEmpty(node.SSHUser, "core")
	sshPort = node.Port
	if sshPort == 0 {
		sshPort = 22
	}
	return hostRoute, sshUser, sshPort
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func nodeDisplayName(node Node) string {
	return firstNonEmpty(node.ShortName, node.Name, node.ID)
}

// newTaskID matches the TS id format `task-<UnixMilli>-<6 base36 chars>`.
func newTaskID() string {
	now := time.Now().UnixMilli()
	var b [6]byte
	const base36 = "0123456789abcdefghijklmnopqrstuvwxyz"
	n := nextRandomUint64()
	// Fold the whole 64-bit draw into a 36^6 space, most significant first.
	const space = uint64(36 * 36 * 36 * 36 * 36 * 36)
	n %= space
	for i := 5; i >= 0; i-- {
		b[i] = base36[n%36]
		n /= 36
	}
	return fmt.Sprintf("task-%d-%s", now, string(b[:]))
}

// baseTask builds the shared task envelope every builder fills in.
func baseTask(node Node, title, category, primaryModule string, steps []Step, become bool) Task {
	hostRoute, sshUser, sshPort := resolveNodeConnection(node)
	return Task{
		ID:              newTaskID(),
		Title:           title,
		TargetNodeID:    node.ID,
		TargetNodeName:  nodeDisplayName(node),
		TargetHostRoute: hostRoute,
		TargetSSHUser:   sshUser,
		TargetSSHPort:   sshPort,
		Category:        category,
		PrimaryModule:   primaryModule,
		Steps:           steps,
		Become:          BoolPtr(become),
		BecomeMethod:    resolveBecomeMethod(node),
		Status:          StatusStaged,
		CreatedAt:       time.Now().UnixMilli(),
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// CompileServiceTask ports compileServiceTask (ansible.builtin.systemd_service).
func CompileServiceTask(node Node, serviceName, action string) Task {
	step := Step{
		Name:   fmt.Sprintf("%s %s service", titleCase(action), serviceName),
		Module: "ansible.builtin.systemd_service",
		Parameters: map[string]any{
			"name":  serviceName,
			"state": action,
		},
	}
	return baseTask(node,
		fmt.Sprintf("%s service %s", titleCase(action), serviceName),
		"system", "ansible.builtin.systemd_service", []Step{step}, true)
}

// CompileFirewallTask ports compileFirewallTask (ansible.posix.firewalld).
func CompileFirewallTask(node Node, port, proto, zone string) Task {
	portProto := fmt.Sprintf("%s/%s", port, proto)
	params := map[string]any{
		"port":      portProto,
		"permanent": true,
		"state":     "enabled",
		"immediate": true,
	}
	if zone != "" {
		params["zone"] = zone
	}
	step := Step{
		Name:       fmt.Sprintf("Allow port %s in firewall", portProto),
		Module:     "ansible.posix.firewalld",
		Parameters: params,
	}
	return baseTask(node,
		fmt.Sprintf("Allow port %s through firewall", portProto),
		"network", "ansible.posix.firewalld", []Step{step}, true)
}

// CompileShareTask ports compileShareTask, including secret masking.
func CompileShareTask(node Node, shareName, sharePath string, users []string, passwordVaultKey string) Task {
	step := Step{
		Name:   fmt.Sprintf("Configure share %s at %s", shareName, sharePath),
		Module: "ansible.builtin.template",
		Parameters: map[string]any{
			"src":         "smb.conf.j2",
			"dest":        fmt.Sprintf("/etc/samba/smb.conf.d/%s.conf", shareName),
			"owner":       "root",
			"group":       "root",
			"mode":        "0644",
			"share_path":  sharePath,
			"valid_users": strings.Join(users, " "),
		},
	}
	t := baseTask(node,
		fmt.Sprintf("Configure share %s", shareName),
		"storage", "ansible.builtin.template", []Step{step}, true)
	t.SecretsMasked = map[string]string{"smb_password": MaskedSecretValue(passwordVaultKey)}
	return t
}

// CompileContainerTask ports compileContainerTask
// (containers.podman.podman_container). Containers run unprivileged, so
// become is false exactly like the TS builder.
func CompileContainerTask(node Node, containerName, action string) Task {
	stateMap := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}
	targetState, ok := stateMap[action]
	if !ok {
		targetState = "started"
	}
	step := Step{
		Name:   fmt.Sprintf("%s container %s", titleCase(action), containerName),
		Module: "containers.podman.podman_container",
		Parameters: map[string]any{
			"name":  containerName,
			"state": targetState,
		},
	}
	return baseTask(node,
		fmt.Sprintf("%s container %s", titleCase(action), containerName),
		"container", "containers.podman.podman_container", []Step{step}, false)
}

// CompilePackageTask ports compilePackageTask (ansible.builtin.package).
func CompilePackageTask(node Node, packageName, state string) Task {
	step := Step{
		Name:   fmt.Sprintf("Ensure package %s is %s", packageName, state),
		Module: "ansible.builtin.package",
		Parameters: map[string]any{
			"name":  packageName,
			"state": state,
		},
	}
	return baseTask(node,
		fmt.Sprintf("Ensure package %s is %s", packageName, state),
		"package", "ansible.builtin.package", []Step{step}, true)
}

// ---------------------------------------------------------------------------
// YAML parameter formatting
// ---------------------------------------------------------------------------

// yamlNeedsQuote matches the TS character class `[:#{}[\],&*?|<>=!%@`]`: any
// YAML indicator character forces a JSON-quoted scalar.
var yamlNeedsQuote = regexp.MustCompile("[:#{}[\\],&*?|<>=!%@`]")

// yamlKeyword matches the TS /^(true|false|yes|no|null)$/i check.
var yamlKeyword = regexp.MustCompile(`(?i)^(true|false|yes|no|null)$`)

// FormatYAMLParamValue reproduces formatYamlParamValue byte-for-byte: block
// scalars for multiline strings, JSON quoting for indicator characters /
// keywords / empty strings, `[]` and `{}` for empty containers, and nested
// list/map indentation of two spaces per level.
//
// Go maps are unordered where a JS object literal preserves insertion order, so
// map keys are emitted sorted: the resulting YAML is stable and semantically
// identical, and an exact-output test would otherwise be flaky.
func FormatYAMLParamValue(val any, indentLevel int) string {
	switch v := val.(type) {
	case nil:
		return ""
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		if strings.ContainsAny(v, "\n\r") {
			indent := strings.Repeat(" ", indentLevel)
			lines := strings.Split(v, "\n")
			for i, l := range lines {
				lines[i] = indent + "  " + l
			}
			return "|\n" + strings.Join(lines, "\n")
		}
		if v == "" || yamlNeedsQuote.MatchString(v) || yamlKeyword.MatchString(v) {
			return jsonQuote(v)
		}
		return v
	case json.Number:
		return v.String()
	case float64:
		return formatJSNumber(v)
	case float32:
		return formatJSNumber(float64(v))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case []any:
		if len(v) == 0 {
			return "[]"
		}
		indent := strings.Repeat(" ", indentLevel)
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = indent + "- " + FormatYAMLParamValue(item, indentLevel+2)
		}
		return "\n" + strings.Join(parts, "\n")
	case []string:
		if len(v) == 0 {
			return "[]"
		}
		indent := strings.Repeat(" ", indentLevel)
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = indent + "- " + FormatYAMLParamValue(item, indentLevel+2)
		}
		return "\n" + strings.Join(parts, "\n")
	case map[string]string:
		conv := make(map[string]any, len(v))
		for k, item := range v {
			conv[k] = item
		}
		return FormatYAMLParamValue(conv, indentLevel)
	case map[string]any:
		if len(v) == 0 {
			return "{}"
		}
		indent := strings.Repeat(" ", indentLevel)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = indent + k + ": " + FormatYAMLParamValue(v[k], indentLevel+2)
		}
		return "\n" + strings.Join(parts, "\n")
	default:
		return fmt.Sprint(v)
	}
}

// formatJSNumber renders a float like JS String(number): shortest round-trip
// form, integral values without a trailing ".0".
func formatJSNumber(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// ---------------------------------------------------------------------------
// proxy command + inventory + playbook
// ---------------------------------------------------------------------------

// IsSystemTailscaledActive reports whether a host-level tailscaled socket is
// present (the ProxyCommand bridge is only needed when it is not).
func IsSystemTailscaledActive() bool {
	for _, sock := range []string{"/run/tailscale/tailscaled.sock", "/var/run/tailscale/tailscaled.sock"} {
		if _, err := os.Stat(sock); err == nil {
			return true
		}
	}
	return false
}

// isSystemTailscaledActive is the socket probe ResolveProxyCommand consults.
// It is a variable so tests can pin the "no system tailscaled" branch on a
// machine that happens to be running one; production always uses the real probe.
var isSystemTailscaledActive = IsSystemTailscaledActive

// daemonProxyCandidates lists the daemon binary locations searched (in order)
// for the Ansible ProxyCommand bridge. A variable so tests can pin one
// candidate without depending on what exists in /tmp on the build machine.
var daemonProxyCandidates = func(cwd string) []string {
	return []string{
		"/tmp/air-traffic-control-daemon",
		filepath.Join(cwd, "backend-go", "air-traffic-control-daemon"),
		"/usr/local/bin/air-traffic-control-daemon",
	}
}

// ResolveProxyCommand ports resolveProxyCommand: an explicit
// connection.proxyCommand wins; tailscale routes need the daemon proxy bridge
// when no system tailscaled is running.
func ResolveProxyCommand(task Task) string {
	if task.Connection != nil && task.Connection.ProxyCommand != "" {
		return task.Connection.ProxyCommand
	}

	isTailscale := (task.Connection != nil && task.Connection.Plugin == "connection/tailscale") ||
		(task.TargetHostRoute != "" &&
			(strings.Contains(task.TargetHostRoute, ".ts.net") || strings.HasPrefix(task.TargetHostRoute, "100.")))

	if isTailscale && !isSystemTailscaledActive() {
		cwd, _ := os.Getwd()
		for _, c := range daemonProxyCandidates(cwd) {
			if _, err := os.Stat(c); err == nil {
				return c + " proxy %h %p"
			}
		}
		return "air-traffic-control-daemon proxy %h %p"
	}
	return ""
}

// hostVars is an ordered hostvars block: field order matches the TS object
// literal so the generated inventory JSON is byte-identical apart from
// indentation of the outer document.
type hostVars struct {
	AnsibleHost          string `json:"ansible_host"`
	AnsibleUser          string `json:"ansible_user"`
	AnsiblePort          int    `json:"ansible_port"`
	AnsibleConnection    string `json:"ansible_connection,omitempty"`
	AnsibleBecomeMethod  string `json:"ansible_become_method,omitempty"`
	AnsibleSSHCommonArgs string `json:"ansible_ssh_common_args,omitempty"`
	AnsibleSSHExtraArgs  string `json:"ansible_ssh_extra_args,omitempty"`
}

func qualifyBecomeMethod(m string) string {
	if strings.EqualFold(strings.TrimSpace(m), "run0") {
		return "community.general.run0"
	}
	return m
}

// GenerateTaskInventory ports generateTaskInventory: an ephemeral, per-task
// inventory JSON document derived from the task's connection block.
func GenerateTaskInventory(task Task) (string, error) {
	hostName := firstNonEmpty(task.TargetNodeName, task.TargetNodeID, "target")
	var conn Connection
	if task.Connection != nil {
		conn = *task.Connection
	}

	route := firstNonEmpty(conn.Host, task.TargetHostRoute, "localhost")
	user := firstNonEmpty(conn.User, task.TargetSSHUser, "core")
	port := conn.Port
	if port == 0 {
		port = task.TargetSSHPort
	}
	if port == 0 {
		port = 22
	}

	hv := hostVars{AnsibleHost: route, AnsibleUser: user, AnsiblePort: port}

	switch conn.Plugin {
	case "connection/local":
		hv.AnsibleConnection = "local"
	case "connection/winrm":
		hv.AnsibleConnection = "winrm"
	case "connection/ssh", "connection/tailscale":
		hv.AnsibleConnection = "ssh"
	}

	if method := firstNonEmpty(conn.BecomeMethod, task.BecomeMethod); method != "" {
		hv.AnsibleBecomeMethod = qualifyBecomeMethod(method)
	}
	if proxyCmd := ResolveProxyCommand(task); proxyCmd != "" {
		hv.AnsibleSSHCommonArgs = fmt.Sprintf(`-o ProxyCommand="%s"`, proxyCmd)
	}
	if len(conn.ExtraArgs) > 0 {
		hv.AnsibleSSHExtraArgs = strings.Join(conn.ExtraArgs, " ")
	}

	// DELIBERATE DEVIATION from the TS generator: ts emits
	// `{_meta:{hostvars:{...}}, all:{hosts:[name]}}`, which is the *dynamic
	// inventory script* shape. Ansible only accepts `_meta` from an executable
	// inventory script; used as a static -i file the yaml/ini plugins reject it
	// ("Invalid hosts entry for all group, requires a dictionary") and the play
	// targets no hosts. This emits the equivalent static-inventory document
	// (all.hosts.<name> = hostvars), which ansible parses and which carries the
	// exact same variables.
	inv := struct {
		All struct {
			Hosts map[string]hostVars `json:"hosts"`
		} `json:"all"`
	}{}
	inv.All.Hosts = map[string]hostVars{hostName: hv}

	b, err := marshalNoEscape(inv)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return "", err
	}
	return out.String(), nil
}

// CompileOnTheFlyPlaybook ports compileOnTheFlyPlaybook. Every mutation is a
// single play against `hosts: all` with the node pinned through the inventory
// and `--limit`, exactly like the TS generator.
func CompileOnTheFlyPlaybook(task Task) string {
	var conn Connection
	if task.Connection != nil {
		conn = *task.Connection
	}

	host := firstNonEmpty(conn.Host, task.TargetHostRoute, "127.0.0.1")
	user := firstNonEmpty(conn.User, task.TargetSSHUser, "core")
	port := conn.Port
	if port == 0 {
		port = task.TargetSSHPort
	}
	if port == 0 {
		port = 22
	}
	becomeMethod := qualifyBecomeMethod(firstNonEmpty(conn.BecomeMethod, task.BecomeMethod, "run0"))

	lines := []string{
		"---",
		"- name: " + jsonQuote(task.Title),
		"  hosts: all",
		"  gather_facts: false",
		"  vars:",
		"    ansible_host: " + jsonQuote(host),
		"    ansible_user: " + jsonQuote(user),
		"    ansible_port: " + strconv.Itoa(port),
	}

	switch conn.Plugin {
	case "connection/local":
		lines = append(lines, `    ansible_connection: "local"`)
	case "connection/winrm":
		lines = append(lines, `    ansible_connection: "winrm"`)
	}

	if proxyCmd := ResolveProxyCommand(task); proxyCmd != "" {
		commonArgs := fmt.Sprintf(`-o ProxyCommand="%s"`, proxyCmd)
		lines = append(lines, "    ansible_ssh_common_args: "+jsonQuote(commonArgs))
	}

	become := "false"
	if task.BecomeEnabled() {
		become = "true"
	}
	lines = append(lines,
		"  become: "+become,
		"  become_method: "+becomeMethod,
		"  tasks:",
	)

	for _, step := range task.Steps {
		lines = append(lines, "    - name: "+jsonQuote(step.Name))
		lines = append(lines, "      "+step.Module+":")
		keys := make([]string, 0, len(step.Parameters))
		for k := range step.Parameters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, "        "+k+": "+FormatYAMLParamValue(step.Parameters[k], 10))
		}
	}

	return strings.Join(lines, "\n") + "\n"
}

// ---------------------------------------------------------------------------
// secrets
// ---------------------------------------------------------------------------

// MaskedSecretValue renders the operator-facing mask for a vault-backed
// secret: `•••••••• (vault: <varName>)`, or the bare mask when no vault key is
// configured. The value itself is never represented.
func MaskedSecretValue(varName string) string {
	if varName == "" {
		return "••••••••"
	}
	return fmt.Sprintf("•••••••• (vault: %s)", varName)
}

// secretKeyPattern finds secret-looking keys followed by a value.
var (
	secretQuotedValue   = regexp.MustCompile(`(?i)("?(?:password|passwd|secret|token|api_key|private_key)"?\s*[:=]\s*)"[^"]*"`)
	secretUnquotedValue = regexp.MustCompile(`(?i)("?(?:password|passwd|secret|token|api_key|private_key)"?\s*[:=]\s*)([^\s,;}]+)`)
)

// RedactSecrets scrubs the values of secret-looking parameter keys from a log
// line, error string or task JSON blob before it is persisted.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	out := secretQuotedValue.ReplaceAllString(s, `${1}"`+maskRunes+`"`)
	return secretUnquotedValue.ReplaceAllString(out, `${1}`+maskRunes)
}

const maskRunes = "••••••••"
