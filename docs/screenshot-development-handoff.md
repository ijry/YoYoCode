# Screenshot development handoff

This document describes the screenshot feature as it exists on `main`. It is intended to let another agent reproduce the feature in this repository without relying on conversation history. It describes the final interaction and contracts; the earlier two-window editor design is obsolete.

## Product behavior

The screenshot entry is a camera button immediately to the right of the shared message composer’s `+` menu. It is rendered only for local desktop sessions. Web and remote workspaces do not render it. Linux remains disabled pending dependency compatibility and X11/Wayland integration work; this is not a claim that xcap universally lacks Wayland support.

Hovering for about 200 ms or clicking the camera opens two actions:

- **Screenshot** keeps the main window visible while the desktop is frozen.
- **Screenshot with window hidden** hides the main window before the desktop is frozen and restores it after cancel or completion.

The capture must feel like QQ/WeChat capture:

1. Freeze every supported monitor and open one transparent selection overlay per monitor.
2. Before a region is selected, hover previews the frontmost eligible window under the pointer.
3. A click on a preview selects that window’s visible monitor-local bounds.
4. Pointer movement beyond 4 CSS pixels locks the gesture into free rectangular selection, including when the gesture started on a window candidate; a click without that movement selects the candidate.
5. After selection, show the toolbar beside the bright region in the same overlay. Do not open a second editor window.
6. Eight edge/corner handles resize the region. Dragging empty selected content with the Select tool moves the region. Dragging an existing annotation moves that annotation.
7. Starting a drag in the dark area starts a new selection while annotations remain in frozen-frame coordinates.
8. The toolbar supports Select, pen, rectangle, ellipse, arrow, text, mosaic, and numbered markers; five colors; undo and redo.
9. Escape cancels an active text edit or drag first. A second Escape cancels the capture.

The confirmation/check action copies the final annotated crop to the system clipboard and ends the capture. It must not attach an image to the current AI conversation or emit a retired screenshot event. Save opens a native path dialog and writes PNG while leaving the overlay open. Pin creates a borderless always-on-top image window that can be dragged and is closed by clicking it; closing the pin removes its temporary PNG.

If an image cannot be decoded, copy/pin/finish controls stay disabled and an inline error remains visible. Output operations use a synchronous busy gate so double clicks cannot finish twice. Save errors preserve the selection and show an inline alert.

## Source layout

Relevant frontend files:

- `src/components/chat/composer/composer-screenshot-button.tsx` — camera button and action menu.
- `src/components/chat/message-input.tsx` — shared composer integration and local-desktop gate.
- `src/app/screenshot-overlay/page.tsx` — overlay route.
- `src/app/screenshot-pin/page.tsx` — pinned-image route.
- `src/components/screenshot/screenshot-capture-overlay.tsx` — selection, hover window preview, handles, annotations, toolbar, output actions.
- `src/lib/screenshot/types.ts` — wire DTOs and frontend geometry types.
- `src/lib/screenshot/annotation.ts` — annotation model and rendering helpers.
- `src/lib/screenshot/overlay-geometry.ts` — CSS/device-pixel conversion and viewport placement.
- `src/lib/copy-image.ts` — clipboard image writing, including WebKit timing requirements.
- `src/lib/tauri.ts` — invoke wrappers and platform detection.
- `src/components/screenshot/screenshot-capture-overlay.test.tsx` — interaction regressions.

Relevant backend files:

- `src-tauri/src/commands/screenshot.rs` — managed frame store, Tauri commands, lifecycle, PNG validation and pin cleanup.
- `src-tauri/src/commands/screenshot/windows.rs` — Windows capture, window enumeration and DWM bounds.
- `src-tauri/src/commands/screenshot/macos.rs` — desktop-side macOS command bridge to the helper.
- `src-tauri/src/computer/composer_capture.rs` — helper capture protocol, chunk validation and limits.
- `src-tauri/src/computer/helper/composer_capture_macos.rs` — ScreenCaptureKit/CoreGraphics helper implementation.
- `src-tauri/src/computer/helper/composer_capture_sck.rs` — ScreenCaptureKit declarations and capture path.
- `src-tauri/src/computer/helper/composer_capture_geometry.rs` — macOS logical-to-pixel geometry.
- `src-tauri/src/computer/protocol.rs` — helper protocol version and authenticated messages.
- `src-tauri/src/lib.rs` — independent `ScreenshotFrameStore` management and command registration.
- `src-tauri/capabilities/default.json` — screenshot window permissions.

The screenshot state is deliberately managed as `ScreenshotFrameStore`. Do not change commands to take `State<AppState>`: the desktop runtime does not manage the whole `AppState`, and that mistake makes every screenshot invoke fail before the command body runs.

## Tauri command contract

Rust command names use snake case. JavaScript invokes the same commands through the transport wrapper, which serializes camel-case fields where the DTO uses `rename_all = "camelCase"`.

```rust
screenshot_begin_selection(
    app: AppHandle,
    state: State<'_, ScreenshotFrameStore>,
    request_id: String,
    hide_main: bool,
) -> Result<ScreenshotBeginOutcome, AppCommandError>

screenshot_get_frame(
    state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
    monitor_index: u32,
) -> Result<MonitorFramePayload, AppCommandError>

screenshot_finish_selection(
    app: AppHandle,
    state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
    monitor_index: u32,
    rect: ScreenshotRect,
    annotated_png_base64: Option<String>,
) -> Result<(), AppCommandError>

screenshot_cancel_selection(
    app: AppHandle,
    state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
) -> Result<(), AppCommandError>

screenshot_request_permission() -> Result<(), AppCommandError>
screenshot_save_to_file(png_base64: String, path: String) -> Result<(), AppCommandError>
screenshot_pin(
    app: AppHandle,
    state: State<'_, ScreenshotFrameStore>,
    png_base64: String,
    x: i32,
    y: i32,
    width: u32,
    height: u32,
    logical_rect: Option<ScreenshotRect>,
) -> Result<String, AppCommandError>
screenshot_read_pin_image(app: AppHandle, path: String) -> Result<String, AppCommandError>
```

`screenshot_take_cropped` remains registered for compatibility with older callers, but it is not part of the current overlay output path. The current finish command validates the selection, releases the capture, closes only that capture’s overlay windows, and restores the main window. It drops the deprecated annotated payload and does not save or attach an AI message.

## DTOs and coordinate rules

The wire selection rectangle passed to `screenshot_finish_selection` uses physical PNG pixels, represented as floating-point numbers and validated before integer cropping. Overlay interaction uses CSS pixels and must convert before invoking it:

```rust
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotRect {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotWindowBounds {
    pub x: u32,
    pub y: u32,
    pub width: u32,
    pub height: u32,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MonitorFramePayload {
    pub monitor_index: u32,
    pub png_base64: String,
    pub width: u32,
    pub height: u32,
    pub windows: Vec<ScreenshotWindowBounds>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub logical_bounds: Option<ScreenshotRect>,
}
```

`logical_bounds` is omitted on Windows and represents global macOS desktop logical points when present. `width` and `height` are always the actual PNG pixel dimensions and are the crop validation basis. Never assume CSS pixels equal image pixels.

For HTML canvas conversion, compute independent horizontal and vertical ratios from the displayed frame dimensions. Use `cssRectToPhysical` for output and `physicalRectToCss` for handles and hit testing. Clamp and validate positive finite rectangles on both sides of the command boundary.

Windows window candidates are physical monitor-local rectangles. The native enumerator keeps front-to-back order and rejects invisible, iconic, cloaked, zero-sized, child, shell/taskbar and failed-query windows. It uses DWM extended frame bounds and clips signed global coordinates to the current monitor. Do not blanket-exclude every window from the application process; the hidden main window is naturally absent and a visible normal app window can be selected.

macOS candidate bounds start as global logical points and are mapped into each captured image’s local pixel space. Pin placement must derive logical position from the frozen monitor’s `logical_bounds`; never add a global pixel origin to a logical coordinate. Retina and non-Retina displays therefore use the same logical interaction model with per-display pixel dimensions.

## Capture lifecycle and safety

Each capture has a UUID and an overlay label containing that UUID. Window close handlers extract the owner capture ID and cancel only that capture. Never use a global `cancel_all_pending` callback when one overlay closes, because a stale window from an older capture can otherwise cancel a newer capture.

The store entry is removed before sibling overlay windows are closed. Recursive close callbacks then become no-ops. Main-window hide/show transitions are serialized on the UI thread so an old restore cannot race a new capture’s hide request.

`finish` and `cancel` must release all frame bytes. A completed capture must not remain in an in-memory cache waiting for a consumer. The temporary pin file name is derived from the same window label used by the close hook, so cleanup and creation cannot disagree about the filename.

Save accepts only a path ending in `.png`, decodes base64, and writes the bytes. The command intentionally does not claim full PNG semantic validation beyond the coded checks. Pin image reads canonicalize both the requested path and the configured pins directory, require a PNG extension, and reject paths outside that directory; do not turn the reader into an arbitrary file-read command.

## Windows implementation notes

The Windows implementation uses xcap 0.9.0 behind the desktop Windows feature dependency. Capture uses native monitor metadata and xcap pixels. Keep the dependency platform-gated; Linux should not accidentally link Windows APIs.

Native window metadata uses `windows-sys` 0.59 and DWM APIs. Preserve z-order and skip query failures rather than inventing bounds. If a frame’s actual size differs from monitor metadata, disable window candidates for that frame but keep free-form capture available.

The local fixture verified a front window hover preview, click-to-select, drag-to-resize/move and an exposed back-window preview. The test fixture is evidence for interaction geometry only; it does not replace a packaged desktop smoke test.

## macOS helper implementation

The main desktop binary must not link ScreenCaptureKit, CoreGraphics capture or accessibility capture symbols. The existing release symbol gate is intentional and must remain strict. All macOS capture APIs live in the `codeg-computer-helper` binary.

The helper uses a dedicated authenticated connection per capture. It launches through the existing helper runner, sends the capture request, and reaps/cancels the helper with RAII. It must not switch Computer Use drivers, download a driver, or request a Computer Use grant.

The helper protocol currently uses version 15 and adds:

```text
ComposerCaptureBegin { excluded_window_ids: Vec<u64> }
ComposerCaptureChunk { monitor_index: u32, offset: u64 }
```

The capture manifest carries per-display index, display ID, name, logical desktop bounds, actual pixel width/height, local front-to-back window rectangles and PNG byte count. Chunks carry monitor index, offset, base64 data and EOF. The receiver validates display indices, offsets, lengths, EOF, bounds and total sizes strictly. A malformed or oversized chunk is an error; never silently truncate or downscale.

Current limits are 32 displays, 64,000,000 pixels per display, 256 MiB total PNG bytes and 1 MiB raw chunks. The existing 16 MiB helper frame cap still applies to individual protocol messages. Capture displays sequentially, so the desktop is not an atomic multi-monitor snapshot.

On macOS 14 and newer, dynamically load ScreenCaptureKit and use the modern screenshot manager path. On older supported systems use the CoreGraphics compatibility path. A modern API error must not silently fall back to the old API. Normalize output to opaque 8-bit sRGB; protected content, HDR fidelity and window-server restrictions remain platform limits.

Permission preflight runs in a fresh helper. A request-permission command launches the existing explicit permission helper. A successful request does not prove that permission was granted; the next manual capture starts a fresh helper and reports the real result. The permission belongs to `codeg-computer-helper`, not the main app or CLI.

## Clipboard and WebKit timing

The overlay must call `navigator.clipboard.write` synchronously from the user gesture and pass a `Promise<Blob>` to the `ClipboardItem`. Do not await PNG synthesis before calling `write`; WebKit can reject a delayed write after the gesture has ended. The copy operation may resolve the blob asynchronously after the synchronous write has started.

Use the same final annotated crop for clipboard, save and pin. The native finish call only releases the frozen capture after the output operation has succeeded. A failed output keeps the overlay and selection available for retry.

## Internationalization and errors

Add screenshot editor text under the existing `screenshotEditor` namespace in all ten locale files: English, Simplified Chinese, Traditional Chinese, Japanese, Korean, Spanish, German, French, Portuguese and Arabic. Keep ICU placeholder names identical across locales.

Use structured `AppCommandError` values from Tauri/HTTP and convert them with the existing frontend error formatter. Never display `String(error)` directly; structured errors become `[object Object]` in the UI. Inline output failures should use an accessible alert and retain the current selection.

## Tests and acceptance

The focused frontend regression command used on main is:

```bash
node node_modules/vitest/vitest.mjs run \
  src/components/chat/composer \
  src/components/screenshot \
  src/lib/screenshot \
  src/lib/copy-image.test.ts \
  src/i18n
```

The merged screenshot regression evidence was 38 files and 617 tests. Run the exact current test paths rather than assuming that count remains unchanged after later changes.

Test at least these transitions:

- camera visible only in local desktop mode;
- hover action menu and hidden-window action;
- frame request and monitor geometry;
- window hover preview versus free drag;
- click-to-select and eight-handle resize;
- selection movement and annotation movement;
- dark-area reselect while annotations persist;
- text Escape and capture Escape;
- undo/redo and annotation export;
- copy, save, pin success and output failure;
- duplicate output clicks and stale capture cleanup;
- cancellation of one capture without closing another;
- physical/CSS conversion on mixed-DPI monitors;
- payload rejection for invalid dimensions and malformed helper chunks.

For Windows, run a packaged or native smoke test that checks multi-monitor capture, window candidates and hidden-main restoration. Do not infer packaged runtime success from a browser-only test.

For macOS, a real Mac is required for complete acceptance. Build the main app and helper, verify the helper is bundled under `Contents/Helpers`, run the symbol gate, exercise TCC permission, test a Retina plus external monitor layout, copy/save/pin, helper crash cleanup and permission revocation. The Windows development machine only provides protocol/type checks and frontend evidence; it cannot prove Mac linking, TCC or runtime behavior.

## Local packaging

Local-only packaging must not publish or push tags. Use an external temporary Tauri config with product name `YoYoCode`, identifier `net.lingyun.yoyocode`, updater artifacts disabled and an ad-hoc signing identity where the platform permits it. Do not read or print signing private keys.

The sidecar preparation script expects release binaries under `src-tauri/target/<triple>/release`. If a custom Cargo target directory is used, it must still produce that exact layout before running sidecar preparation. The binary names are `yoyocode-mcp`, `codeg-computer-helper` and `yoyo-cli`.

Build sidecars separately before `tauri dev` or `tauri build`; the development command can otherwise wait for the frontend while sidecar compilation is still running. Do not run two Tauri builds against the same target directory, and stop the development app before replacing locked binaries.

## Known pitfalls

- Do not use the old region-select plus separate editor-window flow.
- Do not restore automatic AI attachment after copy; screenshot is a general desktop utility.
- Do not emit or consume `screenshot://captured` for the current path.
- Do not reintroduce `State<AppState>` into screenshot commands.
- Do not use physical monitor origins as macOS logical pin coordinates.
- Do not await before starting a WebKit clipboard write.
- Do not use a global pending-capture cancellation from an individual overlay close.
- Do not discard the encoded crop and accidentally return the original full frame; encoded crop helpers must return the encoder buffer.
- Do not broaden the Linux capture dependency without validating Wayland support.
- Do not loosen the main-app macOS capture-symbol gate to make a build pass.
- Do not describe partial Apple type checks as a complete Mac build.
- Do not use shell pipelines that hide a failed compiler exit status. Preserve the subprocess return code and inspect fresh compiler artifacts.
- When verifying a Windows lib test executable, use the executable path emitted by the current Cargo JSON run. An old executable can produce a false pass; if a manifest is required, apply it only to that fresh artifact and report the limitation.

## Reimplementation order

1. Add the platform-gated capture backend, frame DTOs and independently managed frame store; test crop bounds and capture cancellation before adding UI.
2. Register the Tauri commands, static overlay/pin routes and capability scopes. `begin` returns `{ captureId, requestId, monitors: [{ index, name, width, height }] }`; overlays request frame pixels separately.
3. Build the shared-composer camera menu and per-monitor selection overlay. Verify hidden-main restoration and stale-window ownership first.
4. Add coordinate conversion, hover window candidates, free selection, move/resize and viewport-constrained toolbar placement.
5. Add annotation rendering/history, then route a single composed crop into copy/save/pin. Verify decode and busy/error states.
6. Integrate the macOS helper protocol and native capture behind platform/feature gates. Package matching protocol versions together.
7. Add all locales, focused regression tests and native smoke tests; only then build a local package.

Existing Windows local packages and their checksums were preserved under `dist/local-packages/screenshot-7d3977dfb/` and `dist/local-packages/screenshot-c9d298f06/`. These are ignored local artifacts, not source dependencies. The second includes window-hover selection; neither includes the later comic workflow repair. The former `src-tauri/target/local-packages` paths are obsolete after cache cleanup.

Implementation landmarks: `7d3977dfb` added resize/move/pin/save/copy, `c9d298f06` added Windows window-hover selection, and `83542990b` added the macOS helper path. Screenshot branches were merged into main with `3f5075c35` and `2ad79c2f3`; deleted branch names are historical references, not required checkouts.

## Evidence boundary at handoff

Windows screenshot interaction and the merged frontend suite were validated on the development machine. The macOS helper source passed an isolated Apple-target type check with protocol/DTO stubs, but a complete Mac app/helper build, DMG and real TCC/Retina runtime test still require a Mac. Linux remains intentionally disabled. The feature is ready for further development and local verification; this document does not authorize a release or CI publication.
