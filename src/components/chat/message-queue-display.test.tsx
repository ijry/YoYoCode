import {
  act,
  fireEvent,
  render,
  screen,
  cleanup,
  waitFor,
  within,
} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { afterEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"
import type { QueuedMessage } from "@/hooks/use-message-queue"
import type { MessageQueueState } from "@/lib/types"

// jsdom has no layout to drag against. Register the actual Reorder.Item values,
// then simulate a group reorder with Motion's values-membership contract. A
// QueueItem passing its message instead of its group cannot be reordered.
vi.mock("motion/react", async () => {
  const React = await import("react")
  const Registry = React.createContext<((value: unknown) => () => void) | null>(
    null
  )
  const Group = ({
    values,
    onReorder,
    children,
  }: {
    values: unknown[]
    onReorder: (next: unknown[]) => void
    children: React.ReactNode
  }) => {
    const items = React.useRef<unknown[]>([])
    const register = React.useCallback((value: unknown) => {
      items.current.push(value)
      return () => {
        items.current = items.current.filter((item) => item !== value)
      }
    }, [])
    return React.createElement(
      Registry.Provider,
      { value: register },
      React.createElement(
        "div",
        {
          "data-testid": "message-queue-reorder-group",
          onClick: (event: React.MouseEvent<HTMLDivElement>) => {
            if (event.target !== event.currentTarget) return
            const registered = values.filter((value) =>
              items.current.includes(value)
            )
            if (registered.length > 1) onReorder([...registered].reverse())
          },
        },
        children
      )
    )
  }
  const Item = ({
    value,
    children,
  }: {
    value: unknown
    children: React.ReactNode
  }) => {
    const register = React.useContext(Registry)
    React.useLayoutEffect(() => register?.(value), [register, value])
    return React.createElement("div", null, children)
  }
  return {
    Reorder: { Group, Item },
    useDragControls: () => ({ start: vi.fn() }),
  }
})

import { MessageQueueDisplay } from "./message-queue-display"

const TQ = enMessages.Folder.chat.messageQueue

function item(id: string, text: string): QueuedMessage {
  return {
    id,
    draft: { blocks: [{ type: "text", text }], displayText: text },
    modeId: null,
  }
}

/** A queued draft that carries an attachment — only the native wire takes it. */
function itemWithAttachment(id: string, text: string): QueuedMessage {
  return {
    id,
    draft: {
      blocks: [
        { type: "text", text },
        {
          type: "resource",
          uri: "clipboard://shot.png-1",
          mime_type: "image/png",
          text: null,
          blob: "QUJD",
        },
      ],
      displayText: text,
    },
    modeId: null,
  }
}

function renderDisplay(
  props: Partial<React.ComponentProps<typeof MessageQueueDisplay>> = {}
) {
  const display = (next: typeof props) => (
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <MessageQueueDisplay
        queue={[item("q1", "use pnpm"), item("q2", "run the tests")]}
        onReorder={vi.fn()}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        editingItemId={null}
        {...next}
      />
    </NextIntlClientProvider>
  )
  const view = render(display(props))
  return {
    ...view,
    rerenderDisplay: (next: typeof props) =>
      view.rerender(display({ ...props, ...next })),
  }
}

function queueState(patch: Partial<MessageQueueState> = {}): MessageQueueState {
  return {
    owner_id: "42",
    status: "open",
    failed_attempt_id: null,
    active_attempt_id: null,
    error: null,
    hold: null,
    revision: 1,
    ...patch,
  }
}

afterEach(cleanup)

describe("MessageQueueDisplay manual pause", () => {
  it("pauses automatically delivered rows and keeps single-row sending separate from resume", async () => {
    const onPauseQueue = vi.fn(async () => {})
    const onReleaseHold = vi.fn(async () => {})
    const onSendNow = vi.fn(async () => {})
    const view = renderDisplay({
      queueControl: queueState(),
      manualQueuePause: true,
      onPauseQueue,
      onReleaseHold,
      onSendNow,
    })
    await userEvent.click(screen.getByRole("button", { name: TQ.pauseQueue }))
    expect(onPauseQueue).toHaveBeenCalledTimes(1)

    view.rerenderDisplay({
      queueControl: queueState({ hold: "manual_pause", revision: 2 }),
    })
    expect(
      screen.queryByRole("button", { name: TQ.pauseQueue })
    ).not.toBeInTheDocument()
    expect(screen.getByText(TQ.manualPauseNotice)).toBeInTheDocument()
    await userEvent.click(screen.getAllByTitle(TQ.sendNow)[1])
    expect(onSendNow).toHaveBeenCalledWith("q2")
    expect(onReleaseHold).not.toHaveBeenCalled()
    expect(
      screen.getByRole("button", { name: TQ.resumeQueue })
    ).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: TQ.resumeQueue }))
    expect(onReleaseHold).toHaveBeenCalledTimes(1)
  })

  it.each([
    { manualQueuePause: false, queueControl: queueState() },
    { manualQueuePause: true, queueControl: null },
    {
      manualQueuePause: true,
      queueControl: queueState({ hold: "manual_stop" }),
    },
    { manualQueuePause: true, queueControl: queueState({ status: "paused" }) },
    { manualQueuePause: true, queueControl: queueState(), queue: [] },
  ])("does not offer an unsupported or inapplicable pause: %j", (props) => {
    renderDisplay({ onPauseQueue: vi.fn(), ...props })
    expect(
      screen.queryByRole("button", { name: TQ.pauseQueue })
    ).not.toBeInTheDocument()
  })

  it.each(["open", "paused", "resuming", "resume_requested"] as const)(
    "keeps explicit resume accessible when manually held, empty and running (%s)",
    async (status) => {
      const onReleaseHold = vi.fn(async () => {})
      renderDisplay({
        queue: [],
        queueControl: queueState({ status, hold: "manual_pause" }),
        sessionRunning: true,
        onReleaseHold,
      })
      expect(screen.getByText(TQ.manualPauseNotice)).toBeInTheDocument()
      // Manual pause is persistent, unlike the dismissible user-stop notice.
      const resume = screen.getByRole("button", { name: TQ.resumeQueue })
      await userEvent.click(resume)
      expect(onReleaseHold).toHaveBeenCalledTimes(1)
      expect(
        screen.getByRole("button", { name: TQ.resumeQueue })
      ).toBeInTheDocument()
    }
  )

  it("does not lose resume when the error notice is dismissed", async () => {
    renderDisplay({
      queueControl: queueState({
        status: "paused",
        hold: "manual_pause",
        error: "quota error",
      }),
      onReleaseHold: vi.fn(),
    })
    await userEvent.click(
      screen.getByRole("button", { name: TQ.dismissNotice })
    )
    expect(
      screen.getByRole("button", { name: TQ.resumeQueue })
    ).toBeInTheDocument()
  })

  it("refuses repeated pause clicks while the host request is pending", async () => {
    let resolve!: () => void
    const onPauseQueue = vi.fn(
      () =>
        new Promise<void>((done) => {
          resolve = done
        })
    )
    renderDisplay({
      queueControl: queueState(),
      manualQueuePause: true,
      onPauseQueue,
    })
    const button = screen.getByRole("button", { name: TQ.pauseQueue })
    fireEvent.click(button)
    fireEvent.click(button)
    expect(onPauseQueue).toHaveBeenCalledTimes(1)
    expect(screen.getByRole("button", { name: TQ.pausingQueue })).toBeDisabled()
    await act(async () => resolve())
    expect(screen.getByRole("button", { name: TQ.pauseQueue })).toBeEnabled()
  })
})

describe("MessageQueueDisplay reorder", () => {
  it("expands reordered folded groups back to the real queue", () => {
    const onReorder = vi.fn()
    renderDisplay({
      queue: [item("q1", "same"), item("q2", "other"), item("q3", "same")],
      onReorder,
    })
    fireEvent.click(screen.getByTestId("message-queue-reorder-group"))
    expect(onReorder).toHaveBeenCalledWith([
      expect.objectContaining({ id: "q2" }),
      expect.objectContaining({ id: "q1" }),
      expect.objectContaining({ id: "q3" }),
    ])
  })
})

describe("MessageQueueDisplay click-to-insert", () => {
  it("shows the unfinished-task control even when no queue rows remain", async () => {
    const onContinue = vi.fn(async () => {})
    renderDisplay({
      queue: [],
      queueControl: {
        owner_id: "42",
        status: "paused",
        failed_attempt_id: "a",
        active_attempt_id: null,
        error: "quota error",
        hold: null,
        revision: 1,
      },
      onContinue,
    })
    expect(screen.getByText("quota error")).toBeInTheDocument()
    await userEvent.click(
      screen.getByRole("button", { name: "Continue unfinished task" })
    )
    expect(onContinue).toHaveBeenCalledTimes(1)
  })

  it("lets the user close the notice and raises it again for a NEW failure", async () => {
    const control = {
      owner_id: "42",
      status: "paused" as const,
      failed_attempt_id: "attempt-a",
      active_attempt_id: null,
      error: "quota error",
      hold: null,
      revision: 1,
    }
    const Harness = ({ state }: { state: typeof control }) => (
      <NextIntlClientProvider locale="en" messages={enMessages}>
        <MessageQueueDisplay
          queue={[item("q1", "use pnpm")]}
          queueControl={state}
          onContinue={vi.fn()}
          onReorder={vi.fn()}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          editingItemId={null}
        />
      </NextIntlClientProvider>
    )
    const view = render(<Harness state={control} />)
    expect(screen.getByText(TQ.unfinishedTitle)).toBeInTheDocument()
    // Closing is local to the notice: the paused queue rows are untouched.
    await userEvent.click(
      screen.getByRole("button", { name: TQ.dismissNotice })
    )
    expect(screen.queryByText(TQ.unfinishedTitle)).toBeNull()
    expect(screen.getByText("use pnpm")).toBeInTheDocument()

    view.rerender(
      <Harness
        state={{ ...control, failed_attempt_id: "attempt-b", revision: 2 }}
      />
    )
    expect(screen.getByText(TQ.unfinishedTitle)).toBeInTheDocument()
  })

  it("hides the banner once the recovery turn is actually running", () => {
    // The conversation itself shows the running turn; keeping the banner up
    // would only duplicate it. A later failure re-raises it as `paused`.
    renderDisplay({
      queue: [],
      queueControl: {
        owner_id: "42",
        status: "resuming",
        failed_attempt_id: "a",
        active_attempt_id: "r",
        error: "quota error",
        hold: null,
        revision: 2,
      },
      onContinue: vi.fn(),
    })
    expect(screen.queryByText("quota error")).toBeNull()
    expect(
      screen.queryByRole("button", { name: "Continuing unfinished task…" })
    ).toBeNull()
  })

  it("prevents another continuation while recovery is requested but not yet admitted", () => {
    renderDisplay({
      queue: [],
      queueControl: {
        owner_id: "42",
        status: "resume_requested",
        failed_attempt_id: "a",
        active_attempt_id: null,
        error: "quota error",
        hold: null,
        revision: 2,
      },
      onContinue: vi.fn(),
    })
    expect(
      screen.getByRole("button", { name: "Continuing unfinished task…" })
    ).toBeDisabled()
  })

  it("shows the held notice with a release button and per-row send buttons", async () => {
    const onReleaseHold = vi.fn(async () => {})
    const onSendNow = vi.fn(async () => {})
    renderDisplay({
      queueControl: {
        owner_id: "42",
        status: "open",
        failed_attempt_id: null,
        active_attempt_id: null,
        error: null,
        hold: "manual_stop",
        revision: 3,
      },
      onReleaseHold,
      onSendNow,
    })
    expect(
      screen.getByText(
        "Turn stopped manually — the queue is holding. Send rows one by one or resume auto-send."
      )
    ).toBeInTheDocument()
    // Rows gain the manual send; releasing is offered too.
    await userEvent.click(screen.getAllByTitle("Send now")[0])
    expect(onSendNow).toHaveBeenCalledWith("q1")
    await userEvent.click(
      screen.getByRole("button", { name: "Resume auto-send" })
    )
    expect(onReleaseHold).toHaveBeenCalledTimes(1)
  })

  it("hides the held notice once a fresh turn is running, without touching the queue", () => {
    renderDisplay({
      sessionRunning: true,
      queueControl: {
        owner_id: "42",
        status: "open",
        failed_attempt_id: null,
        active_attempt_id: null,
        error: null,
        hold: "manual_stop",
        revision: 3,
      },
      onReleaseHold: vi.fn(async () => {}),
      onSendNow: vi.fn(async () => {}),
    })
    // The conversation itself shows the running turn — no stale "stopped" banner.
    expect(screen.queryByText(TQ.heldNotice)).toBeNull()
    expect(screen.queryByRole("button", { name: TQ.resumeQueue })).toBeNull()
    // The hold still applies: the rows remain and keep their manual send.
    expect(screen.getAllByTitle(TQ.sendNow)).toHaveLength(2)
  })

  it("lets the held notice be closed by hand and re-arms on a NEW hold", async () => {
    const control = {
      owner_id: "42",
      status: "open" as const,
      failed_attempt_id: null,
      active_attempt_id: null,
      error: null,
      hold: "manual_stop" as string | null,
      revision: 3,
    }
    const Harness = ({ state }: { state: typeof control }) => (
      <NextIntlClientProvider locale="en" messages={enMessages}>
        <MessageQueueDisplay
          queue={[item("q1", "use pnpm")]}
          queueControl={state}
          onReleaseHold={vi.fn()}
          onSendNow={vi.fn(async () => {})}
          onReorder={vi.fn()}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          editingItemId={null}
        />
      </NextIntlClientProvider>
    )
    const view = render(<Harness state={control} />)
    expect(screen.getByText(TQ.heldNotice)).toBeInTheDocument()
    // Closing only hides the notice: the queue is still held, so its row keeps
    // the manual send and no auto-drain was silently resumed.
    await userEvent.click(
      screen.getByRole("button", { name: TQ.dismissNotice })
    )
    expect(screen.queryByText(TQ.heldNotice)).toBeNull()
    expect(screen.getByTitle(TQ.sendNow)).toBeInTheDocument()

    // The hold clears on a successful turn, then a NEW stop raises it again.
    view.rerender(<Harness state={{ ...control, hold: null, revision: 4 }} />)
    expect(screen.queryByText(TQ.heldNotice)).toBeNull()
    view.rerender(<Harness state={{ ...control, revision: 5 }} />)
    expect(screen.getByText(TQ.heldNotice)).toBeInTheDocument()
  })

  it("lets an error-paused queue resume from ANY pending row, even with the notice closed", async () => {
    const onSendNow = vi.fn(async () => {})
    renderDisplay({
      queue: [item("q1", "first"), item("q2", "second")],
      queueControl: {
        owner_id: "42",
        status: "paused",
        failed_attempt_id: "attempt-a",
        active_attempt_id: null,
        error: "quota error",
        hold: null,
        revision: 1,
      },
      onContinue: vi.fn(),
      onSendNow,
    })
    const buttons = screen.getAllByTitle(TQ.sendNowResume)
    expect(buttons).toHaveLength(2)
    // The user picks the SECOND row: that message is what resumes the session.
    await userEvent.click(buttons[1])
    expect(onSendNow).toHaveBeenCalledWith("q2")

    await userEvent.click(
      screen.getByRole("button", { name: TQ.dismissNotice })
    )
    expect(screen.queryByText(TQ.unfinishedTitle)).toBeNull()
    expect(screen.getAllByTitle(TQ.sendNowResume)).toHaveLength(2)
  })

  it("keeps manual sends off while a recovery turn owns the session", () => {
    renderDisplay({
      queueControl: {
        owner_id: "42",
        status: "resuming",
        failed_attempt_id: "attempt-a",
        active_attempt_id: "r",
        error: null,
        hold: null,
        revision: 2,
      },
      onContinue: vi.fn(),
      onSendNow: vi.fn(async () => {}),
    })
    expect(screen.queryByTitle(TQ.sendNow)).toBeNull()
    expect(screen.queryByTitle(TQ.sendNowResume)).toBeNull()
  })

  it("offers no held banner or send buttons while the queue drains normally", () => {
    renderDisplay({
      queueControl: {
        owner_id: "42",
        status: "open",
        failed_attempt_id: null,
        active_attempt_id: null,
        error: null,
        hold: null,
        revision: 1,
      },
      onReleaseHold: vi.fn(async () => {}),
      onSendNow: vi.fn(async () => {}),
    })
    expect(
      screen.queryByRole("button", { name: "Resume auto-send" })
    ).toBeNull()
    expect(screen.queryByTitle("Send now")).toBeNull()
  })

  it("withholds the manual send from failed rows and the row under edit", () => {
    renderDisplay({
      queue: [
        item("q1", "pending A"),
        { ...item("q2", "failed B"), deliveryState: "failed" },
      ],
      queueControl: {
        owner_id: "42",
        status: "open",
        failed_attempt_id: null,
        active_attempt_id: null,
        error: null,
        hold: "manual_stop",
        revision: 3,
      },
      onSendNow: vi.fn(async () => {}),
      editingItemId: "q1",
    })
    // q1 is under edit (its content lives in the composer), q2 is a retry case
    // — only rows that are neither offer the manual send.
    expect(screen.queryByTitle("Send now")).toBeNull()
  })

  it("does not offer edit, delete or steering for a backend-owned claim", () => {
    renderDisplay({
      queue: [{ ...item("q1", "A"), deliveryState: "claimed" }],
      onSteerItem: vi.fn(async () => {}),
      steerChannel: "native",
    })
    expect(screen.queryByTitle(TQ.steerItemNow)).toBeNull()
    expect(screen.getByTitle(TQ.editItem)).toBeDisabled()
    expect(screen.getByTitle(TQ.deleteItem)).toBeDisabled()
  })

  it("keeps an uncertain draft visible with its error rather than showing it as pending", () => {
    renderDisplay({
      queue: [
        {
          ...item("q1", "A"),
          deliveryState: "uncertain",
          lastError: "delivery not confirmed",
        },
      ],
    })
    expect(screen.getByText("A")).toBeInTheDocument()
    expect(screen.getByTitle("delivery not confirmed")).toBeInTheDocument()
  })

  it("asks before retrying uncertain delivery to avoid an accidental duplicate", async () => {
    const onRetry = vi.fn(async () => {})
    renderDisplay({
      queue: [
        {
          ...item("q1", "A"),
          deliveryState: "uncertain",
          lastError: "unknown delivery",
        },
      ],
      onRetry,
    })
    await userEvent.click(screen.getByTitle("Retry queued message"))
    expect(onRetry).not.toHaveBeenCalled()
    expect(screen.getByRole("alertdialog")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "Retry" }))
    expect(onRetry).toHaveBeenCalledWith("q1")
  })

  it("offers no insert button without a steering handler", () => {
    renderDisplay()
    expect(screen.queryByTitle(TQ.steerItemNow)).toBeNull()
    expect(screen.queryByTitle(TQ.steerItemAsNote)).toBeNull()
  })

  it("inserts the row the button belongs to, with the native promise", async () => {
    const onSteerItem = vi.fn(async () => {})
    renderDisplay({ onSteerItem, steerChannel: "native" })

    const buttons = screen.getAllByTitle(TQ.steerItemNow)
    expect(buttons).toHaveLength(2)

    await userEvent.click(buttons[1])
    expect(onSteerItem).toHaveBeenCalledWith("q2")
  })

  it("keys the copy to the pull channel (waiting note, not an insert)", async () => {
    const onSteerItem = vi.fn(async () => {})
    renderDisplay({ onSteerItem, steerChannel: "pull" })

    // The insert label must not appear on a pull session — it would promise
    // an instant injection the channel can't deliver.
    expect(screen.queryByTitle(TQ.steerItemNow)).toBeNull()
    await userEvent.click(screen.getAllByTitle(TQ.steerItemAsNote)[0])
    expect(onSteerItem).toHaveBeenCalledWith("q1")
  })

  it("disables every row while an insert is in flight (single-flight)", async () => {
    let release: () => void = () => {}
    const onSteerItem = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          release = resolve
        })
    )
    renderDisplay({ onSteerItem, steerChannel: "native" })

    const buttons = screen.getAllByTitle(TQ.steerItemNow) as HTMLButtonElement[]
    await userEvent.click(buttons[0])
    await waitFor(() => expect(onSteerItem).toHaveBeenCalledTimes(1))

    expect(buttons[1].disabled).toBe(true)
    // A second click while in flight must not race the same channel.
    await userEvent.click(buttons[1])
    expect(onSteerItem).toHaveBeenCalledTimes(1)

    release()
    await waitFor(() => expect(buttons[1].disabled).toBe(false))
  })

  it("admits only one insertion before React commits the disabled buttons", async () => {
    let release!: () => void
    const onSteerItem = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          release = resolve
        })
    )
    renderDisplay({ onSteerItem, steerChannel: "native" })
    const buttons = screen.getAllByTitle(TQ.steerItemNow)
    act(() => {
      fireEvent.click(buttons[0])
      fireEvent.click(buttons[1])
    })
    expect(onSteerItem).toHaveBeenCalledTimes(1)
    await act(async () => {
      release()
    })
  })

  it("hides the note button on a pull row the channel cannot carry", () => {
    // The pull tool delivers text only, so the backend rejects a draft with
    // attachments there — the click could never insert anything. The row is
    // still sent whole (attachment included) by the queue's own flush.
    renderDisplay({
      queue: [item("q1", "use pnpm"), itemWithAttachment("q2", "look at this")],
      onSteerItem: vi.fn(async () => {}),
      steerChannel: "pull",
    })
    expect(screen.getAllByTitle(TQ.steerItemAsNote)).toHaveLength(1)
  })

  it("keeps the attachment row insertable on the native wire", () => {
    renderDisplay({
      queue: [item("q1", "use pnpm"), itemWithAttachment("q2", "look at this")],
      onSteerItem: vi.fn(async () => {}),
      steerChannel: "native",
    })
    expect(screen.getAllByTitle(TQ.steerItemNow)).toHaveLength(2)
  })

  it("hides the insert on the row being edited (the composer owns its text)", () => {
    // While a row is under edit its real content lives in the composer;
    // inserting would send the stale pre-edit draft and drop the row the save
    // was headed for.
    renderDisplay({
      onSteerItem: vi.fn(async () => {}),
      steerChannel: "native",
      editingItemId: "q1",
    })
    expect(screen.getAllByTitle(TQ.steerItemNow)).toHaveLength(1)
  })
})

describe("MessageQueueDisplay scheduled sends", () => {
  it("schedules an ordinary row through the dialog at the picked instant", async () => {
    const onScheduleItem = vi.fn(async () => {})
    renderDisplay({ onScheduleItem })

    await userEvent.click(screen.getAllByTitle(TQ.scheduleItem)[0])
    const dialog = screen.getByRole("dialog")
    expect(within(dialog).getByText(TQ.scheduleTitle)).toBeInTheDocument()
    // Nothing picked yet — no instant, no confirm.
    expect(
      within(dialog).getByRole("button", { name: TQ.scheduleConfirm })
    ).toBeDisabled()

    await userEvent.click(
      within(dialog).getByRole("button", { name: TQ.preset30m })
    )
    await userEvent.click(
      within(dialog).getByRole("button", { name: TQ.scheduleConfirm })
    )
    await waitFor(() =>
      expect(onScheduleItem).toHaveBeenCalledWith("q1", expect.any(String))
    )
  })

  it("shows a scheduled row's time and can put it back into the ordinary queue", async () => {
    const onScheduleItem = vi.fn(async () => {})
    renderDisplay({
      queue: [
        { ...item("q1", "later"), scheduledAt: "2026-11-08T09:00:00+00:00" },
      ],
      onScheduleItem,
    })
    expect(screen.getByTitle("2026-11-08T09:00:00+00:00")).toBeInTheDocument()

    await userEvent.click(screen.getByTitle(TQ.scheduleItem))
    const dialog = screen.getByRole("dialog")
    // The existing plan seeds the fields — modifying, not starting over.
    expect(
      within(dialog).getByRole("button", { name: TQ.scheduleClear })
    ).toBeInTheDocument()
    await userEvent.click(
      within(dialog).getByRole("button", { name: TQ.scheduleClear })
    )
    await waitFor(() => expect(onScheduleItem).toHaveBeenCalledWith("q1", null))
  })

  it("keeps the dialog open and the row intact when the host rejects the schedule", async () => {
    const onScheduleItem = vi.fn(async () => {
      throw new Error("only pending items can be scheduled")
    })
    renderDisplay({ onScheduleItem })
    await userEvent.click(screen.getAllByTitle(TQ.scheduleItem)[0])
    const dialog = screen.getByRole("dialog")
    await userEvent.click(
      within(dialog).getByRole("button", { name: TQ.preset30m })
    )
    await userEvent.click(
      within(dialog).getByRole("button", { name: TQ.scheduleConfirm })
    )
    await waitFor(() => expect(onScheduleItem).toHaveBeenCalledTimes(1))
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })

  it("offers no clock without a schedule handler or on non-pending rows", () => {
    renderDisplay({
      queue: [
        { ...item("q1", "in flight"), deliveryState: "claimed" },
        { ...item("q2", "failed"), deliveryState: "failed" },
      ],
      onScheduleItem: vi.fn(async () => {}),
    })
    expect(screen.queryByTitle(TQ.scheduleItem)).toBeNull()
  })
})

describe("MessageQueueDisplay +1 fold", () => {
  const plusOne = vi.fn()

  afterEach(() => {
    plusOne.mockClear()
  })

  it("folds identical pending rows into one row with an xN badge", () => {
    renderDisplay({
      queue: [
        item("q1", "继续"),
        item("q2", "use pnpm"),
        item("q3", "继续"),
        item("q4", "继续"),
      ],
    })

    // 三份相同 pending 折叠成一行，按首现位置展示计数徽标。
    expect(screen.getByText("x3")).toBeInTheDocument()
    expect(screen.queryByText("x2")).not.toBeInTheDocument()
    expect(screen.getAllByText("继续")).toHaveLength(1)
    expect(screen.getByText("use pnpm")).toBeInTheDocument()
  })

  it("offers +1 only on continue-type pending rows and enqueues a copy", async () => {
    renderDisplay({
      queue: [item("q1", "继续"), item("q2", "use pnpm")],
      onQueuePlusOne: plusOne,
    })

    const plusButtons = screen.getAllByRole("button", { name: TQ.plusOne })
    expect(plusButtons).toHaveLength(1)
    await userEvent.click(plusButtons[0])
    expect(plusOne).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ id: "q1" })
    )
  })

  it("keeps the count honest after one copy drains", () => {
    const { rerender } = renderDisplay({
      queue: [item("q1", "继续"), item("q3", "继续"), item("q4", "继续")],
      onQueuePlusOne: plusOne,
    })
    expect(screen.getByText("x3")).toBeInTheDocument()

    // q1 被 claim 发出（离开 pending 集合）：x3 → x2，组仍在。
    rerender(
      <NextIntlClientProvider locale="en" messages={enMessages}>
        <MessageQueueDisplay
          queue={[item("q3", "继续"), item("q4", "继续")]}
          onQueuePlusOne={plusOne}
          onReorder={vi.fn()}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          editingItemId={null}
        />
      </NextIntlClientProvider>
    )
    expect(screen.getByText("x2")).toBeInTheDocument()
    expect(screen.queryByText("x3")).not.toBeInTheDocument()
  })

  it("does not fold rows that carry their own state UI", () => {
    const failedRow = {
      ...item("qf", "继续"),
      deliveryState: "failed" as const,
    }
    const scheduledRow = {
      ...item("qs", "继续"),
      scheduledAt: "2026-12-01T00:00:00Z",
    }
    renderDisplay({
      queue: [item("q1", "继续"), failedRow, scheduledRow],
      onQueuePlusOne: plusOne,
    })

    // pending 的那行折叠成 x1（无徽标）；failed / 定时行各自独立渲染。
    expect(screen.getAllByText("继续")).toHaveLength(3)
    expect(screen.queryByText("x2")).not.toBeInTheDocument()
    expect(screen.getByTitle("2026-12-01T00:00:00Z")).toBeInTheDocument()
  })

  it("hides +1 when the handler is absent or the text is not a continue instruction", () => {
    renderDisplay({ queue: [item("q1", "继续"), item("q2", "use pnpm")] })
    expect(
      screen.queryByRole("button", { name: TQ.plusOne })
    ).not.toBeInTheDocument()
  })

  it("leaves the row under edit unfolded so its edit UI stays visible", () => {
    renderDisplay({
      queue: [item("q1", "继续"), item("q2", "继续")],
      editingItemId: "q1",
      onQueuePlusOne: plusOne,
    })

    expect(screen.getAllByText("继续")).toHaveLength(2)
    expect(screen.queryByText("x2")).not.toBeInTheDocument()
  })
})
