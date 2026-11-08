//! Pure mapping from the helper's frozen macOS displays to composer frames.

use base64::{engine::general_purpose::STANDARD, Engine as _};

use super::{CapturedMonitor, MonitorFramePayload, ScreenshotRect, ScreenshotWindowBounds};
use crate::app_error::{AppCommandError, AppErrorCode};
use crate::computer::backend::BackendError;
use crate::computer::composer_capture::ComposerCapturedMonitor;
use crate::computer::protocol::OsPermission;

pub(super) fn validate_logical_rect(rect: &ScreenshotRect) -> Result<(), AppCommandError> {
    if ![
        rect.x,
        rect.y,
        rect.width,
        rect.height,
        rect.x + rect.width,
        rect.y + rect.height,
    ]
    .into_iter()
    .all(f64::is_finite)
        || rect.width <= 0.0
        || rect.height <= 0.0
    {
        return Err(AppCommandError::invalid_input(
            "The logical screenshot bounds must be finite with positive dimensions",
        ));
    }
    Ok(())
}

pub(super) fn map_monitor(
    raw: ComposerCapturedMonitor,
) -> Result<CapturedMonitor, AppCommandError> {
    let display = raw.display;
    let logical_bounds = ScreenshotRect {
        x: display.bounds.x,
        y: display.bounds.y,
        width: display.bounds.width,
        height: display.bounds.height,
    };
    validate_logical_rect(&logical_bounds)?;
    if display.width == 0 || display.height == 0 {
        return Err(AppCommandError::invalid_input(
            "The screenshot helper returned an empty display",
        ));
    }
    let mut windows = Vec::with_capacity(display.windows.len());
    for rect in display.windows {
        let candidate = ScreenshotRect {
            x: rect.x,
            y: rect.y,
            width: rect.width,
            height: rect.height,
        };
        validate_logical_rect(&candidate)?;
        let right = rect.x + rect.width;
        let bottom = rect.y + rect.height;
        if rect.x < 0.0
            || rect.y < 0.0
            || right > f64::from(display.width)
            || bottom > f64::from(display.height)
        {
            return Err(AppCommandError::invalid_input(
                "The screenshot helper returned window bounds outside the frozen image",
            ));
        }
        let x = rect.x.floor() as u32;
        let y = rect.y.floor() as u32;
        windows.push(ScreenshotWindowBounds {
            x,
            y,
            width: right.ceil() as u32 - x,
            height: bottom.ceil() as u32 - y,
        });
    }
    Ok(CapturedMonitor {
        index: display.index,
        name: display.name,
        // macOS placement always uses logical_bounds below.
        x: 0,
        y: 0,
        width: display.width,
        height: display.height,
        logical_bounds: Some(logical_bounds.clone()),
        frame: MonitorFramePayload {
            monitor_index: display.index,
            png_base64: STANDARD.encode(raw.png),
            width: display.width,
            height: display.height,
            windows,
            logical_bounds: Some(logical_bounds),
        },
    })
}

pub(super) fn backend_error(error: BackendError) -> AppCommandError {
    let detail = error.to_string();
    match error {
        BackendError::PermissionMissing(OsPermission::ScreenRecording) => AppCommandError::permission_denied(
            "Allow Screen Recording for codeg-computer-helper in System Settings, then retry the screenshot",
        ).with_detail(detail),
        BackendError::PermissionMissing(_) => AppCommandError::permission_denied(
            "The screenshot helper is missing an operating-system permission",
        ).with_detail(detail),
        BackendError::Unavailable(_) => AppCommandError::dependency_missing(
            "The screenshot helper could not start; check the installation and retry",
        ).with_detail(detail),
        _ => AppCommandError::new(AppErrorCode::ExternalCommandFailed, "The screenshot helper could not capture the displays")
            .with_detail(detail),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::computer::protocol::ComposerDisplay;
    use crate::computer::types::Rect;

    fn monitor() -> ComposerCapturedMonitor {
        ComposerCapturedMonitor {
            display: ComposerDisplay {
                index: 2,
                display_id: 42,
                name: "Retina".into(),
                bounds: Rect {
                    x: -1440.0,
                    y: -100.0,
                    width: 1440.0,
                    height: 900.0,
                },
                width: 2880,
                height: 1800,
                windows: vec![
                    Rect {
                        x: 20.0,
                        y: 40.0,
                        width: 600.0,
                        height: 400.0,
                    },
                    Rect {
                        x: 0.0,
                        y: 0.0,
                        width: 1000.0,
                        height: 800.0,
                    },
                ],
                png_bytes: 3,
            },
            png: vec![1, 2, 3],
        }
    }

    #[test]
    fn retina_frame_keeps_pixels_and_negative_logical_placement_separate() {
        let mapped = map_monitor(monitor()).unwrap();
        assert_eq!((mapped.frame.width, mapped.frame.height), (2880, 1800));
        assert_eq!((mapped.width, mapped.height), (2880, 1800));
        assert_eq!(mapped.frame.png_base64, "AQID");
        assert_eq!((mapped.index, mapped.name.as_str()), (2, "Retina"));
        assert_eq!((mapped.x, mapped.y), (0, 0));
        let placement = mapped.logical_bounds.unwrap();
        assert_eq!(
            (placement.x, placement.y, placement.width, placement.height),
            (-1440.0, -100.0, 1440.0, 900.0)
        );
        let serialized = serde_json::to_value(&mapped.frame).unwrap();
        assert_eq!(
            serialized["logicalBounds"],
            serde_json::json!({
                "x": -1440.0, "y": -100.0, "width": 1440.0, "height": 900.0,
            })
        );
        let bounds = mapped.frame.logical_bounds.unwrap();
        assert_eq!(
            (bounds.x, bounds.y, bounds.width, bounds.height),
            (-1440.0, -100.0, 1440.0, 900.0)
        );
        assert_eq!(
            mapped.frame.windows,
            vec![
                ScreenshotWindowBounds {
                    x: 20,
                    y: 40,
                    width: 600,
                    height: 400
                },
                ScreenshotWindowBounds {
                    x: 0,
                    y: 0,
                    width: 1000,
                    height: 800
                },
            ]
        );
    }

    #[test]
    fn one_times_display_keeps_native_size() {
        let mut raw = monitor();
        raw.display.width = 1440;
        raw.display.height = 900;
        let mapped = map_monitor(raw).unwrap();
        assert_eq!((mapped.width, mapped.height), (1440, 900));
        assert_eq!(mapped.frame.windows[0].width, 600);
    }

    #[test]
    fn invalid_monitor_geometry_is_refused() {
        for bad in [f64::NAN, f64::INFINITY, 0.0, -1.0] {
            let mut raw = monitor();
            raw.display.bounds.width = bad;
            assert!(map_monitor(raw).is_err());
        }
        let mut raw = monitor();
        raw.display.width = 0;
        assert!(map_monitor(raw).is_err());
        let mut raw = monitor();
        raw.display.bounds.x = f64::INFINITY;
        assert!(map_monitor(raw).is_err());
    }

    #[test]
    fn candidates_outside_the_frozen_image_are_refused() {
        for rect in [
            Rect {
                x: -1.0,
                y: 0.0,
                width: 10.0,
                height: 10.0,
            },
            Rect {
                x: 2879.0,
                y: 0.0,
                width: 2.0,
                height: 10.0,
            },
            Rect {
                x: 0.0,
                y: 0.0,
                width: f64::NAN,
                height: 10.0,
            },
            Rect {
                x: 0.0,
                y: 0.0,
                width: 10.0,
                height: 0.0,
            },
        ] {
            let mut raw = monitor();
            raw.display.windows = vec![rect];
            assert!(map_monitor(raw).is_err());
        }
    }

    #[test]
    fn fractional_candidate_edges_cover_their_pixels() {
        let mut raw = monitor();
        raw.display.windows = vec![Rect {
            x: 10.25,
            y: 20.75,
            width: 30.5,
            height: 40.5,
        }];
        assert_eq!(
            map_monitor(raw).unwrap().frame.windows,
            vec![ScreenshotWindowBounds {
                x: 10,
                y: 20,
                width: 31,
                height: 42
            }]
        );
    }

    #[test]
    fn logical_pin_rect_allows_negative_origins_but_refuses_invalid_sizes() {
        let rect = ScreenshotRect {
            x: -1200.5,
            y: -300.0,
            width: 400.5,
            height: 200.0,
        };
        assert!(validate_logical_rect(&rect).is_ok());
        for bad in [f64::NAN, f64::INFINITY, 0.0, -1.0] {
            assert!(validate_logical_rect(&ScreenshotRect {
                width: bad,
                ..rect.clone()
            })
            .is_err());
        }
    }

    #[test]
    fn permission_and_missing_helper_errors_keep_machine_readable_codes() {
        assert!(matches!(
            backend_error(BackendError::PermissionMissing(
                OsPermission::ScreenRecording
            ))
            .code,
            AppErrorCode::PermissionDenied
        ));
        assert!(matches!(
            backend_error(BackendError::Unavailable("missing helper".into())).code,
            AppErrorCode::DependencyMissing
        ));
    }
}
