package contextmenu

import (
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin/newapi"
)

// MenuTarget identifies the UI entity type that a context menu targets.
// It is an alias for newapi.ContextMenuTarget so plugins implementing
// newapi.ContextMenuProvider interoperate with the contextmenu package
// without type conversions.
type MenuTarget = newapi.ContextMenuTarget

const (
	TargetNode      MenuTarget = newapi.TargetNode
	TargetContainer MenuTarget = newapi.TargetContainer
	TargetVM        MenuTarget = newapi.TargetVM
	TargetStorage   MenuTarget = newapi.TargetStorage
	TargetShare     MenuTarget = newapi.TargetShare
	TargetNetwork   MenuTarget = newapi.TargetNetwork
	TargetFirewall  MenuTarget = newapi.TargetFirewall
	TargetService   MenuTarget = newapi.TargetService
	TargetDatabase  MenuTarget = newapi.TargetDatabase
)

// AllTargets returns the canonical list of supported context menu targets.
func AllTargets() []MenuTarget {
	return []MenuTarget{
		TargetNode,
		TargetContainer,
		TargetVM,
		TargetStorage,
		TargetShare,
		TargetNetwork,
		TargetFirewall,
		TargetService,
		TargetDatabase,
	}
}

// ValidateTarget checks whether t is a known context menu target.
func ValidateTarget(t MenuTarget) error {
	switch t {
	case TargetNode,
		TargetContainer,
		TargetVM,
		TargetStorage,
		TargetShare,
		TargetNetwork,
		TargetFirewall,
		TargetService,
		TargetDatabase:
		return nil
	case "":
		return fmt.Errorf("context menu target cannot be empty")
	default:
		return fmt.Errorf("unsupported context menu target %q", t)
	}
}

// ParseTarget normalizes a raw string into a validated MenuTarget.
// If raw is empty, it defaults to TargetNode.
func ParseTarget(raw string) (MenuTarget, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return TargetNode, nil
	}
	target := MenuTarget(trimmed)
	if err := ValidateTarget(target); err != nil {
		return "", err
	}
	return target, nil
}

// MenuItemKind distinguishes actionable items, submenus, and visual dividers.
type MenuItemKind string

const (
	ItemKindAction    MenuItemKind = "action"
	ItemKindSubmenu   MenuItemKind = "submenu"
	ItemKindSeparator MenuItemKind = "separator"
)

// MenuItem represents a single context menu item (action, submenu, or separator).
type MenuItem struct {
	ID             string         `json:"id"`
	PluginID       string         `json:"pluginId,omitempty"`
	Target         MenuTarget     `json:"target"`
	Kind           MenuItemKind   `json:"kind"`
	Label          string         `json:"label,omitempty"`
	Icon           string         `json:"icon,omitempty"`
	Group          string         `json:"group,omitempty"`
	Order          int            `json:"order"`
	Action         string         `json:"action,omitempty"`
	Shortcut       string         `json:"shortcut,omitempty"`
	ConfirmPrompt  string         `json:"confirmPrompt,omitempty"`
	Danger         bool           `json:"danger,omitempty"`
	Disabled       bool           `json:"disabled,omitempty"`
	DisabledReason string         `json:"disabledReason,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	Children       []MenuItem     `json:"children,omitempty"`
}

// MenuSection groups logically related MenuItems under a common section header.
type MenuSection struct {
	ID    string     `json:"id"`
	Label string     `json:"label,omitempty"`
	Order int        `json:"order"`
	Items []MenuItem `json:"items"`
}

// RenderedMenu is the complete context menu payload returned to the frontend
// for a given target, node, and optional resource identifier.
type RenderedMenu struct {
	Target     MenuTarget    `json:"target"`
	NodeID     string        `json:"nodeId,omitempty"`
	ResourceID string        `json:"resourceId,omitempty"`
	Sections   []MenuSection `json:"sections"`
	Items      []MenuItem    `json:"items"`
}

// RenderRequest specifies the filtering and context parameters for rendering a
// context menu.
type RenderRequest struct {
	Target     MenuTarget
	NodeID     string
	ResourceID string
	PluginID   string
}

// FromNewAPIEntry converts a newapi.ContextMenuEntry into a MenuItem.
func FromNewAPIEntry(e newapi.ContextMenuEntry) MenuItem {
	kind := ItemKindAction
	if len(e.Children) > 0 && e.Action == "" {
		kind = ItemKindSubmenu
	} else if e.Label == "-" || e.ID == "-" || strings.HasPrefix(e.ID, "sep-") || strings.HasPrefix(e.ID, "separator") {
		if e.Action == "" && len(e.Children) == 0 {
			kind = ItemKindSeparator
		}
	}

	var children []MenuItem
	if len(e.Children) > 0 {
		children = make([]MenuItem, 0, len(e.Children))
		for _, child := range e.Children {
			children = append(children, FromNewAPIEntry(child))
		}
	}

	target := e.Target
	if target == "" {
		target = TargetNode
	}

	return MenuItem{
		ID:             e.ID,
		PluginID:       e.PluginID,
		Target:         target,
		Kind:           kind,
		Label:          e.Label,
		Icon:           e.Icon,
		Group:          e.Group,
		Order:          e.Order,
		Action:         e.Action,
		ConfirmPrompt:  e.ConfirmPrompt,
		Danger:         e.Danger,
		Disabled:       e.Disabled,
		DisabledReason: e.DisabledReason,
		Children:       children,
	}
}

// ToNewAPIEntry converts a MenuItem into a newapi.ContextMenuEntry.
func ToNewAPIEntry(item MenuItem) newapi.ContextMenuEntry {
	var children []newapi.ContextMenuEntry
	if len(item.Children) > 0 {
		children = make([]newapi.ContextMenuEntry, 0, len(item.Children))
		for _, child := range item.Children {
			children = append(children, ToNewAPIEntry(child))
		}
	}

	return newapi.ContextMenuEntry{
		ID:             item.ID,
		PluginID:       item.PluginID,
		Target:         item.Target,
		Label:          item.Label,
		Icon:           item.Icon,
		Group:          item.Group,
		Order:          item.Order,
		Action:         item.Action,
		ConfirmPrompt:  item.ConfirmPrompt,
		Danger:         item.Danger,
		Disabled:       item.Disabled,
		DisabledReason: item.DisabledReason,
		Children:       children,
	}
}
