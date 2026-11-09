import type { StoryShotInput } from "@/lib/comic"

type ImportErrorKey =
  | "storyboardJsonSyntax"
  | "storyboardJsonShape"
  | "storyboardJsonShot"
  | "storyboardJsonDuplicate"

export class ComicStoryboardImportError extends Error {
  constructor(
    public readonly key: ImportErrorKey,
    public readonly params: Record<string, string | number> = {}
  ) {
    super(key)
  }
}

const isObject = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value)
const safeId = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[a-z0-9_][a-z0-9_-]{0,63}$/.test(value) &&
  !value.endsWith("-") &&
  !["__proto__", "prototype", "constructor"].includes(value)
const optionalString = (value: unknown) =>
  value == null || typeof value === "string"
const keysAllowed = (obj: Record<string, unknown>, keys: string[]) =>
  Object.keys(obj).every((key) => keys.includes(key))

function validRefs(value: unknown): boolean {
  if (value === undefined) return true
  if (!isObject(value) || !keysAllowed(value, ["characters", "scene", "props"]))
    return false
  return (
    (value.scene == null || safeId(value.scene)) &&
    (value.props === undefined ||
      (Array.isArray(value.props) && value.props.every(safeId))) &&
    (value.characters === undefined ||
      (Array.isArray(value.characters) &&
        value.characters.every(
          (character) =>
            isObject(character) &&
            keysAllowed(character, ["slug", "outfit"]) &&
            safeId(character.slug) &&
            (character.outfit == null ||
              character.outfit === "" ||
              safeId(character.outfit))
        )))
  )
}

/** Validate the entire import before any write. Media bindings cannot be
 * imported: they must go through the existing explicit review/bind actions. */
export function parseComicStoryboardImport(text: string): StoryShotInput[] {
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new ComicStoryboardImportError("storyboardJsonSyntax")
  }
  const shots = Array.isArray(parsed)
    ? parsed
    : isObject(parsed) && keysAllowed(parsed, ["shots"])
      ? parsed.shots
      : null
  if (!Array.isArray(shots) || shots.length === 0)
    throw new ComicStoryboardImportError("storyboardJsonShape")
  const ids = new Set<string>()
  return shots.map((shot: unknown, index) => {
    if (
      !isObject(shot) ||
      !keysAllowed(shot, [
        "id",
        "no",
        "description",
        "dialogue",
        "duration_secs",
        "camera",
        "shot_size",
        "transition",
        "refs",
        "state",
        "selected",
        "audio",
        "video",
      ]) ||
      !safeId(shot.id) ||
      typeof shot.description !== "string" ||
      !shot.description.trim() ||
      (shot.no !== undefined &&
        (typeof shot.no !== "number" ||
          !Number.isInteger(shot.no) ||
          shot.no < 1)) ||
      (shot.duration_secs !== undefined &&
        (typeof shot.duration_secs !== "number" ||
          !Number.isFinite(shot.duration_secs) ||
          shot.duration_secs < 0.1 ||
          shot.duration_secs > 120)) ||
      ![shot.camera, shot.shot_size, shot.transition].every(optionalString) ||
      (shot.dialogue !== undefined &&
        (!Array.isArray(shot.dialogue) ||
          !shot.dialogue.every(
            (line: unknown) =>
              isObject(line) &&
              keysAllowed(line, ["character", "text"]) &&
              typeof line.text === "string" &&
              (line.character == null || safeId(line.character))
          ))) ||
      !validRefs(shot.refs) ||
      (shot.state !== undefined &&
        ![
          "draft",
          "prompted",
          "generating",
          "candidate",
          "selected",
          "rendering",
          "done",
        ].includes(shot.state as string)) ||
      shot.selected != null ||
      shot.audio != null ||
      shot.video != null
    )
      throw new ComicStoryboardImportError("storyboardJsonShot", {
        index: index + 1,
      })
    if (ids.has(shot.id))
      throw new ComicStoryboardImportError("storyboardJsonDuplicate", {
        id: shot.id,
      })
    ids.add(shot.id)
    // Backend assigns shot numbers from the input order.
    const copy: Record<string, unknown> = {
      ...shot,
      duration_secs: shot.duration_secs ?? 3,
    }
    if (isObject(shot.refs) && Array.isArray(shot.refs.characters)) {
      copy.refs = {
        ...shot.refs,
        characters: shot.refs.characters.map(
          (character: { slug: string; outfit?: string | null }) => ({
            ...character,
            outfit: character.outfit ?? "",
          })
        ),
      }
    }
    delete copy.no
    return copy as unknown as StoryShotInput
  })
}
