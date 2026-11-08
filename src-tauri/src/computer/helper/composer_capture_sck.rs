//! ScreenCaptureKit screenshot API (macOS 14+), dynamically loaded only by
//! the native helper module. No ScreenCaptureKit framework link/load command
//! is added to the main executable. All Objective-C objects stay in callbacks;
//! only owned RGBA bytes cross to the blocking worker.
//!
//! Apple DocC contracts checked when implementing:
//! - SCScreenshotManager captureImageWithFilter:configuration:completionHandler:
//!   void callback(CGImageRef, NSError*), macOS 14.
//! - SCShareableContent getShareableContentExcludingDesktopWindows:
//!   onScreenWindowsOnly:completionHandler: void callback(SCShareableContent*, NSError*).
//! - SCContentFilter initWithDisplay:excludingWindows:, contentRect CGRect,
//!   pointPixelScale float (the latter two macOS 14).
//! - SCStreamConfiguration width/height size_t; showsCursor/capturesAudio BOOL.

use std::ffi::{c_void, CStr};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{mpsc, Arc, OnceLock};
use std::time::Duration;

use block2::RcBlock;
use objc2::rc::{autoreleasepool, Allocated, Retained};
use objc2::runtime::{AnyClass, AnyObject, Bool};
use objc2::{msg_send, sel};
use objc2_foundation::{NSArray, NSError, NSProcessInfo, NSRect};

use super::{HelperError, OwnedCg, MAX_COMPOSER_PIXELS};
use crate::computer::protocol::OsPermission;

type Pixels = (u32, u32, Vec<u8>);
type CaptureResult = Result<Pixels, HelperError>;
const CAPTURE_TIMEOUT: Duration = Duration::from_secs(20);

pub(super) fn available() -> Result<bool, HelperError> {
    if NSProcessInfo::processInfo()
        .operatingSystemVersion()
        .majorVersion
        < 14
    {
        return Ok(false);
    }
    static LOADED: OnceLock<bool> = OnceLock::new();
    let loaded = LOADED.get_or_init(|| {
        // SAFETY: absolute system-framework path. Deliberately never dlclose:
        // ObjC classes and late completion blocks must remain valid for the
        // process lifetime, including after a capture timeout.
        let handle = unsafe {
            libc::dlopen(
                c"/System/Library/Frameworks/ScreenCaptureKit.framework/ScreenCaptureKit".as_ptr(),
                libc::RTLD_NOW | libc::RTLD_LOCAL,
            )
        };
        !handle.is_null()
    });
    if !loaded {
        return Err(HelperError::failed(
            "ScreenCaptureKit could not be loaded on this macOS system",
        ));
    }
    let manager = class(c"SCScreenshotManager")?;
    let content = class(c"SCShareableContent")?;
    let filter = class(c"SCContentFilter")?;
    let config = class(c"SCStreamConfiguration")?;
    if manager.class_method(sel!(captureImageWithFilter:configuration:completionHandler:)).is_none()
        || content.class_method(sel!(getShareableContentExcludingDesktopWindows:onScreenWindowsOnly:completionHandler:)).is_none()
        || filter.instance_method(sel!(initWithDisplay:excludingWindows:)).is_none()
        || filter.instance_method(sel!(contentRect)).is_none()
        || filter.instance_method(sel!(pointPixelScale)).is_none()
        || config.instance_method(sel!(setWidth:)).is_none()
        || config.instance_method(sel!(setHeight:)).is_none()
        || config.instance_method(sel!(setShowsCursor:)).is_none()
        || config.instance_method(sel!(setCapturesAudio:)).is_none()
    {
        return Err(HelperError::failed("ScreenCaptureKit screenshot APIs are unavailable on this macOS system"));
    }
    Ok(true)
}

fn class(name: &CStr) -> Result<&'static AnyClass, HelperError> {
    AnyClass::get(name).ok_or_else(|| {
        HelperError::failed(format!(
            "ScreenCaptureKit class {} is unavailable",
            name.to_string_lossy()
        ))
    })
}

/// Called only from capture's spawn_blocking worker. Completion delivery is
/// owned by ScreenCaptureKit; this worker never waits on the application's UI
/// thread. Callback delivery still needs native macOS runtime verification.
pub(super) fn capture_display(display_id: u32) -> CaptureResult {
    let (tx, rx) = mpsc::channel();
    let cancelled = Arc::new(AtomicBool::new(false));
    let callback_cancelled = cancelled.clone();
    let content_class = class(c"SCShareableContent")?;
    let callback: RcBlock<dyn Fn(*mut AnyObject, *mut NSError)> =
        RcBlock::new(move |content: *mut AnyObject, error: *mut NSError| {
            autoreleasepool(|_| {
                if callback_cancelled.load(Ordering::Acquire) {
                    return;
                }
                let started = if !error.is_null() {
                    // SAFETY: NSError is borrowed only for this callback.
                    Err(unsafe { capture_error(error, "listing displays") })
                } else {
                    // SAFETY: content is a borrowed SCShareableContent or null.
                    // Retain now, never assume a borrowed callback value is +1.
                    let content = unsafe { Retained::retain(content) };
                    match content {
                        Some(content) => {
                            start_image(content, display_id, tx.clone(), callback_cancelled.clone())
                        }
                        None => Err(HelperError::failed(
                            "ScreenCaptureKit returned no shareable content",
                        )),
                    }
                };
                if let Err(error) = started {
                    let _ = tx.send(Err(error));
                }
            });
        });
    // SAFETY: selector and BOOL argument encodings checked above. The async
    // API copies this heap block; its closure owns all state it will access.
    unsafe {
        let _: () = msg_send![content_class,
            getShareableContentExcludingDesktopWindows: Bool::NO,
            onScreenWindowsOnly: Bool::YES,
            completionHandler: &*callback
        ];
    }
    let result = rx.recv_timeout(CAPTURE_TIMEOUT).map_err(|error| {
        HelperError::failed(format!(
            "ScreenCaptureKit screenshot did not complete: {error}"
        ))
    });
    cancelled.store(true, Ordering::Release);
    // A late callback owns its sender/cancel flag and retained objects. It can
    // safely finish after this receiver is gone; send failure drops its pixels.
    result?
}

fn start_image(
    content: Retained<AnyObject>,
    display_id: u32,
    tx: mpsc::Sender<CaptureResult>,
    cancelled: Arc<AtomicBool>,
) -> Result<(), HelperError> {
    // SAFETY: content is an SCShareableContent; displays returns NSArray<SCDisplay*>.
    let displays: Retained<NSArray<AnyObject>> = unsafe { msg_send![&*content, displays] };
    let display = displays
        .iter()
        .find(|display| {
            // SAFETY: SCDisplay.displayID is CGDirectDisplayID (uint32_t).
            let id: u32 = unsafe { msg_send![&**display, displayID] };
            id == display_id
        })
        .ok_or_else(|| {
            HelperError::failed(format!(
                "display {display_id} is not in ScreenCaptureKit's current display list"
            ))
        })?;
    let filter_class = class(c"SCContentFilter")?;
    let config_class = class(c"SCStreamConfiguration")?;
    let manager = class(c"SCScreenshotManager")?;
    let excluded = NSArray::<AnyObject>::new();
    // SAFETY: alloc/init method families give owned objects. Empty exclusions
    // capture the entire display; hiding composer overlays belongs to caller.
    let filter: Option<Retained<AnyObject>> = unsafe {
        let allocated: Allocated<AnyObject> = msg_send![filter_class, alloc];
        msg_send![allocated, initWithDisplay: &*display, excludingWindows: &*excluded]
    };
    let filter = filter
        .ok_or_else(|| HelperError::failed("ScreenCaptureKit could not create display filter"))?;
    // SAFETY: NSRect is the existing CGRect alias with correct ObjC encoding
    // and x86_64 structure-return ABI; pointPixelScale is float, not CGFloat.
    let rect: NSRect = unsafe { msg_send![&*filter, contentRect] };
    let scale: f32 = unsafe { msg_send![&*filter, pointPixelScale] };
    let width = (rect.size.width * f64::from(scale)).ceil();
    let height = (rect.size.height * f64::from(scale)).ceil();
    if !scale.is_finite()
        || scale <= 0.0
        || !width.is_finite()
        || !height.is_finite()
        || width < 1.0
        || height < 1.0
        || width > f64::from(u32::MAX)
        || height > f64::from(u32::MAX)
        || width * height > MAX_COMPOSER_PIXELS as f64
    {
        return Err(HelperError::failed(
            "ScreenCaptureKit display pixel dimensions are invalid or exceed the capture limit",
        ));
    }
    let config: Option<Retained<AnyObject>> = unsafe { msg_send![config_class, new] };
    let config = config.ok_or_else(|| {
        HelperError::failed("ScreenCaptureKit could not create screenshot configuration")
    })?;
    // SAFETY: width/height are size_t, BOOL uses the architecture-specific
    // Objective-C encoding. No pixelFormat assumption: CG normalizes the image.
    unsafe {
        let _: () = msg_send![&*config, setWidth: width as usize];
        let _: () = msg_send![&*config, setHeight: height as usize];
        let _: () = msg_send![&*config, setShowsCursor: Bool::NO];
        let _: () = msg_send![&*config, setCapturesAudio: Bool::NO];
    }
    if cancelled.load(Ordering::Acquire) {
        return Ok(());
    }
    // Keep these references in the completion block, independently of this
    // stack frame and the waiting worker. Never turn ObjC pointers into usize
    // to bypass Send, nor send ObjC/CG references across the Rust channel.
    let keep_alive = (content, display, filter.clone(), config.clone());
    let callback: RcBlock<dyn Fn(*mut c_void, *mut NSError)> =
        RcBlock::new(move |image: *mut c_void, error: *mut NSError| {
            autoreleasepool(|_| {
                let _held_until_completion = &keep_alive;
                if cancelled.load(Ordering::Acquire) {
                    return;
                }
                let result = if !error.is_null() {
                    // SAFETY: borrowed NSError stays live through this callback.
                    Err(unsafe { capture_error(error, "capturing display") })
                } else if image.is_null() {
                    Err(HelperError::failed(
                        "ScreenCaptureKit returned no screenshot image",
                    ))
                } else {
                    // SAFETY: callback's CGImage is borrowed. Retain using CF/CG,
                    // never objc Retained, and release via OwnedCg on all exits.
                    let retained = unsafe {
                        OwnedCg::new(
                            super::CGImageRetain(image),
                            super::CGImageRelease,
                            "ScreenCaptureKit image",
                        )
                    };
                    retained.and_then(|image| super::normalized_pixels(&image))
                };
                let _ = tx.send(result);
            });
        });
    // SAFETY: checked macOS14 class method, CGImageRef/NSError callback ABI.
    // The framework copies the completion block until completion, including
    // after our worker times out; the block in turn retains its request objects.
    unsafe {
        let _: () = msg_send![manager,
            captureImageWithFilter: &*filter,
            configuration: &*config,
            completionHandler: &*callback
        ];
    }
    Ok(())
}

unsafe fn capture_error(error: *mut NSError, operation: &str) -> HelperError {
    // SAFETY: caller supplies the callback's non-null borrowed NSError.
    let error = unsafe { &*error };
    // SCStreamErrorDomain / userDeclined (-3801) is explicit permission denial;
    // recheck preflight as well to catch revocation after the initial check.
    if (error.domain().to_string() == "SCStreamErrorDomain" && error.code() == -3801)
        || !crate::computer::tcc::screen_recording_granted()
    {
        return HelperError::permission_missing(OsPermission::ScreenRecording);
    }
    HelperError::failed(format!(
        "ScreenCaptureKit {operation} failed ({} {}): {}",
        error.domain(),
        error.code(),
        error.localizedDescription()
    ))
}
