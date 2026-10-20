package plugin

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

// SettingsPage describes a datacenter-wide ("global") settings page contributed
// by an addon module. The ID is the page's route slug and must be unique across
// all registered plugins (the conventional form is "<kind>/<name>").
type SettingsPage struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Icon        string          `json:"icon"`
	Description string          `json:"description"`
	Order       int             `json:"order"`
	Fields      []SettingsField `json:"fields"`
}

// SettingsPageProvider is the optional interface an addon module implements to
// contribute a datacenter-wide settings page to the Datacenter Settings sidebar.
// Implementations declare the page schema; the framework owns persistence and
// rendering, so a module never has to ship its own HTML for a settings page.
type SettingsPageProvider interface {
	// SettingsPage returns the static schema (id, route, fields) for the page.
	SettingsPage() SettingsPage
}
