import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ComicWorkbenchPanel } from "./comic-workbench-panel"
import { useComicStore } from "@/stores/comic-store"
import {
  consumeAskSelectionPrompts,
  resetAskSelectionPromptsForTests,
} from "@/lib/ask-selection-handoff"
import type { ComicOverviewPayload, EpisodeMetaInput } from "@/lib/comic"

const mocks = vi.hoisted(() => ({
  getOverview: vi.fn(),
  initProject: vi.fn(),
  ingestSource: vi.fn(),
  getAsset: vi.fn(),
  listChapters: vi.fn(),
  readSource: vi.fn(),
  listTasks: vi.fn(),
  upsertEpisode: vi.fn(),
  writeScript: vi.fn(),
  replaceStoryboard: vi.fn(),
  error: vi.fn(),
  tab: {
    id: "current",
    kind: "conversation",
    folderId: 7,
    agentType: "codex",
  } as Record<string, unknown> | null,
}))
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, params?: Record<string, unknown>) =>
    key + (params ? ` ${JSON.stringify(params)}` : ""),
}))
vi.mock("sonner", () => ({ toast: { error: mocks.error, success: vi.fn() } }))
vi.mock("@/contexts/active-folder-context", () => ({
  useActiveFolder: () => ({ activeFolderId: 7 }),
}))
vi.mock("@/lib/comic", () => ({ comicApi: mocks }))
vi.mock("@/lib/transport", () => ({
  getTransport: () => ({ subscribe: async () => () => {} }),
}))
vi.mock("@/stores/tab-store", () => {
  const getState = () => ({
    tabs: mocks.tab ? [mocks.tab] : [],
    activeTabId: mocks.tab?.id,
  })
  return {
    useTabStore: Object.assign(
      (selector: (s: ReturnType<typeof getState>) => unknown) =>
        selector(getState()),
      { getState }
    ),
  }
})

let project: ComicOverviewPayload
let docs: Record<
  string,
  { meta: EpisodeMetaInput; script: string; storyboard: { shots: unknown[] } }
>
function openPanel() {
  return render(<ComicWorkbenchPanel folderId={7} />)
}
function enterNewEpisode(title: string, script: string) {
  fireEvent.click(screen.getByRole("button", { name: "episodeNew" }))
  const dialog = screen.getByRole("dialog")
  fireEvent.change(within(dialog).getByLabelText("episodeTitle"), {
    target: { value: title },
  })
  fireEvent.change(within(dialog).getByLabelText("script"), {
    target: { value: script },
  })
  fireEvent.click(within(dialog).getByRole("button", { name: "create" }))
}

beforeEach(() => {
  vi.clearAllMocks()
  resetAskSelectionPromptsForTests()
  mocks.tab = {
    id: "current",
    kind: "conversation",
    folderId: 7,
    agentType: "codex",
  }
  project = {
    folderId: 7,
    has_source: true,
    source: { mode: "original", char_count: 0, chapter_count: 0 },
    episodes: [],
    characters: [],
    scenes: [],
    props: [],
    providerPool: [],
  }
  docs = {}
  mocks.getOverview.mockImplementation(async () => ({
    ...project,
    episodes: [...project.episodes],
  }))
  mocks.listTasks.mockResolvedValue([])
  mocks.listChapters.mockResolvedValue([])
  mocks.readSource.mockResolvedValue({ text: "Unexpected novel" })
  mocks.getAsset.mockImplementation(async (_folder, _kind, id) => ({
    ...docs[id]?.meta,
    script: docs[id]?.script,
    storyboard: docs[id]?.storyboard,
  }))
  mocks.upsertEpisode.mockImplementation(
    async (_folder, meta: EpisodeMetaInput) => {
      docs[meta.id] = {
        ...docs[meta.id],
        meta,
        script: docs[meta.id]?.script ?? "",
        storyboard: docs[meta.id]?.storyboard ?? { shots: [] },
      }
      project.episodes = [
        ...project.episodes.filter((ep) => ep.id !== meta.id),
        {
          ...meta,
          status: meta.status ?? "draft",
          shots_total: 0,
          shots_done: 0,
        },
      ]
      return meta
    }
  )
  mocks.writeScript.mockImplementation(async (_folder, id, script) => {
    docs[id].script = script
  })
  mocks.replaceStoryboard.mockImplementation(async (_folder, id, shots) => {
    docs[id].storyboard = {
      shots: shots.map((shot: Record<string, unknown>, index: number) => ({
        no: index + 1,
        dialogue: [],
        refs: { characters: [], props: [] },
        duration_secs: 3,
        state: "draft",
        ...shot,
      })),
    }
    return docs[id].storyboard
  })
  mocks.initProject.mockResolvedValue(project)
  mocks.ingestSource.mockResolvedValue({ mode: "original" })
  useComicStore.setState({
    overviews: { 7: project },
    errors: {},
    loading: {},
    panel: {},
    wizardOpen: {},
    drafts: {},
  })
})

describe("comic original series workflow", () => {
  it.each(["original", null, "unknown"])(
    "does not read nonexistent novels for source mode %s",
    async (mode) => {
      project.source = { ...project.source!, mode: mode as "original" }
      openPanel()
      await screen.findByText(
        mode === "original" ? "originalSourceHint" : "sourceUnavailable"
      )
      expect(mocks.listChapters).not.toHaveBeenCalled()
      expect(mocks.readSource).not.toHaveBeenCalled()
      expect(screen.getByRole("button", { name: "episodeFirst" })).toBeEnabled()
    }
  )

  it("creates a first episode with pasted script and opens its editable storyboard", async () => {
    openPanel()
    enterNewEpisode("Pilot", "Already written script")
    await screen.findByDisplayValue("Already written script")
    expect(mocks.writeScript).toHaveBeenCalledWith(
      7,
      "ep001",
      "Already written script",
      { createOnly: true }
    )
    expect(useComicStore.getState().panel[7]).toEqual({
      kind: "storyboard",
      episodeId: "ep001",
    })
    expect(screen.getByRole("button", { name: "addShot" })).toBeEnabled()
    expect(
      screen.getByRole("button", { name: "storyboardGenerate" })
    ).toBeEnabled()
    expect(
      screen.queryByRole("button", { name: "storyboardImport" })
    ).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "storyboardMore" })).toBeEnabled()
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual([])
  })

  it("rechecks the next episode ID against fresh metadata before creating", async () => {
    openPanel()
    project.episodes = [
      {
        id: "ep001",
        title: "Existing elsewhere",
        status: "draft",
        shots_total: 0,
        shots_done: 0,
      },
    ]
    enterNewEpisode("New", "Script")
    await screen.findByDisplayValue("Script")
    expect(mocks.upsertEpisode).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ id: "ep002", title: "New" }),
      { createOnly: true }
    )
  })

  it("keeps a failed script draft and retries the same episode", async () => {
    mocks.writeScript.mockRejectedValueOnce({
      code: "io",
      message: "Script is read-only",
    })
    openPanel()
    enterNewEpisode("Pilot", "Keep this script")
    await screen.findByRole("alert")
    expect(screen.getByLabelText("script")).toHaveValue("Keep this script")
    expect(project.episodes).toHaveLength(1)
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "create" })
    )
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    )
    expect(project.episodes).toHaveLength(1)
    expect(docs.ep001.script).toBe("Keep this script")
  })

  it("does not claim an episode exists when creating its metadata fails", async () => {
    mocks.upsertEpisode.mockRejectedValueOnce({
      code: "io",
      message: "Cannot create episode",
    })
    openPanel()
    enterNewEpisode("Pilot", "Draft")
    await screen.findByRole("alert")
    expect(mocks.error).toHaveBeenLastCalledWith("Cannot create episode")
    expect(mocks.writeScript).not.toHaveBeenCalled()
    expect(project.episodes).toHaveLength(0)
  })

  it("creates a second stable ID and isolates script drafts when switching episodes", async () => {
    openPanel()
    enterNewEpisode("Pilot", "First script")
    await screen.findByDisplayValue("First script")
    fireEvent.change(screen.getByLabelText("script"), {
      target: { value: "Unsaved first edit" },
    })
    enterNewEpisode("Second", "Second script")
    await screen.findByDisplayValue("Second script")
    expect(mocks.writeScript).toHaveBeenLastCalledWith(
      7,
      "ep002",
      "Second script",
      { createOnly: true }
    )
    fireEvent.click(screen.getByRole("button", { name: /ep001.*Pilot/ }))
    await screen.findByDisplayValue("Unsaved first edit")
    fireEvent.click(screen.getByRole("button", { name: "save" }))
    await waitFor(() => expect(docs.ep001.script).toBe("Unsaved first edit"))
    expect(docs.ep002.script).toBe("Second script")
  })

  it("renames the episode without discarding full metadata", async () => {
    openPanel()
    enterNewEpisode("Pilot", "Script")
    await screen.findByDisplayValue("Script")
    docs.ep001.meta = {
      id: "ep001",
      title: "Pilot",
      synopsis: "Keep synopsis",
      status: "done",
      source_chapters: [2, 3],
    }
    fireEvent.click(screen.getByRole("button", { name: "episodeEdit" }))
    const dialog = await screen.findByRole("dialog")
    await waitFor(() =>
      expect(within(dialog).getByLabelText("episodeTitle")).toHaveValue("Pilot")
    )
    fireEvent.change(within(dialog).getByLabelText("episodeTitle"), {
      target: { value: "New title" },
    })
    fireEvent.click(within(dialog).getByRole("button", { name: "save" }))
    await waitFor(() =>
      expect(mocks.upsertEpisode).toHaveBeenLastCalledWith(
        7,
        { ...docs.ep001.meta, title: "New title" },
        { expectedMeta: { ...docs.ep001.meta, title: "Pilot" } }
      )
    )
    expect(docs.ep001.meta).toEqual({
      id: "ep001",
      title: "New title",
      synopsis: "Keep synopsis",
      status: "done",
      source_chapters: [2, 3],
    })
  })

  it("does not call replace on invalid JSON and confirms before replacing an existing board", async () => {
    openPanel()
    enterNewEpisode("Pilot", "Script")
    await screen.findByDisplayValue("Script")
    docs.ep001.storyboard = {
      shots: [
        {
          id: "old",
          no: 1,
          description: "Existing",
          dialogue: [],
          refs: { characters: [], props: [] },
          duration_secs: 3,
          state: "draft",
        },
      ],
    }
    fireEvent.keyDown(screen.getByRole("button", { name: "storyboardMore" }), {
      key: "Enter",
    })
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "storyboardImport" })
    )
    const input = screen.getByLabelText("storyboardJson")
    fireEvent.change(input, {
      target: { value: '[{"id":"../bad","description":"bad"}]' },
    })
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "storyboardImport",
      })
    )
    await screen.findByRole("alert")
    expect(mocks.replaceStoryboard).not.toHaveBeenCalled()
    fireEvent.change(input, {
      target: {
        value: '[{"id":"new","description":"Frame","duration_secs":0.01}]',
      },
    })
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "storyboardImport",
      })
    )
    await screen.findByRole("alert")
    expect(mocks.replaceStoryboard).not.toHaveBeenCalled()
    fireEvent.change(input, {
      target: { value: '[{"id":"new","description":"New frame"}]' },
    })
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "storyboardImport",
      })
    )
    await screen.findByRole("alertdialog")
    expect(mocks.replaceStoryboard).not.toHaveBeenCalled()
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "cancel",
      })
    )
    expect(mocks.replaceStoryboard).not.toHaveBeenCalled()
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "storyboardImport",
      })
    )
    await screen.findByRole("alertdialog")
    fireEvent.click(screen.getByRole("button", { name: "storyboardReplace" }))
    await waitFor(() =>
      expect(mocks.replaceStoryboard).toHaveBeenCalledTimes(1)
    )
    expect(mocks.replaceStoryboard).toHaveBeenCalledWith(
      7,
      "ep001",
      [{ id: "new", description: "New frame", duration_secs: 3 }],
      {
        shots: [
          expect.objectContaining({ id: "old", description: "Existing" }),
        ],
      }
    )
    await screen.findByText("New frame")
  })

  it("uses the selected original episode and blocks storyboarding unsaved script changes", async () => {
    openPanel()
    enterNewEpisode("Pilot", "First")
    await screen.findByDisplayValue("First")
    enterNewEpisode("Second", "Second")
    await screen.findByDisplayValue("Second")
    fireEvent.click(screen.getByRole("button", { name: "qaOriginal" }))
    const prompt = consumeAskSelectionPrompts("current", {
      folderId: 7,
      agentType: "codex",
    })[0]
    expect(prompt).toContain("qaOriginalEpisodePrompt")
    expect(prompt).toContain("ep002")
    expect(prompt).not.toContain("ep001")
    fireEvent.change(screen.getByLabelText("script"), {
      target: { value: "Not saved" },
    })
    expect(
      screen.getByRole("button", { name: "storyboardGenerate" })
    ).toBeDisabled()
    expect(screen.getByRole("button", { name: "qaOriginal" })).toBeDisabled()
    expect(screen.getByText("scriptSaveBeforeAI")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "qaOriginal" }))
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual([])
    fireEvent.click(screen.getByRole("button", { name: "save" }))
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "qaOriginal" })).toBeEnabled()
    )
    expect(docs.ep002.script).toBe("Not saved")
    expect(screen.queryByText("scriptSaveBeforeAI")).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "qaOriginal" }))
    expect(
      consumeAskSelectionPrompts("current", {
        folderId: 7,
        agentType: "codex",
      })[0]
    ).toContain("ep002")
  })

  it("generates a saved script only on explicit request in the current conversation", async () => {
    openPanel()
    enterNewEpisode("Pilot", "Saved script")
    await screen.findByDisplayValue("Saved script")
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual([])
    fireEvent.click(screen.getByRole("button", { name: "storyboardGenerate" }))
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual(['qaStoryboardPrompt {"episode":"ep001"}'])
    expect(mocks.replaceStoryboard).not.toHaveBeenCalled()
  })

  it("explains the missing script and enables generation only after saving", async () => {
    openPanel()
    enterNewEpisode("Pilot", "")
    await screen.findByRole("button", { name: "storyboardGenerate" })
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    )
    expect(
      screen.getByRole("button", { name: "storyboardGenerate" })
    ).toBeDisabled()
    expect(screen.getByText("storyboardNeedsScript")).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("script"), {
      target: { value: "New script" },
    })
    expect(
      screen.getByRole("button", { name: "storyboardGenerate" })
    ).toBeDisabled()
    fireEvent.click(screen.getByRole("button", { name: "save" }))
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "storyboardGenerate" })
      ).toBeEnabled()
    )
  })

  it("asks before requesting regeneration of existing shots", async () => {
    openPanel()
    enterNewEpisode("Pilot", "Script")
    await screen.findByDisplayValue("Script")
    docs.ep001.storyboard = {
      shots: [
        {
          id: "old",
          description: "Existing",
          dialogue: [],
          refs: { characters: [], props: [] },
          duration_secs: 3,
          state: "draft",
        },
      ],
    }
    fireEvent.change(screen.getByLabelText("resources"), {
      target: { value: "providers" },
    })
    fireEvent.click(screen.getByRole("button", { name: /ep001.*Pilot/ }))
    await screen.findByText("Existing")
    fireEvent.click(screen.getByRole("button", { name: "storyboardGenerate" }))
    const confirmation = await screen.findByRole("alertdialog")
    expect(
      within(confirmation).getByText(/storyboardRegenerateConfirm/)
    ).toBeInTheDocument()
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual([])
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "cancel" })
    )
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual([])
    fireEvent.click(screen.getByRole("button", { name: "storyboardGenerate" }))
    fireEvent.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "storyboardGenerate",
      })
    )
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toHaveLength(1)
  })

  it("saves existing conversation work only on request and blocks hidden script drafts", async () => {
    openPanel()
    await screen.findByText("originalSourceHint")
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual([])
    fireEvent.click(screen.getByRole("button", { name: "qaSaveConversation" }))
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual(["qaSaveConversationPrompt"])
    act(() =>
      useComicStore.getState().setResourceDraft(7, "episodes/ep009/script", {
        kind: "script",
        text: "Hidden edits",
      })
    )
    expect(
      screen.getByRole("button", { name: "qaSaveConversation" })
    ).toBeDisabled()
    expect(screen.getByText("scriptSaveBeforeAI")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "qaSaveConversation" }))
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual([])
  })

  it.each(["pi", "cline", "open_claw"])(
    "keeps manual edits available but blocks MCP actions for %s",
    async (agentType) => {
      mocks.tab = { ...mocks.tab, agentType }
      openPanel()
      enterNewEpisode("Pilot", "Script")
      await screen.findByDisplayValue("Script")
      expect(
        screen.getByRole("button", { name: "storyboardGenerate" })
      ).toBeDisabled()
      expect(
        screen.getByRole("button", { name: "qaSaveConversation" })
      ).toBeDisabled()
      expect(screen.getByText("comicManualOnlyHint")).toBeInTheDocument()
      expect(screen.getByRole("button", { name: "addShot" })).toBeEnabled()
      expect(
        consumeAskSelectionPrompts("current", {
          folderId: 7,
          agentType: agentType as "codex",
        })
      ).toEqual([])
    }
  )

  it("rechecks project and agent at click time for the local generation action", async () => {
    openPanel()
    enterNewEpisode("Pilot", "Script")
    await screen.findByDisplayValue("Script")
    mocks.tab = { ...mocks.tab, folderId: 8 }
    fireEvent.click(screen.getByRole("button", { name: "storyboardGenerate" }))
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "codex" })
    ).toEqual([])
    mocks.tab = { ...mocks.tab, folderId: 7, agentType: "cline" }
    fireEvent.click(screen.getByRole("button", { name: "storyboardGenerate" }))
    expect(
      consumeAskSelectionPrompts("current", { folderId: 7, agentType: "cline" })
    ).toEqual([])
  })

  it("explains that AI actions need a conversation in this project", () => {
    mocks.tab = null
    openPanel()
    expect(screen.getByText("noCurrentConversation")).toBeInTheDocument()
  })

  it("keeps setup busy when the init event changes an empty folder into a project", async () => {
    useComicStore.setState({
      overviews: {},
      errors: { 7: "Not a project" },
      wizardOpen: { 7: true },
    })
    mocks.getOverview.mockRejectedValueOnce({
      code: "not_found",
      message: "Not a project",
    })
    let finish!: () => void
    mocks.ingestSource.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve
        })
    )
    openPanel()
    await screen.findByLabelText("seriesTitle")
    fireEvent.change(screen.getByLabelText("seriesTitle"), {
      target: { value: "Original" },
    })
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "create" })
    )
    await waitFor(() => expect(mocks.ingestSource).toHaveBeenCalled())
    act(() => useComicStore.setState({ overviews: { 7: project } }))
    expect(
      within(screen.getByRole("dialog")).getByRole("button", { name: "create" })
    ).toBeDisabled()
    await act(async () => finish())
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    )
  })

  it("defaults setup to original and shows an init error without losing inputs", async () => {
    useComicStore.setState({ wizardOpen: { 7: true } })
    mocks.initProject.mockRejectedValueOnce({
      code: "io",
      message: "Cannot create story directory",
    })
    openPanel()
    expect(screen.getByRole("combobox", { name: "" })).toHaveTextContent(
      "sourceOriginal"
    )
    fireEvent.change(screen.getByLabelText("seriesTitle"), {
      target: { value: "My original" },
    })
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "create" })
    )
    await screen.findByRole("alert")
    expect(mocks.error).toHaveBeenLastCalledWith(
      "Cannot create story directory"
    )
    expect(screen.getByLabelText("seriesTitle")).toHaveValue("My original")
    expect(mocks.ingestSource).not.toHaveBeenCalled()
  })

  it("keeps the setup draft and reports a structured partial error; retry skips project init", async () => {
    useComicStore.setState({
      wizardOpen: { 7: true },
      drafts: {
        7: {
          setup: {
            kind: "setup",
            title: "Original series",
            sourceMode: "original",
            novelPath: "",
            fileBase64: null,
            fileName: null,
          },
        },
      },
    })
    mocks.ingestSource
      .mockRejectedValueOnce({
        code: "io",
        message: "Source failed",
        detail: "Permission denied writing source.json",
      })
      .mockResolvedValueOnce({ mode: "original" })
    openPanel()
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "create" })
    )
    await waitFor(() =>
      expect(mocks.error).toHaveBeenCalledWith(
        expect.stringContaining("Permission denied writing source.json")
      )
    )
    expect(screen.getByLabelText("seriesTitle")).toHaveValue("Original series")
    expect(useComicStore.getState().drafts[7].setup).toBeDefined()
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "create" })
    )
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    )
    expect(mocks.initProject).toHaveBeenCalledTimes(1)
    expect(mocks.ingestSource).toHaveBeenCalledTimes(2)
  })
})
