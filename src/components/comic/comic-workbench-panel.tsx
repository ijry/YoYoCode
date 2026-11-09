"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ArrowDown,
  ArrowUp,
  BookOpen,
  Clapperboard,
  FileText,
  Film,
  MoreHorizontal,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Save,
  Trash2,
  Wand2,
  X,
} from "lucide-react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"

import { ComicImage } from "./comic-image"
import { ComicEpisodeManager } from "./comic-episode-manager"
import { ComicStoryboardImportDialog } from "./comic-storyboard-import-dialog"
import { toErrorMessage } from "@/lib/app-error"
import { readComicUpload } from "@/lib/comic-upload"
import { openComicExport, openComicShotClip } from "@/lib/comic-export"
import {
  resolveComicVideoPreview,
  type ComicVideoPreview,
} from "@/lib/comic-video-preview"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { cn } from "@/lib/utils"
import { comicEntryIsManualOnly } from "@/lib/comic-production-entry"
import {
  comicApi,
  type Chapter,
  type CharacterCard,
  type ComicOverviewPayload,
  type GenerationTaskView,
  type SceneCard,
  type SourceReport,
  type StoryShot,
  type WindowRead,
} from "@/lib/comic"
import { useActiveFolder } from "@/contexts/active-folder-context"
import { useTabStore } from "@/stores/tab-store"
import { requestComicConversationTask } from "@/lib/comic-session-bridge"
import {
  subscribeComicChanged,
  useComicStore,
  type ComicPanelKind,
  type ComicShotDraft,
  type ComicResourceDraft,
} from "@/stores/comic-store"

/**
 * The comic (漫剧) workbench: a global observation surface over one project's
 * `story/` tree plus quick actions. Production itself is conversation-driven —
 * quick actions queue requests in the current conversation; mechanical edits (storyboard fields,
 * scripts, asset cards, reference images) write directly through the store
 * without burning a conversation turn.
 */

export function ComicWorkbenchPanel({ folderId }: { folderId?: number } = {}) {
  const t = useTranslations("Comic")
  const { activeFolderId: currentFolderId } = useActiveFolder()
  const activeFolderId = folderId ?? currentFolderId
  const overview = useComicStore((s) =>
    activeFolderId != null ? s.overviews[activeFolderId] : undefined
  )
  const error = useComicStore((s) =>
    activeFolderId != null ? s.errors[activeFolderId] : undefined
  )
  const loading = useComicStore((s) =>
    activeFolderId != null ? s.loading[activeFolderId] : false
  )
  const load = useComicStore((s) => s.load)
  const wizardOpen = useComicStore((s) =>
    activeFolderId != null ? (s.wizardOpen[activeFolderId] ?? false) : false
  )
  const setWizardOpen = useComicStore((s) => s.setWizardOpen)

  // Load on folder change; refetch on `comic://changed` for this folder.
  useEffect(() => {
    if (activeFolderId == null) return
    void load(activeFolderId)
    return subscribeComicChanged((folderId) => {
      if (folderId === activeFolderId) void load(folderId)
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeFolderId])

  if (activeFolderId == null) {
    return (
      <ComicShell>
        <EmptyState icon={Clapperboard} text={t("noFolderActive")} />
      </ComicShell>
    )
  }
  return (
    <ComicShell>
      {overview ? (
        <ComicWorkbench
          key={activeFolderId}
          folderId={activeFolderId}
          overview={overview}
        />
      ) : loading ? (
        <div className="flex h-full items-center justify-center">
          <Loader2 className="size-5 animate-spin text-muted-foreground/60" />
        </div>
      ) : (
        <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
          <Clapperboard className="size-8 opacity-60" />
          <p className="max-w-sm text-center text-sm">
            {error ?? t("initProjectDesc")}
          </p>
          <Button
            variant="outline"
            onClick={() => setWizardOpen(activeFolderId, true)}
          >
            <Plus className="size-4" /> {t("initProject")}
          </Button>
        </div>
      )}
      {/* Keep the same wizard mounted when init emits the first project
          overview, so an in-flight source import cannot be submitted twice. */}
      <InitWizardDialog
        key={`setup-${activeFolderId}`}
        folderId={activeFolderId}
        open={wizardOpen}
        onOpenChange={(open) => setWizardOpen(activeFolderId, open)}
      />
    </ComicShell>
  )
}

function ComicShell({ children }: { children: React.ReactNode }) {
  return <div className="h-full min-h-0 overflow-hidden">{children}</div>
}

function EmptyState({
  icon: Icon,
  text,
}: {
  icon: React.ComponentType<{ className?: string }>
  text: string
}) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
      <Icon className="size-8 opacity-60" />
      <p className="max-w-sm text-center text-sm">{text}</p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// The workbench
// ---------------------------------------------------------------------------

function ComicWorkbench({
  folderId,
  overview,
}: {
  folderId: number
  overview: ComicOverviewPayload
}) {
  const t = useTranslations("Comic")
  const panel = useComicStore((s) => s.panel[folderId] ?? null)
  const openPanel = useComicStore((s) => s.openPanel)
  const [createEpisodeOpen, setCreateEpisodeOpen] = useState(false)
  const activePanel: ComicPanelKind =
    panel ??
    (overview.episodes.length > 0
      ? { kind: "storyboard", episodeId: overview.episodes[0].id }
      : { kind: "source" })
  const value =
    activePanel.kind === "storyboard"
      ? `episode:${activePanel.episodeId}`
      : activePanel.kind === "asset"
        ? `${activePanel.assetKind}:${activePanel.slug}`
        : activePanel.kind

  const selectResource = (value: string) => {
    if (value === "source" || value === "providers") {
      openPanel(folderId, { kind: value })
      return
    }
    const [kind, id] = value.split(":")
    if (kind === "episode") {
      openPanel(folderId, { kind: "storyboard", episodeId: id })
    } else if (kind === "character" || kind === "scene" || kind === "prop") {
      openPanel(folderId, { kind: "asset", assetKind: kind, slug: id })
    }
  }

  return (
    <section
      className="flex h-full min-h-0 min-w-0 flex-col"
      aria-label={t("title")}
    >
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <h3 className="min-w-0 flex-1 truncate text-sm font-semibold">
          {overview.series?.title ?? t("untitledProject")}
        </h3>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          aria-label={t("refresh")}
          onClick={() => void useComicStore.getState().refresh(folderId)}
        >
          <RefreshCw className="size-3.5" />
        </Button>
      </div>
      <ComicEpisodeManager
        key={folderId}
        folderId={folderId}
        overview={overview}
        selectedId={
          activePanel.kind === "storyboard" ? activePanel.episodeId : undefined
        }
        createOpen={createEpisodeOpen}
        onCreateOpenChange={setCreateEpisodeOpen}
      />
      <div className="shrink-0 space-y-2 border-b p-3">
        <label
          className="block text-xs text-muted-foreground"
          htmlFor={`comic-resource-${folderId}`}
        >
          {t("resources")}
        </label>
        <select
          id={`comic-resource-${folderId}`}
          className="h-9 w-full min-w-0 rounded-md border bg-background px-2 text-sm"
          value={value}
          onChange={(event) => selectResource(event.target.value)}
        >
          <option value="source">
            {t("source")}
            {overview.has_source ? "" : ` · ${t("sourceMissing")}`}
          </option>
          <optgroup label={t("episodes")}>
            {overview.episodes.map((episode) => (
              <option key={episode.id} value={`episode:${episode.id}`}>
                {episode.title || episode.id} · {episode.shots_done}/
                {episode.shots_total}
              </option>
            ))}
          </optgroup>
          <optgroup label={t("characters")}>
            {overview.characters.map((card) => (
              <option key={card.slug} value={`character:${card.slug}`}>
                {card.name}
              </option>
            ))}
          </optgroup>
          <optgroup label={t("scenes")}>
            {overview.scenes.map((card) => (
              <option key={card.slug} value={`scene:${card.slug}`}>
                {card.name}
              </option>
            ))}
          </optgroup>
          <optgroup label={t("props")}>
            {overview.props.map((card) => (
              <option key={card.slug} value={`prop:${card.slug}`}>
                {card.name}
              </option>
            ))}
          </optgroup>
          <option value="providers">{t("providers")}</option>
        </select>
        <ComicQuickActions
          folderId={folderId}
          overview={overview}
          episodeId={
            activePanel.kind === "storyboard"
              ? activePanel.episodeId
              : undefined
          }
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {activePanel.kind === "source" && (
          <SourcePanel
            key={folderId}
            folderId={folderId}
            overview={overview}
            onCreateEpisode={() => setCreateEpisodeOpen(true)}
          />
        )}
        {activePanel.kind === "storyboard" && (
          <StoryboardPanel
            key={`${folderId}:${activePanel.episodeId}`}
            folderId={folderId}
            episodeId={activePanel.episodeId}
          />
        )}
        {activePanel.kind === "asset" && (
          <AssetPanel
            key={`${folderId}:${activePanel.assetKind}:${activePanel.slug}`}
            folderId={folderId}
            assetKind={activePanel.assetKind}
            slug={activePanel.slug}
          />
        )}
        {activePanel.kind === "providers" && (
          <ProvidersPanel folderId={folderId} overview={overview} />
        )}
        <div className="p-3">
          <NextActionCard overview={overview} />
        </div>
      </div>
    </section>
  )
}

/** Exact shot-target parse — `episodes/<episode>/shots/<shot>` only. A plain
 * suffix split would misread targets like `episodes/ep001/export`. */
function parseShotTarget(
  targetId: string | null | undefined
): string | undefined {
  const match = /^episodes\/[a-z0-9_-]+\/shots\/([a-z0-9_-]+)$/.exec(
    targetId ?? ""
  )
  return match?.[1]
}

/** The compact task list keeps its recent-six cap, except that a succeeded
 * video always stays visible: it is the thing waiting for review. */
function shownTasks(tasks: GenerationTaskView[]): GenerationTaskView[] {
  const capped = tasks.slice(0, 6)
  const pendingReview = tasks.filter(
    (task) =>
      task.kind === "video" &&
      task.status === "succeeded" &&
      !capped.includes(task)
  )
  return [...pendingReview, ...capped]
}

/** Read the target on every click. A project switch racing the render must
 * never submit this project's action to a different conversation. */
function useComicTaskTrigger(folderId: number) {
  const t = useTranslations("Comic")
  const current = useTabStore(
    (state) => state.tabs.find((tab) => tab.id === state.activeTabId) ?? null
  )
  const manualOnly = comicEntryIsManualOnly(current?.agentType ?? null)
  const enabled =
    current?.kind === "conversation" &&
    current.folderId === folderId &&
    !current.isChat &&
    !manualOnly
  const send = useCallback(
    (text: string) => {
      const state = useTabStore.getState()
      const tab = state.tabs.find((tab) => tab.id === state.activeTabId) ?? null
      if (comicEntryIsManualOnly(tab?.agentType ?? null)) {
        toast.error(t("comicManualOnlyHint"))
        return false
      }
      const queued = requestComicConversationTask(tab, folderId, text)
      if (queued) toast.success(t("instructionQueued"))
      else toast.error(t("noCurrentConversation"))
      return queued
    },
    [folderId, t]
  )
  return { enabled, send, manualOnly }
}

function ComicQuickActions({
  folderId,
  overview,
  episodeId,
}: {
  folderId: number
  overview: ComicOverviewPayload
  episodeId?: string
}) {
  const t = useTranslations("Comic")
  const { enabled, send, manualOnly } = useComicTaskTrigger(folderId)
  const episode = episodeId
  const scriptDraft = useComicStore((state) =>
    episode ? state.drafts[folderId]?.[`episodes/${episode}/script`] : undefined
  )
  const scriptDirty = scriptDraft?.kind === "script"
  const hasScriptDrafts = useComicStore((state) =>
    Object.values(state.drafts[folderId] ?? {}).some(
      (draft) => draft.kind === "script"
    )
  )
  const original = overview.source?.mode === "original"
  const scriptActionDisabled =
    !enabled || (original && !!episode && scriptDirty)
  return (
    <div className="space-y-2">
      <p className="text-xs leading-5 text-muted-foreground">
        {t(
          manualOnly
            ? "comicManualOnlyHint"
            : enabled
              ? "currentConversationHint"
              : "noCurrentConversation"
        )}
      </p>
      {hasScriptDrafts && (
        <p className="text-xs text-muted-foreground">
          {t("scriptSaveBeforeAI")}
        </p>
      )}
      <div className="flex flex-wrap gap-1.5">
        <Button
          variant="outline"
          size="sm"
          disabled={scriptActionDisabled}
          onClick={() =>
            !scriptActionDisabled &&
            send(
              original
                ? episode
                  ? t("qaOriginalEpisodePrompt", { episode })
                  : t("qaOriginalPrompt")
                : t("qaAdaptPrompt")
            )
          }
        >
          <Wand2 className="size-3.5" />
          {t(original ? "qaOriginal" : "qaAdapt")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!enabled || hasScriptDrafts}
          onClick={() => {
            const pendingDrafts =
              useComicStore.getState().drafts[folderId] ?? {}
            if (
              Object.values(pendingDrafts).some(
                (draft) => draft.kind === "script"
              )
            ) {
              toast.error(t("scriptSaveBeforeAI"))
              return
            }
            send(t("qaSaveConversationPrompt"))
          }}
        >
          <Save className="size-3.5" />
          {t("qaSaveConversation")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!enabled || !episode}
          onClick={() =>
            episode &&
            send(
              t("qaImagesPrompt", {
                episode,
                count: overview.series?.default_candidate_count ?? 4,
              })
            )
          }
        >
          {t("qaImages")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!enabled || !episodeId}
          onClick={() =>
            episodeId && send(t("qaVideosPrompt", { episode: episodeId }))
          }
        >
          <Film className="size-3.5" />
          {t("qaVideos")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!enabled || !episode}
          onClick={() => episode && send(t("qaSpeechPrompt", { episode }))}
        >
          {t("qaSpeech")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!enabled || !episode}
          onClick={() => episode && send(t("qaComposePrompt", { episode }))}
        >
          {t("qaCompose")}
        </Button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Source panel — read the novel, chapter by chapter
// ---------------------------------------------------------------------------

function SourcePanel({
  folderId,
  overview,
  onCreateEpisode,
}: {
  folderId: number
  overview: ComicOverviewPayload
  onCreateEpisode: () => void
}) {
  const t = useTranslations("Comic")
  const [chapters, setChapters] = useState<Chapter[] | null>(null)
  const [selected, setSelected] = useState<number>(1)
  const [offset, setOffset] = useState(0)
  const [read, setRead] = useState<WindowRead | null>(null)
  const [readFor, setReadFor] = useState<number | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const original = overview.source?.mode === "original"
  const readableSource =
    overview.has_source &&
    (overview.source?.mode === "upload" ||
      overview.source?.mode === "external_path") &&
    !!overview.source.novel_file

  useEffect(() => {
    if (!readableSource) return
    let cancelled = false
    void comicApi
      .listChapters(folderId, 500)
      .then((result) => {
        if (!cancelled) setChapters(result)
      })
      .catch((err) => {
        if (!cancelled) toast.error(toErrorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [folderId, readableSource])

  useEffect(() => {
    if (!readableSource) return
    let cancelled = false
    void comicApi
      .readSource(folderId, {
        fromChapter: selected,
        offsetChars: offset,
        maxChars: 8000,
      })
      .then((result) => {
        if (cancelled) return
        setRead(result)
        setReadFor(selected)
      })
      .catch((err) => {
        if (!cancelled) toast.error(toErrorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [folderId, selected, offset, readableSource])

  const busy = readFor !== selected

  const ingestFile = (file: File) => {
    void readComicUpload(file)
      .then((base64) => {
        return comicApi
          .ingestSource(folderId, {
            uploadBase64: base64,
            title: file.name.replace(/\.[^.]+$/, ""),
          })
          .then((report: SourceReport) => {
            toast.success(
              t("ingested", {
                chapters: report.chapter_count,
                chars: report.total_chars,
              })
            )
            void useComicStore.getState().refresh(folderId)
          })
      })
      .catch((err) => toast.error(toErrorMessage(err)))
  }

  if (original) {
    return (
      <div className="space-y-4 p-4">
        <p className="text-sm text-muted-foreground">
          {t("originalSourceHint")}
        </p>
        <Button onClick={onCreateEpisode}>
          <Plus className="size-4" />
          {t(overview.episodes.length ? "episodeNew" : "episodeFirst")}
        </Button>
      </div>
    )
  }

  if (!readableSource) {
    return (
      <div className="p-4">
        <EmptyState icon={BookOpen} text={t("sourceUnavailable")} />
        <div className="mt-4 flex flex-wrap justify-center gap-2">
          <Button variant="outline" onClick={onCreateEpisode}>
            <Plus className="size-4" />
            {t(overview.episodes.length ? "episodeNew" : "episodeFirst")}
          </Button>
          <Button onClick={() => fileRef.current?.click()}>
            <BookOpen className="size-4" /> {t("uploadNovel")}
          </Button>
          <input
            ref={fileRef}
            type="file"
            accept=".txt,.md,text/plain"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0]
              if (file) ingestFile(file)
            }}
          />
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Select
          value={String(selected)}
          onValueChange={(v) => {
            setSelected(Number(v))
            setOffset(0)
          }}
        >
          <SelectTrigger className="w-72">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(chapters ?? []).map((c) => (
              <SelectItem key={c.no} value={String(c.no)}>
                {c.no}. {c.title} ({c.chars})
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="text-xs text-muted-foreground">
          {t("sourcePageHint")}
        </span>
        {busy && (
          <Loader2 className="size-4 animate-spin text-muted-foreground" />
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto whitespace-pre-wrap p-4 font-serif text-sm leading-7">
        {read?.text ?? ""}
        {read?.has_more && (
          <div className="py-3 text-center">
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                if (read.next_offset != null) setOffset(read.next_offset)
              }}
            >
              {t("nextWindow")}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Storyboard panel — the shot table + script editor
// ---------------------------------------------------------------------------

/** The video clip under explicit review. `expectedCurrentTaskId` is the
 * binding observed when the dialog opened; the backend compares against it
 * so a stale confirmation can never overwrite a newer binding. */
type VideoReview = {
  task: GenerationTaskView
  shotId: string
  expectedCurrentTaskId: number | null
  preview: ComicVideoPreview
}

function StoryboardPanel({
  folderId,
  episodeId,
}: {
  folderId: number
  episodeId: string
}) {
  const t = useTranslations("Comic")
  const { enabled, send } = useComicTaskTrigger(folderId)
  const [shots, setShots] = useState<StoryShot[]>([])
  const [script, setScript] = useState<string | null>(null)
  const draftKey = `episodes/${episodeId}/script`
  const draft = useComicStore((state) => state.drafts[folderId]?.[draftKey])
  const scriptDraft = draft?.kind === "script" ? draft.text : (script ?? "")
  const scriptDirty = draft?.kind === "script"
  const [showScript, setShowScript] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const [confirmRegenerate, setConfirmRegenerate] = useState(false)
  const [loading, setLoading] = useState(true)
  const shotDraftKey = `episodes/${episodeId}/shot-editor`
  const shotDraft = useComicStore(
    (state) => state.drafts[folderId]?.[shotDraftKey]
  )
  const editing = shotDraft?.kind === "shot" ? shotDraft.shot : null
  const setEditing = (shot: StoryShot | "new" | null) => {
    const state = useComicStore.getState()
    if (shot)
      state.setResourceDraft(folderId, shotDraftKey, makeShotDraft(shot))
    else {
      const current = state.drafts[folderId]?.[shotDraftKey]
      if (current) state.clearResourceDraft(folderId, shotDraftKey, current)
    }
  }
  const [tasks, setTasks] = useState<GenerationTaskView[]>([])
  const [videoReview, setVideoReview] = useState<VideoReview | null>(null)

  const openVideoReview = async (task: GenerationTaskView, shotId: string) => {
    const expected =
      shots.find((shot) => shot.id === shotId)?.video?.task_id ?? null
    try {
      const preview = await resolveComicVideoPreview(folderId, task.id)
      setVideoReview({
        task,
        shotId,
        expectedCurrentTaskId: expected,
        preview,
      })
    } catch (error) {
      toast.error(toErrorMessage(error))
    }
  }

  const reload = useMemo(
    () => async () => {
      setLoading(true)
      try {
        const doc = await comicApi.getAsset(folderId, "episode", episodeId)
        const board = doc.storyboard as { shots?: StoryShot[] } | undefined
        setShots(board?.shots ?? [])
        const scriptText = (doc.script as string | undefined) ?? null
        setScript(scriptText)
      } catch (err) {
        toast.error(toErrorMessage(err))
      } finally {
        setLoading(false)
      }
    },
    [folderId, episodeId]
  )

  useEffect(() => {
    void reload()
  }, [reload])

  // Recent generation tasks for this episode — succeeded ones expose their
  // candidates for one-click binding (the session-side card is the other path).
  // The episode filter runs server-side before the limit, so newer tasks in
  // other episodes cannot push this episode's rows out of the window.
  const reloadTasks = useMemo(
    () => async () => {
      try {
        const scoped = await comicApi.listTasks(folderId, 50, episodeId)
        setTasks(
          scoped.filter(
            (task) =>
              task.targetId === `episodes/${episodeId}` ||
              task.targetId?.startsWith(`episodes/${episodeId}/`)
          )
        )
      } catch {
        setTasks([])
      }
    },
    [folderId, episodeId]
  )

  useEffect(() => {
    void reloadTasks()
    return subscribeComicChanged((changedFolder) => {
      if (changedFolder === folderId) {
        void reload()
        void reloadTasks()
      }
    })
  }, [folderId, reload, reloadTasks])

  const bind = async (shotId: string, candidate: string) => {
    try {
      await comicApi.bindCandidate(folderId, episodeId, shotId, candidate)
      toast.success(t("imageBound"))
      await reload()
      await reloadTasks()
      void useComicStore.getState().refresh(folderId)
    } catch (err) {
      toast.error(toErrorMessage(err))
    }
  }

  const move = async (index: number, delta: -1 | 1) => {
    const target = index + delta
    if (target < 0 || target >= shots.length) return
    const ids = shots.map((s) => s.id)
    const [moved] = ids.splice(index, 1)
    ids.splice(target, 0, moved)
    try {
      const board = await comicApi.reorderShots(folderId, episodeId, ids)
      setShots(board.shots)
      void useComicStore.getState().refresh(folderId)
    } catch (err) {
      toast.error(toErrorMessage(err))
    }
  }

  const remove = async (shotId: string) => {
    if (!window.confirm(t("deleteShotConfirm", { shot: shotId }))) return
    try {
      await comicApi.deleteShot(folderId, episodeId, shotId)
      await reload()
      void useComicStore.getState().refresh(folderId)
    } catch (err) {
      toast.error(toErrorMessage(err))
    }
  }

  const saveScript = async () => {
    try {
      await comicApi.writeScript(folderId, episodeId, scriptDraft)
      setScript(scriptDraft)
      if (draft) {
        useComicStore.getState().clearResourceDraft(folderId, draftKey, draft)
      }
      toast.success(t("scriptSaved"))
      void useComicStore.getState().refresh(folderId)
    } catch (err) {
      toast.error(toErrorMessage(err))
    }
  }

  const canGenerate = enabled && !loading && !scriptDirty && !!script?.trim()
  const generateStoryboard = (confirmed = false) => {
    if (!canGenerate) return
    if (
      useComicStore.getState().drafts[folderId]?.[draftKey]?.kind === "script"
    ) {
      toast.error(t("scriptSaveBeforeAI"))
      return
    }
    if (shots.length > 0 && !confirmed) {
      setConfirmRegenerate(true)
      return
    }
    send(t("qaStoryboardPrompt", { episode: episodeId }))
  }

  return (
    <div className="p-4">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-semibold">{episodeId}</h3>
        <Button
          size="sm"
          disabled={!canGenerate}
          onClick={() => generateStoryboard()}
        >
          <Wand2 className="size-3.5" /> {t("storyboardGenerate")}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setShowScript((v) => !v)}
        >
          <FileText className="size-3.5" /> {t("script")}
        </Button>
        <Button variant="outline" size="sm" onClick={() => setEditing("new")}>
          <Plus className="size-3.5" /> {t("addShot")}
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm">
              <MoreHorizontal className="size-3.5" />
              {t("storyboardMore")}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => setImportOpen(true)}>
              {t("storyboardImport")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <p className="mb-4 text-xs text-muted-foreground">
        {t(
          !loading && !script?.trim()
            ? "storyboardNeedsScript"
            : "storyboardWorkflowHint"
        )}
      </p>
      <AlertDialog open={confirmRegenerate} onOpenChange={setConfirmRegenerate}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("storyboardGenerate")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("storyboardRegenerateConfirm", {
                episode: episodeId,
                count: shots.length,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={!canGenerate}
              onClick={() => generateStoryboard(true)}
            >
              {t("storyboardGenerate")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <ComicStoryboardImportDialog
        folderId={folderId}
        episodeId={episodeId}
        open={importOpen}
        onOpenChange={setImportOpen}
        onImported={reload}
      />

      {(showScript || scriptDirty || (!loading && shots.length === 0)) && (
        <div className="mb-4">
          <Textarea
            aria-label={t("script")}
            value={scriptDraft}
            onChange={(e) => {
              useComicStore.getState().setResourceDraft(folderId, draftKey, {
                kind: "script",
                text: e.target.value,
              })
            }}
            className="min-h-72 font-mono text-xs leading-6"
            placeholder={script || t("scriptPlaceholder")}
          />
          <div className="mt-2 flex justify-end">
            <Button
              size="sm"
              disabled={!scriptDirty}
              onClick={() => void saveScript()}
            >
              <Save className="size-3.5" /> {t("save")}
            </Button>
          </div>
        </div>
      )}

      {/* Recent generation tasks — candidates bindable in one click */}
      {tasks.length > 0 && (
        <div className="mb-4 space-y-1.5 rounded-md border bg-muted/20 p-2.5">
          <p className="text-xs font-medium text-muted-foreground">
            {t("recentTasks")}
          </p>
          {shownTasks(tasks).map((task) => {
            const shotId = parseShotTarget(task.targetId)
            return (
              <div
                key={task.id}
                className="flex flex-wrap items-center gap-1.5 text-xs"
              >
                <span className="font-mono text-muted-foreground">
                  #{task.id}
                </span>
                <Badge
                  variant={
                    task.status === "succeeded"
                      ? "default"
                      : task.status === "failed"
                        ? "destructive"
                        : "outline"
                  }
                >
                  {task.status}
                </Badge>
                <span className="text-muted-foreground">{shotId}</span>
                {task.errorMessage && (
                  <span className="truncate text-destructive">
                    {task.errorKind}: {task.errorMessage}
                  </span>
                )}
                {task.kind === "tts" &&
                  task.status === "succeeded" &&
                  shotId &&
                  task.artifactPath && (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={shots.some(
                        (shot) =>
                          shot.id === shotId && shot.audio?.task_id === task.id
                      )}
                      onClick={() =>
                        void comicApi
                          .bindSpeech(folderId, episodeId, shotId, task.id)
                          .then(async () => {
                            await reload()
                            await reloadTasks()
                          })
                          .catch((error) => toast.error(toErrorMessage(error)))
                      }
                    >
                      {t("useSpeech")}
                    </Button>
                  )}
                {task.kind === "video" &&
                  task.status === "succeeded" &&
                  task.artifactPath &&
                  shotId && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => void openVideoReview(task, shotId)}
                    >
                      <Film className="size-3.5" />
                      {t("videoReview")}
                    </Button>
                  )}
                {task.kind === "compose" &&
                  task.status === "succeeded" &&
                  task.artifactPath && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        void openComicExport(
                          folderId,
                          task.artifactPath!
                        ).catch((error) => toast.error(toErrorMessage(error)))
                      }
                    >
                      <Film className="size-3.5" />
                      {t("openExport")}
                    </Button>
                  )}
                {task.status === "succeeded" &&
                  task.candidates.map((rel: string) => {
                    const fileName = rel.split("/").pop() ?? rel
                    const bound = shots.find(
                      (s) => s.id === shotId && s.selected === rel
                    )
                    return (
                      <button
                        key={rel}
                        type="button"
                        disabled={bound != null}
                        onClick={() => shotId && void bind(shotId, rel)}
                        className={cn(
                          "rounded border px-1.5 py-0.5 transition-colors",
                          bound
                            ? "border-primary/40 bg-primary/10 text-primary"
                            : "hover:bg-accent"
                        )}
                      >
                        <ComicImage
                          folderId={folderId}
                          path={rel}
                          alt={fileName}
                          className="mb-1 h-32 w-28 rounded object-contain"
                        />
                        {fileName}
                        {bound ? " ✓" : ""}
                      </button>
                    )
                  })}
              </div>
            )
          })}
        </div>
      )}

      {loading ? (
        <Loader2 className="size-4 animate-spin text-muted-foreground" />
      ) : shots.length === 0 ? (
        <EmptyState icon={Film} text={t("noShotsYet")} />
      ) : (
        <div className="space-y-2">
          {shots.map((shot, index) => (
            <div
              key={shot.id}
              className="group flex items-start gap-2 rounded-md border p-2.5 text-sm"
            >
              <div className="flex flex-col gap-0.5 pt-0.5">
                <button
                  type="button"
                  aria-label={t("moveUp")}
                  className="rounded p-0.5 text-muted-foreground hover:bg-accent disabled:opacity-30"
                  disabled={index === 0}
                  onClick={() => void move(index, -1)}
                >
                  <ArrowUp className="size-3.5" />
                </button>
                <button
                  type="button"
                  aria-label={t("moveDown")}
                  className="rounded p-0.5 text-muted-foreground hover:bg-accent disabled:opacity-30"
                  disabled={index === shots.length - 1}
                  onClick={() => void move(index, 1)}
                >
                  <ArrowDown className="size-3.5" />
                </button>
              </div>
              <span className="w-16 shrink-0 pt-1 font-mono text-xs text-muted-foreground">
                {shot.id}
              </span>
              <div className="min-w-0 flex-1">
                <p className="line-clamp-2">{shot.description}</p>
                {shot.dialogue.length > 0 && (
                  <p className="mt-1 truncate text-xs text-muted-foreground">
                    {shot.dialogue
                      .map(
                        (d) =>
                          `${d.character ? `${d.character}: ` : ""}“${d.text}”`
                      )
                      .join(" / ")}
                  </p>
                )}
              </div>
              {shot.selected && (
                <Badge variant="outline" className="shrink-0">
                  {t("imageBound")}
                </Badge>
              )}
              <Badge variant="outline" className="shrink-0">
                {shot.state}
              </Badge>
              <div className="flex shrink-0 gap-1 opacity-0 transition-opacity group-hover:opacity-100">
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  onClick={() => setEditing(shot)}
                >
                  <Pencil className="size-3.5" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7 text-destructive"
                  onClick={() => void remove(shot.id)}
                >
                  <Trash2 className="size-3.5" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      <ShotEditDialog
        folderId={folderId}
        episodeId={episodeId}
        shots={shots}
        editing={editing}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        onSaved={async () => {
          setEditing(null)
          await reload()
          void useComicStore.getState().refresh(folderId)
        }}
      />
      <VideoReviewDialog
        folderId={folderId}
        episodeId={episodeId}
        review={videoReview}
        onClose={() => setVideoReview(null)}
        onChanged={async () => {
          setVideoReview(null)
          await reload()
          await reloadTasks()
          void useComicStore.getState().refresh(folderId)
        }}
      />
    </div>
  )
}

/** One preview at a time: this dialog is the only mounted player, and
 * closing it — or opening another shot — tears the current one down. The
 * confirm button carries the binding observed when the dialog opened; a
 * `conflict` refusal closes the review and refreshes the observed state
 * instead of reporting success. */
function VideoReviewDialog({
  folderId,
  episodeId,
  review,
  onClose,
  onChanged,
}: {
  folderId: number
  episodeId: string
  review: VideoReview | null
  onClose: () => void
  onChanged: () => Promise<void> | void
}) {
  const t = useTranslations("Comic")
  const [busy, setBusy] = useState(false)
  if (!review) return null
  const confirm = async () => {
    setBusy(true)
    try {
      await comicApi.bindVideo(
        folderId,
        episodeId,
        review.shotId,
        review.task.id,
        review.expectedCurrentTaskId
      )
      toast.success(t("videoBound"))
      await onChanged()
    } catch (error) {
      if ((error as { code?: string })?.code === "conflict") {
        toast.error(t("videoConflict"))
        await onChanged()
      } else {
        toast.error(toErrorMessage(error))
      }
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {review.expectedCurrentTaskId == null
              ? t("videoBindTitle", { shot: review.shotId })
              : t("videoReplaceTitle", { shot: review.shotId })}
          </DialogTitle>
          <DialogDescription>
            {review.expectedCurrentTaskId == null
              ? t("videoBindHint")
              : t("videoReplaceHint", {
                  current: review.expectedCurrentTaskId,
                })}
          </DialogDescription>
        </DialogHeader>
        {review.preview.kind === "inline" ? (
          <video
            key={review.preview.url}
            controls
            preload="metadata"
            src={review.preview.url}
            className="max-h-[50vh] w-full rounded-md bg-black"
          />
        ) : (
          <Button
            variant="outline"
            onClick={() =>
              void openComicShotClip(folderId, review.task.artifactPath!).catch(
                (error) => toast.error(toErrorMessage(error))
              )
            }
          >
            <Film className="size-3.5" /> {t("videoOpenExternal")}
          </Button>
        )}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={busy} onClick={() => void confirm()}>
            {review.expectedCurrentTaskId == null
              ? t("videoConfirm")
              : t("videoReplaceConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// Shot editor
// ---------------------------------------------------------------------------

function makeShotDraft(shot: StoryShot | "new" | null): ComicShotDraft {
  const current = shot === "new" ? null : shot
  return {
    kind: "shot",
    shot: shot ?? "new",
    description: current?.description ?? "",
    dialogue:
      current?.dialogue.map((line) => ({
        character: line.character ?? "",
        text: line.text,
      })) ?? [],
    duration: String(current?.duration_secs || 3),
    camera: current?.camera ?? "",
    shotSize: current?.shot_size ?? "",
    transition: current?.transition ?? "",
    charRefs:
      current?.refs.characters.map((ref) => ({
        slug: ref.slug,
        outfit: ref.outfit ?? "",
      })) ?? [],
    sceneRef: current?.refs.scene ?? "",
    propRefs: [...(current?.refs.props ?? [])],
  }
}

function ShotEditDialog({
  folderId,
  episodeId,
  shots,
  editing,
  onOpenChange,
  onSaved,
}: {
  folderId: number
  episodeId: string
  shots: StoryShot[]
  editing: StoryShot | "new" | null
  onOpenChange: (open: boolean) => void
  onSaved: () => Promise<void> | void
}) {
  const t = useTranslations("Comic")
  const overview = useComicStore((state) => state.overviews[folderId])
  const draftKey = `episodes/${episodeId}/shot-editor`
  const savedDraft = useComicStore(
    (state) => state.drafts[folderId]?.[draftKey]
  )
  const draft =
    savedDraft?.kind === "shot" ? savedDraft : makeShotDraft(editing)
  // An unbind is its own confirmed CAS action: ordinary edits carry the
  // current clip through untouched, so this dialog is the only place a bound
  // clip can be cleared.
  const boundVideo =
    editing !== "new" && editing ? (editing.video ?? null) : null
  const [confirmUnbind, setConfirmUnbind] = useState(false)
  const {
    description,
    dialogue,
    duration,
    camera,
    shotSize,
    transition,
    charRefs,
    sceneRef,
    propRefs,
  } = draft
  const updateDraft = <K extends keyof ComicShotDraft>(
    key: K,
    value: ComicShotDraft[K]
  ) => {
    useComicStore.getState().setResourceDraft(folderId, draftKey, {
      ...draft,
      [key]: value,
    })
  }
  const setDescription = (value: string) => updateDraft("description", value)
  const setDialogue = (value: ComicShotDraft["dialogue"]) =>
    updateDraft("dialogue", value)
  const setDuration = (value: string) => updateDraft("duration", value)
  const setCamera = (value: string) => updateDraft("camera", value)
  const setShotSize = (value: string) => updateDraft("shotSize", value)
  const setTransition = (value: string) => updateDraft("transition", value)
  const setCharRefs = (value: ComicShotDraft["charRefs"]) =>
    updateDraft("charRefs", value)
  const setSceneRef = (value: string) => updateDraft("sceneRef", value)
  const setPropRefs = (value: string[]) => updateDraft("propRefs", value)
  const [busy, setBusy] = useState(false)

  const isNew = editing === "new"
  const open = editing != null

  const nextShotId = () => {
    const nums = shots
      .map((s) => Number(/(\d+)$/.exec(s.id)?.[1] ?? 0))
      .filter((n) => Number.isFinite(n))
    const next = (nums.length > 0 ? Math.max(...nums) : 0) + 1
    return `shot-${String(next).padStart(4, "0")}`
  }

  const submit = async () => {
    if (!description.trim()) return
    setBusy(true)
    try {
      await comicApi.upsertShot(folderId, episodeId, {
        id: isNew ? nextShotId() : (editing as StoryShot).id,
        description: description.trim(),
        dialogue: dialogue
          .filter((d) => d.text.trim())
          .map((d) => ({ character: d.character || null, text: d.text })),
        duration_secs: Number(duration) || 3,
        camera: camera || null,
        shot_size: shotSize || null,
        transition: transition || null,
        refs: {
          characters: charRefs.filter((c) => c.slug),
          scene: sceneRef || null,
          props: propRefs.filter(Boolean),
        },
        state: isNew ? "draft" : (editing as StoryShot).state,
        selected: isNew ? null : (editing as StoryShot).selected,
        audio: isNew ? null : (editing as StoryShot).audio,
        video: isNew ? null : (editing as StoryShot).video,
      })
      await onSaved()
    } catch (err) {
      toast.error(toErrorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const unbind = async () => {
    if (!boundVideo || editing === "new" || !editing) return
    setBusy(true)
    try {
      await comicApi.clearShotVideo(
        folderId,
        episodeId,
        editing.id,
        boundVideo.task_id
      )
      toast.success(t("videoUnbound"))
      setConfirmUnbind(false)
      await onSaved()
    } catch (err) {
      toast.error(toErrorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const characters = overview?.characters ?? []
  const scenes = overview?.scenes ?? []
  const props = overview?.props ?? []

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {isNew ? t("addShot") : (editing as StoryShot)?.id}
          </DialogTitle>
          <DialogDescription>{t("shotEditDesc")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label>{t("shotDescription")}</Label>
            <Textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              className="min-h-20 text-sm"
            />
          </div>
          <div className="space-y-1.5">
            <Label>{t("shotDialogue")}</Label>
            {dialogue.map((line, i) => (
              <div key={i} className="flex gap-1.5">
                <Input
                  value={line.character}
                  onChange={(e) => {
                    const next = [...dialogue]
                    next[i] = { ...line, character: e.target.value }
                    setDialogue(next)
                  }}
                  placeholder={t("characterName")}
                  className="w-28 shrink-0 text-sm"
                />
                <Input
                  value={line.text}
                  onChange={(e) => {
                    const next = [...dialogue]
                    next[i] = { ...line, text: e.target.value }
                    setDialogue(next)
                  }}
                  className="flex-1 text-sm"
                />
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8 shrink-0"
                  onClick={() =>
                    setDialogue(dialogue.filter((_, j) => j !== i))
                  }
                >
                  <X className="size-3.5" />
                </Button>
              </div>
            ))}
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                setDialogue([...dialogue, { character: "", text: "" }])
              }
            >
              <Plus className="size-3" /> {t("addLine")}
            </Button>
          </div>
          <div className="grid grid-cols-4 gap-2">
            <div className="space-y-1.5">
              <Label className="text-xs">{t("durationSecs")}</Label>
              <Input
                value={duration}
                onChange={(e) => setDuration(e.target.value)}
                type="number"
                min="0.5"
                step="0.5"
                className="text-sm"
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">{t("camera")}</Label>
              <Input
                value={camera}
                onChange={(e) => setCamera(e.target.value)}
                className="text-sm"
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">{t("shotSize")}</Label>
              <Input
                value={shotSize}
                onChange={(e) => setShotSize(e.target.value)}
                className="text-sm"
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">{t("transition")}</Label>
              <Input
                value={transition}
                onChange={(e) => setTransition(e.target.value)}
                className="text-sm"
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>{t("shotRefs")}</Label>
            <div className="flex flex-wrap items-center gap-1.5">
              {charRefs.map((c, i) => (
                <Badge key={i} variant="secondary" className="gap-1">
                  {c.slug}
                  {c.outfit ? `@${c.outfit}` : ""}
                  <button
                    type="button"
                    onClick={() =>
                      setCharRefs(charRefs.filter((_, j) => j !== i))
                    }
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              ))}
              {sceneRef && (
                <Badge variant="secondary" className="gap-1">
                  {t("scene")}: {sceneRef}
                  <button type="button" onClick={() => setSceneRef("")}>
                    <X className="size-3" />
                  </button>
                </Badge>
              )}
              {propRefs.map((p) => (
                <Badge key={p} variant="secondary" className="gap-1">
                  {t("prop")}: {p}
                  <button
                    type="button"
                    onClick={() => setPropRefs(propRefs.filter((x) => x !== p))}
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              ))}
            </div>
            <div className="flex flex-wrap gap-1.5">
              <Select
                value=""
                onValueChange={(slug) =>
                  setCharRefs([...charRefs, { slug, outfit: "" }])
                }
              >
                <SelectTrigger
                  className="h-8 w-32 text-xs"
                  aria-label={t("addCharacter")}
                >
                  <SelectValue placeholder={t("addCharacter")} />
                </SelectTrigger>
                <SelectContent>
                  {characters.map((c) => (
                    <SelectItem key={c.slug} value={c.slug}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Select value="" onValueChange={setSceneRef}>
                <SelectTrigger
                  className="h-8 w-32 text-xs"
                  aria-label={t("addScene")}
                >
                  <SelectValue placeholder={t("addScene")} />
                </SelectTrigger>
                <SelectContent>
                  {scenes.map((sc) => (
                    <SelectItem key={sc.slug} value={sc.slug}>
                      {sc.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Select
                value=""
                onValueChange={(slug) => setPropRefs([...propRefs, slug])}
              >
                <SelectTrigger
                  className="h-8 w-32 text-xs"
                  aria-label={t("addProp")}
                >
                  <SelectValue placeholder={t("addProp")} />
                </SelectTrigger>
                <SelectContent>
                  {props.map((p) => (
                    <SelectItem key={p.slug} value={p.slug}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
        </div>
        {boundVideo && (
          <div className="flex items-center justify-between rounded-md border p-3 text-sm">
            <span className="text-muted-foreground">
              {t("videoBoundShot", { task: boundVideo.task_id })}
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => setConfirmUnbind(true)}
            >
              {t("videoUnbind")}
            </Button>
          </div>
        )}
        <DialogFooter>
          <Button
            disabled={!description.trim() || busy}
            onClick={() => void submit()}
          >
            {busy && <Loader2 className="size-4 animate-spin" />}
            <Save className="size-4" /> {t("save")}
          </Button>
        </DialogFooter>
        <AlertDialog open={confirmUnbind} onOpenChange={setConfirmUnbind}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("videoUnbindTitle")}</AlertDialogTitle>
              <AlertDialogDescription>
                {t("videoUnbindHint")}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
              <AlertDialogAction onClick={() => void unbind()}>
                {t("videoUnbind")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// Asset panel — character / scene / prop card editor
// ---------------------------------------------------------------------------

function AssetPanel({
  folderId,
  assetKind,
  slug,
}: {
  folderId: number
  assetKind: "character" | "scene" | "prop"
  slug: string
}) {
  const t = useTranslations("Comic")
  const [card, setCard] = useState<Record<string, unknown> | null>(null)
  const draftKey = `${assetKind}/${slug}`
  const draft = useComicStore((state) => state.drafts[folderId]?.[draftKey])
  const fields =
    draft?.kind === "asset"
      ? draft
      : {
          name: String(card?.name ?? ""),
          description: String(card?.description ?? ""),
          promptFragment: String(card?.prompt_fragment ?? ""),
        }
  const { name, description, promptFragment } = fields
  const updateFields = (patch: Partial<typeof fields>) => {
    useComicStore.getState().setResourceDraft(folderId, draftKey, {
      ...fields,
      ...patch,
      kind: "asset",
    })
  }
  const [busy, setBusy] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  const slotRef = useRef<string>("refs")

  useEffect(() => {
    void comicApi
      .getAsset(folderId, assetKind, slug)
      .then((doc) => {
        setCard(doc)
      })
      .catch((err) => toast.error(toErrorMessage(err)))
  }, [folderId, assetKind, slug])

  if (!card) {
    return (
      <div className="p-4">
        <Loader2 className="size-4 animate-spin text-muted-foreground" />
      </div>
    )
  }

  const turnaround = (card.turnaround ?? {}) as Record<string, string | null>
  const refs = (card.refs ?? []) as string[]
  const outfits = (card.outfits ?? []) as {
    slug: string
    name: string
    prompt_fragment: string
    front?: string | null
    side_45?: string | null
    side?: string | null
  }[]

  const save = async () => {
    setBusy(true)
    try {
      if (assetKind === "character") {
        await comicApi.upsertCharacter(folderId, {
          slug,
          name,
          description,
          prompt_fragment: promptFragment,
          personality: String(card.personality ?? ""),
          turnaround: turnaround as CharacterCard["turnaround"],
          outfits: outfits as CharacterCard["outfits"],
          expressions: (card.expressions ?? []) as string[],
          refs,
        })
      } else if (assetKind === "scene") {
        await comicApi.upsertScene(folderId, {
          slug,
          name,
          description,
          prompt_fragment: promptFragment,
          refs,
        } as SceneCard)
      } else {
        await comicApi.upsertProp(folderId, {
          slug,
          name,
          description,
          prompt_fragment: promptFragment,
          refs,
        })
      }
      setCard({ ...card, name, description, prompt_fragment: promptFragment })
      if (draft) {
        useComicStore.getState().clearResourceDraft(folderId, draftKey, draft)
      }
      toast.success(t("assetSaved"))
      void useComicStore.getState().refresh(folderId)
    } catch (err) {
      toast.error(toErrorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const uploadTo = (slot: string) => {
    slotRef.current = slot
    fileRef.current?.click()
  }

  const onFile = (file: File) => {
    void file.arrayBuffer().then((buf) => {
      const bytes = new Uint8Array(buf)
      let binary = ""
      for (let i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i])
      }
      void comicApi
        .saveAssetImage(
          folderId,
          assetKind,
          slug,
          slotRef.current,
          btoa(binary)
        )
        .then(() => {
          toast.success(t("imageSaved"))
          return comicApi.getAsset(folderId, assetKind, slug)
        })
        .then((doc) => setCard(doc))
        .catch((err) => toast.error(toErrorMessage(err)))
    })
  }

  const imageButtons: {
    slot: string
    label: string
    path?: string | null
  }[] =
    assetKind === "character"
      ? [
          {
            slot: "turnaround/front",
            label: t("viewFront"),
            path: turnaround.front,
          },
          {
            slot: "turnaround/side_45",
            label: t("viewSide45"),
            path: turnaround.side_45,
          },
          {
            slot: "turnaround/side",
            label: t("viewSide"),
            path: turnaround.side,
          },
        ]
      : []

  return (
    <div className="max-w-2xl space-y-4 p-4 text-sm">
      <div className="flex items-center gap-2">
        <h3 className="text-base font-semibold">{slug}</h3>
        <Badge variant="outline">{assetKind}</Badge>
      </div>
      <div className="space-y-1.5">
        <Label>{t("assetName")}</Label>
        <Input
          value={name}
          onChange={(e) => updateFields({ name: e.target.value })}
          className="text-sm"
        />
      </div>
      <div className="space-y-1.5">
        <Label>{t("assetDescription")}</Label>
        <Textarea
          value={description}
          onChange={(e) => updateFields({ description: e.target.value })}
          className="min-h-16 text-sm"
        />
      </div>
      <div className="space-y-1.5">
        <Label>{t("assetPromptFragment")}</Label>
        <Textarea
          value={promptFragment}
          onChange={(e) => updateFields({ promptFragment: e.target.value })}
          className="min-h-16 text-sm"
        />
        <p className="text-xs text-muted-foreground">
          {t("promptFragmentHint")}
        </p>
      </div>

      {imageButtons.length > 0 && (
        <div className="space-y-1.5">
          <Label>{t("turnaround")}</Label>
          <div className="flex gap-2">
            {imageButtons.map((b) => (
              <div key={b.slot} className="flex flex-col items-center gap-1">
                <div className="flex h-24 w-20 items-center justify-center rounded-md border bg-muted/40 text-xs text-muted-foreground">
                  {b.path ? (
                    <ComicImage
                      folderId={folderId}
                      path={b.path}
                      alt={b.label}
                      className="h-24 w-20 rounded object-contain"
                    />
                  ) : (
                    <Plus className="size-4 opacity-50" />
                  )}
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => uploadTo(b.slot)}
                >
                  {b.label}
                </Button>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <Label>{t("refImages")}</Label>
          <Button variant="ghost" size="sm" onClick={() => uploadTo("refs")}>
            <Plus className="size-3" /> {t("upload")}
          </Button>
        </div>
        <div className="flex flex-wrap gap-2">
          {refs.map((path) => (
            <ComicImage
              key={path}
              folderId={folderId}
              path={path}
              alt={path.split("/").pop() ?? path}
              className="h-28 w-28 rounded object-contain"
            />
          ))}
          {refs.length === 0 && (
            <p className="text-xs text-muted-foreground">{t("noRefs")}</p>
          )}
        </div>
      </div>

      {assetKind === "character" && outfits.length > 0 && (
        <div className="space-y-1.5">
          <Label>{t("outfits")}</Label>
          <div className="flex flex-wrap gap-1.5">
            {outfits.map((o) => (
              <Badge key={o.slug} variant="secondary">
                {o.name}
              </Badge>
            ))}
          </div>
        </div>
      )}

      <div className="flex justify-end">
        <Button disabled={busy} onClick={() => void save()}>
          {busy ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <Save className="size-4" />
          )}
          {t("save")}
        </Button>
      </div>

      <input
        ref={fileRef}
        type="file"
        accept="image/png,image/jpeg,image/webp"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0]
          if (file) onFile(file)
        }}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Providers panel — project pointers over the global pool
// ---------------------------------------------------------------------------

function ProvidersPanel({
  folderId,
  overview,
}: {
  folderId: number
  overview: ComicOverviewPayload
}) {
  const t = useTranslations("Comic")
  const kinds: ("image" | "video" | "tts")[] = ["image", "video", "tts"]
  const setProvider = async (
    kind: "image" | "video" | "tts",
    providerId?: number
  ) => {
    try {
      await comicApi.setProvider(folderId, kind, providerId)
      await useComicStore.getState().refresh(folderId)
    } catch (err) {
      toast.error(toErrorMessage(err))
    }
  }

  return (
    <div className="max-w-xl space-y-4 p-4">
      <h3 className="text-sm font-semibold">{t("providers")}</h3>
      {kinds.map((kind) => {
        const options = overview.providerPool.filter((p) => p.kind === kind)
        const current = overview.series?.providers[kind] ?? null
        return (
          <div key={kind} className="flex items-center gap-2">
            <Label className="w-14 capitalize">{kind}</Label>
            <Select
              value={current ? String(current.id) : "none"}
              onValueChange={(v) =>
                void setProvider(kind, v === "none" ? undefined : Number(v))
              }
            >
              <SelectTrigger className="w-64">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">{t("noProvider")}</SelectItem>
                {options.map((p) => (
                  <SelectItem key={p.id} value={String(p.id)}>
                    {p.name}
                    {p.isDefault ? " ★" : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )
      })}
      {overview.providerPool.length === 0 && (
        <p className="text-xs text-muted-foreground">{t("poolEmpty")}</p>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Pipeline guidance card
// ---------------------------------------------------------------------------

function NextActionCard({ overview }: { overview: ComicOverviewPayload }) {
  const t = useTranslations("Comic")
  const na = overview.next_action
  if (!na) return null
  const label = (() => {
    switch (na.action) {
      case "provide_source":
        return t("nextProvideSource")
      case "adapt_script":
        return t("nextAdaptScript")
      case "design_assets":
        return t("nextDesignAssets")
      case "create_storyboard":
        return t("nextStoryboard", { episode: na.episode })
      case "generate_images":
        return t("nextImages", { episode: na.episode, count: na.pending_shots })
      case "generate_videos":
        return t("nextVideos", { episode: na.episode, count: na.pending_shots })
      case "compose_episode":
        return t("nextCompose", { episode: na.episode })
      default:
        return t("nextNone")
    }
  })()
  return (
    <div className="rounded-md border p-3">
      <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
        <Wand2 className="size-3.5" /> {t("nextAction")}
      </div>
      <p className="mt-1.5 leading-6">{label}</p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Init wizard
// ---------------------------------------------------------------------------

function InitWizardDialog({
  folderId,
  open,
  onOpenChange,
}: {
  folderId: number
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const t = useTranslations("Comic")
  const savedDraft = useComicStore((state) => state.drafts[folderId]?.setup)
  const draft: Extract<ComicResourceDraft, { kind: "setup" }> =
    savedDraft?.kind === "setup"
      ? savedDraft
      : {
          kind: "setup" as const,
          title: "",
          sourceMode: "original" as const,
          novelPath: "",
          fileBase64: null,
          fileName: null,
        }
  const { title, sourceMode, novelPath, fileBase64, fileName } = draft
  const updateDraft = (
    patch: Partial<Extract<ComicResourceDraft, { kind: "setup" }>>
  ) => {
    const state = useComicStore.getState()
    const current = state.drafts[folderId]?.setup
    state.setResourceDraft(folderId, "setup", {
      ...(current?.kind === "setup" ? current : draft),
      ...patch,
      kind: "setup",
    })
  }
  const [busy, setBusy] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)

  const canSubmit =
    title.trim().length > 0 &&
    (sourceMode !== "path" || novelPath.trim().length > 0) &&
    (sourceMode !== "upload" || fileBase64 != null)

  const submit = async () => {
    setBusy(true)
    let initialized = draft.initialized ?? false
    let submittedDraft = savedDraft
    updateDraft({ error: undefined })
    try {
      if (!initialized) {
        await comicApi.initProject(folderId, title.trim())
        initialized = true
        updateDraft({ initialized: true })
      }
      submittedDraft = useComicStore.getState().drafts[folderId]?.setup
      if (sourceMode === "original") {
        await comicApi.ingestSource(folderId, { original: true })
      } else if (sourceMode === "path") {
        await comicApi.ingestSource(folderId, {
          path: novelPath.trim(),
          title: title.trim(),
        })
      } else if (fileBase64 != null) {
        await comicApi.ingestSource(folderId, {
          uploadBase64: fileBase64,
          title: fileName ?? title.trim(),
        })
      }
      if (submittedDraft) {
        useComicStore
          .getState()
          .clearResourceDraft(folderId, "setup", submittedDraft)
      }
      await useComicStore.getState().refresh(folderId)
      onOpenChange(false)
    } catch (err) {
      const message = initialized
        ? t("initSourceFailed", { error: toErrorMessage(err) })
        : toErrorMessage(err)
      updateDraft({ error: message, initialized })
      toast.error(message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!busy) onOpenChange(value)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("initProject")}</DialogTitle>
          <DialogDescription>{t("initProjectDesc")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="comic-title">{t("seriesTitle")}</Label>
            <Input
              id="comic-title"
              disabled={busy || draft.initialized}
              value={title}
              onChange={(e) => updateDraft({ title: e.target.value })}
              placeholder={t("seriesTitlePlaceholder")}
            />
          </div>
          <div className="space-y-1.5">
            <Label>{t("adaptSource")}</Label>
            <Select
              disabled={busy}
              value={sourceMode}
              onValueChange={(v) =>
                updateDraft({ sourceMode: v as "upload" | "path" | "original" })
              }
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="upload">{t("sourceUpload")}</SelectItem>
                <SelectItem value="path">{t("sourcePath")}</SelectItem>
                <SelectItem value="original">{t("sourceOriginal")}</SelectItem>
              </SelectContent>
            </Select>
            {sourceMode === "upload" && (
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy}
                  onClick={() => fileRef.current?.click()}
                >
                  {t("chooseFile")}
                </Button>
                {fileName && <span className="text-xs">{fileName}</span>}
                <input
                  ref={fileRef}
                  type="file"
                  accept=".txt,.md,text/plain"
                  className="hidden"
                  onChange={(e) => {
                    const file = e.target.files?.[0]
                    if (!file) return
                    updateDraft({ fileName: file.name, fileBase64: null })
                    void readComicUpload(file)
                      .then((fileBase64) => updateDraft({ fileBase64 }))
                      .catch((error) => toast.error(toErrorMessage(error)))
                  }}
                />
              </div>
            )}
            {sourceMode === "path" && (
              <Input
                disabled={busy}
                value={novelPath}
                onChange={(e) => updateDraft({ novelPath: e.target.value })}
                placeholder={t("novelPathPlaceholder")}
              />
            )}
          </div>
        </div>
        {draft.error && (
          <p role="alert" className="text-sm text-destructive">
            {draft.error}
          </p>
        )}
        <DialogFooter>
          <Button disabled={!canSubmit || busy} onClick={() => void submit()}>
            {busy && <Loader2 className="size-4 animate-spin" />}
            {t("create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
