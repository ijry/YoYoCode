import type { AgentType } from "@/lib/types"

/**
 * Workbench actions submit prompts to an existing conversation tab. The tab's
 * panel consumes them into its normal message queue, which owns readiness,
 * persistence and sending. This buffer only bridges the panel's lifecycle.
 *
 * A task may arrive before the panel mounts, so keep it in memory and drain
 * on mount. The event also wakes an already-mounted panel. Keying by tab id
 * prevents another split group's panel from consuming the task.
 */

export const WORKBENCH_TASK_PARKED_EVENT = "codeg:workbench-task-parked"

export interface WorkbenchTaskParkedDetail {
  /** Conversation tab the task was parked for. */
  tabId: string
}

export interface ParkedWorkbenchTask {
  /** Instruction submitted by a workbench action. */
  prompt: string
  /**
   * The target tab's agent and project when the task was submitted. A draft
   * can be retargeted asynchronously, so its panel must match both before
   * consuming the task. A mismatch stays buffered until the identity matches.
   */
  agentType: AgentType
  folderId: number
}

/** The state a draining panel reports about its own tab. */
export interface WorkbenchTaskDrainState {
  agentType: AgentType
  folderId: number
}

/** Parked prompts per target tab. A list, not a single slot: two actions aimed at
 *  the same conversation tab before it drains must both survive. */
const parked = new Map<string, ParkedWorkbenchTask[]>()

/** Park a composed prompt for `tabId` and nudge its panel if already mounted. */
export function parkWorkbenchTask(
  tabId: string,
  entry: ParkedWorkbenchTask
): void {
  const queued = parked.get(tabId)
  if (queued) {
    queued.push(entry)
  } else {
    parked.set(tabId, [entry])
  }
  if (typeof window === "undefined") return
  window.dispatchEvent(
    new CustomEvent<WorkbenchTaskParkedDetail>(WORKBENCH_TASK_PARKED_EVENT, {
      detail: { tabId },
    })
  )
}

/**
 * Take the prompts parked for `tabId` that match the tab's CURRENT state, in
 * order. Anything that doesn't match stays parked for a later drain — the
 * caller re-runs this when its agent or folder changes, which is precisely when
 * a pending retarget lands.
 *
 * Returns [] when nothing was parked, or nothing parked matches yet.
 */
export function consumeWorkbenchTasks(
  tabId: string,
  state: WorkbenchTaskDrainState
): string[] {
  const queued = parked.get(tabId)
  if (!queued) return []

  const taken: string[] = []
  const held: ParkedWorkbenchTask[] = []
  for (const entry of queued) {
    if (
      entry.agentType === state.agentType &&
      entry.folderId === state.folderId
    ) {
      taken.push(entry.prompt)
    } else {
      held.push(entry)
    }
  }
  if (held.length === 0) {
    parked.delete(tabId)
  } else {
    parked.set(tabId, held)
  }
  return taken
}

/** Drop everything parked for a tab — its panel is going away for good, so
 *  nothing will ever drain it. Called on tab close so a prompt held back by the
 *  match guard can't outlive its target. */
export function discardWorkbenchTasks(tabId: string): void {
  parked.delete(tabId)
}

/** Test seam: drop everything still parked. */
export function resetWorkbenchTasksForTests(): void {
  parked.clear()
}
