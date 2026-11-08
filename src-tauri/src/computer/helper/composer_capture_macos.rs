//! Composer freeze frames, only in the macOS computer-helper binary.
//!
//! macOS 14+ uses ScreenCaptureKit's screenshot manager, loaded dynamically;
//! older systems use deprecated CGDisplayCreateImage. Modern capture failures
//! never fall back to the legacy API. Both paths still need Mac build/runtime
//! acceptance. Protected content may be blank, the cursor is not included,
//! and displays/window metadata are sampled sequentially, not as an atomic
//! desktop transaction. Each returned PNG is a fixed snapshot.
//!
//! Exclusions affect hover candidates only. The caller must hide its overlays
//! before capturing; the full-display image includes all other visible pixels,
//! including ordinary codeg windows. The helper performs only a Screen
//! Recording preflight, never a prompt, Accessibility call or driver launch.
//!
//! Images are rendered to opaque 8-bit sRGB, preserving pixel dimensions but
//! not HDR precision. CoreGraphics converts native channel order, stride and
//! color space; no assumption is made about the source image's backing bytes.

#![cfg(all(target_os = "macos", feature = "computer-helper"))]

#[path = "composer_capture_sck.rs"]
mod sck;

use std::ffi::c_void;
use std::io::{self, Write};
use std::ptr::NonNull;

use core_foundation_sys::string::CFStringRef;
use image::ImageEncoder;

use super::composer_capture_geometry::{valid_rect, window_candidates};
use super::HelperError;
use crate::computer::composer_capture::{
    ComposerCapturedMonitor, MAX_COMPOSER_DISPLAYS, MAX_COMPOSER_PIXELS, MAX_COMPOSER_PNG_BYTES,
};
use crate::computer::protocol::{ComposerDisplay, OsPermission};
use crate::computer::types::Rect;

// CGRect's four contiguous CGFloat values on the supported 64-bit macOS
// targets. This matches the existing CGDisplayBounds declaration in screen.rs.
#[repr(C)]
#[derive(Clone, Copy)]
struct CgRect {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

#[link(name = "CoreGraphics", kind = "framework")]
extern "C" {
    fn CGGetActiveDisplayList(max: u32, displays: *mut u32, count: *mut u32) -> i32;
    fn CGDisplayBounds(display: u32) -> CgRect;
    fn CGDisplayCreateImage(display: u32) -> *mut c_void;
    fn CGImageGetWidth(image: *mut c_void) -> usize;
    fn CGImageGetHeight(image: *mut c_void) -> usize;
    fn CGImageRetain(image: *mut c_void) -> *mut c_void;
    fn CGImageRelease(image: *mut c_void);
    static kCGColorSpaceSRGB: CFStringRef;
    fn CGColorSpaceCreateWithName(name: CFStringRef) -> *mut c_void;
    fn CGColorSpaceRelease(space: *mut c_void);
    fn CGBitmapContextCreate(
        data: *mut c_void,
        width: usize,
        height: usize,
        bits_per_component: usize,
        bytes_per_row: usize,
        space: *mut c_void,
        bitmap_info: u32,
    ) -> *mut c_void;
    fn CGContextDrawImage(context: *mut c_void, rect: CgRect, image: *mut c_void);
    fn CGContextFlush(context: *mut c_void);
    fn CGContextRelease(context: *mut c_void);
}

/// One +1 CoreGraphics reference, paired with its matching release function.
struct OwnedCg {
    pointer: NonNull<c_void>,
    release: unsafe extern "C" fn(*mut c_void),
}

impl OwnedCg {
    /// `pointer` must be a newly created object for exactly this `release`.
    unsafe fn new(
        pointer: *mut c_void,
        release: unsafe extern "C" fn(*mut c_void),
        what: &str,
    ) -> Result<Self, HelperError> {
        let pointer = NonNull::new(pointer)
            .ok_or_else(|| HelperError::failed(format!("CoreGraphics could not create {what}")))?;
        Ok(Self { pointer, release })
    }

    fn as_ptr(&self) -> *mut c_void {
        self.pointer.as_ptr()
    }
}

impl Drop for OwnedCg {
    fn drop(&mut self) {
        // SAFETY: new paired this owned, non-null +1 reference with its release.
        unsafe { (self.release)(self.as_ptr()) }
    }
}

pub(super) fn capture(
    excluded_window_ids: &[u64],
) -> Result<Vec<crate::computer::composer_capture::ComposerCapturedMonitor>, super::HelperError> {
    // Invoked by helper dispatch's spawn_blocking, never the UI thread.
    objc2::rc::autoreleasepool(|_| capture_inner(excluded_window_ids))
}

fn capture_inner(excluded_window_ids: &[u64]) -> Result<Vec<ComposerCapturedMonitor>, HelperError> {
    if !crate::computer::tcc::screen_recording_granted() {
        return Err(HelperError::permission_missing(
            OsPermission::ScreenRecording,
        ));
    }
    let modern = sck::available()?;
    let displays = active_displays()?;
    let mut captured = Vec::with_capacity(displays.len());
    let mut total_png_bytes = 0_u64;
    for (index, display_id) in displays.into_iter().enumerate() {
        let bounds = display_bounds(display_id)?;
        // WindowServer preserves front-to-back order. This is metadata only;
        // alpha-zero windows are already excluded by its existing parser.
        let windows = super::screen::window_server_windows();
        let (width, height, rgba) = if modern {
            // Once selected, ScreenCaptureKit errors propagate without fallback.
            sck::capture_display(display_id)?
        } else {
            // SAFETY: synchronous capture on older macOS; returns +1 or null.
            let raw_image = unsafe { CGDisplayCreateImage(display_id) };
            if raw_image.is_null() && !crate::computer::tcc::screen_recording_granted() {
                return Err(HelperError::permission_missing(
                    OsPermission::ScreenRecording,
                ));
            }
            // SAFETY: the create result is owned; null is rejected by new.
            let image = unsafe { OwnedCg::new(raw_image, CGImageRelease, "display image") }?;
            normalized_pixels(&image)?
        };
        if display_bounds(display_id)? != bounds {
            return Err(HelperError::failed(
                "a display changed geometry during the screenshot; try again",
            ));
        }
        let remaining = MAX_COMPOSER_PNG_BYTES
            .checked_sub(total_png_bytes)
            .ok_or_else(|| {
                HelperError::failed("composer screenshot PNG data exceeds the session limit")
            })?;
        let png = encode_png(&rgba, width, height, remaining)?;
        let png_bytes = u64::try_from(png.len())
            .map_err(|_| HelperError::failed("composer screenshot PNG length overflow"))?;
        total_png_bytes = total_png_bytes
            .checked_add(png_bytes)
            .ok_or_else(|| HelperError::failed("composer screenshot PNG total overflow"))?;
        captured.push(ComposerCapturedMonitor {
            display: ComposerDisplay {
                index: u32::try_from(index)
                    .map_err(|_| HelperError::failed("composer display index overflow"))?,
                display_id,
                name: format!("Display {}", index + 1),
                bounds,
                width,
                height,
                windows: window_candidates(
                    &windows,
                    std::process::id(),
                    excluded_window_ids,
                    bounds,
                    width,
                    height,
                ),
                png_bytes,
            },
            png,
        });
    }
    Ok(captured)
}

fn active_displays() -> Result<Vec<u32>, HelperError> {
    fn count() -> Result<u32, HelperError> {
        let mut count = 0;
        // SAFETY: null output buffer with max=0 queries the count only.
        let error = unsafe { CGGetActiveDisplayList(0, std::ptr::null_mut(), &mut count) };
        if error != 0 {
            return Err(HelperError::failed(format!(
                "CoreGraphics could not count active displays ({error})"
            )));
        }
        if count == 0 || count as usize > MAX_COMPOSER_DISPLAYS {
            return Err(HelperError::failed(format!(
                "composer screenshot requires 1..={MAX_COMPOSER_DISPLAYS} active displays; got {count}"
            )));
        }
        Ok(count)
    }
    let before = count()?;
    // Fill the full bounded buffer, not just the count observed earlier: a
    // display connecting between calls must not silently truncate the list.
    let mut displays = vec![0; MAX_COMPOSER_DISPLAYS];
    let mut filled = 0;
    // SAFETY: the output buffer has max entries and count is a valid out pointer.
    let error = unsafe {
        CGGetActiveDisplayList(displays.len() as u32, displays.as_mut_ptr(), &mut filled)
    };
    if error != 0 {
        return Err(HelperError::failed(format!(
            "CoreGraphics could not list active displays ({error})"
        )));
    }
    let after = count()?;
    if filled == 0 || filled as usize > displays.len() || before != filled || after != filled {
        return Err(HelperError::failed(
            "the active displays changed during enumeration; try again",
        ));
    }
    displays.truncate(filled as usize);
    Ok(displays)
}

fn display_bounds(display: u32) -> Result<Rect, HelperError> {
    // SAFETY: query with a display ID; disconnected displays return empty bounds.
    let frame = unsafe { CGDisplayBounds(display) };
    let rect = Rect {
        x: frame.x,
        y: frame.y,
        width: frame.width,
        height: frame.height,
    };
    if !valid_rect(rect) {
        return Err(HelperError::failed(format!(
            "display {display} has invalid desktop bounds"
        )));
    }
    Ok(rect)
}

fn normalized_pixels(image: &OwnedCg) -> Result<(u32, u32, Vec<u8>), HelperError> {
    // SAFETY: the owned image remains alive throughout the queries and drawing.
    let (width, height) = unsafe {
        (
            CGImageGetWidth(image.as_ptr()),
            CGImageGetHeight(image.as_ptr()),
        )
    };
    let invalid_size = || {
        HelperError::failed(format!(
        "display image {width}x{height} exceeds the {MAX_COMPOSER_PIXELS}-pixel limit or is empty"
    ))
    };
    let width_u32 = u32::try_from(width).map_err(|_| invalid_size())?;
    let height_u32 = u32::try_from(height).map_err(|_| invalid_size())?;
    let pixels = u64::from(width_u32) * u64::from(height_u32);
    if pixels == 0 || pixels > MAX_COMPOSER_PIXELS {
        return Err(invalid_size());
    }
    let stride = width.checked_mul(4).ok_or_else(invalid_size)?;
    let length = stride.checked_mul(height).ok_or_else(invalid_size)?;
    let mut rgba = Vec::new();
    rgba.try_reserve_exact(length)
        .map_err(|e| HelperError::failed(format!("cannot allocate screenshot pixels: {e}")))?;
    rgba.resize(length, 0_u8);
    // SAFETY: the framework's immortal sRGB name creates a +1 color space.
    let color_space = unsafe {
        OwnedCg::new(
            CGColorSpaceCreateWithName(kCGColorSpaceSRGB),
            CGColorSpaceRelease,
            "sRGB color space",
        )
    }?;
    // kCGBitmapByteOrder32Big | kCGImageAlphaNoneSkipLast gives R,G,B,X
    // bytes regardless of CPU endianness. An opaque desktop requires no
    // premultiplied-alpha unassociation; X is explicitly made 255 below.
    const RGBX: u32 = (4 << 12) | 5;
    // SAFETY: rgba owns height*stride writable bytes and is not moved/resized
    // until the context is released. 8-bit RGBX is a supported bitmap layout.
    let context = unsafe {
        OwnedCg::new(
            CGBitmapContextCreate(
                rgba.as_mut_ptr().cast(),
                width,
                height,
                8,
                stride,
                color_space.as_ptr(),
                RGBX,
            ),
            CGContextRelease,
            "screenshot bitmap context",
        )
    }?;
    // SAFETY: all objects and the backing allocation remain alive. Drawing an
    // image into the default bitmap CTM preserves the image's row order (no
    // AppKit/UIKit flipped view transform is involved).
    unsafe {
        CGContextDrawImage(
            context.as_ptr(),
            CgRect {
                x: 0.0,
                y: 0.0,
                width: f64::from(width_u32),
                height: f64::from(height_u32),
            },
            image.as_ptr(),
        );
        CGContextFlush(context.as_ptr());
    }
    // End the context's access to the buffer before Rust accesses its bytes.
    drop(context);
    drop(color_space);
    for pixel in rgba.chunks_exact_mut(4) {
        pixel[3] = 255;
    }
    Ok((width_u32, height_u32, rgba))
}

/// Stop the encoder at the remaining session budget, rather than allocating
/// an oversized PNG and checking only after it has been fully encoded.
struct BoundedPng {
    bytes: Vec<u8>,
    limit: usize,
}

impl Write for BoundedPng {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        if bytes.len() > self.limit.saturating_sub(self.bytes.len()) {
            return Err(io::Error::other(
                "composer screenshot PNG data exceeds the 256 MiB session limit",
            ));
        }
        self.bytes
            .try_reserve_exact(bytes.len())
            .map_err(io::Error::other)?;
        self.bytes.extend_from_slice(bytes);
        Ok(bytes.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

fn encode_png(
    rgba: &[u8],
    width: u32,
    height: u32,
    remaining: u64,
) -> Result<Vec<u8>, HelperError> {
    let mut writer = BoundedPng {
        bytes: Vec::new(),
        limit: usize::try_from(remaining)
            .map_err(|_| HelperError::failed("composer screenshot PNG budget overflow"))?,
    };
    image::codecs::png::PngEncoder::new(&mut writer)
        .write_image(rgba, width, height, image::ExtendedColorType::Rgba8)
        .map_err(|e| {
            HelperError::failed(format!("composer screenshot PNG encoding failed: {e}"))
        })?;
    Ok(writer.bytes)
}
