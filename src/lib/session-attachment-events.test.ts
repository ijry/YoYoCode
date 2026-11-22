import { act } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { DbConversationSummary } from "@/lib/types"

import {
  ATTACH_SESSION_TO_SESSION_EVENT,
  APPEND_TEXT_TO_SESSION_EVENT,
  PENDING_SESSION_ATTACHMENTS_TTL_MS,
  drainPendingSessionAttachments,
  flushPendingSessionAttachments,
  queuePendingSessionAttachments,
  type AttachSessionToSessionDetail,
  type AppendTextToSessionDetail,
} from "./session-attachment-events"

const conversation = {
  id: 42,
  title: "Fix auth",
} as DbConversationSummary

/**
 * Window-level spy for the two attachment events. `consumes` mirrors whether a
 * mounted composer is listening: a real MessageInput handler stamps
 * `detail.consumed = true` when it takes an event, and the queue keys retry
 * decisions on exactly that mark. Cleaned up per test — a leaked consuming
 * listener would stamp later tests' "no composer" events.
 */
const spyCleanups: Array<() => void> = []
function spyOnEvents(initialConsumes: boolean) {
  const spy = {
    consumes: initialConsumes,
    sessions: [] as AttachSessionToSessionDetail[],
    texts: [] as AppendTextToSessionDetail[],
  }
  const onSession = (event: Event) => {
    const detail = (event as CustomEvent<AttachSessionToSessionDetail>).detail
    if (spy.consumes) detail.consumed = true
    spy.sessions.push(detail)
  }
  const onText = (event: Event) => {
    const detail = (event as CustomEvent<AppendTextToSessionDetail>).detail
    if (spy.consumes) detail.consumed = true
    spy.texts.push(detail)
  }
  window.addEventListener(ATTACH_SESSION_TO_SESSION_EVENT, onSession)
  window.addEventListener(APPEND_TEXT_TO_SESSION_EVENT, onText)
  spyCleanups.push(() => {
    window.removeEventListener(ATTACH_SESSION_TO_SESSION_EVENT, onSession)
    window.removeEventListener(APPEND_TEXT_TO_SESSION_EVENT, onText)
  })
  return spy
}

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  for (const cleanup of spyCleanups.splice(0)) cleanup()
})

describe("pending session attachment queue", () => {
  it("drains queued attachments as consumed events and empties the queue", () => {
    const spy = spyOnEvents(true)
    queuePendingSessionAttachments("tab-1", [
      { kind: "session", conversation },
      { kind: "text", text: "请继续" },
    ])

    drainPendingSessionAttachments("tab-1")

    expect(spy.sessions).toHaveLength(1)
    expect(spy.sessions[0]).toMatchObject({
      tabId: "tab-1",
      conversation,
      consumed: true,
    })
    expect(spy.texts).toHaveLength(1)
    expect(spy.texts[0]).toMatchObject({ tabId: "tab-1", text: "请继续" })

    drainPendingSessionAttachments("tab-1")
    expect(spy.sessions).toHaveLength(1)
    expect(spy.texts).toHaveLength(1)
  })

  it("keeps items for retry when no composer consumed the dispatch", () => {
    const spy = spyOnEvents(false)
    queuePendingSessionAttachments("tab-1", [{ kind: "session", conversation }])

    drainPendingSessionAttachments("tab-1")
    expect(spy.sessions).toHaveLength(1)
    expect(spy.sessions[0].consumed).toBeUndefined()

    // The composer mounts and starts consuming: the next drain delivers.
    spy.consumes = true
    drainPendingSessionAttachments("tab-1")
    expect(spy.sessions).toHaveLength(2)
    expect(spy.sessions[1].consumed).toBe(true)

    drainPendingSessionAttachments("tab-1")
    expect(spy.sessions).toHaveLength(2)
  })

  it("flush retries until a composer consumes, then stops", () => {
    const spy = spyOnEvents(false)
    queuePendingSessionAttachments("tab-1", [{ kind: "session", conversation }])

    flushPendingSessionAttachments("tab-1")
    act(() => vi.advanceTimersByTime(300))
    expect(spy.sessions).toHaveLength(3)

    spy.consumes = true
    act(() => vi.advanceTimersByTime(150))
    expect(spy.sessions).toHaveLength(4)
    expect(spy.sessions[3].consumed).toBe(true)

    act(() => vi.advanceTimersByTime(10_000))
    expect(spy.sessions).toHaveLength(4)
  })

  it("flush drops leftovers after the TTL and warns once", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    const spy = spyOnEvents(false)
    queuePendingSessionAttachments("tab-1", [{ kind: "session", conversation }])

    flushPendingSessionAttachments("tab-1")
    act(() => vi.advanceTimersByTime(PENDING_SESSION_ATTACHMENTS_TTL_MS + 300))

    const dispatchedBeforeExpiry = spy.sessions.length
    expect(dispatchedBeforeExpiry).toBeGreaterThan(0)
    expect(warn).toHaveBeenCalledTimes(1)

    spy.consumes = true
    act(() => vi.advanceTimersByTime(5_000))
    expect(spy.sessions).toHaveLength(dispatchedBeforeExpiry)
  })
})
