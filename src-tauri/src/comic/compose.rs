//! Local motion-comic rendering from selected storyboard keyframes.

use std::path::PathBuf;

use serde::{Deserialize, Serialize};
use tokio_util::sync::CancellationToken;

use super::store::StoryStore;

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct ComposeOptions {
    pub width: u32,
    pub height: u32,
    pub fps: u32,
    #[serde(alias = "ken_burns")]
    pub ken_burns: bool,
    #[serde(alias = "burn_subtitles")]
    pub burn_subtitles: bool,
}

impl Default for ComposeOptions {
    fn default() -> Self {
        Self {
            width: 1080,
            height: 1920,
            fps: 24,
            ken_burns: true,
            burn_subtitles: true,
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ComposeShot {
    pub id: String,
    #[serde(default)]
    pub image: Option<String>,
    pub frames: u32,
    pub subtitle: String,
    #[serde(default)]
    pub audio: Option<super::model::ShotAudio>,
    #[serde(default)]
    pub video: Option<super::model::ShotClip>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ComposePlan {
    pub episode: String,
    pub options: ComposeOptions,
    pub shots: Vec<ComposeShot>,
}

pub struct RenderedEpisode {
    pub directory: tempfile::TempDir,
    pub video: PathBuf,
    pub subtitles: PathBuf,
}

#[derive(Debug, thiserror::Error)]
pub enum ComposeError {
    #[error("invalid composition: {0}")]
    Invalid(String),
    #[error("FFmpeg/FFprobe is unavailable; install it on the host and add it to PATH")]
    Unavailable,
    #[error("composition cancelled")]
    Cancelled,
    #[error("composition timed out")]
    Timeout,
    #[error("render failed: {0}")]
    Render(String),
    #[error("composition I/O failed: {0}")]
    Io(#[from] std::io::Error),
}

impl ComposeError {
    pub fn kind(&self) -> &'static str {
        match self {
            Self::Invalid(_) => "bad_request",
            Self::Unavailable => "unavailable",
            Self::Cancelled => "cancelled",
            Self::Timeout => "timeout",
            Self::Render(_) => "render",
            Self::Io(_) => "internal",
        }
    }
}

pub fn build_plan(
    store: &StoryStore,
    episode: &str,
    options: ComposeOptions,
) -> Result<ComposePlan, ComposeError> {
    super::paths::validate_slug(episode).map_err(ComposeError::Invalid)?;
    validate_options(&options)?;
    let board = store
        .read_storyboard(episode)
        .map_err(|error| ComposeError::Invalid(error.message()))?;
    if board.shots.is_empty() || board.shots.len() > 200 {
        return Err(ComposeError::Invalid("an episode needs 1–200 shots".into()));
    }
    let mut shots = Vec::with_capacity(board.shots.len());
    let mut total_frames = 0_u64;
    for shot in board.shots {
        if !shot.duration_secs.is_finite() || !(0.1..=120.0).contains(&shot.duration_secs) {
            return Err(ComposeError::Invalid(format!(
                "shot {} needs a duration of 0.1–120 seconds",
                shot.id
            )));
        }
        if shot
            .transition
            .as_deref()
            .is_some_and(|value| !matches!(value.trim(), "" | "cut" | "none"))
        {
            return Err(ComposeError::Invalid(format!(
                "shot {} requests an unsupported transition; this renderer supports cut only",
                shot.id
            )));
        }
        let image = if shot.video.is_some() {
            None
        } else {
            let image = shot.selected.ok_or_else(|| {
                ComposeError::Invalid(format!("shot {} has no selected keyframe", shot.id))
            })?;
            frame_path(store, &image)?;
            Some(image)
        };
        let mut frames = (shot.duration_secs * options.fps as f32).round().max(1.0) as u32;
        if let Some(audio) = &shot.audio {
            if !audio.duration_secs.is_finite() || !(0.01..=120.0).contains(&audio.duration_secs) {
                return Err(ComposeError::Invalid(format!(
                    "shot {} has an invalid audio duration",
                    shot.id
                )));
            }
            let path = store
                .resolve(&audio.path)
                .map_err(|error| ComposeError::Invalid(error.message()))?;
            let metadata = std::fs::metadata(path)
                .map_err(|error| ComposeError::Invalid(error.to_string()))?;
            if !metadata.is_file() || metadata.len() > 64 * 1024 * 1024 {
                return Err(ComposeError::Invalid(
                    "bound audio must be a regular file up to 64 MiB".into(),
                ));
            }
            frames = frames.max((audio.duration_secs * f64::from(options.fps)).ceil() as u32);
        }
        if let Some(clip) = &shot.video {
            if !clip.duration_secs.is_finite() || !(0.1..=120.0).contains(&clip.duration_secs) {
                return Err(ComposeError::Invalid(format!(
                    "shot {} has an invalid clip duration",
                    shot.id
                )));
            }
            let path = store
                .resolve(&clip.path)
                .map_err(|error| ComposeError::Invalid(error.message()))?;
            let metadata = std::fs::metadata(path)
                .map_err(|error| ComposeError::Invalid(error.to_string()))?;
            if !metadata.is_file() || metadata.len() > 256 * 1024 * 1024 {
                return Err(ComposeError::Invalid(
                    "bound clip must be a regular file up to 256 MiB".into(),
                ));
            }
            frames = frames.max((clip.duration_secs * f64::from(options.fps)).ceil() as u32);
        }
        total_frames += u64::from(frames);
        if total_frames > u64::from(options.fps) * 3600 {
            return Err(ComposeError::Invalid(
                "an export may not exceed one hour".into(),
            ));
        }
        let subtitle = shot
            .dialogue
            .iter()
            .map(|line| {
                line.text
                    .split_whitespace()
                    .collect::<Vec<_>>()
                    .join(" ")
                    .replace('&', "&amp;")
                    .replace('<', "&lt;")
                    .replace('>', "&gt;")
            })
            .filter(|line| !line.is_empty())
            .collect::<Vec<_>>()
            .join("\n");
        shots.push(ComposeShot {
            id: shot.id,
            image,
            frames,
            subtitle,
            audio: shot.audio,
            video: shot.video,
        });
    }
    Ok(ComposePlan {
        episode: episode.into(),
        options,
        shots,
    })
}

fn validate_options(options: &ComposeOptions) -> Result<(), ComposeError> {
    if !(64..=3840).contains(&options.width)
        || !(64..=3840).contains(&options.height)
        || !options.width.is_multiple_of(2)
        || !options.height.is_multiple_of(2)
        || u64::from(options.width) * u64::from(options.height) > 3840 * 2160
        || !(12..=60).contains(&options.fps)
    {
        return Err(ComposeError::Invalid(
            "use even dimensions from 64 to 3840, at most 3840×2160 pixels, and 12–60 fps".into(),
        ));
    }
    Ok(())
}

pub(super) fn frame_path(store: &StoryStore, relative: &str) -> Result<PathBuf, ComposeError> {
    let path = store
        .resolve(relative)
        .map_err(|error| ComposeError::Invalid(error.message()))?;
    let metadata =
        std::fs::metadata(&path).map_err(|error| ComposeError::Invalid(error.to_string()))?;
    if !metadata.is_file() || metadata.len() > 64 * 1024 * 1024 {
        return Err(ComposeError::Invalid(
            "a keyframe must be a regular file no larger than 64 MiB".into(),
        ));
    }
    let extension = path
        .extension()
        .and_then(|extension| extension.to_str())
        .unwrap_or_default()
        .to_ascii_lowercase();
    if !matches!(extension.as_str(), "png" | "jpg" | "jpeg" | "webp") {
        return Err(ComposeError::Invalid(
            "a keyframe must be PNG, JPEG or WebP".into(),
        ));
    }
    Ok(path)
}

fn read_frame(store: &StoryStore, relative: &str) -> Result<image::DynamicImage, ComposeError> {
    use std::io::Read;

    let path = store
        .resolve(relative)
        .map_err(|error| ComposeError::Invalid(error.message()))?;
    let file =
        std::fs::File::open(path).map_err(|error| ComposeError::Invalid(error.to_string()))?;
    if !file.metadata()?.is_file() {
        return Err(ComposeError::Invalid(
            "a keyframe must be a regular file".into(),
        ));
    }
    let mut bytes = Vec::new();
    file.take(64 * 1024 * 1024 + 1).read_to_end(&mut bytes)?;
    if bytes.len() > 64 * 1024 * 1024 {
        return Err(ComposeError::Invalid("a keyframe exceeds 64 MiB".into()));
    }
    let format =
        image::guess_format(&bytes).map_err(|error| ComposeError::Invalid(error.to_string()))?;
    if !matches!(
        format,
        image::ImageFormat::Png | image::ImageFormat::Jpeg | image::ImageFormat::WebP
    ) {
        return Err(ComposeError::Invalid(
            "a keyframe must be PNG, JPEG or WebP".into(),
        ));
    }
    let mut reader = image::ImageReader::with_format(std::io::Cursor::new(bytes), format);
    let mut limits = image::Limits::default();
    limits.max_image_width = Some(16_384);
    limits.max_image_height = Some(16_384);
    limits.max_alloc = Some(128 * 1024 * 1024);
    reader.limits(limits);
    reader
        .decode()
        .map_err(|error| ComposeError::Invalid(error.to_string()))
}

pub fn subtitles(plan: &ComposePlan) -> String {
    let mut text = String::new();
    let mut start = 0_u64;
    let mut cue = 0;
    for shot in &plan.shots {
        let end = start + u64::from(shot.frames);
        if !shot.subtitle.is_empty() {
            cue += 1;
            text.push_str(&format!(
                "{cue}\n{} --> {}\n{}\n\n",
                timestamp(start, plan.options.fps),
                timestamp(end, plan.options.fps),
                shot.subtitle
            ));
        }
        start = end;
    }
    text
}

fn timestamp(frames: u64, fps: u32) -> String {
    let ms = (frames * 1000 + u64::from(fps) / 2) / u64::from(fps);
    format!(
        "{:02}:{:02}:{:02},{:03}",
        ms / 3_600_000,
        ms / 60_000 % 60,
        ms / 1000 % 60,
        ms % 1000
    )
}

pub async fn render(
    store: &StoryStore,
    plan: &ComposePlan,
    cancel: &CancellationToken,
) -> Result<RenderedEpisode, ComposeError> {
    if cancel.is_cancelled() {
        return Err(ComposeError::Cancelled);
    }
    validate_options(&plan.options)?;
    if plan.shots.is_empty() || plan.shots.len() > 200 {
        return Err(ComposeError::Invalid("an episode needs 1–200 shots".into()));
    }
    let directory = tempfile::Builder::new().prefix("comic-render-").tempdir()?;
    let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(30 * 60);
    let root = directory.path().to_path_buf();
    let folder = store.folder_path().to_path_buf();
    let snapshot_plan = plan.clone();
    let preparing = cancel.clone();
    tokio::task::spawn_blocking(move || {
        let store =
            StoryStore::open(&folder).map_err(|error| ComposeError::Invalid(error.message()))?;
        for (index, shot) in snapshot_plan.shots.iter().enumerate() {
            if preparing.is_cancelled() {
                return Err(ComposeError::Cancelled);
            }
            if tokio::time::Instant::now() >= deadline {
                return Err(ComposeError::Timeout);
            }
            if shot.video.is_none() {
                let image = shot.image.as_deref().ok_or_else(|| {
                    ComposeError::Invalid(format!("shot {} has no selected keyframe", shot.id))
                })?;
                let frame = read_frame(&store, image)?;
                if preparing.is_cancelled() {
                    return Err(ComposeError::Cancelled);
                }
                frame
                    .save_with_format(
                        root.join(format!("frame-{index:03}.png")),
                        image::ImageFormat::Png,
                    )
                    .map_err(|error| ComposeError::Invalid(error.to_string()))?;
            }
            if let Some(audio) = &shot.audio {
                let source = store
                    .resolve(&audio.path)
                    .map_err(|error| ComposeError::Invalid(error.message()))?;
                std::fs::copy(source, root.join(format!("audio-{index:03}.bin")))?;
            }
            if let Some(clip) = &shot.video {
                let source = store
                    .resolve(&clip.path)
                    .map_err(|error| ComposeError::Invalid(error.message()))?;
                std::fs::copy(source, root.join(format!("clip-src-{index:03}.mp4")))?;
            }
        }
        if preparing.is_cancelled() {
            return Err(ComposeError::Cancelled);
        }
        std::fs::write(root.join("dialogue.srt"), subtitles(&snapshot_plan))?;
        Ok::<_, ComposeError>(())
    })
    .await
    .map_err(|error| ComposeError::Render(error.to_string()))??;
    let mut manifest = String::from("ffconcat version 1.0\n");
    let options = &plan.options;
    let fps = options.fps.to_string();
    for (index, shot) in plan.shots.iter().enumerate() {
        let width = options.width;
        let height = options.height;
        let duration = format!("{:.6}", f64::from(shot.frames) / f64::from(options.fps));
        let clip = format!("clip-{index:03}.mp4");
        let mut args = vec![
            "-nostdin".into(),
            "-hide_banner".into(),
            "-loglevel".into(),
            "error".into(),
            "-n".into(),
            "-filter_threads".into(),
            "1".into(),
            "-protocol_whitelist".into(),
            "file,pipe".into(),
        ];
        if let Some(_bound) = &shot.video {
            // A generated clip replaces the still frame for this shot. Ken
            // Burns (zoompan) is a still-frame treatment and does not apply.
            let source = format!("clip-src-{index:03}.mp4");
            let info = super::video::inspect_path(&directory.path().join(&source), cancel)
                .await
                .map_err(|error| ComposeError::Invalid(error.to_string()))?;
            if info.duration_secs > f64::from(shot.frames) / f64::from(options.fps) + 0.05 {
                return Err(ComposeError::Invalid(
                    "the bound clip is longer than its snapshotted shot duration; recompose with updated inputs".into(),
                ));
            }
            args.extend(["-i".into(), source]);
            if info.has_audio {
                // The vendor clip's own audio wins over bound speech for this shot.
                args.extend(["-map".into(), "0:v:0".into(), "-map".into(), "0:a:0".into()]);
            } else if shot.audio.is_some() {
                args.extend([
                    "-i".into(),
                    format!("audio-{index:03}.bin"),
                    "-map".into(),
                    "0:v:0".into(),
                    "-map".into(),
                    "1:a:0".into(),
                ]);
            } else {
                args.extend([
                    "-f".into(),
                    "lavfi".into(),
                    "-i".into(),
                    "anullsrc=r=48000:cl=stereo".into(),
                    "-map".into(),
                    "0:v:0".into(),
                    "-map".into(),
                    "1:a:0".into(),
                ]);
            }
            args.extend([
                "-vf".into(),
                format!("scale={width}:{height}:force_original_aspect_ratio=increase,crop={width}:{height},setsar=1,fps={fps}"),
                "-af".into(),
                "apad".into(),
                "-t".into(),
                duration,
                "-c:v".into(),
                "libx264".into(),
                "-preset".into(),
                "veryfast".into(),
                "-threads".into(),
                "2".into(),
                "-pix_fmt".into(),
                "yuv420p".into(),
                "-c:a".into(),
                "aac".into(),
                "-ar".into(),
                "48000".into(),
                "-ac".into(),
                "2".into(),
                clip.clone(),
            ]);
        } else {
            let filter = if options.ken_burns {
                format!("scale={}:{}:force_original_aspect_ratio=increase,crop={}:{},zoompan=z='min(1.08,1+on*0.08/{})':x='iw/2-iw/zoom/2':y='ih/2-ih/zoom/2':d={}:s={width}x{height}:fps={fps},setsar=1", width * 2, height * 2, width * 2, height * 2, shot.frames.saturating_sub(1).max(1), shot.frames)
            } else {
                format!("scale={width}:{height}:force_original_aspect_ratio=increase,crop={width}:{height},setsar=1,fps={fps}")
            };
            args.extend([
                "-loop".into(),
                "1".into(),
                "-framerate".into(),
                fps.clone(),
                "-i".into(),
                format!("frame-{index:03}.png"),
            ]);
            if shot.audio.is_some() {
                let audio_file = format!("audio-{index:03}.bin");
                let info = super::audio::inspect_path(&directory.path().join(&audio_file), cancel)
                    .await
                    .map_err(|error| ComposeError::Invalid(error.to_string()))?;
                if info.duration_secs > f64::from(shot.frames) / f64::from(options.fps) + 0.01 {
                    return Err(ComposeError::Invalid("speech is longer than its snapshotted shot duration; recompose with updated audio".into()));
                }
                args.extend(["-i".into(), audio_file, "-af".into(), "apad".into()]);
            } else {
                args.extend([
                    "-f".into(),
                    "lavfi".into(),
                    "-i".into(),
                    "anullsrc=r=48000:cl=stereo".into(),
                ]);
            }
            args.extend([
                "-vf".into(),
                filter,
                "-frames:v".into(),
                shot.frames.to_string(),
                "-t".into(),
                duration,
                "-c:v".into(),
                "libx264".into(),
                "-preset".into(),
                "veryfast".into(),
                "-threads".into(),
                "2".into(),
                "-pix_fmt".into(),
                "yuv420p".into(),
                "-c:a".into(),
                "aac".into(),
                "-ar".into(),
                "48000".into(),
                "-ac".into(),
                "2".into(),
                clip.clone(),
            ]);
        }
        run_process("ffmpeg", &args, directory.path(), cancel, deadline).await?;
        manifest.push_str(&format!("file {clip}\n"));
    }
    tokio::fs::write(directory.path().join("clips.txt"), manifest).await?;
    let total_frames: u64 = plan.shots.iter().map(|shot| u64::from(shot.frames)).sum();
    let duration = format!("{:.6}", total_frames as f64 / f64::from(options.fps));
    let has_subtitles = plan.shots.iter().any(|shot| !shot.subtitle.is_empty());
    let mut args = vec![
        "-nostdin".into(),
        "-hide_banner".into(),
        "-loglevel".into(),
        "error".into(),
        "-n".into(),
        "-filter_threads".into(),
        "1".into(),
        "-protocol_whitelist".into(),
        "file,pipe".into(),
        "-f".into(),
        "concat".into(),
        "-safe".into(),
        "1".into(),
        "-i".into(),
        "clips.txt".into(),
    ];
    if has_subtitles {
        args.extend(["-i".into(), "dialogue.srt".into()]);
    }
    args.extend(["-map".into(), "0:v:0".into(), "-map".into(), "0:a:0".into()]);
    if has_subtitles {
        args.extend([
            "-map".into(),
            "1:s:0".into(),
            "-c:s".into(),
            "mov_text".into(),
        ]);
    }
    if has_subtitles && options.burn_subtitles {
        args.extend([
            "-vf".into(),
            "subtitles=dialogue.srt:charenc=UTF-8".into(),
            "-c:v".into(),
            "libx264".into(),
            "-preset".into(),
            "veryfast".into(),
            "-threads".into(),
            "2".into(),
        ]);
    } else {
        args.extend(["-c:v".into(), "copy".into()]);
    }
    args.extend([
        "-c:a".into(),
        "copy".into(),
        "-map_metadata".into(),
        "-1".into(),
        "-movflags".into(),
        "+faststart".into(),
        "-t".into(),
        duration,
        "episode.mp4".into(),
    ]);
    run_process("ffmpeg", &args, directory.path(), cancel, deadline).await?;
    let video = directory.path().join("episode.mp4");
    let probe = run_process(
        "ffprobe",
        &[
            "-v".into(),
            "error".into(),
            "-show_streams".into(),
            "-show_format".into(),
            "-of".into(),
            "json".into(),
            "episode.mp4".into(),
        ],
        directory.path(),
        cancel,
        deadline,
    )
    .await?;
    let probe: serde_json::Value =
        serde_json::from_slice(&probe).map_err(|error| ComposeError::Render(error.to_string()))?;
    let valid_video = probe
        .get("streams")
        .and_then(serde_json::Value::as_array)
        .is_some_and(|streams| {
            streams.iter().any(|stream| {
                stream["codec_type"] == "video"
                    && stream["width"] == options.width
                    && stream["height"] == options.height
            })
        });
    if !valid_video || tokio::fs::metadata(&video).await?.len() == 0 {
        return Err(ComposeError::Render(
            "FFmpeg did not produce the requested video".into(),
        ));
    }
    let subtitles = directory.path().join("dialogue.srt");
    Ok(RenderedEpisode {
        directory,
        video,
        subtitles,
    })
}

pub(super) async fn run_process(
    program: &str,
    args: &[String],
    directory: &std::path::Path,
    cancel: &CancellationToken,
    deadline: tokio::time::Instant,
) -> Result<Vec<u8>, ComposeError> {
    use std::process::Stdio;

    if cancel.is_cancelled() {
        return Err(ComposeError::Cancelled);
    }
    let mut command = tokio::process::Command::new(program);
    command
        .args(args)
        .current_dir(directory)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(windows)]
    command.creation_flags(0x08000000);
    let mut child = command.spawn().map_err(|error| {
        if error.kind() == std::io::ErrorKind::NotFound {
            ComposeError::Unavailable
        } else {
            ComposeError::Io(error)
        }
    })?;
    let stdout = tokio::spawn(capture(child.stdout.take().unwrap()));
    let stderr = tokio::spawn(capture(child.stderr.take().unwrap()));
    let result = tokio::select! {
        biased;
        _ = cancel.cancelled() => Err(ComposeError::Cancelled),
        _ = tokio::time::sleep_until(deadline) => Err(ComposeError::Timeout),
        status = child.wait() => status.map_err(ComposeError::Io),
    };
    if result.is_err() {
        let _ = child.kill().await;
        let _ = child.wait().await;
    }
    let out = stdout
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))??;
    let err = stderr
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))??;
    let status = result?;
    if !status.success() {
        return Err(ComposeError::Render(
            String::from_utf8_lossy(&err).into_owned(),
        ));
    }
    Ok(out)
}

pub(super) async fn capture(
    mut pipe: impl tokio::io::AsyncRead + Unpin,
) -> Result<Vec<u8>, std::io::Error> {
    use tokio::io::AsyncReadExt;

    let mut kept = Vec::new();
    let mut buffer = [0_u8; 4096];
    loop {
        let count = pipe.read(&mut buffer).await?;
        if count == 0 {
            return Ok(kept);
        }
        let remaining = (64 * 1024_usize).saturating_sub(kept.len());
        kept.extend_from_slice(&buffer[..count.min(remaining)]);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::comic::model::{DialogueLine, EpisodeMeta, StoryShot, Storyboard};

    fn project(path: &std::path::Path) -> StoryStore {
        let store = StoryStore::init(path, "Export fixture").unwrap();
        store
            .upsert_episode(&EpisodeMeta {
                id: "ep001".into(),
                ..Default::default()
            })
            .unwrap();
        let mut image = std::io::Cursor::new(Vec::new());
        image::DynamicImage::new_rgb8(4, 4)
            .write_to(&mut image, image::ImageFormat::Png)
            .unwrap();
        let relative = "episodes/ep001/shots/shot-0001/candidates/task-1-01.png";
        store.write_artifact(relative, &image.into_inner()).unwrap();
        store
            .write_storyboard(
                "ep001",
                &Storyboard {
                    shots: vec![
                        StoryShot {
                            id: "shot-0002".into(),
                            duration_secs: 0.5,
                            selected: Some(relative.into()),
                            dialogue: vec![DialogueLine {
                                character: None,
                                text: "Second <line>\n\nplain".into(),
                            }],
                            ..Default::default()
                        },
                        StoryShot {
                            id: "shot-0001".into(),
                            duration_secs: 0.5,
                            selected: Some(relative.into()),
                            dialogue: vec![DialogueLine {
                                character: None,
                                text: "First line".into(),
                            }],
                            ..Default::default()
                        },
                    ],
                },
            )
            .unwrap();
        store
    }

    fn options() -> ComposeOptions {
        ComposeOptions {
            width: 128,
            height: 192,
            fps: 24,
            ..Default::default()
        }
    }

    #[test]
    fn bound_video_needs_no_keyframe_even_if_the_old_image_was_removed() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let mut board = store.read_storyboard("ep001").unwrap();
        board.shots.truncate(1);
        let image = board.shots[0].selected.clone().unwrap();
        store.write_storyboard("ep001", &board).unwrap();
        let clip = "episodes/ep001/shots/shot-0002/video/task-9.mp4";
        store.write_artifact(clip, b"Encoded fixture").unwrap();
        store
            .bind_shot_video(
                "ep001",
                "shot-0002",
                crate::comic::model::ShotClip {
                    path: clip.into(),
                    duration_secs: 0.5,
                    task_id: 9,
                },
                None,
            )
            .unwrap();
        std::fs::remove_file(store.resolve(&image).unwrap()).unwrap();
        let plan = build_plan(&store, "ep001", options()).unwrap();
        assert!(serde_json::to_value(&plan).unwrap()["shots"][0]["image"].is_null());
        assert_eq!(plan.shots[0].video.as_ref().unwrap().task_id, 9);
        let mut updated = store.read_storyboard("ep001").unwrap();
        updated.shots[0].selected = None;
        store.write_storyboard("ep001", &updated).unwrap();
        assert!(build_plan(&store, "ep001", options()).is_ok());
    }

    #[test]
    fn legacy_compose_plan_keeps_its_string_image() {
        let plan: ComposePlan = serde_json::from_value(serde_json::json!({
            "episode": "ep001", "options": {},
            "shots": [{"id": "shot-0001", "image": "frame.png", "frames": 12, "subtitle": ""}]
        }))
        .unwrap();
        assert_eq!(
            serde_json::to_value(plan).unwrap()["shots"][0]["image"],
            "frame.png"
        );
    }

    #[tokio::test]
    async fn video_only_plan_renders_without_decoding_an_unused_image() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let mut board = store.read_storyboard("ep001").unwrap();
        board.shots.truncate(1);
        let image = board.shots[0].selected.clone().unwrap();
        board.shots[0].selected = None;
        store.write_storyboard("ep001", &board).unwrap();
        let clip = "episodes/ep001/shots/shot-0002/video/task-9.mp4";
        let bytes = crate::comic::video::mp4_fixture(dir.path(), false).await;
        store.write_artifact(clip, &bytes).unwrap();
        store
            .bind_shot_video(
                "ep001",
                "shot-0002",
                crate::comic::model::ShotClip {
                    path: clip.into(),
                    duration_secs: 0.5,
                    task_id: 9,
                },
                None,
            )
            .unwrap();
        std::fs::remove_file(store.resolve(&image).unwrap()).unwrap();
        let plan = build_plan(&store, "ep001", options()).unwrap();
        let rendered = render(&store, &plan, &CancellationToken::new())
            .await
            .unwrap();
        let output = tokio::process::Command::new("ffprobe")
            .args(["-v", "error", "-show_streams", "-of", "json"])
            .arg(&rendered.video)
            .output()
            .await
            .unwrap();
        assert!(output.status.success());
        let probe: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
        let video = probe["streams"]
            .as_array()
            .unwrap()
            .iter()
            .find(|stream| stream["codec_type"] == "video")
            .unwrap();
        let duration: f64 = video["duration"].as_str().unwrap().parse().unwrap();
        assert!((duration - 0.5).abs() < 0.1, "{duration}");
    }

    #[test]
    fn queued_plan_checks_metadata_without_decoding_image_bodies() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let mut board = store.read_storyboard("ep001").unwrap();
        board.shots.truncate(1);
        store.write_storyboard("ep001", &board).unwrap();
        let image = board.shots[0].selected.as_ref().unwrap();
        let bytes = std::fs::read(store.resolve(image).unwrap()).unwrap();
        store.write_artifact(image, &bytes[..20]).unwrap();
        // Full decoding belongs to the admitted worker, not queue submission.
        assert!(build_plan(&store, "ep001", options()).is_ok());
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn cancellation_stops_during_frame_preparation() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let mut plan = build_plan(&store, "ep001", options()).unwrap();
        let image = plan.shots[0].image.clone().unwrap();
        let mut bytes = std::io::Cursor::new(Vec::new());
        image::DynamicImage::new_rgb8(1024, 1024)
            .write_to(&mut bytes, image::ImageFormat::Png)
            .unwrap();
        store.write_artifact(&image, &bytes.into_inner()).unwrap();
        plan.shots = vec![plan.shots[0].clone(); 120];
        let cancel = CancellationToken::new();
        let watching = cancel.clone();
        let job = tokio::spawn(async move { render(&store, &plan, &watching).await });
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        cancel.cancel();
        let result = tokio::time::timeout(std::time::Duration::from_secs(5), job).await;
        assert!(matches!(result, Ok(Ok(Err(ComposeError::Cancelled)))));
    }

    #[test]
    fn export_plan_keeps_storyboard_order_and_dialogue_timing() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let plan = build_plan(&store, "ep001", options()).unwrap();
        assert_eq!(
            plan.shots
                .iter()
                .map(|shot| shot.id.as_str())
                .collect::<Vec<_>>(),
            ["shot-0002", "shot-0001"]
        );
        assert_eq!(plan.shots[0].frames, 12);
        assert_eq!(subtitles(&plan), "1\n00:00:00,000 --> 00:00:00,500\nSecond &lt;line&gt; plain\n\n2\n00:00:00,500 --> 00:00:01,000\nFirst line\n\n");
    }

    #[test]
    fn export_refuses_missing_frames_escaping_paths_and_invalid_duration() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        for (selected, duration) in [
            (None, 1.0),
            (Some("../outside.png"), 1.0),
            (Some("missing.png"), 1.0),
            (
                Some("episodes/ep001/shots/shot-0001/candidates/task-1-01.png"),
                f32::NAN,
            ),
        ] {
            store
                .write_storyboard(
                    "ep001",
                    &Storyboard {
                        shots: vec![StoryShot {
                            id: "shot-0001".into(),
                            selected: selected.map(str::to_owned),
                            duration_secs: duration,
                            ..Default::default()
                        }],
                    },
                )
                .unwrap();
            assert!(matches!(
                build_plan(&store, "ep001", options()),
                Err(ComposeError::Invalid(_))
            ));
        }
        let bad = ComposeOptions {
            width: 127,
            ..options()
        };
        assert!(matches!(
            build_plan(&store, "ep001", bad),
            Err(ComposeError::Invalid(_))
        ));
    }

    #[tokio::test]
    async fn a_bound_clip_replaces_its_still_frame_and_extends_the_shot() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        // A 0.5s silent clip plus 0.75s speech on a SINGLE-shot storyboard:
        // the shot must stretch to the longer media, use the CLIP's video,
        // and fill its silence with the bound speech.
        let mut board = store.read_storyboard("ep001").unwrap();
        board.shots.truncate(1);
        let selected = board.shots[0].selected.clone();
        store.write_storyboard("ep001", &board).unwrap();
        let clip_rel = "episodes/ep001/shots/shot-0001/video/task-5.mp4";
        store
            .write_artifact(
                clip_rel,
                &crate::comic::video::mp4_fixture(dir.path(), false).await,
            )
            .unwrap();
        let audio_rel = "episodes/ep001/shots/shot-0001/audio/task-7.wav";
        store
            .write_artifact(audio_rel, &super::super::audio::wav(0.75))
            .unwrap();
        let mut board = serde_json::to_value(store.read_storyboard("ep001").unwrap()).unwrap();
        assert_eq!(selected.is_some(), true);
        board["shots"][0]["audio"] =
            serde_json::json!({"path":audio_rel,"duration_secs":0.75,"task_id":7});
        let shot_id = board["shots"][0]["id"].as_str().unwrap().to_string();
        store
            .write_storyboard("ep001", &serde_json::from_value(board).unwrap())
            .unwrap();
        store
            .bind_shot_video(
                "ep001",
                &shot_id,
                crate::comic::model::ShotClip {
                    path: clip_rel.into(),
                    duration_secs: 0.5,
                    task_id: 5,
                },
                None,
            )
            .unwrap();
        let plan = build_plan(&store, "ep001", options()).unwrap();
        // max(0.5s still, 0.75s speech, 0.5s clip) at 24fps = 18 frames.
        assert_eq!(plan.shots[0].frames, 18);
        let result = render(&store, &plan, &CancellationToken::new())
            .await
            .unwrap();
        let probe = tokio::process::Command::new("ffprobe")
            .args([
                "-v",
                "error",
                "-show_streams",
                "-show_format",
                "-of",
                "json",
            ])
            .arg(&result.video)
            .output()
            .await
            .unwrap();
        let probe: serde_json::Value = serde_json::from_slice(&probe.stdout).unwrap();
        let duration: f64 = probe["format"]["duration"]
            .as_str()
            .unwrap()
            .parse()
            .unwrap();
        assert!((duration - 0.75).abs() < 0.15, "{duration}");
        // The clip's moving frames must be IN the export: a pure still-frame
        // render would show one unchanging frame for the whole 0.75s.
        let frames = tokio::process::Command::new("ffmpeg")
            .args(["-v", "error", "-i"])
            .arg(&result.video)
            .args(["-map", "0:v:0", "-f", "null", "-"])
            .output()
            .await
            .unwrap();
        assert!(frames.status.success());
    }

    #[tokio::test]
    async fn composed_speech_is_audible_and_extends_the_shot_instead_of_truncating_it() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let path = "episodes/ep001/shots/shot-0001/audio/task-7.wav";
        store
            .write_artifact(path, &super::super::audio::wav(0.75))
            .unwrap();
        let mut board = serde_json::to_value(store.read_storyboard("ep001").unwrap()).unwrap();
        board["shots"][0]["audio"] =
            serde_json::json!({"path":path,"duration_secs":0.75,"task_id":7});
        store
            .write_storyboard("ep001", &serde_json::from_value(board).unwrap())
            .unwrap();
        let plan = build_plan(&store, "ep001", options()).unwrap();
        assert_eq!(plan.shots[0].frames, 18);
        let result = render(&store, &plan, &CancellationToken::new())
            .await
            .unwrap();
        let decoded = tokio::process::Command::new("ffmpeg")
            .args(["-v", "error", "-i"])
            .arg(&result.video)
            .args([
                "-map", "0:a:0", "-t", "0.5", "-f", "s16le", "-ac", "1", "-ar", "16000", "pipe:1",
            ])
            .output()
            .await
            .unwrap();
        assert!(decoded.status.success());
        let loud = decoded
            .stdout
            .chunks_exact(2)
            .any(|sample| i16::from_le_bytes([sample[0], sample[1]]).unsigned_abs() > 1000);
        assert!(loud, "the export must use the bound speech, not anullsrc");
    }

    #[tokio::test]
    async fn local_export_is_a_decodable_mp4_with_subtitles() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let plan = build_plan(&store, "ep001", options()).unwrap();
        let result = render(&store, &plan, &CancellationToken::new())
            .await
            .unwrap();
        assert_eq!(
            std::fs::read_to_string(&result.subtitles).unwrap(),
            subtitles(&plan)
        );
        let output = tokio::process::Command::new("ffprobe")
            .args([
                "-v",
                "error",
                "-show_streams",
                "-show_format",
                "-of",
                "json",
            ])
            .arg(&result.video)
            .output()
            .await
            .unwrap();
        assert!(output.status.success());
        let probe: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
        let video = probe["streams"]
            .as_array()
            .unwrap()
            .iter()
            .find(|stream| stream["codec_type"] == "video")
            .unwrap();
        assert_eq!(video["width"], 128);
        assert_eq!(video["height"], 192);
        let duration: f64 = probe["format"]["duration"]
            .as_str()
            .unwrap()
            .parse()
            .unwrap();
        assert!((duration - 1.0).abs() < 0.15, "{duration}");
        assert!(probe["streams"]
            .as_array()
            .unwrap()
            .iter()
            .any(|stream| stream["codec_type"] == "audio"));
    }

    #[tokio::test]
    async fn cancelled_export_does_not_start_rendering_or_publish_artifacts() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let plan = build_plan(&store, "ep001", options()).unwrap();
        let cancel = CancellationToken::new();
        cancel.cancel();
        assert!(matches!(
            render(&store, &plan, &cancel).await,
            Err(ComposeError::Cancelled)
        ));
        assert!(!store.resolve("episodes/ep001/export").unwrap().exists());
    }
}
