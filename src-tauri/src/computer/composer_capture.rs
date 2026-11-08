//! Frozen composer screenshots and their bounded helper transfer.

use std::collections::HashSet;

use base64::engine::general_purpose::STANDARD as BASE64;
use base64::Engine;

use super::protocol::{
    ComposerCaptureChunk, ComposerCaptureManifest, ComposerDisplay, HelperError, HelperErrorCode,
};
use super::types::Rect;

pub(crate) const MAX_COMPOSER_DISPLAYS: usize = 32;
pub(crate) const MAX_COMPOSER_PIXELS: u64 = 64_000_000;
pub(crate) const MAX_COMPOSER_PNG_BYTES: u64 = 256 * 1024 * 1024;
pub(crate) const COMPOSER_CHUNK_BYTES: usize = 1024 * 1024;
#[cfg(any(test, feature = "tauri-runtime"))]
const MAX_ENCODED_CHUNK: usize = COMPOSER_CHUNK_BYTES.div_ceil(3) * 4;

#[derive(Debug)]
pub(crate) struct ComposerCapturedMonitor {
    pub display: ComposerDisplay,
    pub png: Vec<u8>,
}

fn invalid(message: impl Into<String>) -> HelperError {
    HelperError::new(HelperErrorCode::BadRequest, message)
}

fn valid_rect(rect: Rect) -> bool {
    rect.x.is_finite()
        && rect.y.is_finite()
        && rect.width.is_finite()
        && rect.height.is_finite()
        && rect.width > 0.0
        && rect.height > 0.0
        && (rect.x + rect.width).is_finite()
        && (rect.y + rect.height).is_finite()
}

/// Check every advertised bound before reserving PNG storage or asking for bytes.
pub(crate) fn validate_manifest(manifest: &ComposerCaptureManifest) -> Result<(), HelperError> {
    if manifest.displays.is_empty() || manifest.displays.len() > MAX_COMPOSER_DISPLAYS {
        return Err(invalid("composer capture requires 1 to 32 displays"));
    }
    let mut indices = HashSet::new();
    let mut total = 0u64;
    for display in &manifest.displays {
        if !indices.insert(display.index) {
            return Err(invalid("composer capture has duplicate monitor indices"));
        }
        if !valid_rect(display.bounds) {
            return Err(invalid(
                "composer capture has invalid logical display bounds",
            ));
        }
        let pixels = u64::from(display.width) * u64::from(display.height);
        if pixels == 0 || pixels > MAX_COMPOSER_PIXELS {
            return Err(invalid(
                "composer capture exceeds the 64M pixels per display limit",
            ));
        }
        if display.windows.iter().any(|window| {
            !valid_rect(*window)
                || window.x < 0.0
                || window.y < 0.0
                || window.x + window.width > f64::from(display.width)
                || window.y + window.height > f64::from(display.height)
        }) {
            return Err(invalid(
                "composer capture has window bounds outside its pixel frame",
            ));
        }
        if display.png_bytes == 0 || display.png_bytes > MAX_COMPOSER_PNG_BYTES - total {
            return Err(invalid(
                "composer capture PNG data must be nonempty and total at most 256MiB",
            ));
        }
        total += display.png_bytes;
    }
    Ok(())
}

/// One frozen capture per dedicated helper connection. Chunks never recapture.
#[cfg(any(test, feature = "computer-helper"))]
#[derive(Debug, Default)]
pub(crate) struct ComposerCaptureCache {
    monitors: Vec<ComposerCapturedMonitor>,
    offsets: Vec<u64>,
}

#[cfg(any(test, feature = "computer-helper"))]
impl ComposerCaptureCache {
    pub(crate) fn new(monitors: Vec<ComposerCapturedMonitor>) -> Result<Self, HelperError> {
        let cache = Self {
            offsets: vec![0; monitors.len()],
            monitors,
        };
        validate_manifest(&cache.manifest())?;
        if cache
            .monitors
            .iter()
            .any(|m| m.png.len() as u64 != m.display.png_bytes)
        {
            return Err(invalid(
                "composer capture PNG length disagrees with its manifest",
            ));
        }
        Ok(cache)
    }

    pub(crate) fn manifest(&self) -> ComposerCaptureManifest {
        ComposerCaptureManifest {
            displays: self.monitors.iter().map(|m| m.display.clone()).collect(),
        }
    }

    pub(crate) fn chunk(
        &mut self,
        index: u32,
        offset: u64,
    ) -> Result<ComposerCaptureChunk, HelperError> {
        let slot = self
            .monitors
            .iter()
            .position(|m| m.display.index == index)
            .ok_or_else(|| invalid("no frozen composer capture for this monitor"))?;
        let monitor = &self.monitors[slot];
        if offset != self.offsets[slot] || offset >= monitor.display.png_bytes {
            return Err(invalid(
                "composer capture chunk offset is not the next unread byte",
            ));
        }
        // The cache validated this length (at most 256MiB) before accepting it.
        let start = offset as usize;
        let end = (start + COMPOSER_CHUNK_BYTES).min(monitor.png.len());
        let chunk = ComposerCaptureChunk {
            monitor_index: index,
            offset,
            data: BASE64.encode(&monitor.png[start..end]),
            eof: end == monitor.png.len(),
        };
        self.offsets[slot] = end as u64;
        Ok(chunk)
    }
}

/// The client accumulates only validated bytes. Full chunks except the last bound
/// the number of requests by the manifest's total size, even for a faulty helper.
#[cfg(any(test, feature = "tauri-runtime"))]
#[derive(Debug)]
pub(crate) struct ComposerCaptureCollector {
    monitors: Vec<ComposerCapturedMonitor>,
    current: usize,
}

#[cfg(any(test, feature = "tauri-runtime"))]
impl ComposerCaptureCollector {
    pub(crate) fn new(manifest: ComposerCaptureManifest) -> Result<Self, HelperError> {
        validate_manifest(&manifest)?;
        Ok(Self {
            monitors: manifest
                .displays
                .into_iter()
                .map(|display| ComposerCapturedMonitor {
                    display,
                    png: Vec::new(),
                })
                .collect(),
            current: 0,
        })
    }

    pub(crate) fn next_request(&self) -> Option<(u32, u64)> {
        self.monitors
            .get(self.current)
            .map(|m| (m.display.index, m.png.len() as u64))
    }

    pub(crate) fn push(&mut self, chunk: ComposerCaptureChunk) -> Result<(), HelperError> {
        let monitor = self
            .monitors
            .get_mut(self.current)
            .ok_or_else(|| invalid("composer capture already finished"))?;
        let offset = monitor.png.len() as u64;
        if chunk.monitor_index != monitor.display.index || chunk.offset != offset {
            return Err(invalid(
                "composer capture returned an unexpected monitor or offset",
            ));
        }
        let remaining = monitor.display.png_bytes - offset;
        let expected = remaining.min(COMPOSER_CHUNK_BYTES as u64) as usize;
        if chunk.data.len() > MAX_ENCODED_CHUNK
            || chunk.data.len() != expected.div_ceil(3) * 4
            || chunk.eof != (remaining == expected as u64)
        {
            return Err(invalid("composer capture chunk has invalid length or eof"));
        }
        let bytes = BASE64
            .decode(&chunk.data)
            .map_err(|_| invalid("composer capture chunk is not valid base64"))?;
        if bytes.len() != expected {
            return Err(invalid(
                "composer capture chunk has an incorrect decoded length",
            ));
        }
        // Reserve the bounded final size once, only after the first chunk is valid.
        // try_reserve_exact also reports allocation failure instead of aborting.
        if monitor.png.is_empty() {
            monitor
                .png
                .try_reserve_exact(monitor.display.png_bytes as usize)
                .map_err(|_| HelperError::failed("not enough memory for composer capture PNG"))?;
        }
        monitor.png.extend_from_slice(&bytes);
        if chunk.eof {
            self.current += 1;
        }
        Ok(())
    }

    pub(crate) fn finish(self) -> Result<Vec<ComposerCapturedMonitor>, HelperError> {
        if self.current != self.monitors.len() {
            return Err(invalid(
                "composer capture ended before every PNG was complete",
            ));
        }
        Ok(self.monitors)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::computer::protocol::{HelperMessage, HelperReply, MAX_FRAME_BYTES};
    use crate::computer::types::Rect;

    fn display(index: u32, png_bytes: u64) -> ComposerDisplay {
        ComposerDisplay {
            index,
            display_id: index + 100,
            name: format!("Display {index}"),
            bounds: Rect {
                x: -1920.0,
                y: 0.0,
                width: 1920.0,
                height: 1080.0,
            },
            width: 3840,
            height: 2160,
            windows: vec![Rect {
                x: 1.0,
                y: 2.0,
                width: 100.0,
                height: 200.0,
            }],
            png_bytes,
        }
    }

    fn captured(index: u32, bytes: usize) -> ComposerCapturedMonitor {
        ComposerCapturedMonitor {
            display: display(index, bytes as u64),
            png: vec![42; bytes],
        }
    }

    fn manifest(displays: Vec<ComposerDisplay>) -> ComposerCaptureManifest {
        ComposerCaptureManifest { displays }
    }

    fn bad<T: std::fmt::Debug>(result: Result<T, HelperError>) {
        assert_eq!(result.unwrap_err().code, HelperErrorCode::BadRequest);
    }

    #[test]
    fn composer_cache_streams_frozen_bytes_at_chunk_boundaries_under_wire_limit() {
        for length in [1, 1_048_576, 1_048_577, 18_000_000] {
            let mut cache = ComposerCaptureCache::new(vec![captured(9, length)]).unwrap();
            let mut collector = ComposerCaptureCollector::new(cache.manifest()).unwrap();
            let mut offset = 0;
            while let Some((index, next)) = collector.next_request() {
                assert_eq!((index, next), (9, offset as u64));
                let chunk = cache.chunk(index, next).unwrap();
                let want = (length - offset).min(1_048_576);
                assert_eq!(BASE64.decode(&chunk.data).unwrap(), vec![42; want]);
                assert_eq!(chunk.eof, offset + want == length);
                let wire =
                    serde_json::to_vec(&HelperMessage::Reply(HelperReply::ok(1, &chunk))).unwrap();
                assert!(wire.len() < MAX_FRAME_BYTES);
                collector.push(chunk).unwrap();
                offset += want;
            }
            let monitors = collector.finish().unwrap();
            assert_eq!(monitors.len(), 1);
            assert_eq!(monitors[0].png, vec![42; length]);
            bad(cache.chunk(9, length as u64));
        }
    }

    #[test]
    fn composer_cache_rejects_missing_replayed_and_out_of_order_offsets() {
        let mut empty = ComposerCaptureCache::default();
        bad(empty.chunk(0, 0));
        let mut cache = ComposerCaptureCache::new(vec![captured(7, 1_048_578)]).unwrap();
        bad(cache.chunk(8, 0));
        bad(cache.chunk(7, 1));
        bad(cache.chunk(7, u64::MAX));
        assert!(!cache.chunk(7, 0).unwrap().eof);
        bad(cache.chunk(7, 0));
        bad(cache.chunk(7, 1_048_577));
        assert!(cache.chunk(7, 1_048_576).unwrap().eof);
    }

    #[test]
    fn composer_manifest_rejects_duplicate_indices_and_invalid_geometry() {
        bad(validate_manifest(&manifest(vec![])));
        bad(validate_manifest(&manifest(vec![
            display(0, 1),
            display(0, 2),
        ])));
        for bounds in [
            Rect {
                x: f64::NAN,
                ..display(0, 1).bounds
            },
            Rect {
                y: f64::INFINITY,
                ..display(0, 1).bounds
            },
            Rect {
                width: 0.0,
                ..display(0, 1).bounds
            },
            Rect {
                height: -1.0,
                ..display(0, 1).bounds
            },
            Rect {
                x: f64::MAX,
                width: f64::MAX,
                ..display(0, 1).bounds
            },
        ] {
            let mut invalid = display(0, 1);
            invalid.bounds = bounds;
            bad(validate_manifest(&manifest(vec![invalid])));
        }
        let mut outside = display(0, 1);
        outside.windows[0].x = -1.0;
        bad(validate_manifest(&manifest(vec![outside])));
        let mut outside = display(0, 1);
        outside.windows[0].width = 3840.0;
        bad(validate_manifest(&manifest(vec![outside])));
    }

    #[test]
    fn composer_manifest_enforces_display_pixel_and_png_budgets_without_allocating_images() {
        let maximum: Vec<_> = (0..32).map(|i| display(i, 8_388_608)).collect();
        validate_manifest(&manifest(maximum.clone())).unwrap();
        let mut excess = maximum.clone();
        excess[0].png_bytes += 1;
        bad(validate_manifest(&manifest(excess)));
        let mut excess = maximum;
        excess.push(display(32, 1));
        bad(validate_manifest(&manifest(excess)));
        for (width, height, bytes) in [
            (0, 2160, 1),
            (8001, 8000, 1),
            (u32::MAX, u32::MAX, 1),
            (3840, 2160, 0),
            (1, 1, u64::MAX),
        ] {
            let mut invalid = display(0, bytes);
            invalid.width = width;
            invalid.height = height;
            bad(ComposerCaptureCollector::new(manifest(vec![invalid])));
        }
        let mut edge = display(0, 1);
        edge.width = 8000;
        edge.height = 8000;
        validate_manifest(&manifest(vec![edge])).unwrap();
    }

    #[test]
    fn composer_cache_checks_actual_png_length_before_accepting_frames() {
        let mut frame = captured(0, 5);
        frame.display.png_bytes = 4;
        bad(ComposerCaptureCache::new(vec![frame]));
    }

    #[test]
    fn composer_collector_rejects_bad_chunks_without_advancing_or_allocating_output() {
        let expected = manifest(vec![display(2, 3)]);
        for chunk in [
            ComposerCaptureChunk {
                monitor_index: 1,
                offset: 0,
                data: "AQID".into(),
                eof: true,
            },
            ComposerCaptureChunk {
                monitor_index: 2,
                offset: 1,
                data: "AQID".into(),
                eof: true,
            },
            ComposerCaptureChunk {
                monitor_index: 2,
                offset: 0,
                data: "".into(),
                eof: false,
            },
            ComposerCaptureChunk {
                monitor_index: 2,
                offset: 0,
                data: "AQI=".into(),
                eof: true,
            },
            ComposerCaptureChunk {
                monitor_index: 2,
                offset: 0,
                data: "AQIDBA==".into(),
                eof: true,
            },
            ComposerCaptureChunk {
                monitor_index: 2,
                offset: 0,
                data: "AQID".into(),
                eof: false,
            },
            ComposerCaptureChunk {
                monitor_index: 2,
                offset: 0,
                data: "!!!!".into(),
                eof: true,
            },
            ComposerCaptureChunk {
                monitor_index: 2,
                offset: 0,
                data: "A".repeat(1_398_105),
                eof: true,
            },
        ] {
            let mut collector = ComposerCaptureCollector::new(expected.clone()).unwrap();
            bad(collector.push(chunk));
            assert_eq!(collector.next_request(), Some((2, 0)));
            bad(collector.finish());
        }
    }

    #[test]
    fn composer_collector_requires_full_chunks_and_exact_eof_to_bound_requests() {
        let mut collector =
            ComposerCaptureCollector::new(manifest(vec![display(2, 1_048_577)])).unwrap();
        bad(collector.push(ComposerCaptureChunk {
            monitor_index: 2,
            offset: 0,
            data: "AQID".into(),
            eof: false,
        }));
        bad(collector.push(ComposerCaptureChunk {
            monitor_index: 2,
            offset: 0,
            data: BASE64.encode(vec![42; 1_048_576]),
            eof: true,
        }));
        assert_eq!(collector.next_request(), Some((2, 0)));
    }

    #[test]
    fn composer_collector_keeps_multiple_monitors_in_manifest_order() {
        let mut cache = ComposerCaptureCache::new(vec![captured(4, 2), captured(1, 3)]).unwrap();
        let mut collector = ComposerCaptureCollector::new(cache.manifest()).unwrap();
        while let Some((index, offset)) = collector.next_request() {
            collector.push(cache.chunk(index, offset).unwrap()).unwrap();
        }
        bad(collector.push(ComposerCaptureChunk {
            monitor_index: 1,
            offset: 3,
            data: String::new(),
            eof: true,
        }));
        let monitors = collector.finish().unwrap();
        assert_eq!(
            monitors.iter().map(|m| m.display.index).collect::<Vec<_>>(),
            vec![4, 1]
        );
        assert_eq!(monitors[0].png, vec![42; 2]);
        assert_eq!(monitors[1].png, vec![42; 3]);
    }
}
