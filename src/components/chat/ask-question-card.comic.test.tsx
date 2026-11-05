import { fireEvent, render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { describe, expect, it, vi } from "vitest"
import { AskQuestionCard } from "./ask-question-card"
import enMessages from "@/i18n/messages/en.json"

const transport = vi.hoisted(() => ({ call: vi.fn() }))
vi.mock("@/lib/transport", () => ({ getTransport: () => transport }))

const question = {
  question_id: "comic-choice",
  created_at: "2026-11-03T00:00:00Z",
  questions: [
    {
      id: "shot-choice",
      question: "Choose the frame",
      header: "Frame",
      multi_select: false,
      options: [
        {
          label: "Candidate 1",
          description: "First composition",
          image: {
            folder_id: 7,
            path: "episodes/ep001/shots/shot-0001/candidates/task-9-01.png",
          },
        },
        {
          label: "Candidate 2",
          description: "Second composition",
          image: {
            folder_id: 7,
            path: "episodes/ep001/shots/shot-0001/candidates/task-9-02.png",
          },
        },
      ],
    },
  ],
}

describe("comic candidates inside the conversation", () => {
  it("renders local thumbnails and submits the image's selected label", async () => {
    transport.call.mockResolvedValue({
      mime: "image/png",
      data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
    })
    const onAnswer = vi.fn()
    render(
      <NextIntlClientProvider locale="en" messages={enMessages}>
        <AskQuestionCard question={question} onAnswer={onAnswer} />
      </NextIntlClientProvider>
    )
    const second = await screen.findByRole("img", { name: "Candidate 2" })
    expect(second).toHaveAttribute(
      "src",
      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB"
    )
    expect(transport.call).toHaveBeenCalledWith("comic_read_image", {
      folderId: 7,
      path: "episodes/ep001/shots/shot-0001/candidates/task-9-02.png",
    })
    fireEvent.click(screen.getByRole("radio", { name: /Candidate 2/ }))
    fireEvent.click(screen.getByRole("button", { name: "Submit" }))
    expect(onAnswer).toHaveBeenCalledWith("comic-choice", {
      answers: [{ questionId: "shot-choice", labels: ["Candidate 2"] }],
      declined: false,
    })
  })
})
