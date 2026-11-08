//! Desktop-point window frames projected into a captured monitor's pixels.
//! Kept independent of native APIs so these contracts also run on Windows.

use crate::computer::types::Rect;

pub(super) fn valid_rect(rect: Rect) -> bool {
    [rect.x, rect.y, rect.width, rect.height]
        .iter()
        .all(|v| v.is_finite())
        && rect.width > 0.0
        && rect.height > 0.0
        && (rect.x + rect.width).is_finite()
        && (rect.y + rect.height).is_finite()
}

/// Intersect before rounding so an offscreen edge cannot gain a pixel.
/// Independent X/Y ratios come from the captured image, never a nominal DPI.
pub(super) fn window_pixels(window: Rect, monitor: Rect, width: u32, height: u32) -> Option<Rect> {
    if !valid_rect(window) || !valid_rect(monitor) || width == 0 || height == 0 {
        return None;
    }
    let left = window.x.max(monitor.x);
    let top = window.y.max(monitor.y);
    let right = (window.x + window.width).min(monitor.x + monitor.width);
    let bottom = (window.y + window.height).min(monitor.y + monitor.height);
    if right <= left || bottom <= top {
        return None;
    }
    // Divide before multiplying to avoid overflow for very small monitors.
    let x = |edge: f64| ((edge - monitor.x) / monitor.width) * f64::from(width);
    let y = |edge: f64| ((edge - monitor.y) / monitor.height) * f64::from(height);
    let left = x(left).floor().clamp(0.0, f64::from(width));
    let top = y(top).floor().clamp(0.0, f64::from(height));
    let right = x(right).ceil().clamp(0.0, f64::from(width));
    let bottom = y(bottom).ceil().clamp(0.0, f64::from(height));
    (right > left && bottom > top).then_some(Rect {
        x: left,
        y: top,
        width: right - left,
        height: bottom - top,
    })
}

/// WindowServer order is already front to back. Exclude only the helper PID
/// and explicitly named overlays: ordinary main-app windows remain eligible.
pub(super) fn window_candidates(
    windows: &[(u64, u32, i64, Rect)],
    helper_pid: u32,
    excluded_window_ids: &[u64],
    monitor: Rect,
    width: u32,
    height: u32,
) -> Vec<Rect> {
    windows
        .iter()
        .filter(|(id, pid, layer, _)| {
            *layer == 0 && *pid != helper_pid && !excluded_window_ids.contains(id)
        })
        .filter_map(|(_, _, _, bounds)| window_pixels(*bounds, monitor, width, height))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rect(x: f64, y: f64, width: f64, height: f64) -> Rect {
        Rect {
            x,
            y,
            width,
            height,
        }
    }

    #[test]
    fn negative_origin_retina_window_uses_frame_local_pixels() {
        assert_eq!(
            window_pixels(
                rect(-1400.0, -150.0, 200.0, 100.0),
                rect(-1440.0, -200.0, 1440.0, 900.0),
                2880,
                1800,
            ),
            Some(rect(80.0, 100.0, 400.0, 200.0))
        );
    }

    #[test]
    fn a_window_crossing_one_and_two_times_displays_is_clipped_for_each() {
        let window = rect(-100.0, 50.0, 300.0, 100.0);
        assert_eq!(
            window_pixels(window, rect(-1440.0, 0.0, 1440.0, 900.0), 1440, 900),
            Some(rect(1340.0, 50.0, 100.0, 100.0))
        );
        assert_eq!(
            window_pixels(window, rect(0.0, 0.0, 1920.0, 1080.0), 3840, 2160),
            Some(rect(0.0, 100.0, 400.0, 200.0))
        );
    }

    #[test]
    fn fractional_edges_round_outward_and_stay_inside_the_frame() {
        let monitor = rect(-10.0, -20.0, 100.0, 100.0);
        assert_eq!(
            window_pixels(rect(-9.75, -19.25, 10.1, 20.1), monitor, 200, 200),
            Some(rect(0.0, 1.0, 21.0, 41.0))
        );
        assert_eq!(
            window_pixels(rect(89.25, 79.25, 10.0, 10.0), monitor, 200, 200),
            Some(rect(198.0, 198.0, 2.0, 2.0))
        );
        assert_eq!(
            window_pixels(rect(-11.0, -21.0, 102.0, 102.0), monitor, 200, 200),
            Some(rect(0.0, 0.0, 200.0, 200.0))
        );
    }

    #[test]
    fn image_dimensions_determine_both_scales_independently() {
        assert_eq!(
            window_pixels(
                rect(10.0, 20.0, 20.0, 30.0),
                rect(0.0, 0.0, 100.0, 100.0),
                150,
                200,
            ),
            Some(rect(15.0, 40.0, 30.0, 60.0))
        );
    }

    #[test]
    fn offscreen_or_just_touching_windows_have_no_candidate() {
        let monitor = rect(0.0, 0.0, 100.0, 100.0);
        for window in [
            rect(-20.0, 0.0, 10.0, 10.0),
            rect(-10.0, 0.0, 10.0, 10.0),
            rect(100.0, 0.0, 10.0, 10.0),
            rect(0.0, -10.0, 10.0, 10.0),
            rect(0.0, 100.0, 10.0, 10.0),
        ] {
            assert_eq!(window_pixels(window, monitor, 200, 200), None);
        }
    }

    #[test]
    fn invalid_or_overflowing_geometry_and_empty_images_are_rejected() {
        let valid = rect(0.0, 0.0, 100.0, 100.0);
        for invalid in [
            rect(f64::NAN, 0.0, 10.0, 10.0),
            rect(0.0, f64::INFINITY, 10.0, 10.0),
            rect(0.0, 0.0, f64::INFINITY, 10.0),
            rect(0.0, 0.0, 10.0, f64::NAN),
            rect(0.0, 0.0, 0.0, 10.0),
            rect(0.0, 0.0, 10.0, -1.0),
            rect(f64::MAX, 0.0, f64::MAX, 10.0),
            rect(0.0, f64::MAX, 10.0, f64::MAX),
        ] {
            assert_eq!(window_pixels(invalid, valid, 100, 100), None);
            assert_eq!(window_pixels(valid, invalid, 100, 100), None);
        }
        assert_eq!(window_pixels(valid, valid, 0, 100), None);
        assert_eq!(window_pixels(valid, valid, 100, 0), None);
    }

    #[test]
    fn candidates_keep_front_to_back_order_and_keep_ordinary_app_windows() {
        let bounds = rect(0.0, 0.0, 100.0, 100.0);
        let front = rect(10.0, 10.0, 20.0, 20.0);
        let back = rect(20.0, 20.0, 40.0, 40.0);
        let windows = [
            (1, 42, 0, front),  // Ordinary main-app window stays.
            (2, 99, 0, bounds), // Helper goes.
            (3, 42, 0, bounds), // Explicit overlay goes, not all of its PID.
            (4, 50, 1, bounds), // Non-normal layer goes.
            (5, 50, 0, back),
        ];
        assert_eq!(
            window_candidates(&windows, 99, &[3], bounds, 100, 100),
            vec![front, back]
        );
    }
}
