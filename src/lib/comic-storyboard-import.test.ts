import { describe, expect, it } from "vitest"
import { parseComicStoryboardImport } from "./comic-storyboard-import"

describe("storyboard JSON import", () => {
  it("accepts an array or envelope and preserves authored order", () => {
    const shots = [
      { id: "shot-z", description: "Opening" },
      {
        id: "shot-a",
        description: "Ending",
        duration_secs: 4,
        dialogue: [{ text: "Hello" }],
      },
    ]
    const expected = shots.map((shot) => ({ duration_secs: 3, ...shot }))
    expect(parseComicStoryboardImport(JSON.stringify(shots))).toEqual(expected)
    expect(parseComicStoryboardImport(JSON.stringify({ shots }))).toEqual(
      expected
    )
  })

  it("defaults an omitted duration to the manual editor's three seconds", () => {
    expect(
      parseComicStoryboardImport('[{"id":"shot-1","description":"Frame"}]')[0]
        .duration_secs
    ).toBe(3)
  })

  it.each([0.1, 3, 120])("accepts supported duration %s", (duration_secs) => {
    expect(
      parseComicStoryboardImport(
        JSON.stringify([{ id: "shot-1", description: "Frame", duration_secs }])
      )[0].duration_secs
    ).toBe(duration_secs)
  })

  it.each([0, 0.01, 0.099, 120.001, 121, null, "3"])(
    "rejects unsupported duration %s",
    (duration_secs) => {
      expect(() =>
        parseComicStoryboardImport(
          JSON.stringify([
            { id: "shot-1", description: "Frame", duration_secs },
          ])
        )
      ).toThrow()
    }
  )

  it.each(["", null, undefined])(
    "normalizes the default character outfit %s",
    (outfit) => {
      const shots = parseComicStoryboardImport(
        JSON.stringify([
          {
            id: "shot-1",
            description: "Frame",
            refs: {
              characters: [{ slug: "hero", outfit }],
              scene: "room",
              props: ["sword"],
            },
          },
        ])
      )
      expect(shots[0].refs).toEqual({
        characters: [{ slug: "hero", outfit: "" }],
        scene: "room",
        props: ["sword"],
      })
    }
  )

  it("preserves a named character outfit", () => {
    const shots = parseComicStoryboardImport(
      '[{"id":"shot-1","description":"Frame","refs":{"characters":[{"slug":"hero","outfit":"school"}]}}]'
    )
    expect(shots[0].refs?.characters).toEqual([
      { slug: "hero", outfit: "school" },
    ])
  })

  it.each([
    "../outside",
    "shot/one",
    "..",
    "UPPER",
    "a.b",
    "a\\b",
    "",
    "__proto__",
    "constructor",
  ])("rejects unsafe id %s", (id) => {
    expect(() =>
      parseComicStoryboardImport(JSON.stringify([{ id, description: "Frame" }]))
    ).toThrow()
  })

  it.each([
    "not json",
    "{}",
    "[]",
    '{"shots":[]}',
    "[null]",
    '[{"id":"a","description":"  "}]',
    '[{"id":"a","description":"A"},{"id":"a","description":"B"}]',
    '[{"id":"a","description":"A","duration_secs":-1}]',
    '[{"id":"a","description":"A","dialogue":[{"text":1}]}]',
    '[{"id":"a","description":"A","refs":{"characters":[{"slug":"../outside"}]}}]',
    '[{"id":"a","description":"A","state":"invented"}]',
    '[{"id":"a","description":"A","video":{"path":"../secret","task_id":1,"duration_secs":3}}]',
    '[{"id":"a","description":"A","selected":"../../secret"}]',
  ])("rejects invalid input atomically: %s", (input) => {
    expect(() => parseComicStoryboardImport(input)).toThrow()
  })
})
