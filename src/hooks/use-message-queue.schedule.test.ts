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
  enqueue: vi.fn(),
  setSchedule: vi.fn(),
  remove: vi.fn(),
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
  messageQueueEnqueue: wire.enqueue,
  messageQueueSetSchedule: wire.setSchedule,
  messageQueueRemove: wire.remove,
}))
vi.mock("@/lib/platform", () => ({
  subscribe: async () => async () => {},
  onTransportReconnect: () => null,
  onTransportReady: () => null,
}))

function persisted(id: string, extra: Record<string, unknown> = {}) {
  return {
    id,
    draft_json: JSON.stringify({
      blocks: [{ type: "text", text: id }],
      displayText: id,
    }),
    mode_id: null,
    adopt_send_time_mode: false,
    ...extra,
  }
}

beforeEach(() => {
  for (const mock of Object.values(wire)) mock.mockReset()
  wire.list.mockResolvedValue([])
  wire.save.mockResolvedValue(null)
  wire.enqueue.mockResolvedValue([])
  wire.setSchedule.mockResolvedValue([])
  wire.remove.mockResolvedValue([])
})

describe("scheduled send", () => {
  it("a scheduled enqueue goes through the atomic API and adopts the returned queue", async () => {
    wire.capabilities.mockResolvedValue({
      backend_drain: true,
      pause_recovery: true,
      scheduled_send: true,
    })
    wire.enqueue.mockResolvedValue([
      persisted("timed", { scheduled_at: "2026-11-08T09:00:00+00:00" }),
    ])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.scheduledSend).toBe(true))
    await act(async () => {
      await result.current.enqueueScheduled(
        { blocks: [{ type: "text", text: "hi" }], displayText: "hi" },
        null,
        "2026-11-08T09:00:00+00:00"
      )
    })
    expect(wire.enqueue).toHaveBeenCalledWith(
      "42",
      expect.objectContaining({
        draftJson: expect.stringContaining("hi"),
        adoptSendTimeMode: false,
      }),
      "2026-11-08T09:00:00+00:00",
      expect.any(String)
    )
    expect(result.current.queue[0]?.scheduledAt).toBe(
      "2026-11-08T09:00:00+00:00"
    )
    expect(wire.save).not.toHaveBeenCalled()
  })

  it("clearing a schedule converts the item back to ordinary", async () => {
    wire.capabilities.mockResolvedValue({
      backend_drain: true,
      scheduled_send: true,
    })
    wire.list.mockResolvedValue([
      persisted("timed", { scheduled_at: "2026-11-08T09:00:00+00:00" }),
    ])
    wire.setSchedule.mockResolvedValue([persisted("timed")])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() =>
      expect(result.current.queue[0]?.scheduledAt).toBe(
        "2026-11-08T09:00:00+00:00"
      )
    )
    await act(async () => {
      await result.current.scheduleItem("timed", null)
    })
    expect(wire.setSchedule).toHaveBeenCalledWith("42", "timed", null)
    expect(result.current.queue[0]?.scheduledAt).toBeUndefined()
  })

  it("delete goes through the explicit remove API on a capable host", async () => {
    wire.capabilities.mockResolvedValue({
      backend_drain: true,
      scheduled_send: true,
    })
    wire.list.mockResolvedValue([persisted("a"), persisted("b")])
    wire.remove.mockResolvedValue([persisted("b")])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.queue).toHaveLength(2))
    await act(async () => {
      await result.current.removeItem("a")
    })
    expect(wire.remove).toHaveBeenCalledWith("42", "a")
    expect(result.current.queue.map((entry) => entry.id)).toEqual(["b"])
    expect(wire.save).not.toHaveBeenCalled()
  })

  it("schedule actions fail closed until the host declares the capability", async () => {
    wire.capabilities.mockResolvedValue({ backend_drain: true })
    wire.list.mockResolvedValue([persisted("a")])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.scheduledSend).toBe(false))
    await act(async () => {
      await expect(
        result.current.scheduleItem("a", null)
      ).rejects.toThrow()
      await expect(
        result.current.enqueueScheduled(
          { blocks: [], displayText: "x" },
          null,
          "2026-11-08T09:00:00+00:00"
        )
      ).rejects.toThrow()
    })
    expect(wire.setSchedule).not.toHaveBeenCalled()
    expect(wire.enqueue).not.toHaveBeenCalled()
  })

  it("delete falls back to the legacy full save on an older host", async () => {
    wire.capabilities.mockRejectedValue({ code: "not_implemented" })
    wire.list
      .mockResolvedValueOnce([persisted("a"), persisted("b")])
      .mockResolvedValue([persisted("b")])
    const { result } = renderHook(() => useMessageQueue("42"))
    await waitFor(() => expect(result.current.backendDrain).toBe(false))
    await act(async () => {
      await result.current.removeItem("a")
    })
    expect(wire.remove).not.toHaveBeenCalled()
    expect(wire.save).toHaveBeenCalledWith(
      "42",
      [expect.objectContaining({ id: "b" })],
      expect.any(String)
    )
    expect(result.current.queue.map((entry) => entry.id)).toEqual(["b"])
  })
})
