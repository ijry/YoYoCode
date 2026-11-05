//! Generation tasks — the durable side of the media pipeline.
//!
//! One row of `comic_generation_task` per job. The row is written at submit
//! time and advanced by an in-process worker (`run_task`), so a closed window
//! or crashed session loses nothing that matters: artifacts land in the
//! `story/` tree, and the row records what ran. `recover_pending` re-queues
//! rows a restart interrupted, called once at startup from both binaries.
//!
//! Concurrency: a process-wide semaphore bounds in-flight vendor jobs (the
//! series config's `max_concurrent_tasks`, clamped). Cancel is cooperative —
//! `cancel_task` flips the row; the worker checks between polls and refuses
//! to write results into a cancelled row.

use std::sync::Arc;

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ActiveValue::NotSet, ColumnTrait, Condition, DatabaseConnection, EntityTrait,
    QueryFilter, QueryOrder, QuerySelect, Set,
};
use serde::Serialize;
use serde_json::{json, Value};
use tokio::sync::Semaphore;

use crate::db::entities::comic_generation_task;
use crate::db::service::media_provider_service;
use crate::web::event_bridge::{emit_event, ComicChange, EventEmitter, COMIC_CHANGED_EVENT};

use super::model::{GenerationRecord, ShotState, Storyboard};
use super::provider::{
    self, GenerationError, GenerationRequest, MediaKind, MediaProvider, PollState,
};
use super::store::StoryStore;

/// Process-wide in-flight vendor jobs. Generous enough that a single project's
/// `max_concurrent_tasks` is the effective bound; small enough that a runaway
/// batch cannot monopolize the app's connections.
pub(super) fn job_slots() -> Arc<Semaphore> {
    static SLOTS: std::sync::OnceLock<Arc<Semaphore>> = std::sync::OnceLock::new();
    SLOTS.get_or_init(|| Arc::new(Semaphore::new(8))).clone()
}

fn provider_slots(provider_id: i32) -> Arc<Semaphore> {
    static SLOTS: std::sync::OnceLock<
        std::sync::Mutex<std::collections::HashMap<i32, Arc<Semaphore>>>,
    > = std::sync::OnceLock::new();
    SLOTS
        .get_or_init(Default::default)
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .entry(provider_id)
        .or_insert_with(|| Arc::new(Semaphore::new(2)))
        .clone()
}

pub(super) fn project_slots(folder_id: i32, limit: usize) -> Arc<Semaphore> {
    static SLOTS: std::sync::OnceLock<
        std::sync::Mutex<std::collections::HashMap<i32, Arc<Semaphore>>>,
    > = std::sync::OnceLock::new();
    SLOTS
        .get_or_init(Default::default)
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .entry(folder_id)
        .or_insert_with(|| Arc::new(Semaphore::new(limit.clamp(1, 8))))
        .clone()
}

pub(super) fn task_lock(task_id: i32) -> Arc<tokio::sync::Mutex<()>> {
    static LOCKS: std::sync::OnceLock<
        std::sync::Mutex<std::collections::HashMap<i32, std::sync::Weak<tokio::sync::Mutex<()>>>>,
    > = std::sync::OnceLock::new();
    let mut locks = LOCKS
        .get_or_init(Default::default)
        .lock()
        .unwrap_or_else(|e| e.into_inner());
    locks.retain(|_, lock| lock.strong_count() > 0);
    if let Some(lock) = locks.get(&task_id).and_then(std::sync::Weak::upgrade) {
        return lock;
    }
    let lock = Arc::new(tokio::sync::Mutex::new(()));
    locks.insert(task_id, Arc::downgrade(&lock));
    lock
}

/// A task as the workbench and `comic_check_generation` see it — no request
/// payloads, just state.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TaskView {
    pub id: i32,
    pub folder_id: i32,
    pub kind: String,
    pub status: String,
    pub provider_name: Option<String>,
    pub model: Option<String>,
    pub target_kind: Option<String>,
    pub target_id: Option<String>,
    pub artifact_path: Option<String>,
    pub error_kind: Option<String>,
    pub error_message: Option<String>,
    pub candidates: Vec<String>,
    pub created_at: String,
}

fn row_to_view(row: comic_generation_task::Model) -> TaskView {
    let candidates: Vec<String> = serde_json::from_str(&row.candidates_json).unwrap_or_default();
    TaskView {
        id: row.id,
        folder_id: row.folder_id,
        kind: row.kind,
        status: row.status,
        provider_name: row.provider_name,
        model: row.model,
        target_kind: row.target_kind,
        target_id: row.target_id,
        artifact_path: row.artifact_path,
        error_kind: row.error_kind,
        error_message: row.error_message,
        candidates,
        created_at: row.created_at.to_rfc3339(),
    }
}

pub(super) fn emit_task(emitter: &EventEmitter, folder_id: i32, task_id: i32) {
    emit_event(
        emitter,
        COMIC_CHANGED_EVENT,
        &ComicChange::Task { folder_id, task_id },
    );
}

/// Submit one generation job. The provider row is resolved before the row is
/// created, so a bad pointer refuses without leaving a task behind.
#[allow(clippy::too_many_arguments)]
pub async fn submit_generation(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &std::path::Path,
    kind: MediaKind,
    target_kind: &str,
    target_id: &str,
    req: GenerationRequest,
    provider_id: Option<i32>,
    provider_name_hint: Option<String>,
) -> Result<i32, GenerationError> {
    let store = StoryStore::open(folder_path)
        .map_err(|e| GenerationError::new("bad_request", e.message()))?;
    let series = store
        .read_series()
        .map_err(|e| GenerationError::new("bad_request", e.message()))?;

    // Resolve the provider: explicit override first, then the project
    // pointer, then the pool's default for the kind.
    let pointer = series.providers.get(kind.as_str());
    let row = if let Some(id) = provider_id {
        media_provider_service::get_by_id(conn, id)
            .await
            .map_err(|e| GenerationError::new("internal", e.to_string()))?
            .filter(|row| row.kind == kind.as_str())
            .ok_or_else(|| {
                GenerationError::new(
                    "bad_request",
                    format!("no media provider {id} of kind {}", kind.as_str()),
                )
            })?
    } else if let Some(name) = provider_name_hint {
        media_provider_service::resolve_ref(conn, kind.as_str(), 0, &name)
            .await
            .map_err(|e| GenerationError::new("internal", e.to_string()))?
            .ok_or_else(|| {
                GenerationError::new("bad_request", "requested provider name is not configured")
            })?
    } else if let Some(pointer) = pointer {
        media_provider_service::resolve_ref(conn, kind.as_str(), pointer.id, &pointer.name)
            .await
            .map_err(|e| GenerationError::new("internal", e.to_string()))?
            .ok_or_else(|| {
                GenerationError::new(
                    "bad_request",
                    format!(
                        "the project's {} provider ({}, #{}) no longer exists — re-point it \
                     with comic_set_project_provider",
                        kind.as_str(),
                        pointer.name,
                        pointer.id
                    ),
                )
            })?
    } else {
        media_provider_service::default_for_kind(conn, kind.as_str())
            .await
            .map_err(|e| GenerationError::new("internal", e.to_string()))?
            .ok_or_else(|| {
                GenerationError::new(
                    "bad_request",
                    format!(
                        "no {} provider configured: point the project at one \
                         (comic_set_project_provider) or add a default in settings",
                        kind.as_str()
                    ),
                )
            })?
    };

    let adapter = provider::build_provider(
        kind,
        &row.api_url,
        &row.api_key,
        &row.model,
        &row.extra_json,
    )?;

    let mut req = req;
    adapter.prepare(&mut req)?;

    use sea_orm::TransactionTrait;
    // SQLite serializes the insert transaction. Guarding queue admission keeps
    // repeated tool calls from paying for duplicate active batches of a shot.
    static ADMISSION: std::sync::OnceLock<tokio::sync::Mutex<()>> = std::sync::OnceLock::new();
    let _admission = ADMISSION
        .get_or_init(|| tokio::sync::Mutex::new(()))
        .lock()
        .await;
    let transaction = conn
        .begin()
        .await
        .map_err(|e| GenerationError::new("internal", e.to_string()))?;
    let duplicate = comic_generation_task::Entity::find()
        .filter(comic_generation_task::Column::FolderId.eq(folder_id))
        .filter(comic_generation_task::Column::TargetId.eq(target_id))
        .filter(comic_generation_task::Column::Kind.eq(kind.as_str()))
        .filter(comic_generation_task::Column::Status.is_in(["queued", "running"]))
        .one(&transaction)
        .await
        .map_err(|e| GenerationError::new("internal", e.to_string()))?;
    if let Some(existing) = duplicate {
        return Err(GenerationError::new(
            "busy",
            format!("task {} is already generating this shot", existing.id),
        ));
    }
    let now = Utc::now();
    let active = comic_generation_task::ActiveModel {
        id: NotSet,
        folder_id: Set(folder_id),
        kind: Set(kind.as_str().to_string()),
        status: Set("queued".to_string()),
        provider_id: Set(Some(row.id)),
        provider_name: Set(Some(row.name.clone())),
        model: Set(Some(row.model.clone())),
        request_json: Set(serde_json::to_string(&req).unwrap_or_else(|_| "{}".to_string())),
        result_json: Set("{}".to_string()),
        artifact_path: Set(None),
        target_kind: Set(Some(target_kind.to_string())),
        target_id: Set(Some(target_id.to_string())),
        error_kind: Set(None),
        error_message: Set(None),
        candidates_json: Set("[]".to_string()),
        created_at: Set(now),
        updated_at: Set(now),
    };
    let row = active
        .insert(&transaction)
        .await
        .map_err(|e| GenerationError::new("internal", e.to_string()))?;
    transaction
        .commit()
        .await
        .map_err(|e| GenerationError::new("internal", e.to_string()))?;
    emit_task(emitter, folder_id, row.id);

    let conn = conn.clone();
    let emitter = emitter.clone();
    let task_id = row.id;
    tokio::spawn(async move {
        run_task(conn, emitter, task_id, adapter, req).await;
    });
    Ok(task_id)
}

/// The worker: one task, end to end. Everything it writes is behind the
/// story store's write mutex and re-checked against the row's status, so a
/// cancel always wins.
async fn run_task(
    conn: DatabaseConnection,
    emitter: EventEmitter,
    task_id: i32,
    adapter: Box<dyn MediaProvider>,
    req: GenerationRequest,
) {
    let mut req = req;
    let Ok(_slot) = job_slots().acquire_owned().await else {
        return;
    };
    let Ok(Some(row)) = comic_generation_task::Entity::find_by_id(task_id)
        .one(&conn)
        .await
    else {
        return;
    };
    if !matches!(row.status.as_str(), "queued" | "running") {
        return;
    }
    let limit =
        match crate::db::service::folder_service::get_folder_by_id(&conn, row.folder_id).await {
            Ok(Some(folder)) => StoryStore::open(std::path::Path::new(&folder.path))
                .ok()
                .and_then(|store| store.read_series().ok())
                .map(|series| series.max_concurrent_tasks as usize)
                .unwrap_or(2),
            _ => 2,
        };
    let Ok(_project_slot) = project_slots(row.folder_id, limit).acquire_owned().await else {
        return;
    };
    let Ok(_provider_slot) = provider_slots(row.provider_id.unwrap_or(0))
        .acquire_owned()
        .await
    else {
        return;
    };
    if task_status(&conn, task_id).await.as_deref() == Some("cancelled") {
        return;
    }
    if !req.reference_paths.is_empty() || req.first_frame_path.is_some() {
        let references = async {
            let folder = crate::db::service::folder_service::get_folder_by_id(&conn, row.folder_id)
                .await
                .map_err(|e| GenerationError::new("bad_request", e.to_string()))?
                .ok_or_else(|| GenerationError::new("bad_request", "comic project was removed"))?;
            let store = StoryStore::open(std::path::Path::new(&folder.path))
                .map_err(|e| GenerationError::new("bad_request", e.message()))?;
            provider::load_references(&store, &mut req)?;
            if let Some(path) = req.first_frame_path.clone() {
                load_first_frame(&store, &path, &mut req)?;
            }
            Ok::<(), GenerationError>(())
        }
        .await;
        if let Err(error) = references {
            fail_task(&conn, &emitter, task_id, &error).await;
            return;
        }
    }
    let previous: Value = serde_json::from_str(&row.result_json).unwrap_or(Value::Null);
    let handle = if row.status == "running" {
        match previous
            .get("handle")
            .filter(|handle| handle.get("mode") != Some(&json!("inline")))
        {
            Some(handle) => handle.clone(),
            None => {
                fail_task(
                    &conn,
                    &emitter,
                    task_id,
                    &GenerationError::new(
                        "interrupted",
                        "No resumable remote handle; the request was not resubmitted.",
                    ),
                )
                .await;
                return;
            }
        }
    } else {
        // Persist before contacting the vendor: a crash during submission is
        // ambiguous and must not lead to a second charge on restart.
        if set_row_status(&conn, task_id, "running", json!({"stage": "submitting"}))
            .await
            .is_none()
        {
            return;
        }
        emit_task(&emitter, row.folder_id, task_id);
        match adapter.submit(&req).await {
            Ok(handle) => {
                if set_row_status(&conn, task_id, "running", json!({"handle": handle}))
                    .await
                    .is_none()
                {
                    return;
                }
                handle
            }
            Err(error) => {
                fail_task(&conn, &emitter, task_id, &error).await;
                return;
            }
        }
    };

    let inline = handle.get("mode").and_then(Value::as_str) == Some("inline");
    let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(15 * 60);
    let mut poll_failures = 0;
    loop {
        if task_status(&conn, task_id).await.as_deref() != Some("running") {
            return;
        }
        let polled = tokio::time::timeout_at(deadline, adapter.poll(&handle, &req)).await;
        match polled {
            Ok(Ok(PollState::Running(progress))) => {
                poll_failures = 0;
                if set_row_status(
                    &conn,
                    task_id,
                    "running",
                    json!({"handle": handle, "progress": progress}),
                )
                .await
                .is_none()
                {
                    return;
                }
                emit_task(&emitter, row.folder_id, task_id);
            }
            Ok(Ok(PollState::Succeeded(artifacts))) => {
                finish_task(&conn, &emitter, task_id, artifacts, &req).await;
                return;
            }
            Ok(Ok(PollState::Failed(error))) => {
                fail_task(&conn, &emitter, task_id, &error).await;
                return;
            }
            Ok(Err(error)) => {
                poll_failures += 1;
                // Only retry read-only remote polling. The inline adapter's
                // poll performs a paid POST, so replaying it is not safe.
                if inline || error.kind != "network" || poll_failures >= 5 {
                    fail_task(&conn, &emitter, task_id, &error).await;
                    return;
                }
            }
            Err(_) => {
                fail_task(
                    &conn,
                    &emitter,
                    task_id,
                    &GenerationError::new("network", "generation timed out after 15 minutes"),
                )
                .await;
                return;
            }
        }
        tokio::time::sleep(std::time::Duration::from_secs(2)).await;
    }
}

/// Load the shot's bound first-frame image into the request just before
/// submission. The task row keeps only the story-relative path.
fn load_first_frame(
    store: &StoryStore,
    path: &str,
    req: &mut GenerationRequest,
) -> Result<(), GenerationError> {
    use std::io::Read;

    const MAX_FIRST_FRAME_BYTES: usize = 32 * 1024 * 1024;
    let absolute = store
        .resolve(path)
        .map_err(|e| GenerationError::new("bad_request", e.message()))?;
    let mut bytes = Vec::new();
    std::fs::File::open(&absolute)
        .and_then(|mut file| {
            file.take(MAX_FIRST_FRAME_BYTES as u64 + 1)
                .read_to_end(&mut bytes)
        })
        .map_err(|e| {
            GenerationError::new(
                "bad_request",
                format!("cannot read first frame {path}: {e}"),
            )
        })?;
    if bytes.len() > MAX_FIRST_FRAME_BYTES {
        return Err(GenerationError::new(
            "bad_request",
            "first-frame image exceeds 32 MiB",
        ));
    }
    let format = image::guess_format(&bytes).map_err(|e| {
        GenerationError::new("bad_request", format!("invalid first-frame image: {e}"))
    })?;
    let mime = match format {
        image::ImageFormat::Png => "image/png",
        image::ImageFormat::Jpeg => "image/jpeg",
        image::ImageFormat::WebP => "image/webp",
        _ => {
            return Err(GenerationError::new(
                "bad_request",
                "unsupported first-frame image format",
            ))
        }
    };
    req.first_frame = Some(provider::ReferenceImage {
        name: absolute
            .file_name()
            .map(|s| s.to_string_lossy().into_owned())
            .unwrap_or_else(|| "first-frame.png".into()),
        mime: mime.into(),
        data: bytes,
    });
    Ok(())
}

async fn task_status(conn: &DatabaseConnection, task_id: i32) -> Option<String> {
    comic_generation_task::Entity::find_by_id(task_id)
        .one(conn)
        .await
        .ok()
        .flatten()
        .map(|row| row.status)
}

async fn task_folder(conn: &DatabaseConnection, task_id: i32) -> Option<i32> {
    comic_generation_task::Entity::find_by_id(task_id)
        .one(conn)
        .await
        .ok()
        .flatten()
        .map(|row| row.folder_id)
}

pub(super) async fn fail_task(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    task_id: i32,
    err: &GenerationError,
) {
    use sea_orm::sea_query::Expr;

    let changed = comic_generation_task::Entity::update_many()
        .col_expr(comic_generation_task::Column::Status, Expr::value("failed"))
        .col_expr(
            comic_generation_task::Column::ErrorKind,
            Expr::value(err.kind.clone()),
        )
        .col_expr(
            comic_generation_task::Column::ErrorMessage,
            Expr::value(err.message.clone()),
        )
        .col_expr(
            comic_generation_task::Column::UpdatedAt,
            Expr::value(Utc::now()),
        )
        .filter(comic_generation_task::Column::Id.eq(task_id))
        .filter(comic_generation_task::Column::Status.is_in(["queued", "running"]))
        .exec(conn)
        .await;
    match changed {
        Ok(result) if result.rows_affected > 0 => {
            if let Some(folder_id) = task_folder(conn, task_id).await {
                emit_task(emitter, folder_id, task_id);
            }
        }
        Err(error) => tracing::error!(task_id, %error, "cannot record comic generation failure"),
        _ => {}
    }
}

/// Write the artifacts into the story tree and settle the row.
async fn finish_task(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    task_id: i32,
    artifacts: Vec<Vec<u8>>,
    req: &GenerationRequest,
) {
    let lock = task_lock(task_id);
    let _guard = lock.lock().await;
    let Ok(Some(row)) = comic_generation_task::Entity::find_by_id(task_id)
        .one(conn)
        .await
    else {
        return;
    };
    if !matches!(row.status.as_str(), "queued" | "running") {
        return;
    }
    let folder_id = row.folder_id;
    let target_kind = row.target_kind.clone().unwrap_or_default();
    let target_id = row.target_id.clone().unwrap_or_default();

    // Candidate names include their task id; regeneration never overwrites
    // a previously bound image. Other targets use generated/<task>-NN.ext.
    let mut candidate_rels: Vec<String> = Vec::new();
    let mut primary: Option<String> = None;
    let mut write_error: Option<String> = None;

    let store = match crate::db::service::folder_service::get_folder_by_id(conn, folder_id).await {
        Ok(Some(folder_row)) => StoryStore::open(std::path::Path::new(&folder_row.path)).ok(),
        _ => None,
    };
    let Some(store) = store else {
        fail_task(
            conn,
            emitter,
            task_id,
            &GenerationError::new("internal", "the comic project is no longer available"),
        )
        .await;
        return;
    };
    if artifacts.is_empty() {
        fail_task(
            conn,
            emitter,
            task_id,
            &GenerationError::new("bad_response", "the provider returned no artifacts"),
        )
        .await;
        return;
    }
    let artifacts = if row.kind == "image" {
        let validation = tokio::task::spawn_blocking(move || {
            artifacts
                .into_iter()
                .map(|bytes| image_artifact_ext(&bytes).map(|extension| (bytes, extension)))
                .collect::<Result<Vec<_>, GenerationError>>()
        })
        .await
        .unwrap_or_else(|error| Err(GenerationError::new("internal", error.to_string())));
        match validation {
            Ok(artifacts) => artifacts,
            Err(error) => {
                fail_task(conn, emitter, task_id, &error).await;
                return;
            }
        }
    } else if row.kind == "tts" {
        if artifacts.len() != 1 {
            fail_task(
                conn,
                emitter,
                task_id,
                &GenerationError::new("bad_response", "speech needs exactly one audio artifact"),
            )
            .await;
            return;
        }
        let bytes = artifacts.into_iter().next().unwrap();
        let inspected =
            super::audio::inspect_bytes(&bytes, &tokio_util::sync::CancellationToken::new()).await;
        match inspected {
            Ok(info) => {
                let extension = if info.extension == "wav" {
                    "wav"
                } else {
                    "mp3"
                };
                vec![(bytes, extension)]
            }
            Err(error) => {
                fail_task(conn, emitter, task_id, &error).await;
                return;
            }
        }
    } else if row.kind == "video" {
        if artifacts.len() != 1 {
            fail_task(
                conn,
                emitter,
                task_id,
                &GenerationError::new("bad_response", "video needs exactly one clip artifact"),
            )
            .await;
            return;
        }
        let bytes = artifacts.into_iter().next().unwrap();
        match super::video::inspect_bytes(&bytes, &tokio_util::sync::CancellationToken::new()).await
        {
            Ok(info) => {
                let extension = if info.extension == "webm" {
                    "webm"
                } else if info.extension == "mkv" {
                    "mkv"
                } else {
                    "mp4"
                };
                vec![(bytes, extension)]
            }
            Err(error) => {
                fail_task(conn, emitter, task_id, &error).await;
                return;
            }
        }
    } else {
        artifacts
            .into_iter()
            .map(|bytes| {
                let extension = sniff_ext(&bytes);
                (bytes, extension)
            })
            .collect()
    };
    {
        for (idx, (bytes, ext)) in artifacts.iter().enumerate() {
            let rel = match target_kind.as_str() {
                "shot" if row.kind == "tts" => format!(
                    "episodes/{}/shots/{}/audio/task-{task_id}.{}",
                    target_id_episode(&target_id),
                    target_id_shot(&target_id),
                    ext
                ),
                "shot" if row.kind == "video" => format!(
                    "episodes/{}/shots/{}/video/task-{task_id}.{}",
                    target_id_episode(&target_id),
                    target_id_shot(&target_id),
                    ext
                ),
                "shot" => format!(
                    "episodes/{}/shots/{}/candidates/task-{task_id}-{:02}.{}",
                    target_id_episode(&target_id),
                    target_id_shot(&target_id),
                    idx + 1,
                    ext
                ),
                _ => format!("generated/task-{}-{:02}.{}", task_id, idx + 1, ext),
            };
            match store.write_artifact(&rel, bytes) {
                Ok(()) => {
                    if primary.is_none() {
                        primary = Some(rel.clone());
                    }
                    if row.kind == "image" {
                        candidate_rels.push(rel);
                    }
                }
                Err(e) => {
                    write_error = Some(e.message());
                    break;
                }
            }
        }
        // Shot bookkeeping: generation record + state → candidate.
        if target_kind == "shot" && write_error.is_none() {
            let (episode, shot) = split_shot_target(&target_id);
            let record = GenerationRecord {
                task_id,
                kind: row.kind.clone(),
                provider_id: row.provider_id,
                provider_name: row.provider_name.clone(),
                model: row.model.clone(),
                prompt: Some(req.prompt.clone()),
                seed: req.seed,
                at: Utc::now().to_rfc3339(),
                parameters: serde_json::to_value(req).unwrap_or(Value::Null),
            };
            append_generation(&store, episode, shot, record);
            if row.kind == "image" {
                mark_candidate(&store, episode, shot);
            }
        }
    }

    let mut active = comic_generation_task::ActiveModel {
        id: Set(row.id),
        ..Default::default()
    };
    if let Some(err_text) = write_error {
        active.status = Set("failed".to_string());
        active.error_kind = Set(Some("internal".to_string()));
        active.error_message = Set(Some(err_text));
    } else {
        active.status = Set("succeeded".to_string());
        active.artifact_path = Set(primary.clone());
        active.candidates_json = Set(serde_json::to_string(&candidate_rels).unwrap_or_default());
        active.result_json = Set(
            serde_json::to_string(&json!({ "count": candidate_rels.len() })).unwrap_or_default(),
        );
    }
    active.updated_at = Set(Utc::now());
    let _ = active.update(conn).await;
    emit_task(emitter, folder_id, task_id);
}

fn target_id_episode(target: &str) -> &str {
    // `episodes/ep001/shots/shot-0003` → `ep001`
    target
        .strip_prefix("episodes/")
        .and_then(|rest| rest.split('/').next())
        .unwrap_or(target)
}

fn target_id_shot(target: &str) -> &str {
    target.rsplit('/').next().unwrap_or(target)
}

fn split_shot_target(target: &str) -> (&str, &str) {
    (target_id_episode(target), target_id_shot(target))
}

fn image_artifact_ext(bytes: &[u8]) -> Result<&'static str, GenerationError> {
    let invalid = |message| GenerationError::new("bad_response", message);
    if bytes.len() > 64 * 1024 * 1024 {
        return Err(invalid("a generated image exceeds 64 MiB"));
    }
    let format = image::guess_format(bytes)
        .map_err(|_| invalid("the provider returned an unrecognized image"))?;
    let extension = match format {
        image::ImageFormat::Png => "png",
        image::ImageFormat::Jpeg => "jpg",
        image::ImageFormat::WebP => "webp",
        _ => return Err(invalid("generated images must be PNG, JPEG or WebP")),
    };
    let mut reader = image::ImageReader::with_format(std::io::Cursor::new(bytes), format);
    let mut limits = image::Limits::default();
    limits.max_image_width = Some(16_384);
    limits.max_image_height = Some(16_384);
    limits.max_alloc = Some(128 * 1024 * 1024);
    reader.limits(limits);
    let decoded = reader
        .decode()
        .map_err(|_| invalid("the generated image is corrupt or exceeds decoding limits"))?;
    if decoded.width() == 0 || decoded.height() == 0 {
        return Err(invalid("the generated image is empty"));
    }
    Ok(extension)
}

fn sniff_ext(bytes: &[u8]) -> &'static str {
    if bytes.starts_with(&[0x89, b'P', b'N', b'G']) {
        "png"
    } else if bytes.starts_with(&[0xFF, 0xD8, 0xFF]) {
        "jpg"
    } else if bytes.len() > 12 && &bytes[0..4] == b"RIFF" && &bytes[8..12] == b"WEBP" {
        "webp"
    } else if bytes.starts_with(b"ID3") || bytes.starts_with(&[0xFF, 0xFB]) {
        "mp3"
    } else {
        "bin"
    }
}

fn append_generation(store: &StoryStore, episode: &str, shot: &str, record: GenerationRecord) {
    if let Ok(mut meta) = store.read_shot_meta(episode, shot) {
        meta.generations.push(record);
        let _ = store.write_shot_meta(episode, shot, &meta);
    }
}

fn mark_candidate(store: &StoryStore, episode: &str, shot: &str) {
    let Ok(mut board) = store.read_storyboard(episode) else {
        return;
    };
    let Some(target) = board.shots.iter_mut().find(|s| s.id == shot) else {
        return;
    };
    if matches!(target.state, ShotState::Prompted | ShotState::Generating) {
        target.state = ShotState::Candidate;
        let _ = store.write_storyboard(episode, &board);
    }
}

/// Long-poll one task. Terminal rows return immediately; queued/running rows
/// wait up to `wait_ms` (0 = snapshot only).
pub async fn check_task(conn: &DatabaseConnection, task_id: i32, wait_ms: u64) -> Option<TaskView> {
    let deadline = tokio::time::Instant::now() + std::time::Duration::from_millis(wait_ms);
    loop {
        let row = comic_generation_task::Entity::find_by_id(task_id)
            .one(conn)
            .await
            .ok()
            .flatten()?;
        let terminal = matches!(row.status.as_str(), "succeeded" | "failed" | "cancelled");
        if terminal || tokio::time::Instant::now() >= deadline {
            return Some(row_to_view(row));
        }
        tokio::time::sleep(std::time::Duration::from_millis(500)).await;
    }
}

/// Mark a task cancelled; the worker notices between polls and drops out.
pub async fn cancel_task(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    task_id: i32,
) -> Option<TaskView> {
    let lock = task_lock(task_id);
    let _guard = lock.lock().await;
    let row = comic_generation_task::Entity::find_by_id(task_id)
        .one(conn)
        .await
        .ok()
        .flatten()?;
    if matches!(row.status.as_str(), "succeeded" | "failed" | "cancelled") {
        return Some(row_to_view(row));
    }
    let mut active = comic_generation_task::ActiveModel {
        id: Set(row.id),
        ..Default::default()
    };
    active.status = Set("cancelled".to_string());
    active.updated_at = Set(Utc::now());
    if let Ok(updated) = active.update(conn).await {
        emit_task(emitter, row.folder_id, task_id);
        return Some(row_to_view(updated));
    }
    None
}

/// Tasks for a project, newest first. `episode` scopes to one episode's
/// targets (its compose row or any shot under it) BEFORE the limit applies,
/// so newer unrelated tasks cannot push an episode's rows out of the window.
pub async fn list_tasks(
    conn: &DatabaseConnection,
    folder_id: i32,
    episode: Option<&str>,
    limit: u64,
) -> Vec<TaskView> {
    if episode.is_some_and(|episode| super::paths::validate_slug(episode).is_err()) {
        return Vec::new();
    }
    let mut query = comic_generation_task::Entity::find()
        .filter(comic_generation_task::Column::FolderId.eq(folder_id));
    if let Some(episode) = episode {
        let prefix = format!("episodes/{episode}");
        query = query.filter(
            Condition::any()
                .add(comic_generation_task::Column::TargetId.eq(prefix.clone()))
                .add(comic_generation_task::Column::TargetId.starts_with(format!("{prefix}/"))),
        );
    }
    query
        .order_by_desc(comic_generation_task::Column::Id)
        .limit(limit.clamp(1, 200))
        .all(conn)
        .await
        .map(|rows| rows.into_iter().map(row_to_view).collect())
        .unwrap_or_default()
}

/// Reconcile interrupted tasks without repeating an ambiguous paid submit.
/// A running job needs its durable remote handle; older rows without one are
/// failed explicitly so the user can decide whether to submit again.
pub async fn recover_pending(conn: DatabaseConnection, emitter: EventEmitter) {
    let Ok(rows) = comic_generation_task::Entity::find()
        .filter(comic_generation_task::Column::Status.is_in(["queued", "running"]))
        .all(&conn)
        .await
    else {
        return;
    };
    for row in rows {
        if row.kind == "compose" {
            super::compose_task::recover(conn.clone(), emitter.clone(), row).await;
            continue;
        }
        if row.status == "running" {
            let state: Value = serde_json::from_str(&row.result_json).unwrap_or(Value::Null);
            if state.get("handle").is_none() {
                fail_task(
                    &conn,
                    &emitter,
                    row.id,
                    &GenerationError::new(
                        "interrupted",
                        "Generation was interrupted without a saved remote task handle; it was not resubmitted. Check the provider before requesting another batch.",
                    ),
                )
                .await;
                continue;
            }
        }
        let recovery = async {
            let req: GenerationRequest = serde_json::from_str(&row.request_json)
                .map_err(|e| GenerationError::new("interrupted", e.to_string()))?;
            let provider_id = row
                .provider_id
                .ok_or_else(|| GenerationError::new("interrupted", "the task has no provider"))?;
            let provider_row = media_provider_service::get_by_id(&conn, provider_id)
                .await
                .map_err(|e| GenerationError::new("interrupted", e.to_string()))?
                .ok_or_else(|| {
                    GenerationError::new("interrupted", "the task's provider was removed")
                })?;
            let kind = match row.kind.as_str() {
                "image" => MediaKind::Image,
                "tts" => MediaKind::Tts,
                "video" => MediaKind::Video,
                _ => return Err(GenerationError::new("interrupted", "unsupported task kind")),
            };
            let adapter = provider::build_provider(
                kind,
                &provider_row.api_url,
                &provider_row.api_key,
                row.model.as_deref().unwrap_or(&provider_row.model),
                &provider_row.extra_json,
            )?;
            Ok((req, adapter))
        }
        .await;
        match recovery {
            Ok((req, adapter)) => {
                let conn = conn.clone();
                let emitter = emitter.clone();
                tokio::spawn(async move {
                    run_task(conn, emitter, row.id, adapter, req).await;
                });
            }
            Err(error) => fail_task(&conn, &emitter, row.id, &error).await,
        }
    }
}

pub(super) async fn set_row_status(
    conn: &DatabaseConnection,
    task_id: i32,
    status: &str,
    result: Value,
) -> Option<()> {
    use sea_orm::sea_query::Expr;

    let changed = comic_generation_task::Entity::update_many()
        .col_expr(comic_generation_task::Column::Status, Expr::value(status))
        .col_expr(
            comic_generation_task::Column::ResultJson,
            Expr::value(result.to_string()),
        )
        .col_expr(
            comic_generation_task::Column::UpdatedAt,
            Expr::value(Utc::now()),
        )
        .filter(comic_generation_task::Column::Id.eq(task_id))
        .filter(comic_generation_task::Column::Status.is_in(["queued", "running"]))
        .exec(conn)
        .await
        .ok()?;
    (changed.rows_affected == 1).then_some(())
}

/// Bind one candidate as the shot's image (`selected` + state → selected).
pub async fn bind_candidate(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &std::path::Path,
    episode: &str,
    shot: &str,
    candidate_rel: &str,
) -> Result<Storyboard, GenerationError> {
    let store = StoryStore::open(folder_path)
        .map_err(|e| GenerationError::new("bad_request", e.message()))?;
    // The bound path must belong to this shot's tree (a stray path from
    // another shot would quietly corrupt the pipeline).
    let allowed_prefix = format!("episodes/{episode}/shots/{shot}/");
    if !candidate_rel.starts_with(&allowed_prefix) {
        return Err(GenerationError::new(
            "bad_request",
            format!(
                "candidate {candidate_rel} does not belong to shot {shot} \
                 (expected under {allowed_prefix})"
            ),
        ));
    }
    if !store
        .resolve(candidate_rel)
        .map(|p| p.exists())
        .unwrap_or(false)
    {
        return Err(GenerationError::new(
            "bad_request",
            format!("candidate file not found: {candidate_rel}"),
        ));
    }
    let mut board = store
        .read_storyboard(episode)
        .map_err(|e| GenerationError::new("bad_request", e.message()))?;
    let Some(target) = board.shots.iter_mut().find(|s| s.id == shot) else {
        return Err(GenerationError::new(
            "bad_request",
            format!("episode {episode} has no shot {shot}"),
        ));
    };
    target.selected = Some(candidate_rel.to_string());
    target.state = ShotState::Selected;
    store
        .write_storyboard(episode, &board)
        .map_err(|e| GenerationError::new("internal", e.message()))?;
    // Selection does not delete history. Storage cleanup is a separate,
    // explicit operation so an old task never points at a removed candidate.
    emit_event(
        emitter,
        COMIC_CHANGED_EVENT,
        &ComicChange::Upsert {
            folder_id,
            resource: "episode".into(),
            id: episode.into(),
        },
    );
    Ok(board)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::comic::model::{EpisodeMeta, StoryShot};
    use crate::db::test_helpers::{fresh_in_memory_db, seed_folder};

    const TARGET: &str = "episodes/ep001/shots/shot-0001";

    fn png(pixel: u8) -> Vec<u8> {
        let mut output = std::io::Cursor::new(Vec::new());
        image::DynamicImage::ImageRgb8(image::RgbImage::from_pixel(
            2,
            2,
            image::Rgb([pixel, 0, 0]),
        ))
        .write_to(&mut output, image::ImageFormat::Png)
        .unwrap();
        output.into_inner()
    }

    fn project(path: &std::path::Path) -> StoryStore {
        let store = StoryStore::init(path, "Regression").unwrap();
        store
            .upsert_episode(&EpisodeMeta {
                id: "ep001".into(),
                title: "Episode".into(),
                ..Default::default()
            })
            .unwrap();
        store
            .replace_storyboard(
                "ep001",
                vec![StoryShot {
                    id: "shot-0001".into(),
                    description: "A character in a room".into(),
                    state: ShotState::Prompted,
                    ..Default::default()
                }],
            )
            .unwrap();
        store
    }

    async fn seed_task(conn: &DatabaseConnection, folder_id: i32, status: &str) -> i32 {
        let now = Utc::now();
        comic_generation_task::ActiveModel {
            id: NotSet,
            folder_id: Set(folder_id),
            kind: Set("image".into()),
            status: Set(status.into()),
            provider_id: Set(None),
            provider_name: Set(Some("Test provider".into())),
            model: Set(Some("test-image".into())),
            request_json: Set("{}".into()),
            result_json: Set("{}".into()),
            artifact_path: Set(None),
            target_kind: Set(Some("shot".into())),
            target_id: Set(Some(TARGET.into())),
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

    async fn seed_speech_task(conn: &DatabaseConnection, folder_id: i32) -> i32 {
        let id = seed_task(conn, folder_id, "running").await;
        comic_generation_task::ActiveModel {
            id: Set(id),
            kind: Set("tts".into()),
            ..Default::default()
        }
        .update(conn)
        .await
        .unwrap();
        id
    }

    #[tokio::test]
    async fn episode_task_filter_runs_before_the_limit_and_is_not_a_like_pattern() {
        let db = fresh_in_memory_db().await;
        let wanted = seed_task(&db.conn, 7, "succeeded").await;
        let other = seed_task(&db.conn, 7, "succeeded").await;
        comic_generation_task::ActiveModel {
            id: Set(other),
            target_id: Set(Some("episodes/ep002/shots/shot-0001".into())),
            ..Default::default()
        }
        .update(&db.conn)
        .await
        .unwrap();
        let adjacent = seed_task(&db.conn, 7, "succeeded").await;
        comic_generation_task::ActiveModel {
            id: Set(adjacent),
            target_id: Set(Some("episodes/ep0010/shots/shot-0001".into())),
            ..Default::default()
        }
        .update(&db.conn)
        .await
        .unwrap();
        let rows = list_tasks(&db.conn, 7, Some("ep001"), 1).await;
        assert_eq!(
            rows.iter().map(|row| row.id).collect::<Vec<_>>(),
            vec![wanted]
        );
        assert!(list_tasks(&db.conn, 7, Some("ep/not-a-slug"), 10)
            .await
            .is_empty());
    }

    #[tokio::test]
    async fn speech_artifacts_are_not_image_candidates_and_bad_audio_never_succeeds() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let folder_id = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let store = project(dir.path());
        let id = seed_speech_task(&db.conn, folder_id).await;
        let req: GenerationRequest = serde_json::from_value(json!({ "prompt":"Approved dialogue", "speech":{"voice":"nova","responseFormat":"wav"} })).unwrap();
        finish_task(
            &db.conn,
            &EventEmitter::Noop,
            id,
            vec![super::super::audio::wav(0.5)],
            &req,
        )
        .await;
        let view = check_task(&db.conn, id, 0).await.unwrap();
        assert_eq!(view.status, "succeeded");
        assert!(view.candidates.is_empty());
        assert_eq!(
            view.artifact_path.as_deref(),
            Some(format!("{TARGET}/audio/task-{id}.wav").as_str())
        );
        assert_eq!(
            store.read_storyboard("ep001").unwrap().shots[0].state,
            ShotState::Prompted
        );
        let record = store
            .read_shot_meta("ep001", "shot-0001")
            .unwrap()
            .generations
            .remove(0);
        let record = serde_json::to_value(record).unwrap();
        assert_eq!(record["parameters"]["speech"]["voice"], "nova");
        let invalid = seed_speech_task(&db.conn, folder_id).await;
        finish_task(
            &db.conn,
            &EventEmitter::Noop,
            invalid,
            vec![b"ID3not mp3".to_vec()],
            &req,
        )
        .await;
        let view = check_task(&db.conn, invalid, 0).await.unwrap();
        assert_eq!(view.status, "failed");
        assert_eq!(view.error_kind.as_deref(), Some("bad_response"));
        assert!(view.artifact_path.is_none() && view.candidates.is_empty());
    }

    #[tokio::test]
    async fn missing_project_cannot_produce_success_without_artifacts() {
        let db = fresh_in_memory_db().await;
        let id = seed_task(&db.conn, 999, "running").await;
        finish_task(
            &db.conn,
            &EventEmitter::Noop,
            id,
            vec![png(1)],
            &GenerationRequest::default(),
        )
        .await;
        let task = check_task(&db.conn, id, 0).await.unwrap();
        assert_eq!(task.status, "failed");
        assert!(task.candidates.is_empty());
        assert!(task.error_message.is_some());
    }

    #[tokio::test]
    async fn late_provider_failure_cannot_overwrite_a_user_cancellation() {
        let db = fresh_in_memory_db().await;
        let id = seed_task(&db.conn, 1, "cancelled").await;
        fail_task(
            &db.conn,
            &EventEmitter::Noop,
            id,
            &GenerationError::new("network", "late timeout"),
        )
        .await;
        assert_eq!(
            check_task(&db.conn, id, 0).await.unwrap().status,
            "cancelled"
        );
    }

    #[tokio::test]
    async fn regeneration_keeps_the_previously_bound_candidate_bytes() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let folder_id = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let store = project(dir.path());
        let first = seed_task(&db.conn, folder_id, "running").await;
        finish_task(
            &db.conn,
            &EventEmitter::Noop,
            first,
            vec![png(20)],
            &GenerationRequest::default(),
        )
        .await;
        let first_path = check_task(&db.conn, first, 0).await.unwrap().candidates[0].clone();
        bind_candidate(
            &EventEmitter::Noop,
            folder_id,
            dir.path(),
            "ep001",
            "shot-0001",
            &first_path,
        )
        .await
        .unwrap();
        let second = seed_task(&db.conn, folder_id, "running").await;
        finish_task(
            &db.conn,
            &EventEmitter::Noop,
            second,
            vec![png(80)],
            &GenerationRequest::default(),
        )
        .await;
        let second_path = check_task(&db.conn, second, 0).await.unwrap().candidates[0].clone();
        assert_ne!(first_path, second_path);
        assert_eq!(
            std::fs::read(store.resolve(&first_path).unwrap()).unwrap(),
            png(20)
        );
        assert_eq!(
            store.read_storyboard("ep001").unwrap().shots[0]
                .selected
                .as_deref(),
            Some(first_path.as_str())
        );
    }

    #[tokio::test]
    async fn cancelled_task_cannot_be_resurrected_by_late_progress() {
        let db = fresh_in_memory_db().await;
        let id = seed_task(&db.conn, 1, "cancelled").await;
        assert!(
            set_row_status(&db.conn, id, "running", json!({"progress": 90}))
                .await
                .is_none()
        );
        assert_eq!(
            check_task(&db.conn, id, 0).await.unwrap().status,
            "cancelled"
        );
    }

    #[tokio::test]
    async fn selecting_a_candidate_does_not_delete_unselected_history() {
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let mut config = store.read_series().unwrap();
        config.candidate_retention.keep_count = 1;
        store.write_series(&config).unwrap();
        for i in 1..=4 {
            store
                .write_artifact(&format!("{TARGET}/candidates/task-{i}-01.png"), &png(i))
                .unwrap();
        }
        bind_candidate(
            &EventEmitter::Noop,
            1,
            dir.path(),
            "ep001",
            "shot-0001",
            &format!("{TARGET}/candidates/task-4-01.png"),
        )
        .await
        .unwrap();
        assert!(store
            .resolve(&format!("{TARGET}/candidates/task-1-01.png"))
            .unwrap()
            .is_file());
    }

    #[tokio::test]
    async fn invalid_image_response_never_becomes_a_successful_candidate() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let folder_id = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let store = project(dir.path());
        let id = seed_task(&db.conn, folder_id, "running").await;
        finish_task(
            &db.conn,
            &EventEmitter::Noop,
            id,
            vec![b"not an image".to_vec()],
            &GenerationRequest::default(),
        )
        .await;
        let view = check_task(&db.conn, id, 0).await.unwrap();
        assert_eq!(view.status, "failed");
        assert_eq!(view.error_kind.as_deref(), Some("bad_response"));
        assert!(view.candidates.is_empty());
        assert!(!store
            .resolve(&format!("{TARGET}/candidates"))
            .unwrap()
            .exists());
    }

    #[tokio::test]
    async fn malformed_candidate_batch_is_rejected_before_writing_any_artifact() {
        for invalid in [
            png(4)[..20].to_vec(),
            b"\xFF\xD8\xFFfake jpeg".to_vec(),
            b"RIFF\x00\x00\x00\x00WEBPbroken".to_vec(),
            b"GIF89a".to_vec(),
        ] {
            let db = fresh_in_memory_db().await;
            let dir = tempfile::tempdir().unwrap();
            let folder_id = seed_folder(&db, &dir.path().to_string_lossy()).await;
            let store = project(dir.path());
            let id = seed_task(&db.conn, folder_id, "running").await;
            finish_task(
                &db.conn,
                &EventEmitter::Noop,
                id,
                vec![png(3), invalid],
                &GenerationRequest::default(),
            )
            .await;
            let view = check_task(&db.conn, id, 0).await.unwrap();
            assert_eq!(view.status, "failed");
            assert_eq!(view.error_kind.as_deref(), Some("bad_response"));
            assert!(view.candidates.is_empty());
            assert!(view.artifact_path.is_none());
            assert!(!store
                .resolve(&format!("{TARGET}/candidates"))
                .unwrap()
                .exists());
            assert!(store
                .read_shot_meta("ep001", "shot-0001")
                .unwrap()
                .generations
                .is_empty());
            assert_eq!(
                store.read_storyboard("ep001").unwrap().shots[0].state,
                ShotState::Prompted
            );
        }
    }

    #[tokio::test]
    async fn generated_images_accept_supported_formats_and_reject_oversized_dimensions() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let folder_id = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let store = project(dir.path());
        for (format, extension) in [
            (image::ImageFormat::Png, "png"),
            (image::ImageFormat::Jpeg, "jpg"),
            (image::ImageFormat::WebP, "webp"),
        ] {
            let mut output = std::io::Cursor::new(Vec::new());
            image::DynamicImage::ImageRgb8(image::RgbImage::from_pixel(
                2,
                2,
                image::Rgb([10, 20, 30]),
            ))
            .write_to(&mut output, format)
            .unwrap();
            let id = seed_task(&db.conn, folder_id, "running").await;
            finish_task(
                &db.conn,
                &EventEmitter::Noop,
                id,
                vec![output.into_inner()],
                &GenerationRequest::default(),
            )
            .await;
            let view = check_task(&db.conn, id, 0).await.unwrap();
            assert_eq!(view.status, "succeeded");
            assert!(view.candidates[0].ends_with(extension));
            assert!(store.resolve(&view.candidates[0]).unwrap().is_file());
        }
        let mut output = std::io::Cursor::new(Vec::new());
        image::DynamicImage::new_rgb8(16_385, 1)
            .write_to(&mut output, image::ImageFormat::Png)
            .unwrap();
        let id = seed_task(&db.conn, folder_id, "running").await;
        finish_task(
            &db.conn,
            &EventEmitter::Noop,
            id,
            vec![output.into_inner()],
            &GenerationRequest::default(),
        )
        .await;
        let view = check_task(&db.conn, id, 0).await.unwrap();
        assert_eq!(view.status, "failed");
        assert_eq!(view.error_kind.as_deref(), Some("bad_response"));
    }

    #[tokio::test]
    async fn interrupted_task_without_remote_handle_is_not_left_running() {
        let db = fresh_in_memory_db().await;
        let id = seed_task(&db.conn, 1, "running").await;
        recover_pending(db.conn.clone(), EventEmitter::Noop).await;
        let task = check_task(&db.conn, id, 0).await.unwrap();
        assert_eq!(task.status, "failed");
        assert_eq!(task.error_kind.as_deref(), Some("interrupted"));
    }
}
