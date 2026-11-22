"use client"

import {
  useCallback,
  useMemo,
  useRef,
  useState,
  type PointerEvent,
} from "react"
import { Reorder, useDragControls } from "motion/react"
import {
  CalendarClock,
  Clock,
  GripVertical,
  Pencil,
  RotateCcw,
  Send,
  X,
  Zap,
} from "lucide-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import { toErrorMessage } from "@/lib/app-error"
import { formatScheduleShort } from "@/lib/task-schedule"
import { draftRidesBlocks } from "@/lib/prompt-draft"
import {
  expandFoldedGroups,
  foldQueueForDisplay,
  isContinueInstruction,
  type FoldedQueueGroup,
} from "@/lib/queue-flush"
import type { QueuedMessage } from "@/hooks/use-message-queue"
import type { MessageQueueState } from "@/lib/types"
import { MessageScheduleDialog } from "./message-schedule-dialog"

interface MessageQueueDisplayProps {
  queue: QueuedMessage[]
  queueControl?: MessageQueueState | null
  onContinue?: () => Promise<void> | void
  /** Lift the user-stop hold (`queueControl.hold`) so the queue drains again.
   *  Present only on hosts with the hold command; the banner renders when it
   *  can be acted on. */
  onReleaseHold?: () => Promise<void> | void
  /**
   * Send ONE queued row immediately at the user's explicit request. A manual
   * override for a queue parked by a user stop OR by an error pause: in the
   * latter case sending a chosen row IS the decision to resume the session
   * (the backend admits that prompt through the pause). A recovery turn that
   * is requested or running is still refused, and a failure surfaces on the
   * row's delivery state.
   */
  onSendNow?: (id: string) => Promise<void> | void
  onReorder: (items: QueuedMessage[]) => void
  onEdit: (id: string) => void
  onDelete: (id: string) => void
  onRetry?: (id: string) => Promise<void> | void
  editingItemId: string | null
  /**
   * Move one pending item between ordinary (`null`) and a one-off schedule.
   * Present only on hosts that declare `scheduled_send`; without it the clock
   * actions are hidden rather than offered-but-broken.
   */
  onScheduleItem?: (
    id: string,
    scheduledAt: string | null
  ) => Promise<void> | void
  /**
   * Send one queued item straight into the RUNNING turn over the session's
   * live-feedback channel (same delivery as the composer's mid-turn send).
   * Present only while the session has a working channel AND a turn is in
   * flight; the host decides whether the row is removed (it stays queued on
   * the turn-end race). Resolves once delivery is settled.
   */
  onSteerItem?: (id: string) => Promise<void> | void
  /** Which channel {@link onSteerItem} rides; picks the honest icon/copy,
   *  mirroring the composer's split-button (`Zap` = instant insert,
   *  `Clock` = note the agent reads on its next check). */
  steerChannel?: "native" | "pull"
  /**
   * Queue ONE more identical copy of a pending row (the row's own +1 button).
   * Offered on continue-type rows only — the fold keeps the copies collapsed
   * as one `继续 xN` row until each copy drains (N-1 per send). Absent on
   * hosts/rows where it cannot apply.
   */
  onQueuePlusOne?: (item: QueuedMessage) => void
}

interface QueueItemProps {
  item: QueuedMessage
  index: number
  isEditing: boolean
  onEdit: (id: string) => void
  onDelete: (id: string) => void
  onRetry?: (id: string) => void
  onScheduleItem?: (
    id: string,
    scheduledAt: string | null
  ) => Promise<void> | void
  onSteerItem?: (id: string) => Promise<void> | void
  steerChannel: "native" | "pull"
  /** Whether an insert from ANY row is in flight — every row's button is
   *  disabled for the duration, so two rows can't race the one channel. */
  steering: boolean
  onSteerStart: (id: string) => Promise<void>
  onScheduleOpen: (id: string) => void
  /** Manual send for a parked queue; absent when no override is available. */
  onSendNow?: (id: string) => Promise<void> | void
  /** True on an error pause: this manual send also lifts the pause, so the
   *  tooltip says what the click will do to the conversation. */
  resumeOnSend?: boolean
  /** The row whose manual send is in flight — every row's button is disabled
   *  for the duration, mirroring the insert's single-flight. */
  sendNowId: string | null
  onSendNowStart: (id: string) => Promise<void>
  /** 折叠组内相同拷贝数（>1 时渲染 xN 徽标）；未折叠行不传。 */
  count?: number
  /** 该行文本是「继续」指令且宿主可用：渲染 +1（再排一份相同消息）。 */
  onPlusOne?: () => void
}

function QueueItem({
  item,
  index,
  isEditing,
  onEdit,
  onDelete,
  onRetry,
  onScheduleItem,
  onSteerItem,
  steerChannel,
  steering,
  onSteerStart,
  onScheduleOpen,
  onSendNow,
  resumeOnSend,
  sendNowId,
  onSendNowStart,
  count,
  onPlusOne,
}: QueueItemProps) {
  const t = useTranslations("Folder.chat.messageQueue")
  const dragControls = useDragControls()

  // Which rows may offer the insert at all. Two rows can't, and offering a
  // click that provably does nothing is worse than offering none:
  // * The row under edit. Its authoritative text is in the composer now, so
  //   inserting would send the PRE-edit draft and then drop the row the edit
  //   was going to save into. (The composer hides its own mid-turn send while
  //   editing for the same reason.)
  // * A draft carrying attachments on a pull-tool session. The pull channel
  //   delivers text, so the backend rejects blocks there — every click would
  //   land on the turn-end fallback, which only moves the row to the front
  //   and never inserts it. It still goes out whole with the next turn.
  const claimed =
    item.deliveryState === "claimed" || item.deliveryState === "sending"
  const canSteer =
    !claimed &&
    Boolean(onSteerItem) &&
    !isEditing &&
    (steerChannel === "native" || !draftRidesBlocks(item.draft))
  // The held-queue manual send targets PENDING rows only — a pending row is
  // one with NO delivery state (the field is absent, never "pending") — while
  // failed/uncertain rows belong to the retry flow and claimed/sending rows
  // are already going.
  const canSendNow =
    Boolean(onSendNow) && !claimed && !isEditing && item.deliveryState == null

  const startDrag = useCallback(
    (event: PointerEvent<HTMLButtonElement>) => {
      event.preventDefault()
      event.stopPropagation()
      dragControls.start(event)
    },
    [dragControls]
  )

  return (
    <Reorder.Item
      as="div"
      value={item}
      dragListener={false}
      dragControls={dragControls}
      className={cn(
        "flex items-center gap-1 rounded-md border px-1.5 py-1 text-3xs leading-none select-none [text-box-trim:both] [text-box-edge:cap_alphabetic]",
        "bg-muted/40 border-border/70",
        isEditing && "border-primary/50 bg-primary/5"
      )}
    >
      <button
        type="button"
        className="shrink-0 cursor-grab touch-none active:cursor-grabbing p-0"
        onPointerDown={startDrag}
        disabled={claimed}
      >
        <GripVertical className="h-3 w-3 text-muted-foreground/60" />
      </button>
      <span className="shrink-0 font-mono text-3xs text-muted-foreground/70">
        #{index + 1}
      </span>
      <span className="min-w-0 flex-1 truncate text-3xs text-foreground/80">
        {item.draft.displayText}
      </span>
      {count != null && count > 1 && (
        // 相同 pending 拷贝的折叠计数：每发出一份 -1，最后一份发出后整组消失。
        <span
          className="shrink-0 font-mono text-3xs text-muted-foreground"
          title={item.draft.displayText}
        >
          x{count}
        </span>
      )}
      {item.scheduledAt && (
        // The raw ISO rides as the title: the short text is locale-rendered,
        // the tooltip is the exact instant the host will honor.
        <span
          className="shrink-0 text-3xs text-muted-foreground"
          title={item.scheduledAt}
        >
          {formatScheduleShort(item.scheduledAt)}
        </span>
      )}
      {item.lastError && (
        <span className="text-destructive text-3xs" title={item.lastError}>
          !
        </span>
      )}
      {claimed && <Clock className="h-2.5 w-2.5 text-muted-foreground" />}
      {onPlusOne && (
        <button
          type="button"
          onClick={onPlusOne}
          title={t("plusOne")}
          aria-label={t("plusOne")}
          className="shrink-0 rounded-sm px-0.5 font-mono text-3xs leading-none hover:bg-muted-foreground/15 text-muted-foreground"
        >
          +1
        </button>
      )}
      {onScheduleItem &&
        !claimed &&
        !isEditing &&
        item.deliveryState !== "failed" &&
        item.deliveryState !== "uncertain" && (
          <button
            type="button"
            onClick={() => onScheduleOpen(item.id)}
            title={t("scheduleItem")}
            className={cn(
              "shrink-0 rounded-sm p-0.5 hover:bg-muted-foreground/15 text-muted-foreground",
              item.scheduledAt && "text-primary"
            )}
          >
            <CalendarClock className="h-2.5 w-2.5" />
          </button>
        )}
      {onRetry &&
        (item.deliveryState === "failed" ||
          item.deliveryState === "uncertain") && (
          <button
            type="button"
            onClick={() => onRetry(item.id)}
            title={t("retryItem")}
            className="shrink-0 rounded-sm p-0.5 hover:bg-muted-foreground/15 text-muted-foreground"
          >
            <RotateCcw className="h-2.5 w-2.5" />
          </button>
        )}
      {canSteer && (
        <button
          type="button"
          onClick={() => void onSteerStart(item.id)}
          disabled={steering}
          className="shrink-0 rounded-sm p-0.5 hover:bg-muted-foreground/15 text-muted-foreground disabled:opacity-50"
          title={t(
            steerChannel === "pull" ? "steerItemAsNote" : "steerItemNow"
          )}
        >
          {steerChannel === "pull" ? (
            <Clock className="h-2.5 w-2.5" />
          ) : (
            <Zap className="h-2.5 w-2.5" />
          )}
        </button>
      )}
      {canSendNow && (
        <button
          type="button"
          onClick={() => void onSendNowStart(item.id)}
          disabled={sendNowId !== null}
          className="shrink-0 rounded-sm p-0.5 hover:bg-muted-foreground/15 text-muted-foreground disabled:opacity-50"
          title={t(resumeOnSend ? "sendNowResume" : "sendNow")}
        >
          <Send className="h-2.5 w-2.5" />
        </button>
      )}
      <button
        type="button"
        onClick={() => onEdit(item.id)}
        disabled={claimed}
        className="shrink-0 rounded-sm p-0.5 hover:bg-muted-foreground/15 text-muted-foreground"
        title={t("editItem")}
      >
        <Pencil className="h-2.5 w-2.5" />
      </button>
      <button
        type="button"
        onClick={() => onDelete(item.id)}
        disabled={claimed}
        className="shrink-0 rounded-sm p-0.5 hover:bg-muted-foreground/15 text-muted-foreground"
        title={t("deleteItem")}
      >
        <X className="h-2.5 w-2.5" />
      </button>
    </Reorder.Item>
  )
}

export function MessageQueueDisplay({
  queue,
  queueControl,
  onContinue,
  onReleaseHold,
  onSendNow,
  onQueuePlusOne,
  onReorder,
  onEdit,
  onDelete,
  onRetry,
  onScheduleItem,
  editingItemId,
  onSteerItem,
  steerChannel = "pull",
}: MessageQueueDisplayProps) {
  const t = useTranslations("Folder.chat.messageQueue")
  const [retryId, setRetryId] = useState<string | null>(null)
  const [continuing, setContinuing] = useState(false)
  const continuingRef = useRef(false)
  // The interruption banner is a *question*, not a modal gate. A user who
  // does not want to continue the failed task can close it like any other
  // notice and just keep typing — a direct send lifts the pause on its own
  // (see `handleSend`). Dismissing only hides the notice: the queue's pause
  // stays in place until a turn succeeds, so no parked row is silently sent.
  // Keyed on the failed attempt so a NEW failure raises the banner again.
  const [dismissedFailure, setDismissedFailure] = useState<string | null>(null)
  const failedAttemptId = queueControl?.failed_attempt_id ?? null
  // A paused row always carries a failed attempt, but the fallback keeps the
  // close button functional on a state written by an older host.
  const noticeKey = failedAttemptId ?? "paused"
  const noticeDismissed =
    dismissedFailure !== null && noticeKey === dismissedFailure
  // The row whose clock is open: its id plus the schedule it already carries
  // (seeds the picker and offers "back to ordinary").
  const [scheduleTarget, setScheduleTarget] = useState<{
    id: string
    initialAt: string | null
  } | null>(null)
  const [scheduling, setScheduling] = useState(false)
  // The banner is an interruption notice, not a progress display. Once the
  // recovery turn is admitted (`resuming`) the conversation itself shows the
  // running turn, so the banner goes away — a later failure re-raises it as
  // `paused`. `resume_requested` keeps it: nothing is visibly running yet and
  // the disabled "Continuing…" button is the click's only feedback.
  const paused =
    queueControl &&
    queueControl.status !== "open" &&
    queueControl.status !== "resuming" &&
    !noticeDismissed
  // A user-stop hold: the queue is "open" but parked. The banner is neutral
  // (nothing failed) and the rows gain a manual send.
  const held = queueControl?.status === "open" && queueControl.hold != null
  // An error pause gets the same manual override as a user-stop hold: each
  // PENDING row can be sent on its own, and that exact prompt lifts the
  // pause. Raw status, not `paused` — dismissing the notice must not take
  // away the rows' way out of the pause.
  const errorPaused = queueControl?.status === "paused"
  const manualSend = held || errorPaused
  const resumeRunning =
    continuing || queueControl?.status === "resume_requested"
  const continueTask = async () => {
    if (
      !onContinue ||
      continuingRef.current ||
      queueControl?.status !== "paused"
    )
      return
    continuingRef.current = true
    setContinuing(true)
    try {
      await onContinue()
    } finally {
      continuingRef.current = false
      setContinuing(false)
    }
  }
  // The id whose insert is in flight. A per-row `steering` would let a
  // concurrent click on another row race the same channel; one shared id
  // both disables the clicked row and (via `steeringId !== null`) the others
  // — matching the composer's single-flight `steering` guard.
  const [steeringId, setSteeringId] = useState<string | null>(null)
  // The click handler's re-entrancy lock. Taken in the click itself and
  // released in `finally`, so a second click that lands before React commits
  // the disabled buttons is still refused: the state above only drives
  // rendering and lags a commit.
  const steeringIdRef = useRef<string | null>(null)

  const handleSteerStart = useCallback(
    async (id: string) => {
      if (!onSteerItem || steeringIdRef.current !== null) return
      steeringIdRef.current = id
      setSteeringId(id)
      try {
        await onSteerItem(id)
      } finally {
        steeringIdRef.current = null
        setSteeringId(null)
      }
    },
    [onSteerItem]
  )

  // Release the user-stop hold. Same lock shape as the continue click: the
  // ref refuses a second click that lands before React commits the disabled
  // button.
  const [releasing, setReleasing] = useState(false)
  const releasingRef = useRef(false)
  const handleReleaseHold = useCallback(async () => {
    if (!onReleaseHold || releasingRef.current) return
    releasingRef.current = true
    setReleasing(true)
    try {
      await onReleaseHold()
    } finally {
      releasingRef.current = false
      setReleasing(false)
    }
  }, [onReleaseHold])

  // The manual send of one held-queue row. One shared in-flight id, same
  // single-flight rationale as the insert: the queue admits one turn at a
  // time, so a second concurrent click could only fail.
  const [sendNowId, setSendNowId] = useState<string | null>(null)
  const sendNowIdRef = useRef<string | null>(null)
  const handleSendNowStart = useCallback(
    async (id: string) => {
      if (!onSendNow || sendNowIdRef.current !== null) return
      sendNowIdRef.current = id
      setSendNowId(id)
      try {
        await onSendNow(id)
      } finally {
        sendNowIdRef.current = null
        setSendNowId(null)
      }
    },
    [onSendNow]
  )

  const confirmSchedule = useCallback(
    async (iso: string) => {
      if (!scheduleTarget || !onScheduleItem) return
      setScheduling(true)
      try {
        await onScheduleItem(scheduleTarget.id, iso)
        setScheduleTarget(null)
      } catch (error) {
        // The row keeps its current state; the dialog stays open with the
        // picked time so the user can correct rather than re-pick.
        toast.error(toErrorMessage(error))
      } finally {
        setScheduling(false)
      }
    },
    [scheduleTarget, onScheduleItem]
  )

  const clearSchedule = useCallback(async () => {
    if (!scheduleTarget || !onScheduleItem) return
    setScheduling(true)
    try {
      await onScheduleItem(scheduleTarget.id, null)
      setScheduleTarget(null)
    } catch (error) {
      toast.error(toErrorMessage(error))
    } finally {
      setScheduling(false)
    }
  }, [scheduleTarget, onScheduleItem])

  // 折叠是展示透镜：相同 pending 文本的行并成一组（xN 徽标），任何行都不消失。
  // 拖拽在组级别进行，展开回真实行序交给既有 onReorder。useMemo 保持组对象
  // 引用稳定 —— Reorder 依赖 values 的引用一致性。
  const displayGroups = useMemo(
    () => foldQueueForDisplay(queue, editingItemId),
    [queue, editingItemId]
  )
  const handleGroupReorder = useCallback(
    (groups: FoldedQueueGroup<QueuedMessage>[]) => {
      onReorder(expandFoldedGroups(groups))
    },
    [onReorder]
  )

  if (queue.length === 0 && !paused && !held) return null

  return (
    <div className="max-h-40 overflow-y-auto pb-1">
      {held && (
        <div className="mb-1 rounded-md border border-primary/30 bg-primary/5 p-2 text-xs">
          <p>{t("heldNotice")}</p>
          {onReleaseHold && (
            <button
              type="button"
              className="mt-2 rounded-md bg-primary px-2 py-1 text-primary-foreground disabled:opacity-50"
              disabled={releasing}
              onClick={() => void handleReleaseHold()}
            >
              {t("resumeQueue")}
            </button>
          )}
        </div>
      )}
      {paused && (
        <div className="relative mb-1 rounded-md border border-destructive/30 bg-destructive/5 p-2 text-xs">
          <button
            type="button"
            onClick={() => setDismissedFailure(noticeKey)}
            className="absolute right-1 top-1 rounded-sm p-0.5 text-muted-foreground hover:bg-muted-foreground/15 hover:text-foreground"
            aria-label={t("dismissNotice")}
            title={t("dismissNotice")}
          >
            <X className="h-3 w-3" />
          </button>
          <p className="pr-5">{t("unfinishedTitle")}</p>
          {queueControl.error && (
            <p className="mt-1 text-muted-foreground">{queueControl.error}</p>
          )}
          {onContinue && (
            <button
              type="button"
              className="mt-2 rounded-md bg-primary px-2 py-1 text-primary-foreground disabled:opacity-50"
              disabled={resumeRunning}
              onClick={() => void continueTask()}
            >
              {t(resumeRunning ? "continuingTask" : "continueTask")}
            </button>
          )}
        </div>
      )}
      <Reorder.Group
        as="div"
        axis="y"
        values={displayGroups}
        onReorder={handleGroupReorder}
        className="flex flex-col gap-0.5"
      >
        {displayGroups.map((group, index) => {
          // +1 只属于「继续」类 pending 行（判定与 Rust 侧报错自动放行同表）。
          const canPlusOne =
            Boolean(onQueuePlusOne) &&
            group.item.deliveryState == null &&
            !group.item.scheduledAt &&
            isContinueInstruction(group.item.draft.displayText)
          return (
            <QueueItem
              key={group.item.id}
              item={group.item}
              index={index}
              count={group.copies.length}
              onPlusOne={
                canPlusOne && onQueuePlusOne
                  ? () => onQueuePlusOne(group.item)
                  : undefined
              }
              isEditing={editingItemId === group.item.id}
              onEdit={onEdit}
              onDelete={onDelete}
              onRetry={onRetry ? setRetryId : undefined}
              onScheduleItem={onScheduleItem}
              onScheduleOpen={(id) =>
                setScheduleTarget({
                  id,
                  initialAt:
                    queue.find((entry) => entry.id === id)?.scheduledAt ?? null,
                })
              }
              onSteerItem={onSteerItem}
              steerChannel={steerChannel}
              steering={steeringId !== null}
              onSteerStart={handleSteerStart}
              onSendNow={manualSend ? onSendNow : undefined}
              resumeOnSend={errorPaused}
              sendNowId={sendNowId}
              onSendNowStart={handleSendNowStart}
            />
          )
        })}
      </Reorder.Group>
      <MessageScheduleDialog
        open={scheduleTarget !== null}
        onOpenChange={(open) => {
          if (!open) setScheduleTarget(null)
        }}
        initialAt={scheduleTarget?.initialAt ?? null}
        busy={scheduling}
        onConfirm={confirmSchedule}
        onClear={clearSchedule}
      />
      <AlertDialog
        open={retryId !== null}
        onOpenChange={(open) => {
          if (!open) setRetryId(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("retryItem")}</AlertDialogTitle>
            <AlertDialogDescription>{t("retryWarning")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("retryCancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (retryId && onRetry) void onRetry(retryId)
                setRetryId(null)
              }}
            >
              {t("retryConfirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
