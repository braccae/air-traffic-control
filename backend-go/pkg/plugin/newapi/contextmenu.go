package newapi

import (
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

// ContextMenuTarget identifies the UI entity type that a context menu targets.
type ContextMenuTarget = plugin.ContextMenuTarget

const (
	TargetNode      = plugin.TargetNode
	TargetContainer = plugin.TargetContainer
	TargetVM        = plugin.TargetVM
	TargetStorage   = plugin.TargetStorage
	TargetShare     = plugin.TargetShare
	TargetNetwork   = plugin.TargetNetwork
	TargetFirewall  = plugin.TargetFirewall
	TargetService   = plugin.TargetService
	TargetDatabase  = plugin.TargetDatabase
)

// ContextMenuEntry represents a single actionable item or nested submenu
// contributed by a plugin to a UI context menu.
type ContextMenuEntry = plugin.ContextMenuEntry

// ContextMenuBuilder provides a fluent interface for constructing sorted,
// plugin-attributed context menu entries.
type ContextMenuBuilder interface {
	WithTarget(target ContextMenuTarget) ContextMenuBuilder
	WithGroup(group string) ContextMenuBuilder
	AddEntry(entry ContextMenuEntry) ContextMenuBuilder
	AddAction(id, label, icon, action string, order int) ContextMenuBuilder
	AddDangerAction(id, label, icon, action, confirmPrompt string, order int) ContextMenuBuilder
	AddSubmenu(id, label, icon string, order int, children ...ContextMenuEntry) ContextMenuBuilder
	Build() []ContextMenuEntry
}

// ContextMenuProvider is an optional interface implemented by plugins that
// contribute context menu entries for a host or resource target.
type ContextMenuProvider = plugin.ContextMenuProvider

type contextMenuBuilder struct {
	pluginID string
	target   ContextMenuTarget
	group    string
	entries  []ContextMenuEntry
}

// NewContextMenuBuilder creates a new fluent ContextMenuBuilder bound to the
// given plugin ID and defaulting to TargetNode.
func NewContextMenuBuilder(pluginID string) ContextMenuBuilder {
	return &contextMenuBuilder{
		pluginID: strings.TrimSpace(pluginID),
		target:   TargetNode,
	}
}

func (b *contextMenuBuilder) WithTarget(target ContextMenuTarget) ContextMenuBuilder {
	b.target = target
	return b
}

func (b *contextMenuBuilder) WithGroup(group string) ContextMenuBuilder {
	b.group = strings.TrimSpace(group)
	return b
}

func (b *contextMenuBuilder) AddEntry(entry ContextMenuEntry) ContextMenuBuilder {
	if entry.PluginID == "" {
		entry.PluginID = b.pluginID
	}
	if entry.Target == "" {
		entry.Target = b.target
	}
	if entry.Group == "" && b.group != "" {
		entry.Group = b.group
	}
	b.entries = append(b.entries, entry)
	return b
}

func (b *contextMenuBuilder) AddAction(id, label, icon, action string, order int) ContextMenuBuilder {
	return b.AddEntry(ContextMenuEntry{
		ID:       id,
		PluginID: b.pluginID,
		Target:   b.target,
		Group:    b.group,
		Label:    label,
		Icon:     icon,
		Action:   action,
		Order:    order,
	})
}

func (b *contextMenuBuilder) AddDangerAction(id, label, icon, action, confirmPrompt string, order int) ContextMenuBuilder {
	return b.AddEntry(ContextMenuEntry{
		ID:            id,
		PluginID:      b.pluginID,
		Target:        b.target,
		Group:         b.group,
		Label:         label,
		Icon:          icon,
		Action:        action,
		ConfirmPrompt: confirmPrompt,
		Danger:        true,
		Order:         order,
	})
}

func (b *contextMenuBuilder) AddSubmenu(id, label, icon string, order int, children ...ContextMenuEntry) ContextMenuBuilder {
	normalized := make([]ContextMenuEntry, 0, len(children))
	for _, child := range children {
		if child.PluginID == "" {
			child.PluginID = b.pluginID
		}
		if child.Target == "" {
			child.Target = b.target
		}
		normalized = append(normalized, child)
	}
	sortContextMenuEntries(normalized)
	return b.AddEntry(ContextMenuEntry{
		ID:       id,
		PluginID: b.pluginID,
		Target:   b.target,
		Group:    b.group,
		Label:    label,
		Icon:     icon,
		Order:    order,
		Children: normalized,
	})
}

func (b *contextMenuBuilder) Build() []ContextMenuEntry {
	out := make([]ContextMenuEntry, len(b.entries))
	copy(out, b.entries)
	sortContextMenuEntries(out)
	return out
}

func sortContextMenuEntries(entries []ContextMenuEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Order != entries[j].Order {
			return entries[i].Order < entries[j].Order
		}
		if entries[i].Label != entries[j].Label {
			return entries[i].Label < entries[j].Label
		}
		return entries[i].ID < entries[j].ID
	})
}
