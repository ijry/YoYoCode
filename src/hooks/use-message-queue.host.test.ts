import { act, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useMessageQueue } from "./use-message-queue"

const wire = vi.hoisted(() => ({
  capabilities: vi.fn(),
  list: vi.fn(),
  save: vi.fn(),
  retry: vi.fn(),
  state: vi.fn(),
  continue: vi.fn(),
  releaseHold: vi.fn(),
  sendNow: vi.fn(),
  stateListener: null as null | ((payload: unknown) => void),
  listener: null as null | ((payload: unknown) => void),
  ready: null as null | (() => void),
}))
vi.mock("@/lib/transport", () => ({
  getTransport: () => ({ call: wire.capabilities }),
}))
vi.mock("@/lib/api", () => ({
  messageQueueList: wire.list,
  messageQueueSave: wire.save,
  messageQueueRetry: wire.retry,
  messageQueueState: wire.state,
  messageQueueContinue: wire.continue,
  messageQueueReleaseHold: wire.releaseHold,
  messageQueueSendNow: wire.sendNow,
}))
vi.mock("@/lib/platform", () => ({
  subscribe: async (channel: string, callback: (payload: unknown) => void) => {
    if (channel === "message-queue://state") {
      wire.stateListener = callback
      return () => {
        wire.stateListener = null
      }
    }
    wire.listener = callback
    return () => {
      wire.listener = null
    }
  },
  onTransportReconnect: () => null,
  onTransportReady: (callback: () => void) => {
    wire.ready = callback
    return () => {
      wire.ready = null
    }
  },
}))

function item(id: string, state?: string) {
  return {
    id,
    draft_json: JSON.stringify({
      blocks: [{ type: "text", text: id }],
      displayText: id,
    }),
    mode_id: null,
    adopt_send_time_mode: false,
    ...(state ? { delivery_state: state } : {}),
  }
}

beforeEach(() => {
  wire.capabilities.mockReset()
  wire.list.mockReset().mockResolvedValue([])
  wire.save.mockReset().mockResolvedValue(null)
  wire.retry.mockReset().mockResolvedValue(null)
  wire.state
    .mockReset()
    .mockResolvedValue({
      owner_id: "42",
      status: "open",
      active_attempt_id: null,
      failed_attempt_id: null,
      error: null,
      hold: null,
      revision: 0,
    })
  wire.continue.mockReset().mockResolvedValue(undefined)
  wire.releaseHold.mockReset().mockResolvedValue(undefined)
  wire.sendNow.mockReset().mockResolvedValue(undefined)
  wire.stateListener = null
  wire.listener = null
  wire.ready = null
})

describe("owned queues never race the host drainer", () => {
  it("hydrates the failed task and requests original-context continuation", async () => {
    wire.capabilities.mockResolvedValue({
      backend_drain: true,
      pause_recovery: true,
    })
    wire.state.mockResolvedValue({
      owner_id: "42",
      status: "paused",
      active_attempt_id: null,
      failed_attempt_id: "failed-A",
      error: "quota",
      revision: 2,
    })
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() =>
      expect(result.current.control?.failed_attempt_id).toBe("failed-A")
    )
    await act(async () => result.current.continueTask())
    expect(wire.continue).toHaveBeenCalledWith("42", "failed-A")
    act(() =>
      wire.stateListener?.({
        owner_id: "42",
        status: "resuming",
        active_attempt_id: "resume-A",
        failed_attempt_id: "failed-A",
        error: "quota",
        revision: 3,
      })
    )
    act(() =>
      wire.stateListener?.({
        owner_id: "42",
        status: "open",
        active_attempt_id: null,
        failed_attempt_id: null,
        error: null,
        revision: 1,
      })
    )
    expect(result.current.control?.status).toBe("resuming")
  })

  it("releases a user-stop hold and adopts the newer barrier state", async () => {
    wire.capabilities.mockResolvedValue({
      backend_drain: true,
      pause_recovery: true,
    })
    let revision = 0
    wire.state.mockImplementation(async () => ({
      owner_id: "42",
      status: "open",
      active_attempt_id: null,
      failed_attempt_id: null,
      error: null,
      hold: revision === 0 ? "manual_stop" : null,
      revision,
    }))
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.control?.hold).toBe("manual_stop"))

    // The release command bumps the revision; the hook re-reads and adopts it
    // (a stale read must not clobber a newer broadcast).
    wire.state.mockImplementation(async () => {
      revision = 5
      return {
        owner_id: "42",
        status: "open",
        active_attempt_id: null,
        failed_attempt_id: null,
        error: null,
        hold: null,
        revision,
      }
    })
    await act(async () => result.current.releaseHold())
    expect(wire.releaseHold).toHaveBeenCalledWith("42")
    expect(result.current.control?.hold).toBeNull()
    expect(result.current.control?.revision).toBe(5)
  })

  it("sends one held row now and refreshes the queue and barrier", async () => {
    wire.capabilities.mockResolvedValue({
      backend_drain: true,
      pause_recovery: true,
    })
    wire.list.mockResolvedValue([
      {
        id: "row-B",
        draft_json: JSON.stringify({
          blocks: [{ type: "text", text: "B" }],
          displayText: "B",
        }),
        mode_id: null,
        adopt_send_time_mode: false,
      },
    ])
    wire.state.mockResolvedValue({
      owner_id: "42",
      status: "open",
      active_attempt_id: null,
      failed_attempt_id: null,
      error: null,
      hold: "manual_stop",
      revision: 4,
    })
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.control?.hold).toBe("manual_stop"))

    await act(async () => result.current.sendNow("row-B"))
    expect(wire.sendNow).toHaveBeenCalledWith("42", "row-B")
    await waitFor(() =>
      expect(result.current.queue.map((entry) => entry.id)).toEqual(["row-B"])
    )
  })

  it("retains a current paused broadcast when the capability probe finishes later", async () => {
    let probe!: (value: {
      backend_drain: boolean
      pause_recovery: boolean
    }) => void
    wire.capabilities.mockReturnValue(
      new Promise((resolve) => {
        probe = resolve
      })
    )
    wire.state.mockResolvedValue({
      owner_id: "42",
      status: "open",
      active_attempt_id: null,
      failed_attempt_id: null,
      error: null,
      revision: 0,
    })
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(wire.stateListener).not.toBeNull())
    act(() =>
      wire.stateListener?.({
        owner_id: "42",
        status: "paused",
        active_attempt_id: null,
        failed_attempt_id: "failed",
        error: "quota",
        revision: 2,
      })
    )
    await act(async () => probe({ backend_drain: true, pause_recovery: true }))
    expect(result.current.control?.status).toBe("paused")
  })

  it("blocks legacy dequeue while the host capability request is unresolved", () => {
    wire.capabilities.mockReturnValue(new Promise(() => {}))
    const { result } = renderHook(() => useMessageQueue("42"))
    act(() => {
      result.current.enqueue(
        { blocks: [{ type: "text", text: "A" }], displayText: "A" },
        null
      )
    })
    act(() => expect(result.current.dequeue()).toBeUndefined())
    expect(result.current.queue).toHaveLength(1)
  })

  it("does not permit client dequeue after learning backend ownership", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    wire.list.mockResolvedValue([item("A")])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.backendDrain).toBe(true))
    act(() => expect(result.current.dequeue()).toBeUndefined())
    expect(result.current.queue).toHaveLength(1)
  })

  it("keeps dequeue working against an explicitly older host", async () => {
    wire.capabilities.mockRejectedValue({ code: "not_implemented" })
    wire.list.mockResolvedValue([item("A")])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.backendDrain).toBe(false))
    await waitFor(() => expect(result.current.queue).toHaveLength(1))
    act(() => expect(result.current.dequeue()?.id).toBe("A"))
  })

  it("does not overwrite a newer drain broadcast with the initial list response", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    let finish!: (value: unknown[]) => void
    wire.list.mockReturnValue(
      new Promise<unknown[]>((resolve) => {
        finish = resolve
      })
    )
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(wire.listener).not.toBeNull())
    act(() =>
      wire.listener?.({ owner_id: "42", origin: "backend-drain", items: [] })
    )
    await act(async () => finish([item("already-sent")]))
    expect(result.current.queue).toEqual([])
  })

  it("recovers the initial queue when the page started offline", async () => {
    wire.capabilities.mockRejectedValue(new Error("offline"))
    wire.list.mockRejectedValue(new Error("offline"))
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(wire.ready).not.toBeNull())
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    wire.list.mockResolvedValue([item("persisted-offline")])
    await act(async () => wire.ready?.())
    await waitFor(() =>
      expect(result.current.queue[0]?.id).toBe("persisted-offline")
    )
  })

  it("refreshes persisted queue changes missed during a transport disconnect", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    wire.list.mockResolvedValue([item("A")])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.queue).toHaveLength(1))
    wire.list.mockResolvedValue([])
    await act(async () => wire.ready?.())
    await waitFor(() => expect(result.current.queue).toEqual([]))
  })

  it("serializes full-queue saves so earlier requests cannot overwrite later enqueues", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.backendDrain).toBe(true))
    let finish!: (value: null) => void
    wire.save.mockImplementationOnce(
      () =>
        new Promise<null>((resolve) => {
          finish = resolve
        })
    )
    act(() => {
      result.current.enqueue(
        { blocks: [{ type: "text", text: "A" }], displayText: "A" },
        null
      )
      result.current.enqueue(
        { blocks: [{ type: "text", text: "B" }], displayText: "B" },
        null
      )
    })
    await waitFor(() => expect(wire.save).toHaveBeenCalledTimes(1))
    await act(async () => finish(null))
    await waitFor(() => expect(wire.save).toHaveBeenCalledTimes(2))
  })

  it("persists a parked unbound draft before host hydration can clear it", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    const { result, rerender } = renderHook(
      ({ owner }: { owner: string | null }) => useMessageQueue(owner),
      { initialProps: { owner: null as string | null } }
    )
    act(() =>
      result.current.enqueue(
        { blocks: [{ type: "text", text: "parked" }], displayText: "parked" },
        null
      )
    )
    rerender({ owner: "42" })
    await waitFor(() => expect(wire.save).toHaveBeenCalled())
    expect(wire.save.mock.calls[0][1]).toHaveLength(1)
    expect(
      JSON.parse(wire.save.mock.calls[0][1][0].draftJson).displayText
    ).toBe("parked")
  })

  it("does not let a faster capability response clear a parked draft before owner adoption", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    let hydrate!: (items: unknown[]) => void
    wire.list.mockImplementationOnce(
      () =>
        new Promise<unknown[]>((resolve) => {
          hydrate = resolve
        })
    )
    const { result, rerender } = renderHook(
      ({ owner }: { owner: string | null }) => useMessageQueue(owner),
      { initialProps: { owner: null as string | null } }
    )
    act(() =>
      result.current.enqueue(
        { blocks: [{ type: "text", text: "parked" }], displayText: "parked" },
        null
      )
    )
    rerender({ owner: "42" })
    await waitFor(() => expect(result.current.backendDrain).toBe(true))
    await act(async () => hydrate([]))
    await waitFor(() => expect(wire.save).toHaveBeenCalled())
    expect(
      JSON.parse(wire.save.mock.calls[0][1][0].draftJson).displayText
    ).toBe("parked")
  })

  it("a failed null draft cannot crash the queue row renderer", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    wire.list.mockResolvedValue([
      {
        ...item("null-draft"),
        draft_json: "null",
        delivery_state: "failed",
        last_error: "invalid draft",
      },
    ])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.queue).toHaveLength(1))
    expect(result.current.queue[0].draft.displayText).toBe("null-draft")
    expect(result.current.queue[0].lastError).toBe("invalid draft")
  })

  it("initial hydration preserves pending local enqueues instead of dropping the other client's queue", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    let hydrate!: (items: unknown[]) => void
    wire.list.mockImplementationOnce(
      () =>
        new Promise<unknown[]>((resolve) => {
          hydrate = resolve
        })
    )
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.backendDrain).toBe(true))
    act(() =>
      result.current.enqueue(
        { blocks: [{ type: "text", text: "local" }], displayText: "local" },
        null
      )
    )
    await act(async () => hydrate([item("remote")]))
    await waitFor(() => expect(wire.save).toHaveBeenCalled())
    expect(
      wire.save.mock.calls[0][1].map((entry: { id: string }) => entry.id)
    ).toContain("remote")
    expect(wire.save.mock.calls[0][1]).toHaveLength(2)
  })

  it("ready recovery does not discard a draft parked before the first successful list", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    wire.list.mockRejectedValueOnce(new Error("offline"))
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.backendDrain).toBe(true))
    act(() =>
      result.current.enqueue(
        { blocks: [{ type: "text", text: "parked" }], displayText: "parked" },
        null
      )
    )
    wire.list.mockResolvedValue([item("remote")])
    await act(async () => wire.ready?.())
    await waitFor(() => expect(wire.save).toHaveBeenCalled())
    expect(wire.save.mock.calls[0][1]).toHaveLength(2)
  })

  it("the first broadcast keeps an unsaved parked draft instead of replacing it", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    wire.list.mockReturnValue(new Promise(() => {}))
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(wire.listener).not.toBeNull())
    act(() =>
      result.current.enqueue(
        { blocks: [{ type: "text", text: "parked" }], displayText: "parked" },
        null
      )
    )
    act(() =>
      wire.listener?.({
        owner_id: "42",
        origin: "remote",
        items: [item("remote")],
      })
    )
    await waitFor(() => expect(wire.save).toHaveBeenCalled())
    expect(
      result.current.queue.map((entry) => entry.draft.displayText)
    ).toEqual(["remote", "parked"])
  })

  it("retains a malformed failed draft so it can be removed or recovered", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    wire.list.mockResolvedValue([
      {
        ...item("broken"),
        draft_json: "not-json",
        delivery_state: "failed",
        last_error: "invalid draft",
      },
    ])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.queue).toHaveLength(1))
    expect(result.current.queue[0].lastError).toBe("invalid draft")
  })

  it("does not revive a row after the backend drained it while save was in flight", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    let finish!: (value: null) => void
    wire.save.mockReturnValue(
      new Promise<null>((resolve) => {
        finish = resolve
      })
    )
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.backendDrain).toBe(true))
    act(() =>
      result.current.enqueue(
        { blocks: [{ type: "text", text: "A" }], displayText: "A" },
        null
      )
    )
    act(() =>
      wire.listener?.({ owner_id: "42", origin: "backend-drain", items: [] })
    )
    await act(async () => finish(null))
    expect(result.current.queue).toEqual([])
  })
})
