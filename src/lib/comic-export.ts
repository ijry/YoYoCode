import { downloadWorkspaceFile } from "@/lib/api"
import { joinRootRel } from "@/lib/file-open-target"
import { openPath } from "@/lib/platform"
import { isDesktop, isRemoteDesktopMode } from "@/lib/transport"
import { useAppWorkspaceStore } from "@/stores/app-workspace-store"

export async function openComicExport(
  folderId: number,
  path: string
): Promise<void> {
  if (!/^episodes\/[a-z0-9_-]+\/export\/task-\d+\.(mp4|srt)$/.test(path)) {
    throw new Error("Not a comic episode export")
  }
  await openComicFile(folderId, path)
}

/**
 * Open a shot's generated video clip with the system player — the preview
 * fallback when inline playback is unavailable (remote desktop, or a codec
 * the embedded player cannot decode).
 */
export async function openComicShotClip(
  folderId: number,
  path: string
): Promise<void> {
  if (
    !/^episodes\/[a-z0-9_-]+\/shots\/[a-z0-9_-]+\/video\/task-\d+\.(mp4|webm|mkv)$/.test(
      path
    )
  ) {
    throw new Error("Not a comic shot clip")
  }
  await openComicFile(folderId, path)
}

async function openComicFile(folderId: number, path: string): Promise<void> {
  const folder = useAppWorkspaceStore
    .getState()
    .allFolders.find((item) => item.id === folderId)
  if (!folder) throw new Error("The export's project is no longer open")
  const relative = `story/${path}`
  if (isDesktop() && !isRemoteDesktopMode()) {
    await openPath(joinRootRel(folder.path, relative))
  } else {
    await downloadWorkspaceFile(folder.path, relative, path.split("/").pop()!)
  }
}
