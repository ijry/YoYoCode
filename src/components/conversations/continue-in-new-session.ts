import type { DbConversationSummary } from "@/lib/types"

/**
 * Everything the continue-in-new-session flow needs from its host, injected so
 * the whole stop → mark-completed → open-and-prefill chain is unit-testable
 * without stores or the backend.
 */
export interface ContinueDeps {
  /** The tab currently holding this conversation, if any (triple match). */
  findTabForConversation(
    conversation: DbConversationSummary
  ): { id: string } | null
  /** Live connection bound to a tab: status + the backend cancel handle. */
  getConnection(tabId: string): {
    status: string
    connectionId: string
  } | null
  cancelConnection(connectionId: string): Promise<void>
  /** Marks the session completed (optimistic UI + persisted command). */
  markCompleted(conversationId: number): void
  /** The folder's working dir, or null when the folder no longer resolves. */
  getFolderPath(folderId: number): string | null
  /** Opens (and activates) the new conversation draft; returns its tab id.
   *  `preferredConfigValues` is the caller's one-shot model pick (or null:
   *  the agent's defaults), applied by the draft's connect. */
  openNewConversation(
    folderId: number,
    path: string,
    agentType: string,
    preferredConfigValues: Record<string, string> | null
  ): string
  queuePrefill(
    tabId: string,
    conversation: DbConversationSummary,
    promptText: string
  ): void
  flushPrefill(tabId: string): void
}

/**
 * The connection to cancel for a conversation that is still generating, or
 * null: either nothing holds it in a tab (no connection to stop — the status
 * mark alone settles it) or its tab is connected but not prompting.
 */
export function resolveRunningConnectionId(
  conversation: DbConversationSummary,
  deps: ContinueDeps
): string | null {
  const tab = deps.findTabForConversation(conversation)
  if (!tab) return null
  const conn = deps.getConnection(tab.id)
  return conn?.status === "prompting" ? conn.connectionId : null
}

/** Caller's choices from the continue dialog: the agent to continue on
 *  (default: the old session's own) and the model pinned one-shot on the new
 *  draft (default: none — the agent's own defaults apply). The @-mention
 *  attachment always carries the OLD conversation, whatever the choice. */
export interface ContinueOverrides {
  agentType?: string
  preferredConfigValues?: Record<string, string> | null
}

/**
 * "Continue in a new session": settle the old session, then hand off. The
 * folder path resolves FIRST so a folder that no longer exists aborts the
 * whole thing before anything is stopped or marked — losing the running work
 * with nowhere to continue would be the worst outcome. Resolves false on that
 * abort (the caller toasts); true once the handoff is underway. A failed
 * cancel (connection already gone) never blocks the handoff: the completed
 * mark is what keeps the list free of duplicate names, so it always runs.
 */
export async function runContinueInNewSession(
  conversation: DbConversationSummary,
  deps: ContinueDeps,
  promptText: string,
  options?: ContinueOverrides
): Promise<boolean> {
  const folderPath = deps.getFolderPath(conversation.folder_id)
  if (!folderPath) return false

  const connectionId = resolveRunningConnectionId(conversation, deps)
  if (connectionId != null) {
    try {
      await deps.cancelConnection(connectionId)
    } catch {
      // Connection gone mid-flight: the completed mark below still lands.
    }
  }
  deps.markCompleted(conversation.id)

  const tabId = deps.openNewConversation(
    conversation.folder_id,
    folderPath,
    options?.agentType ?? conversation.agent_type,
    options?.preferredConfigValues ?? null
  )
  deps.queuePrefill(tabId, conversation, promptText)
  deps.flushPrefill(tabId)
  return true
}
