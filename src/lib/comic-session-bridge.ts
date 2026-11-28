import type { TabItem } from "@/stores/tab-store"
import { parkWorkbenchTask } from "@/lib/workbench-task-handoff"

/** A workbench click is a user request for the current conversation, not a
 *  reason to open a second session. The workbench handoff delivers the task to
 *  that tab's panel, whose normal message queue sends it when ready. */
export function requestComicConversationTask(
  tab: Pick<
    TabItem,
    "id" | "kind" | "folderId" | "agentType" | "isChat"
  > | null,
  folderId: number,
  text: string
): boolean {
  if (
    !tab ||
    tab.kind !== "conversation" ||
    tab.isChat ||
    tab.folderId !== folderId ||
    folderId <= 0 ||
    !text.trim()
  ) {
    return false
  }
  parkWorkbenchTask(tab.id, {
    prompt: text.trim(),
    agentType: tab.agentType,
    folderId,
  })
  return true
}
