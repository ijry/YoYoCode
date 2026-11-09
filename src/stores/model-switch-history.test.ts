import { beforeEach, describe, expect, it, vi } from "vitest"
import type {
  DbConversationDetail,
  MessageTurn,
  ModelSwitchInfo,
} from "@/lib/types"
import {
  getTimelineTurns,
  resetConversationRuntimeStore,
  useConversationRuntimeStore,
} from "@/stores/conversation-runtime-store"

vi.mock("@/lib/api", () => ({
  getFolderConversation: vi.fn(),
}))
const { getFolderConversation } = await import("@/lib/api")

function record(
  id: string,
  overrides: Partial<ModelSwitchInfo> = {}
): ModelSwitchInfo {
  return {
    id,
    connection_id: "conn-1",
    session_id: "session-1",
    sequence: 1,
    config_id: "model",
    previous_model_id: "a",
    previous_model_name: "A",
    model_id: "b",
    model_name: "B",
    requested_model_id: "b",
    status: "confirmed",
    created_at: "2026-11-09T10:00:05.000Z",
    after_turn_id: "user-1",
    after_turn_started_at: "2026-11-09T10:00:00.000Z",
    ...overrides,
  }
}

function turn(
  id: string,
  role: MessageTurn["role"],
  timestamp: string
): MessageTurn {
  return { id, role, timestamp, blocks: [{ type: "text", text: id }] }
}

function detail(
  turns: MessageTurn[],
  overrides: Partial<DbConversationDetail> = {}
): DbConversationDetail {
  return {
    summary: {
      id: 7,
      folder_id: 1,
      agent_type: "claude_code",
      title: "models",
      title_locked: false,
      status: "completed",
      kind: "regular",
      model: null,
      git_branch: null,
      external_id: "session-1",
      message_count: 0,
      child_count: 0,
      created_at: "2026-11-09T09:00:00.000Z",
      updated_at: "2026-11-09T10:00:00.000Z",
      pinned_at: null,
    },
    turns,
    session_stats: null,
    ...overrides,
  }
}

const actions = () => useConversationRuntimeStore.getState().actions
const ids = () => getTimelineTurns(7).map((item) => item.turn.id)
async function load(value: DbConversationDetail, preserveLive = false) {
  vi.mocked(getFolderConversation).mockResolvedValueOnce(value)
  actions().refetchDetail(7, { preserveLive })
  await vi.waitFor(() => {
    expect(
      useConversationRuntimeStore.getState().byConversationId.get(7)?.detail
    ).toBe(value)
  })
}

beforeEach(() => {
  resetConversationRuntimeStore()
  vi.mocked(getFolderConversation).mockReset()
})

describe("model switch history", () => {
  it("keeps A to B to A and deduplicates replay by event id", () => {
    const first = record("switch-1")
    const second = record("switch-2", {
      sequence: 2,
      previous_model_id: "b",
      model_id: "a",
    })
    actions().applyModelSwitches(7, [second, first, first])
    actions().applyModelSwitches(7, [first])
    expect(ids()).toEqual(["model-switch-switch-1", "model-switch-switch-2"])
    expect(getTimelineTurns(7).map((item) => item.turn.role)).toEqual([
      "system",
      "system",
    ])
  })

  it("places a mid-turn switch after the entire streaming reply", () => {
    actions().appendOptimisticTurn(
      7,
      turn("user-1", "user", "2026-11-09T10:00:00.000Z"),
      "token"
    )
    actions().setLiveMessage(
      7,
      {
        id: "reply-1",
        role: "assistant",
        startedAt: Date.parse("2026-11-09T10:00:01.000Z"),
        content: [{ type: "text", text: "still answering" }],
      },
      true
    )
    actions().applyModelSwitches(7, [record("switch-1")])
    const timeline = getTimelineTurns(7)
    expect(timeline.map((item) => item.turn.role)).toEqual([
      "user",
      "assistant",
      "system",
    ])
    expect(timeline[1].phase).toBe("streaming")
    actions().completeTurn(7)
    expect(getTimelineTurns(7).map((item) => item.turn.role)).toEqual([
      "user",
      "assistant",
      "system",
    ])
  })

  it("repositions persisted switch after a locally completed reply missing from the parse", async () => {
    const user = turn("user-1", "user", "2026-11-09T10:00:00.000Z")
    actions().appendOptimisticTurn(7, user, "token")
    actions().completeTurn(7, {
      id: "reply-1",
      role: "assistant",
      startedAt: Date.parse("2026-11-09T10:00:01.000Z"),
      content: [{ type: "text", text: "answer" }],
    })
    const event = record("switch-1")
    actions().applyModelSwitches(7, [event])
    await load(
      detail([
        user,
        {
          id: "model-switch-switch-1",
          role: "system",
          timestamp: event.created_at,
          blocks: [{ type: "model_switch", record: event }],
        },
      ]),
      true
    )
    expect(getTimelineTurns(7).map((item) => item.turn.role)).toEqual([
      "user",
      "assistant",
      "system",
    ])
    expect(ids().filter((id) => id === "model-switch-switch-1")).toHaveLength(1)
  })

  it.each([null, "2026-11-09T10:00:00.200Z"])(
    "keeps an unanchored persisted switch after the local reply (%s)",
    async (anchor) => {
      const user = turn("parser-user", "user", "2026-11-09T10:00:00.000Z")
      actions().appendOptimisticTurn(7, user, "token")
      actions().completeTurn(7, {
        id: "reply-1",
        role: "assistant",
        startedAt: Date.parse("2026-11-09T10:00:01.000Z"),
        content: [{ type: "text", text: "answer" }],
      })
      const event = record("switch-1", { after_turn_started_at: anchor })
      await load(
        detail([
          user,
          {
            id: "model-switch-switch-1",
            role: "system",
            timestamp: event.created_at,
            blocks: [{ type: "model_switch", record: event }],
          },
        ]),
        true
      )
      expect(getTimelineTurns(7).map((item) => item.turn.role)).toEqual([
        "user",
        "assistant",
        "system",
      ])
    }
  )

  it("keeps switches before the following user prompt when parser IDs differ", async () => {
    await load(
      detail([
        turn("turn-0", "user", "2026-11-09T10:00:00.000Z"),
        turn("turn-1", "assistant", "2026-11-09T10:00:01.000Z"),
        turn("turn-2", "user", "2026-11-09T10:01:00.000Z"),
        turn("turn-3", "assistant", "2026-11-09T10:01:01.000Z"),
      ])
    )
    actions().applyModelSwitches(7, [record("switch-1")])
    expect(ids()).toEqual([
      "turn-0",
      "turn-1",
      "model-switch-switch-1",
      "turn-2",
      "turn-3",
    ])
  })

  it("anchors to a native prompt written after the backend admitted it", async () => {
    await load(
      detail([
        turn("turn-0", "user", "2026-11-09T09:00:00.000Z"),
        turn("turn-1", "assistant", "2026-11-09T09:00:01.000Z"),
        turn("turn-2", "user", "2026-11-09T10:00:00.020Z"),
        turn("turn-3", "assistant", "2026-11-09T10:00:01.000Z"),
      ])
    )
    actions().applyModelSwitches(7, [record("switch-1")])
    expect(ids()).toEqual([
      "turn-0",
      "turn-1",
      "turn-2",
      "turn-3",
      "model-switch-switch-1",
    ])
  })

  it("keeps a new branch switch that arrives while its fork history is loading", async () => {
    actions().applyModelSwitches(7, [record("old")])
    actions().setExternalId(7, "session-2")
    actions().applyModelSwitches(7, [
      record("new", { session_id: "session-2", sequence: 2 }),
    ])
    const forkDetail = detail([])
    forkDetail.summary.external_id = "session-2"
    vi.mocked(getFolderConversation).mockResolvedValueOnce(forkDetail)
    actions().refetchDetail(7, { preserveLive: true, dropLiveTurnIds: [] })
    await vi.waitFor(() => {
      expect(
        useConversationRuntimeStore.getState().byConversationId.get(7)?.detail
      ).toBe(forkDetail)
    })
    expect(ids()).toEqual(["model-switch-new"])
  })

  it("does not prepend snapshot records from before the loaded history window", async () => {
    await load(
      detail(
        [
          turn("turn-20", "user", "2026-11-09T11:00:00.000Z"),
          turn("turn-21", "assistant", "2026-11-09T11:00:01.000Z"),
        ],
        { turns_offset: 20, turns_total: 22 }
      )
    )
    actions().applyModelSwitches(7, [record("old")])
    expect(ids()).toEqual(["turn-20", "turn-21"])
  })

  it("places an unanchored historical record before the next prompt and preserves raw model IDs", () => {
    actions().applyModelSwitches(7, [
      record("initial", {
        after_turn_id: null,
        after_turn_started_at: null,
        model_id: "custom:codeg:glm-5.2",
        model_name: null,
        status: "accepted",
        created_at: "2026-11-09T09:59:00.000Z",
      }),
    ])
    actions().appendOptimisticTurn(
      7,
      turn("user-1", "user", "2026-11-09T10:00:00.000Z"),
      "token"
    )
    expect(ids()).toEqual(["model-switch-initial", "user-1"])
    expect(getTimelineTurns(7)[0].turn.blocks[0]).toMatchObject({
      record: { model_id: "custom:codeg:glm-5.2", status: "accepted" },
    })
  })
})
