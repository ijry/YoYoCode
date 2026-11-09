"use client"

/**
 * Comic (漫剧) workbench state. One overview per open project folder, plus
 * which panel/selection the workbench shows. `comic://changed` events call
 * {@link ComicStore.refresh} — payloads are ids-only, so the store refetches
 * the overview instead of diffing.
 */

import { create } from "zustand"

import {
  comicApi,
  type ComicOverviewPayload,
  type MediaProviderInfo,
} from "@/lib/comic"
import { getTransport } from "@/lib/transport"
import { toErrorMessage } from "@/lib/app-error"
import type { StoryShot } from "@/lib/comic"

const requests = new Map<number, symbol>()

export type ComicChange =
  | { kind: "upsert"; folder_id: number; resource: string; id: string }
  | { kind: "task"; folder_id: number; task_id: number }
  | { kind: "refresh"; folder_id: number }

export type ComicPanelKind =
  | { kind: "storyboard"; episodeId: string }
  | { kind: "asset"; assetKind: "character" | "scene" | "prop"; slug: string }
  | { kind: "source" }
  | { kind: "providers" }

export interface ComicShotDraft {
  kind: "shot"
  shot: StoryShot | "new"
  description: string
  dialogue: { character: string; text: string }[]
  duration: string
  camera: string
  shotSize: string
  transition: string
  charRefs: { slug: string; outfit: string }[]
  sceneRef: string
  propRefs: string[]
}

export type ComicResourceDraft =
  | { kind: "script"; text: string }
  | { kind: "storyboard-import"; text: string }
  | { kind: "episode"; title: string; script: string; createdId?: string }
  | { kind: "asset"; name: string; description: string; promptFragment: string }
  | ComicShotDraft
  | {
      kind: "setup"
      title: string
      sourceMode: "upload" | "path" | "original"
      novelPath: string
      fileBase64: string | null
      fileName: string | null
      initialized?: boolean
      error?: string
    }

interface ComicState {
  /** Overview per folder id — survives switching between open projects. */
  overviews: Record<number, ComicOverviewPayload>
  /** Per-folder load/refresh errors (string when the folder is not comic). */
  errors: Record<number, string | null>
  loading: Record<number, boolean>
  /** What the middle column shows, per folder. */
  panel: Record<number, ComicPanelKind | null>
  /** Setup wizard open, per folder. */
  wizardOpen: Record<number, boolean>
  /** Unsaved edits survive file-tab and mobile-pane unmounts; never sent to disk. */
  drafts: Record<number, Record<string, ComicResourceDraft>>
  setResourceDraft: (
    folderId: number,
    key: string,
    draft: ComicResourceDraft
  ) => void
  clearResourceDraft: (
    folderId: number,
    key: string,
    expected: ComicResourceDraft
  ) => void

  load: (folderId: number) => Promise<void>
  refresh: (folderId: number) => Promise<void>
  openPanel: (folderId: number, panel: ComicPanelKind) => void
  setWizardOpen: (folderId: number, open: boolean) => void
  /** Server-mode subscription setup is handled by the caller; this only
   *  tears a folder out of the store when its tab closes. */
  forget: (folderId: number) => void
}

export const useComicStore = create<ComicState>((set, get) => ({
  overviews: {},
  errors: {},
  loading: {},
  panel: {},
  wizardOpen: {},
  drafts: {},

  setResourceDraft: (folderId, key, draft) =>
    set((state) => ({
      drafts: {
        ...state.drafts,
        [folderId]: { ...state.drafts[folderId], [key]: draft },
      },
    })),

  clearResourceDraft: (folderId, key, expected) =>
    set((state) => {
      if (state.drafts[folderId]?.[key] !== expected) return state
      const next = { ...state.drafts[folderId] }
      delete next[key]
      return { drafts: { ...state.drafts, [folderId]: next } }
    }),

  load: async (folderId) => {
    const request = Symbol()
    requests.set(folderId, request)
    set((s) => ({ loading: { ...s.loading, [folderId]: true } }))
    try {
      const overview = await comicApi.getOverview(folderId)
      if (requests.get(folderId) !== request) return
      set((s) => ({
        overviews: { ...s.overviews, [folderId]: overview },
        errors: { ...s.errors, [folderId]: null },
      }))
    } catch (err) {
      if (requests.get(folderId) !== request) return
      set((s) => ({
        errors: {
          ...s.errors,
          [folderId]: toErrorMessage(err),
        },
      }))
    } finally {
      if (requests.get(folderId) === request) {
        requests.delete(folderId)
        set((s) => ({ loading: { ...s.loading, [folderId]: false } }))
      }
    }
  },

  refresh: async (folderId) => {
    await get().load(folderId)
  },

  openPanel: (folderId, panel) =>
    set((s) => ({ panel: { ...s.panel, [folderId]: panel } })),

  setWizardOpen: (folderId, open) =>
    set((s) => ({ wizardOpen: { ...s.wizardOpen, [folderId]: open } })),

  forget: (folderId) => {
    requests.delete(folderId)
    set((s) => {
      const drop = <T>(rec: Record<number, T>) => {
        const next = { ...rec }
        delete next[folderId]
        return next
      }
      return {
        overviews: drop(s.overviews),
        errors: drop(s.errors),
        loading: drop(s.loading),
        panel: drop(s.panel),
        wizardOpen: drop(s.wizardOpen),
        drafts: drop(s.drafts),
      }
    })
  },
}))

/** Subscribe to `comic://changed` once per page mount; returns a disposer.
 *  Payloads are ids-only (`ComicChange`), so the handler just refetches. */
export function subscribeComicChanged(
  onNotice: (folderId: number) => void
): () => void {
  const transport = getTransport()
  let disposed = false
  let unsubscribe: (() => void) | undefined
  void transport
    .subscribe<ComicChange>("comic://changed", (change) => {
      if (!disposed && Number.isInteger(change?.folder_id)) {
        onNotice(change.folder_id)
      }
    })
    .then((fn) => {
      if (disposed) fn()
      else unsubscribe = fn
    })
    .catch((error: unknown) => {
      // A disconnected transport must not turn mounting the workbench into
      // an unhandled rejection. Manual refresh remains available.
      if (!disposed)
        console.warn("Comic event subscription failed:", toErrorMessage(error))
    })
  return () => {
    disposed = true
    unsubscribe?.()
  }
}

export function selectProviderForKind(
  pool: MediaProviderInfo[],
  kind: "image" | "video" | "tts"
): MediaProviderInfo[] {
  return pool.filter((p) => p.kind === kind)
}
