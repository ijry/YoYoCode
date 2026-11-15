## Context
Comic Studio can generate and persist video tasks, but the workbench cannot review or preview their results, and binding a result would unconditionally replace the current clip. Video artifacts may reach 256 MiB, so the image Base64 reader and one-shot download tickets are not suitable for playback. Build a review flow where generation is queued into the active same-project conversation, the workbench previews succeeded task artifacts, and a clip changes only after explicit user confirmation.

## Recommended Approach
Reuse the existing conversation handoff for generation. Add episode-filtered task listing so older relevant tasks are not hidden by the current 50-task limit. Serve preview bytes through a short-lived, reusable, opaque capability scoped to a succeeded video task and its validated artifact. Each request supports a bounded HTTP byte range; the authenticated UI obtains the grant, while the media element can use the capability URL without a bearer header. Use the same authorization and range behavior for server/Web playback, a dedicated Tauri URI scheme for local desktop, and a streaming range-preserving remote-desktop proxy. Keep only one preview player mounted.

Binding uses optimistic compare-and-swap: the confirmation carries the task ID currently bound when the dialog opened, or null when unbound. Verify task ownership and artifact first, then compare and update under the storyboard write lock. A mismatch returns a distinct conflict and never overwrites the newer binding. Binding the already-bound task is idempotent. The old video stays active until a successful explicit bind.

## Files and Interfaces
- `src-tauri/src/comic/task.rs`, `commands/comic.rs`, `web/handlers/comic.rs`, `web/router.rs`: add optional episode filtering to task listing before applying the limit; expose an authenticated preview-grant request and a capability-based byte-range response.
- `src-tauri/src/comic/video.rs`, `comic/store.rs`, `app_error.rs`, and command/handler call sites: extend bind with `expected_current_task_id: Option<i32>` and return a conflict when the actual binding differs. Map the conflict distinctly to HTTP 409 and a frontend-decodable Tauri/API error.
- `src-tauri/src/app_state.rs` and the Comic preview service module: hold expiring preview grants. A grant is opaque, reusable only until expiry, bound to folder/task/artifact, and revalidated against task status, target, story-relative path, and file containment on every range request. Never accept an arbitrary filesystem path from the media URL.
- `src-tauri/src/lib.rs`: register a dedicated local Tauri media URI protocol. Reuse the established range semantics from `browser/doc_guest.rs` while reading only the requested bounded segment (maximum 16 MiB per response); return correct MIME, `Accept-Ranges`, 206, and 416 headers.
- `src-tauri/src/commands/remote_proxy.rs` plus its transport/API entry points: add a streaming proxy that forwards `Range` and relevant status/headers without Base64 or whole-file buffering.
- `src/lib/comic.ts`, `comic-workbench-panel.tsx`, and `comic-workbench-panel.test.tsx`: type the episode filter, preview URL/grant and video bind call; add a video quick action gated on an actively selected storyboard episode; show episode-scoped video task status and succeeded results; mount one preview player; require confirmation for initial bind and replacement; refresh task/storyboard state on success or conflict.
- `src-tauri/src/comic/provider` and `acp/delegation/tool_schema.json`: fix the MCP `duration_secs` deserialization alias and ensure video-provider setup advertises/selects the supported Ark protocol rather than silently defaulting to unsupported OpenAI-compatible video.
- `src-tauri/src/comic/video.rs` tests, task/preview route tests, command/store tests, remote proxy tests as needed, and `src/i18n/messages/*.json`: cover contracts, authorization and range behavior, concurrent binding, UI states, and add consistent strings in all supported locales.

## Implementation and Verification
1. Add failing focused tests for episode filtering, `duration_secs` snake-case input, compare-and-swap conflict/idempotence, capability ownership/expiry and range responses, and the UI generation/review/replace flow.
2. Implement backend contracts and tests first: provider/deserialization correction, filtered task listing, atomic bind conflict, then grant validation and bounded range serving.
3. Add Web, local Tauri, and remote-desktop preview transports using the same capability scope and range response rules. Verify unauthorized, expired, wrong-folder/task, malformed/unsatisfiable/multiple ranges, and oversized requests do not expose artifact bytes.
4. Add the frontend action and review UI, explicit confirmation, conflict refresh, and locale strings. Test that no generation action binds automatically, an existing clip remains active until confirmation, and a stale confirmation does not report success.
5. Run focused Rust Comic tests, including server-feature route coverage where applicable; focused Vitest workbench/API tests; `pnpm exec tsc --noEmit`; targeted ESLint; then the repository-required frontend tests/build and relevant Rust check/test/clippy commands from `AGENTS.md`. Do not call a live paid video provider as part of verification.

## Constraints
- Preserve existing uncommitted work; do not reset, clean, switch branches, commit, or push.
- Preserve static export compatibility; no dynamic Next.js routes.
- Keep video artifacts on disk and stream requested ranges; do not convert entire videos to Base64 or load them fully into memory.
- Generation remains in the active conversation; binding remains a user-controlled workbench action.
- Retain the currently bound clip until an explicit, non-stale confirmation succeeds.