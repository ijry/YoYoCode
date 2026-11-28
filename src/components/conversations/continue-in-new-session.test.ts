import { describe, expect, it, vi } from "vitest"

import type { DbConversationSummary } from "@/lib/types"

import {
  resolveRunningConnectionId,
  runContinueInNewSession,
  type ContinueDeps,
} from "./continue-in-new-session"

const conversation = {
  id: 42,
  folder_id: 1,
  title: "Fix auth",
  agent_type: "claude_code",
} as DbConversationSummary

function makeDeps(overrides: Partial<ContinueDeps> = {}): ContinueDeps {
  return {
    findTabForConversation: vi.fn(() => null),
    getConnection: vi.fn(() => null),
    cancelConnection: vi.fn(async () => {}),
    markCompleted: vi.fn(),
    getFolderPath: vi.fn(() => "/p/1"),
    openNewConversation: vi.fn(() => "new-tab-1"),
    queuePrefill: vi.fn(),
    flushPrefill: vi.fn(),
    ...overrides,
  }
}

describe("resolveRunningConnectionId", () => {
  it("returns null when no tab holds the conversation", () => {
    const deps = makeDeps()
    expect(resolveRunningConnectionId(conversation, deps)).toBeNull()
    expect(deps.getConnection).not.toHaveBeenCalled()
  })

  it("returns the connection id only while the tab is prompting", () => {
    const deps = makeDeps({
      findTabForConversation: vi.fn(() => ({ id: "tab-1" })),
      getConnection: vi.fn((_tabId: string) => ({
        status: "prompting",
        connectionId: "conn-55",
      })),
    })
    expect(resolveRunningConnectionId(conversation, deps)).toBe("conn-55")

    const idle = makeDeps({
      findTabForConversation: vi.fn(() => ({ id: "tab-1" })),
      getConnection: vi.fn((_tabId: string) => ({
        status: "connected",
        connectionId: "conn-55",
      })),
    })
    expect(resolveRunningConnectionId(conversation, idle)).toBeNull()
  })
})

describe("runContinueInNewSession", () => {
  it("completes the old session, opens a new tab with its agent and queues the prefill", async () => {
    const deps = makeDeps()
    const started = await runContinueInNewSession(conversation, deps, "请继续")
    expect(started).toBe(true)

    expect(deps.cancelConnection).not.toHaveBeenCalled()
    const markOrder = vi.mocked(deps.markCompleted).mock.invocationCallOrder[0]
    const openOrder = vi.mocked(deps.openNewConversation).mock
      .invocationCallOrder[0]
    expect(markOrder).toBeLessThan(openOrder)
    expect(deps.markCompleted).toHaveBeenCalledWith(42)
    expect(deps.openNewConversation).toHaveBeenCalledWith(
      1,
      "/p/1",
      "claude_code",
      null,
      42
    )
    expect(deps.queuePrefill).toHaveBeenCalledWith(
      "new-tab-1",
      conversation,
      "请继续"
    )
    expect(deps.flushPrefill).toHaveBeenCalledWith("new-tab-1")
  })

  it("opens the new tab on the chosen agent when overridden", async () => {
    const deps = makeDeps()
    await runContinueInNewSession(conversation, deps, "请继续", {
      agentType: "codex",
    })

    // The @-mention attachment still carries the OLD conversation; only the
    // tab's agent is the caller's choice.
    expect(deps.openNewConversation).toHaveBeenCalledWith(
      1,
      "/p/1",
      "codex",
      null,
      42
    )
    expect(deps.queuePrefill).toHaveBeenCalledWith(
      "new-tab-1",
      conversation,
      "请继续"
    )
  })

  it("passes the one-shot preferred config values to the new tab", async () => {
    const deps = makeDeps()
    const preferredConfigValues = { model: "dialog-model" }
    await runContinueInNewSession(conversation, deps, "请继续", {
      preferredConfigValues,
    })

    expect(deps.openNewConversation).toHaveBeenCalledWith(
      1,
      "/p/1",
      "claude_code",
      preferredConfigValues,
      42
    )
  })

  it("cancels the running connection before marking completed", async () => {
    const deps = makeDeps({
      findTabForConversation: vi.fn(() => ({ id: "tab-1" })),
      getConnection: vi.fn((_tabId: string) => ({
        status: "prompting",
        connectionId: "conn-55",
      })),
    })
    await runContinueInNewSession(conversation, deps, "请继续")

    expect(deps.cancelConnection).toHaveBeenCalledWith("conn-55")
    const cancelOrder = vi.mocked(deps.cancelConnection).mock
      .invocationCallOrder[0]
    const markOrder = vi.mocked(deps.markCompleted).mock.invocationCallOrder[0]
    expect(cancelOrder).toBeLessThan(markOrder)
  })

  it("does not cancel when the tab holds the conversation but is idle", async () => {
    const deps = makeDeps({
      findTabForConversation: vi.fn(() => ({ id: "tab-1" })),
      getConnection: vi.fn((_tabId: string) => ({
        status: "connected",
        connectionId: "conn-55",
      })),
    })
    await runContinueInNewSession(conversation, deps, "请继续")

    expect(deps.cancelConnection).not.toHaveBeenCalled()
    expect(deps.markCompleted).toHaveBeenCalledWith(42)
  })

  it("keeps going when the cancel fails (connection already gone)", async () => {
    const deps = makeDeps({
      findTabForConversation: vi.fn(() => ({ id: "tab-1" })),
      getConnection: vi.fn((_tabId: string) => ({
        status: "prompting",
        connectionId: "conn-55",
      })),
      cancelConnection: vi.fn(async () => {
        throw new Error("connection gone")
      }),
    })
    await runContinueInNewSession(conversation, deps, "请继续")

    expect(deps.markCompleted).toHaveBeenCalledWith(42)
    expect(deps.openNewConversation).toHaveBeenCalled()
  })

  it("reports an abort — before stopping anything — when the folder no longer resolves", async () => {
    const deps = makeDeps({ getFolderPath: vi.fn(() => null) })
    const started = await runContinueInNewSession(conversation, deps, "请继续")

    // The boolean is the caller's toast signal: a confirmed "Stop and
    // continue" that silently does nothing is worse than a message.
    expect(started).toBe(false)
    expect(deps.cancelConnection).not.toHaveBeenCalled()
    expect(deps.markCompleted).not.toHaveBeenCalled()
    expect(deps.openNewConversation).not.toHaveBeenCalled()
    expect(deps.queuePrefill).not.toHaveBeenCalled()
  })
})
