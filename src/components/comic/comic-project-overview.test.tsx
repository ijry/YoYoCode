import { act, fireEvent, render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { ComicProjectOverview } from "./comic-project-overview"
import { useComicStore } from "@/stores/comic-store"
import type { ComicOverviewPayload, GenerationTaskView } from "@/lib/comic"
import messages from "@/i18n/messages/en.json"

const api = vi.hoisted(() => ({
  overview: vi.fn(),
  tasks: vi.fn(),
  subscribe: vi.fn(),
}))
vi.mock("@/lib/comic", () => ({
  comicApi: { getOverview: api.overview, listTasks: api.tasks },
}))
vi.mock("@/lib/transport", () => ({
  getTransport: () => ({ subscribe: api.subscribe }),
}))

function overview(folderId: number): ComicOverviewPayload {
  return {
    folderId,
    has_source: true,
    series: {
      title: "Test series",
      bible_refs: [],
      default_candidate_count: 4,
      max_images_per_request: 12,
      max_concurrent_tasks: 2,
      providers: {},
      candidate_retention: { keep_count: 8, max_age_days: 14 },
    },
    episodes: [
      {
        id: "ep002",
        title: "Episode two",
        status: "storyboarded",
        shots_total: 4,
        shots_done: 2,
      },
    ],
    characters: [
      { kind: "character", slug: "hero", name: "Hero", has_ref_images: true },
    ],
    scenes: [],
    props: [],
    providerPool: [],
  }
}

function task(
  folderId: number,
  status: GenerationTaskView["status"] = "running"
): GenerationTaskView {
  return {
    id: 9,
    folderId,
    kind: "image",
    status,
    targetId: "episodes/ep002/shots/s1",
    candidates: [],
    createdAt: "2026-11-03",
  }
}

function panel(folderId: number, open = vi.fn()) {
  return (
    <NextIntlClientProvider locale="en" messages={messages}>
      <ComicProjectOverview folderId={folderId} onOpenWorkbench={open} />
    </NextIntlClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  api.overview.mockImplementation(async (folderId) => overview(folderId))
  api.tasks.mockResolvedValue([task(7)])
  api.subscribe.mockResolvedValue(() => {})
  useComicStore.setState({
    overviews: { 7: overview(7), 8: overview(8) },
    loading: {},
    errors: {},
    panel: {},
    wizardOpen: {},
  })
})

describe("narrow comic overview", () => {
  it("shows project resources and tasks but opens editing in the full workbench", async () => {
    const open = vi.fn()
    render(panel(7, open))
    await screen.findByText("running")
    expect(screen.getByText("Episode two")).toBeInTheDocument()
    expect(screen.getByText("2/4")).toBeInTheDocument()
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: "Adapt source into episodes" })
    ).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: /Episode two/ }))
    expect(open).toHaveBeenCalledWith({
      kind: "storyboard",
      episodeId: "ep002",
    })
    fireEvent.click(screen.getByRole("button", { name: "Hero" }))
    expect(open).toHaveBeenCalledWith({
      kind: "asset",
      assetKind: "character",
      slug: "hero",
    })
    fireEvent.click(screen.getByRole("button", { name: "Open full workbench" }))
    expect(open).toHaveBeenLastCalledWith()
    expect(api.tasks).toHaveBeenCalledWith(7, 8)
  })

  it("keeps the newest task refresh when responses complete out of order", async () => {
    let receive!: (notice: { kind: "task"; folder_id: number }) => void
    let finishOld!: (tasks: GenerationTaskView[]) => void
    api.tasks.mockImplementationOnce(
      () =>
        new Promise<GenerationTaskView[]>((resolve) => {
          finishOld = resolve
        })
    )
    api.subscribe.mockImplementation(async (_event, handler) => {
      receive = handler
      return () => {}
    })
    render(panel(7))
    await act(async () => {})
    api.tasks.mockResolvedValueOnce([task(7, "succeeded")])
    act(() => receive({ kind: "task", folder_id: 7 }))
    await screen.findByText("succeeded")
    await act(async () => {
      finishOld([task(7)])
    })
    expect(screen.queryByText("running")).not.toBeInTheDocument()
    expect(screen.getByText("succeeded")).toBeInTheDocument()
  })

  it("never shows the old project's tasks after switching folders", async () => {
    let finishOld!: (tasks: GenerationTaskView[]) => void
    api.tasks.mockImplementation((folderId) =>
      folderId === 7
        ? new Promise<GenerationTaskView[]>((resolve) => {
            finishOld = resolve
          })
        : Promise.resolve([task(8, "cancelled")])
    )
    const view = render(panel(7))
    view.rerender(panel(8))
    await screen.findByText("cancelled")
    await act(async () => {
      finishOld([task(7)])
    })
    expect(screen.queryByText("running")).not.toBeInTheDocument()
    expect(screen.getByText("cancelled")).toBeInTheDocument()
  })
})
