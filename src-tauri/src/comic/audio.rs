//! Bounded verification of generated and bound speech artifacts.

use std::path::Path;

use tokio_util::sync::CancellationToken;

use super::provider::GenerationError;

#[derive(Clone, Debug)]
pub struct AudioInfo {
    pub duration_secs: f64,
    pub extension: String,
}

pub async fn inspect_bytes(
    bytes: &[u8],
    cancel: &CancellationToken,
) -> Result<AudioInfo, GenerationError> {
    if cancel.is_cancelled() {
        return Err(GenerationError::new(
            "cancelled",
            "speech verification cancelled",
        ));
    }
    if bytes.is_empty() || bytes.len() > 64 * 1024 * 1024 {
        return Err(GenerationError::new(
            "bad_response",
            "speech artifact is empty or exceeds 64 MiB",
        ));
    }
    let directory =
        tempfile::tempdir().map_err(|error| GenerationError::new("internal", error.to_string()))?;
    let path = directory.path().join("speech.bin");
    tokio::fs::write(&path, bytes)
        .await
        .map_err(|error| GenerationError::new("internal", error.to_string()))?;
    inspect_path(&path, cancel).await
}

pub async fn inspect_path(
    path: &Path,
    cancel: &CancellationToken,
) -> Result<AudioInfo, GenerationError> {
    let metadata = tokio::fs::metadata(path)
        .await
        .map_err(|error| GenerationError::new("bad_response", error.to_string()))?;
    if !metadata.is_file() || metadata.len() == 0 || metadata.len() > 64 * 1024 * 1024 {
        return Err(GenerationError::new(
            "bad_response",
            "speech artifact must be a regular file up to 64 MiB",
        ));
    }
    let directory = path
        .parent()
        .ok_or_else(|| GenerationError::new("bad_response", "speech file has no parent"))?;
    let filename = path
        .file_name()
        .ok_or_else(|| GenerationError::new("bad_response", "speech file has no name"))?
        .to_string_lossy()
        .into_owned();
    let args = [
        "-v".into(),
        "error".into(),
        "-protocol_whitelist".into(),
        "file,pipe".into(),
        "-select_streams".into(),
        "a:0".into(),
        "-show_streams".into(),
        "-show_format".into(),
        "-of".into(),
        "json".into(),
        filename,
    ];
    let output = super::compose::run_process(
        "ffprobe",
        &args,
        directory,
        cancel,
        tokio::time::Instant::now() + std::time::Duration::from_secs(15),
    )
    .await
    .map_err(|error| {
        GenerationError::new(
            if matches!(error, super::compose::ComposeError::Render(_)) {
                "bad_response"
            } else {
                error.kind()
            },
            error.to_string(),
        )
    })?;
    let probe: serde_json::Value = serde_json::from_slice(&output)
        .map_err(|error| GenerationError::new("bad_response", error.to_string()))?;
    let streams = probe
        .get("streams")
        .and_then(serde_json::Value::as_array)
        .ok_or_else(|| GenerationError::new("bad_response", "speech has no audio stream"))?;
    if streams.len() != 1 || streams[0]["codec_type"] != "audio" {
        return Err(GenerationError::new(
            "bad_response",
            "speech must contain exactly one audio stream",
        ));
    }
    // Codec-header fields (sample rate, channels, container format) are
    // reliable; the container's DURATION is not (a stale/corrupt MP3 Xing
    // frame count or a truncated WAV chunk lies). So duration is measured by
    // fully decoding the stream below instead of trusting `format.duration`.
    let format = probe["format"]["format_name"].as_str().unwrap_or_default();
    let extension = match format {
        "wav" => "wav",
        "mp3" => "mp3",
        _ => {
            return Err(GenerationError::new(
                "bad_response",
                "speech must be MP3 or WAV",
            ))
        }
    };
    let sample_rate = streams[0]["sample_rate"]
        .as_str()
        .and_then(|rate| rate.parse::<f64>().ok())
        .or_else(|| streams[0]["sample_rate"].as_f64());
    let channels = streams[0]["channels"]
        .as_i64()
        .filter(|channels| *channels > 0);
    let (Some(sample_rate), Some(channels)) = (sample_rate, channels) else {
        return Err(GenerationError::new(
            "bad_response",
            "speech is missing its sample rate or channel count",
        ));
    };
    if !sample_rate.is_finite() || !(1_000.0..=192_000.0).contains(&sample_rate) {
        return Err(GenerationError::new(
            "bad_response",
            "speech sample rate is out of the supported 1–192 kHz range",
        ));
    }
    let duration = decoded_duration_secs(path, channels as u64, sample_rate, cancel).await?;
    if !duration.is_finite() || !(0.01..=120.0).contains(&duration) {
        return Err(GenerationError::new(
            "bad_response",
            "speech duration must be 0.01–120 seconds",
        ));
    }
    Ok(AudioInfo {
        duration_secs: duration,
        extension: extension.into(),
    })
}

/// Fully decode the audio and measure its real duration from the PCM bytes:
/// `s16le` output is `frames × channels × 2` bytes. A file whose container
/// claims less (or more) than it decodes is judged by what actually plays —
/// the composer must never trim real speech on a lying header's word.
async fn decoded_duration_secs(
    path: &Path,
    channels: u64,
    sample_rate: f64,
    cancel: &CancellationToken,
) -> Result<f64, GenerationError> {
    use std::process::Stdio;
    use tokio::io::AsyncReadExt;

    if cancel.is_cancelled() {
        return Err(GenerationError::new(
            "cancelled",
            "speech verification cancelled",
        ));
    }
    let mut command = tokio::process::Command::new("ffmpeg");
    command
        .args(["-nostdin", "-v", "error", "-i"])
        .arg(path)
        .args(["-map", "0:a:0", "-f", "s16le", "-"])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(windows)]
    command.creation_flags(0x08000000);
    let mut child = command.spawn().map_err(|error| {
        if error.kind() == std::io::ErrorKind::NotFound {
            GenerationError::new(
                "unavailable",
                "FFmpeg/FFprobe is unavailable; install it on the host and add it to PATH",
            )
        } else {
            GenerationError::new("internal", error.to_string())
        }
    })?;
    let stderr = tokio::spawn(super::compose::capture(child.stderr.take().unwrap()));
    let mut stdout = child.stdout.take().unwrap();
    let mut decoded_bytes = 0_u64;
    let mut buffer = [0_u8; 16_384];
    let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(15);
    let outcome = loop {
        tokio::select! {
            biased;
            _ = cancel.cancelled() => break Err(GenerationError::new(
                "cancelled",
                "speech verification cancelled",
            )),
            _ = tokio::time::sleep_until(deadline) => break Err(GenerationError::new(
                "bad_response",
                "decoding speech timed out",
            )),
            read = stdout.read(&mut buffer) => match read {
                Ok(0) => break Ok(()),
                Ok(count) => decoded_bytes += count as u64,
                Err(error) => break Err(GenerationError::new(
                    "bad_response",
                    error.to_string(),
                )),
            }
        }
    };
    if outcome.is_err() {
        let _ = child.kill().await;
        let _ = child.wait().await;
    }
    let status = child
        .wait()
        .await
        .map_err(|error| GenerationError::new("internal", error.to_string()))?;
    let errors = stderr
        .await
        .map_err(|error| GenerationError::new("internal", error.to_string()))
        .and_then(|captured| {
            captured.map_err(|error| GenerationError::new("internal", error.to_string()))
        })?;
    outcome?;
    if !status.success() {
        return Err(GenerationError::new(
            "bad_response",
            format!(
                "speech does not decode: {}",
                String::from_utf8_lossy(&errors).trim()
            ),
        ));
    }
    let frames = decoded_bytes / (2 * channels);
    let duration = frames as f64 / sample_rate;
    if !duration.is_finite() || !(0.01..=120.0).contains(&duration) {
        return Err(GenerationError::new(
            "bad_response",
            "speech duration must be 0.01–120 seconds",
        ));
    }
    Ok(duration)
}

#[cfg(test)]
pub(crate) fn wav(duration_secs: f64) -> Vec<u8> {
    let samples = (duration_secs * 16_000.0) as u32;
    let data_len = samples * 2;
    let mut bytes = Vec::new();
    bytes.extend_from_slice(b"RIFF");
    bytes.extend_from_slice(&(36 + data_len).to_le_bytes());
    bytes.extend_from_slice(b"WAVEfmt ");
    bytes.extend_from_slice(&16_u32.to_le_bytes());
    bytes.extend_from_slice(&1_u16.to_le_bytes());
    bytes.extend_from_slice(&1_u16.to_le_bytes());
    bytes.extend_from_slice(&16_000_u32.to_le_bytes());
    bytes.extend_from_slice(&32_000_u32.to_le_bytes());
    bytes.extend_from_slice(&2_u16.to_le_bytes());
    bytes.extend_from_slice(&16_u16.to_le_bytes());
    bytes.extend_from_slice(b"data");
    bytes.extend_from_slice(&data_len.to_le_bytes());
    for index in 0..samples {
        let sample =
            ((f64::from(index) * 440.0 * std::f64::consts::TAU / 16_000.0).sin() * 12_000.0) as i16;
        bytes.extend_from_slice(&sample.to_le_bytes());
    }
    bytes
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn speech_verification_reads_real_duration_and_rejects_fake_audio() {
        let info = inspect_bytes(&wav(0.75), &CancellationToken::new())
            .await
            .unwrap();
        assert_eq!(info.extension, "wav");
        assert!((info.duration_secs - 0.75).abs() < 0.02);
        for bytes in [
            b"not audio".as_slice(),
            b"ID3fake mp3".as_slice(),
            b"RIFF\0\0\0\0WAVEbroken".as_slice(),
        ] {
            assert!(inspect_bytes(bytes, &CancellationToken::new())
                .await
                .is_err());
        }
    }

    #[tokio::test]
    async fn speech_duration_is_measured_from_decoded_samples_not_container_metadata() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("speech.mp3");
        let encoded = tokio::process::Command::new("ffmpeg")
            .args([
                "-nostdin",
                "-v",
                "error",
                "-f",
                "lavfi",
                "-i",
                "sine=frequency=440:sample_rate=16000",
                "-t",
                "2",
                "-c:a",
                "libmp3lame",
                "-b:a",
                "64k",
            ])
            .arg(&source)
            .output()
            .await
            .unwrap();
        assert!(
            encoded.status.success(),
            "ffmpeg with libmp3lame is required for this regression"
        );
        let bytes = tokio::fs::read(&source).await.unwrap();
        // Drop the tail audio packets while keeping the (now-lying) Xing
        // header: the container still claims ~2s, the decodable audio is
        // shorter. Duration must come from decoded samples, so the composer
        // never trusts stale metadata and silently trims speech.
        let damaged = dir.path().join("truncated.mp3");
        tokio::fs::write(&damaged, &bytes[..bytes.len() * 3 / 5])
            .await
            .unwrap();
        let info = inspect_path(&damaged, &CancellationToken::new())
            .await
            .unwrap();
        assert!(
            info.duration_secs < 1.8,
            "duration must come from decoded samples, not the container header: {info:?}"
        );
        // A fully intact file keeps its real duration.
        let intact = inspect_path(&source, &CancellationToken::new())
            .await
            .unwrap();
        assert!((intact.duration_secs - 2.0).abs() < 0.15, "{intact:?}");
    }

    #[tokio::test]
    async fn speech_probe_obeys_cancellation() {
        let cancel = CancellationToken::new();
        cancel.cancel();
        assert_eq!(
            inspect_bytes(&wav(0.1), &cancel).await.unwrap_err().kind,
            "cancelled"
        );
    }
}
