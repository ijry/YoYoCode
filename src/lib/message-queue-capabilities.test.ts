import { describe, expect, it, vi } from "vitest"
import type { Transport } from "@/lib/transport/types"
import { readMessageQueueCapabilities } from "@/lib/message-queue-capabilities"

function transport(call: Transport["call"]): Transport {
  return { call, subscribe: vi.fn(), isDesktop: () => false }
}

describe("host message queue capabilities", () => {
  it("detects the host drainer without an ACP connection or a snapshot", async () => {
    const host = transport(vi.fn().mockResolvedValue({ backend_drain: true }))
    expect(await readMessageQueueCapabilities(host)).toEqual({
      backend_drain: true,
    })
  })

  it("falls back only when the older host explicitly lacks the command", async () => {
    const older = transport(
      vi.fn().mockRejectedValue({ code: "not_implemented" })
    )
    expect(await readMessageQueueCapabilities(older)).toEqual({
      backend_drain: false,
    })
    const ipc = transport(
      vi.fn().mockRejectedValue("Command message_queue_capabilities not found")
    )
    expect(await readMessageQueueCapabilities(ipc)).toEqual({
      backend_drain: false,
    })
  })

  it("does not turn a network or authentication failure into permission to flush", async () => {
    const offline = transport(
      vi.fn().mockRejectedValue(new Error("Failed to fetch"))
    )
    await expect(readMessageQueueCapabilities(offline)).rejects.toThrow(
      "Failed to fetch"
    )
    const unauthorized = transport(
      vi.fn().mockRejectedValue(new Error("Unauthorized"))
    )
    await expect(readMessageQueueCapabilities(unauthorized)).rejects.toThrow(
      "Unauthorized"
    )
  })
})
