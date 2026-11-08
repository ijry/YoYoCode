//! The Rust half of the composer's screenshot button: where the frozen
//! monitor frames live between "capture" and "crop", and the math that turns
//! a selection rectangle into pixels.
//!
//! Two halves, deliberately separable:
//!
//! * [`ScreenshotFrameStore`] — the in-memory warehouse behind the button. One
//!   live capture at a time (a second `begin_capture` drops the first, so a
//!   stale overlay window can never serve a stale frame), frames in, cropped
//!   image out, everything released when the editor pulls the result. It
//!   names no xcap types, so it compiles — and is tested — in every runtime.
//! * [`validate_selection_rect`] / [`crop_png`] — pure functions over the
//!   pixels, no runtime behind them: round and clamp a selection to the
//!   frame, then cut it out of a PNG.

use std::collections::HashMap;
use std::sync::{Mutex, MutexGuard};

use base64::{engine::general_purpose::STANDARD, Engine as _};
use serde::{Deserialize, Serialize};

use crate::app_error::AppCommandError;

/// The label prefix every selection overlay window carries, followed by the
/// monitor's index (`screenshot-overlay-1`): the commands close leftovers by
/// listing windows and matching it.
pub const OVERLAY_WINDOW_PREFIX: &str = "screenshot-overlay-";

/// A selection rectangle in a frame's pixel coordinates, as an overlay
/// submits it — the overlay converts its CSS-pixel drag to the frame's
/// physical pixels before sending. Floating point so a scaled submit loses
/// nothing before the round to whole pixels.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotRect {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

/// A [`ScreenshotRect`] after validation: whole pixels, inside the frame,
/// at least one pixel on a side.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct NormalizedRect {
    pub x: u32,
    pub y: u32,
    pub width: u32,
    pub height: u32,
}

/// The frozen frame of one monitor: a full-screen PNG in the monitor's
/// physical pixels, handed to that monitor's overlay window to draw as-is.
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MonitorFramePayload {
    pub monitor_index: u32,
    pub png_base64: String,
    pub width: u32,
    pub height: u32,
}

/// The selected region cut out of a frame and PNG-encoded — what the
/// annotation editor opens and what ends up attached to the message.
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CroppedPayload {
    pub png_base64: String,
    pub width: u32,
    pub height: u32,
    pub mime: String,
}

/// The metadata a `screenshot://captured` event carries to the main window:
/// which input asked (request id) and how big the result is. Never the
/// bytes — the editor pulls those with a separate invoke.
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotCapturedMeta {
    pub request_id: String,
    pub width: u32,
    pub height: u32,
}

/// One capture session: the frozen frame of every monitor plus the cropped
/// selection once one is confirmed.
struct ScreenshotCapture {
    request_id: String,
    frames: HashMap<u32, MonitorFramePayload>,
    cropped: Option<(CroppedPayload, ScreenshotCapturedMeta)>,
}

/// The in-memory frame warehouse behind the screenshot flow, one field on
/// It holds at most one live capture at a time — `begin_capture` drops
/// whatever an earlier one left, so a stale overlay can never serve a stale
/// frame — and the entry is gone the moment the editor pulls its image.
///
/// Deliberately free of xcap types: the server and MCP builds compile this
/// struct and the pure functions below, only the desktop commands fill and
/// drain it.
#[derive(Default)]
pub struct ScreenshotFrameStore {
    captures: Mutex<HashMap<String, ScreenshotCapture>>,
}

impl ScreenshotFrameStore {
    /// Lock the map, recovering a poisoned guard: every critical section
    /// below leaves the map a valid `HashMap`, so a panic can cost at most
    /// one in-flight update — and the store's `Option`/`bool` API has no
    /// channel to carry an `AppCommandError` through anyway.
    fn lock(&self) -> MutexGuard<'_, HashMap<String, ScreenshotCapture>> {
        self.captures
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }

    /// Start a capture: drop every earlier entry and register a fresh one
    /// under a new capture id, which is returned.
    pub fn begin_capture(&self, request_id: String) -> String {
        let capture_id = uuid::Uuid::new_v4().to_string();
        let mut captures = self.lock();
        captures.clear();
        captures.insert(
            capture_id.clone(),
            ScreenshotCapture {
                request_id,
                frames: HashMap::new(),
                cropped: None,
            },
        );
        capture_id
    }

    /// Record the frozen frames of the monitors of `capture_id`. A capture
    /// that was cancelled or superseded before the frames arrived has
    /// nowhere to put them: they are dropped, not resurrected.
    pub fn insert_frames(&self, capture_id: &str, frames: Vec<(u32, MonitorFramePayload)>) {
        if let Some(capture) = self.lock().get_mut(capture_id) {
            capture.frames.extend(frames);
        }
    }

    /// The frozen frame of one monitor, as its overlay window asked for it.
    pub fn get_frame(&self, capture_id: &str, monitor_index: u32) -> Option<MonitorFramePayload> {
        self.lock()
            .get(capture_id)
            .and_then(|capture| capture.frames.get(&monitor_index))
            .cloned()
    }

    /// Attach the cropped selection to its capture and hand back the
    /// metadata the `screenshot://captured` event must carry. `None` when
    /// the capture is gone (cancelled, superseded, already taken).
    pub fn store_cropped(
        &self,
        capture_id: &str,
        cropped: CroppedPayload,
    ) -> Option<ScreenshotCapturedMeta> {
        let mut captures = self.lock();
        let capture = captures.get_mut(capture_id)?;
        let meta = ScreenshotCapturedMeta {
            request_id: capture.request_id.clone(),
            width: cropped.width,
            height: cropped.height,
        };
        capture.cropped = Some((cropped, meta.clone()));
        Some(meta)
    }

    /// Take the cropped image out and delete the whole capture: the editor
    /// pulls the frame once and the warehouse releases everything with it.
    pub fn take_cropped(&self, capture_id: &str) -> Option<CroppedPayload> {
        self.lock()
            .remove(capture_id)
            .and_then(|capture| capture.cropped.map(|(cropped, _)| cropped))
    }

    /// Cancel a capture that never confirmed a selection. `false` means
    /// there was nothing left to cancel — the entry is gone, or a cropped
    /// image is already waiting for the editor and must not be dropped
    /// (an overlay unloads as the capture finishes and its parting cancel
    /// arrives after the confirmation).
    pub fn cancel(&self, capture_id: &str) -> bool {
        let mut captures = self.lock();
        let pending = captures
            .get(capture_id)
            .is_some_and(|capture| capture.cropped.is_none());
        if pending {
            captures.remove(capture_id);
        }
        pending
    }

    /// Cancel every capture that has not confirmed a selection — the sweep
    /// an overlay window killed out from under the flow triggers (Alt+F4 on
    /// a focused overlay destroys it without any command to cancel, so the
    /// flow's own bookkeeping never runs). `false` when nothing pending
    /// remained: every live capture had already cropped, or there was no
    /// capture at all, and the caller must not touch the windows.
    pub fn cancel_all_pending(&self) -> bool {
        let mut captures = self.lock();
        let before = captures.len();
        captures.retain(|_, capture| capture.cropped.is_some());
        before > captures.len()
    }
}

/// Round a selection to the integer pixel box it covers, clamping every edge
/// that hangs off the frame: the crop can only ever land on the frame's
/// pixels. A selection that rounds to nothing — a click without a drag, a
/// sub-pixel sliver, a rectangle entirely off the frame — is refused.
pub fn validate_selection_rect(
    rect: &ScreenshotRect,
    frame_width: u32,
    frame_height: u32,
) -> Result<NormalizedRect, AppCommandError> {
    let finite = [rect.x, rect.y, rect.width, rect.height]
        .iter()
        .all(|v| v.is_finite());
    if !finite {
        return Err(AppCommandError::invalid_input(
            "the selection has a non-finite coordinate",
        ));
    }
    let clamp_x = |v: f64| v.round().clamp(0.0, f64::from(frame_width)) as u32;
    let clamp_y = |v: f64| v.round().clamp(0.0, f64::from(frame_height)) as u32;
    let x0 = clamp_x(rect.x);
    let x1 = clamp_x(rect.x + rect.width);
    let y0 = clamp_y(rect.y);
    let y1 = clamp_y(rect.y + rect.height);
    let width = x1.saturating_sub(x0);
    let height = y1.saturating_sub(y0);
    if width < 1 || height < 1 {
        return Err(AppCommandError::invalid_input(format!(
            "the selection covers no pixels after rounding ({width}x{height} at {x0},{y0})"
        )));
    }
    Ok(NormalizedRect {
        x: x0,
        y: y0,
        width,
        height,
    })
}

/// Cut the validated selection out of a full-frame PNG and encode it back as
/// its own PNG. A frame that does not decode, or a rect that falls outside
/// the decoded frame's pixels, is refused — `crop_imm` would panic on the
/// latter, and a panic in a command takes the flow down with it.
pub fn crop_png(png_base64: &str, rect: &NormalizedRect) -> Result<CroppedPayload, AppCommandError> {
    let bytes = STANDARD.decode(png_base64)
        .map_err(|e| AppCommandError::invalid_input(format!("the frame is not valid base64: {e}")))?;
    let image = image::load_from_memory(&bytes)
        .map_err(|e| AppCommandError::invalid_input(format!("the frame could not be decoded: {e}")))?;
    if rect.x.saturating_add(rect.width) > image.width()
        || rect.y.saturating_add(rect.height) > image.height()
    {
        return Err(AppCommandError::invalid_input(format!(
            "the selection {}x{} at ({},{}) falls outside the {}x{} frame",
            rect.width,
            rect.height,
            rect.x,
            rect.y,
            image.width(),
            image.height()
        )));
    }
    let cropped = image.crop_imm(rect.x, rect.y, rect.width, rect.height);
    let mut out = std::io::Cursor::new(Vec::new());
    cropped
        .write_to(&mut out, image::ImageFormat::Png)
        .map_err(|e| AppCommandError::invalid_input(format!("png encoding failed: {e}")))?;
    Ok(CroppedPayload {
        png_base64: STANDARD.encode(out.into_inner()),
        width: rect.width,
        height: rect.height,
        mime: "image/png".to_string(),
    })
}

// ─── Desktop commands ────────────────────────────────────────────────
//
// The capture, the overlay windows and the event back to the main window
// only exist on the desktop: xcap and the window system do not compile into
// the server or the MCP build. The store and the pure functions above stay
// ungated, so they compile — and are tested — in every runtime.

// The capture backend (xcap) ships on Windows only — see the `Cargo.toml`
// note on the target-gated dependency — so the monitor-freezing helpers and
// the `begin_selection` body that drives it carry the same gate, and every
// other desktop platform answers the command with a structured "not
// supported yet".
#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
use std::time::Duration;

#[cfg(feature = "tauri-runtime")]
use serde_json::json;
#[cfg(feature = "tauri-runtime")]
use tauri::{
    AppHandle, Emitter, Manager, PhysicalPosition, PhysicalSize, Position, Size, State, WebviewUrl,
    WebviewWindowBuilder,
};

#[cfg(feature = "tauri-runtime")]
use crate::app_error::AppErrorCode;
#[cfg(feature = "tauri-runtime")]

/// One monitor a capture covers, as `screenshot_begin_selection` reports it:
/// its physical size, never frame data.
#[cfg(feature = "tauri-runtime")]
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotMonitorInfo {
    pub index: u32,
    pub name: String,
    pub width: u32,
    pub height: u32,
}

/// What `screenshot_begin_selection` answers: the capture the frozen frames
/// are filed under, the request that started it, and the monitors it covers.
#[cfg(feature = "tauri-runtime")]
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotBeginOutcome {
    pub capture_id: String,
    pub request_id: String,
    pub monitors: Vec<ScreenshotMonitorInfo>,
}

/// One captured monitor, while it is still inside the blocking task: its
/// frozen frame plus the physical bounds its overlay window must cover —
/// `x`/`y` are negative on monitors left of or above the primary.
#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
struct CapturedMonitor {
    index: u32,
    name: String,
    x: i32,
    y: i32,
    width: u32,
    height: u32,
    frame: MonitorFramePayload,
}

/// What survives a capture per monitor once the megabyte-sized frames have
/// been filed: everything the overlay windows and the outcome need, nothing
/// they would have to copy.
#[cfg(feature = "tauri-runtime")]
struct ScreenshotMonitorPlacement {
    index: u32,
    name: String,
    x: i32,
    y: i32,
    width: u32,
    height: u32,
}

/// A monitor step refused: the xcap error as the detail, the step in the
/// message, so a failure says which monitor and which part of the capture.
#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
fn capture_failed(index: u32, what: &str, error: &xcap::XCapError) -> AppCommandError {
    AppCommandError::new(
        AppErrorCode::ExternalCommandFailed,
        format!("Failed to capture monitor {index}: {what}"),
    )
    .with_detail(error.to_string())
}

/// Capture every monitor on the blocking pool: enumerating monitors and
/// PNG-encoding full-screen frames each take hundreds of milliseconds on a
/// real desktop, nothing a UI thread should wait inside. One entry per
/// monitor, in xcap's enumeration order.
#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
fn capture_all_monitors() -> Result<Vec<CapturedMonitor>, AppCommandError> {
    let monitors = xcap::Monitor::all().map_err(|e| {
        AppCommandError::new(
            AppErrorCode::ExternalCommandFailed,
            "Failed to list the monitors to capture",
        )
        .with_detail(e.to_string())
    })?;
    let mut captured = Vec::with_capacity(monitors.len());
    for (index, monitor) in monitors.into_iter().enumerate() {
        let index = index as u32;
        let name = monitor
            .name()
            .unwrap_or_else(|_| format!("Monitor {index}"));
        let x = monitor
            .x()
            .map_err(|e| capture_failed(index, "reading its position", &e))?;
        let y = monitor
            .y()
            .map_err(|e| capture_failed(index, "reading its position", &e))?;
        let width = monitor
            .width()
            .map_err(|e| capture_failed(index, "reading its size", &e))?;
        let height = monitor
            .height()
            .map_err(|e| capture_failed(index, "reading its size", &e))?;
        let image = monitor
            .capture_image()
            .map_err(|e| capture_failed(index, "freezing its screen", &e))?;
        // The frame reports the picture's own pixels, not the monitor's
        // declared size: validate_selection_rect and crop_png bound the
        // selection against these, and only these are real.
        let (frame_width, frame_height) = (image.width(), image.height());
        let frame = MonitorFramePayload {
            monitor_index: index,
            png_base64: encode_png_base64(image),
            width: frame_width,
            height: frame_height,
        };
        captured.push(CapturedMonitor {
            index,
            name,
            x,
            y,
            width,
            height,
            frame,
        });
    }
    Ok(captured)
}

/// PNG-encode a captured frame and wrap it in base64: the wire format the
/// overlay window and the editor both read.
#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
fn encode_png_base64(image: image::RgbaImage) -> String {
    let mut out = std::io::Cursor::new(Vec::new());
    image::DynamicImage::ImageRgba8(image)
        .write_to(&mut out, image::ImageFormat::Png)
        .expect("encoding a captured frame as png");
    STANDARD.encode(out.into_inner())
}

/// Cover each captured monitor with its selection overlay: a borderless,
/// transparent, always-on-top window at the monitor's physical bounds —
/// negative `x`/`y` included, that is where the desktop actually is — that
/// loads the selection page for this capture. Positioning, sizing, showing
/// or focusing a built window failing is no reason to abandon the capture;
/// building one is.
#[cfg(feature = "tauri-runtime")]
fn open_overlay_windows(
    app: &AppHandle,
    capture_id: &str,
    placements: &[ScreenshotMonitorPlacement],
) -> Result<(), AppCommandError> {
    for monitor in placements {
        let index = monitor.index;
        let label = format!("{OVERLAY_WINDOW_PREFIX}{index}");
        let url = format!("screenshot-overlay?captureId={capture_id}&monitorIndex={index}");
        let window = WebviewWindowBuilder::new(app, &label, WebviewUrl::App(url.into()))
            .decorations(false)
            .transparent(true)
            .always_on_top(true)
            .skip_taskbar(true)
            .shadow(false)
            .visible(false)
            .resizable(false)
            .build()
            .map_err(|e| {
                AppCommandError::window(
                    format!("Failed to open the selection overlay for monitor {index}"),
                    e.to_string(),
                )
            })?;
        let _ = window.set_position(Position::Physical(PhysicalPosition::new(
            monitor.x, monitor.y,
        )));
        let _ = window.set_size(Size::Physical(PhysicalSize::new(
            monitor.width,
            monitor.height,
        )));
        let _ = window.show();
        let _ = window.set_focus();
    }
    Ok(())
}

/// Close every selection overlay still standing, however it got there.
#[cfg(feature = "tauri-runtime")]
fn close_overlay_windows(app: &AppHandle) {
    for window in app.webview_windows().into_values() {
        if window.label().starts_with(OVERLAY_WINDOW_PREFIX) {
            let _ = window.close();
        }
    }
}

/// Bring the main window back for the end of a selection flow: un-minimize
/// it, un-hide it and put it in front. Best-effort — a restore failing must
/// not fail a capture that already succeeded. Also reachable from the window
/// event hook when an overlay is destroyed out from under the flow.
#[cfg(feature = "tauri-runtime")]
pub(crate) fn restore_main_window(app: &AppHandle) {
    if let Some(main) = app.get_webview_window("main") {
        let _ = main.unminimize();
        let _ = main.show();
        let _ = main.set_focus();
    }
}

/// Start a screenshot selection: freeze every monitor into the frame store,
/// then cover each with the overlay window the user drags a selection on.
/// `hide_main` first hides the main window — for capturing what sits behind
/// codeg — and the window comes back when the flow ends, whichever way it
/// ends: confirmed, cancelled, or failed.
#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_begin_selection(
    app: AppHandle,
    state: State<'_, ScreenshotFrameStore>,
    request_id: String,
    hide_main: bool,
) -> Result<ScreenshotBeginOutcome, AppCommandError> {
    screenshot_begin_selection_core(&app, &state, request_id, hide_main).await
}

/// The begin flow over plain references — same shape the web/tauri split
/// calls `_core` helpers with — so the launch diagnostic in `lib.rs` can run
/// a real capture without threading `State` through a spawned task.
#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
pub(crate) async fn screenshot_begin_selection_core(
    app: &AppHandle,
    state: &ScreenshotFrameStore,
    request_id: String,
    hide_main: bool,
) -> Result<ScreenshotBeginOutcome, AppCommandError> {
    // One live capture at a time: registering the new one drops whatever an
    // earlier one left, frames and all.
    let capture_id = state.begin_capture(request_id.clone());
    // A second begin while the first round's overlays are still up: close
    // them before capturing, or the fresh frames would be frozen with the
    // previous round's dark overlay in the picture.
    close_overlay_windows(&app);

    // Hide the main window before the frames are frozen, not after — the
    // capture must not picture codeg itself — then give the compositor a
    // beat to actually drop it.
    let mut main_hidden = false;
    if hide_main {
        if let Some(main) = app.get_webview_window("main") {
            if main.hide().is_ok() {
                main_hidden = true;
            }
        }
        tokio::time::sleep(Duration::from_millis(300)).await;
    }

    let captured = match tauri::async_runtime::spawn_blocking(capture_all_monitors).await {
        Ok(Ok(captured)) => Ok(captured),
        Ok(Err(err)) => Err(err),
        Err(err) => Err(AppCommandError::new(
            AppErrorCode::TaskExecutionFailed,
            "The screenshot capture task failed",
        )
        .with_detail(err.to_string())),
    };

    let captured = match captured {
        Ok(captured) => captured,
        Err(err) => {
            // The capture failed, possibly with the main window already
            // hidden: undo everything before reporting, or the user is left
            // staring at whatever codeg used to cover. The error is logged
            // before it travels back as a bare toast.
            tracing::error!(error = %err, "screenshot_begin_selection capture failed");
            let _ = state.cancel(&capture_id);
            if main_hidden {
                restore_main_window(&app);
            }
            return Err(err);
        }
    };

    // Split each capture into its frame (into the store) and its placement
    // (for the overlay windows), moving the megabytes once.
    let mut frames = Vec::with_capacity(captured.len());
    let mut placements = Vec::with_capacity(captured.len());
    for monitor in captured {
        placements.push(ScreenshotMonitorPlacement {
            index: monitor.index,
            name: monitor.name,
            x: monitor.x,
            y: monitor.y,
            width: monitor.width,
            height: monitor.height,
        });
        frames.push((monitor.index, monitor.frame));
    }
    state.insert_frames(&capture_id, frames);
    match open_overlay_windows(&app, &capture_id, &placements) {
        Ok(()) => Ok(ScreenshotBeginOutcome {
            capture_id,
            request_id,
            monitors: placements
                .into_iter()
                .map(|placement| ScreenshotMonitorInfo {
                    index: placement.index,
                    name: placement.name,
                    width: placement.width,
                    height: placement.height,
                })
                .collect(),
        }),
        Err(err) => {
            // Half-opened overlays would linger over the desktop: take them
            // down, undo the capture and un-hide the main window. The error
            // is logged before it travels back as a bare toast.
            tracing::error!(error = %err, "screenshot_begin_selection failed to open overlay windows");
            close_overlay_windows(&app);
            let _ = state.cancel(&capture_id);
            if main_hidden {
                restore_main_window(&app);
            }
            Err(err)
        }
    }
}

/// The non-Windows shape of `screenshot_begin_selection`: no capture backend
/// ships off Windows yet (see the `Cargo.toml` note on the target-gated xcap
/// dependency), so the command answers with a structured "no" instead of
/// missing from the API — the frontend gets a readable toast rather than an
/// unknown-command error, and nothing here touches any window state.
#[cfg(all(feature = "tauri-runtime", not(target_os = "windows")))]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_begin_selection(
    _app: AppHandle,
    _state: State<'_, ScreenshotFrameStore>,
    _request_id: String,
    _hide_main: bool,
) -> Result<ScreenshotBeginOutcome, AppCommandError> {
    Err(AppCommandError::new(
        AppErrorCode::ExternalCommandFailed,
        "Screen capture is not supported on this platform yet",
    )
    .with_detail(
        "the screenshot feature ships on Windows first; macOS and Linux support are planned with their own capture path"
            .to_string(),
    ))
}

/// Hand a frozen frame to its overlay window: the full-monitor PNG the
/// overlay draws the selection on top of. A missing frame means the overlay
/// is stale — its capture was cancelled, superseded, or already finished —
/// and the overlay closes itself on this error.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_get_frame(
    state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
    monitor_index: u32,
) -> Result<MonitorFramePayload, AppCommandError> {
    state
        .get_frame(&capture_id, monitor_index)
        .ok_or_else(|| {
            AppCommandError::not_found(format!(
                "No frozen frame for capture {capture_id} monitor {monitor_index}"
            ))
            .with_detail("the capture was cancelled, superseded, or already finished".to_string())
        })
}

/// Finish a selection: the overlay hands over the region it confirmed —
/// either as raw rect coordinates (Rust crops the frozen frame) or, when the
/// person annotated in place, as the finished annotated picture itself. The
/// result is filed for the composer, every overlay closes, and the main
/// window is told a capture is waiting. The event carries metadata only — a
/// base64 frame injected into the webview would freeze the UI for seconds —
/// the composer pulls the pixels with `screenshot_take_cropped`.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_finish_selection(
    app: AppHandle,
    state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
    monitor_index: u32,
    rect: ScreenshotRect,
    annotated_png_base64: Option<String>,
) -> Result<(), AppCommandError> {
    let frame = state
        .get_frame(&capture_id, monitor_index)
        .ok_or_else(|| {
            AppCommandError::not_found(format!(
                "No frozen frame for capture {capture_id} monitor {monitor_index}"
            ))
            .with_detail("the capture was cancelled, superseded, or already finished".to_string())
        })?;
    let normalized = validate_selection_rect(&rect, frame.width, frame.height)?;
    // The annotated composite is already the finished picture; a bare rect
    // still needs the full-monitor PNG cropped — both decode megabytes, so
    // neither belongs on the UI thread.
    let cropped = match annotated_png_base64 {
        Some(png) => tauri::async_runtime::spawn_blocking(move || {
            annotated_png(&png, &normalized)
        })
        .await
        .map_err(|e| {
            AppCommandError::new(
                AppErrorCode::TaskExecutionFailed,
                "The screenshot crop task failed",
            )
            .with_detail(e.to_string())
        })??,
        None => {
            let png = frame.png_base64;
            tauri::async_runtime::spawn_blocking(move || crop_png(&png, &normalized))
                .await
                .map_err(|e| {
                    AppCommandError::new(
                        AppErrorCode::TaskExecutionFailed,
                        "The screenshot crop task failed",
                    )
                    .with_detail(e.to_string())
                })??
        }
    };
    let meta = state
        .store_cropped(&capture_id, cropped)
        .ok_or_else(|| {
            AppCommandError::not_found(format!("Capture {capture_id} is gone")).with_detail(
                "the capture was cancelled or superseded while the selection was being cropped"
                    .to_string(),
            )
        })?;

    close_overlay_windows(&app);
    app.emit_to(
        "main",
        "screenshot://captured",
        json!({
            "captureId": capture_id,
            "requestId": meta.request_id,
            "width": meta.width,
            "height": meta.height,
            "mime": "image/png",
        }),
    )
    .map_err(|e| {
        AppCommandError::window(
            "Failed to notify the main window of the capture",
            e.to_string(),
        )
    })?;
    restore_main_window(&app);
    Ok(())
}

/// Hand the cropped selection to the editor and release it: one pull, and
/// the whole capture is gone from the store.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_take_cropped(
    state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
) -> Result<CroppedPayload, AppCommandError> {
    state
        .take_cropped(&capture_id)
        .ok_or_else(|| {
            AppCommandError::not_found(format!("No cropped image for capture {capture_id}"))
                .with_detail("the capture was cancelled, superseded, or already taken".to_string())
        })
}

/// End a selection without confirming one: drop the capture, close the
/// overlays and bring the main window back. A capture that already confirmed
/// its selection is left alone — the overlay's parting cancel (it unloads as
/// the capture finishes) must not take the cropped image down with it.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_cancel_selection(
    app: AppHandle,
    state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
) -> Result<(), AppCommandError> {
    if state.cancel(&capture_id) {
        close_overlay_windows(&app);
        restore_main_window(&app);
    }
    Ok(())
}

/// File the overlay's finished annotated picture: decode it, pin its size to
/// what the selection claims (the overlay composites exactly the selected
/// region — a mismatch means a stale overlay raced the crop), and wrap it as
/// the payload the composer attaches.
pub fn annotated_png(
    png_base64: &str,
    rect: &NormalizedRect,
) -> Result<CroppedPayload, AppCommandError> {
    let bytes = STANDARD
        .decode(png_base64)
        .map_err(|e| AppCommandError::invalid_input(format!("the capture is not valid base64: {e}")))?;
    let image = image::load_from_memory(&bytes)
        .map_err(|e| AppCommandError::invalid_input(format!("the capture could not be decoded: {e}")))?;
    if image.width() != rect.width || image.height() != rect.height {
        return Err(AppCommandError::invalid_input(format!(
            "the annotated capture {}x{} does not match the selection {}x{}",
            image.width(),
            image.height(),
            rect.width,
            rect.height
        )));
    }
    Ok(CroppedPayload {
        png_base64: STANDARD.encode(bytes),
        width: rect.width,
        height: rect.height,
        mime: "image/png".to_string(),
    })
}

#[cfg(test)]
mod tests {
    use base64::{engine::general_purpose::STANDARD, Engine as _};
    use image::{DynamicImage, ImageFormat, RgbImage};

    use super::*;
    use crate::app_error::AppErrorCode;

    /// A 100×80 PNG whose pixel (x, y) is a deterministic function of its
    /// coordinates, so a cropped copy can be checked pixel-for-pixel.
    fn pattern_frame_base64() -> String {
        let img = RgbImage::from_fn(100, 80, |x, y| {
            image::Rgb([(x * 2) as u8, (y * 3) as u8, ((x + y) % 251) as u8])
        });
        let mut out = std::io::Cursor::new(Vec::new());
        DynamicImage::ImageRgb8(img)
            .write_to(&mut out, ImageFormat::Png)
            .unwrap();
        STANDARD.encode(out.into_inner())
    }

    /// A stand-in frozen frame; the store never looks inside the payload.
    fn frame(monitor_index: u32) -> MonitorFramePayload {
        MonitorFramePayload {
            monitor_index,
            png_base64: String::new(),
            width: 100,
            height: 80,
        }
    }

    /// A stand-in cropped image; likewise opaque to the store.
    fn cropped(width: u32, height: u32) -> CroppedPayload {
        CroppedPayload {
            png_base64: "cG5n".to_string(),
            width,
            height,
            mime: "image/png".to_string(),
        }
    }

    // ── validate_selection_rect ──────────────────────────────────────────

    #[test]
    fn a_selection_hanging_off_the_frame_is_clamped_to_it() {
        // Negative edges pull in to the frame's origin, run-off edges to its
        // far corner: the crop can only ever land on the frame's pixels.
        let negative = ScreenshotRect {
            x: -3.6,
            y: -0.2,
            width: 50.4,
            height: 40.9,
        };
        assert_eq!(
            validate_selection_rect(&negative, 100, 80).unwrap(),
            NormalizedRect {
                x: 0,
                y: 0,
                width: 47,
                height: 41
            }
        );
        let overflow = ScreenshotRect {
            x: 90.0,
            y: 70.0,
            width: 500.0,
            height: 500.0,
        };
        assert_eq!(
            validate_selection_rect(&overflow, 100, 80).unwrap(),
            NormalizedRect {
                x: 90,
                y: 70,
                width: 10,
                height: 10
            }
        );
    }

    #[test]
    fn a_selection_inside_the_frame_rounds_to_whole_pixels() {
        let fractional = ScreenshotRect {
            x: 10.4,
            y: 20.6,
            width: 30.3,
            height: 40.7,
        };
        assert_eq!(
            validate_selection_rect(&fractional, 100, 80).unwrap(),
            NormalizedRect {
                x: 10,
                y: 21,
                width: 31,
                height: 40
            }
        );
    }

    #[test]
    fn a_selection_smaller_than_a_pixel_is_refused() {
        // A click without a drag: zero area.
        let click = ScreenshotRect {
            x: 40.0,
            y: 30.0,
            width: 0.0,
            height: 0.0,
        };
        let err = validate_selection_rect(&click, 100, 80).unwrap_err();
        assert!(matches!(err.code, AppErrorCode::InvalidInput));
        // Half a pixel wide: rounds down to nothing.
        let subpixel = ScreenshotRect {
            x: 40.0,
            y: 30.0,
            width: 0.4,
            height: 20.0,
        };
        assert!(validate_selection_rect(&subpixel, 100, 80).is_err());
        // The far edge before the near one: degenerate.
        let backwards = ScreenshotRect {
            x: 40.0,
            y: 30.0,
            width: -10.0,
            height: 20.0,
        };
        assert!(validate_selection_rect(&backwards, 100, 80).is_err());
        // Entirely off the frame: clamps down to nothing.
        let outside = ScreenshotRect {
            x: 200.0,
            y: 200.0,
            width: 50.0,
            height: 50.0,
        };
        assert!(validate_selection_rect(&outside, 100, 80).is_err());
    }

    #[test]
    fn a_selection_with_a_non_finite_coordinate_is_refused() {
        let nan = ScreenshotRect {
            x: f64::NAN,
            y: 0.0,
            width: 10.0,
            height: 10.0,
        };
        assert!(validate_selection_rect(&nan, 100, 80).is_err());
        let infinite_width = ScreenshotRect {
            x: 0.0,
            y: 0.0,
            width: f64::INFINITY,
            height: 10.0,
        };
        assert!(validate_selection_rect(&infinite_width, 100, 80).is_err());
        let negative_infinite_height = ScreenshotRect {
            x: 0.0,
            y: 0.0,
            width: 10.0,
            height: f64::NEG_INFINITY,
        };
        assert!(validate_selection_rect(&negative_infinite_height, 100, 80).is_err());
    }

    // ── crop_png ─────────────────────────────────────────────────────────

    #[test]
    fn a_crop_cuts_exactly_the_requested_pixels_out_of_the_frame() {
        let frame = pattern_frame_base64();
        let payload =
            crop_png(&frame, &NormalizedRect { x: 5, y: 5, width: 10, height: 10 }).unwrap();
        assert_eq!(payload.width, 10);
        assert_eq!(payload.height, 10);
        assert_eq!(payload.mime, "image/png");
        let cropped = image::load_from_memory(&STANDARD.decode(&payload.png_base64).unwrap())
            .unwrap()
            .to_rgba8();
        assert_eq!((cropped.width(), cropped.height()), (10, 10));
        let source = image::load_from_memory(&STANDARD.decode(&frame).unwrap())
            .unwrap()
            .to_rgba8();
        assert_eq!(cropped.get_pixel(0, 0), source.get_pixel(5, 5));
    }

    #[test]
    fn a_crop_outside_the_frame_is_refused_not_panicked() {
        let frame = pattern_frame_base64();
        let beyond = NormalizedRect {
            x: 95,
            y: 75,
            width: 10,
            height: 10,
        };
        let err = crop_png(&frame, &beyond).unwrap_err();
        assert!(matches!(err.code, AppErrorCode::InvalidInput));
    }

    #[test]
    fn a_crop_of_garbage_is_refused() {
        assert!(
            crop_png("not base64!!!", &NormalizedRect { x: 0, y: 0, width: 10, height: 10 })
                .is_err()
        );
        let not_an_image = STANDARD.encode(b"hello");
        assert!(
            crop_png(&not_an_image, &NormalizedRect { x: 0, y: 0, width: 10, height: 10 }).is_err()
        );
    }

    // ── annotated_png ────────────────────────────────────────────────────

    #[test]
    fn an_annotated_capture_is_filed_at_the_selections_size() {
        // The overlay composites exactly the selected region: the payload
        // keeps those dimensions and re-encodes the bytes it was handed.
        let frame = pattern_frame_base64();
        let source = image::load_from_memory(&STANDARD.decode(&frame).unwrap()).unwrap();
        let mut cropped = source.crop_imm(5, 5, 10, 10).to_rgba8();
        // A drawn mark: one pixel changed, still a valid PNG of 10x10.
        cropped.get_pixel_mut(0, 0).0 = [255, 0, 0, 255];
        let mut out = std::io::Cursor::new(Vec::new());
        DynamicImage::ImageRgba8(cropped)
            .write_to(&mut out, ImageFormat::Png)
            .unwrap();
        let base64 = STANDARD.encode(out.into_inner());

        let payload = annotated_png(&base64, &NormalizedRect { x: 5, y: 5, width: 10, height: 10 })
            .unwrap();
        assert_eq!((payload.width, payload.height), (10, 10));
        assert_eq!(payload.mime, "image/png");
        let decoded = image::load_from_memory(&STANDARD.decode(&payload.png_base64).unwrap())
            .unwrap()
            .to_rgba8();
        assert_eq!(decoded.get_pixel(0, 0).0[..3], [255, 0, 0]);
    }

    #[test]
    fn an_annotated_capture_off_size_is_refused() {
        let frame = pattern_frame_base64();
        let source = image::load_from_memory(&STANDARD.decode(&frame).unwrap()).unwrap();
        let mut out = std::io::Cursor::new(Vec::new());
        DynamicImage::ImageRgb8(source.to_rgb8())
            .write_to(&mut out, ImageFormat::Png)
            .unwrap();
        let base64 = STANDARD.encode(out.into_inner());
        let err = annotated_png(&base64, &NormalizedRect { x: 0, y: 0, width: 9, height: 9 })
            .unwrap_err();
        assert!(matches!(err.code, AppErrorCode::InvalidInput));
    }

    // ── ScreenshotFrameStore ─────────────────────────────────────────────

    #[test]
    #[ignore] // diagnostic probe: runs a REAL capture on this machine
    fn diagnostic_probe_real_capture() {
        match capture_all_monitors() {
            Ok(captured) => {
                for m in &captured {
                    println!(
                        "monitor {} {:?} at ({},{}) {}x{} frame {}x{} png {} bytes",
                        m.index,
                        m.name,
                        m.x,
                        m.y,
                        m.width,
                        m.height,
                        m.frame.width,
                        m.frame.height,
                        m.frame.png_base64.len()
                    );
                }
                assert!(!captured.is_empty(), "no monitors captured");
            }
            Err(e) => panic!("capture failed: {e}"),
        }
    }

    #[test]
    fn frames_go_in_and_come_back_per_monitor() {
        let store = ScreenshotFrameStore::default();
        let id = store.begin_capture("req-1".to_string());
        store.insert_frames(&id, vec![(0, frame(0)), (1, frame(1))]);
        assert_eq!(store.get_frame(&id, 0).unwrap().monitor_index, 0);
        assert_eq!(store.get_frame(&id, 1).unwrap().monitor_index, 1);
        assert!(store.get_frame(&id, 2).is_none());
        // A capture that no longer exists has nowhere to put frames: they
        // are dropped, not resurrected under a fresh entry.
        store.insert_frames("gone", vec![(0, frame(0))]);
        assert!(store.get_frame("gone", 0).is_none());
    }

    #[test]
    fn a_second_begin_drops_the_first_capture() {
        let store = ScreenshotFrameStore::default();
        let first = store.begin_capture("req-1".to_string());
        store.insert_frames(&first, vec![(0, frame(0))]);
        let second = store.begin_capture("req-2".to_string());
        assert_ne!(first, second);
        assert!(store.get_frame(&first, 0).is_none());
        assert!(store.get_frame(&second, 0).is_none());
    }

    #[test]
    fn cancel_after_a_crop_is_a_no_op_and_the_editor_still_gets_its_image() {
        let store = ScreenshotFrameStore::default();
        let id = store.begin_capture("req-1".to_string());
        let meta = store.store_cropped(&id, cropped(320, 240)).unwrap();
        assert_eq!(meta.request_id, "req-1");
        assert_eq!(meta.width, 320);
        assert_eq!(meta.height, 240);
        // The overlay's parting cancel (it unloads as the capture finishes)
        // must not take the cropped image down with it.
        assert!(!store.cancel(&id));
        let taken = store.take_cropped(&id).unwrap();
        assert_eq!(taken.width, 320);
        assert_eq!(taken.height, 240);
        // One pull only: taking deletes the whole entry.
        assert!(store.take_cropped(&id).is_none());
    }

    #[test]
    fn cancel_without_a_crop_removes_the_entry() {
        let store = ScreenshotFrameStore::default();
        let id = store.begin_capture("req-1".to_string());
        store.insert_frames(&id, vec![(0, frame(0))]);
        assert!(store.cancel(&id));
        assert!(store.get_frame(&id, 0).is_none());
        // Nothing left to cancel.
        assert!(!store.cancel(&id));
    }

    #[test]
    fn the_pending_sweep_cancels_a_pending_capture_and_spares_a_cropped_one() {
        let store = ScreenshotFrameStore::default();
        // A capture killed out from under the flow (Alt+F4 on its overlay):
        // frames in, nothing cropped — the sweep takes it.
        let pending = store.begin_capture("req-1".to_string());
        store.insert_frames(&pending, vec![(0, frame(0))]);
        assert!(store.cancel_all_pending());
        assert!(store.get_frame(&pending, 0).is_none());
        // Nothing pending left: a second sweep reports as much.
        assert!(!store.cancel_all_pending());
        // One live capture at a time, so a cropped one is all the sweep can
        // ever meet: the editor has not pulled the image yet — it must be
        // left alone.
        let confirmed = store.begin_capture("req-2".to_string());
        store.store_cropped(&confirmed, cropped(320, 240)).unwrap();
        assert!(!store.cancel_all_pending());
        assert!(store.take_cropped(&confirmed).is_some());
    }
}
