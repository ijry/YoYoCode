"use client"

import { useRef, useState } from "react"
import { Loader2 } from "lucide-react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
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
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { extractAppCommandError, toErrorMessage } from "@/lib/app-error"
import {
  comicApi,
  type StoryboardSnapshot,
  type StoryShotInput,
} from "@/lib/comic"
import {
  ComicStoryboardImportError,
  parseComicStoryboardImport,
} from "@/lib/comic-storyboard-import"
import { useComicStore, type ComicResourceDraft } from "@/stores/comic-store"

export function ComicStoryboardImportDialog({
  folderId,
  episodeId,
  open,
  onOpenChange,
  onImported,
}: {
  folderId: number
  episodeId: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: () => Promise<void>
}) {
  const t = useTranslations("Comic")
  const draftKey = `episodes/${episodeId}/storyboard-import`
  const draft = useComicStore((s) => s.drafts[folderId]?.[draftKey])
  const text = draft?.kind === "storyboard-import" ? draft.text : ""
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [needsConfirmation, setNeedsConfirmation] = useState(false)
  const [pending, setPending] = useState<{
    shots: StoryShotInput[]
    count: number
    expectedStoryboard: StoryboardSnapshot
    draft: ComicResourceDraft | undefined
  } | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const updateText = (value: string) => {
    useComicStore.getState().setResourceDraft(folderId, draftKey, {
      kind: "storyboard-import",
      text: value,
    })
    setError(null)
  }
  const reportError = (err: unknown) =>
    setError(
      err instanceof ComicStoryboardImportError
        ? t("storyboardImportInvalid", { reason: t(err.key, err.params) })
        : toErrorMessage(err)
    )

  const replace = async (
    shots: StoryShotInput[],
    submittedDraft: ComicResourceDraft | undefined,
    expectedStoryboard: StoryboardSnapshot
  ) => {
    setBusy(true)
    try {
      await comicApi.replaceStoryboard(
        folderId,
        episodeId,
        shots,
        expectedStoryboard
      )
      if (submittedDraft)
        useComicStore
          .getState()
          .clearResourceDraft(folderId, draftKey, submittedDraft)
      await onImported()
      await useComicStore.getState().refresh(folderId)
      toast.success(t("storyboardImported"))
      setPending(null)
      onOpenChange(false)
    } catch (err) {
      setPending(null)
      if (extractAppCommandError(err)?.code === "conflict") {
        setError(t("storyboardImportConflict"))
        setNeedsConfirmation(true)
        await useComicStore.getState().refresh(folderId)
        // Leave the submitted draft intact. A new submit reads the latest board
        // and opens a new confirmation; never retry a stale destructive import.
      } else {
        reportError(err)
      }
    } finally {
      setBusy(false)
    }
  }
  const submit = async () => {
    setError(null)
    setBusy(true)
    try {
      const shots = parseComicStoryboardImport(text)
      // The board may have changed since the panel loaded. Confirm against a
      // fresh read, including AI writes received while this dialog was open.
      const current = await comicApi.getAsset(folderId, "episode", episodeId)
      const board = current.storyboard as StoryboardSnapshot | undefined
      const expectedStoryboard = board ?? { shots: [] }
      const count = expectedStoryboard.shots.length
      if (count || needsConfirmation)
        setPending({ shots, count, draft, expectedStoryboard })
      else await replace(shots, draft, expectedStoryboard)
    } catch (err) {
      reportError(err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(value) => {
          if (!busy) onOpenChange(value)
        }}
      >
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t("storyboardImport")}</DialogTitle>
            <DialogDescription>
              {t("storyboardImportHint")} {t("storyboardVideoRetainedHint")}
            </DialogDescription>
          </DialogHeader>
          <Label htmlFor={`comic-storyboard-json-${episodeId}`}>
            {t("storyboardJson")}
          </Label>
          <Textarea
            id={`comic-storyboard-json-${episodeId}`}
            className="min-h-64 max-h-96 font-mono text-xs"
            value={text}
            disabled={busy}
            placeholder={'[{"id":"shot-0001","description":"Opening scene"}]'}
            onChange={(e) => updateText(e.target.value)}
          />
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <input
            ref={fileRef}
            type="file"
            accept=".json,application/json"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0]
              if (file) void file.text().then(updateText).catch(reportError)
              e.target.value = ""
            }}
          />
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => fileRef.current?.click()}
            >
              {t("chooseFile")}
            </Button>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => onOpenChange(false)}
            >
              {t("cancel")}
            </Button>
            <Button
              disabled={busy || !text.trim()}
              onClick={() => void submit()}
            >
              {busy && <Loader2 className="size-4 animate-spin" />}
              {t("storyboardImport")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <AlertDialog
        open={pending !== null}
        onOpenChange={(value) => {
          if (!value && !busy) setPending(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("storyboardReplaceTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("storyboardReplaceHint", {
                count: pending?.count ?? 0,
                episode: episodeId,
              })}{" "}
              {t("storyboardVideoRetainedHint")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={busy}
              onClick={(e) => {
                e.preventDefault()
                if (pending)
                  void replace(
                    pending.shots,
                    pending.draft,
                    pending.expectedStoryboard
                  )
              }}
            >
              {t("storyboardReplace")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
