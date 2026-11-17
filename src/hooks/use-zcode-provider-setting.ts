"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import {
  acpListAgents,
  acpUpdateAgentEnv,
  acpZcodeListProviders,
} from "@/lib/api"
import {
  zcodeProviderSetting,
  type SessionChoiceSetting,
} from "@/lib/session-settings"
import type { AcpAgentInfo, ZcodeProviderOption } from "@/lib/types"

const ZCODE_PROVIDER_ENV = "ZCODE_PROVIDER"

export interface ZcodeProviderLabels {
  title: string
  autoLabel: string
  unselectableLabel: string
  /** Toast shown after a successful pin. `{provider}` is the chosen name. */
  pinnedToast: string
  /** Toast shown after going back to auto. */
  autoToast: string
  failedToast: string
  /** Toast explaining that a reconnect is needed for the pin to take effect. */
  reconnectHint: string
}

/**
 * The ZCode provider entry for the composer's model/settings popover.
 *
 * `ZCODE_PROVIDER` is a launch-time env, NOT an ACP config option: picking one
 * writes the zcode agent's env and leaves the running connection on config-stale,
 * so the session's "Reconnect to apply" banner is what actually activates it.
 * This hook owns that one write and the read that feeds the chip; the returned
 * `setting` is a normal `SessionChoiceSetting` the popover already knows how to
 * render. Everything degrades to "auto only" on a failed read, and non-zcode
 * agents get nothing (no fetch, no setting).
 */
export function useZcodeProviderSetting(
  agentType: string | null | undefined,
  labels: ZcodeProviderLabels
) {
  const isZcode = agentType === "zcode"
  const [providers, setProviders] = useState<ZcodeProviderOption[]>([])
  const [pinned, setPinned] = useState("")
  const [loaded, setLoaded] = useState(false)
  const agentRef = useRef<AcpAgentInfo | null>(null)

  const refresh = useCallback(async () => {
    if (!isZcode) return
    try {
      const [list, agents] = await Promise.all([
        acpZcodeListProviders().catch(() => [] as ZcodeProviderOption[]),
        acpListAgents().catch(() => [] as AcpAgentInfo[]),
      ])
      const zcode = agents.find((agent) => agent.agent_type === "zcode") || null
      agentRef.current = zcode
      setProviders(Array.isArray(list) ? list : [])
      setPinned(String(zcode?.env?.[ZCODE_PROVIDER_ENV] ?? "").trim())
    } finally {
      setLoaded(true)
    }
  }, [isZcode])

  useEffect(() => {
    if (!isZcode) return
    void refresh()
  }, [isZcode, refresh])

  const pin = useCallback(
    async (providerId: string) => {
      const next = String(providerId || "").trim()
      const agent = agentRef.current
      if (!agent) {
        toast.error(labels.failedToast)
        return
      }
      const env = { ...(agent.env || {}) }
      if (next) env[ZCODE_PROVIDER_ENV] = next
      else delete env[ZCODE_PROVIDER_ENV]
      try {
        await acpUpdateAgentEnv("zcode", {
          enabled: agent.enabled,
          env,
          modelProviderId: agent.model_provider_id ?? null,
        })
        agentRef.current = { ...agent, env }
        setPinned(next)
        const name = next
          ? providers.find((item) => item.id === next)?.name || next
          : ""
        toast.success(next ? labels.pinnedToast.replace("{provider}", name) : labels.autoToast)
        // The write marked this connection config-stale; its banner offers the
        // reconnect. Say so explicitly rather than leaving the user to notice.
        toast.message(labels.reconnectHint)
      } catch {
        toast.error(labels.failedToast)
      }
    },
    [labels, providers]
  )

  const setting: SessionChoiceSetting | null =
    isZcode && loaded
      ? zcodeProviderSetting({
          providers,
          pinned,
          title: labels.title,
          autoLabel: labels.autoLabel,
          unselectableLabel: labels.unselectableLabel,
        })
      : null

  return { setting, pin }
}
