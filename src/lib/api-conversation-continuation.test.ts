import { beforeEach, describe, expect, it, vi } from "vitest"

import { createConversation, createChatConversation, acpConnect } from "./api"

const { call } = vi.hoisted(() => ({ call: vi.fn() }))
vi.mock("@/lib/transport", () => ({
  getTransport: () => ({ call }),
}))

describe("conversation continuation transport", () => {
  beforeEach(() => call.mockReset().mockResolvedValue(43))

  it("passes the source id in the atomic conversation-create request", async () => {
    await expect(createConversation(1, "codex", "Continue", 42)).resolves.toBe(
      43
    )
    expect(call).toHaveBeenCalledExactlyOnceWith("create_conversation", {
      folderId: 1,
      agentType: "codex",
      title: "Continue",
      continuationSourceId: 42,
    })
  })

  it("keeps ordinary creation compatible without a continuation source", async () => {
    await createConversation(1, "claude_code")
    expect(call).toHaveBeenCalledExactlyOnceWith("create_conversation", {
      folderId: 1,
      agentType: "claude_code",
      title: null,
    })
  })
})

it("carries a chat continuation source into its atomic create", async () => {
  await createChatConversation("codex", "Continue", "/scratch", 42)
  expect(call).toHaveBeenLastCalledWith("create_chat_conversation", {
    agentType: "codex",
    title: "Continue",
    existingDir: "/scratch",
    continuationSourceId: 42,
  })
})

it("carries the draft selection source separately from persisted conversation identity", async () => {
  await acpConnect(
    "codex",
    "/repo",
    undefined,
    null,
    { model: "default" },
    null,
    {
      sourceId: 42,
      configOverrides: { effort: "high" },
    }
  )
  expect(call).toHaveBeenLastCalledWith(
    "acp_connect",
    expect.objectContaining({
      conversationId: null,
      continuation: { sourceId: 42, configOverrides: { effort: "high" } },
    })
  )
})
