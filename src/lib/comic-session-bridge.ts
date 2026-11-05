import type { TabItem } from "@/stores/tab-store"
import { parkAskSelectionPrompt } from "@/lib/ask-selection-handoff"

/** A workbench click is a user request for the current conversation, not a
 *  reason to open a second session. The existing handoff buffers requests for
 *  draft tabs and their normal message queue sends them when ready. */
export function requestComicConversationTask(
  tab: Pick<TabItem, "id" | "kind" | "folderId" | "agentType" | "isChat"> | null,
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
  parkAskSelectionPrompt(tab.id, {
    prompt: text.trim(),
    agentType: tab.agentType,
    folderId,
  })
  return true
}
