"use client"

/**
 * Comic (漫剧) production API — the frontend mirror of
 * `src-tauri/src/commands/comic.rs`. Story-document payloads keep the
 * snake_case field names of the JSON files on disk (they ARE the documents);
 * command envelopes (folderId, providerPool, ...) are camelCase like the rest
 * of the transport.
 */

import { getTransport } from "@/lib/transport"

// ---------------------------------------------------------------------------
// Story-tree document types (snake_case, mirroring the files)
// ---------------------------------------------------------------------------

export interface ProviderRef {
  id: number
  name: string
}

export interface ProviderRefs {
  image?: ProviderRef | null
  video?: ProviderRef | null
  tts?: ProviderRef | null
}

export interface CandidateRetention {
  keep_count: number
  max_age_days: number
}

export interface SeriesConfig {
  title: string
  adapted_from?: string | null
  style_bible?: string | null
  bible_refs: string[]
  global_seed?: number | null
  default_candidate_count: number
  max_images_per_request: number
  max_concurrent_tasks: number
  providers: ProviderRefs
  candidate_retention: CandidateRetention
}

export interface Chapter {
  no: number
  title: string
  start_offset: number
  end_offset: number
  chars: number
}

export type EpisodeStatus =
  | "draft"
  | "scripted"
  | "storyboarded"
  | "rendering"
  | "done"

export type ShotState =
  | "draft"
  | "prompted"
  | "generating"
  | "candidate"
  | "selected"
  | "rendering"
  | "done"

export interface CharacterRef {
  slug: string
  outfit?: string
}

export interface ShotRefs {
  characters: CharacterRef[]
  scene?: string | null
  props: string[]
}

export interface StoryShot {
  id: string
  no: number
  description: string
  dialogue: { character?: string | null; text: string }[]
  duration_secs: number
  camera?: string | null
  shot_size?: string | null
  transition?: string | null
  refs: ShotRefs
  state: ShotState
  selected?: string | null
  audio?: { path: string; duration_secs: number; task_id: number } | null
  video?: { path: string; duration_secs: number; task_id: number } | null
}

export interface PropCard {
  slug: string
  name: string
  description: string
  prompt_fragment: string
  refs: string[]
}

export interface CharacterCard {
  slug: string
  name: string
  description: string
  personality: string
  prompt_fragment: string
  seed?: number | null
  turnaround: {
    front?: string | null
    side_45?: string | null
    side?: string | null
  }
  outfits: {
    slug: string
    name: string
    prompt_fragment: string
    front?: string | null
    side_45?: string | null
    side?: string | null
  }[]
  expressions: string[]
  refs: string[]
}

export interface SceneCard {
  slug: string
  name: string
  description: string
  prompt_fragment: string
  refs: string[]
}

export type NextAction =
  | { action: "provide_source" }
  | { action: "adapt_script" }
  | { action: "design_assets" }
  | { action: "create_storyboard"; episode: string }
  | { action: "generate_images"; episode: string; pending_shots: number }
  | { action: "generate_videos"; episode: string; pending_shots: number }
  | { action: "compose_episode"; episode: string }
  | { action: "none" }

export interface SourceMeta {
  mode: "upload" | "external_path" | "original" | null
  novel_file?: string | null
  external_path?: string | null
  title?: string | null
  char_count: number
  chapter_count: number
  encoding?: string | null
}

export interface ProjectOverview {
  series?: SeriesConfig | null
  source?: SourceMeta | null
  has_source: boolean
  source_hint?: string | null
  episodes: {
    id: string
    title: string
    status: EpisodeStatus
    shots_total: number
    shots_done: number
  }[]
  characters: {
    kind: string
    slug: string
    name: string
    has_ref_images: boolean
  }[]
  scenes: {
    kind: string
    slug: string
    name: string
    has_ref_images: boolean
  }[]
  props: { kind: string; slug: string; name: string; has_ref_images: boolean }[]
  next_action?: NextAction | null
}

// ---------------------------------------------------------------------------
// Command envelopes (camelCase, mirroring the commands)
// ---------------------------------------------------------------------------

export interface MediaProviderInfo {
  id: number
  kind: "image" | "video" | "tts"
  name: string
  apiUrl: string
  model: string
  extraJson: string
  isDefault: boolean
  capabilities?: {
    kind: "image" | "video" | "tts"
    protocol: string
    available: boolean
    maxCandidates: number
    maxReferences: number
    seed: boolean
    negativePrompt: boolean
    size: boolean
    video?: {
      minDurationSecs: number
      maxDurationSecs: number
    } | null
    speech?: {
      formats: string[]
      voices: string[]
      defaultVoice?: string | null
      minSpeed: number
      maxSpeed: number
      maxChars: number
      instructions: boolean
    } | null
  }
}

export interface ComicOverviewPayload extends ProjectOverview {
  folderId: number
  providerPool: MediaProviderInfo[]
}

export interface SourceReport {
  mode: string
  title?: string | null
  encoding: string
  total_chars: number
  chapter_count: number
  strategy: string
  preview_titles: string[]
}

export interface GenerationTaskView {
  id: number
  folderId: number
  kind: string
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled"
  providerName?: string | null
  model?: string | null
  targetKind?: string | null
  targetId?: string | null
  artifactPath?: string | null
  errorKind?: string | null
  errorMessage?: string | null
  candidates: string[]
  createdAt: string
}

export interface SourceReadRequest {
  fromChapter: number
  toChapter?: number
  maxChars?: number
  offsetChars?: number
}

export interface WindowRead {
  from_chapter: number
  to_chapter: number
  offset_chars: number
  returned_chars: number
  has_more: boolean
  next_offset?: number | null
  text: string
}

export interface SourceIngestParams {
  uploadBase64?: string
  path?: string
  original?: boolean
  title?: string
  maxHeadingLen?: number
  fallbackWindowChars?: number
}

export interface SeriesPatchParams {
  title?: string
  adaptedFrom?: string
  styleBible?: string
  bibleRefs?: string[]
  globalSeed?: number
  defaultCandidateCount?: number
  maxImagesPerRequest?: number
  maxConcurrentTasks?: number
}

export interface StoryboardSnapshot {
  shots: StoryShot[]
}

export interface EpisodeMetaInput {
  id: string
  title: string
  synopsis?: string
  status?: EpisodeStatus
  source_chapters?: number[]
}

export interface StoryShotInput {
  id: string
  description: string
  dialogue?: { character?: string | null; text: string }[]
  duration_secs?: number
  camera?: string | null
  shot_size?: string | null
  transition?: string | null
  refs?: Partial<ShotRefs>
  state?: ShotState
  selected?: string | null
  audio?: { path: string; duration_secs: number; task_id: number } | null
  video?: { path: string; duration_secs: number; task_id: number } | null
}

// ---------------------------------------------------------------------------
// API wrappers
// ---------------------------------------------------------------------------

export const comicApi = {
  getOverview: (folderId: number): Promise<ComicOverviewPayload> =>
    getTransport().call("comic_get_overview", { folderId }),

  initProject: (
    folderId: number,
    title: string
  ): Promise<ComicOverviewPayload> =>
    getTransport().call("comic_init_project", { folderId, title }),

  ingestSource: (
    folderId: number,
    params: SourceIngestParams
  ): Promise<SourceReport> =>
    getTransport().call("comic_ingest_source", { folderId, params }),

  readSource: (
    folderId: number,
    window: SourceReadRequest
  ): Promise<WindowRead> =>
    getTransport().call("comic_read_source", {
      folderId,
      window: {
        from_chapter: window.fromChapter,
        to_chapter: window.toChapter,
        max_chars: window.maxChars,
        offset_chars: window.offsetChars,
      },
    }),

  listChapters: (folderId: number, limit?: number): Promise<Chapter[]> =>
    getTransport().call("comic_list_chapters", {
      folderId,
      limit: limit ?? null,
    }),

  listAssets: (
    folderId: number,
    kind: "episode" | "character" | "scene" | "prop",
    offset?: number,
    limit?: number
  ): Promise<{ kind: string; total: number; items: unknown[] }> =>
    getTransport().call("comic_list_assets", {
      folderId,
      kind,
      offset: offset ?? null,
      limit: limit ?? null,
    }),

  getAsset: (
    folderId: number,
    kind: "episode" | "character" | "scene" | "prop",
    id: string
  ): Promise<Record<string, unknown>> =>
    getTransport().call("comic_get_asset", { folderId, kind, id }),

  upsertEpisode: (
    folderId: number,
    meta: EpisodeMetaInput,
    options?: { createOnly?: boolean; expectedMeta?: EpisodeMetaInput }
  ): Promise<EpisodeMetaInput> =>
    getTransport().call("comic_upsert_episode", {
      folderId,
      meta: {
        status: "draft",
        ...meta,
      },
      ...options,
    }),

  writeScript: (
    folderId: number,
    episode: string,
    script: string,
    options?: { createOnly?: boolean }
  ): Promise<void> =>
    getTransport().call("comic_write_script", {
      folderId,
      episode,
      script,
      ...options,
    }),

  replaceStoryboard: (
    folderId: number,
    episode: string,
    shots: StoryShotInput[],
    expectedStoryboard?: StoryboardSnapshot
  ): Promise<{ shots: StoryShot[] }> =>
    getTransport().call("comic_replace_storyboard", {
      folderId,
      episode,
      shots,
      ...(expectedStoryboard ? { expectedStoryboard } : {}),
    }),

  upsertShot: (
    folderId: number,
    episode: string,
    shot: StoryShotInput
  ): Promise<void> =>
    getTransport().call("comic_upsert_shot", { folderId, episode, shot }),

  deleteShot: (
    folderId: number,
    episode: string,
    shot: string
  ): Promise<void> =>
    getTransport().call("comic_delete_shot", { folderId, episode, shot }),

  reorderShots: (
    folderId: number,
    episode: string,
    order: string[]
  ): Promise<{ shots: StoryShot[] }> =>
    getTransport().call("comic_reorder_shots", { folderId, episode, order }),

  upsertCharacter: (
    folderId: number,
    card: Partial<CharacterCard> & { slug: string; name: string }
  ): Promise<{
    slug: string
    affectedShots: { episode: string; shotId: string }[]
  }> => getTransport().call("comic_upsert_character", { folderId, card }),

  upsertScene: (
    folderId: number,
    card: Partial<SceneCard> & { slug: string; name: string }
  ): Promise<void> =>
    getTransport().call("comic_upsert_scene", { folderId, card }),

  upsertProp: (
    folderId: number,
    card: Partial<PropCard> & { slug: string; name: string }
  ): Promise<void> =>
    getTransport().call("comic_upsert_prop", { folderId, card }),

  saveAssetImage: (
    folderId: number,
    kind: "character" | "scene" | "prop",
    slug: string,
    slot: string,
    imageBase64: string
  ): Promise<string> =>
    getTransport().call("comic_save_asset_image", {
      folderId,
      kind,
      slug,
      slot,
      imageBase64,
    }),

  listTasks: (
    folderId: number,
    limit?: number,
    episode?: string
  ): Promise<GenerationTaskView[]> =>
    getTransport().call("comic_list_tasks", {
      folderId,
      episode: episode ?? null,
      limit: limit ?? null,
    }),

  requestSpeech: (
    folderId: number,
    params: {
      episode: string
      shot: string
      text?: string
      voice?: string
      speed?: number
      responseFormat?: "mp3" | "wav"
      instructions?: string
      providerId?: number
      providerName?: string
    }
  ): Promise<{ taskId: number }> =>
    getTransport().call("comic_request_speech", { folderId, params }),

  bindSpeech: (
    folderId: number,
    episode: string,
    shot: string,
    taskId: number
  ): Promise<{ bound: boolean }> =>
    getTransport().call("comic_set_shot_audio", {
      folderId,
      episode,
      shot,
      taskId,
    }),

  /**
   * Bind a succeeded video task after explicit user confirmation. The
   * backend compares `expectedCurrentTaskId` against the binding observed
   * when the confirmation opened and refuses a stale overwrite with a
   * `conflict` error.
   */
  bindVideo: (
    folderId: number,
    episode: string,
    shot: string,
    taskId: number,
    expectedCurrentTaskId: number | null
  ): Promise<{ bound: boolean; video: string; durationSecs: number }> =>
    getTransport().call("comic_set_shot_video", {
      folderId,
      episode,
      shot,
      taskId,
      expectedCurrentTaskId,
    }),

  /**
   * Clear a shot's bound clip after explicit user confirmation. The backend
   * compares `expectedCurrentTaskId` against the binding observed when the
   * confirmation opened and refuses a stale clear with a `conflict` error.
   */
  clearShotVideo: (
    folderId: number,
    episode: string,
    shot: string,
    expectedCurrentTaskId: number | null
  ): Promise<{ unbound: boolean }> =>
    getTransport().call("comic_clear_shot_video", {
      folderId,
      episode,
      shot,
      expectedCurrentTaskId,
    }),

  /** Fetch a candidate endpoint's model IDs for the provider editor. */
  listProviderModels: (params: {
    kind: string
    apiUrl: string
    apiKey: string
    extraJson: string
  }): Promise<{ models: string[] }> =>
    getTransport().call("comic_list_provider_models", params),

  /**
   * Mint a short-lived, task-scoped preview grant for a succeeded video
   * artifact. The grant authorizes range requests for that artifact only.
   */
  requestVideoPreview: (
    folderId: number,
    taskId: number
  ): Promise<{ token: string; url: string | null }> =>
    getTransport().call("comic_request_video_preview", { folderId, taskId }),

  composeEpisode: (
    folderId: number,
    params: {
      episode: string
      width?: number
      height?: number
      fps?: number
      kenBurns?: boolean
      burnSubtitles?: boolean
    }
  ): Promise<{ taskId: number }> =>
    getTransport().call("comic_compose_episode", { folderId, params }),

  checkCompose: (
    folderId: number,
    taskId: number,
    waitMs = 0
  ): Promise<GenerationTaskView> =>
    getTransport().call("comic_check_compose", { folderId, taskId, waitMs }),

  bindCandidate: (
    folderId: number,
    episode: string,
    shot: string,
    candidate: string
  ): Promise<unknown> =>
    getTransport().call("comic_bind_candidate", {
      folderId,
      episode,
      shot,
      candidate,
    }),

  upsertSeries: (
    folderId: number,
    patch: SeriesPatchParams
  ): Promise<SeriesConfig> =>
    getTransport().call("comic_upsert_series", { folderId, patch }),

  setProvider: (
    folderId: number,
    kind: "image" | "video" | "tts",
    providerId?: number,
    providerName?: string
  ): Promise<SeriesConfig> =>
    getTransport().call("comic_set_provider", {
      folderId,
      kind,
      providerId: providerId ?? null,
      providerName: providerName ?? null,
    }),

  listMediaProviders: (): Promise<MediaProviderInfo[]> =>
    getTransport().call("list_media_providers"),

  createMediaProvider: (params: {
    kind: "image" | "video" | "tts"
    name: string
    apiUrl: string
    apiKey: string
    model: string
    extraJson?: string
    isDefault?: boolean
  }): Promise<MediaProviderInfo> =>
    getTransport().call("create_media_provider", { params }),

  updateMediaProvider: (
    id: number,
    params: {
      name?: string
      apiUrl?: string
      apiKey?: string
      model?: string
      extraJson?: string
      isDefault?: boolean
    }
  ): Promise<MediaProviderInfo> =>
    getTransport().call("update_media_provider", { id, params }),

  deleteMediaProvider: (id: number): Promise<void> =>
    getTransport().call("delete_media_provider", { id }),

  testMediaProvider: (
    id: number
  ): Promise<{
    ok: boolean
    urlOk: boolean
    hasKey: boolean
    kind: string
    model: string
  }> => getTransport().call("test_media_provider", { id }),
}
