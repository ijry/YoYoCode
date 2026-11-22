import type { DbConversationSummary } from "@/lib/types"

export const ATTACH_FILE_TO_SESSION_EVENT = "codeg:attach-file-to-session"

export interface AttachFileToSessionDetail {
  tabId: string
  path: string
  /**
   * Optional 1-based, inclusive line span to attach as a ranged file badge
   * (`foo.ts:10-25`). Omitted by whole-file callers (file tree, git changes);
   * supplied by the editor's "add selection to chat". When present the consumer
   * encodes it into the badge uri (`file://…#L10-25`) and label.
   */
  range?: { start: number; end: number }
}

export function emitAttachFileToSession(
  detail: AttachFileToSessionDetail
): void {
  if (typeof window === "undefined") return
  window.dispatchEvent(
    new CustomEvent<AttachFileToSessionDetail>(ATTACH_FILE_TO_SESSION_EVENT, {
      detail,
    })
  )
}

export const ATTACH_SESSION_TO_SESSION_EVENT = "codeg:attach-session-to-session"

export interface AttachSessionToSessionDetail {
  /** The conversation tab whose composer receives the mention badge. */
  tabId: string
  /**
   * The conversation being mentioned. Carried whole (rather than by id) so the
   * consumer builds the badge through the same `sessionToSuggestion` adapter the
   * `@` panel uses — one source of truth for the label / `yoyocode://session/<id>`
   * uri / agent + status + branch meta.
   */
  conversation: DbConversationSummary
  /** Set by the composer that took the event (see the pending queue below). */
  consumed?: boolean
}

export function emitAttachSessionToSession(
  detail: AttachSessionToSessionDetail
): void {
  if (typeof window === "undefined") return
  window.dispatchEvent(
    new CustomEvent<AttachSessionToSessionDetail>(
      ATTACH_SESSION_TO_SESSION_EVENT,
      { detail }
    )
  )
}

export const ATTACH_PAGE_TO_SESSION_EVENT = "codeg:attach-page-to-session"

/**
 * Something from the built-in browser going to a conversation: an element the
 * person picked, a screenshot of the page, the lines it printed.
 *
 * `text` is the block the agent reads and is page content — the backend
 * (`browser/handoff.rs`) has already capped it and put a "data, not
 * instructions" header on it. `label` names the badge that stands in for it in
 * the composer; the picker's label comes from the page (`button#export`), the
 * rest are named by the caller in the user's own language.
 */
export interface AttachPageToSessionDetail {
  /** The conversation tab whose composer receives it. */
  tabId: string
  label: string
  text: string
  /** Names the block for the agent: the page's address, with anything
   *  secret-looking already taken out of it by the backend. */
  uri: string
  /** A picture of what was handed over. Goes through the composer's ordinary
   *  image path, so its wire encoding follows the agent's capabilities and an
   *  agent that takes no images simply does not get it. */
  image?: File
  /** Set by the composer that took it. Dispatch is synchronous, so the sender
   *  reads this the moment it returns — and a conversation closed while the
   *  person was choosing an element has no listener at all, which must not be
   *  reported back to them as "added to the chat". */
  accepted?: boolean
}

/** Hand something from the built-in browser to a conversation's composer.
 *  Returns whether a composer took it. */
export function emitAttachPageToSession(
  detail: AttachPageToSessionDetail
): boolean {
  if (typeof window === "undefined") return false
  const event = new CustomEvent<AttachPageToSessionDetail>(
    ATTACH_PAGE_TO_SESSION_EVENT,
    { detail }
  )
  window.dispatchEvent(event)
  return event.detail.accepted === true
}

export const APPEND_TEXT_TO_SESSION_EVENT = "codeg:append-text-to-session"

export interface AppendTextToSessionDetail {
  tabId: string
  text: string
  /** Set by the composer that took the event (see the pending queue below). */
  consumed?: boolean
}

export function emitAppendTextToSession(
  detail: AppendTextToSessionDetail
): void {
  if (typeof window === "undefined") return
  window.dispatchEvent(
    new CustomEvent<AppendTextToSessionDetail>(APPEND_TEXT_TO_SESSION_EVENT, {
      detail,
    })
  )
}

// ─── Pending attachments: prefill a composer that isn't mounted yet ───

export type PendingSessionAttachment =
  | { kind: "session"; conversation: DbConversationSummary }
  | { kind: "text"; text: string }

/** How long a queued prefill may wait for its composer before it is dropped. */
export const PENDING_SESSION_ATTACHMENTS_TTL_MS = 3000
const PENDING_RETRY_INTERVAL_MS = 150

const pendingAttachments = new Map<string, PendingSessionAttachment[]>()
const warnedDropTabs = new Set<string>()

/** Queue composer content for a tab whose composer does not exist yet. */
export function queuePendingSessionAttachments(
  tabId: string,
  items: PendingSessionAttachment[]
): void {
  if (items.length === 0) return
  const queue = pendingAttachments.get(tabId) ?? []
  queue.push(...items)
  pendingAttachments.set(tabId, queue)
}

/**
 * One drain pass over a tab's queue: dispatch each item as its ordinary
 * attachment event and drop only the ones a composer actually consumed
 * (`detail.consumed`). Events nobody took stay queued for the next pass.
 */
export function drainPendingSessionAttachments(tabId: string): void {
  if (typeof window === "undefined") return
  const queue = pendingAttachments.get(tabId)
  if (!queue?.length) return
  for (const item of [...queue]) {
    const consumed =
      item.kind === "session"
        ? dispatchUntilConsumed<AttachSessionToSessionDetail>(
            ATTACH_SESSION_TO_SESSION_EVENT,
            { tabId, conversation: item.conversation }
          )
        : dispatchUntilConsumed<AppendTextToSessionDetail>(
            APPEND_TEXT_TO_SESSION_EVENT,
            { tabId, text: item.text }
          )
    if (consumed) queue.splice(queue.indexOf(item), 1)
  }
  if (queue.length === 0) pendingAttachments.delete(tabId)
}

function dispatchUntilConsumed<T extends { consumed?: boolean }>(
  eventType: string,
  detail: T
): boolean {
  const event = new CustomEvent<T>(eventType, { detail })
  window.dispatchEvent(event)
  return event.detail.consumed === true
}

/**
 * Prefill right after opening the tab: drain immediately, then keep retrying
 * until the composer consumed everything. A tab whose composer never mounts
 * drops its queue after `PENDING_SESSION_ATTACHMENTS_TTL_MS` (warned once) so
 * a dead tab cannot hold prefill forever.
 */
export function flushPendingSessionAttachments(tabId: string): void {
  if (typeof window === "undefined") return
  drainPendingSessionAttachments(tabId)
  if (!pendingAttachments.get(tabId)?.length) return
  const startedAt = Date.now()
  const retry = setInterval(() => {
    drainPendingSessionAttachments(tabId)
    if (!pendingAttachments.get(tabId)?.length) {
      clearInterval(retry)
      return
    }
    if (Date.now() - startedAt >= PENDING_SESSION_ATTACHMENTS_TTL_MS) {
      clearInterval(retry)
      pendingAttachments.delete(tabId)
      if (!warnedDropTabs.has(tabId)) {
        warnedDropTabs.add(tabId)
        console.warn(
          `[session-attachment] dropped pending prefill for tab ${tabId}: composer never became ready`
        )
      }
    }
  }, PENDING_RETRY_INTERVAL_MS)
}
