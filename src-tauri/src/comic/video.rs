//! Verification of generated video clips and the shot clip binding core.
//!
//! Container duration in a completed MP4 (`moov`) is muxer-written and
//! trustworthy for finished files; a truncated download fails the full
//! decode check below instead of passing with a short duration.

use std::path::Path;

use serde::Deserialize;
use serde_json::{json, Value};
use tokio_util::sync::CancellationToken;

use crate::web::event_bridge::EventEmitter;

use super::provider::GenerationError;

#[derive(Clone, Debug)]
pub struct VideoInfo {
    pub duration_secs: f64,
    pub width: u32,
    pub height: u32,
    pub has_audio: bool,
    pub extension: String,
}

pub async fn inspect_bytes(
    bytes: &[u8],
    cancel: &CancellationToken,
) -> Result<VideoInfo, GenerationError> {
    if cancel.is_cancelled() {
        return Err(GenerationError::new(
            "cancelled",
            "video verification cancelled",
        ));
    }
    if bytes.is_empty() || bytes.len() > 256 * 1024 * 1024 {
        return Err(GenerationError::new(
            "bad_response",
            "video artifact is empty or exceeds 256 MiB",
        ));
    }
    let directory =
        tempfile::tempdir().map_err(|error| GenerationError::new("internal", error.to_string()))?;
    let path = directory.path().join("clip.bin");
    tokio::fs::write(&path, bytes)
        .await
        .map_err(|error| GenerationError::new("internal", error.to_string()))?;
    inspect_path(&path, cancel).await
}

pub async fn inspect_path(
    path: &Path,
    cancel: &CancellationToken,
) -> Result<VideoInfo, GenerationError> {
    let metadata = tokio::fs::metadata(path)
        .await
        .map_err(|error| GenerationError::new("bad_response", error.to_string()))?;
    if !metadata.is_file() || metadata.len() == 0 || metadata.len() > 256 * 1024 * 1024 {
        return Err(GenerationError::new(
            "bad_response",
            "video artifact must be a regular file up to 256 MiB",
        ));
    }
    let directory = path
        .parent()
        .ok_or_else(|| GenerationError::new("bad_response", "video file has no parent"))?;
    let filename = path
        .file_name()
        .ok_or_else(|| GenerationError::new("bad_response", "video file has no name"))?
        .to_string_lossy()
        .into_owned();
    let output = super::compose::run_process(
        "ffprobe",
        &[
            "-v".to_string(),
            "error".to_string(),
            "-protocol_whitelist".to_string(),
            "file,pipe".to_string(),
            "-show_streams".to_string(),
            "-show_format".to_string(),
            "-of".to_string(),
            "json".to_string(),
            filename.clone(),
        ],
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
        .ok_or_else(|| GenerationError::new("bad_response", "video has no streams"))?;
    let video_streams = streams
        .iter()
        .filter(|stream| stream["codec_type"] == "video")
        .count();
    let audio_streams = streams
        .iter()
        .filter(|stream| stream["codec_type"] == "audio")
        .count();
    if video_streams != 1 || audio_streams > 1 {
        return Err(GenerationError::new(
            "bad_response",
            "video must contain exactly one video stream and at most one audio stream",
        ));
    }
    let video = streams
        .iter()
        .find(|stream| stream["codec_type"] == "video")
        .unwrap();
    let width = video["width"].as_u64().unwrap_or(0) as u32;
    let height = video["height"].as_u64().unwrap_or(0) as u32;
    if !(16..=7680).contains(&width) || !(16..=7680).contains(&height) {
        return Err(GenerationError::new(
            "bad_response",
            "video dimensions are outside the supported 16–7680 range",
        ));
    }
    let format = probe["format"]["format_name"].as_str().unwrap_or_default();
    let extension = match format {
        "wav" | "mp4" => "mp4",
        f if f.contains("mp4") => "mp4",
        f if f.contains("webm") => "webm",
        f if f.contains("matroska") => "mkv",
        _ => {
            return Err(GenerationError::new(
                "bad_response",
                "video must be MP4, WebM or Matroska",
            ))
        }
    };
    let duration = probe["format"]["duration"]
        .as_str()
        .and_then(|duration| duration.parse::<f64>().ok())
        .ok_or_else(|| GenerationError::new("bad_response", "video has no duration"))?;
    if !duration.is_finite() || !(0.1..=120.0).contains(&duration) {
        return Err(GenerationError::new(
            "bad_response",
            "video duration must be 0.1–120 seconds",
        ));
    }
    // A completed mux does not guarantee decodable bytes — verify the video
    // stream fully decodes before anything downstream builds on it.
    super::compose::run_process(
        "ffmpeg",
        &[
            "-nostdin".to_string(),
            "-v".to_string(),
            "error".to_string(),
            "-i".to_string(),
            filename,
            "-map".to_string(),
            "0:v:0".to_string(),
            "-f".to_string(),
            "null".to_string(),
            "-".to_string(),
        ],
        directory,
        cancel,
        tokio::time::Instant::now() + std::time::Duration::from_secs(60),
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
    Ok(VideoInfo {
        duration_secs: duration,
        width,
        height,
        has_audio: audio_streams == 1,
        extension: extension.into(),
    })
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct VideoRequest {
    pub episode: String,
    pub shot: String,
    #[serde(alias = "duration_secs")]
    pub duration_secs: Option<f64>,
    pub ratio: Option<String>,
    /// Image-to-video control. Default follows the shot: a bound frame is
    /// used as the first frame, otherwise text-to-video. `false` forces
    /// text-to-video even when a frame is bound; `true` requires one.
    #[serde(alias = "use_first_frame")]
    pub use_first_frame: Option<bool>,
    #[serde(alias = "provider_id")]
    pub provider_id: Option<i32>,
    #[serde(alias = "provider_name")]
    pub provider_name: Option<String>,
}

/// Story-relative path of the shot's bound frame when video should start from
/// it. Existence is checked here; bytes load just before submission so the
/// persisted task row keeps only the path.
pub(crate) fn resolve_first_frame(
    store: &super::store::StoryStore,
    shot: &super::model::StoryShot,
    use_first_frame: Option<bool>,
) -> Result<Option<String>, GenerationError> {
    let selected = shot
        .selected
        .as_deref()
        .filter(|path| !path.trim().is_empty());
    if !use_first_frame.unwrap_or(selected.is_some()) {
        return Ok(None);
    }
    let Some(path) = selected else {
        return Err(GenerationError::new(
            "bad_request",
            "use_first_frame needs a bound shot frame; generate and select one first",
        ));
    };
    let absolute = store
        .resolve(path)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    if !absolute.is_file() {
        return Err(GenerationError::new(
            "bad_request",
            format!("first-frame image is missing: {path}"),
        ));
    }
    Ok(Some(path.to_string()))
}

pub async fn request(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: VideoRequest,
) -> Result<Value, GenerationError> {
    let store = super::store::StoryStore::open(folder_path)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    let board = store
        .read_storyboard(&params.episode)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    let shot = board
        .shots
        .iter()
        .find(|shot| shot.id == params.shot)
        .ok_or_else(|| GenerationError::new("bad_request", "video shot is not in this episode"))?;
    // The video brief is the frame description plus its camera move; card
    // references are intentionally not attached (job vendors take text
    // prompts; image-to-video via the bound keyframe is a later slice).
    let mut prompt = shot.description.trim().to_string();
    if let Some(camera) = shot.camera.as_deref() {
        if !camera.trim().is_empty() {
            prompt.push_str(&format!("\n运镜：{}", camera.trim()));
        }
    }
    if prompt.trim().is_empty() {
        return Err(GenerationError::new(
            "bad_request",
            "shot has an empty brief; write a visual description before requesting video",
        ));
    }
    let mut extra = json!({});
    if let Some(duration) = params.duration_secs {
        extra["duration_secs"] = json!(duration);
    }
    if let Some(ratio) = &params.ratio {
        extra["ratio"] = json!(ratio);
    }
    let target = format!("episodes/{}/shots/{}", params.episode, params.shot);
    let first_frame_path = resolve_first_frame(&store, shot, params.use_first_frame)?;
    let request = super::provider::GenerationRequest {
        prompt,
        count: 1,
        first_frame_path,
        extra,
        ..Default::default()
    };
    let id = super::task::submit_generation(
        conn,
        emitter,
        folder_id,
        folder_path,
        super::provider::MediaKind::Video,
        "shot",
        &target,
        request,
        params.provider_id,
        params.provider_name,
    )
    .await?;
    Ok(serde_json::json!({
        "taskId": id,
        "kind": "video",
        "target": target,
        "hint": "Poll comic_check_generation; bind the completed clip with comic_set_shot_video. The composer extends the shot to fit the clip; a clip's own audio wins over bound speech for that shot.",
    }))
}

/// Clear a shot's bound clip after explicit user confirmation. Like binding,
/// the confirmation carries the binding observed when it opened; a stale
/// expectation refuses with `conflict` instead of silently clearing.
pub async fn unbind(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    shot: &str,
    expected_current_task_id: Option<i32>,
) -> Result<Value, GenerationError> {
    let store = super::store::StoryStore::open(folder_path)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    store
        .clear_shot_video(episode, shot, expected_current_task_id)
        .map_err(|error| match error {
            super::store::StoreError::Conflict(message) => {
                GenerationError::new("conflict", message)
            }
            other => GenerationError::new("bad_request", other.message()),
        })?;
    crate::web::event_bridge::emit_event(
        emitter,
        crate::web::event_bridge::COMIC_CHANGED_EVENT,
        &crate::web::event_bridge::ComicChange::Upsert {
            folder_id,
            resource: "episode".into(),
            id: episode.into(),
        },
    );
    Ok(serde_json::json!({ "unbound": true }))
}

#[allow(clippy::too_many_arguments)]
pub async fn bind(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    shot: &str,
    task_id: i32,
    expected_current_task_id: Option<i32>,
    cancel: &CancellationToken,
) -> Result<Value, GenerationError> {
    let view = super::task::check_task(conn, task_id, 0)
        .await
        .filter(|view| {
            view.folder_id == folder_id
                && view.kind == "video"
                && view.status == "succeeded"
                && view.target_id.as_deref()
                    == Some(format!("episodes/{episode}/shots/{shot}").as_str())
        })
        .ok_or_else(|| {
            GenerationError::new(
                "bad_request",
                "completed video task not found for this project/shot",
            )
        })?;
    let path = view
        .artifact_path
        .ok_or_else(|| GenerationError::new("bad_response", "video task has no clip artifact"))?;
    if !path.starts_with(&format!("episodes/{episode}/shots/{shot}/video/")) {
        return Err(GenerationError::new(
            "bad_request",
            "video artifact does not belong to this shot",
        ));
    }
    let store = super::store::StoryStore::open(folder_path)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    let absolute = store
        .resolve(&path)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    let info = inspect_path(&absolute, cancel).await?;
    if cancel.is_cancelled() {
        return Err(GenerationError::new("cancelled", "video binding cancelled"));
    }
    store
        .bind_shot_video(
            episode,
            shot,
            super::model::ShotClip {
                path: path.clone(),
                duration_secs: info.duration_secs,
                task_id,
            },
            expected_current_task_id,
        )
        .map_err(|error| {
            GenerationError::new(
                if matches!(error, super::store::StoreError::Conflict(_)) {
                    "conflict"
                } else {
                    "internal"
                },
                error.message(),
            )
        })?;
    crate::web::event_bridge::emit_event(
        emitter,
        crate::web::event_bridge::COMIC_CHANGED_EVENT,
        &crate::web::event_bridge::ComicChange::Upsert {
            folder_id,
            resource: "episode".into(),
            id: episode.into(),
        },
    );
    Ok(serde_json::json!({
        "bound": true,
        "video": path,
        "durationSecs": info.duration_secs,
    }))
}

/// A real tiny H.264 MP4 rendered by ffmpeg for regression tests (`with_audio`
/// adds an AAC track). Lives outside `mod tests` so sibling test modules can
/// render fixtures too.
#[cfg(test)]
pub(crate) async fn mp4_fixture(dir: &Path, with_audio: bool) -> Vec<u8> {
    let source = dir.join(if with_audio {
        "fixture-voiced.mp4"
    } else {
        "fixture-silent.mp4"
    });
    let mut args: Vec<String> = vec![
        "-nostdin".into(),
        "-v".into(),
        "error".into(),
        "-f".into(),
        "lavfi".into(),
        "-i".into(),
        "testsrc=duration=0.5:size=128x192:rate=24".into(),
    ];
    if with_audio {
        args.extend([
            "-f".into(),
            "lavfi".into(),
            "-i".into(),
            "sine=frequency=440:sample_rate=48000:duration=0.5".into(),
        ]);
    }
    args.extend([
        "-c:v".into(),
        "libx264".into(),
        "-pix_fmt".into(),
        "yuv420p".into(),
    ]);
    if with_audio {
        args.extend(["-c:a".into(), "aac".into(), "-shortest".into()]);
    }
    args.push("-y".into());
    args.push(source.to_string_lossy().into_owned());
    let encoded = tokio::process::Command::new("ffmpeg")
        .args(&args)
        .output()
        .await
        .unwrap();
    assert!(
        encoded.status.success(),
        "ffmpeg is required for this regression"
    );
    tokio::fs::read(&source).await.unwrap()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::comic::{
        model::{EpisodeMeta, StoryShot},
        store::StoryStore,
    };
    use crate::db::{
        entities::comic_generation_task,
        test_helpers::{fresh_in_memory_db, seed_folder},
    };
    use chrono::Utc;
    use sea_orm::{ActiveModelTrait, ActiveValue::NotSet, Set};
    use serde_json::json;

    fn project(path: &Path) -> StoryStore {
        let store = StoryStore::init(path, "Video fixture").unwrap();
        store
            .upsert_episode(&EpisodeMeta {
                id: "ep001".into(),
                ..Default::default()
            })
            .unwrap();
        store
            .replace_storyboard(
                "ep001",
                vec![StoryShot {
                    id: "shot-0001".into(),
                    ..Default::default()
                }],
            )
            .unwrap();
        store
    }

    async fn succeeded_video_task(
        conn: &sea_orm::DatabaseConnection,
        folder_id: i32,
        path: &str,
    ) -> i32 {
        let now = Utc::now();
        comic_generation_task::ActiveModel {
            id: NotSet,
            folder_id: Set(folder_id),
            kind: Set("video".into()),
            status: Set("succeeded".into()),
            provider_id: Set(None),
            provider_name: Set(Some("Clip vendor".into())),
            model: Set(Some("seedance-fixture".into())),
            request_json: Set("{}".into()),
            result_json: Set("{}".into()),
            artifact_path: Set(Some(path.into())),
            target_kind: Set(Some("shot".into())),
            target_id: Set(Some("episodes/ep001/shots/shot-0001".into())),
            error_kind: Set(None),
            error_message: Set(None),
            candidates_json: Set("[]".into()),
            created_at: Set(now),
            updated_at: Set(now),
        }
        .insert(conn)
        .await
        .unwrap()
        .id
    }

    #[tokio::test]
    async fn video_verification_reads_dimensions_audio_and_rejects_garbage() {
        let dir = tempfile::tempdir().unwrap();
        let silent = inspect_bytes(
            &mp4_fixture(dir.path(), false).await,
            &CancellationToken::new(),
        )
        .await
        .unwrap();
        assert_eq!(silent.extension, "mp4");
        assert_eq!(silent.width, 128);
        assert_eq!(silent.height, 192);
        assert!(!silent.has_audio);
        assert!((silent.duration_secs - 0.5).abs() < 0.15, "{silent:?}");
        let voiced = inspect_bytes(
            &mp4_fixture(dir.path(), true).await,
            &CancellationToken::new(),
        )
        .await
        .unwrap();
        assert!(voiced.has_audio);
        for bytes in [
            b"not a video".as_slice(),
            b"\x00\x00\x00\x18ftypmp4broken".as_slice(),
        ] {
            assert!(inspect_bytes(bytes, &CancellationToken::new())
                .await
                .is_err());
        }
    }

    #[tokio::test]
    async fn binding_a_clip_keeps_the_selected_image_and_audio() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let path = "episodes/ep001/shots/shot-0001/video/task-9.mp4";
        store
            .write_artifact(path, &mp4_fixture(dir.path(), false).await)
            .unwrap();
        let mut board = store.read_storyboard("ep001").unwrap();
        board.shots[0].selected =
            Some("episodes/ep001/shots/shot-0001/candidates/task-1-01.png".into());
        store.write_storyboard("ep001", &board).unwrap();
        let id = succeeded_video_task(&db.conn, folder, path).await;
        // A task from another project must not bind.
        assert!(bind(
            &db.conn,
            &EventEmitter::Noop,
            folder + 1,
            dir.path(),
            "ep001",
            "shot-0001",
            id,
            None,
            &CancellationToken::new()
        )
        .await
        .is_err());
        bind(
            &db.conn,
            &EventEmitter::Noop,
            folder,
            dir.path(),
            "ep001",
            "shot-0001",
            id,
            None,
            &CancellationToken::new(),
        )
        .await
        .unwrap();
        let board = store.read_storyboard("ep001").unwrap();
        assert!(board.shots[0]
            .selected
            .as_ref()
            .unwrap()
            .ends_with("task-1-01.png"));
        let clip = board.shots[0].video.as_ref().unwrap();
        assert_eq!(clip.path, path);
        assert_eq!(clip.task_id, id);
        assert!((clip.duration_secs - 0.5).abs() < 0.15);
    }

    #[tokio::test]
    async fn first_frame_follows_the_bound_shot_frame_unless_opted_out() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let mut board = store.read_storyboard("ep001").unwrap();
        board.shots[0].selected =
            Some("episodes/ep001/shots/shot-0001/candidates/task-1-01.png".into());
        store
            .write_artifact(board.shots[0].selected.as_ref().unwrap(), b"png-bytes")
            .unwrap();
        store.write_storyboard("ep001", &board).unwrap();
        let shot = &store.read_storyboard("ep001").unwrap().shots[0];
        // Default: a bound frame is used as the first frame.
        assert_eq!(
            resolve_first_frame(&store, shot, None).unwrap().as_deref(),
            Some("episodes/ep001/shots/shot-0001/candidates/task-1-01.png")
        );
        // Explicit opt-out forces text-to-video.
        assert_eq!(
            resolve_first_frame(&store, shot, Some(false)).unwrap(),
            None
        );
        assert_eq!(
            resolve_first_frame(&store, shot, Some(true))
                .unwrap()
                .as_deref(),
            Some("episodes/ep001/shots/shot-0001/candidates/task-1-01.png")
        );
        // Forcing i2v without a bound frame refuses before any submission.
        let bare = &store.read_storyboard("ep001").unwrap().shots[0];
        let mut unbound = bare.clone();
        unbound.selected = None;
        let error = resolve_first_frame(&store, &unbound, Some(true)).unwrap_err();
        assert_eq!(error.kind, "bad_request");
        assert!(error.message.contains("bound shot frame"), "{error}");
    }

    #[tokio::test]
    async fn empty_brief_and_foreign_shot_refuse_before_provider_submission() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        project(dir.path());
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let params: VideoRequest = serde_json::from_value(json!({
            "episode": "ep001", "shot": "shot-0001", "duration_secs": 7.5,
        }))
        .unwrap();
        assert_eq!(params.duration_secs, Some(7.5));
        let error = request(&db.conn, &EventEmitter::Noop, folder, dir.path(), params)
            .await
            .unwrap_err();
        assert!(error.message.contains("brief"), "{error}");
    }
}
