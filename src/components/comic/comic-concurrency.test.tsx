import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ComicEpisodeManager } from "./comic-episode-manager"
import { ComicStoryboardImportDialog } from "./comic-storyboard-import-dialog"
import { useComicStore } from "@/stores/comic-store"
import type { ComicOverviewPayload, StoryShot } from "@/lib/comic"

const mocks = vi.hoisted(() => ({
  call: vi.fn(),
  close: vi.fn(),
  imported: vi.fn(),
}))
vi.mock("@/lib/transport", () => ({
  getTransport: () => ({ call: mocks.call }),
}))
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}))
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }))

const overview: ComicOverviewPayload = {
  folderId: 7,
  has_source: false,
  episodes: [],
  characters: [],
  scenes: [],
  props: [],
  providerPool: [],
}
const conflict = { code: "conflict", message: "Changed concurrently" }
const shot: StoryShot = {
  id: "shot-1",
  no: 1,
  description: "Original",
  dialogue: [],
  duration_secs: 3,
  refs: { characters: [], props: [] },
  state: "draft",
  video: { path: "video/task-7.mp4", duration_secs: 3, task_id: 7 },
}

beforeEach(() => {
  vi.resetAllMocks()
  mocks.imported.mockResolvedValue(undefined)
  useComicStore.setState({
    overviews: { 7: overview },
    drafts: {},
    panel: {},
    loading: {},
    errors: {},
  })
})

function newEpisode() {
  render(
    <ComicEpisodeManager
      folderId={7}
      overview={overview}
      createOpen
      onCreateOpenChange={mocks.close}
    />
  )
  fireEvent.change(screen.getByLabelText("episodeTitle"), {
    target: { value: "Pilot" },
  })
  fireEvent.change(screen.getByLabelText("script"), {
    target: { value: "My script" },
  })
  fireEvent.click(screen.getByRole("button", { name: "create" }))
}

function importBoard() {
  render(
    <ComicStoryboardImportDialog
      folderId={7}
      episodeId="ep001"
      open
      onOpenChange={mocks.close}
      onImported={mocks.imported}
    />
  )
  fireEvent.change(screen.getByLabelText("storyboardJson"), {
    target: { value: '[{"id":"shot-1","description":"Imported"}]' },
  })
  fireEvent.click(screen.getByRole("button", { name: "storyboardImport" }))
}

describe("comic write preconditions", () => {
  it("reallocates after an ID is claimed between overview and create", async () => {
    let attempts = 0
    mocks.call.mockImplementation(async (command, params) => {
      if (command === "comic_get_overview") return overview // Deliberately stale.
      if (command === "comic_upsert_episode") {
        if (++attempts === 1) throw conflict
        return params.meta
      }
      if (command === "comic_write_script") return
      throw new Error(`Unexpected ${command}`)
    })
    newEpisode()
    await waitFor(() => expect(mocks.close).toHaveBeenCalledWith(false))
    expect(mocks.call).toHaveBeenCalledWith("comic_upsert_episode", {
      folderId: 7,
      meta: { id: "ep002", title: "Pilot", status: "draft" },
      createOnly: true,
    })
    expect(mocks.call).toHaveBeenCalledWith("comic_write_script", {
      folderId: 7,
      episode: "ep002",
      script: "My script",
      createOnly: true,
    })
    expect(useComicStore.getState().panel[7]).toEqual({
      kind: "storyboard",
      episodeId: "ep002",
    })
  })

  it("retries only the owned episode's create-only script after a failed write", async () => {
    let scriptAttempts = 0
    mocks.call.mockImplementation(async (command, params) => {
      if (command === "comic_get_overview") return overview
      if (command === "comic_upsert_episode") return params.meta
      if (command === "comic_write_script") {
        if (++scriptAttempts === 1)
          throw { code: "io", message: "Disk unavailable" }
        // A different writer supplied the script while the dialog was waiting.
        throw conflict
      }
      throw new Error(`Unexpected ${command}`)
    })
    newEpisode()
    await screen.findByRole("alert")
    fireEvent.click(screen.getByRole("button", { name: "create" }))
    await waitFor(() => expect(scriptAttempts).toBe(2))
    expect(
      mocks.call.mock.calls.filter(
        ([command]) => command === "comic_upsert_episode"
      )
    ).toHaveLength(1)
    expect(mocks.call).toHaveBeenLastCalledWith("comic_write_script", {
      folderId: 7,
      episode: "ep001",
      script: "My script",
      createOnly: true,
    })
    expect(screen.getByLabelText("script")).toHaveValue("My script")
    expect(useComicStore.getState().drafts[7]["episode-create"]).toMatchObject({
      createdId: "ep001",
    })
    expect(mocks.close).not.toHaveBeenCalled()
  })

  it.each([{ initial: [] }, { initial: [shot] }])(
    "requires a refreshed confirmation when a board changes after reading $initial",
    async ({ initial }) => {
      const newer = { shots: [{ ...shot, description: "Concurrent edit" }] }
      let reads = 0
      let writes = 0
      mocks.call.mockImplementation(async (command) => {
        if (command === "comic_get_overview") return overview
        if (command === "comic_get_asset") {
          return { storyboard: ++reads === 1 ? { shots: initial } : newer }
        }
        if (command === "comic_replace_storyboard") {
          if (++writes === 1) throw conflict
          return { shots: [] }
        }
        throw new Error(`Unexpected ${command}`)
      })
      importBoard()
      if (initial.length) {
        await screen.findByRole("alertdialog")
        fireEvent.click(
          screen.getByRole("button", { name: "storyboardReplace" })
        )
      }
      await screen.findByRole("alert")
      expect(mocks.call).toHaveBeenCalledWith(
        "comic_replace_storyboard",
        expect.objectContaining({
          expectedStoryboard: { shots: initial },
        })
      )
      expect(mocks.imported).not.toHaveBeenCalled()
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
      expect(screen.getByLabelText("storyboardJson")).toHaveValue(
        '[{"id":"shot-1","description":"Imported"}]'
      )
      fireEvent.click(
        within(screen.getByRole("dialog")).getByRole("button", {
          name: "storyboardImport",
        })
      )
      await screen.findByRole("alertdialog")
      expect(writes).toBe(1)
      expect(screen.getByRole("alertdialog")).toHaveTextContent(
        "storyboardVideoRetainedHint"
      )
      fireEvent.click(screen.getByRole("button", { name: "storyboardReplace" }))
      await waitFor(() => expect(mocks.close).toHaveBeenCalledWith(false))
      expect(mocks.call).toHaveBeenCalledWith(
        "comic_replace_storyboard",
        expect.objectContaining({
          expectedStoryboard: newer,
        })
      )
    }
  )
})
