import { beforeEach, describe, expect, it, vi } from "vitest"
import { comicApi, type ComicOverviewPayload } from "@/lib/comic"
import { subscribeComicChanged, useComicStore } from "./comic-store"

const transport = vi.hoisted(() => ({
  subscribe: vi.fn(),
}))
vi.mock("@/lib/transport", () => ({ getTransport: () => transport }))
vi.mock("@/lib/comic", () => ({ comicApi: { getOverview: vi.fn() } }))

function overview(folderId: number): ComicOverviewPayload {
  return {
    folderId,
    has_source: false,
    episodes: [],
    characters: [],
    scenes: [],
    props: [],
    providerPool: [],
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  useComicStore.setState({
    drafts: {},
    overviews: {},
    errors: {},
    loading: {},
    panel: {},
    wizardOpen: {},
  })
})

describe("comic resource drafts", () => {
  it("keeps unsaved resource drafts separate by folder and drops them when the project is forgotten", () => {
    const store = useComicStore.getState()
    expect(store.setResourceDraft).toBeTypeOf("function")
    store.setResourceDraft(7, "episodes/ep001/script", {
      kind: "script",
      text: "Draft seven",
    })
    store.setResourceDraft(8, "episodes/ep001/script", {
      kind: "script",
      text: "Draft eight",
    })
    expect(useComicStore.getState().drafts[7]["episodes/ep001/script"]).toEqual(
      { kind: "script", text: "Draft seven" }
    )
    store.forget(7)
    expect(useComicStore.getState().drafts[7]).toBeUndefined()
    expect(useComicStore.getState().drafts[8]["episodes/ep001/script"]).toEqual(
      { kind: "script", text: "Draft eight" }
    )
  })

  it("does not discard edits made while an older draft was being saved", () => {
    const store = useComicStore.getState()
    expect(store.setResourceDraft).toBeTypeOf("function")
    const saving = { kind: "script" as const, text: "Saving" }
    const newer = { kind: "script" as const, text: "Still editing" }
    store.setResourceDraft(7, "episodes/ep001/script", saving)
    store.setResourceDraft(7, "episodes/ep001/script", newer)
    store.clearResourceDraft(7, "episodes/ep001/script", saving)
    expect(useComicStore.getState().drafts[7]["episodes/ep001/script"]).toBe(
      newer
    )
    store.clearResourceDraft(7, "episodes/ep001/script", newer)
    expect(
      useComicStore.getState().drafts[7]?.["episodes/ep001/script"]
    ).toBeUndefined()
  })
})

describe("comic project synchronization", () => {
  it("shows structured backend details instead of an object string", async () => {
    vi.mocked(comicApi.getOverview).mockRejectedValueOnce({
      code: "io",
      message: "Cannot open project",
      detail: "Permission denied: story/series.json",
    })
    await useComicStore.getState().load(7)
    expect(useComicStore.getState().errors[7]).toBe(
      "Permission denied: story/series.json"
    )
  })

  it("handles failed event subscriptions without an unhandled rejection", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    transport.subscribe.mockRejectedValueOnce(new Error("Disconnected"))
    const dispose = subscribeComicChanged(vi.fn())
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(dispose).not.toThrow()
    expect(warn).toHaveBeenCalledWith(
      "Comic event subscription failed:",
      "Disconnected"
    )
    warn.mockRestore()
  })

  it("consumes the Rust snake_case task notification", async () => {
    let receive: (payload: unknown) => void = () => {}
    const unlisten = vi.fn()
    transport.subscribe.mockImplementationOnce(
      async (_event: string, handler: (payload: unknown) => void) => {
        receive = handler
        return unlisten
      }
    )
    const noticed = vi.fn()
    const dispose = subscribeComicChanged(noticed)
    receive({ kind: "task", folder_id: 7, task_id: 23 })
    expect(noticed).toHaveBeenCalledWith(7)
    await Promise.resolve()
    dispose()
    receive({ kind: "upsert", folder_id: 7, resource: "episode", id: "ep001" })
    expect(noticed).toHaveBeenCalledTimes(1)
    expect(unlisten).toHaveBeenCalledTimes(1)
  })

  it("a forgotten project cannot return through an old request", async () => {
    let finish: (data: ComicOverviewPayload) => void = () => {}
    vi.mocked(comicApi.getOverview).mockImplementationOnce(
      () => new Promise((resolve) => (finish = resolve))
    )
    const loading = useComicStore.getState().load(7)
    useComicStore.getState().forget(7)
    finish(overview(7))
    await loading
    expect(useComicStore.getState().overviews[7]).toBeUndefined()
    expect(useComicStore.getState().loading[7]).toBeUndefined()
  })

  it("a slower older request cannot overwrite a newer overview", async () => {
    let finish: (data: ComicOverviewPayload) => void = () => {}
    vi.mocked(comicApi.getOverview)
      .mockImplementationOnce(
        () => new Promise((resolve) => (finish = resolve))
      )
      .mockResolvedValueOnce({ ...overview(7), has_source: true })
    const older = useComicStore.getState().load(7)
    await useComicStore.getState().load(7)
    finish(overview(7))
    await older
    expect(useComicStore.getState().overviews[7].has_source).toBe(true)
  })
})
