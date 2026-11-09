import type { MessageTurn, ModelSwitchInfo } from "@/lib/types"
import type { ConversationTimelineTurn } from "@/stores/conversation-runtime-store"

/** Switch records are immutable facts; replay only contributes unseen ids. */
export function mergeModelSwitches(
  current: ModelSwitchInfo[],
  incoming: readonly ModelSwitchInfo[]
): ModelSwitchInfo[] {
  const known = new Set(current.map((record) => record.id))
  const added = incoming.filter((record) => {
    if (known.has(record.id)) return false
    known.add(record.id)
    return true
  })
  if (added.length === 0) return current
  return [...current, ...added].sort(compareModelSwitches)
}

function compareModelSwitches(a: ModelSwitchInfo, b: ModelSwitchInfo): number {
  if (a.connection_id === b.connection_id) return a.sequence - b.sequence
  return (
    Date.parse(a.created_at) - Date.parse(b.created_at) ||
    a.id.localeCompare(b.id)
  )
}

export function modelSwitchFromTurn(turn: MessageTurn): ModelSwitchInfo | null {
  if (turn.role !== "system") return null
  const block = turn.blocks[0]
  return block?.type === "model_switch" ? block.record : null
}

export function retireModelSwitches(
  records: ModelSwitchInfo[],
  turns: MessageTurn[]
): ModelSwitchInfo[] {
  if (records.length === 0) return records
  const covered = new Set(
    turns.flatMap((turn) => {
      const record = modelSwitchFromTurn(turn)
      return record ? [record.id] : []
    })
  )
  const remaining = records.filter((record) => !covered.has(record.id))
  return remaining.length === records.length ? records : remaining
}

/** Insert metadata after assembling replies, so a stale parse cannot put a
 * persisted switch ahead of the local/streaming reply that preceded it. */
export function mergeModelSwitchTimeline(
  timeline: ConversationTimelineTurn[],
  records: ModelSwitchInfo[],
  turnsOffset: number
): ConversationTimelineTurn[] {
  const persisted = new Map<
    string,
    { item: ConversationTimelineTurn; slot: number }
  >()
  const native: ConversationTimelineTurn[] = []
  for (const item of timeline) {
    const record = modelSwitchFromTurn(item.turn)
    if (record) persisted.set(record.id, { item, slot: native.length })
    else native.push(item)
  }
  if (persisted.size === 0 && records.length === 0) return timeline

  const all = mergeModelSwitches(
    [...persisted.values()].map(({ item }) => modelSwitchFromTurn(item.turn)!),
    records
  )
    .slice()
    .sort(compareModelSwitches)
  const slots = new Map<number, ConversationTimelineTurn[]>()
  for (const record of all) {
    const historical = persisted.get(record.id)
    let anchor = record.after_turn_id
      ? native.findIndex(
          ({ turn }) =>
            turn.id === record.after_turn_id ||
            turn.source_turn_id === record.after_turn_id ||
            turn.agent_message_id === record.after_turn_id
        )
      : -1
    const at = Date.parse(record.created_at)
    if (anchor < 0 && record.after_turn_started_at) {
      const startedAt = Date.parse(record.after_turn_started_at)
      // Native transcripts stamp the prompt after admission. Matching an
      // earlier timestamp instead would attach the switch to the prior round.
      anchor = native.findIndex(({ turn }) => {
        const timestamp = Date.parse(turn.timestamp)
        return turn.role === "user" && timestamp >= startedAt && timestamp <= at
      })
    }
    // A snapshot can include records outside a paged tail. They belong to
    // older history, and must not create rows at the head of this window.
    if (
      !historical &&
      anchor < 0 &&
      turnsOffset > 0 &&
      native.length > 0 &&
      at < Date.parse(native[0].turn.timestamp)
    )
      continue

    let slot: number
    if (anchor >= 0) {
      slot = anchor + 1
      while (slot < native.length && native[slot].turn.role !== "user") slot++
    } else if (historical) {
      slot = historical.slot
      // A parser may round timestamps or synthesize ids. Its position still
      // names a round, but a locally completed/streaming tail can follow it.
      while (slot < native.length && native[slot].turn.role !== "user") slot++
    } else {
      // Round-boundary fallback: never insert into the middle of a reply.
      slot = native.findIndex(
        ({ turn }) => turn.role === "user" && Date.parse(turn.timestamp) > at
      )
      if (slot < 0) slot = native.length
    }
    const item = historical?.item ?? {
      key: `model-switch-${record.id}`,
      phase: "persisted" as const,
      turn: {
        id: `model-switch-${record.id}`,
        role: "system" as const,
        timestamp: record.created_at,
        blocks: [{ type: "model_switch" as const, record }],
      },
    }
    const bucket = slots.get(slot) ?? []
    bucket.push(item)
    slots.set(slot, bucket)
  }
  const result: ConversationTimelineTurn[] = []
  for (let i = 0; i <= native.length; i++) {
    result.push(...(slots.get(i) ?? []))
    if (i < native.length) result.push(native[i])
  }
  return result
}
