//! The Rust half of the composer's screenshot button: where the frozen
//! monitor frames live between "capture" and "crop", and the math that turns
//! a selection rectangle into pixels.
//!
//! Two halves, deliberately separable:
//!
//! * [`ScreenshotFrameStore`] — the in-memory warehouse behind the button. One
//!   live capture at a time (a second `begin_capture` drops the first, so a
//!   stale overlay window can never serve a stale frame), frames in, cropped
//!   image out, everything released on finish or cancellation. It
//!   names no xcap types, so it compiles — and is tested — in every runtime.
//! * [`validate_selection_rect`] / [`crop_png`] — pure functions over the
//!   pixels, no runtime behind them: round and clamp a selection to the
//!   frame, then cut it out of a PNG.

use std::collections::HashMap;
use std::path::Path;
#[cfg(any(feature = "tauri-runtime", test))]
use std::path::PathBuf;
use std::sync::{Mutex, MutexGuard};

use base64::{engine::general_purpose::STANDARD, Engine as _};
use serde::{Deserialize, Serialize};

use crate::app_error::AppCommandError;

#[cfg(any(test, all(feature = "tauri-runtime", target_os = "macos")))]
mod macos;
#[cfg(any(test, all(feature = "tauri-runtime", target_os = "windows")))]
mod windows;

/// Overlay labels carry the capture UUID and monitor index. Lifecycle
/// callbacks only act on the capture that owned the closing window.
pub const OVERLAY_WINDOW_PREFIX: &str = "screenshot-overlay-";

/// The label prefix every pin window carries, followed by the pinned file's
/// id (`screenshot-pin-1a2b3c4d`): the window-event hook in lib.rs derives
/// the picture a destroyed pin owned from it — label + `.png`, under the
/// pins directory `screenshot_pin` filed it in.
pub const PIN_WINDOW_PREFIX: &str = "screenshot-pin-";

#[cfg(any(feature = "tauri-runtime", test))]
fn overlay_label(capture_id: &str, monitor_index: u32) -> String {
    format!("{OVERLAY_WINDOW_PREFIX}{capture_id}-{monitor_index}")
}

#[cfg(any(feature = "tauri-runtime", test))]
pub(crate) fn overlay_capture_id(label: &str) -> Option<&str> {
    let (capture_id, monitor) = label
        .strip_prefix(OVERLAY_WINDOW_PREFIX)?
        .rsplit_once('-')?;
    uuid::Uuid::parse_str(capture_id).ok()?;
    monitor.parse::<u32>().ok()?;
    Some(capture_id)
}

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

/// A native top-level window clipped to one monitor's frozen frame, in
/// monitor-local physical pixels. Candidates are ordered front-to-back.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotWindowBounds {
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
    pub windows: Vec<ScreenshotWindowBounds>,
    /// macOS desktop points, frozen with the pixels for overlay and pin placement.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub logical_bounds: Option<ScreenshotRect>,
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
/// frame — and the entry is removed when the capture finishes or cancels.
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
        self.begin_capture_replacing(request_id).0
    }

    fn begin_capture_replacing(&self, request_id: String) -> (String, Vec<String>) {
        let capture_id = uuid::Uuid::new_v4().to_string();
        let mut captures = self.lock();
        let previous = captures.keys().cloned().collect();
        captures.clear();
        captures.insert(
            capture_id.clone(),
            ScreenshotCapture {
                request_id,
                frames: HashMap::new(),
                cropped: None,
            },
        );
        (capture_id, previous)
    }

    #[cfg(feature = "tauri-runtime")]
    fn is_active(&self, capture_id: &str) -> bool {
        self.lock().contains_key(capture_id)
    }

    /// Release every image before closing windows. Late events then become
    /// no-ops, and an old capture can never remove its replacement.
    pub fn finish(&self, capture_id: &str) -> bool {
        self.lock().remove(capture_id).is_some()
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
pub fn crop_png(
    png_base64: &str,
    rect: &NormalizedRect,
) -> Result<CroppedPayload, AppCommandError> {
    let bytes = STANDARD.decode(png_base64).map_err(|e| {
        AppCommandError::invalid_input(format!("the frame is not valid base64: {e}"))
    })?;
    let image = image::load_from_memory(&bytes).map_err(|e| {
        AppCommandError::invalid_input(format!("the frame could not be decoded: {e}"))
    })?;
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

/// Whether a path may be read as a pinned screenshot's picture: the
/// canonicalized path must sit inside the canonicalized pins directory and
/// end in `.png`. The guard behind `screenshot_read_pin_image` — its path
/// arrives through the pin window's query string, i.e. from untrusted
/// input, and the command must never widen into a general file reader.
/// Both sides are canonicalized before the prefix check, so a `..` hop out
/// of the directory cannot masquerade as an inside one, and the extension
/// is matched case-insensitively the way Windows names files.
pub fn is_pin_image_path(path: &Path, pins_dir: &Path) -> bool {
    let Ok(resolved) = path.canonicalize() else {
        return false;
    };
    let Ok(resolved_dir) = pins_dir.canonicalize() else {
        return false;
    };
    resolved
        .extension()
        .is_some_and(|ext| ext.eq_ignore_ascii_case("png"))
        && resolved.starts_with(&resolved_dir)
}

// ─── Desktop commands ────────────────────────────────────────────────
//
// The capture, the overlay windows and the event back to the main window
// only exist on the desktop: xcap and the window system do not compile into
// the server or the MCP build. The store and the pure functions above stay
// ungated, so they compile — and are tested — in every runtime.

// Windows freezes monitors through xcap; macOS asks an authenticated helper.
// Pixel capture APIs must never be linked into the macOS main process.
#[cfg(all(
    feature = "tauri-runtime",
    any(target_os = "windows", target_os = "macos")
))]
use std::time::Duration;

#[cfg(feature = "tauri-runtime")]
use tauri::{
    AppHandle, Manager, PhysicalPosition, PhysicalSize, Position, Size, State, WebviewUrl,
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
#[cfg(any(
    test,
    all(
        feature = "tauri-runtime",
        any(target_os = "windows", target_os = "macos")
    )
))]
struct CapturedMonitor {
    index: u32,
    name: String,
    x: i32,
    y: i32,
    width: u32,
    height: u32,
    logical_bounds: Option<ScreenshotRect>,
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
    logical_bounds: Option<ScreenshotRect>,
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
fn capture_all_monitors(
    excluded_overlays: &[isize],
) -> Result<Vec<CapturedMonitor>, AppCommandError> {
    let monitors = xcap::Monitor::all().map_err(|e| {
        AppCommandError::new(
            AppErrorCode::ExternalCommandFailed,
            "Failed to list the monitors to capture",
        )
        .with_detail(e.to_string())
    })?;
    // Main-window hiding and old-overlay cleanup have completed. Freeze one
    // z-ordered list alongside the monitor frames, before opening new overlays.
    let window_bounds = windows::collect_window_bounds(excluded_overlays);
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
            windows: windows::windows_for_frame(
                &window_bounds,
                windows::DesktopBounds::from_monitor(x, y, width, height),
                frame_width,
                frame_height,
            ),
            logical_bounds: None,
        };
        captured.push(CapturedMonitor {
            index,
            name,
            x,
            y,
            width,
            height,
            logical_bounds: None,
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
async fn open_overlay_windows(
    app: &AppHandle,
    capture_id: &str,
    placements: &[ScreenshotMonitorPlacement],
) -> Result<(), AppCommandError> {
    for monitor in placements {
        if !app.state::<ScreenshotFrameStore>().is_active(capture_id) {
            return Err(AppCommandError::not_found(format!(
                "Capture {capture_id} is gone"
            )));
        }
        let index = monitor.index;
        let label = overlay_label(capture_id, index);
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
        let id = capture_id.to_string();
        let (x, y, width, height) = (monitor.x, monitor.y, monitor.width, monitor.height);
        let logical_bounds = monitor.logical_bounds.clone();
        on_main_thread(app, move |app| {
            // A cancellation may have won while the webview was building.
            if !app.state::<ScreenshotFrameStore>().is_active(&id) {
                let _ = window.close();
                return Err(AppCommandError::not_found(format!("Capture {id} is gone")));
            }
            if let Some(bounds) = logical_bounds {
                let _ = window.set_position(Position::Logical(tauri::LogicalPosition::new(
                    bounds.x, bounds.y,
                )));
                let _ = window.set_size(Size::Logical(tauri::LogicalSize::new(
                    bounds.width,
                    bounds.height,
                )));
            } else {
                let _ = window.set_position(Position::Physical(PhysicalPosition::new(x, y)));
                let _ = window.set_size(Size::Physical(PhysicalSize::new(width, height)));
            }
            let _ = window.show();
            let _ = window.set_focus();
            Ok(())
        })
        .await?;
    }
    Ok(())
}

/// Close only this capture's overlays, after removing its state so recursive
/// close callbacks become no-ops. External close skips its originating window.
#[cfg(feature = "tauri-runtime")]
fn close_overlay_windows(app: &AppHandle, capture_id: &str, except: Option<&str>) {
    for window in app.webview_windows().into_values() {
        if overlay_capture_id(window.label()) == Some(capture_id) && except != Some(window.label())
        {
            let _ = window.close();
        }
    }
}

/// Native close callbacks and command transitions run on the UI thread.
#[cfg(feature = "tauri-runtime")]
pub(crate) fn overlay_closed(app: &AppHandle, label: &str) {
    let Some(capture_id) = overlay_capture_id(label) else {
        return;
    };
    if app.state::<ScreenshotFrameStore>().cancel(capture_id) {
        close_overlay_windows(app, capture_id, Some(label));
        restore_main_window(app);
    }
}

/// Serialize state/window transitions so an old restore cannot follow a new hide.
#[cfg(feature = "tauri-runtime")]
async fn on_main_thread<T: Send + 'static>(
    app: &AppHandle,
    action: impl FnOnce(AppHandle) -> Result<T, AppCommandError> + Send + 'static,
) -> Result<T, AppCommandError> {
    let (tx, rx) = tokio::sync::oneshot::channel();
    let handle = app.clone();
    app.run_on_main_thread(move || {
        let _ = tx.send(action(handle));
    })
    .map_err(|e| AppCommandError::window("Failed to update screenshot windows", e.to_string()))?;
    rx.await
        .map_err(|e| AppCommandError::window("Screenshot window update stopped", e.to_string()))?
}

#[cfg(feature = "tauri-runtime")]
async fn cancel_capture(app: &AppHandle, capture_id: String) -> Result<(), AppCommandError> {
    on_main_thread(app, move |app| {
        if app.state::<ScreenshotFrameStore>().cancel(&capture_id) {
            close_overlay_windows(&app, &capture_id, None);
            restore_main_window(&app);
        }
        Ok(())
    })
    .await
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
#[cfg(all(
    feature = "tauri-runtime",
    any(target_os = "windows", target_os = "macos")
))]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_begin_selection(
    app: AppHandle,
    state: State<'_, ScreenshotFrameStore>,
    request_id: String,
    hide_main: bool,
) -> Result<ScreenshotBeginOutcome, AppCommandError> {
    let request = request_id.clone();
    let (capture_id, excluded_overlays) = on_main_thread(&app, move |app| {
        // Keep explicit handles for managed overlays even if closing one is
        // delayed. Never filter by caption or exclude the whole Codeg process.
        #[cfg(target_os = "windows")]
        let excluded_overlays: Vec<isize> = app
            .webview_windows()
            .into_values()
            .filter(|window| overlay_capture_id(window.label()).is_some())
            .filter_map(|window| window.hwnd().ok().map(|hwnd| hwnd.0 as isize))
            .collect();
        #[cfg(target_os = "macos")]
        let excluded_overlays: Vec<u64> = app
            .webview_windows()
            .into_values()
            .filter(|window| overlay_capture_id(window.label()).is_some())
            .filter_map(|window| {
                let ptr = window.ns_window().ok()?;
                // SAFETY: Tauri owns this live NSWindow and this closure runs
                // on the UI thread, before any of these windows are closed.
                let native = unsafe { ptr.cast::<objc2_app_kit::NSWindow>().as_ref() }?;
                u64::try_from(native.windowNumber())
                    .ok()
                    .filter(|id| *id > 0)
            })
            .collect();
        let (capture_id, previous) = app
            .state::<ScreenshotFrameStore>()
            .begin_capture_replacing(request);
        for old in previous {
            close_overlay_windows(&app, &old, None);
        }
        if hide_main {
            if let Some(main) = app.get_webview_window("main") {
                let _ = main.hide();
            }
        }
        Ok((capture_id, excluded_overlays))
    })
    .await?;
    if hide_main {
        tokio::time::sleep(Duration::from_millis(300)).await;
    }

    #[cfg(target_os = "windows")]
    let captured = match tauri::async_runtime::spawn_blocking(move || {
        capture_all_monitors(&excluded_overlays)
    })
    .await
    {
        Ok(Ok(captured)) => Ok(captured),
        Ok(Err(err)) => Err(err),
        Err(err) => Err(AppCommandError::new(
            AppErrorCode::TaskExecutionFailed,
            "The screenshot capture task failed",
        )
        .with_detail(err.to_string())),
    };

    #[cfg(target_os = "macos")]
    let captured = crate::computer::local::capture_composer_screens(excluded_overlays)
        .await
        .map_err(macos::backend_error)
        .and_then(|monitors| {
            monitors
                .into_iter()
                .map(macos::map_monitor)
                .collect::<Result<Vec<_>, _>>()
        });

    let captured = match captured {
        Ok(captured) => captured,
        Err(err) => {
            // The capture failed, possibly with the main window already
            // hidden: undo everything before reporting, or the user is left
            // staring at whatever codeg used to cover. The error is logged
            // before it travels back as a bare toast.
            tracing::error!(error = %err, "screenshot_begin_selection capture failed");
            let _ = cancel_capture(&app, capture_id.clone()).await;
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
            logical_bounds: monitor.logical_bounds,
        });
        frames.push((monitor.index, monitor.frame));
    }
    state.insert_frames(&capture_id, frames);
    match open_overlay_windows(&app, &capture_id, &placements).await {
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
            let _ = cancel_capture(&app, capture_id.clone()).await;
            Err(err)
        }
    }
}

/// Other desktop platforms retain the same wire arguments and return a
/// structured refusal without touching window state.
#[cfg(all(
    feature = "tauri-runtime",
    not(any(target_os = "windows", target_os = "macos"))
))]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_begin_selection(
    _app: AppHandle,
    _state: State<'_, ScreenshotFrameStore>,
    request_id: String,
    hide_main: bool,
) -> Result<ScreenshotBeginOutcome, AppCommandError> {
    let _ = (request_id, hide_main);
    Err(AppCommandError::new(
        AppErrorCode::ExternalCommandFailed,
        "Screen capture is not supported on this platform yet",
    )
    .with_detail("The screenshot feature is available on Windows and macOS; Linux support requires its own capture backend"))
}

/// Ask only after the person explicitly chooses to authorize screen capture.
/// The helper owns the grant; this process never requests Screen Recording.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_request_permission() -> Result<(), AppCommandError> {
    #[cfg(target_os = "macos")]
    {
        crate::computer::local::request_composer_screen_permission()
            .await
            .map_err(macos::backend_error)
    }
    #[cfg(not(target_os = "macos"))]
    {
        Ok(())
    }
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
    state.get_frame(&capture_id, monitor_index).ok_or_else(|| {
        AppCommandError::not_found(format!(
            "No frozen frame for capture {capture_id} monitor {monitor_index}"
        ))
        .with_detail("the capture was cancelled, superseded, or already finished".to_string())
    })
}

/// Finish after the overlay copies or pins the picture. Keep wire arguments
/// compatible, but release pixels instead of filing an attachment or emitting
/// the retired screenshot://captured event.
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
    let frame = state.get_frame(&capture_id, monitor_index).ok_or_else(|| {
        AppCommandError::not_found(format!(
            "No frozen frame for capture {capture_id} monitor {monitor_index}"
        ))
        .with_detail("the capture was cancelled, superseded, or already finished".to_string())
    })?;
    validate_selection_rect(&rect, frame.width, frame.height)?;
    drop(frame);
    drop(annotated_png_base64);
    on_main_thread(&app, move |app| {
        if !app.state::<ScreenshotFrameStore>().finish(&capture_id) {
            return Err(AppCommandError::not_found(format!(
                "Capture {capture_id} is gone"
            )));
        }
        close_overlay_windows(&app, &capture_id, None);
        restore_main_window(&app);
        Ok(())
    })
    .await
}

/// Hand the cropped selection to the editor and release it: one pull, and
/// the whole capture is gone from the store.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_take_cropped(
    state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
) -> Result<CroppedPayload, AppCommandError> {
    state.take_cropped(&capture_id).ok_or_else(|| {
        AppCommandError::not_found(format!("No cropped image for capture {capture_id}"))
            .with_detail("the capture was cancelled, superseded, or already taken".to_string())
    })
}

/// Write an overlay-produced capture out as a PNG file: the overlay's save
/// dialog handed back the path, this decodes the base64 and writes the
/// bytes. Saving is not finishing — the overlay stays up across it — so
/// nothing here touches windows or the frame store.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_save_to_file(
    png_base64: String,
    path: String,
) -> Result<(), AppCommandError> {
    if !path.to_lowercase().ends_with(".png") {
        return Err(AppCommandError::invalid_input(format!(
            "the save path does not name a .png file: {path}"
        )));
    }
    let bytes = STANDARD.decode(&png_base64).map_err(|e| {
        AppCommandError::invalid_input(format!("the capture is not valid base64: {e}"))
    })?;
    std::fs::write(&path, bytes).map_err(AppCommandError::io)?;
    Ok(())
}

/// The directory pinned screenshots live in: `screenshots/pins` under the
/// effective data root — `resolve_effective_data_dir`, not Tauri's raw
/// `app_data_dir`, the one-root contract every other consumer follows (see
/// the setup hook in lib.rs). Both the pin command and the window-event
/// hook that reaps dead pins' files go through here, so the two can never
/// part ways.
#[cfg(feature = "tauri-runtime")]
fn pins_dir(app: &AppHandle) -> Result<PathBuf, AppCommandError> {
    app.path()
        .app_data_dir()
        .map(|dir| {
            crate::paths::resolve_effective_data_dir(&dir)
                .join("screenshots")
                .join("pins")
        })
        .map_err(|e| {
            AppCommandError::new(
                AppErrorCode::ConfigurationMissing,
                "the app data directory is unavailable",
            )
            .with_detail(e.to_string())
        })
}

/// Decode the pinned picture and file it under `dir` as `<window-label>.png`,
/// where `<id>` is a fresh UUID's first 8 hex digits — the same id the
/// window label carries, so a dead pin's file is reachable from its label
/// alone. Returns (window label, file path). The picture is verified to
/// decode but kept byte-for-byte: the pin is whatever the overlay
/// composited, its size is nobody's business here.
#[cfg(any(feature = "tauri-runtime", test))]
fn write_pin_file(dir: &Path, png_base64: &str) -> Result<(String, PathBuf), AppCommandError> {
    let bytes = STANDARD.decode(png_base64).map_err(|e| {
        AppCommandError::invalid_input(format!("the capture is not valid base64: {e}"))
    })?;
    image::load_from_memory(&bytes).map_err(|e| {
        AppCommandError::invalid_input(format!("the capture could not be decoded: {e}"))
    })?;
    std::fs::create_dir_all(dir).map_err(AppCommandError::io)?;
    let uuid = uuid::Uuid::new_v4().to_string();
    let id = &uuid[..8];
    let label = format!("{PIN_WINDOW_PREFIX}{id}");
    let path = dir.join(format!("{label}.png"));
    std::fs::write(&path, &bytes).map_err(AppCommandError::io)?;
    Ok((label, path))
}

/// Pin the finished annotated picture to the desktop: file the PNG under
/// the pins directory, then open the frameless, always-on-top window that
/// shows it at the absolute screen position the overlay computed — a
/// QQ-style pin, standing there until its window is clicked away. The
/// window's label and the file's name carry the same id, so the window
/// event hook in lib.rs deletes the file when the window is destroyed. The
/// capture itself keeps flowing through `screenshot_finish_selection`: the
/// pin adds a floating copy; finishing releases the capture frames.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_pin(
    app: AppHandle,
    _state: State<'_, ScreenshotFrameStore>,
    png_base64: String,
    x: i32,
    y: i32,
    width: u32,
    height: u32,
    logical_rect: Option<ScreenshotRect>,
) -> Result<String, AppCommandError> {
    #[cfg(target_os = "macos")]
    if let Some(rect) = &logical_rect {
        macos::validate_logical_rect(rect)?;
    }
    #[cfg(not(target_os = "macos"))]
    let _ = logical_rect;
    let dir = pins_dir(&app)?;
    // Decode and write on the blocking pool: the picture is megabytes of
    // base64, nothing a UI thread should wait inside.
    let (label, file_path) =
        tauri::async_runtime::spawn_blocking(move || write_pin_file(&dir, &png_base64))
            .await
            .map_err(|e| {
                AppCommandError::new(
                    AppErrorCode::TaskExecutionFailed,
                    "The screenshot pin task failed",
                )
                .with_detail(e.to_string())
            })??;

    let url = format!(
        "screenshot-pin?src={}",
        urlencoding::encode(&file_path.to_string_lossy())
    );
    let window = match WebviewWindowBuilder::new(&app, &label, WebviewUrl::App(url.into()))
        .decorations(false)
        .always_on_top(true)
        .skip_taskbar(true)
        .resizable(false)
        .shadow(true)
        .visible(false)
        .build()
    {
        Ok(window) => window,
        Err(e) => {
            // No window will ever own the file now — take it back, or the
            // pins directory fills up with orphans.
            let _ = std::fs::remove_file(&file_path);
            return Err(AppCommandError::window(
                format!("Failed to open the pin window {label}"),
                e.to_string(),
            ));
        }
    };
    // Positioning, sizing or showing a built window failing is no reason
    // to abandon the pin — it is up, just perhaps misplaced. The same
    // tolerance the selection overlays get.
    #[cfg(target_os = "macos")]
    let placement = logical_rect.map(|rect| {
        (
            Position::Logical(tauri::LogicalPosition::new(rect.x, rect.y)),
            Size::Logical(tauri::LogicalSize::new(rect.width, rect.height)),
        )
    });
    #[cfg(not(target_os = "macos"))]
    let placement: Option<(Position, Size)> = None;
    let (position, size) = placement.unwrap_or_else(|| {
        (
            Position::Physical(PhysicalPosition::new(x, y)),
            Size::Physical(PhysicalSize::new(width, height)),
        )
    });
    let _ = window.set_position(position);
    let _ = window.set_size(size);
    let _ = window.show();
    Ok(label)
}

/// Hand a pin its picture back: the file's bytes as base64. The path comes
/// from the pin window's query string — untrusted input — so it passes
/// [`is_pin_image_path`] first: inside the pins directory and a `.png`, or
/// refused. (The pin page reads through this instead of any filesystem
/// plugin, exactly so this one gate stands between the window and the disk.)
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_read_pin_image(
    app: AppHandle,
    path: String,
) -> Result<String, AppCommandError> {
    let pins = pins_dir(&app)?;
    if !is_pin_image_path(Path::new(&path), &pins) {
        return Err(AppCommandError::invalid_input(format!(
            "the pin image path is outside the pins directory or not a .png file: {path}"
        )));
    }
    let bytes = std::fs::read(&path).map_err(AppCommandError::io)?;
    Ok(STANDARD.encode(bytes))
}

/// End a selection without confirming one: drop the capture, close the
/// overlays and bring the main window back. A capture that already confirmed
/// its selection is left alone — the overlay's parting cancel (it unloads as
/// the capture finishes) must not take the cropped image down with it.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn screenshot_cancel_selection(
    app: AppHandle,
    _state: State<'_, ScreenshotFrameStore>,
    capture_id: String,
) -> Result<(), AppCommandError> {
    cancel_capture(&app, capture_id).await
}

/// File the overlay's finished annotated picture: decode it, pin its size to
/// what the selection claims (the overlay composites exactly the selected
/// region — a mismatch means a stale overlay raced the crop), and wrap it as
/// the payload the composer attaches.
pub fn annotated_png(
    png_base64: &str,
    rect: &NormalizedRect,
) -> Result<CroppedPayload, AppCommandError> {
    let bytes = STANDARD.decode(png_base64).map_err(|e| {
        AppCommandError::invalid_input(format!("the capture is not valid base64: {e}"))
    })?;
    let image = image::load_from_memory(&bytes).map_err(|e| {
        AppCommandError::invalid_input(format!("the capture could not be decoded: {e}"))
    })?;
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
            windows: Vec::new(),
            logical_bounds: None,
        }
    }

    #[test]
    fn frame_serializes_frozen_window_bounds_in_camel_case() {
        let mut payload = frame(2);
        payload.windows = vec![ScreenshotWindowBounds {
            x: 10,
            y: 20,
            width: 30,
            height: 40,
        }];
        assert_eq!(
            serde_json::to_value(payload).unwrap(),
            serde_json::json!({
                "monitorIndex": 2,
                "pngBase64": "",
                "width": 100,
                "height": 80,
                "windows": [{"x": 10, "y": 20, "width": 30, "height": 40}]
            })
        );
        assert_eq!(
            serde_json::to_value(frame(0)).unwrap()["windows"],
            serde_json::json!([])
        );
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
        let payload = crop_png(
            &frame,
            &NormalizedRect {
                x: 5,
                y: 5,
                width: 10,
                height: 10,
            },
        )
        .unwrap();
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
        assert!(crop_png(
            "not base64!!!",
            &NormalizedRect {
                x: 0,
                y: 0,
                width: 10,
                height: 10
            }
        )
        .is_err());
        let not_an_image = STANDARD.encode(b"hello");
        assert!(crop_png(
            &not_an_image,
            &NormalizedRect {
                x: 0,
                y: 0,
                width: 10,
                height: 10
            }
        )
        .is_err());
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

        let payload = annotated_png(
            &base64,
            &NormalizedRect {
                x: 5,
                y: 5,
                width: 10,
                height: 10,
            },
        )
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
        let err = annotated_png(
            &base64,
            &NormalizedRect {
                x: 0,
                y: 0,
                width: 9,
                height: 9,
            },
        )
        .unwrap_err();
        assert!(matches!(err.code, AppErrorCode::InvalidInput));
    }

    // ── is_pin_image_path ────────────────────────────────────────────────

    #[test]
    fn a_png_inside_the_pins_directory_is_readable() {
        let pins = tempfile::tempdir().unwrap();
        let file = pins.path().join("pin-1a2b3c4d.png");
        std::fs::write(&file, b"png").unwrap();
        assert!(is_pin_image_path(&file, pins.path()));
    }

    #[test]
    fn a_png_extension_is_matched_case_insensitively() {
        // Windows names files without case discipline: `.PNG` opens the
        // same door `.png` does.
        let pins = tempfile::tempdir().unwrap();
        let file = pins.path().join("pin-1a2b3c4d.PNG");
        std::fs::write(&file, b"png").unwrap();
        assert!(is_pin_image_path(&file, pins.path()));
    }

    #[test]
    fn a_path_outside_the_pins_directory_is_refused() {
        let pins = tempfile::tempdir().unwrap();
        let sibling = tempfile::tempdir().unwrap();
        let file = sibling.path().join("pin-1a2b3c4d.png");
        std::fs::write(&file, b"png").unwrap();
        assert!(!is_pin_image_path(&file, pins.path()));
    }

    #[test]
    fn a_traversal_path_is_refused_even_dressed_up_as_inside() {
        // The literal path lexically lives "inside" the pins directory; the
        // canonicalize-both-sides check is what refuses it — the parent of
        // pins/ is not pins/, and that is where the traversal lands.
        let root = tempfile::tempdir().unwrap();
        let pins = root.path().join("pins");
        std::fs::create_dir(&pins).unwrap();
        let secret = root.path().join("secret.png");
        std::fs::write(&secret, b"no").unwrap();
        let dressed_up = pins.join("..").join("secret.png");
        assert!(!is_pin_image_path(&dressed_up, &pins));
    }

    #[test]
    fn a_wrong_extension_or_no_extension_is_refused() {
        let pins = tempfile::tempdir().unwrap();
        let jpg = pins.path().join("pin-1a2b3c4d.jpg");
        std::fs::write(&jpg, b"jpg").unwrap();
        assert!(!is_pin_image_path(&jpg, pins.path()));
        let bare = pins.path().join("pin-1a2b3c4d");
        std::fs::write(&bare, b"").unwrap();
        assert!(!is_pin_image_path(&bare, pins.path()));
    }

    #[test]
    fn a_path_that_names_no_file_is_refused() {
        // A path that cannot be canonicalized — nothing there, or a
        // directory in the middle of it — reads as outside, not as inside.
        let pins = tempfile::tempdir().unwrap();
        assert!(!is_pin_image_path(
            &pins.path().join("pin-absent.png"),
            pins.path()
        ));
    }

    // ── ScreenshotFrameStore ─────────────────────────────────────────────

    #[test]
    fn finish_releases_every_frame_and_crop_once() {
        let store = ScreenshotFrameStore::default();
        let id = store.begin_capture("request".into());
        store.insert_frames(&id, vec![(0, frame(0)), (1, frame(1))]);
        store.store_cropped(&id, cropped(20, 10)).unwrap();
        assert!(store.finish(&id));
        assert!(store.get_frame(&id, 0).is_none());
        assert!(store.get_frame(&id, 1).is_none());
        assert!(store.take_cropped(&id).is_none());
        assert!(!store.finish(&id));
        assert!(!store.cancel(&id));
    }

    #[test]
    fn old_overlay_events_and_finish_leave_new_capture_untouched() {
        let store = ScreenshotFrameStore::default();
        let old = store.begin_capture("old".into());
        let old_label = overlay_label(&old, 1);
        let new = store.begin_capture("new".into());
        store.insert_frames(&new, vec![(0, frame(0))]);
        assert!(!store.cancel(overlay_capture_id(&old_label).unwrap()));
        assert!(!store.finish(&old));
        assert!(store.get_frame(&new, 0).is_some());
        assert_eq!(
            overlay_capture_id(&overlay_label(&new, 2)),
            Some(new.as_str())
        );
        assert!(overlay_capture_id("screenshot-overlay-1").is_none());
        assert!(overlay_capture_id("screenshot-pin-1a2b3c4d").is_none());
    }

    #[test]
    fn cancellation_releases_images_before_sibling_close_events() {
        let store = ScreenshotFrameStore::default();
        let id = store.begin_capture("request".into());
        store.insert_frames(&id, vec![(0, frame(0)), (1, frame(1))]);
        assert!(store.cancel(&id));
        assert!(store.get_frame(&id, 0).is_none());
        assert!(store.get_frame(&id, 1).is_none());
        assert!(!store.cancel(overlay_capture_id(&overlay_label(&id, 1)).unwrap()));
        store.insert_frames(&id, vec![(0, frame(0))]);
        assert!(store.get_frame(&id, 0).is_none());
    }

    #[test]
    fn pinned_file_can_be_deleted_using_its_window_label() {
        let dir = tempfile::tempdir().unwrap();
        let (label, path) = write_pin_file(dir.path(), &pattern_frame_base64()).unwrap();
        assert!(label.starts_with("screenshot-pin-"));
        assert_eq!(path, dir.path().join(format!("{label}.png")));
        std::fs::remove_file(dir.path().join(format!("{label}.png"))).unwrap();
        assert!(!path.exists());
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
        let (second, replaced) = store.begin_capture_replacing("req-2".to_string());
        assert_eq!(replaced, vec![first.clone()]);
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
}
