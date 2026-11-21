"use client"

import { useSidebarContext } from "@/contexts/sidebar-context"
import { cn } from "@/lib/utils"

import { Sidebar } from "./sidebar"
import type { SidebarHoverFlyout } from "./use-sidebar-hover-flyout"

/**
 * The collapsed sidebar's hover preview: the full sidebar rendered in a fixed
 * column that overlays the content area (never re-opens the resizable panel,
 * which stays at width 0). Sits under the fixed window-chrome strip (z-50), so
 * the toggle/search buttons stay visible and clickable over its top edge —
 * the same stacking the real sidebar has while open.
 *
 * Close decisions belong to the controller's document-level pointer watcher
 * (see `useSidebarHoverFlyout`); this container only marks itself as part of
 * the keep zone. Clicking a conversation row navigates AND dismisses the
 * preview (the same `[data-conversation-id]` marker the mobile Drawer closes
 * on); everything else — dropdowns, view options — stays interactive until the
 * pointer leaves.
 */
export function SidebarFlyout({ flyout }: { flyout: SidebarHoverFlyout }) {
  const { isOpen, width } = useSidebarContext()
  if (isOpen || !flyout.open) return null

  return (
    <div
      data-testid="sidebar-flyout"
      data-sidebar-hover-flyout=""
      className={cn(
        "fixed inset-y-0 left-0 z-40 flex flex-col ws-surface-sidebar",
        "border-r border-border/50 shadow-xl",
        "animate-in slide-in-from-left fade-in duration-200",
        "motion-reduce:animate-none"
      )}
      style={{ width }}
      onClick={(event) => {
        const target = event.target as HTMLElement
        if (target.closest("[data-conversation-id]")) flyout.close()
      }}
    >
      <SidebarFlyoutInstance />
    </div>
  )
}

// A stable element identity for the flyout's Sidebar instance: keeping it a
// named component (like layout.tsx does for the panel instance) avoids
// re-creating the subtree element on every FolderLayoutShell render.
function SidebarFlyoutInstance() {
  return <Sidebar variant="flyout" flyoutOpen />
}
