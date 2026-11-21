import { describe, it, expect } from "vitest"
import {
  CONTINUE_INSTRUCTION_PREFIXES,
  expandFoldedGroups,
  flushRetryDelayMs,
  isConnectionReady,
  QUEUE_FLUSH_RETRY_BACKOFF_MS,
  shouldQueueDirectSend,
  shouldRejectDuplicateCreate,
  canFlushClientQueue,
  foldQueueForDisplay,
  isContinueInstruction,
  type QueueFoldRow,
} from "./queue-flush"

describe("client queue ownership", () => {
  it("leaves an unbound draft queue drainable even on a new host", () => {
    expect(canFlushClientQueue(false, true, null)).toBe(true)
  })

  it("blocks bound queues while host ownership is known or being probed", () => {
    expect(canFlushClientQueue(true, true, false)).toBe(false)
    expect(canFlushClientQueue(true, false, null)).toBe(false)
    expect(canFlushClientQueue(true, false, true)).toBe(false)
    expect(canFlushClientQueue(true, false, false)).toBe(true)
  })
})

describe("flushRetryDelayMs", () => {
  it("returns 0 when no bounce has happened (lastBounceAt = 0)", () => {
    expect(flushRetryDelayMs(10_000, 0)).toBe(0)
  })

  it("returns 0 once the backoff window has fully elapsed", () => {
    const now = 10_000
    expect(flushRetryDelayMs(now, now - QUEUE_FLUSH_RETRY_BACKOFF_MS)).toBe(0)
    expect(
      flushRetryDelayMs(now, now - QUEUE_FLUSH_RETRY_BACKOFF_MS - 500)
    ).toBe(0)
  })

  it("returns the remaining backoff for a recent bounce (rate-limits retries)", () => {
    const now = 10_000
    // Bounced 200ms ago → wait the rest of the window.
    expect(flushRetryDelayMs(now, now - 200, 1000)).toBe(800)
    // Bounced this instant → wait the full window.
    expect(flushRetryDelayMs(now, now, 1000)).toBe(1000)
  })

  it("clamps to the backoff window (skewed/future bounce → full backoff, never negative)", () => {
    // A future/skewed bounce timestamp is treated as just-bounced — full
    // backoff, never a negative or unbounded delay.
    expect(flushRetryDelayMs(10_000, 20_000, 1000)).toBe(1000)
    expect(flushRetryDelayMs(10_000, 9_500, 1000)).toBe(500)
  })
})

describe("shouldQueueDirectSend", () => {
  it("tail-routes a direct send when the queue is non-empty (preserve FIFO)", () => {
    expect(shouldQueueDirectSend(false, 2)).toBe(true)
    expect(shouldQueueDirectSend(false, 1)).toBe(true)
  })

  it("sends a direct send immediately when the queue is empty", () => {
    expect(shouldQueueDirectSend(false, 0)).toBe(false)
  })

  it("never tail-routes the auto-flush itself (it is draining the queue)", () => {
    expect(shouldQueueDirectSend(true, 5)).toBe(false)
    expect(shouldQueueDirectSend(true, 0)).toBe(false)
  })
})

describe("isConnectionReady", () => {
  const cwd = "/work/chat-sessions/2026-06-11/abc"
  const AGENT = "codex"
  /** connected, cwd-matched, agent-matched — ready unless a term is varied. */
  const ready = (
    status: string | null,
    connectedCwd: string | null | undefined,
    intendedCwd: string | null | undefined,
    connectedAgent: string | null | undefined = AGENT,
    selectedAgent: string | null | undefined = AGENT
  ) =>
    isConnectionReady(
      status,
      connectedCwd,
      intendedCwd,
      connectedAgent,
      selectedAgent
    )

  it("is ready when connected AND the connection cwd matches the intended cwd", () => {
    expect(ready("connected", cwd, cwd)).toBe(true)
  })

  it("is NOT ready when connected but the connection cwd differs (stale reconnect window)", () => {
    // The crux of the chat-draft fix: a stale "connected" for the PREVIOUS cwd
    // must not be treated as ready, or a send would hit the wrong workspace.
    expect(ready("connected", "/old/folder", cwd)).toBe(false)
  })

  it("is NOT ready in any non-connected status, even if cwds match", () => {
    expect(ready("connecting", cwd, cwd)).toBe(false)
    expect(ready("disconnected", cwd, cwd)).toBe(false)
    expect(ready("prompting", cwd, cwd)).toBe(false)
    expect(ready(null, cwd, cwd)).toBe(false)
  })

  it("normalizes nullish cwds so null and undefined compare equal", () => {
    expect(ready("connected", null, undefined)).toBe(true)
    expect(ready("connected", undefined, null)).toBe(true)
    // A real cwd vs. no cwd is still a mismatch.
    expect(ready("connected", cwd, null)).toBe(false)
    expect(ready("connected", null, cwd)).toBe(false)
  })

  it("is NOT ready while the live connection still belongs to another agent", () => {
    // Switching a draft's agent leaves the OLD agent's connection live at the
    // SAME cwd until the lifecycle reconnects — and for a not-installed target
    // it never reconnects at all. The queue flush dequeues before sending, so
    // treating that as ready takes the message off the queue and then drops it.
    expect(ready("connected", cwd, cwd, "claude_code", "codex")).toBe(false)
  })

  it("treats a connection with no agent recorded yet as no mismatch", () => {
    expect(ready("connected", cwd, cwd, null, "codex")).toBe(true)
    expect(ready("connected", cwd, cwd, undefined, "codex")).toBe(true)
  })
})

describe("shouldRejectDuplicateCreate", () => {
  it("rejects a second submit while an unbound create is in flight", () => {
    expect(shouldRejectDuplicateCreate(false, true)).toBe(true)
  })

  it("allows the first submit (no create pending yet)", () => {
    expect(shouldRejectDuplicateCreate(false, false)).toBe(false)
  })

  it("never single-flights a persisted conversation (it allows concurrent queued sends)", () => {
    expect(shouldRejectDuplicateCreate(true, true)).toBe(false)
    expect(shouldRejectDuplicateCreate(true, false)).toBe(false)
  })
})

describe("isContinueInstruction", () => {
  it("recognizes continue instructions across the ten UI languages", () => {
    for (const text of [
      "继续",
      "继续。",
      "继续吧",
      "繼續之前的任務",
      "接着完成任务",
      "继续之前因错误中断的任务。从中断处接着完成尚未完成的部分，不要重新执行已完成操作，也不要开始待发送队列中的后续任务。",
      "continue",
      "Continue.",
      "continue the task from where it stopped",
      "resume the task",
      "carry on with the work",
      "go on",
      "続行します",
      "続けてください",
      "続きをお願い",
      "계속 진행하세요",
      "이어서 진행해 주세요",
      "continúa con la tarea",
      "continuar el trabajo",
      "sigue adelante",
      "fortsetzen",
      "Weitermachen",
      "continuez la tâche",
      "reprends le travail",
      "continuar a tarefa",
      "retoma o trabalho",
      "متابعة المهمة",
      "تابع العمل",
      "استمر من حيث توقفت",
    ]) {
      expect(isContinueInstruction(text)).toBe(true)
    }
  })

  it("rejects anything that is not a short single-line continue instruction", () => {
    for (const text of [
      "",
      "      ",
      "task A",
      "完成登录模块的重构",
      "先写文档，然后继续其他任务",
      "继续阅读并总结这本书的每一章内容，整理出核心观点、论证结构和可执行的改进建议，同时对比作者在不同章节中的论述方式，最后给出一份完整的读书报告，包括引言、正文和结论三个部分，并附上每章的关键引文和页码，方便后续核对原始材料，另外请把所有引用的术语都翻译成英文并保留原文对照，方便跨语言的读者理解这些概念在不同语境下的含义差异。",
      "继续\n然后清点剩余工作",
      "。。。",
    ]) {
      expect(isContinueInstruction(text)).toBe(false)
    }
  })

  it("stays in sync with the Rust classifier by construction", () => {
    // 与 src-tauri 的 CONTINUE_INSTRUCTION_PREFIXES 同表：两侧任一侧加词，
    // 这里以长度断言提醒同步另一侧。
    expect(CONTINUE_INSTRUCTION_PREFIXES.length).toBe(25)
  })
})

describe("foldQueueForDisplay", () => {
  const row = (
    id: string,
    text: string,
    extra: Partial<QueueFoldRow> = {}
  ) => ({
    id,
    draft: { displayText: text },
    ...extra,
  })

  it("folds identical pending rows into one group with a copy count", () => {
    const groups = foldQueueForDisplay(
      [
        row("a", "继续"),
        row("b", "task A"),
        row("c", "继续"),
        row("d", "继续"),
      ],
      null
    )

    expect(groups).toHaveLength(2)
    expect(groups[0].item.id).toBe("a")
    expect(groups[0].copies.map((copy) => copy.id)).toEqual(["a", "c", "d"])
    expect(groups[1].item.id).toBe("b")
    expect(groups[1].copies).toHaveLength(1)
  })

  it("keeps scheduled, delivered, failed and edited rows out of the fold", () => {
    const groups = foldQueueForDisplay(
      [
        row("a", "继续"),
        row("s", "继续", { scheduledAt: "2026-12-01T00:00:00Z" }),
        row("f", "继续", { deliveryState: "failed" }),
        row("claimed", "继续", { deliveryState: "claimed" }),
        row("edit", "继续"),
      ],
      "edit"
    )

    // 除编辑中的行外，其余状态行都独立渲染：折叠只属于普通 pending 拷贝。
    expect(groups).toHaveLength(5)
    expect(groups[0].copies).toHaveLength(1)
  })

  it("preserves first-occurrence order for groups and standalone rows", () => {
    const groups = foldQueueForDisplay(
      [
        row("a", "task A"),
        row("b", "继续"),
        row("c", "task A"),
        row("d", "继续"),
      ],
      null
    )

    expect(groups.map((group) => group.item.id)).toEqual(["a", "b"])
    expect(groups[0].copies.map((copy) => copy.id)).toEqual(["a", "c"])
    expect(groups[1].copies.map((copy) => copy.id)).toEqual(["b", "d"])
  })
})

describe("expandFoldedGroups", () => {
  const row = (id: string, text: string): QueueFoldRow => ({
    id,
    draft: { displayText: text },
  })

  it("expands groups back to the raw row order without losing copies", () => {
    const a1 = row("a1", "继续")
    const a2 = row("a2", "继续")
    const b = row("b", "task A")
    const groups = [
      { item: a1, copies: [a1, a2] },
      { item: b, copies: [b] },
    ]

    expect(expandFoldedGroups(groups)).toEqual([a1, a2, b])
  })
})
