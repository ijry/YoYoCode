//! Durable task wrapper for the local episode renderer.

use std::path::Path;

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ActiveValue::NotSet, ColumnTrait, DatabaseConnection, EntityTrait,
    QueryFilter, Set, TransactionTrait,
};
use serde_json::json;
use tokio_util::sync::CancellationToken;

use crate::db::entities::comic_generation_task;
use crate::web::event_bridge::EventEmitter;

use super::compose::{self, ComposeError, ComposeOptions, ComposePlan};
use super::provider::GenerationError;
use super::store::StoryStore;
use super::task;

pub async fn submit(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    options: ComposeOptions,
) -> Result<i32, ComposeError> {
    submit_with_tools(
        conn,
        emitter,
        folder_id,
        folder_path,
        episode,
        options,
        &super::media_tools::HOST_TOOLS,
    )
    .await
}

async fn submit_with_tools(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    options: ComposeOptions,
    tools: &super::media_tools::MediaTools,
) -> Result<i32, ComposeError> {
    super::paths::validate_slug(episode).map_err(ComposeError::Invalid)?;
    tools.check(super::provider::MediaKind::Video).await?;
    static ADMISSION: std::sync::OnceLock<tokio::sync::Mutex<()>> = std::sync::OnceLock::new();
    let _guard = ADMISSION
        .get_or_init(|| tokio::sync::Mutex::new(()))
        .lock()
        .await;
    let target = format!("episodes/{episode}");
    let duplicate = comic_generation_task::Entity::find()
        .filter(comic_generation_task::Column::FolderId.eq(folder_id))
        .filter(comic_generation_task::Column::Kind.eq("compose"))
        .filter(comic_generation_task::Column::TargetId.eq(&target))
        .filter(comic_generation_task::Column::Status.is_in(["queued", "running"]))
        .one(conn)
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))?;
    if let Some(existing) = duplicate {
        return Err(ComposeError::Invalid(format!(
            "composition task {} is already active for this episode",
            existing.id
        )));
    }
    let folder = folder_path.to_path_buf();
    let episode = episode.to_string();
    // Freeze encoded input bytes, not decoded image buffers. Only admitted
    // workers decode, under the global/project permits. A queued/recovered
    // request no longer depends on files someone may edit or delete meanwhile.
    let plan = tokio::task::spawn_blocking(move || {
        let store =
            StoryStore::open(&folder).map_err(|error| ComposeError::Invalid(error.message()))?;
        let mut plan = compose::build_plan(&store, &episode, options)?;
        let snapshot = uuid::Uuid::new_v4();
        let mut copied: std::collections::HashMap<String, String> =
            std::collections::HashMap::new();
        let mut total_bytes = 0_u64;
        for (index, shot) in plan.shots.iter_mut().enumerate() {
            if shot.video.is_some() {
                shot.image = None;
            } else {
                let image = shot.image.as_ref().ok_or_else(|| {
                    ComposeError::Invalid(format!("shot {} has no selected keyframe", shot.id))
                })?;
                if let Some(relative) = copied.get(image) {
                    shot.image = Some(relative.clone());
                } else {
                    let source = compose::frame_path(&store, image)?;
                    total_bytes += source.metadata()?.len();
                    if total_bytes > 512 * 1024 * 1024 {
                        return Err(ComposeError::Invalid(
                            "an export's input frames exceed 512 MiB".into(),
                        ));
                    }
                    let extension = source
                        .extension()
                        .and_then(|extension| extension.to_str())
                        .unwrap_or("png");
                    let relative = format!(
                        "episodes/{}/export/inputs-{snapshot}/frame-{index:03}.{extension}",
                        plan.episode
                    );
                    store
                        .copy_artifact(&relative, &source)
                        .map_err(|error| ComposeError::Invalid(error.message()))?;
                    copied.insert(image.clone(), relative.clone());
                    shot.image = Some(relative);
                }
            }
            if let Some(audio) = &mut shot.audio {
                if let Some(relative) = copied.get(&audio.path) {
                    audio.path = relative.clone();
                } else {
                    let source = store
                        .resolve(&audio.path)
                        .map_err(|error| ComposeError::Invalid(error.message()))?;
                    total_bytes += source.metadata()?.len();
                    if total_bytes > 512 * 1024 * 1024 {
                        return Err(ComposeError::Invalid(
                            "an export's inputs exceed 512 MiB".into(),
                        ));
                    }
                    let extension = source
                        .extension()
                        .and_then(|extension| extension.to_str())
                        .unwrap_or("wav");
                    let relative = format!(
                        "episodes/{}/export/inputs-{snapshot}/audio-{index:03}.{extension}",
                        plan.episode
                    );
                    store
                        .copy_artifact(&relative, &source)
                        .map_err(|error| ComposeError::Invalid(error.message()))?;
                    copied.insert(audio.path.clone(), relative.clone());
                    audio.path = relative;
                }
            }
            if let Some(clip) = &mut shot.video {
                if let Some(relative) = copied.get(&clip.path) {
                    clip.path = relative.clone();
                } else {
                    let source = store
                        .resolve(&clip.path)
                        .map_err(|error| ComposeError::Invalid(error.message()))?;
                    total_bytes += source.metadata()?.len();
                    if total_bytes > 512 * 1024 * 1024 {
                        return Err(ComposeError::Invalid(
                            "an export's inputs exceed 512 MiB".into(),
                        ));
                    }
                    let extension = source
                        .extension()
                        .and_then(|extension| extension.to_str())
                        .unwrap_or("mp4");
                    let relative = format!(
                        "episodes/{}/export/inputs-{snapshot}/clip-{index:03}.{extension}",
                        plan.episode
                    );
                    store
                        .copy_artifact(&relative, &source)
                        .map_err(|error| ComposeError::Invalid(error.message()))?;
                    copied.insert(clip.path.clone(), relative.clone());
                    clip.path = relative;
                }
            }
        }
        Ok::<_, ComposeError>(plan)
    })
    .await
    .map_err(|error| ComposeError::Render(error.to_string()))??;
    let transaction = conn
        .begin()
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))?;
    let now = Utc::now();
    let row = comic_generation_task::ActiveModel {
        id: NotSet,
        folder_id: Set(folder_id),
        kind: Set("compose".into()),
        status: Set("queued".into()),
        provider_id: Set(None),
        provider_name: Set(None),
        model: Set(None),
        request_json: Set(serde_json::to_string(&plan)
            .map_err(|error| ComposeError::Invalid(error.to_string()))?),
        result_json: Set("{}".into()),
        artifact_path: Set(None),
        target_kind: Set(Some("episode".into())),
        target_id: Set(Some(format!("episodes/{}", plan.episode))),
        error_kind: Set(None),
        error_message: Set(None),
        candidates_json: Set("[]".into()),
        created_at: Set(now),
        updated_at: Set(now),
    }
    .insert(&transaction)
    .await
    .map_err(|error| ComposeError::Render(error.to_string()))?;
    transaction
        .commit()
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))?;
    task::emit_task(emitter, folder_id, row.id);
    let conn = conn.clone();
    let emitter = emitter.clone();
    let id = row.id;
    tokio::spawn(async move {
        run(conn, emitter, id, plan).await;
    });
    Ok(id)
}

pub async fn recover(
    conn: DatabaseConnection,
    emitter: EventEmitter,
    row: comic_generation_task::Model,
) {
    if row.status == "queued" {
        match serde_json::from_str::<ComposePlan>(&row.request_json) {
            Ok(plan) => {
                tokio::spawn(async move {
                    run(conn, emitter, row.id, plan).await;
                });
            }
            Err(error) => {
                record_error(
                    &conn,
                    &emitter,
                    row.id,
                    ComposeError::Invalid(error.to_string()),
                )
                .await
            }
        }
    } else {
        task::fail_task(&conn, &emitter, row.id, &GenerationError::new("interrupted", "Local composition was interrupted. Prior exports remain unchanged; request a new composition to retry.")).await;
    }
}

async fn record_error(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    id: i32,
    error: ComposeError,
) {
    task::fail_task(
        conn,
        emitter,
        id,
        &GenerationError::new(error.kind(), error.to_string()),
    )
    .await;
}

async fn run(conn: DatabaseConnection, emitter: EventEmitter, id: i32, plan: ComposePlan) {
    if let Err(error) = execute(&conn, &emitter, id, plan).await {
        record_error(&conn, &emitter, id, error).await;
    }
}

async fn execute(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    id: i32,
    plan: ComposePlan,
) -> Result<(), ComposeError> {
    let _global = task::job_slots()
        .acquire_owned()
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))?;
    let row = comic_generation_task::Entity::find_by_id(id)
        .one(conn)
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))?
        .ok_or_else(|| ComposeError::Invalid("composition task was removed".into()))?;
    if row.status != "queued" {
        return Ok(());
    }
    let folder = crate::db::service::folder_service::get_folder_by_id(conn, row.folder_id)
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))?
        .ok_or_else(|| ComposeError::Invalid("composition project was removed".into()))?;
    let store = StoryStore::open(Path::new(&folder.path))
        .map_err(|error| ComposeError::Invalid(error.message()))?;
    let limit = store
        .read_series()
        .map_err(|error| ComposeError::Invalid(error.message()))?
        .max_concurrent_tasks;
    let _project = task::project_slots(row.folder_id, limit as usize)
        .acquire_owned()
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))?;
    if task::set_row_status(conn, id, "running", json!({"stage": "rendering"}))
        .await
        .is_none()
    {
        return Ok(());
    }
    task::emit_task(emitter, row.folder_id, id);
    let cancel = CancellationToken::new();
    let _cancel_guard = cancel.clone().drop_guard();
    let watching = cancel.clone();
    let db = conn.clone();
    tokio::spawn(async move {
        loop {
            tokio::select! {
                _ = watching.cancelled() => return,
                _ = tokio::time::sleep(std::time::Duration::from_millis(200)) => {}
            }
            match task::check_task(&db, id, 0).await {
                Some(view) if matches!(view.status.as_str(), "queued" | "running") => {}
                _ => {
                    watching.cancel();
                    return;
                }
            }
        }
    });
    let rendered = compose::render(&store, &plan, &cancel).await?;
    let lock = task::task_lock(id);
    let _settlement = lock.lock().await;
    let current = task::check_task(conn, id, 0)
        .await
        .ok_or_else(|| ComposeError::Invalid("composition task was removed".into()))?;
    if current.status != "running" || cancel.is_cancelled() {
        return Ok(());
    }
    let output = format!("episodes/{}/export/task-{id}.mp4", plan.episode);
    let subtitles = format!("episodes/{}/export/task-{id}.srt", plan.episode);
    let folder = store.folder_path().to_path_buf();
    let video_path = rendered.video.clone();
    let subtitle_path = rendered.subtitles.clone();
    let output_copy = output.clone();
    let subtitle_copy = subtitles.clone();
    tokio::task::spawn_blocking(move || {
        let store =
            StoryStore::open(&folder).map_err(|error| ComposeError::Invalid(error.message()))?;
        store
            .copy_artifact(&subtitle_copy, &subtitle_path)
            .map_err(|error| ComposeError::Render(error.message()))?;
        store
            .copy_artifact(&output_copy, &video_path)
            .map_err(|error| ComposeError::Render(error.message()))
    })
    .await
    .map_err(|error| ComposeError::Render(error.to_string()))??;
    let active = comic_generation_task::ActiveModel {
        id: Set(id), status: Set("succeeded".into()), artifact_path: Set(Some(output)),
        result_json: Set(json!({ "subtitles": subtitles, "durationSecs": plan.shots.iter().map(|shot| u64::from(shot.frames)).sum::<u64>() as f64 / f64::from(plan.options.fps) }).to_string()),
        updated_at: Set(Utc::now()), ..Default::default()
    };
    active
        .update(conn)
        .await
        .map_err(|error| ComposeError::Render(error.to_string()))?;
    task::emit_task(emitter, row.folder_id, id);
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::comic::{
        model::{EpisodeMeta, StoryShot},
        store::StoryStore,
        task,
    };
    use crate::db::test_helpers::{fresh_in_memory_db, seed_folder};

    fn options() -> ComposeOptions {
        ComposeOptions {
            width: 128,
            height: 192,
            burn_subtitles: false,
            ken_burns: false,
            ..Default::default()
        }
    }

    fn project(path: &Path) -> StoryStore {
        let store = StoryStore::init(path, "Compose task fixture").unwrap();
        store
            .upsert_episode(&EpisodeMeta {
                id: "ep001".into(),
                ..Default::default()
            })
            .unwrap();
        let mut bytes = std::io::Cursor::new(Vec::new());
        image::DynamicImage::new_rgb8(4, 4)
            .write_to(&mut bytes, image::ImageFormat::Png)
            .unwrap();
        let image = "episodes/ep001/shots/shot-0001/candidates/task-1-01.png";
        store.write_artifact(image, &bytes.into_inner()).unwrap();
        store
            .replace_storyboard(
                "ep001",
                vec![StoryShot {
                    id: "shot-0001".into(),
                    selected: Some(image.into()),
                    duration_secs: 0.5,
                    ..Default::default()
                }],
            )
            .unwrap();
        store
    }

    #[tokio::test]
    async fn missing_media_tools_do_not_copy_inputs_or_queue_composition() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let tools = super::super::media_tools::MediaTools {
            ffmpeg: "codeg-missing-ffmpeg-test",
            ffprobe: "codeg-missing-ffprobe-test",
        };
        let result = submit_with_tools(
            &db.conn,
            &EventEmitter::Noop,
            folder,
            dir.path(),
            "ep001",
            options(),
            &tools,
        )
        .await;
        assert!(matches!(result, Err(ComposeError::Unavailable)));
        assert!(task::list_tasks(&db.conn, folder, None, 50)
            .await
            .is_empty());
        assert!(!store.root().join("episodes/ep001/export").exists());
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn queued_video_only_inputs_are_frozen_without_a_keyframe() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let mut board = store.read_storyboard("ep001").unwrap();
        board.shots[0].selected = None;
        store.write_storyboard("ep001", &board).unwrap();
        let clip = "episodes/ep001/shots/shot-0001/video/task-9.mp4";
        let bytes = crate::comic::video::mp4_fixture(dir.path(), false).await;
        store.write_artifact(clip, &bytes).unwrap();
        store
            .bind_shot_video(
                "ep001",
                "shot-0001",
                crate::comic::model::ShotClip {
                    path: clip.into(),
                    duration_secs: 0.5,
                    task_id: 9,
                },
                None,
            )
            .unwrap();
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let permit = task::job_slots().acquire_many_owned(8).await.unwrap();
        let id = submit(
            &db.conn,
            &EventEmitter::Noop,
            folder,
            dir.path(),
            "ep001",
            options(),
        )
        .await
        .unwrap();
        let row = comic_generation_task::Entity::find_by_id(id)
            .one(&db.conn)
            .await
            .unwrap()
            .unwrap();
        let request: serde_json::Value = serde_json::from_str(&row.request_json).unwrap();
        assert!(request["shots"][0]["image"].is_null());
        assert_ne!(request["shots"][0]["video"]["path"], clip);
        std::fs::remove_file(store.resolve(clip).unwrap()).unwrap();
        drop(permit);
        let finished = task::check_task(&db.conn, id, 60_000).await.unwrap();
        assert_eq!(finished.status, "succeeded", "{:?}", finished.error_message);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn accepted_media_stays_frozen_through_queueing_and_recovery() {
        use sea_orm::IntoActiveModel;

        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let permit = task::job_slots().acquire_many_owned(8).await.unwrap();
        let id = submit(
            &db.conn,
            &EventEmitter::Noop,
            folder,
            dir.path(),
            "ep001",
            options(),
        )
        .await
        .unwrap();
        let row = comic_generation_task::Entity::find_by_id(id)
            .one(&db.conn)
            .await
            .unwrap()
            .unwrap();
        let image = store.read_storyboard("ep001").unwrap().shots[0]
            .selected
            .clone()
            .unwrap();
        std::fs::remove_file(store.resolve(&image).unwrap()).unwrap();
        drop(permit);
        let finished = task::check_task(&db.conn, id, 60_000).await.unwrap();
        assert_eq!(finished.status, "succeeded", "{:?}", finished.error_message);
        // A restart reads only the persisted request and its durable input files.
        let mut queued = row.into_active_model();
        queued.id = NotSet;
        queued.status = Set("queued".into());
        let queued = queued.insert(&db.conn).await.unwrap();
        let recovered_id = queued.id;
        recover(db.conn.clone(), EventEmitter::Noop, queued).await;
        let recovered = task::check_task(&db.conn, recovered_id, 60_000)
            .await
            .unwrap();
        assert_eq!(
            recovered.status, "succeeded",
            "{:?}",
            recovered.error_message
        );
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn compose_task_publishes_an_immutable_export_without_a_media_provider() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let id = submit(
            &db.conn,
            &EventEmitter::Noop,
            folder,
            dir.path(),
            "ep001",
            options(),
        )
        .await
        .unwrap();
        let view = task::check_task(&db.conn, id, 60_000).await.unwrap();
        assert_eq!(view.status, "succeeded", "{:?}", view.error_message);
        assert_eq!(view.kind, "compose");
        assert!(view.provider_name.is_none());
        assert!(view.candidates.is_empty());
        assert_eq!(
            view.artifact_path.as_deref(),
            Some(format!("episodes/ep001/export/task-{id}.mp4").as_str())
        );
        let output = store.resolve(view.artifact_path.as_ref().unwrap()).unwrap();
        assert!(output.metadata().unwrap().len() > 0);
        assert!(store
            .resolve(&format!("episodes/ep001/export/task-{id}.srt"))
            .unwrap()
            .is_file());
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn duplicate_and_cancelled_compositions_do_not_publish_an_export() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let slots = task::job_slots();
        let permit = slots.acquire_many_owned(8).await.unwrap();
        let id = submit(
            &db.conn,
            &EventEmitter::Noop,
            folder,
            dir.path(),
            "ep001",
            options(),
        )
        .await
        .unwrap();
        let duplicate = submit(
            &db.conn,
            &EventEmitter::Noop,
            folder,
            dir.path(),
            "ep001",
            options(),
        )
        .await;
        assert!(duplicate.is_err());
        task::cancel_task(&db.conn, &EventEmitter::Noop, id)
            .await
            .unwrap();
        drop(permit);
        let view = task::check_task(&db.conn, id, 0).await.unwrap();
        assert_eq!(view.status, "cancelled");
        assert!(!store
            .resolve(&format!("episodes/ep001/export/task-{id}.mp4"))
            .unwrap()
            .exists());
    }
}
