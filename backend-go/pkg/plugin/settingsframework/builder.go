package settingsframework

import (
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

// FieldOption is a functional option that customizes a plugin.SettingsField
// created by one of the fluent field helper functions.
type FieldOption func(*plugin.SettingsField)

// WithDescription sets the help description on a settings field.
func WithDescription(desc string) FieldOption {
	return func(f *plugin.SettingsField) {
		f.Description = desc
	}
}

// WithPlaceholder sets the UI placeholder text on a settings field.
func WithPlaceholder(placeholder string) FieldOption {
	return func(f *plugin.SettingsField) {
		f.Placeholder = placeholder
	}
}

// WithDefault sets the default value on a settings field.
func WithDefault(def any) FieldOption {
	return func(f *plugin.SettingsField) {
		f.Default = def
	}
}

// WithRequired marks a settings field as required (or optional when false).
func WithRequired(required bool) FieldOption {
	return func(f *plugin.SettingsField) {
		f.Required = required
	}
}

// WithMinMax sets optional integer minimum and maximum bounds on a field.
func WithMinMax(min, max int) FieldOption {
	return func(f *plugin.SettingsField) {
		minCopy := min
		maxCopy := max
		f.Min = &minCopy
		f.Max = &maxCopy
	}
}

// WithMin sets an optional integer minimum bound on a field.
func WithMin(min int) FieldOption {
	return func(f *plugin.SettingsField) {
		minCopy := min
		f.Min = &minCopy
	}
}

// WithMax sets an optional integer maximum bound on a field.
func WithMax(max int) FieldOption {
	return func(f *plugin.SettingsField) {
		maxCopy := max
		f.Max = &maxCopy
	}
}

// applyFieldOptions applies functional options to a SettingsField.
func applyFieldOptions(f plugin.SettingsField, opts []FieldOption) plugin.SettingsField {
	for _, opt := range opts {
		if opt != nil {
			opt(&f)
		}
	}
	return f
}

// StringField constructs a single-line string SettingsField.
func StringField(name, label string, defaultVal string, opts ...FieldOption) plugin.SettingsField {
	f := plugin.SettingsField{
		Name:  strings.TrimSpace(name),
		Label: label,
		Type:  plugin.SettingsFieldString,
	}
	if defaultVal != "" {
		f.Default = defaultVal
	}
	return applyFieldOptions(f, opts)
}

// TextField constructs a multi-line text SettingsField.
func TextField(name, label string, defaultVal string, opts ...FieldOption) plugin.SettingsField {
	f := plugin.SettingsField{
		Name:  strings.TrimSpace(name),
		Label: label,
		Type:  plugin.SettingsFieldText,
	}
	if defaultVal != "" {
		f.Default = defaultVal
	}
	return applyFieldOptions(f, opts)
}

// IntField constructs an integer SettingsField.
func IntField(name, label string, defaultVal int, opts ...FieldOption) plugin.SettingsField {
	f := plugin.SettingsField{
		Name:    strings.TrimSpace(name),
		Label:   label,
		Type:    plugin.SettingsFieldInt,
		Default: defaultVal,
	}
	return applyFieldOptions(f, opts)
}

// BoolField constructs a boolean toggle SettingsField.
func BoolField(name, label string, defaultVal bool, opts ...FieldOption) plugin.SettingsField {
	f := plugin.SettingsField{
		Name:    strings.TrimSpace(name),
		Label:   label,
		Type:    plugin.SettingsFieldBool,
		Default: defaultVal,
	}
	return applyFieldOptions(f, opts)
}

// SelectField constructs an enumerated dropdown SettingsField.
func SelectField(name, label string, defaultVal string, options []plugin.SettingsFieldOption, opts ...FieldOption) plugin.SettingsField {
	copiedOpts := make([]plugin.SettingsFieldOption, len(options))
	copy(copiedOpts, options)
	f := plugin.SettingsField{
		Name:    strings.TrimSpace(name),
		Label:   label,
		Type:    plugin.SettingsFieldSelect,
		Options: copiedOpts,
	}
	if defaultVal != "" {
		f.Default = defaultVal
	}
	return applyFieldOptions(f, opts)
}

// SecretField constructs a write-only secret SettingsField that is always
// masked in read responses according to the Secret Protection Invariant.
func SecretField(name, label string, opts ...FieldOption) plugin.SettingsField {
	f := plugin.SettingsField{
		Name:   strings.TrimSpace(name),
		Label:  label,
		Type:   plugin.SettingsFieldSecret,
		Secret: true,
	}
	return applyFieldOptions(f, opts)
}

// PageBuilder fluently constructs a plugin.SettingsPage or
// newapi.CategorizedSettingsPage.
type PageBuilder struct {
	page newapi.CategorizedSettingsPage
}

// NewPageBuilder creates a fluent builder for the given page ID and title.
// By default, Scope is SettingsScopeDatacenter and Category is inferred from
// the "<kind>/<name>" prefix of ID (or "general" when no slash is present).
func NewPageBuilder(id, title string) *PageBuilder {
	trimmedID := strings.TrimSpace(id)
	category := "general"
	if idx := strings.IndexByte(trimmedID, '/'); idx > 0 {
		category = trimmedID[:idx]
	}
	return &PageBuilder{
		page: newapi.CategorizedSettingsPage{
			SettingsPage: plugin.SettingsPage{
				ID:     trimmedID,
				Title:  title,
				Fields: []plugin.SettingsField{},
			},
			Scope:    newapi.SettingsScopeDatacenter,
			Category: category,
		},
	}
}

// WithIcon sets the Material icon name on the settings page.
func (b *PageBuilder) WithIcon(icon string) *PageBuilder {
	b.page.Icon = icon
	return b
}

// WithDescription sets the description text on the settings page.
func (b *PageBuilder) WithDescription(desc string) *PageBuilder {
	b.page.Description = desc
	return b
}

// WithOrder sets the sidebar sort order on the settings page.
func (b *PageBuilder) WithOrder(order int) *PageBuilder {
	b.page.Order = order
	return b
}

// WithCategory sets the sidebar category grouping on the settings page.
func (b *PageBuilder) WithCategory(category string) *PageBuilder {
	if trimmed := strings.TrimSpace(category); trimmed != "" {
		b.page.Category = trimmed
	}
	return b
}

// WithScope sets the settings scope (datacenter vs node).
func (b *PageBuilder) WithScope(scope newapi.SettingsScope) *PageBuilder {
	if scope != "" {
		b.page.Scope = scope
	}
	return b
}

// AddField appends one or more SettingsField definitions to the page.
func (b *PageBuilder) AddField(fields ...plugin.SettingsField) *PageBuilder {
	b.page.Fields = append(b.page.Fields, fields...)
	return b
}

// StringField appends a string field to the page.
func (b *PageBuilder) StringField(name, label, defaultVal string, opts ...FieldOption) *PageBuilder {
	return b.AddField(StringField(name, label, defaultVal, opts...))
}

// TextField appends a multi-line text field to the page.
func (b *PageBuilder) TextField(name, label, defaultVal string, opts ...FieldOption) *PageBuilder {
	return b.AddField(TextField(name, label, defaultVal, opts...))
}

// IntField appends an integer field to the page.
func (b *PageBuilder) IntField(name, label string, defaultVal int, opts ...FieldOption) *PageBuilder {
	return b.AddField(IntField(name, label, defaultVal, opts...))
}

// BoolField appends a boolean field to the page.
func (b *PageBuilder) BoolField(name, label string, defaultVal bool, opts ...FieldOption) *PageBuilder {
	return b.AddField(BoolField(name, label, defaultVal, opts...))
}

// SelectField appends a select dropdown field to the page.
func (b *PageBuilder) SelectField(name, label, defaultVal string, options []plugin.SettingsFieldOption, opts ...FieldOption) *PageBuilder {
	return b.AddField(SelectField(name, label, defaultVal, options, opts...))
}

// SecretField appends a write-only secret field to the page.
func (b *PageBuilder) SecretField(name, label string, opts ...FieldOption) *PageBuilder {
	return b.AddField(SecretField(name, label, opts...))
}

// Build returns the underlying plugin.SettingsPage with a defensive copy of its
// fields slice.
func (b *PageBuilder) Build() plugin.SettingsPage {
	return b.BuildCategorized().SettingsPage
}

// BuildCategorized returns the full newapi.CategorizedSettingsPage with a
// defensive copy of its fields slice.
func (b *PageBuilder) BuildCategorized() newapi.CategorizedSettingsPage {
	out := b.page
	fields := make([]plugin.SettingsField, len(b.page.Fields))
	for i, f := range b.page.Fields {
		if len(f.Options) > 0 {
			opts := make([]plugin.SettingsFieldOption, len(f.Options))
			copy(opts, f.Options)
			f.Options = opts
		}
		fields[i] = f
	}
	out.Fields = fields
	return out
}
