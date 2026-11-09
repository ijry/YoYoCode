"use client"

import { useEffect, useState } from "react"
import { Loader2, SquarePen, Plus } from "lucide-react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
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
import { Textarea } from "@/components/ui/textarea"
import { extractAppCommandError, toErrorMessage } from "@/lib/app-error"
import {
  comicApi,
  type ComicOverviewPayload,
  type EpisodeMetaInput,
} from "@/lib/comic"
import { useComicStore } from "@/stores/comic-store"

function nextEpisodeId(episodes: { id: string }[]): string {
  const ids = new Set(episodes.map((ep) => ep.id))
  let number = 1
  while (ids.has(`ep${String(number).padStart(3, "0")}`)) number++
  return `ep${String(number).padStart(3, "0")}`
}

export function ComicEpisodeManager({
  folderId,
  overview,
  selectedId,
  createOpen,
  onCreateOpenChange,
}: {
  folderId: number
  overview: ComicOverviewPayload
  selectedId?: string
  createOpen: boolean
  onCreateOpenChange: (open: boolean) => void
}) {
  const t = useTranslations("Comic")
  const [editingId, setEditingId] = useState<string | null>(null)
  return (
    <section
      className="shrink-0 space-y-2 border-b p-3"
      aria-label={t("seriesEpisodes")}
    >
      <div className="flex items-center justify-between gap-2">
        <h4 className="text-xs font-medium text-muted-foreground">
          {t("seriesEpisodes")} · {overview.episodes.length}
        </h4>
        <div className="flex gap-1">
          {selectedId && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setEditingId(selectedId)}
            >
              <SquarePen className="size-3.5" />
              {t("episodeEdit")}
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            onClick={() => onCreateOpenChange(true)}
          >
            <Plus className="size-3.5" />
            {t("episodeNew")}
          </Button>
        </div>
      </div>
      {overview.episodes.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t("episodeEmpty")}</p>
      ) : (
        <nav
          className="flex max-h-32 flex-wrap gap-1.5 overflow-y-auto"
          aria-label={t("episodes")}
        >
          {overview.episodes.map((ep) => (
            <Button
              key={ep.id}
              variant={selectedId === ep.id ? "secondary" : "ghost"}
              size="sm"
              aria-pressed={selectedId === ep.id}
              onClick={() =>
                useComicStore
                  .getState()
                  .openPanel(folderId, { kind: "storyboard", episodeId: ep.id })
              }
            >
              <span className="max-w-52 truncate">
                {ep.id} · {ep.title || ep.id}
              </span>
              <span className="text-muted-foreground">
                {ep.shots_done}/{ep.shots_total}
              </span>
            </Button>
          ))}
        </nav>
      )}
      {(createOpen || editingId) && (
        <EpisodeDialog
          key={`${folderId}:${editingId ?? "new"}`}
          folderId={folderId}
          overview={overview}
          editingId={editingId}
          onClose={() => {
            setEditingId(null)
            onCreateOpenChange(false)
          }}
        />
      )}
    </section>
  )
}

function EpisodeDialog({
  folderId,
  overview,
  editingId,
  onClose,
}: {
  folderId: number
  overview: ComicOverviewPayload
  editingId: string | null
  onClose: () => void
}) {
  const t = useTranslations("Comic")
  const draftKey = editingId ? `episodes/${editingId}/meta` : "episode-create"
  const savedDraft = useComicStore((s) => s.drafts[folderId]?.[draftKey])
  const [meta, setMeta] = useState<EpisodeMetaInput | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const draft =
    savedDraft?.kind === "episode"
      ? savedDraft
      : { kind: "episode" as const, title: meta?.title ?? "", script: "" }
  const id = editingId ?? draft.createdId ?? nextEpisodeId(overview.episodes)
  const update = (patch: Partial<typeof draft>) =>
    useComicStore
      .getState()
      .setResourceDraft(folderId, draftKey, { ...draft, ...patch })

  useEffect(() => {
    if (!editingId) return
    let cancelled = false
    void comicApi
      .getAsset(folderId, "episode", editingId)
      .then((doc) => {
        if (cancelled) return
        setMeta({
          id: editingId,
          title: typeof doc.title === "string" ? doc.title : "",
          synopsis: doc.synopsis as string | undefined,
          status: doc.status as EpisodeMetaInput["status"],
          source_chapters: doc.source_chapters as number[] | undefined,
        })
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(toErrorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [folderId, editingId])

  const save = async () => {
    if (busy) return
    if (!draft.title.trim()) {
      setError(t("episodeTitleRequired"))
      return
    }
    setBusy(true)
    setError(null)
    let createdId = draft.createdId
    let writingScript = false
    let finalDraft = savedDraft
    try {
      if (editingId) {
        if (!meta) return
        // Fetch again at save time so background script/episode edits are kept.
        const latest = await comicApi.getAsset(folderId, "episode", editingId)
        const expectedMeta: EpisodeMetaInput = {
          id: editingId,
          title: latest.title as string,
          synopsis: latest.synopsis as string | undefined,
          status: latest.status as EpisodeMetaInput["status"],
          source_chapters: latest.source_chapters as number[] | undefined,
        }
        await comicApi.upsertEpisode(
          folderId,
          { ...expectedMeta, title: draft.title.trim() },
          { expectedMeta }
        )
      } else {
        if (!createdId) {
          // The overview is only a hint. Claim the directory atomically and
          // skip conflicts even when a refresh still returns an older overview.
          const occupied: { id: string }[] = [...overview.episodes]
          for (let attempt = 0; attempt < 10; attempt++) {
            const latest = await comicApi.getOverview(folderId)
            occupied.push(...latest.episodes)
            const candidate = nextEpisodeId(occupied)
            try {
              await comicApi.upsertEpisode(
                folderId,
                { id: candidate, title: draft.title.trim(), status: "draft" },
                { createOnly: true }
              )
              createdId = candidate
              break
            } catch (err) {
              if (
                extractAppCommandError(err)?.code !== "conflict" ||
                attempt === 9
              )
                throw err
              occupied.push({ id: candidate })
            }
          }
          finalDraft = { ...draft, createdId }
          useComicStore
            .getState()
            .setResourceDraft(folderId, draftKey, finalDraft)
        }
        // Once allocated, retry only this episode's missing script. Re-upserting
        // metadata here would erase another writer's work after a failed write.
        if (draft.script.trim()) {
          writingScript = true
          await comicApi.writeScript(folderId, createdId!, draft.script, {
            createOnly: true,
          })
          writingScript = false
        }
      }
      if (finalDraft)
        useComicStore
          .getState()
          .clearResourceDraft(folderId, draftKey, finalDraft)
      useComicStore.getState().openPanel(folderId, {
        kind: "storyboard",
        episodeId: editingId ?? createdId!,
      })
      await useComicStore.getState().refresh(folderId)
      toast.success(t("episodeCreated"))
      onClose()
    } catch (err) {
      const message = writingScript
        ? t("episodeScriptFailed", { error: toErrorMessage(err) })
        : toErrorMessage(err)
      setError(message)
      toast.error(message)
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
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {t(editingId ? "episodeEdit" : "episodeNew")}
          </DialogTitle>
          <DialogDescription>
            {t(editingId ? "episodeEditHint" : "episodeCreateHint")}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            {t("episodeId")}: {id}
          </p>
          <Label htmlFor="comic-episode-title">{t("episodeTitle")}</Label>
          <Input
            id="comic-episode-title"
            value={draft.title}
            disabled={busy || !!draft.createdId || (!!editingId && !meta)}
            onChange={(e) => update({ title: e.target.value })}
          />
          {!editingId && (
            <>
              <Label htmlFor="comic-episode-script">{t("script")}</Label>
              <Textarea
                id="comic-episode-script"
                value={draft.script}
                disabled={busy}
                onChange={(e) => update({ script: e.target.value })}
                placeholder={t("scriptPlaceholder")}
                className="min-h-48 max-h-80 font-mono text-xs"
              />
            </>
          )}
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={busy || !draft.title.trim() || (!!editingId && !meta)}
            onClick={() => void save()}
          >
            {busy && <Loader2 className="size-4 animate-spin" />}
            {t(editingId ? "save" : "create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
