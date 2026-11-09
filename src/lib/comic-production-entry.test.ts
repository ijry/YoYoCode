import { existsSync, readFileSync } from "node:fs"
import { resolve } from "node:path"
import { parse as parseToml } from "smol-toml"
import { parse as parseYaml } from "yaml"
import { describe, expect, it } from "vitest"

import { OFFICE_ACTIONS } from "./office-actions"

const root = resolve(process.cwd(), "src-tauri")
const skillPath = resolve(root, "experts/skills/comic-production/SKILL.md")

type Schema = {
  type?: string | string[]
  required?: string[]
  properties?: Record<string, Schema>
  items?: Schema
  enum?: unknown[]
  pattern?: string
  minimum?: number
  maximum?: number
}

function validateExample(value: unknown, schema: Schema) {
  if (value === null) {
    expect(schema.type).toContain("null")
  } else if (Array.isArray(value)) {
    expect(schema.type).toBe("array")
    for (const item of value) validateExample(item, schema.items!)
  } else if (typeof value === "object") {
    const fields = value as Record<string, unknown>
    expect(schema.type).toBe("object")
    for (const required of schema.required ?? []) {
      expect(fields).toHaveProperty(required)
    }
    for (const [key, entry] of Object.entries(fields)) {
      expect(schema.properties, `unknown argument ${key}`).toHaveProperty(key)
      validateExample(entry, schema.properties![key])
    }
  } else {
    expect(typeof value).toBe(
      schema.type === "integer" ? "number" : schema.type
    )
    if (schema.type === "integer") expect(Number.isInteger(value)).toBe(true)
    if (schema.enum) expect(schema.enum).toContain(value)
    if (schema.pattern) expect(value).toMatch(new RegExp(schema.pattern))
    if (schema.minimum !== undefined)
      expect(value).toBeGreaterThanOrEqual(schema.minimum)
    if (schema.maximum !== undefined)
      expect(value).toBeLessThanOrEqual(schema.maximum)
  }
}

describe("bundled comic production entry contract", () => {
  it("resolves the Office entry to an installable localized expert skill", () => {
    const action = OFFICE_ACTIONS.find((item) => item.id === "comicProduction")!
    const metadata = parseToml(
      readFileSync(resolve(root, "experts/experts.toml"), "utf8")
    )
    const experts = metadata.expert as Array<{
      id: string
      category: string
      display_name: Record<string, string>
      description: Record<string, string>
    }>
    const expert = experts.find((item) => item.id === action.skillId)
    expect(expert).toBeDefined()
    expect(action.settingsSection).toBe("experts")
    expect(
      experts
        .filter((item) => item.id !== action.skillId)
        .map((item) => item.category)
    ).toContain(expert!.category)
    for (const locale of [
      "en",
      "zh-CN",
      "zh-TW",
      "ja",
      "ko",
      "es",
      "de",
      "fr",
      "pt",
      "ar",
    ]) {
      expect(expert!.display_name[locale]).toBeTruthy()
      expect(expert!.description[locale]).toBeTruthy()
    }
    expect(existsSync(skillPath)).toBe(true)
    const skill = readFileSync(skillPath, "utf8")
    const frontmatter = parseYaml(skill.split("---")[1])
    expect(frontmatter.name).toBe(action.skillId)
    expect(skill.split("\n").length).toBeLessThanOrEqual(250)
  })

  it("uses real MCP tool argument schemas in every JSON example", () => {
    expect(existsSync(skillPath)).toBe(true)
    const skill = readFileSync(skillPath, "utf8")
    const tools = JSON.parse(
      readFileSync(resolve(root, "src/acp/delegation/tool_schema.json"), "utf8")
    ) as Array<{ name: string; inputSchema: Schema }>
    const examples = [
      ...skill.matchAll(/\*\*(comic_[a-z_]+)\*\*\n```json\n([\s\S]*?)\n```/g),
    ]
    expect(examples.length).toBeGreaterThanOrEqual(8)
    for (const [, name, json] of examples) {
      const tool = tools.find((entry) => entry.name === name)
      expect(tool, name).toBeDefined()
      validateExample(JSON.parse(json), tool!.inputSchema)
    }
  })
})
