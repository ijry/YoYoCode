"use client"

import { useCallback } from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"

import { useActiveFolder } from "@/contexts/active-folder-context"
import { useOptionalWorkspaceActions } from "@/contexts/workspace-context"
import { getAgentLabel } from "@/lib/custom-agents"
import type { AgentType } from "@/lib/types"

export const COMIC_PRODUCTION_SKILL_ID = "comic-production"

/** These adapters cannot receive the built-in companion: connection.rs's
 * agent_delivers_wire_mcp excludes Pi/Cline, registry.rs disables OpenClaw MCP.
 * Other agents are deliberately NOT reported as tool-ready: skill installation
 * and even accepting MCP do not prove that a particular live session has tools.
 */
export function comicEntryIsManualOnly(agentType: AgentType | null): boolean {
  return (
    agentType === "pi" || agentType === "cline" || agentType === "open_claw"
  )
}

/** UI-only entry. It neither initializes a project nor sends an agent prompt.
 * Optional workspace actions keep the shared task composer usable outside the
 * workspace shell. The workbench handles its existing empty-project screen.
 */
export function useComicProductionEntry(agentType: AgentType | null) {
  const { activeFolderId, activeFolder } = useActiveFolder()
  const hasProjectFolder = activeFolder != null && activeFolder.kind !== "chat"
  const workspace = useOptionalWorkspaceActions()
  const t = useTranslations("Folder.chat.welcomePanel.quickActions")
  const manualOnly = comicEntryIsManualOnly(agentType)

  const open = useCallback(() => {
    if (hasProjectFolder && activeFolderId != null && workspace) {
      workspace.openComicWorkbench(activeFolderId)
    } else {
      toast.warning(t("comicEntry.folderRequired"))
    }
    if (manualOnly && agentType) {
      toast.warning(
        t("comicEntry.unsupported", { agent: getAgentLabel(agentType) })
      )
    } else {
      toast.info(t("comicEntry.checkTools"))
    }
  }, [activeFolderId, hasProjectFolder, workspace, t, manualOnly, agentType])

  return { manualOnly, open }
}
