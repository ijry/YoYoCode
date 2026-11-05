"use client"

import { useCallback } from "react"
import { useTranslations } from "next-intl"

import { ComicProjectOverview } from "@/components/comic/comic-project-overview"
import { useActiveFolder } from "@/contexts/active-folder-context"
import { useAuxPanelContext } from "@/contexts/aux-panel-context"
import { useWorkspaceActions } from "@/contexts/workspace-context"
import { useIsMobile } from "@/hooks/use-mobile"
import { useComicStore, type ComicPanelKind } from "@/stores/comic-store"

export function ComicAuxTab() {
  const t = useTranslations("Comic")
  const { activeFolderId } = useActiveFolder()
  const { openComicWorkbench } = useWorkspaceActions()
  const { setOpen } = useAuxPanelContext()
  const isMobile = useIsMobile()
  const openWorkbench = useCallback(
    (panel?: ComicPanelKind) => {
      if (activeFolderId == null) return
      if (panel) useComicStore.getState().openPanel(activeFolderId, panel)
      openComicWorkbench(activeFolderId)
      if (isMobile) setOpen(false)
    },
    [activeFolderId, openComicWorkbench, isMobile, setOpen]
  )

  if (activeFolderId == null) {
    return (
      <p className="p-3 text-sm text-muted-foreground">{t("noFolderActive")}</p>
    )
  }
  return (
    <ComicProjectOverview
      key={activeFolderId}
      folderId={activeFolderId}
      onOpenWorkbench={openWorkbench}
    />
  )
}
