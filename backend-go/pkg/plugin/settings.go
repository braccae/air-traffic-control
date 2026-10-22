package plugin

import "strings"

// SettingsFieldType enumerates the typed inputs the framework knows how to
// render and persist on a global settings page.
type SettingsFieldType string

const (
	SettingsFieldString SettingsFieldType = "string"
	SettingsFieldText   SettingsFieldType = "text"
	SettingsFieldInt    SettingsFieldType = "int"
	SettingsFieldBool   SettingsFieldType = "bool"
	SettingsFieldSelect SettingsFieldType = "select"
	SettingsFieldSecret SettingsFieldType = "secret"
)

// SettingsFieldOption is a selectable value for a "select" field.
type SettingsFieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// SettingsField describes a single configurable control on a global settings page.
type SettingsField struct {
	Name        string                `json:"name"`
	Label       string                `json:"label"`
	Type        SettingsFieldType     `json:"type"`
	Description string                `json:"description,omitempty"`
	Placeholder string                `json:"placeholder,omitempty"`
	Default     any                   `json:"default,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	Secret      bool                  `json:"secret,omitempty"`
	Options     []SettingsFieldOption `json:"options,omitempty"`
	Min         *int                  `json:"min,omitempty"`
	Max         *int                  `json:"max,omitempty"`
}

// SettingsPage describes a settings page contributed by an addon module.
// The ID is the page's route slug and must be unique across all registered
// plugins (the conventional form is "<kind>/<name>"). Scope ("datacenter" vs
// "node") and Category are optional and default to "datacenter" and the page's
// kind prefix when omitted.
type SettingsPage struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Icon        string          `json:"icon"`
	Description string          `json:"description"`
	Order       int             `json:"order"`
	Scope       SettingsScope   `json:"scope,omitempty"`
	Category    string          `json:"category,omitempty"`
	Fields      []SettingsField `json:"fields"`
}

// SecretMaskPrefix is the fixed bullet run used for every masked secret value.
// The mask is the only representation of a secret that ever leaves the process.
const SecretMaskPrefix = "••••••••"

// IsSecretField reports whether a field carries a write-only secret. A field is
// secret when it is typed "secret" or flagged with Secret.
func IsSecretField(f SettingsField) bool {
	return f.Secret || f.Type == SettingsFieldSecret
}

// MaskedSecretValue renders the mask for a stored secret field, in the shape
// required by the Secret Protection Invariant: "•••••••• (vault: <name>)".
// Note that the mask names where the secret *would* live; it never contains the
// secret itself, and the framework stores module secrets in the settings store
// rather than in an on-disk Ansible Vault.
func MaskedSecretValue(name string) string {
	return SecretMaskPrefix + " (vault: " + name + ")"
}

// IsMaskedSecretValue reports whether a value returned by this package is a
// mask rather than real secret material. Clients echo the mask back when they
// re-save a page they only partially edited; the framework must never persist
// the mask over the real value.
func IsMaskedSecretValue(v string) bool {
	return strings.Contains(v, SecretMaskPrefix)
}

// SettingsPageProvider is the optional interface an addon module implements to
// contribute a datacenter-wide settings page to the Datacenter Settings sidebar.
// Implementations declare the page schema; the framework owns persistence and
// rendering, so a module never has to ship its own HTML for a settings page.
type SettingsPageProvider interface {
	// SettingsPage returns the static schema (id, route, fields) for the page.
	SettingsPage() SettingsPage
}
