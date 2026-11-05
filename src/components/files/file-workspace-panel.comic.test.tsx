import { act, fireEvent, render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { FileWorkspacePanel } from "./file-workspace-panel"
import { MonacoModelProvider } from "@/contexts/monaco-model-context"
import type {
  ComicWorkspaceTab,
  FileWorkspaceTab,
} from "@/contexts/workspace-context"
import { useComicStore } from "@/stores/comic-store"
import { resetTabStore, useTabStore } from "@/stores/tab-store"
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
  activeFileTab: {
    id: "comic:7",
    kind: "comic",
    folderId: 7,
    title: "Comic Studio · Project seven",
    description: "/comic-seven",
    path: null,
    language: "comic",
    content: "",
    loading: false,
    readonly: true,
  } as FileWorkspaceTab,
}))

vi.mock("@/contexts/workspace-context", () => ({
  useWorkspaceFileTabs: () => ({
    fileTabs: [mocks.activeFileTab],
    allFileTabs: [mocks.activeFileTab],
    activeFileTab: mocks.activeFileTab,
    activeFileTabId: mocks.activeFileTab.id,
    previewFileTabIds: new Set<string>(),
    pendingFileReveal: null,
  }),
  useWorkspaceActions: () => ({}),
}))
vi.mock("@/contexts/active-folder-context", () => ({
  useActiveFolder: () => ({
    activeFolderId: 8,
    activeFolder: { id: 8, path: "/comic-eight" },
  }),
}))
vi.mock("@/hooks/use-appearance", () => ({
  useZoomLevel: () => ({ zoomLevel: 100 }),
  useEditorFont: () => ({
    editorFontStack: "monospace",
    editorFontSize: 13,
    editorLigatures: false,
    editorWordWrap: false,
    setEditorWordWrap: vi.fn(),
  }),
}))
vi.mock("@/lib/monaco-local", () => ({}))
vi.mock("@/lib/monaco-themes", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useMonacoWorkspaceTheme: () => "vs-dark",
}))
vi.mock("@/lib/comic", () => ({
  comicApi: {
    getOverview: mocks.overview,
    getAsset: mocks.asset,
    listTasks: mocks.tasks,
  },
}))
vi.mock("@/lib/transport", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  getTransport: () => ({ subscribe: async () => () => {} }),
}))

const overview: ComicOverviewPayload = {
  folderId: 7,
  has_source: true,
  series: {
    title: "Project seven series",
    bible_refs: [],
    default_candidate_count: 4,
    max_images_per_request: 12,
    max_concurrent_tasks: 2,
    providers: {},
    candidate_retention: { keep_count: 8, max_age_days: 14 },
  },
  episodes: [],
  characters: [],
  scenes: [],
  props: [],
  providerPool: [],
}

beforeEach(() => {
  vi.clearAllMocks()
  resetTabStore()
  resetAskSelectionPromptsForTests()
  mocks.activeFileTab = comicTab(7)
  mocks.overview.mockResolvedValue(overview)
  mocks.tasks.mockResolvedValue([])
  mocks.asset.mockResolvedValue({
    script: "Original script",
    storyboard: { shots: [] },
  })
  useComicStore.setState({
    drafts: {},
    overviews: { 7: overview },
    errors: {},
    loading: {},
    panel: { 7: { kind: "providers" } },
    wizardOpen: {},
  })
})

function comicTab(folderId: number): ComicWorkspaceTab {
  return {
    id: `comic:${folderId}`,
    kind: "comic",
    folderId,
    title: `Comic Studio · Project ${folderId}`,
    description: null,
    path: null,
    language: "comic",
    content: "",
    loading: false,
    readonly: true,
  }
}

function content() {
  return (
    <NextIntlClientProvider locale="en" messages={messages}>
      <MonacoModelProvider fileTabs={[mocks.activeFileTab]}>
        <FileWorkspacePanel />
      </MonacoModelProvider>
    </NextIntlClientProvider>
  )
}

function renderPanel() {
  return render(content())
}

describe("comic in the wide file workspace", () => {
  it("renders the captured project's resource workbench instead of a file editor", async () => {
    renderPanel()
    await screen.findByRole("region", { name: "Comic Studio" })
    expect(screen.getByText("Project seven series")).toBeInTheDocument()
    expect(
      screen.getByRole("combobox", { name: "Resources" })
    ).toBeInTheDocument()
    expect(mocks.overview).toHaveBeenCalledWith(7)
    expect(mocks.overview).not.toHaveBeenCalledWith(8)
  })

  it("preserves a script draft after leaving the workbench for another file tab", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "storyboard", episodeId: "ep001" } },
    })
    const view = renderPanel()
    const script = await screen.findByRole("button", { name: "Script" })
    fireEvent.click(script)
    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "Unsubmitted script" },
    })
    mocks.activeFileTab = {
      id: "diff:1",
      kind: "diff",
      folderId: 7,
      title: "Diff",
      description: null,
      path: null,
      language: "diff",
      content: "",
      loading: false,
      readonly: true,
    }
    view.rerender(content())
    expect(screen.queryByRole("region", { name: "Comic Studio" })).toBeNull()
    mocks.activeFileTab = comicTab(7)
    view.rerender(content())
    fireEvent.click(await screen.findByRole("button", { name: "Script" }))
    expect(screen.getByRole("textbox")).toHaveValue("Unsubmitted script")
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled()
  })

  it("restores unsaved asset fields after a project-scope and mobile-pane unmount", async () => {
    useComicStore.setState({
      panel: { 7: { kind: "asset", assetKind: "prop", slug: "sword" } },
    })
    mocks.asset.mockResolvedValue({
      slug: "sword",
      name: "Sword",
      description: "Old sword",
      prompt_fragment: "metal",
      refs: [],
    })
    let view = renderPanel()
    await screen.findByDisplayValue("Sword")
    fireEvent.change(screen.getByDisplayValue("Sword"), {
      target: { value: "Unsaved sword" },
    })
    fireEvent.change(screen.getByDisplayValue("Old sword"), {
      target: { value: "Handmade prop" },
    })
    fireEvent.change(screen.getByDisplayValue("metal"), {
      target: { value: "silver blade" },
    })
    mocks.activeFileTab = comicTab(8)
    act(() => {
      useComicStore.setState({
        overviews: { 7: overview, 8: { ...overview, folderId: 8 } },
        panel: {
          7: { kind: "asset", assetKind: "prop", slug: "sword" },
          8: { kind: "providers" },
        },
      })
    })
    view.rerender(content())
    expect(screen.queryByDisplayValue("Unsaved sword")).toBeNull()
    view.unmount()
    mocks.activeFileTab = comicTab(7)
    view = renderPanel()
    await screen.findByDisplayValue("Unsaved sword")
    expect(screen.getByDisplayValue("Handmade prop")).toBeInTheDocument()
    expect(screen.getByDisplayValue("silver blade")).toBeInTheDocument()
    view.unmount()
  })

  it("restores the setup draft and selected novel when the file pane remounts", async () => {
    useComicStore.setState({ wizardOpen: { 7: true } })
    let view = renderPanel()
    const title = await screen.findByRole("textbox", { name: "Series title" })
    fireEvent.change(title, { target: { value: "Draft series" } })
    const upload =
      view.container.ownerDocument.querySelector<HTMLInputElement>(
        "input[type=file]"
      )!
    fireEvent.change(upload, {
      target: {
        files: [
          new File(["chapter one"], "original.txt", { type: "text/plain" }),
        ],
      },
    })
    await screen.findByText("original.txt")
    await screen.findByRole("button", { name: "Create" })
    view.unmount()
    view = renderPanel()
    expect(
      await screen.findByRole("textbox", { name: "Series title" })
    ).toHaveValue("Draft series")
    expect(screen.getByText("original.txt")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Create" })).toBeEnabled()
    view.unmount()
  })

  it("restores an edited shot and uses references from its captured project", async () => {
    const shot = {
      id: "shot-0001",
      no: 1,
      description: "Old frame",
      dialogue: [],
      duration_secs: 3,
      refs: { characters: [], props: [] },
      state: "draft",
    }
    mocks.asset.mockResolvedValue({
      script: "Original script",
      storyboard: { shots: [shot] },
    })
    const hero = {
      kind: "character",
      slug: "hero",
      name: "Project seven hero",
      has_ref_images: false,
    }
    const otherHero = { ...hero, name: "Other project hero" }
    useComicStore.setState({
      overviews: {
        6: { ...overview, folderId: 6, characters: [otherHero] },
        7: { ...overview, characters: [hero] },
      },
      panel: { 7: { kind: "storyboard", episodeId: "ep001" } },
    })
    mocks.overview.mockResolvedValue({ ...overview, characters: [hero] })
    let view = renderPanel()
    await screen.findByText("Old frame")
    fireEvent.click(
      view.container.querySelector(".lucide-pencil")!.closest("button")!
    )
    const description = await screen.findByDisplayValue("Old frame")
    fireEvent.change(description, {
      target: { value: "Unsaved shot description" },
    })
    view.unmount()
    view = renderPanel()
    await screen.findByDisplayValue("Unsaved shot description")
    fireEvent.keyDown(screen.getByRole("combobox", { name: "Add character" }), {
      key: "ArrowDown",
    })
    expect(
      await screen.findByRole("option", { name: "Project seven hero" })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole("option", { name: "Other project hero" })
    ).not.toBeInTheDocument()
    view.unmount()
  })

  it("sends its quick action to the existing project conversation", async () => {
    const tab = {
      id: "current-draft",
      kind: "conversation" as const,
      folderId: 7,
      conversationId: null,
      agentType: "codex" as const,
      title: "Draft",
      isPinned: false,
    }
    useTabStore.setState({ rawTabs: [tab], tabs: [tab], activeTabId: tab.id })
    renderPanel()
    const adapt = await screen.findByRole("button", {
      name: "Adapt source into episodes",
    })
    fireEvent.click(adapt)
    expect(
      consumeAskSelectionPrompts(tab.id, {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual([expect.stringContaining("adapt")])
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
    expect(useTabStore.getState().tabs).toHaveLength(1)
  })
})
