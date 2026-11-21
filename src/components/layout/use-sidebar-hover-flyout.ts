"use client"

import { useCallback, useEffect, useRef, useState } from "react"

/**
 * Hover-intent delay before the collapsed sidebar's preview opens: long enough
 * that driving the pointer across the strip never flashes the flyout, short
 * enough that a deliberate hover feels instant.
 */
export const SIDEBAR_FLYOUT_OPEN_DELAY_MS = 250
/**
 * Grace period after the pointer last left the keep zone. Crossing between the
 * strip, the flyout and their portal layers must never dismiss the preview, so
 * leaving only closes once the pointer has stayed outside for this long.
 */
export const SIDEBAR_FLYOUT_CLOSE_DELAY_MS = 200

/**
 * Elements that keep the preview open while the pointer is over them: the
 * trigger strip, the flyout itself, and any Radix portal layer spawned from
 * inside the flyout. Radix portals menus, hover cards and dialogs to `body`,
 * which breaks DOM-ancestry hover chains — a container-level mouseleave fires
 * the moment the pointer moves onto portaled content, so close decisions are
 * made from one document-level watcher that consults this selector instead.
 */
const FLYOUT_KEEP_ZONE_SELECTOR = [
  "[data-sidebar-flyout-trigger]",
  "[data-sidebar-hover-flyout]",
  "[data-radix-popper-content-wrapper]",
  "[data-radix-portal]",
  '[role="dialog"]',
  '[role="alertdialog"]',
  '[role="menu"]',
  '[role="listbox"]',
  '[role="tooltip"]',
].join(",")

/**
 * What the trigger strip in `FolderLayoutShell` (and tests) receive from the
 * controller. `open` is the transient hover-preview state — distinct from the
 * sidebar context's formal `isOpen`, which only the toggle button (and ⌘B)
 * flip.
 */
export interface SidebarHoverFlyout {
  open: boolean
  onTriggerEnter: () => void
  onTriggerLeave: () => void
  close: () => void
}

/**
 * Hover-preview controller for the collapsed left sidebar: hovering the fixed
 * window-chrome strip opens the full sidebar as a floating overlay; the
 * pointer leaving the keep zone (or pressing Escape) closes it.
 *
 * The preview is deliberately local state, not persisted panel state: it never
 * survives a re-mount and never touches `workspace:left-sidebar`. A formal
 * expand (toggle button / ⌘B) always wins — `isOpen` closing it here keeps the
 * two channels from fighting, and hover-while-open never arms the preview, so
 * collapsing later can't resurface a stale preview without a fresh hover.
 */
export function useSidebarHoverFlyout(isOpen: boolean): SidebarHoverFlyout {
  const [open, setOpen] = useState(false)
  const openRef = useRef(false)
  const isOpenRef = useRef(isOpen)
  const openTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const clearTimers = useCallback(() => {
    if (openTimerRef.current) {
      clearTimeout(openTimerRef.current)
      openTimerRef.current = null
    }
    if (closeTimerRef.current) {
      clearTimeout(closeTimerRef.current)
      closeTimerRef.current = null
    }
  }, [])

  const setOpenBoth = useCallback((value: boolean) => {
    openRef.current = value
    setOpen(value)
  }, [])

  const close = useCallback(() => {
    clearTimers()
    setOpenBoth(false)
  }, [clearTimers, setOpenBoth])

  // A formal expand supersedes the preview: closing here (not in render) keeps
  // the effect ordering honest and clears any pending timer with it. The
  // explicit setState is the point — isOpen flipping true IS the external
  // change this effect responds to (same pattern as SidebarProvider hydration).
  useEffect(() => {
    isOpenRef.current = isOpen
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (isOpen) close()
  }, [isOpen, close])

  // Escape dismisses the preview — but only Escapes Radix did not consume. A
  // menu opened inside the flyout marks the keydown defaultPrevented (Radix
  // listens on capture and never stops propagation), so one press closes the
  // menu and the next press closes this.
  useEffect(() => {
    if (!open) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return
      if (event.key === "Escape") close()
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [open, close])

  const scheduleClose = useCallback(() => {
    if (openTimerRef.current) {
      clearTimeout(openTimerRef.current)
      openTimerRef.current = null
    }
    if (closeTimerRef.current || !openRef.current) return
    closeTimerRef.current = setTimeout(() => {
      closeTimerRef.current = null
      setOpenBoth(false)
    }, SIDEBAR_FLYOUT_CLOSE_DELAY_MS)
  }, [setOpenBoth])

  const cancelClose = useCallback(() => {
    if (closeTimerRef.current) {
      clearTimeout(closeTimerRef.current)
      closeTimerRef.current = null
    }
  }, [])

  // While the preview is up, one document-level watcher decides everything:
  // over the strip, the flyout or a portal layer → stay; anywhere else → arm
  // the grace close. Container-level mouseleave cannot do this — portaled
  // Radix layers (row hover cards, view options, context menus, rename/delete
  // dialogs) leave the DOM subtree, and a MODAL layer additionally sets
  // `pointer-events: none` on body, which makes Chrome retarget hover to body
  // with the pointer perfectly stationary.
  useEffect(() => {
    if (!open) return
    const onPointerOver = (event: MouseEvent) => {
      // A modal Radix layer is up: defer every decision until it is gone (the
      // click that dismisses it fires a fresh mouseover that re-judges).
      if (document.body.style.pointerEvents === "none") return
      const target = event.target
      if (!(target instanceof Element)) return
      if (target.closest(FLYOUT_KEEP_ZONE_SELECTOR)) {
        cancelClose()
        return
      }
      scheduleClose()
    }
    document.addEventListener("mouseover", onPointerOver)
    return () => document.removeEventListener("mouseover", onPointerOver)
  }, [open, cancelClose, scheduleClose])

  useEffect(() => clearTimers, [clearTimers])

  const onTriggerEnter = useCallback(() => {
    // Re-entering the strip while the preview is up just cancels the pending
    // close — the strip sits over the flyout, so this is the stay-open path.
    if (closeTimerRef.current) {
      clearTimeout(closeTimerRef.current)
      closeTimerRef.current = null
    }
    if (openRef.current || openTimerRef.current || isOpenRef.current) return
    openTimerRef.current = setTimeout(() => {
      openTimerRef.current = null
      setOpenBoth(true)
    }, SIDEBAR_FLYOUT_OPEN_DELAY_MS)
  }, [setOpenBoth])

  return {
    open,
    onTriggerEnter,
    // Leaving the strip only cancels a pending OPEN. Once the preview is up,
    // the document watcher above owns close decisions.
    onTriggerLeave: useCallback(() => {
      if (openTimerRef.current) {
        clearTimeout(openTimerRef.current)
        openTimerRef.current = null
      }
    }, []),
    close,
  }
}
