import { beforeEach, describe, expect, it, vi } from "vitest"

import { openComicExport, openComicShotClip } from "./comic-export"
import {
  resetAppWorkspaceStore,
  useAppWorkspaceStore,
} from "@/stores/app-workspace-store"

const mocks = vi.hoisted(() => ({
  openPath: vi.fn(),
  download: vi.fn(),
  desktop: true,
  remote: false,
}))
vi.mock("@/lib/platform", () => ({ openPath: mocks.openPath }))
vi.mock("@/lib/api", () => ({ downloadWorkspaceFile: mocks.download }))
vi.mock("@/lib/transport", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  isDesktop: () => mocks.desktop,
  isRemoteDesktopMode: () => mocks.remote,
}))

beforeEach(() => {
  vi.clearAllMocks()
  resetAppWorkspaceStore()
  useAppWorkspaceStore.setState({
    allFolders: [
      { id: 7, path: "D:/project-seven" },
      { id: 8, path: "D:/project-eight" },
    ] as never,
    activeFolderId: 8,
  })
  mocks.desktop = true
  mocks.remote = false
  mocks.openPath.mockResolvedValue(undefined)
  mocks.download.mockResolvedValue({ status: "started" })
})

describe("comic exported artifact access", () => {
  it("opens a local export in its captured project, not the current project", async () => {
    await openComicExport(7, "episodes/ep001/export/task-23.mp4")
    expect(mocks.openPath).toHaveBeenCalledWith(
      "D:/project-seven/story/episodes/ep001/export/task-23.mp4"
    )
    expect(mocks.download).not.toHaveBeenCalled()
  })

  it("downloads web and remote exports without reading media into JS memory", async () => {
    for (const remote of [false, true]) {
      mocks.desktop = remote
      mocks.remote = remote
      await openComicExport(7, "episodes/ep001/export/task-23.srt")
    }
    expect(mocks.download).toHaveBeenCalledWith(
      "D:/project-seven",
      "story/episodes/ep001/export/task-23.srt",
      "task-23.srt"
    )
    expect(mocks.openPath).not.toHaveBeenCalled()
  })

  it("opens a bound shot clip only within its own shot's video directory", async () => {
    await openComicShotClip(
      7,
      "episodes/ep001/shots/shot-0001/video/task-42.mkv"
    )
    expect(mocks.openPath).toHaveBeenCalledWith(
      "D:/project-seven/story/episodes/ep001/shots/shot-0001/video/task-42.mkv"
    )
    expect(mocks.download).not.toHaveBeenCalled()
    for (const path of [
      "episodes/ep001/shots/shot-0001/first-frame.png",
      "episodes/ep001/export/task-42.mp4",
      "episodes/ep001/shots/shot-0001/video/task-42.mp4.exe",
      "episodes/ep001/shots/shot-0001/video/../../export/task-42.mp4",
    ]) {
      await expect(openComicShotClip(7, path)).rejects.toThrow()
    }
    expect(mocks.download).not.toHaveBeenCalled()
  })

  it("rejects arbitrary or escaping paths before opening or requesting downloads", async () => {
    for (const path of [
      "../outside.mp4",
      "https://vendor.example/video.mp4",
      "episodes/ep001/export/task-2.exe",
      "episodes/ep001/export/../task-2.mp4",
    ]) {
      await expect(openComicExport(7, path)).rejects.toThrow()
    }
    expect(mocks.openPath).not.toHaveBeenCalled()
    expect(mocks.download).not.toHaveBeenCalled()
  })
})
