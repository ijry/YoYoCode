import { beforeEach, describe, expect, it } from "vitest"
import {
  consumeWorkbenchTasks,
  resetWorkbenchTasksForTests,
} from "./workbench-task-handoff"
import { requestComicConversationTask } from "./comic-session-bridge"

const tab = {
  id: "current-tab",
  kind: "conversation" as const,
  folderId: 7,
  agentType: "codex",
  conversationId: null,
  title: "Draft",
  isPinned: false,
}

beforeEach(resetWorkbenchTasksForTests)

describe("comic actions in the current conversation", () => {
  it("queues to the current draft without requiring a database conversation", () => {
    expect(requestComicConversationTask(tab, 7, "Generate episode 2")).toBe(
      true
    )
    expect(
      consumeWorkbenchTasks("current-tab", {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual(["Generate episode 2"])
  })

  it("keeps multiple clicks in order and never delivers them to another tab", () => {
    requestComicConversationTask(tab, 7, "First")
    requestComicConversationTask(tab, 7, "Second")
    expect(
      consumeWorkbenchTasks("other-tab", {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual([])
    expect(
      consumeWorkbenchTasks("current-tab", {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual(["First", "Second"])
    expect(
      consumeWorkbenchTasks("current-tab", {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual([])
  })

  it("refuses a missing target, another project, folderless chat, or empty instruction", () => {
    expect(requestComicConversationTask(null, 7, "Generate")).toBe(false)
    expect(requestComicConversationTask(tab, 8, "Generate")).toBe(false)
    expect(
      requestComicConversationTask({ ...tab, isChat: true }, 7, "Generate")
    ).toBe(false)
    expect(requestComicConversationTask(tab, 7, " ")).toBe(false)
    expect(
      consumeWorkbenchTasks("current-tab", {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual([])
  })

  it("holds an instruction while a tab's folder or agent identity differs", () => {
    requestComicConversationTask(tab, 7, "Generate")
    expect(
      consumeWorkbenchTasks("current-tab", {
        folderId: 8,
        agentType: "codex",
      })
    ).toEqual([])
    expect(
      consumeWorkbenchTasks("current-tab", {
        folderId: 7,
        agentType: "claude_code",
      })
    ).toEqual([])
    expect(
      consumeWorkbenchTasks("current-tab", {
        folderId: 7,
        agentType: "codex",
      })
    ).toEqual(["Generate"])
  })
})
