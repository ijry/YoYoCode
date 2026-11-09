import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { describe, expect, it, vi } from "vitest"
import { ComicWorkbenchPanel } from "./comic-workbench-panel"
import { useComicStore } from "@/stores/comic-store"
import messages from "@/i18n/messages/en.json"

const api = vi.hoisted(() => ({
  overview: vi.fn(),
  listChapters: vi.fn(),
  readSource: vi.fn(),
  listSessions: vi.fn(),
}))
vi.mock("@/contexts/active-folder-context", () => ({
  useActiveFolder: () => ({
    activeFolderId: 3,
    activeFolder: { id: 3, path: "D:/novel" },
  }),
}))
vi.mock("@/contexts/workbench-route-context", () => ({
  useWorkbenchRoute: () => ({ setRoute: vi.fn() }),
}))
vi.mock("@/stores/tab-store", () => {
  const state = {
    tabs: [
      {
        id: "source-tab",
        folderId: 3,
        kind: "conversation",
        agentType: "codex",
      },
    ],
    activeTabId: "source-tab",
  }
  return {
    useTabStore: Object.assign(
      (selector: (value: typeof state) => unknown) => selector(state),
      { getState: () => state }
    ),
  }
})
vi.mock("@/lib/api", () => ({
  listAllConversations: api.listSessions,
  createConversation: vi.fn(),
}))
vi.mock("@/lib/comic", () => ({
  comicApi: {
    getOverview: api.overview,
    listChapters: api.listChapters,
    readSource: api.readSource,
  },
}))
vi.mock("@/lib/transport", () => ({
  getTransport: () => ({ subscribe: async () => () => {} }),
}))

describe("comic source reader", () => {
  it("continues inside the same long chapter rather than skipping its remaining text", async () => {
    const overview = {
      folderId: 3,
      has_source: true,
      source: {
        mode: "upload" as const,
        novel_file: "source/novel.txt",
        char_count: 16000,
        chapter_count: 1,
      },
      episodes: [],
      characters: [],
      scenes: [],
      props: [],
      providerPool: [],
    }
    api.overview.mockResolvedValue(overview)
    api.listSessions.mockResolvedValue([])
    api.listChapters.mockResolvedValue([
      { no: 1, title: "Long chapter", chars: 16000 },
    ])
    api.readSource
      .mockResolvedValueOnce({
        text: "Beginning",
        from_chapter: 1,
        to_chapter: 1,
        offset_chars: 0,
        returned_chars: 8000,
        has_more: true,
        next_offset: 8000,
      })
      .mockResolvedValueOnce({
        text: "Continuation",
        from_chapter: 1,
        to_chapter: 1,
        offset_chars: 8000,
        returned_chars: 8000,
        has_more: false,
        next_offset: null,
      })
    useComicStore.setState({
      overviews: { 3: overview },
      errors: {},
      loading: {},
      panel: { 3: { kind: "source" } },
      wizardOpen: {},
    })
    render(
      <NextIntlClientProvider locale="en" messages={messages}>
        <ComicWorkbenchPanel />
      </NextIntlClientProvider>
    )
    await screen.findByText("Beginning")
    fireEvent.click(screen.getByRole("button", { name: "Continue reading" }))
    await waitFor(() => expect(api.readSource).toHaveBeenCalledTimes(2))
    expect(api.readSource).toHaveBeenLastCalledWith(
      3,
      expect.objectContaining({ fromChapter: 1, offsetChars: 8000 })
    )
    await screen.findByText("Continuation")
  })
})
