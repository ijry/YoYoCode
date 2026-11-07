//! The Rust half of the composer's screenshot button: where the frozen
//! monitor frames live between "capture" and "crop", and the math that turns
//! a selection rectangle into pixels.
//!
//! Two halves, deliberately separable:
//!
//! * [`ScreenshotFrameStore`] — the in-memory warehouse on `AppState`. One
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
/// selection once one is confirmed. `hide_main` records whether the main
/// window was hidden for the capture, so cancellation knows whether to
/// bring it back.
struct ScreenshotCapture {
    request_id: String,
    #[allow(dead_code)] // read by the desktop commands' window-restore step
    hide_main: bool,
    frames: HashMap<u32, MonitorFramePayload>,
    cropped: Option<(CroppedPayload, ScreenshotCapturedMeta)>,
}

/// The in-memory frame warehouse behind the screenshot flow, one field on
/// `AppState`. At most one live capture at a time — `begin_capture` drops
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
    pub fn begin_capture(&self, request_id: String, hide_main: bool) -> String {
        let capture_id = uuid::Uuid::new_v4().to_string();
        let mut captures = self.lock();
        captures.clear();
        captures.insert(
            capture_id.clone(),
            ScreenshotCapture {
                request_id,
                hide_main,
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

    // ── ScreenshotFrameStore ─────────────────────────────────────────────

    #[test]
    fn frames_go_in_and_come_back_per_monitor() {
        let store = ScreenshotFrameStore::default();
        let id = store.begin_capture("req-1".to_string(), false);
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
        let first = store.begin_capture("req-1".to_string(), false);
        store.insert_frames(&first, vec![(0, frame(0))]);
        let second = store.begin_capture("req-2".to_string(), true);
        assert_ne!(first, second);
        assert!(store.get_frame(&first, 0).is_none());
        assert!(store.get_frame(&second, 0).is_none());
    }

    #[test]
    fn cancel_after_a_crop_is_a_no_op_and_the_editor_still_gets_its_image() {
        let store = ScreenshotFrameStore::default();
        let id = store.begin_capture("req-1".to_string(), false);
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
        let id = store.begin_capture("req-1".to_string(), false);
        store.insert_frames(&id, vec![(0, frame(0))]);
        assert!(store.cancel(&id));
        assert!(store.get_frame(&id, 0).is_none());
        // Nothing left to cancel.
        assert!(!store.cancel(&id));
    }
}
