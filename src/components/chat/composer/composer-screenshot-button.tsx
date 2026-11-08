"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslations } from "next-intl"
import { Camera, Monitor } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { isLocalDesktop } from "@/lib/platform"
import { extractAppCommandError } from "@/lib/app-error"
import { randomUUID } from "@/lib/utils"
import {
  screenshotBeginSelection,
  screenshotRequestPermission,
} from "@/lib/tauri"

/** Hover this long before the menu opens on its own: a pass over the button
 *  on the way somewhere else must not flash a menu. A click opens it at once. */
const HOVER_OPEN_DELAY_MS = 200

export interface ComposerScreenshotButtonProps {
  disabled?: boolean
}

/**
 * The composer's screenshot button, sitting right of the "+" menu — desktop
 * only, so web and remote-desktop windows render nothing here.
 *
 * Hovering opens a small menu with two ways to capture: with the window up,
 * or with the main window hidden first so it cannot photobomb the picture.
 * The selection, annotation and clipboard copy all happen in the backend's
 * own overlay windows, so this component only starts the flow — a finished
 * capture needs no reaction from the main window.
 */
export function ComposerScreenshotButton({
  disabled = false,
}: ComposerScreenshotButtonProps) {
  const t = useTranslations("Folder.chat.messageInput")
  const te = useTranslations("Folder.chat.screenshotEditor")
  const [menuOpen, setMenuOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const hoverTimerRef = useRef<number | null>(null)

  const clearHoverTimer = useCallback(() => {
    if (hoverTimerRef.current !== null) {
      window.clearTimeout(hoverTimerRef.current)
      hoverTimerRef.current = null
    }
  }, [])

  useEffect(() => clearHoverTimer, [clearHoverTimer])

  const failureLabel = useCallback(
    (error: unknown) => {
      const message = error instanceof Error ? error.message : String(error)
      // macOS denies screen capture with a permission-shaped error (TCC);
      // name the missing grant instead of a generic failure.
      return /permission|权限|screen recording/i.test(message)
        ? te("permissionDenied")
        : te("failed")
    },
    [te]
  )

  const requestPermission = useCallback(async () => {
    setBusy(true)
    try {
      await screenshotRequestPermission()
      // The system may still be waiting for a decision. Only the next
      // explicit capture checks the grant; asking never assumes success.
    } catch {
      toast.error(te("failed"))
    } finally {
      setBusy(false)
    }
  }, [te])

  const beginCapture = useCallback(
    async (hideMain: boolean) => {
      setMenuOpen(false)
      setBusy(true)
      const requestId = randomUUID()
      try {
        await screenshotBeginSelection(requestId, hideMain)
      } catch (error) {
        const appError = extractAppCommandError(error)
        if (appError?.code === "permission_denied") {
          toast.error(te("permissionDenied"), {
            action: {
              label: te("requestPermission"),
              onClick: () => void requestPermission(),
            },
          })
        } else {
          toast.error(failureLabel(error))
        }
      } finally {
        setBusy(false)
      }
    },
    [failureLabel, requestPermission, te]
  )

  const handleTriggerMouseEnter = useCallback(() => {
    if (disabled) return
    clearHoverTimer()
    hoverTimerRef.current = window.setTimeout(() => {
      hoverTimerRef.current = null
      setMenuOpen(true)
    }, HOVER_OPEN_DELAY_MS)
  }, [clearHoverTimer, disabled])

  if (!isLocalDesktop()) return null

  return (
    <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          disabled={disabled || busy}
          variant="ghost"
          size="icon-xs"
          className="shrink-0 text-muted-foreground"
          title={t("screenshot")}
          aria-label={t("screenshot")}
          onMouseEnter={handleTriggerMouseEnter}
          onMouseLeave={clearHoverTimer}
          onClick={clearHoverTimer}
        >
          <Camera className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="min-w-44">
        <DropdownMenuItem
          disabled={disabled || busy}
          onClick={() => void beginCapture(false)}
        >
          <Camera className="size-4" />
          {t("screenshot")}
        </DropdownMenuItem>
        <DropdownMenuItem
          disabled={disabled || busy}
          onClick={() => void beginCapture(true)}
        >
          <Monitor className="size-4" />
          {t("screenshotHideWindow")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
