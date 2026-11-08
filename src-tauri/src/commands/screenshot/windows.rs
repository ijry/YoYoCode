//! Frozen top-level window candidates and monitor-local physical-pixel clipping.

use super::ScreenshotWindowBounds;

/// Signed desktop coordinates, before clipping to a monitor. Never sent over IPC.
#[derive(Clone, Copy, Debug)]
pub(super) struct DesktopBounds {
    left: i64,
    top: i64,
    right: i64,
    bottom: i64,
}

impl DesktopBounds {
    pub(super) fn from_monitor(x: i32, y: i32, width: u32, height: u32) -> Self {
        Self {
            left: i64::from(x),
            top: i64::from(y),
            right: i64::from(x) + i64::from(width),
            bottom: i64::from(y) + i64::from(height),
        }
    }
}

/// Preserve z-order (including overlapping windows), clipping each candidate
/// independently. A frame/monitor size mismatch has no proven pixel mapping.
pub(super) fn windows_for_frame(
    windows: &[DesktopBounds],
    monitor: DesktopBounds,
    frame_width: u32,
    frame_height: u32,
) -> Vec<ScreenshotWindowBounds> {
    if monitor.right.checked_sub(monitor.left) != Some(i64::from(frame_width))
        || monitor.bottom.checked_sub(monitor.top) != Some(i64::from(frame_height))
    {
        tracing::warn!(
            ?monitor,
            frame_width,
            frame_height,
            "Screenshot frame size differs from monitor bounds; window selection disabled"
        );
        return Vec::new();
    }
    windows
        .iter()
        .filter_map(|window| {
            let left = window.left.max(monitor.left);
            let top = window.top.max(monitor.top);
            let right = window.right.min(monitor.right);
            let bottom = window.bottom.min(monitor.bottom);
            if right <= left || bottom <= top {
                return None;
            }
            // Intersection bounds are within the verified u32-sized monitor,
            // so differences fit u32 even at extreme signed desktop origins.
            Some(ScreenshotWindowBounds {
                x: u32::try_from(left.checked_sub(monitor.left)?).ok()?,
                y: u32::try_from(top.checked_sub(monitor.top)?).ok()?,
                width: u32::try_from(right.checked_sub(left)?).ok()?,
                height: u32::try_from(bottom.checked_sub(top)?).ok()?,
            })
        })
        .collect()
}

#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
pub(super) use native::collect_window_bounds;

#[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
mod native {
    use std::mem::size_of;

    use windows_sys::Win32::{
        Foundation::{GetLastError, SetLastError, BOOL, HWND, LPARAM, RECT},
        Graphics::Dwm::{DwmGetWindowAttribute, DWMWA_CLOAKED, DWMWA_EXTENDED_FRAME_BOUNDS},
        UI::WindowsAndMessaging::{
            EnumWindows, GetClassNameW, GetDesktopWindow, GetShellWindow, GetWindowLongW, IsIconic,
            IsWindow, IsWindowVisible, GWL_STYLE, WS_CHILD,
        },
    };

    use super::DesktopBounds;

    struct Enumeration<'a> {
        excluded: &'a [isize],
        desktop: HWND,
        shell: HWND,
        bounds: Vec<DesktopBounds>,
    }

    /// EnumWindows visits top-level windows in front-to-back z-order. Collect
    /// once per capture, before its overlays exist; never query again on hover.
    pub(in crate::commands::screenshot) fn collect_window_bounds(
        excluded: &[isize],
    ) -> Vec<DesktopBounds> {
        // SAFETY: These getters return borrowed system handles, never owned here.
        let mut enumeration = Enumeration {
            excluded,
            desktop: unsafe { GetDesktopWindow() },
            shell: unsafe { GetShellWindow() },
            bounds: Vec::new(),
        };
        // SAFETY: EnumWindows invokes the callback synchronously; the context
        // stays alive and exclusively borrowed until enumeration returns.
        let result = unsafe {
            EnumWindows(
                Some(visit_window),
                &mut enumeration as *mut Enumeration<'_> as LPARAM,
            )
        };
        if result == 0 {
            tracing::warn!(
                error = %std::io::Error::last_os_error(),
                "Failed to enumerate screenshot window bounds; using free selection"
            );
            return Vec::new();
        }
        enumeration.bounds
    }

    unsafe extern "system" fn visit_window(hwnd: HWND, param: LPARAM) -> BOOL {
        // SAFETY: Only collect_window_bounds installs this callback and passes
        // its live Enumeration pointer for the duration of the synchronous call.
        let enumeration = unsafe { &mut *(param as *mut Enumeration<'_>) };
        if let Some(bounds) = window_bounds(hwnd, enumeration) {
            enumeration.bounds.push(bounds);
        }
        1
    }

    fn window_bounds(hwnd: HWND, enumeration: &Enumeration<'_>) -> Option<DesktopBounds> {
        // SAFETY: All queries accept borrowed HWNDs. Windows can disappear
        // during enumeration, so every fallible query is checked and skipped.
        unsafe {
            if hwnd.is_null()
                || hwnd == enumeration.desktop
                || hwnd == enumeration.shell
                || enumeration.excluded.contains(&(hwnd as isize))
                || IsWindow(hwnd) == 0
                || IsWindowVisible(hwnd) == 0
                || IsIconic(hwnd) != 0
            {
                return None;
            }
            SetLastError(0);
            let style = GetWindowLongW(hwnd, GWL_STYLE);
            if (style == 0 && GetLastError() != 0) || (style as u32 & WS_CHILD) != 0 {
                return None;
            }
            let mut class = [0u16; 256];
            let class_len = GetClassNameW(hwnd, class.as_mut_ptr(), class.len() as i32);
            if class_len <= 0 {
                return None;
            }
            let class = String::from_utf16_lossy(&class[..class_len as usize]);
            if matches!(
                class.as_str(),
                "Progman"
                    | "WorkerW"
                    | "Shell_TrayWnd"
                    | "Shell_SecondaryTrayWnd"
                    | "MultitaskingViewFrame"
                    | "XamlExplorerHostIslandWindow"
                    | "TaskListThumbnailWnd"
            ) {
                return None;
            }
            let mut cloaked = 0u32;
            if DwmGetWindowAttribute(
                hwnd,
                DWMWA_CLOAKED as u32,
                (&mut cloaked as *mut u32).cast(),
                size_of::<u32>() as u32,
            ) < 0
                || cloaked != 0
            {
                return None;
            }
            let mut rect = RECT {
                left: 0,
                top: 0,
                right: 0,
                bottom: 0,
            };
            if DwmGetWindowAttribute(
                hwnd,
                DWMWA_EXTENDED_FRAME_BOUNDS as u32,
                (&mut rect as *mut RECT).cast(),
                size_of::<RECT>() as u32,
            ) < 0
                || rect.right <= rect.left
                || rect.bottom <= rect.top
            {
                return None;
            }
            Some(DesktopBounds {
                left: i64::from(rect.left),
                top: i64::from(rect.top),
                right: i64::from(rect.right),
                bottom: i64::from(rect.bottom),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn bounds(left: i64, top: i64, right: i64, bottom: i64) -> DesktopBounds {
        DesktopBounds {
            left,
            top,
            right,
            bottom,
        }
    }

    #[cfg(all(feature = "tauri-runtime", target_os = "windows"))]
    #[test]
    #[ignore = "manual native window metadata probe; does not capture pixels"]
    fn native_window_metadata_probe() {
        let windows = collect_window_bounds(&[]);
        println!("count={}", windows.len());
        for window in windows {
            println!(
                "left={} top={} right={} bottom={}",
                window.left, window.top, window.right, window.bottom
            );
            assert!(window.right > window.left);
            assert!(window.bottom > window.top);
        }
    }

    #[test]
    fn clips_negative_desktop_coordinates_to_monitor_local_pixels() {
        let monitor = bounds(-1920, -1080, 0, 0);
        let windows = [bounds(-2000, -1200, -1600, -800)];
        assert_eq!(
            windows_for_frame(&windows, monitor, 1920, 1080),
            vec![ScreenshotWindowBounds {
                x: 0,
                y: 0,
                width: 320,
                height: 280,
            }]
        );
    }

    #[test]
    fn a_window_crossing_monitors_is_clipped_for_each_frame() {
        let windows = [bounds(-100, 50, 200, 250)];
        assert_eq!(
            windows_for_frame(&windows, bounds(-1920, 0, 0, 1080), 1920, 1080),
            vec![ScreenshotWindowBounds {
                x: 1820,
                y: 50,
                width: 100,
                height: 200,
            }]
        );
        assert_eq!(
            windows_for_frame(&windows, bounds(0, 0, 2560, 1440), 2560, 1440),
            vec![ScreenshotWindowBounds {
                x: 0,
                y: 50,
                width: 200,
                height: 200,
            }]
        );
    }

    #[test]
    fn offscreen_empty_and_inverted_bounds_are_omitted() {
        let windows = [
            bounds(-200, 0, -1, 50),
            bounds(100, 0, 150, 50),
            bounds(0, 80, 50, 90),
            bounds(10, 10, 10, 30),
            bounds(10, 10, 30, 10),
            bounds(30, 30, 10, 10),
        ];
        assert!(windows_for_frame(&windows, bounds(0, 0, 100, 80), 100, 80).is_empty());
        assert!(windows_for_frame(&[], bounds(0, 0, 100, 80), 100, 80).is_empty());
    }

    #[test]
    fn overlapping_candidates_keep_front_to_back_order() {
        let windows = [
            bounds(20, 20, 60, 60),
            bounds(-50, -50, -10, -10),
            bounds(0, 0, 100, 80),
            bounds(20, 20, 60, 60),
        ];
        assert_eq!(
            windows_for_frame(&windows, bounds(0, 0, 100, 80), 100, 80),
            vec![
                ScreenshotWindowBounds {
                    x: 20,
                    y: 20,
                    width: 40,
                    height: 40,
                },
                ScreenshotWindowBounds {
                    x: 0,
                    y: 0,
                    width: 100,
                    height: 80,
                },
                ScreenshotWindowBounds {
                    x: 20,
                    y: 20,
                    width: 40,
                    height: 40,
                },
            ]
        );
    }

    #[test]
    fn frame_size_mismatch_disables_candidates_without_rescaling() {
        let windows = [bounds(10, 10, 90, 70)];
        let monitor = bounds(0, 0, 100, 80);
        for (width, height) in [(200, 160), (99, 80), (100, 79), (0, 0)] {
            assert!(windows_for_frame(&windows, monitor, width, height).is_empty());
        }
    }

    #[test]
    fn extreme_signed_bounds_cannot_overflow_clipping() {
        let windows = [bounds(i64::MIN, i64::MIN, i64::MAX, i64::MAX)];
        let monitor = DesktopBounds::from_monitor(i32::MIN, i32::MIN, u32::MAX, u32::MAX);
        assert_eq!(
            windows_for_frame(&windows, monitor, u32::MAX, u32::MAX),
            vec![ScreenshotWindowBounds {
                x: 0,
                y: 0,
                width: u32::MAX,
                height: u32::MAX,
            }]
        );
        assert!(windows_for_frame(&windows, windows[0], 100, 80).is_empty());
    }
}
