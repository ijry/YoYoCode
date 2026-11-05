import { comicApi } from "@/lib/comic"
import {
  getServerBaseUrl,
  isDesktop,
  isRemoteDesktopMode,
} from "@/lib/transport"

/**
 * How a bound video candidate can be played from this runtime:
 *
 * - `inline` — a grant URL the `<video>` element can range-request itself.
 *   Local desktop uses the `codeg-media:` scheme served by the app; web
 *   builds the public grant route on the codeg server.
 * - `external` — remote desktop: the webview must not address the remote
 *   server directly, so the caller falls back to the download + system
 *   player path (`openComicShotClip`).
 */
export type ComicVideoPreview =
  | { kind: "inline"; url: string }
  | { kind: "external" }

export async function resolveComicVideoPreview(
  folderId: number,
  taskId: number
): Promise<ComicVideoPreview> {
  if (isRemoteDesktopMode()) return { kind: "external" }
  const grant = await comicApi.requestVideoPreview(folderId, taskId)
  if (isDesktop()) {
    return {
      kind: "inline",
      url: grant.url ?? `codeg-media://localhost/${grant.token}`,
    }
  }
  return {
    kind: "inline",
    url: `${getServerBaseUrl()}/api/comic_video_preview/${grant.token}`,
  }
}
