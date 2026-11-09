"use client"

import { useEffect, useState } from "react"
import { BookOpen, Clapperboard, ExternalLink, Loader2 } from "lucide-react"
import { useTranslations } from "next-intl"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { comicApi, type GenerationTaskView } from "@/lib/comic"
import { toErrorMessage } from "@/lib/app-error"
import {
  subscribeComicChanged,
  useComicStore,
  type ComicPanelKind,
} from "@/stores/comic-store"

export function ComicProjectOverview({
  folderId,
  onOpenWorkbench,
}: {
  folderId: number
  onOpenWorkbench: (panel?: ComicPanelKind) => void
}) {
  const t = useTranslations("Comic")
  const overview = useComicStore((state) => state.overviews[folderId])
  const loading = useComicStore((state) => state.loading[folderId])
  const error = useComicStore((state) => state.errors[folderId])
  const load = useComicStore((state) => state.load)
  const [taskState, setTaskState] = useState<{
    folderId: number
    tasks: GenerationTaskView[]
    error: string | null
  }>({ folderId, tasks: [], error: null })

  useEffect(() => {
    let disposed = false
    let latest = 0
    const refresh = () => {
      void load(folderId)
      const request = ++latest
      void comicApi.listTasks(folderId, 8).then(
        (tasks) => {
          if (!disposed && request === latest) {
            setTaskState({ folderId, tasks, error: null })
          }
        },
        (err: unknown) => {
          if (!disposed && request === latest) {
            setTaskState({
              folderId,
              tasks: [],
              error: toErrorMessage(err),
            })
          }
        }
      )
    }
    refresh()
    const unsubscribe = subscribeComicChanged((changedFolderId) => {
      if (changedFolderId === folderId) refresh()
    })
    return () => {
      disposed = true
      unsubscribe()
    }
  }, [folderId, load])

  const tasks = taskState.folderId === folderId ? taskState.tasks : []
  const taskError = taskState.folderId === folderId ? taskState.error : null
  const assetGroups = overview
    ? [
        {
          kind: "character" as const,
          label: t("characters"),
          cards: overview.characters,
        },
        { kind: "scene" as const, label: t("scenes"), cards: overview.scenes },
        { kind: "prop" as const, label: t("props"), cards: overview.props },
      ]
    : []

  return (
    <section
      className="h-full min-h-0 space-y-4 overflow-y-auto p-3 text-sm"
      aria-label={t("overviewTitle")}
    >
      <div className="space-y-2">
        <h3 className="truncate font-semibold" title={overview?.series?.title}>
          {overview?.series?.title ?? t("title")}
        </h3>
        <Button
          className="w-full"
          size="sm"
          variant="outline"
          onClick={() => onOpenWorkbench()}
        >
          <ExternalLink className="size-3.5" />
          {t("openWorkbench")}
        </Button>
      </div>
      {loading && !overview && (
        <div className="flex justify-center py-4" role="status">
          <Loader2 className="size-4 animate-spin text-muted-foreground" />
        </div>
      )}
      {error && (
        <p role="alert" className="break-words text-xs text-destructive">
          {error}
        </p>
      )}
      {!overview && !loading && (
        <Button variant="ghost" size="sm" onClick={() => onOpenWorkbench()}>
          <Clapperboard className="size-3.5" />
          {t("initProject")}
        </Button>
      )}
      {overview && (
        <>
          <div className="space-y-1">
            <ResourceButton onClick={() => onOpenWorkbench({ kind: "source" })}>
              <BookOpen className="size-3.5 shrink-0" />
              <span className="min-w-0 flex-1 truncate">{t("source")}</span>
              {!overview.has_source && (
                <Badge variant="outline">{t("sourceMissing")}</Badge>
              )}
            </ResourceButton>
            <ResourceButton
              onClick={() => onOpenWorkbench({ kind: "providers" })}
            >
              <span className="min-w-0 flex-1 truncate">{t("providers")}</span>
            </ResourceButton>
          </div>
          <div className="space-y-1">
            <h4 className="text-xs font-medium text-muted-foreground">
              {t("episodes")} · {overview.episodes.length}
            </h4>
            {overview.episodes.map((episode) => (
              <ResourceButton
                key={episode.id}
                onClick={() =>
                  onOpenWorkbench({ kind: "storyboard", episodeId: episode.id })
                }
              >
                <span className="min-w-0 flex-1 truncate">
                  {episode.title || episode.id}
                </span>
                <span className="shrink-0 text-xs text-muted-foreground">
                  {episode.shots_done}/{episode.shots_total}
                </span>
              </ResourceButton>
            ))}
          </div>
          {assetGroups.map(({ kind, label, cards }) => (
            <div key={kind} className="space-y-1">
              <h4 className="text-xs font-medium text-muted-foreground">
                {label} · {cards.length}
              </h4>
              {cards.map((card) => (
                <ResourceButton
                  key={card.slug}
                  onClick={() =>
                    onOpenWorkbench({
                      kind: "asset",
                      assetKind: kind,
                      slug: card.slug,
                    })
                  }
                >
                  <span className="min-w-0 flex-1 truncate">
                    {card.name || card.slug}
                  </span>
                  {!card.has_ref_images && (
                    <Badge variant="outline">{t("noRefs")}</Badge>
                  )}
                </ResourceButton>
              ))}
            </div>
          ))}
        </>
      )}
      <div className="space-y-2 border-t pt-3">
        <h4 className="text-xs font-medium text-muted-foreground">
          {t("recentTasks")}
        </h4>
        {taskError && (
          <p role="alert" className="break-words text-xs text-destructive">
            {taskError}
          </p>
        )}
        {tasks.length === 0 && !taskError && (
          <p className="text-xs text-muted-foreground">{t("noTasks")}</p>
        )}
        {tasks.map((task) => (
          <div key={task.id} className="space-y-1 rounded-md border p-2">
            <div className="flex items-center justify-between gap-2 text-xs">
              <span className="truncate">
                #{task.id} · {task.kind}
              </span>
              <Badge variant="outline">{task.status}</Badge>
            </div>
            {task.targetId && (
              <p className="truncate text-xs" title={task.targetId}>
                {task.targetId}
              </p>
            )}
            {task.providerName && (
              <p className="truncate text-xs text-muted-foreground">
                {task.providerName}
              </p>
            )}
            {task.errorMessage && (
              <p className="break-words text-xs text-destructive">
                {task.errorMessage}
              </p>
            )}
          </div>
        ))}
      </div>
    </section>
  )
}

function ResourceButton({
  children,
  onClick,
}: {
  children: React.ReactNode
  onClick: () => void
}) {
  return (
    <button
      type="button"
      className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-start hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      onClick={onClick}
    >
      {children}
    </button>
  )
}
