import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ComicWorkbenchPanel } from "./comic-workbench-panel"
import { useComicStore } from "@/stores/comic-store"
import {
  consumeAskSelectionPrompts,
  resetAskSelectionPromptsForTests,
} from "@/lib/ask-selection-handoff"
import type { ComicOverviewPayload } from "@/lib/comic"
import messages from "@/i18n/messages/en.json"

const mocks = vi.hoisted(() => ({
  overview: vi.fn(),
  asset: vi.fn(),
  tasks: vi.fn(),
  bindAudio: vi.fn(),
  bindVideo: vi.fn(),
  clearShotVideo: vi.fn(),
  preview: vi.fn(),
  upsertShot: vi.fn(),
  subscribe: vi.fn(),
  createConversation: vi.fn(),
  listConversations: vi.fn(),
  openTab: vi.fn(),
  setRoute: vi.fn(),
  activeFolderId: 7,
  isDesktop: false,
  isRemote: false,
  tab: {
    id: "current-conversation",
    kind: "conversation",
    folderId: 7,
    agentType: "codex",
    conversationId: null,
    title: "Draft",
    isPinned: false,
  } as Record<string, unknown> | null,
}))
vi.mock("@/contexts/active-folder-context", () => ({
  useActiveFolder: () => ({
    activeFolderId: mocks.activeFolderId,
    activeFolder: { id: mocks.activeFolderId, path: "D:/comic" },
  }),
}))
vi.mock("@/contexts/workbench-route-context", () => ({
  useWorkbenchRoute: () => ({ setRoute: mocks.setRoute }),
}))
vi.mock("@/stores/tab-store", () => {
  const getState = () => ({
    tabs: mocks.tab ? [mocks.tab] : [],
    activeTabId: mocks.tab?.id ?? null,
    openTab: mocks.openTab,
  })
  return {
    useTabStore: Object.assign(
      (selector: (state: ReturnType<typeof getState>) => unknown) =>
        selector(getState()),
      { getState }
    ),
  }
})
vi.mock("@/lib/api", () => ({
  createConversation: mocks.createConversation,
  listAllConversations: mocks.listConversations,
  listConversations: mocks.listConversations,
}))
vi.mock("@/lib/comic", () => ({
  comicApi: {
    getOverview: mocks.overview,
    getAsset: mocks.asset,
    listTasks: mocks.tasks,
    bindSpeech: mocks.bindAudio,
    bindVideo: mocks.bindVideo,
    clearShotVideo: mocks.clearShotVideo,
    requestVideoPreview: mocks.preview,
    upsertShot: mocks.upsertShot,
  },
}))
vi.mock("@/lib/transport", () => ({
  getTransport: () => ({ subscribe: mocks.subscribe }),
  isDesktop: () => mocks.isDesktop,
  isRemoteDesktopMode: () => mocks.isRemote,
  getServerBaseUrl: () => "http://workbench.local",
}))

vi.mock("@/lib/comic-export", () => ({
  openComicExport: vi.fn(),
  openComicShotClip: vi.fn(),
}))

function overview(folderId: number): ComicOverviewPayload {
  return {
    folderId,
    has_source: true,
    characters: [],
    scenes: [],
    props: [],
    providerPool: [],
    episodes: [
      {
        id: "ep001",
        title: "One",
        status: "draft",
        shots_total: 1,
        shots_done: 0,
      },
      {
        id: "ep002",
        title: "Two",
        status: "draft",
        shots_total: 0,
        shots_done: 0,
      },
    ],
  }
}
function renderPanel() {
  return render(
    <NextIntlClientProvider locale="en" messages={messages}>
      <ComicWorkbenchPanel />
    </NextIntlClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  resetAskSelectionPromptsForTests()
  mocks.activeFolderId = 7
  mocks.tab = {
    id: "current-conversation",
    kind: "conversation",
    folderId: 7,
    agentType: "codex",
    conversationId: null,
    title: "Draft",
    isPinned: false,
  }
  mocks.overview.mockImplementation(async (folderId) => overview(folderId))
  mocks.listConversations.mockResolvedValue([])
  mocks.tasks.mockResolvedValue([])
  mocks.upsertShot.mockResolvedValue(undefined)
  mocks.bindVideo.mockResolvedValue({ bound: true })
  mocks.preview.mockResolvedValue({ token: "tok", url: null })
  mocks.subscribe.mockResolvedValue(() => {})
  mocks.asset.mockResolvedValue({
    script: "Original script",
    storyboard: {
      shots: [
        {
          id: "shot-0001",
          no: 1,
          description: "Old frame",
          dialogue: [],
          duration_secs: 3,
          refs: { characters: [], props: [] },
          state: "draft",
        },
      ],
    },
  })
  useComicStore.setState({
    drafts: {},
    overviews: { 7: overview(7), 8: overview(8) },
    errors: {},
    loading: {},
    panel: { 7: { kind: "providers" }, 8: { kind: "providers" } },
    wizardOpen: {},
  })
})

describe("comic workbench next to the existing conversation", () => {
  it("keeps a project-scoped workbench on its own folder when the conversation switches projects", async () => {
    mocks.activeFolderId = 8
    mocks.tab = {
      id: "other-project",
      kind: "conversation",
      folderId: 8,
      agentType: "codex",
    }
    render(
      <NextIntlClientProvider locale="en" messages={messages}>
        <ComicWorkbenchPanel folderId={7} />
      </NextIntlClientProvider>
    )
    await screen.findByRole("region", { name: "Comic Studio" })
    expect(mocks.overview).toHaveBeenCalledWith(7)
    expect(mocks.overview).not.toHaveBeenCalledWith(8)
    expect(
      screen.getByRole("button", { name: "Adapt source into episodes" })
    ).toBeDisabled()
  })

  it("does not display a second conversation or a project main-session selector", () => {
    renderPanel()
    expect(screen.queryByText("Main session")).not.toBeInTheDocument()
    expect(
      screen.queryByText("None — a new one is created on first quick action")
    ).not.toBeInTheDocument()
  })

  it("starts a visible action in the current draft and keeps the current tab and route", () => {
    renderPanel()
    fireEvent.click(
      screen.getByRole("button", { name: "Adapt source into episodes" })
    )
    expect(
      consumeAskSelectionPrompts("current-conversation", {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual([expect.stringContaining("adapt")])
    expect(mocks.createConversation).not.toHaveBeenCalled()
    expect(mocks.openTab).not.toHaveBeenCalled()
    expect(mocks.setRoute).not.toHaveBeenCalled()
  })

  it("targets the selected episode and requests immediate draft persistence and readback", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
    })
    mocks.asset.mockResolvedValue({
      script: "Saved episode script",
      storyboard: { shots: [] },
    })
    renderPanel()
    const generate = screen.getByRole("button", {
      name: messages.Comic.storyboardGenerate,
    })
    expect(generate).toBeDisabled()
    await waitFor(() => expect(generate).toBeEnabled())
    fireEvent.click(generate)
    const prompt = consumeAskSelectionPrompts("current-conversation", {
      folderId: 7,
      agentType: "codex",
    })[0]
    expect(prompt).toContain("ep002")
    expect(prompt).toContain("comic_upsert_storyboard")
    expect(prompt).toContain("comic_get_asset")
    expect(prompt).toMatch(/immediately save the draft/i)
    expect(prompt).toMatch(/ask me before overwriting existing manual edits/i)
    expect(prompt).toMatch(
      /do not generate paid media, images, video, speech, or compose/i
    )
    expect(mocks.createConversation).not.toHaveBeenCalled()
  })

  it("queues a safe request to save conversation scripts and read them back", async () => {
    renderPanel()
    await screen.findByRole("button", {
      name: messages.Comic.qaSaveConversation,
    })
    fireEvent.click(
      screen.getByRole("button", { name: messages.Comic.qaSaveConversation })
    )
    const prompt = consumeAskSelectionPrompts("current-conversation", {
      folderId: 7,
      agentType: "codex",
    })[0]
    expect(prompt).toContain("comic_get_project")
    expect(prompt).toContain("comic_get_asset")
    expect(prompt).toContain("comic_upsert_episode")
    expect(prompt).toContain("comic_write_script")
    expect(prompt).toMatch(/ask me.*content conflicts/i)
    expect(prompt).toMatch(
      /what was actually saved and what remains a proposal/i
    )
    expect(prompt).toMatch(
      /do not generate paid media, images, video, speech, or compose/i
    )
    expect(mocks.createConversation).not.toHaveBeenCalled()
  })

  it("develops the selected original episode without asking to read a novel", async () => {
    const original = {
      ...overview(7),
      source: { mode: "original" as const, char_count: 0, chapter_count: 0 },
    }
    mocks.overview.mockResolvedValue(original)
    useComicStore.setState({
      overviews: { 7: original },
      panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
    })
    renderPanel()
    await screen.findByText("Old frame")
    fireEvent.click(
      screen.getByRole("button", { name: messages.Comic.qaOriginal })
    )
    const prompt = consumeAskSelectionPrompts("current-conversation", {
      folderId: 7,
      agentType: "codex",
    })[0]
    expect(prompt).toContain("ep002")
    expect(prompt).toContain("comic_get_asset")
    expect(prompt).toContain("comic_write_script")
    expect(prompt).not.toContain("ep001")
    expect(prompt).not.toMatch(
      /comic_read_source|novel_read|comic_get_source_outline/
    )
  })

  it("does not target the first episode from an unrelated resource panel", () => {
    renderPanel()
    expect(
      screen.queryByRole("button", { name: messages.Comic.storyboardGenerate })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole("button", { name: "Generate images for this episode" })
    ).toBeDisabled()
  })

  it("uses the tab that is current at click time and refuses another project's tab", () => {
    renderPanel()
    mocks.tab = {
      id: "new-current",
      kind: "conversation",
      folderId: 7,
      agentType: "claude_code",
    }
    fireEvent.click(
      screen.getByRole("button", { name: "Adapt source into episodes" })
    )
    expect(
      consumeAskSelectionPrompts("new-current", {
        folderId: 7,
        agentType: "claude_code",
      })
    ).toHaveLength(1)
    mocks.tab = {
      id: "other-project",
      kind: "conversation",
      folderId: 8,
      agentType: "codex",
    }
    fireEvent.click(
      screen.getByRole("button", { name: "Adapt source into episodes" })
    )
    expect(
      consumeAskSelectionPrompts("other-project", {
        folderId: 8,
        agentType: "codex",
      })
    ).toEqual([])
  })

  it("queues episode speech in the current conversation and binds a completed voice task", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
    })
    mocks.tasks.mockResolvedValue([
      {
        id: 31,
        folderId: 7,
        kind: "tts",
        status: "succeeded",
        targetKind: "shot",
        targetId: "episodes/ep002/shots/shot-0001",
        artifactPath: "episodes/ep002/shots/shot-0001/audio/task-31.wav",
        candidates: [],
        createdAt: "2026-11-03",
      },
    ])
    mocks.bindAudio.mockResolvedValue({ bound: true })
    renderPanel()
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Generate speech for this episode",
      })
    )
    const prompt = consumeAskSelectionPrompts("current-conversation", {
      folderId: 7,
      agentType: "codex",
    })[0]
    expect(prompt).toContain("ep002")
    expect(prompt).toContain("comic_request_speech")
    fireEvent.click(
      await screen.findByRole("button", { name: "Use this speech" })
    )
    expect(mocks.bindAudio).toHaveBeenCalledWith(7, "ep002", "shot-0001", 31)
    expect(mocks.createConversation).not.toHaveBeenCalled()
  })

  it("shows the finished episode export and queues composition in the current conversation", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
    })
    mocks.tasks.mockResolvedValue([
      {
        id: 23,
        folderId: 7,
        kind: "compose",
        status: "succeeded",
        targetKind: "episode",
        targetId: "episodes/ep002",
        artifactPath: "episodes/ep002/export/task-23.mp4",
        candidates: [],
        createdAt: "2026-11-03",
      },
    ])
    renderPanel()
    await screen.findByRole("button", { name: "Open exported video" })
    fireEvent.click(
      screen.getByRole("button", { name: "Compose this episode" })
    )
    expect(
      consumeAskSelectionPrompts("current-conversation", {
        folderId: 7,
        agentType: "codex",
      })[0]
    ).toContain("ep002")
  })

  it("queues videos only for the selected storyboard episode without binding", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
    })
    renderPanel()
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Generate videos for this episode",
      })
    )
    const prompt = consumeAskSelectionPrompts("current-conversation", {
      folderId: 7,
      agentType: "codex",
    })[0]
    expect(prompt).toContain("ep002")
    expect(prompt).toContain("comic_request_video")
    expect(prompt).toContain("comic_check_generation")
    expect(prompt).toMatch(/do not call comic_set_shot_video/i)
    expect(mocks.createConversation).not.toHaveBeenCalled()
  })

  it("disables video generation without an explicitly selected episode", () => {
    renderPanel()
    expect(
      screen.getByRole("button", { name: "Generate videos for this episode" })
    ).toBeDisabled()
    expect(
      consumeAskSelectionPrompts("current-conversation", {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual([])
  })

  it("binds a confirmed video and refreshes after a conflict refusal", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
    })
    mocks.tasks.mockResolvedValue([
      {
        id: 42,
        folderId: 7,
        kind: "video",
        status: "succeeded",
        targetKind: "shot",
        targetId: "episodes/ep002/shots/shot-0001",
        artifactPath: "episodes/ep002/shots/shot-0001/video/task-42.mp4",
        candidates: [],
        createdAt: "2026-11-04",
      },
    ])
    mocks.bindVideo.mockRejectedValueOnce({
      code: "conflict",
      message: "the clip changed elsewhere",
    })
    renderPanel()
    fireEvent.click(await screen.findByRole("button", { name: "Review video" }))
    expect(
      (await screen.findByRole("dialog")).querySelector("video")
    ).toHaveAttribute(
      "src",
      "http://workbench.local/api/comic_video_preview/tok"
    )
    const assetCalls = mocks.asset.mock.calls.length
    fireEvent.click(screen.getByRole("button", { name: "Use this clip" }))
    await waitFor(() =>
      expect(mocks.bindVideo).toHaveBeenCalledWith(
        7,
        "ep002",
        "shot-0001",
        42,
        null
      )
    )
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    )
    await waitFor(() =>
      expect(mocks.asset.mock.calls.length).toBeGreaterThan(assetCalls)
    )
  })

  it("replaces a bound clip only with the binding observed when review opened", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
    })
    mocks.asset.mockResolvedValue({
      script: "Script",
      storyboard: {
        shots: [
          {
            id: "shot-0001",
            no: 1,
            description: "Frame",
            dialogue: [],
            duration_secs: 3,
            refs: { characters: [], props: [] },
            state: "done",
            selected: null,
            audio: null,
            video: {
              path: "episodes/ep002/shots/shot-0001/video/task-42.mp4",
              duration_secs: 3.2,
              task_id: 42,
            },
          },
        ],
      },
    })
    mocks.tasks.mockResolvedValue([
      {
        id: 43,
        folderId: 7,
        kind: "video",
        status: "succeeded",
        targetKind: "shot",
        targetId: "episodes/ep002/shots/shot-0001",
        artifactPath: "episodes/ep002/shots/shot-0001/video/task-43.mp4",
        candidates: [],
        createdAt: "2026-11-04",
      },
    ])
    mocks.isDesktop = true
    mocks.preview.mockResolvedValue({
      token: "tok2",
      url: "http://codeg-media.localhost/tok2",
    })
    renderPanel()
    fireEvent.click(await screen.findByRole("button", { name: "Review video" }))
    await screen.findByRole("dialog")
    expect(
      screen.getByText("Replace video clip for shot-0001")
    ).toBeInTheDocument()
    expect(
      (screen.getByRole("dialog").querySelector("video") as HTMLVideoElement)
        .src
    ).toBe("http://codeg-media.localhost/tok2")
    fireEvent.click(
      screen.getByRole("button", { name: "Replace with this clip" })
    )
    await waitFor(() =>
      expect(mocks.bindVideo).toHaveBeenCalledWith(
        7,
        "ep002",
        "shot-0001",
        43,
        42
      )
    )
  })

  it.each(["success", "conflict"])(
    "allows another video review after a %s closes the previous confirmation",
    async (outcome) => {
      useComicStore.setState({
        panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
      })
      mocks.tasks.mockResolvedValue([
        {
          id: 42,
          folderId: 7,
          kind: "video",
          status: "succeeded",
          targetKind: "shot",
          targetId: "episodes/ep002/shots/shot-0001",
          artifactPath: "episodes/ep002/shots/shot-0001/video/task-42.mp4",
          candidates: [],
          createdAt: "2026-11-04",
        },
      ])
      if (outcome === "conflict")
        mocks.bindVideo.mockRejectedValueOnce({ code: "conflict" })
      renderPanel()
      fireEvent.click(
        await screen.findByRole("button", { name: "Review video" })
      )
      fireEvent.click(
        await screen.findByRole("button", { name: "Use this clip" })
      )
      await waitFor(() =>
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
      )
      fireEvent.click(
        await screen.findByRole("button", { name: "Review video" })
      )
      const confirm = await screen.findByRole("button", {
        name: "Use this clip",
      })
      expect(confirm).toBeEnabled()
      fireEvent.click(confirm)
      await waitFor(() => expect(mocks.bindVideo).toHaveBeenCalledTimes(2))
    }
  )

  it("keeps the current clip when review is cancelled", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep002" } },
    })
    mocks.tasks.mockResolvedValue([
      {
        id: 42,
        folderId: 7,
        kind: "video",
        status: "succeeded",
        targetKind: "shot",
        targetId: "episodes/ep002/shots/shot-0001",
        artifactPath: "episodes/ep002/shots/shot-0001/video/task-42.mp4",
        candidates: [],
        createdAt: "2026-11-04",
      },
    ])
    renderPanel()
    fireEvent.click(await screen.findByRole("button", { name: "Review video" }))
    await screen.findByRole("dialog")
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    )
    expect(mocks.bindVideo).not.toHaveBeenCalled()
  })

  it("unbinds a bound clip only through the confirmed dialog action", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep001" } },
    })
    mocks.asset.mockResolvedValue({
      script: "Script",
      storyboard: {
        shots: [
          {
            id: "shot-0001",
            no: 1,
            description: "Old frame",
            dialogue: [],
            duration_secs: 3,
            refs: { characters: [], props: [] },
            state: "done",
            selected: null,
            audio: null,
            video: {
              path: "episodes/ep001/shots/shot-0001/video/task-42.mp4",
              duration_secs: 3.2,
              task_id: 42,
            },
          },
        ],
      },
    })
    mocks.clearShotVideo.mockResolvedValue({ unbound: true })
    renderPanel()
    await screen.findByText("Old frame")
    fireEvent.click(
      document.querySelector(".lucide-pencil")!.closest("button")!
    )
    expect(screen.getByText("Bound clip: task 42")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Unbind clip" }))
    fireEvent.click(await screen.findByRole("button", { name: "Unbind clip" }))
    await waitFor(() =>
      expect(mocks.clearShotVideo).toHaveBeenCalledWith(
        7,
        "ep001",
        "shot-0001",
        42
      )
    )
  })

  it("preserves a bound video when editing another shot field", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep001" } },
    })
    mocks.asset.mockResolvedValue({
      script: "Original script",
      storyboard: {
        shots: [
          {
            id: "shot-0001",
            no: 1,
            description: "Old frame",
            dialogue: [],
            duration_secs: 3,
            camera: null,
            shot_size: null,
            transition: null,
            refs: { characters: [], scene: null, props: [] },
            state: "done",
            selected: "episodes/ep001/shots/shot-0001/first-frame.png",
            audio: null,
            video: {
              path: "episodes/ep001/shots/shot-0001/video/task-42.mp4",
              duration_secs: 3.25,
              task_id: 42,
            },
          },
        ],
      },
    })

    renderPanel()
    await screen.findByText("Old frame")
    const editButton = document
      .querySelector(".lucide-pencil")
      ?.closest("button")
    expect(editButton).not.toBeNull()
    fireEvent.click(editButton!)
    fireEvent.change(screen.getAllByRole("textbox")[0], {
      target: { value: "Updated frame" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))

    await waitFor(() =>
      expect(mocks.upsertShot).toHaveBeenCalledWith(
        7,
        "ep001",
        expect.objectContaining({
          description: "Updated frame",
          video: {
            path: "episodes/ep001/shots/shot-0001/video/task-42.mp4",
            duration_secs: 3.25,
            task_id: 42,
          },
        })
      )
    )
  })

  it("updates finished shots without replacing a script draft", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep001" } },
    })
    const receive: ((payload: unknown) => void)[] = []
    mocks.subscribe.mockImplementation(async (_event, handler) => {
      receive.push(handler)
      return () => {}
    })
    renderPanel()
    await screen.findByText("Old frame")
    fireEvent.click(screen.getByRole("button", { name: "Script" }))
    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "My unsaved script" },
    })
    mocks.asset.mockResolvedValue({
      script: "Server script",
      storyboard: {
        shots: [
          {
            id: "shot-0001",
            no: 1,
            description: "New frame",
            dialogue: [],
            duration_secs: 3,
            refs: { characters: [], props: [] },
            state: "candidate",
          },
        ],
      },
    })
    act(() =>
      receive.forEach((handler) =>
        handler({ kind: "task", folder_id: 7, task_id: 5 })
      )
    )
    await screen.findByText("New frame")
    expect(screen.getByRole("textbox")).toHaveValue("My unsaved script")
  })
})
