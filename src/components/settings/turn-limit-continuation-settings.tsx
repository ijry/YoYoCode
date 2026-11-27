"use client"

/**
 * What the backend does when a provider reports that the session hit its
 * model turn limit. The computer owns the recovery, so this setting lives on
 * the desktop settings page; mobile reaches the same value through the HTTP
 * mirror of the Tauri commands.
 *
 * "compact" is deliberately labelled unverified in the UI: compaction keeps
 * the same agent session and there is no code path here that resets a
 * provider-side turn counter. It may still be the user's preferred attempt.
 */

import { useCallback, useEffect, useState } from "react"
import { useTranslations } from "next-intl"
import { RefreshCw } from "lucide-react"
import { toast } from "sonner"

import { SettingsSection } from "@/components/shared/settings-section"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  getTurnLimitContinuationSettings,
  updateTurnLimitContinuationSettings,
} from "@/lib/api"
import { toErrorMessage } from "@/lib/app-error"
import type { TurnLimitContinuationMode } from "@/lib/types"

const MODE_IDS = ["off", "compact", "new_session"] as const

const LABEL_KEYS = {
  off: "optionOff",
  compact: "optionCompact",
  new_session: "optionNewSession",
} as const satisfies Record<TurnLimitContinuationMode, string>

export function TurnLimitContinuationSettingsSection() {
  const t = useTranslations("TurnLimitContinuationSettings")
  const [mode, setMode] = useState<TurnLimitContinuationMode | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    getTurnLimitContinuationSettings()
      .then((settings) => {
        if (!cancelled) setMode(settings.mode)
      })
      .catch((err) => {
        console.error("[Settings] load turn-limit recovery failed:", err)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const save = useCallback(
    async (
      next: TurnLimitContinuationMode,
      prev: TurnLimitContinuationMode
    ) => {
      setSaving(true)
      try {
        const result = await updateTurnLimitContinuationSettings(next)
        setMode(result.mode)
      } catch (err) {
        setMode(prev)
        toast.error(t("saveFailed", { message: toErrorMessage(err) }))
      } finally {
        setSaving(false)
      }
    },
    [t]
  )

  // Hidden until loaded: the picker replaces the whole stored value, so a
  // guessed default would invite the user to "confirm" a choice they never made.
  if (mode === null) return null

  return (
    <SettingsSection
      icon={RefreshCw}
      title={t("title")}
      description={
        mode === "compact"
          ? `${t("description")} ${t("compactWarning")}`
          : t("description")
      }
      htmlFor="turn-limit-continuation-mode"
      control={
        <Select
          value={mode}
          disabled={saving}
          onValueChange={(next) => {
            const prev = mode
            const value = next as TurnLimitContinuationMode
            setMode(value)
            void save(value, prev)
          }}
        >
          <SelectTrigger
            id="turn-limit-continuation-mode"
            size="sm"
            className="w-52 bg-background text-xs"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="end">
            {MODE_IDS.map((id) => (
              <SelectItem key={id} value={id}>
                {t(LABEL_KEYS[id])}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      }
    />
  )
}
