import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { afterEach, expect, it, vi } from "vitest"
import enMessages from "@/i18n/messages/en.json"
import { ComposerAddMenu, type QueueControls } from "./composer-add-menu"

afterEach(cleanup)
const t = enMessages.Folder.chat.messageQueue
const mount = (queueControls?: QueueControls, disabled = false) =>
  render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ComposerAddMenu queueControls={queueControls} disabled={disabled} />
    </NextIntlClientProvider>
  )
async function openControls() {
  await userEvent.click(
    screen.getByRole("button", {
      name: enMessages.Folder.chat.messageInput.addActions,
    })
  )
  await userEvent.hover(screen.getByRole("menuitem", { name: t.queueControls }))
}

it("keeps whole-queue resume reachable while the composer is disconnected", async () => {
  const onResume = vi.fn(async () => {})
  mount({ canPause: false, held: true, onResume }, true)
  await openControls()
  fireEvent.click(await screen.findByRole("menuitem", { name: t.resumeQueue }))
  expect(onResume).toHaveBeenCalledTimes(1)
})

it("locks duplicate whole-queue pause requests", async () => {
  let resolve!: () => void
  const onPause = vi.fn(
    () =>
      new Promise<void>((done) => {
        resolve = done
      })
  )
  mount({ canPause: true, held: false, onPause })
  await openControls()
  const pause = await screen.findByRole("menuitem", { name: t.pauseQueue })
  fireEvent.click(pause)
  fireEvent.click(pause)
  expect(onPause).toHaveBeenCalledTimes(1)
  expect(pause).toHaveAttribute("aria-disabled", "true")
  await act(async () => resolve())
  expect(pause).not.toHaveAttribute("aria-disabled", "true")
})

it("does not offer queue controls without host support", async () => {
  mount()
  await userEvent.click(
    screen.getByRole("button", {
      name: enMessages.Folder.chat.messageInput.addActions,
    })
  )
  expect(
    screen.queryByRole("menuitem", { name: t.queueControls })
  ).not.toBeInTheDocument()
})

it("disables pause for an empty or recovering queue", async () => {
  mount({ canPause: false, held: false, onPause: vi.fn() })
  await openControls()
  expect(
    await screen.findByRole("menuitem", { name: t.pauseQueue })
  ).toHaveAttribute("aria-disabled", "true")
})
