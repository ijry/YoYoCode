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
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <MessageQueueDisplay
        queue={[item("q1", "use pnpm"), item("q2", "run the tests")]}
        onReorder={vi.fn()}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        editingItemId={null}
        {...props}
      />
    </NextIntlClientProvider>
  )
}

afterEach(cleanup)

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
        revision: 2,
      },
      onContinue: vi.fn(),
    })
    expect(
      screen.getByRole("button", { name: "Continuing unfinished task…" })
    ).toBeDisabled()
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
    expect(
      within(dialog).getByText(TQ.scheduleTitle)
    ).toBeInTheDocument()
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
      queue: [{ ...item("q1", "later"), scheduledAt: "2026-11-08T09:00:00+00:00" }],
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
