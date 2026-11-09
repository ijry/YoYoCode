---
name: comic-production
description: Use when the user requests motion comic or comic production, novel adaptation, series planning, episodes, scripts, character cards, or storyboards in Codeg; 当用户提出漫剧创作、小说改编、原创短剧、整剧规划、多集剧本、角色设定或分镜制作时使用。
---

# Comic production / 漫剧创作

Create one series across multiple episodes in the current project directory. The workbench's saved `story/` data is the deliverable; chat prose alone is never evidence of a saved script or storyboard. On a supported MCP agent, turn the user's natural-language request into saved episode metadata, full scripts, cards, and storyboard drafts, then direct the user to review the structured result in the workbench. Do not ask the user to find, construct, or paste JSON. This is a bundled expert skill, not an OfficeCLI plugin; no package installation is required.

## 1. Inspect the project and available tools

- Check that this session actually exposes `comic_get_project`, then call it first and on every resume. Tool server prefixes can differ; use the exposed tool with this name.
- A skill badge does not grant MCP access. Pi/Cline do not forward the built-in companion, and OpenClaw does not accept it. If tools are missing, explain the limitation and offer the workbench's manual script/storyboard import or a capable agent. For a session created before the upgrade, ask the user to reconnect or start a new session in this folder. Never silently disconnect, claim a successful save, or install another MCP server.
- All comic calls are scoped to the session's working directory. Do not pass `folderId`, `project_dir`, or an arbitrary output directory; use only the advertised argument keys. Do not move work to a different directory to bypass a refusal.
- If `initialized: false`, there is no project. `nextTool: "comic_init_project"` is guidance, not authorization. Only if the user explicitly asked to create/initialize a project, call `comic_init_project` with the agreed nonempty title (1–200 characters). Otherwise ask whether to create one. Opening the shortcut alone is not an initialization request.
- If initialized, reuse the existing series, episode IDs, assets, and settings. Never initialize another project for each episode.
- Read `comic_list_assets` / `comic_get_asset` for the affected episodes and cards. `comic_get_asset` with `kind: "episode"` returns episode metadata plus script and storyboard when present. Fetch additional pages when results are capped.

**comic_get_project**
```json
{}
```

Only after the user requests a new project titled 《雨夜来信》:

**comic_init_project**
```json
{"title":"雨夜来信"}
```

## 2. Turn the user's request into a saved draft

An explicit user click on a create/save action, or a clear text request to create, organize, continue, or save comic content, authorizes persisting a draft within that stated scope. Opening a navigation shortcut only prepares the composer; it does not submit a request. Do not ask for a redundant confirmation before saving the initial episode scripts, cards, or storyboard draft. Establish source mode, premise, audience, style, episode count, approximate duration, arc, recurring characters/locations/props, and the episode(s) to develop from the user's words and available conversation context. Ask only about an unresolved decision that changes the requested content, such as ambiguous episode mapping, source mode, title, or a conflicting manual edit. Otherwise choose a reasonable draft detail, save it, and send the user to the workbench to review the structured episode and storyboard data.

For a natural-language request such as “把刚才这段整理成第一集并做初版分镜”, read the relevant conversation context, inspect the current project, upsert the affected episode, write its complete script, create or reuse cards, save the storyboard draft, and read it back. The user should not have to know tool names, IDs, JSON shapes, or file paths. Report the saved episode ID/title and the workbench review location after readback.

Honor a request for a proposal only or review before saving. For saved drafts, preserve existing manual work: destructive replacement of a script, card, storyboard, or manual edit requires explicit confirmation of what will be replaced. An explicit instruction that already approves that specific replacement satisfies this gate; routine draft creation or a merge preserving unrelated edits does not need another approval.

- For `source.mode: "original"`, skip novel ingestion, chapter outline, and novel reads. Do not request a missing `novel.txt` or `chapters.json`. A missing novel is normal for original work. For a new original project explicitly requested by the user, register `original: true` once.
- For adaptation, register only the full novel or path the user supplied using `comic_set_source` (`path` OR `upload_base64`, never both). Relay chapter-split results for review, read `comic_get_source_outline`, then bounded `comic_read_source` windows using `from_chapter`, `to_chapter`, `max_chars`, `offset_chars`. Follow `has_more` / `next_offset`; do not invent unread chapters.
- If source mode is unset, use an explicit original/adaptation choice already present in the user's request or context; ask only if that choice remains unclear. Do not infer original solely from `has_source: false`, or overwrite an existing source choice without a request.
- Once the source mode and requested scope are clear enough to act, save the series title/style summary with `comic_upsert_series`; do not request a redundant plan confirmation. For a longer episode map/style bible, use available file tools to merge a small Markdown file under `story/bible/`; add its actual story-relative path to `bible_refs`, preserving existing refs. If file tools are absent, keep the summary in `style_bible` and episode synopses. Do not invent a path to an unwritten file.

**comic_set_source**
```json
{"original":true,"title":"雨夜来信"}
```

**comic_upsert_series**
```json
{"title":"雨夜来信","style_bible":"原创都市悬疑，克制的冷色漫画风。两集，每集约60秒：ep001发现来信；ep002揭示寄信人。"}
```

## 3. Save each episode's script separately

For each requested episode:

1. Re-read its current record before editing; preserve user changes and unrelated metadata. Choose a stable unique episode ID such as `ep001`, `ep002`; IDs are not interchangeable with titles. If the user's text could map to more than one episode, ask only that mapping question.
2. Call `comic_upsert_episode` with `id`, `title`, synopsis, source chapter numbers if adapted, and honest status. This creates the episode before writing its script.
3. Write the full script for that episode using `comic_write_script` with `episode` and `script`. Include scene headings, action, speaker-labelled dialogue/narration, and estimated timing. This replaces ONE episode's script: merge requested edits into the latest text first. Never send the whole show's scripts into `ep001`, or replace an existing episode with only a changed paragraph.
4. Read back with `comic_get_asset`; then set `scripted` via `comic_upsert_episode`, retaining other metadata. Use `draft` for an outline that has no complete script. Multi-episode work repeats these steps for each ID.

When the user asks to organize an existing chat-only script, recover that script from the available conversation context and read the current saved episodes/files before writing. Use existing headings, numbering, and titles to persist all recognized episodes separately. Ask only if episode mapping is ambiguous; do not restart a premise/style questionnaire or ask for the same script in JSON. Preserve the supplied plot/dialogue and manual edits. Read back each saved episode with `comic_get_asset`; create its initial storyboard draft as well when the request includes storyboarding or the overall production flow. If the user asked only to save the script, stop after that save and readback.

**comic_upsert_episode**
```json
{"id":"ep001","title":"第一集：来信","synopsis":"林收到一封写着明日日期的信。","source_chapters":[],"status":"draft"}
```

**comic_write_script**
```json
{"episode":"ep001","script":"# 第一集：来信\n\n## 场景1：公寓玄关，夜，约5秒\n雨声。林拿起门缝下的信，看到明天的日期。\n林：这封信……来自明天？\n"}
```

The short script above is an argument example, not a complete 60-second episode.

## 4. Create or reuse cards before storyboarding

Search/list existing cards to avoid near-duplicates. Read each card before updating it; merge descriptions, outfits, reference paths, seeds, and other manually edited fields. Card upserts are documents, not an excuse to omit existing data. Create new cards with stable slugs and concrete visual `prompt_fragment` text. Do not fabricate image refs or turnaround paths. Record only paths that exist.

**comic_upsert_character**
```json
{"slug":"lin","name":"林","description":"短黑发，灰色外套，疲惫的眼神","personality":"谨慎而好奇","prompt_fragment":"short black hair, grey coat, tired eyes"}
```

**comic_upsert_scene**
```json
{"slug":"apartment-entry","name":"公寓玄关","description":"窄小玄关，门缝透入楼道冷光","prompt_fragment":"narrow apartment entrance, cold corridor light"}
```

**comic_upsert_prop**
```json
{"slug":"letter","name":"来信","description":"泛黄信封，手写明日日期","prompt_fragment":"yellowed envelope, handwritten date"}
```

## 5. Save the ordered storyboard, then verify

- Read the episode again immediately before editing. Derive initial storyboard shots from its saved script, with description, dialogue, duration, camera, shot size, transition, and existing card refs.
- Keep shot IDs stable within an episode; the target is the pair `(episode, shot)`. Do not mix `ep001/shot-0001` with `ep002/shot-0001` or renumber existing IDs to change order.
- For an initial board requested by the user, save the full ordered `shots` array as a draft without an extra confirmation; the user reviews it in the workbench. For a full revision that would destroy or replace existing storyboard/manual edits, read the current board and obtain explicit confirmation before calling `comic_upsert_storyboard`. In both cases merge existing edits, preserve shot order and unmodified shots, selected images, and bound media fields. Re-read if the workbench has changed since your last read. Never replace the entire show.
- For a single-frame change, prefer `comic_upsert_shot` with `episode` and the merged complete `shot`. Preserve other shots and existing media bindings. Ask before a destructive manual-edit replacement.
- Create missing character/scene/prop cards first. `refs.characters` uses objects with `slug` and optional `outfit`; `refs.scene` is one slug; `refs.props` is a slug array. Dialogue's `character` is a character slug, not its display name. New shots start `draft`; do not invent `selected` image paths.

The structured MCP calls are the normal supported workflow. JSON is only an escape hatch when MCP tools are unavailable or when the user is migrating external structured data; do not ask a supported MCP user to find or construct JSON.

Example for a NEW one-shot board only (do not replace a longer existing board with this):

**comic_upsert_storyboard**
```json
{"episode":"ep001","shots":[{"id":"shot-0001","description":"林在玄关展开信封，看见明天的日期。","dialogue":[{"character":"lin","text":"这封信……来自明天？"}],"duration_secs":5,"camera":"static","shot_size":"近景","transition":"cut","refs":{"characters":[{"slug":"lin"}],"scene":"apartment-entry","props":["letter"]},"state":"draft"}]}
```

**comic_get_asset**
```json
{"kind":"episode","id":"ep001"}
```

Verify that the returned title, script, shot IDs/order, card refs, and durations match the intended changes. Read back affected cards too. Update status to `storyboarded` only after the saved board is verified, then call `comic_get_project` again. Report any error rather than marking work complete. Tool errors do not authorize fallback overwrites of `story/` JSON.

## 6. Review before media generation or export

Stop at the saved script/cards/storyboard unless the user already explicitly authorized the next operation and its scope. `next_action`, `nextTool`, “continue planning”, an approved script, or opening the workbench do not authorize paid calls or export. Do not chain text completion into images → video → speech → composition.

| Authorized operation | Tools and review |
| --- | --- |
| Candidate images for agreed shots/count | Inspect providers in `comic_get_project`; preview with `comic_build_shot_prompt`; call `comic_request_generation` only within approved scope. Keep returned `taskId`, poll `comic_check_generation` using `task_id`, then present actual candidate paths with `comic_present_candidates`. That choice binds the image. |
| Speech | `comic_request_speech` for agreed episode/shot/text/voice; poll and review, then `comic_set_shot_audio` with `task_id`. Do not guess missing voices. |
| Video | `comic_request_video` for agreed episode/shot/duration; poll and review. Bind with `comic_set_shot_video` only after explicit review, using `expected_current_task_id` observed when confirmation began (or null if unbound). On conflict, refresh and ask again. Leave workbench-requested binding to its review UI. |
| Composition/export | Only when requested, `comic_compose_episode` for the agreed episode/settings; poll `comic_check_compose`. Report real MP4/SRT paths after success. Local FFmpeg export also requires authorization. |

Use existing configured providers; if missing, direct the user to media-service settings. Never invent credentials or silently choose paid overrides. A batch-limit refusal requires the user's approval before `confirmed: true`. Do not retry paid failed jobs or regenerate declined candidates without an authorized retry scope. Recover running tasks with `comic_list_tasks` rather than queuing duplicates.

## Completion handoff

Give the user the series title, saved episode IDs/titles, script/storyboard counts and status, affected card slugs, and actual story-relative references. For generated media also record the returned task IDs, chosen candidate paths, and export paths. Clearly separate saved work from proposals or incomplete tasks. Direct the user to the current folder's comic workbench and the affected episode to review the saved structured data. On a supported MCP agent, complete the writes and readback yourself instead of handing the user a JSON import task. If MCP is unavailable or the user explicitly requests an external migration, prepare any needed import data yourself, explain the manual import step, and label unsaved data accurately.

## Example user prompts

- “在当前目录新建原创漫剧《雨夜来信》，规划两集，先把第一集剧本和初版分镜保存到工作台，我在那里审核；暂不生成素材。”
- “把刚才聊天里写好的两集剧本整理到工作台，按已有分集标题保存，再生成分镜草稿，不改我的对白。”
- “Adapt this novel into six motion-comic episodes. Read the chapter outline, map the episodes, and save episode one's script and storyboard draft for workbench review. Keep existing manual edits; no paid media generation.”
