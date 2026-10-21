/**
 * Shared model for reusable right-click context menus.
 *
 * Views build a menu description with the `build*ContextMenu` helpers and hand it
 * to the `useContextMenu` hook (see ../components/ContextMenu.tsx). The builders only
 * describe structure + wire callbacks that already exist in the view, so no host
 * action logic is duplicated here.
 */

export interface ContextMenuItem {
  /** Stable id, used for keyboard focus tracking and tests. */
  id: string;
  label: string;
  /** Material Symbols icon name. */
  icon?: string;
  /** Right-aligned hint text (shortcut or type hint). */
  hint?: string;
  disabled?: boolean;
  /** Renders the item in the destructive accent colour. */
  danger?: boolean;
  /** Draws a divider above this item. */
  separatorBefore?: boolean;
  /** Performs the real action. Handlers are supplied by the owning view. */
  onSelect: () => void | Promise<void>;
}

export interface MenuPosition {
  x: number;
  y: number;
}

export interface MenuSize {
  width: number;
  height: number;
}

export interface ViewportSize {
  width: number;
  height: number;
}

/** Estimated width of the menu panel, mirrors the ContextMenu styles. */
export const MENU_WIDTH_PX = 236;
/** Row height including vertical padding. */
export const MENU_ITEM_HEIGHT_PX = 28;
/** Vertical padding of the panel plus its border. */
export const MENU_PADDING_PX = 10;
/** Extra divider height per separator. */
export const MENU_SEPARATOR_PX = 7;
/** Keep the panel this far from the viewport edges. */
export const MENU_VIEWPORT_MARGIN_PX = 8;

/** Best-effort size used before the real element has been measured. */
export function estimateMenuSize(items: ContextMenuItem[]): MenuSize {
  let height = MENU_PADDING_PX;
  for (const item of items) {
    height += MENU_ITEM_HEIGHT_PX + (item.separatorBefore ? MENU_SEPARATOR_PX : 0);
  }
  return { width: MENU_WIDTH_PX, height };
}

/**
 * Clamp a preferred top-left position so the panel stays fully inside the viewport.
 * Falls back to the margin when the panel is larger than the viewport.
 */
export function clampMenuPosition(
  position: MenuPosition,
  size: MenuSize,
  viewport: ViewportSize,
  margin: number = MENU_VIEWPORT_MARGIN_PX
): MenuPosition {
  const maxX = viewport.width - size.width - margin;
  const maxY = viewport.height - size.height - margin;

  const x = maxX >= margin ? Math.min(Math.max(position.x, margin), maxX) : margin;
  const y = maxY >= margin ? Math.min(Math.max(position.y, margin), maxY) : margin;

  return { x: Math.round(x), y: Math.round(y) };
}

export function isContainerRunning(status: string | undefined): boolean {
  if (!status) return false;
  const s = status.toLowerCase();
  return !(s.includes('exited') || s.includes('stopped') || s.includes('created') || s.includes('dead'));
}

/** Copy helpers shared by every menu builder. */
export interface CopyAction {
  (text: string, label: string): void;
}

function copyItem(id: string, label: string, value: string | undefined, onCopy: CopyAction, icon = 'content_copy'): ContextMenuItem {
  return {
    id,
    label,
    icon,
    disabled: !value,
    onSelect: () => onCopy(value ?? '', label.replace(/^Copy\s+/, '')),
  };
}

// ---------------------------------------------------------------------------
// Node / host rows
// ---------------------------------------------------------------------------

export interface NodeMenuActions {
  onSelect?: (node: any) => void;
  onOpenTerminal?: (node: any) => void;
  onOpenLogs?: (node: any) => void;
  onEdit?: (node: any) => void;
  onDelete?: (node: any) => void;
  onCopy?: CopyAction;
}

export function buildNodeContextMenu(node: any, actions: NodeMenuActions): ContextMenuItem[] {
  const items: ContextMenuItem[] = [];
  const onCopy = actions.onCopy;

  if (actions.onSelect) {
    items.push({
      id: 'node.select',
      label: 'Select Node',
      icon: 'ads_click',
      onSelect: () => actions.onSelect!(node),
    });
  }

  if (actions.onOpenTerminal) {
    items.push({
      id: 'node.terminal',
      label: 'Open Terminal',
      icon: 'terminal',
      hint: node?.tailscaleHost || node?.ip || undefined,
      onSelect: () => actions.onOpenTerminal!(node),
    });
  }

  if (actions.onOpenLogs) {
    items.push({
      id: 'node.logs',
      label: 'Open System Logs',
      icon: 'article',
      onSelect: () => actions.onOpenLogs!(node),
    });
  }

  if (onCopy) {
    items.push(copyItem('node.copyName', 'Copy Name', node?.name, onCopy));
    items.push(copyItem('node.copyId', 'Copy Node ID', node?.id, onCopy));
    items.push(copyItem('node.copyIp', 'Copy IP Address', node?.tailscaleIp || node?.ip, onCopy));
    if (node?.tailscaleHost) {
      items.push(copyItem('node.copyTailscaleHost', 'Copy Tailscale Host', node.tailscaleHost, onCopy));
    }
  }

  if (actions.onEdit) {
    items.push({
      id: 'node.edit',
      label: 'Edit Node…',
      icon: 'edit',
      separatorBefore: true,
      onSelect: () => actions.onEdit!(node),
    });
  }

  if (actions.onDelete) {
    items.push({
      id: 'node.delete',
      label: 'Remove From Inventory',
      icon: 'delete',
      danger: true,
      onSelect: () => actions.onDelete!(node),
    });
  }

  return items;
}

// ---------------------------------------------------------------------------
// Container rows (Podman / Docker)
// ---------------------------------------------------------------------------

export interface ContainerMenuActions {
  onStart?: (container: any) => void;
  onStop?: (container: any) => void;
  onRestart?: (container: any) => void;
  onExec?: (container: any) => void;
  onLogs?: (container: any) => void;
  onCopy?: CopyAction;
}

export function buildContainerContextMenu(container: any, actions: ContainerMenuActions): ContextMenuItem[] {
  const running = isContainerRunning(container?.status);
  const items: ContextMenuItem[] = [];

  if (actions.onStart) {
    items.push({
      id: 'container.start',
      label: 'Start Container',
      icon: 'play_arrow',
      disabled: running,
      onSelect: () => actions.onStart!(container),
    });
  }

  if (actions.onStop) {
    items.push({
      id: 'container.stop',
      label: 'Stop Container',
      icon: 'stop_circle',
      disabled: !running,
      onSelect: () => actions.onStop!(container),
    });
  }

  if (actions.onRestart) {
    items.push({
      id: 'container.restart',
      label: 'Restart Container',
      icon: 'restart_alt',
      disabled: !running,
      onSelect: () => actions.onRestart!(container),
    });
  }

  if (actions.onExec) {
    items.push({
      id: 'container.exec',
      label: 'Open Exec Session',
      icon: 'terminal',
      separatorBefore: true,
      onSelect: () => actions.onExec!(container),
    });
  }

  if (actions.onLogs) {
    items.push({
      id: 'container.logs',
      label: 'View Container Logs',
      icon: 'article',
      onSelect: () => actions.onLogs!(container),
    });
  }

  if (actions.onCopy) {
    items.push(copyItem('container.copyName', 'Copy Container Name', container?.name, actions.onCopy));
    if (container?.id) {
      items.push(copyItem('container.copyId', 'Copy Container ID', container.id, actions.onCopy));
    }
    items.push(copyItem('container.copyImage', 'Copy Image Reference', container?.image, actions.onCopy));
  }

  return items;
}

// ---------------------------------------------------------------------------
// Task rows (console execution log)
// ---------------------------------------------------------------------------

export interface TaskMenuActions {
  onOpenDetails?: (task: any) => void;
  onCopy?: CopyAction;
}

export function buildTaskContextMenu(task: any, actions: TaskMenuActions): ContextMenuItem[] {
  const items: ContextMenuItem[] = [];

  if (actions.onOpenDetails) {
    items.push({
      id: 'task.details',
      label: 'Open Task Details',
      icon: 'open_in_new',
      onSelect: () => actions.onOpenDetails!(task),
    });
  }

  if (actions.onCopy) {
    items.push(copyItem('task.copyId', 'Copy Task ID', task?.id, actions.onCopy));
    items.push(copyItem('task.copyDescription', 'Copy Description', task?.description, actions.onCopy));
    items.push(copyItem('task.copyTarget', 'Copy Target Host', task?.nodeTarget, actions.onCopy));
  }

  return items;
}

// ---------------------------------------------------------------------------
// Share rows (NFS / Samba)
// ---------------------------------------------------------------------------

export interface ShareMenuActions {
  onEdit?: (share: any) => void;
  onRefresh?: (share: any) => void;
  onCopy?: CopyAction;
}

export function buildShareContextMenu(share: any, actions: ShareMenuActions): ContextMenuItem[] {
  const items: ContextMenuItem[] = [];

  if (actions.onEdit) {
    items.push({
      id: 'share.edit',
      label: 'Edit Share…',
      icon: 'edit',
      onSelect: () => actions.onEdit!(share),
    });
  }

  if (actions.onRefresh) {
    items.push({
      id: 'share.refresh',
      label: 'Refresh Share List',
      icon: 'refresh',
      onSelect: () => actions.onRefresh!(share),
    });
  }

  if (actions.onCopy) {
    items.push(copyItem('share.copyName', 'Copy Share Name', share?.name, actions.onCopy));
    items.push(copyItem('share.copyPath', 'Copy Exported Path', share?.path, actions.onCopy));
    items.push(copyItem('share.copyProtocol', 'Copy Protocol', share?.protocol, actions.onCopy));
  }

  return items;
}
