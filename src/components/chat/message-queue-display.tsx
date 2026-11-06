"use client"

import { useCallback, useRef, useState, type PointerEvent } from "react"
import { Reorder, useDragControls } from "motion/react"
import {
  CalendarClock,
  Clock,
  GripVertical,
  Pencil,
  RotateCcw,
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
import type { QueuedMessage } from "@/hooks/use-message-queue"
import type { MessageQueueState } from "@/lib/types"
import { MessageScheduleDialog } from "./message-schedule-dialog"

interface MessageQueueDisplayProps {
  queue: QueuedMessage[]
  queueControl?: MessageQueueState | null
  onContinue?: () => Promise<void> | void
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
}

interface QueueItemProps {
  item: QueuedMessage
  index: number
  isEditing: boolean
  onEdit: (id: string) => void
  onDelete: (id: string) => void
  onRetry?: (id: string) => void
  onScheduleItem?: (id: string, scheduledAt: string | null) => Promise<void> | void
  onSteerItem?: (id: string) => Promise<void> | void
  steerChannel: "native" | "pull"
  /** Whether an insert from ANY row is in flight — every row's button is
   *  disabled for the duration, so two rows can't race the one channel. */
  steering: boolean
  onSteerStart: (id: string) => Promise<void>
  onScheduleOpen: (id: string) => void
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
      {onScheduleItem && !claimed && !isEditing &&
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
  // The row whose clock is open: its id plus the schedule it already carries
  // (seeds the picker and offers "back to ordinary").
  const [scheduleTarget, setScheduleTarget] = useState<{
    id: string
    initialAt: string | null
  } | null>(null)
  const [scheduling, setScheduling] = useState(false)
  const paused = queueControl && queueControl.status !== "open"
  const resumeRunning =
    continuing ||
    queueControl?.status === "resuming" ||
    queueControl?.status === "resume_requested"
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

  if (queue.length === 0 && !paused) return null

  return (
    <div className="max-h-40 overflow-y-auto pb-1">
      {paused && (
        <div className="mb-1 rounded-md border border-destructive/30 bg-destructive/5 p-2 text-xs">
          <p>{t("unfinishedTitle")}</p>
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
        values={queue}
        onReorder={onReorder}
        className="flex flex-col gap-0.5"
      >
        {queue.map((item, index) => (
          <QueueItem
            key={item.id}
            item={item}
            index={index}
            isEditing={editingItemId === item.id}
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
          />
        ))}
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
