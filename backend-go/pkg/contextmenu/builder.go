package contextmenu

import (
	"fmt"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/plugin/newapi"
)

// Builder constructs a sorted list of MenuItems and MenuSections for a plugin
// or UI surface.
type Builder struct {
	pluginID string
	target   MenuTarget
	group    string
	items    []MenuItem
	sepSeq   int
}

// NewBuilder creates a new fluent context menu Builder for the given pluginID
// and target. If target is empty, it defaults to TargetNode.
func NewBuilder(pluginID string, target ...MenuTarget) *Builder {
	t := TargetNode
	if len(target) > 0 && target[0] != "" {
		t = target[0]
	}
	return &Builder{
		pluginID: strings.TrimSpace(pluginID),
		target:   t,
	}
}

// WithTarget sets the default target for subsequent items added to the Builder.
func (b *Builder) WithTarget(target MenuTarget) *Builder {
	if target != "" {
		b.target = target
	}
	return b
}

// WithGroup sets the default section/group name for subsequent items.
func (b *Builder) WithGroup(group string) *Builder {
	b.group = strings.TrimSpace(group)
	return b
}

// AddItem appends a MenuItem to the builder, filling in default PluginID,
// Target, Kind, and Group when omitted.
func (b *Builder) AddItem(item MenuItem) *Builder {
	normalized := b.normalizeItem(item)
	b.items = append(b.items, normalized)
	return b
}

// AddAction adds a standard actionable menu item.
func (b *Builder) AddAction(id, label, icon, action string, order int) *Builder {
	return b.AddItem(MenuItem{
		ID:     id,
		Kind:   ItemKindAction,
		Label:  label,
		Icon:   icon,
		Action: action,
		Order:  order,
	})
}

// AddDangerAction adds a destructive action item requiring confirmation.
func (b *Builder) AddDangerAction(id, label, icon, action, confirmPrompt string, order int) *Builder {
	return b.AddItem(MenuItem{
		ID:            id,
		Kind:          ItemKindAction,
		Label:         label,
		Icon:          icon,
		Action:        action,
		ConfirmPrompt: confirmPrompt,
		Danger:        true,
		Order:         order,
	})
}

// AddDisabledAction adds a disabled menu item with an explanation tooltip/reason.
func (b *Builder) AddDisabledAction(id, label, icon, action, reason string, order int) *Builder {
	return b.AddItem(MenuItem{
		ID:             id,
		Kind:           ItemKindAction,
		Label:          label,
		Icon:           icon,
		Action:         action,
		Disabled:       true,
		DisabledReason: reason,
		Order:          order,
	})
}

// AddSubmenu adds a nested submenu item with sorted children.
func (b *Builder) AddSubmenu(id, label, icon string, order int, children ...MenuItem) *Builder {
	normChildren := make([]MenuItem, 0, len(children))
	for _, child := range children {
		normChildren = append(normChildren, b.normalizeItem(child))
	}
	SortItems(normChildren)
	return b.AddItem(MenuItem{
		ID:       id,
		Kind:     ItemKindSubmenu,
		Label:    label,
		Icon:     icon,
		Order:    order,
		Children: normChildren,
	})
}

// AddSeparator inserts a visual separator line at the given order position.
func (b *Builder) AddSeparator(order int) *Builder {
	b.sepSeq++
	id := fmt.Sprintf("separator-%d", b.sepSeq)
	if b.pluginID != "" {
		id = fmt.Sprintf("%s-%s", b.pluginID, id)
	}
	return b.AddItem(MenuItem{
		ID:    id,
		Kind:  ItemKindSeparator,
		Label: "-",
		Order: order,
	})
}

// AddEntries imports a slice of newapi.ContextMenuEntry values into the Builder.
func (b *Builder) AddEntries(entries ...newapi.ContextMenuEntry) *Builder {
	for _, entry := range entries {
		b.AddItem(FromNewAPIEntry(entry))
	}
	return b
}

// Build returns a sorted copy of all MenuItems accumulated in the Builder.
func (b *Builder) Build() []MenuItem {
	out := make([]MenuItem, len(b.items))
	for i, item := range b.items {
		out[i] = cloneItem(item)
	}
	SortItems(out)
	return out
}

// BuildEntries returns the built menu items converted to []newapi.ContextMenuEntry.
func (b *Builder) BuildEntries() []newapi.ContextMenuEntry {
	items := b.Build()
	out := make([]newapi.ContextMenuEntry, 0, len(items))
	for _, item := range items {
		out = append(out, ToNewAPIEntry(item))
	}
	return out
}

// BuildSections groups the accumulated MenuItems into ordered MenuSections by
// their Group field. Items with an empty Group are placed in the "default" section.
func (b *Builder) BuildSections() []MenuSection {
	return GroupItemsIntoSections(b.Build())
}

func (b *Builder) normalizeItem(item MenuItem) MenuItem {
	if item.PluginID == "" {
		item.PluginID = b.pluginID
	}
	if item.Target == "" {
		item.Target = b.target
	}
	if item.Group == "" && b.group != "" {
		item.Group = b.group
	}
	if item.Kind == "" {
		if len(item.Children) > 0 && item.Action == "" {
			item.Kind = ItemKindSubmenu
		} else if item.Label == "-" {
			item.Kind = ItemKindSeparator
		} else {
			item.Kind = ItemKindAction
		}
	}
	if len(item.Children) > 0 {
		children := make([]MenuItem, 0, len(item.Children))
		for _, child := range item.Children {
			children = append(children, b.normalizeItem(child))
		}
		SortItems(children)
		item.Children = children
	}
	return item
}

func cloneItem(item MenuItem) MenuItem {
	out := item
	if item.Params != nil {
		out.Params = make(map[string]any, len(item.Params))
		for k, v := range item.Params {
			out.Params[k] = v
		}
	}
	if len(item.Children) > 0 {
		out.Children = make([]MenuItem, len(item.Children))
		for i, c := range item.Children {
			out.Children[i] = cloneItem(c)
		}
	}
	return out
}

// SortItems sorts a slice of MenuItems in place by Order, then Group, then Label, then ID.
func SortItems(items []MenuItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Order != items[j].Order {
			return items[i].Order < items[j].Order
		}
		if items[i].Group != items[j].Group {
			return items[i].Group < items[j].Group
		}
		if items[i].Label != items[j].Label {
			return items[i].Label < items[j].Label
		}
		return items[i].ID < items[j].ID
	})
}

// GroupItemsIntoSections organizes a flat slice of MenuItems into ordered
// MenuSections keyed by each item's Group field.
func GroupItemsIntoSections(items []MenuItem) []MenuSection {
	if len(items) == 0 {
		return []MenuSection{}
	}

	sorted := make([]MenuItem, len(items))
	for i, item := range items {
		sorted[i] = cloneItem(item)
	}
	SortItems(sorted)

	sectionIndex := make(map[string]int)
	var sections []MenuSection

	for _, item := range sorted {
		groupID := strings.TrimSpace(item.Group)
		if groupID == "" {
			groupID = "general"
		}
		idx, exists := sectionIndex[groupID]
		if !exists {
			idx = len(sections)
			sectionIndex[groupID] = idx
			sections = append(sections, MenuSection{
				ID:    groupID,
				Label: formatGroupLabel(groupID),
				Order: item.Order,
				Items: []MenuItem{},
			})
		}
		if item.Order < sections[idx].Order {
			sections[idx].Order = item.Order
		}
		sections[idx].Items = append(sections[idx].Items, item)
	}

	sort.SliceStable(sections, func(i, j int) bool {
		if sections[i].Order != sections[j].Order {
			return sections[i].Order < sections[j].Order
		}
		return sections[i].ID < sections[j].ID
	})

	return sections
}

func formatGroupLabel(groupID string) string {
	cleaned := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(groupID), "_", " "), "-", " ")
	if cleaned == "" {
		return "General"
	}
	parts := strings.Fields(cleaned)
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
