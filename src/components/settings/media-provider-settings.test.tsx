import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { MediaProviderSettings } from "./media-provider-settings"
import messages from "@/i18n/messages/en.json"

const api = vi.hoisted(() => ({
  listMediaProviders: vi.fn(),
  createMediaProvider: vi.fn(),
  updateMediaProvider: vi.fn(),
  deleteMediaProvider: vi.fn(),
  listProviderModels: vi.fn(),
}))
vi.mock("@/lib/comic", () => ({ comicApi: api }))

beforeEach(() => {
  vi.clearAllMocks()
  api.listMediaProviders.mockResolvedValue([])
  api.createMediaProvider.mockResolvedValue({ id: 8 })
})

describe("media provider settings", () => {
  it("saves a reusable image endpoint without making a paid generation call", async () => {
    render(
      <NextIntlClientProvider locale="en" messages={messages}>
        <MediaProviderSettings />
      </NextIntlClientProvider>
    )
    fireEvent.click(await screen.findByRole("button", { name: "Add provider" }))
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "My image provider" },
    })
    fireEvent.change(screen.getByLabelText("API URL"), {
      target: { value: "https://images.example/v1" },
    })
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "secret-123" },
    })
    fireEvent.change(screen.getByLabelText("Model"), {
      target: { value: "gpt-image-1" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(api.createMediaProvider).toHaveBeenCalledWith(
        expect.objectContaining({
          kind: "image",
          name: "My image provider",
          apiUrl: "https://images.example/v1",
          apiKey: "secret-123",
          model: "gpt-image-1",
        })
      )
    )
  })

  it("associates model guidance with video providers without selecting a model", async () => {
    render(
      <NextIntlClientProvider locale="en" messages={messages}>
        <MediaProviderSettings />
      </NextIntlClientProvider>
    )
    fireEvent.click(await screen.findByRole("button", { name: "Add provider" }))
    const model = screen.getByLabelText("Model")
    expect(model).not.toHaveAttribute("aria-describedby")
    fireEvent.change(screen.getByLabelText("Media type"), {
      target: { value: "video" },
    })
    expect(model).toHaveAccessibleDescription(/Seedance.*Ark/)
    expect(model).toHaveValue("")
    fireEvent.change(model, { target: { value: "my-seedance-model" } })
    fireEvent.change(screen.getByLabelText("Media type"), {
      target: { value: "image" },
    })
    expect(model).not.toHaveAttribute("aria-describedby")
    expect(model).toHaveValue("my-seedance-model")
  })

  it("selects the new-api protocol for a gateway and saves it without hand-written JSON", async () => {
    render(
      <NextIntlClientProvider locale="en" messages={messages}>
        <MediaProviderSettings />
      </NextIntlClientProvider>
    )
    fireEvent.click(await screen.findByRole("button", { name: "Add provider" }))
    fireEvent.change(screen.getByLabelText("Media type"), {
      target: { value: "video" },
    })
    // Gateway guidance stays hidden until the new-api protocol is chosen.
    expect(
      screen.queryByText(/choose the new-api gateway protocol above/i)
    ).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("Adapter"), {
      target: { value: "newapi" },
    })
    expect(
      screen.getByText(/choose the new-api gateway protocol above/i)
    ).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "ai-switch video gateway" },
    })
    fireEvent.change(screen.getByLabelText("API URL"), {
      target: { value: "http://127.0.0.1:8080/v1" },
    })
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "gateway-key" },
    })
    fireEvent.change(screen.getByLabelText("Model"), {
      target: { value: "seedance-via-gateway" },
    })
    fireEvent.change(screen.getByLabelText("Adapter configuration (JSON)"), {
      target: {
        value: '{"capabilities":{"minDurationSecs":5,"maxDurationSecs":10}}',
      },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(api.createMediaProvider).toHaveBeenCalled())
    const saved = api.createMediaProvider.mock.calls[0][0]
    expect(saved.kind).toBe("video")
    expect(saved.apiUrl).toBe("http://127.0.0.1:8080/v1")
    expect(JSON.parse(saved.extraJson)).toEqual({
      capabilities: { minDurationSecs: 5, maxDurationSecs: 10 },
      protocol: "newapi",
    })
  })

  it("fetches model ids from the endpoint for the model field", async () => {
    api.listProviderModels.mockResolvedValue({
      models: ["seedance-2-5", "a-model"],
    })
    render(
      <NextIntlClientProvider locale="en" messages={messages}>
        <MediaProviderSettings />
      </NextIntlClientProvider>
    )
    fireEvent.click(await screen.findByRole("button", { name: "Add provider" }))
    const fetchButton = screen.getByRole("button", { name: "Fetch models" })
    // Needs an endpoint and a key before it will talk to anything.
    expect(fetchButton).toBeDisabled()
    fireEvent.change(screen.getByLabelText("API URL"), {
      target: { value: "https://relay.example/v1" },
    })
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "relay-key" },
    })
    expect(screen.getByLabelText("Model")).toHaveValue("")
    fireEvent.click(screen.getByRole("button", { name: "Fetch models" }))
    await waitFor(() =>
      expect(api.listProviderModels).toHaveBeenCalledWith({
        kind: "image",
        apiUrl: "https://relay.example/v1",
        apiKey: "relay-key",
        extraJson: "{}",
      })
    )
    const options = Array.from(
      document.querySelectorAll<HTMLUListElement | HTMLOptionElement>(
        "#media-model-options option"
      )
    ).map((option) => option.getAttribute("value"))
    expect(options).toEqual(["seedance-2-5", "a-model"])
  })

  it("leaves an unspecified video protocol to the backend default instead of forcing one", async () => {
    render(
      <NextIntlClientProvider locale="en" messages={messages}>
        <MediaProviderSettings />
      </NextIntlClientProvider>
    )
    fireEvent.click(await screen.findByRole("button", { name: "Add provider" }))
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Seedance relay" },
    })
    fireEvent.change(screen.getByLabelText("Media type"), {
      target: { value: "video" },
    })
    fireEvent.change(screen.getByLabelText("API URL"), {
      target: { value: "https://ark.example/api/v3" },
    })
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "ark-key" },
    })
    fireEvent.change(screen.getByLabelText("Model"), {
      target: { value: "doubao-seedance" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() =>
      expect(api.createMediaProvider).toHaveBeenCalledWith(
        expect.objectContaining({
          kind: "video",
          extraJson: "{}",
        })
      )
    )
  })
})
