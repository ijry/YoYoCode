import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"
import {
  acpListAgents,
  acpZcodeListProviders,
  listModelProviders,
} from "@/lib/api"
import type { AcpAgentInfo, ZcodeProviderOption } from "@/lib/types"
import {
  setZcodePinnedProvider,
  zcodePinnedProvider,
  AcpAgentSettings,
} from "./acp-agent-settings"

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return Object.fromEntries(
    Object.entries(actual).map(([name, value]) => [
      name,
      typeof value === "function" ? vi.fn(() => new Promise(() => {})) : value,
    ])
  )
})
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(),
}))

const PROVIDERS: ZcodeProviderOption[] = [
  {
    id: "ai-switch-codex",
    name: "AI Switch (Codex)",
    enabled: true,
    has_api_key: true,
    key_optional: false,
    local_base_url: true,
    model_count: 25,
    selectable: true,
  },
  {
    id: "builtin:bigmodel-start-plan",
    name: "BigModel- Coding Plan",
    enabled: false,
    has_api_key: false,
    key_optional: false,
    local_base_url: false,
    model_count: 0,
    selectable: false,
  },
]

function zcodeAgent(env: Record<string, string>): AcpAgentInfo {
  return {
    agent_type: "zcode",
    name: "ZCode",
    registry_id: "codeg-zcode-acp",
    registry_version: "0.76.2",
    installed_version: "0.76.2",
    description: "Official ZCode runtime via the community ACP bridge",
    skills_capable: true,
    supports_custom_version: true,
    available: true,
    distribution_type: "npx",
    is_acp_adapter: true,
    enabled: true,
    sort_order: 0,
    env,
    config_json: null,
    config_file_path: null,
    codex_auth_json: null,
    codex_config_toml: null,
    codex_model_catalog: null,
    codex_sandbox_settings: null,
    cline_secrets_json: null,
    hermes_config_yaml: null,
    grok_config_toml: null,
    grok_settings: null,
    cursor_cli_config_json: null,
    cursor_settings: null,
    model_provider_id: null,
    host_tools_agent_mode: false,
    custom_source: null,
    icon_url: null,
  }
}

async function renderPanel(env: Record<string, string>) {
  vi.mocked(acpListAgents).mockResolvedValue([zcodeAgent(env)])
  vi.mocked(listModelProviders).mockResolvedValue([])
  vi.mocked(acpZcodeListProviders).mockResolvedValue(PROVIDERS)
  render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <AcpAgentSettings />
    </NextIntlClientProvider>
  )
  await screen.findByRole("heading", { name: "ZCode" })
}

function envTextbox(): HTMLTextAreaElement {
  const label = screen.getByText("Environment Variables", {
    selector: "label",
  })
  return within(label.parentElement as HTMLElement).getByRole(
    "textbox"
  ) as HTMLTextAreaElement
}

/** The provider select sits under a visible `<label>` in the env card —
 *  Radix's trigger picks up no accessible name from it, so query positionally
 *  like the provider-rebind suite does. */
function providerCombobox(): HTMLElement {
  const label = screen.getByText(
    enMessages.AcpAgentSettings.zcode.providerLabel,
    { selector: "label" }
  )
  return within(label.parentElement as HTMLElement).getByRole("combobox")
}

describe("ZCode settings panel", () => {
  it("shows runtime overrides without offering unsupported credential/config writes", async () => {
    await renderPanel({
      ZCODE_BIN: "C:/My ZCode/zcode.cjs",
      ZCODE_NODE: "C:/My Node/node.exe",
    })
    expect(await envTextbox()).toHaveValue(
      "ZCODE_BIN=C:/My ZCode/zcode.cjs\nZCODE_NODE=C:/My Node/node.exe"
    )
    expect(
      screen.getByText(enMessages.AcpAgentSettings.zcode.runtimeHint)
    ).toBeInTheDocument()
    expect(
      screen.queryByText("Native JSON Config", { selector: "label" })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText("API Key", { selector: "label" })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: "Save Config Management" })
    ).not.toBeInTheDocument()
  })

  it("offers a provider dropdown that pins ZCODE_PROVIDER into the same draft", async () => {
    const user = userEvent.setup()
    await renderPanel({
      ZCODE_BIN: "C:/My ZCode/zcode.cjs",
      ZCODE_NODE: "C:/My Node/node.exe",
    })

    // No pin yet: the dropdown reads the auto entry, and the pin-able ids
    // come from the bridge-shaped list (a keyless builtin carries a hint).
    const combobox = providerCombobox()
    expect(combobox).toHaveTextContent(
      enMessages.AcpAgentSettings.zcode.providerAuto
    )
    await user.click(combobox)
    const list = await screen.findByRole("listbox")
    expect(
      within(list).getByRole("option", {
        name: `AI Switch (Codex)`,
      })
    ).toBeInTheDocument()
    expect(
      within(list).getByRole("option", {
        name: `BigModel- Coding Plan (${enMessages.AcpAgentSettings.zcode.providerUnselectable})`,
      })
    ).toBeInTheDocument()
    await user.click(
      within(list).getByRole("option", { name: "AI Switch (Codex)" })
    )

    // Choosing a provider patches ONLY its line into the same draft the
    // textarea holds — the runtime overrides stay byte-identical.
    expect(envTextbox()).toHaveValue(
      "ZCODE_BIN=C:/My ZCode/zcode.cjs\nZCODE_NODE=C:/My Node/node.exe\nZCODE_PROVIDER=ai-switch-codex"
    )
    expect(combobox).toHaveTextContent("AI Switch (Codex)")

    // Back to auto: the key leaves the draft entirely, so the bridge falls
    // back to its own scan on the next connection.
    await user.click(combobox)
    const reopened = await screen.findByRole("listbox")
    await user.click(
      within(reopened).getByRole("option", {
        name: enMessages.AcpAgentSettings.zcode.providerAuto,
      })
    )
    expect(envTextbox()).toHaveValue(
      "ZCODE_BIN=C:/My ZCode/zcode.cjs\nZCODE_NODE=C:/My Node/node.exe"
    )
  })

  it("keeps a hand-typed pin selectable even when no listed provider matches", async () => {
    const user = userEvent.setup()
    await renderPanel({
      ZCODE_PROVIDER: "custom:my-endpoint",
      ZCODE_BIN: "C:/My ZCode/zcode.cjs",
    })
    const combobox = providerCombobox()
    expect(combobox).toHaveTextContent("custom:my-endpoint")
    await user.click(combobox)
    const list = await screen.findByRole("listbox")
    expect(
      within(list).getByRole("option", { name: "custom:my-endpoint" })
    ).toBeInTheDocument()
  })
})

describe("ZCode provider env helpers", () => {
  it("reads and patches the pin without touching other lines", () => {
    const draft = "ZCODE_BIN=/a/zcode.cjs\n# comment\n\nZCODE_NODE=/a/node"
    expect(zcodePinnedProvider(draft)).toBe("")
    const pinned = setZcodePinnedProvider(draft, " builtin:zai-coding-plan ")
    expect(zcodePinnedProvider(pinned)).toBe("builtin:zai-coding-plan")
    expect(pinned).toBe(
      "ZCODE_BIN=/a/zcode.cjs\n# comment\n\nZCODE_NODE=/a/node\nZCODE_PROVIDER=builtin:zai-coding-plan"
    )
    expect(setZcodePinnedProvider(pinned, "")).toBe(draft)
  })
})
