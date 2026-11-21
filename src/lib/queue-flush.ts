/**
 * Rate-limiting for the conversation message-queue auto-flush.
 *
 * When a queued message is sent but the backend rejects it because a turn is
 * already in flight (TurnBusyError), the draft is re-queued. The queue's
 * level-triggered flush would otherwise re-send it immediately — and if the
 * backend stays busy while the client still believes it is idle (the
 * "prompting" broadcast is delayed or missed), that becomes a retry storm of
 * one bounced send per round-trip. This backoff bounds the retry RATE.
 */

export function canFlushClientQueue(
  owned: boolean,
  snapshotBackendDrain: boolean,
  hostBackendDrain: boolean | null
): boolean {
  // An unbound draft has no persisted owner for Rust to drain yet.
  return !owned || (!snapshotBackendDrain && hostBackendDrain === false)
}

/** Minimum gap between auto-flush retries after a bounced (busy) send. */
export const QUEUE_FLUSH_RETRY_BACKOFF_MS = 1000

/**
 * Milliseconds to wait before the next queue-flush attempt, given `now` and the
 * timestamp of the last bounce (`lastBounceAt`, 0 if none). Returns 0 when no
 * bounce has happened recently — a normal queued message flushes promptly; only
 * a just-bounced retry is delayed.
 */
export function flushRetryDelayMs(
  now: number,
  lastBounceAt: number,
  backoffMs: number = QUEUE_FLUSH_RETRY_BACKOFF_MS
): number {
  // Clamp elapsed at 0 so a skewed/future bounce timestamp is treated as
  // just-bounced (full backoff) rather than yielding an unbounded delay. The
  // result is always within [0, backoffMs].
  const elapsed = Math.max(0, now - lastBounceAt)
  if (elapsed >= backoffMs) return 0
  return backoffMs - elapsed
}

/**
 * Whether a send should be routed to the TAIL of the message queue instead of
 * being sent immediately, to preserve FIFO order.
 *
 * The shared send handler serves two callers: the queue auto-flush (which
 * dequeued the head and must send it now — `fromQueueFlush`), and direct input
 * sends. A direct send issued while the queue is non-empty must NOT jump ahead
 * of already-queued items — it belongs at the tail. The auto-flush always sends
 * (it IS draining the queue), so it never tail-routes.
 */
export function shouldQueueDirectSend(
  fromQueueFlush: boolean,
  queueLength: number
): boolean {
  return !fromQueueFlush && queueLength > 0
}

/**
 * Whether the live connection is ready to accept a send for THIS tab: connected,
 * its established cwd matches the tab's intended working dir, AND it belongs to
 * the agent the tab currently has selected.
 *
 * Bare `connStatus === "connected"` is insufficient. A chat draft that just
 * retargeted into folderless mode (or any tab mid-reconnect) can read a stale
 * "connected" belonging to the PREVIOUS cwd for a render or two before the
 * reconnect lands. Sending then would deliver the prompt to the wrong
 * agent/workspace.
 *
 * The agent check covers the same hazard on the other axis: switching a draft's
 * agent leaves the OLD agent's connection live — at the same cwd — until the
 * lifecycle's reconnect lands, and for a not-installed target it never lands at
 * all. All three terms belong together because the queue auto-flush DEQUEUES
 * before handing the message to the send path: if the flush gate were weaker
 * than the send's own check, the message would be taken off the queue and then
 * silently dropped.
 *
 * Nullish cwds are normalized so `null`/`undefined` compare equal (both mean "no
 * cwd yet"). A nullish `connectedAgentType` means no agent has been recorded for
 * the connection yet, which is not a mismatch.
 */
export function isConnectionReady(
  connStatus: string | null | undefined,
  connectedWorkingDir: string | null | undefined,
  intendedWorkingDir: string | null | undefined,
  connectedAgentType: string | null | undefined,
  selectedAgentType: string | null | undefined
): boolean {
  return (
    connStatus === "connected" &&
    (connectedWorkingDir ?? null) === (intendedWorkingDir ?? null) &&
    (connectedAgentType == null || connectedAgentType === selectedAgentType)
  )
}

/**
 * Whether a direct submit must be rejected because an unbound new-tab create is
 * already in flight (single-flight the create).
 *
 * A second submit fired before the first create resolves (double Enter / double
 * click) would otherwise append an optimistic turn it can never deliver: the
 * create-pending guard that actually stops the duplicate returns only AFTER the
 * optimistic append. `hasPersistedId` is true once the tab is bound to a real
 * conversation row, so only the unbound path is single-flighted — persisted
 * conversations keep their legitimate concurrent queued-send behavior.
 */
export function shouldRejectDuplicateCreate(
  hasPersistedId: boolean,
  createPending: boolean
): boolean {
  return !hasPersistedId && createPending
}

// ─── +1 折叠：相同 pending 消息折叠为一条 xN 组，「继续」行支持 +1 ─────────

/**
 * 队首消息算不算一条「继续」指令 —— Rust 侧
 * `message_queue_control_service.rs::is_continue_instruction` 的 TS 镜像：
 * 报错自动放行（宿主）与 +1 按钮的显隐（客户端）必须用同一张判定表。
 *
 * 规则：去首尾空白后非空、单行、≤120 字符，且以 10 种 UI 语言的任一「继续」
 * 关键词开头（拉丁文不区分大小写）。与 Rust 侧任一侧增删关键词时，另一侧
 * `CONTINUE_INSTRUCTION_PREFIXES` 必须同步（queue-flush.test.ts 有长度断言提醒）。
 */
const CONTINUE_INSTRUCTION_PREFIXES: readonly string[] = [
  "继续",
  "繼續",
  "接着",
  "接著",
  "continue",
  "resume",
  "carry on",
  "go on",
  "続行",
  "続け",
  "続き",
  "계속",
  "이어서",
  "continúa",
  "continuar",
  "sigue",
  "fortsetzen",
  "weiter",
  "continuez",
  "reprend",
  "continua",
  "retoma",
  "متابعة",
  "تابع",
  "استمر",
]

export { CONTINUE_INSTRUCTION_PREFIXES }

export function isContinueInstruction(raw: string): boolean {
  const text = raw.trim()
  if (!text || text.includes("\n") || [...text].length > 120) return false
  const lowered = text.toLowerCase()
  return CONTINUE_INSTRUCTION_PREFIXES.some((prefix) =>
    lowered.startsWith(prefix)
  )
}

/**
 * 折叠候选行的最小结构（不直接依赖 hooks 层的 QueuedMessage 类型，保持 lib
 * 层不反向依赖 hooks）。
 */
export interface QueueFoldRow {
  id: string
  draft: { displayText: string }
  /** 有值即非 pending：claimed/sending/failed/uncertain 都不参与折叠。 */
  deliveryState?: string | null
  /** 定时行带自己的时间 UI，不折叠。 */
  scheduledAt?: string | null
}

/** 折叠后的一个展示组：`item` 是组代表行（行内操作落在它身上），`copies` 是
 *  它名下的全部真实队列拷贝 —— 计数就是 `copies.length`。 */
export interface FoldedQueueGroup<T extends QueueFoldRow = QueueFoldRow> {
  item: T
  copies: T[]
}

/**
 * 把队列折叠成展示组：相同 displayText 的 pending 行（未定时、非编辑中）并成
 * 一组，按首现位置排列；其余行（带状态 UI / 定时 / 编辑中）独立成单元素组，
 * 任何行都不会消失。组的计数随拷贝发出自然减一（drain 后 pending 拷贝变少）。
 */
export function foldQueueForDisplay<T extends QueueFoldRow>(
  queue: T[],
  editingId: string | null
): FoldedQueueGroup<T>[] {
  const groups: FoldedQueueGroup<T>[] = []
  const byText = new Map<string, FoldedQueueGroup<T>>()
  for (const item of queue) {
    const foldable =
      item.id !== editingId && item.deliveryState == null && !item.scheduledAt
    if (!foldable) {
      groups.push({ item, copies: [item] })
      continue
    }
    const key = item.draft.displayText
    const existing = byText.get(key)
    if (existing) {
      existing.copies.push(item)
    } else {
      const group: FoldedQueueGroup<T> = { item, copies: [item] }
      byText.set(key, group)
      groups.push(group)
    }
  }
  return groups
}

/** 拖拽排序发生在组级别：展开回真实行序交给既有 onReorder，拷贝不丢、组内
 *  相对顺序不变。 */
export function expandFoldedGroups<T extends QueueFoldRow>(
  groups: FoldedQueueGroup<T>[]
): T[] {
  return groups.flatMap((group) => group.copies)
}
