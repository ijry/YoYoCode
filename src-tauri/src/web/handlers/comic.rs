//! HTTP surface for the comic (漫剧) workbench — mirrors the Tauri commands
//! one-to-one, sharing the same `commands::comic` `_core` functions so the
//! web client mutates `story/` through the same store path (jail, write
//! mutex, `comic://changed` broadcast) as the desktop app and the MCP tools.

use std::sync::Arc;

use axum::{
    extract::{Extension, Path},
    http::{header, HeaderMap},
    response::Response,
    Json,
};
use serde::Deserialize;
use serde_json::Value;

use crate::app_error::AppCommandError;
use crate::app_state::AppState;
use crate::comic::model::Chapter;
use crate::comic::model::{CharacterCard, EpisodeMeta, PropCard, SceneCard, StoryShot, Storyboard};
use crate::comic::source::{ReadWindow, WindowRead};
use crate::commands::comic as comic_commands;
use crate::commands::comic::{
    AssetList, ComicOverview, MediaProviderCreate, MediaProviderInfo, MediaProviderUpdate,
    SeriesConfig, SeriesPatch, SourceIngest, SourceReport,
};

pub async fn comic_get_overview(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<JsonFolderId>,
) -> Result<Json<ComicOverview>, AppCommandError> {
    Ok(Json(
        comic_commands::comic_overview_core(&state.db.conn, params.folder_id).await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonFolderId {
    pub folder_id: i32,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ComicInitParams {
    pub folder_id: i32,
    pub title: String,
}

pub async fn comic_init_project(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ComicInitParams>,
) -> Result<Json<ComicOverview>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    comic_commands::comic_init_project_core(&state.emitter, params.folder_id, &path, &params.title)
        .await?;
    Ok(Json(
        comic_commands::comic_overview_core(&state.db.conn, params.folder_id).await?,
    ))
}

pub async fn comic_ingest_source(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<IngestParams>,
) -> Result<Json<SourceReport>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_ingest_source_core(
            &state.emitter,
            params.folder_id,
            &path,
            params.params,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct IngestParams {
    pub folder_id: i32,
    pub params: SourceIngest,
}

pub async fn comic_read_source(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ReadSourceParams>,
) -> Result<Json<WindowRead>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_read_source_core(&path, params.window).await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReadSourceParams {
    pub folder_id: i32,
    pub window: ReadWindow,
}

pub async fn comic_list_chapters(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ChaptersParams>,
) -> Result<Json<Vec<Chapter>>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_outline_core(&path, params.limit.unwrap_or(500)).await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ChaptersParams {
    pub folder_id: i32,
    pub limit: Option<usize>,
}

pub async fn comic_list_assets(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ListAssetsParams>,
) -> Result<Json<AssetList>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_list_assets_core(
            &path,
            &params.kind,
            params.offset.unwrap_or(0),
            params.limit.unwrap_or(200),
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ListAssetsParams {
    pub folder_id: i32,
    pub kind: String,
    pub offset: Option<usize>,
    pub limit: Option<usize>,
}

pub async fn comic_get_asset(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<GetAssetParams>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_get_asset_core(&path, &params.kind, &params.id).await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct GetAssetParams {
    pub folder_id: i32,
    pub kind: String,
    pub id: String,
}

pub async fn comic_upsert_series(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<SeriesParams>,
) -> Result<Json<SeriesConfig>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_upsert_series_core(
            &state.emitter,
            params.folder_id,
            &path,
            params.patch,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SeriesParams {
    pub folder_id: i32,
    pub patch: SeriesPatch,
}

pub async fn comic_set_provider(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<SetProviderParams>,
) -> Result<Json<SeriesConfig>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_set_project_provider_core(
            &state.db.conn,
            &state.emitter,
            params.folder_id,
            &path,
            &params.kind,
            params.provider_id,
            params.provider_name,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SetProviderParams {
    pub folder_id: i32,
    pub kind: String,
    pub provider_id: Option<i32>,
    pub provider_name: Option<String>,
}

pub async fn comic_upsert_episode(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<EpisodeParams>,
) -> Result<Json<EpisodeMeta>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_upsert_episode_checked_core(
            &state.emitter,
            params.folder_id,
            &path,
            params.meta,
            params.create_only.unwrap_or(false),
            params.expected_meta.as_ref(),
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EpisodeParams {
    pub folder_id: i32,
    pub meta: EpisodeMeta,
    pub create_only: Option<bool>,
    pub expected_meta: Option<EpisodeMeta>,
}

pub async fn comic_write_script(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ScriptParams>,
) -> Result<Json<()>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    comic_commands::comic_write_script_checked_core(
        &state.emitter,
        params.folder_id,
        &path,
        &params.episode,
        &params.script,
        params.create_only.unwrap_or(false),
    )
    .await?;
    Ok(Json(()))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScriptParams {
    pub folder_id: i32,
    pub episode: String,
    pub script: String,
    pub create_only: Option<bool>,
}

pub async fn comic_replace_storyboard(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<StoryboardParams>,
) -> Result<Json<Storyboard>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_replace_storyboard_checked_core(
            &state.emitter,
            params.folder_id,
            &path,
            &params.episode,
            params.shots,
            params.expected_storyboard.as_ref(),
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct StoryboardParams {
    pub folder_id: i32,
    pub episode: String,
    pub shots: Vec<StoryShot>,
    pub expected_storyboard: Option<Storyboard>,
}

pub async fn comic_upsert_shot(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ShotParams>,
) -> Result<Json<()>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    comic_commands::comic_upsert_shot_core(
        &state.emitter,
        params.folder_id,
        &path,
        &params.episode,
        params.shot,
    )
    .await?;
    Ok(Json(()))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ShotParams {
    pub folder_id: i32,
    pub episode: String,
    pub shot: StoryShot,
}

pub async fn comic_delete_shot(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ShotRefParams>,
) -> Result<Json<()>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    comic_commands::comic_delete_shot_core(
        &state.emitter,
        params.folder_id,
        &path,
        &params.episode,
        &params.shot,
    )
    .await?;
    Ok(Json(()))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ShotRefParams {
    pub folder_id: i32,
    pub episode: String,
    pub shot: String,
}

pub async fn comic_reorder_shots(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ReorderParams>,
) -> Result<Json<Storyboard>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_reorder_shots_core(
            &state.emitter,
            params.folder_id,
            &path,
            &params.episode,
            params.order,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReorderParams {
    pub folder_id: i32,
    pub episode: String,
    pub order: Vec<String>,
}

pub async fn comic_upsert_character(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<CardParams<CharacterCard>>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_upsert_character_core(
            &state.emitter,
            params.folder_id,
            &path,
            params.card,
        )
        .await?,
    ))
}

pub async fn comic_upsert_scene(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<CardParams<SceneCard>>,
) -> Result<Json<()>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    comic_commands::comic_upsert_scene_core(&state.emitter, params.folder_id, &path, params.card)
        .await?;
    Ok(Json(()))
}

pub async fn comic_upsert_prop(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<CardParams<PropCard>>,
) -> Result<Json<()>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    comic_commands::comic_upsert_prop_core(&state.emitter, params.folder_id, &path, params.card)
        .await?;
    Ok(Json(()))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", bound = "T: Deserialize<'de>")]
pub struct CardParams<T> {
    pub folder_id: i32,
    pub card: T,
}

pub async fn comic_save_asset_image(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<SaveImageParams>,
) -> Result<Json<String>, AppCommandError> {
    use base64::Engine as _;
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(params.image_base64.trim())
        .map_err(|e| AppCommandError::invalid_input(format!("imageBase64: {e}")))?;
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_save_asset_image_core(
            &state.emitter,
            params.folder_id,
            &path,
            &params.kind,
            &params.slug,
            &params.slot,
            bytes,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SaveImageParams {
    pub folder_id: i32,
    pub kind: String,
    pub slug: String,
    pub slot: String,
    pub image_base64: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TasksParams {
    pub folder_id: i32,
    pub episode: Option<String>,
    pub limit: Option<u64>,
}

pub async fn comic_list_tasks(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<TasksParams>,
) -> Result<Json<Vec<crate::comic::task::TaskView>>, AppCommandError> {
    Ok(Json(
        comic_commands::comic_list_tasks_core(
            &state.db.conn,
            params.folder_id,
            params.episode.as_deref(),
            params.limit.unwrap_or(50),
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SpeechParams {
    pub folder_id: i32,
    pub params: crate::comic::speech::SpeechRequest,
}

pub async fn comic_request_speech(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<SpeechParams>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_request_speech_core(
            &state.db.conn,
            &state.emitter,
            params.folder_id,
            &path,
            params.params,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ShotAudioParams {
    pub folder_id: i32,
    pub episode: String,
    pub shot: String,
    pub task_id: i32,
}

pub async fn comic_set_shot_audio(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ShotAudioParams>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_set_shot_audio_core(
            &state.db.conn,
            &state.emitter,
            params.folder_id,
            &path,
            comic_commands::ShotAudioParams {
                episode: params.episode,
                shot: params.shot,
                task_id: params.task_id,
            },
            &tokio_util::sync::CancellationToken::new(),
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct VideoParams {
    pub folder_id: i32,
    pub params: crate::comic::video::VideoRequest,
}

pub async fn comic_request_video(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<VideoParams>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_request_video_core(
            &state.db.conn,
            &state.emitter,
            params.folder_id,
            &path,
            params.params,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ShotVideoParams {
    pub folder_id: i32,
    #[serde(flatten)]
    pub binding: comic_commands::ShotVideoParams,
}

pub async fn comic_set_shot_video(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ShotVideoParams>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_set_shot_video_core(
            &state.db.conn,
            &state.emitter,
            params.folder_id,
            &path,
            params.binding,
            &tokio_util::sync::CancellationToken::new(),
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ClearShotVideoPayload {
    pub folder_id: i32,
    #[serde(flatten)]
    pub binding: comic_commands::ClearShotVideoParams,
}

pub async fn comic_clear_shot_video(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ClearShotVideoPayload>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_clear_shot_video_core(
            &state.db.conn,
            &state.emitter,
            params.folder_id,
            &path,
            params.binding,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ListProviderModelsPayload {
    pub kind: String,
    #[serde(flatten)]
    pub params: comic_commands::ListProviderModelsParams,
}

pub async fn comic_list_provider_models(
    Extension(state): Extension<Arc<AppState>>,
    Json(payload): Json<ListProviderModelsPayload>,
) -> Result<Json<Value>, AppCommandError> {
    let _ = state;
    Ok(Json(
        comic_commands::comic_list_provider_models_core(payload.params).await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct VideoPreviewParams {
    pub folder_id: i32,
    pub task_id: i32,
}

/// Authenticated grant issuance: the caller proves the bearer token, then
/// receives a short-lived capability scoped to one succeeded video artifact.
pub async fn comic_request_video_preview(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<VideoPreviewParams>,
) -> Result<Json<Value>, AppCommandError> {
    Ok(Json(
        comic_commands::comic_request_video_preview_core(
            &state.db.conn,
            params.folder_id,
            params.task_id,
        )
        .await?,
    ))
}

/// Public grant-URL byte serving. The `<video>` element cannot attach the
/// bearer token; the capability URL carries only the opaque grant, and every
/// request re-validates task ownership, the story jail, and the TTL.
pub async fn comic_video_preview(
    Extension(state): Extension<Arc<AppState>>,
    Path(token): Path<String>,
    headers: HeaderMap,
) -> Result<Response, AppCommandError> {
    let range = headers
        .get(header::RANGE)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned);
    let chunk = crate::comic::preview::serve(&state.db.conn, &token, range.as_deref())
        .await
        .map_err(|_| AppCommandError::not_found("video preview is not available"))?;
    let mut builder = Response::builder()
        .status(
            axum::http::StatusCode::from_u16(chunk.status)
                .unwrap_or(axum::http::StatusCode::NOT_FOUND),
        )
        .header(header::CONTENT_TYPE, chunk.content_type)
        .header(header::ACCEPT_RANGES, "bytes")
        .header(header::CACHE_CONTROL, "no-store")
        .header(header::X_CONTENT_TYPE_OPTIONS, "nosniff");
    if let Some(content_range) = &chunk.content_range {
        builder = builder.header(header::CONTENT_RANGE, content_range);
    }
    builder
        .body(axum::body::Body::from(chunk.bytes))
        .map_err(|error| {
            AppCommandError::new(crate::app_error::AppErrorCode::IoError, error.to_string())
        })
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ComposeParams {
    pub folder_id: i32,
    pub params: comic_commands::ComposeRequestParams,
}

pub async fn comic_compose_episode(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ComposeParams>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_compose_episode_core(
            &state.db.conn,
            &state.emitter,
            params.folder_id,
            &path,
            params.params,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CheckComposeParams {
    pub folder_id: i32,
    pub task_id: i32,
    pub wait_ms: Option<u64>,
}

pub async fn comic_check_compose(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<CheckComposeParams>,
) -> Result<Json<Value>, AppCommandError> {
    Ok(Json(
        comic_commands::comic_check_compose_core(
            &state.db.conn,
            params.folder_id,
            params.task_id,
            params.wait_ms.unwrap_or(0),
        )
        .await?,
    ))
}

pub async fn comic_bind_candidate(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<BindParams>,
) -> Result<Json<Value>, AppCommandError> {
    let path = comic_folder_path(&state, params.folder_id).await?;
    Ok(Json(
        comic_commands::comic_set_shot_image_core(
            &state.emitter,
            params.folder_id,
            &path,
            &params.episode,
            &params.shot,
            &params.candidate,
        )
        .await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct BindParams {
    pub folder_id: i32,
    pub episode: String,
    pub shot: String,
    pub candidate: String,
}

pub async fn list_media_providers(
    Extension(state): Extension<Arc<AppState>>,
) -> Result<Json<Vec<MediaProviderInfo>>, AppCommandError> {
    Ok(Json(
        comic_commands::media_provider_list_core(&state.db.conn).await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ImageParams {
    pub folder_id: i32,
    pub path: String,
}

pub async fn comic_read_image(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ImageParams>,
) -> Result<Json<comic_commands::ComicImageData>, AppCommandError> {
    Ok(Json(
        comic_commands::comic_read_image_core(&state.db.conn, params.folder_id, params.path)
            .await?,
    ))
}

#[derive(Deserialize)]
pub struct CreateMediaProviderParams {
    pub params: MediaProviderCreate,
}

pub async fn create_media_provider(
    Extension(state): Extension<Arc<AppState>>,
    Json(wrapper): Json<CreateMediaProviderParams>,
) -> Result<Json<MediaProviderInfo>, AppCommandError> {
    Ok(Json(
        comic_commands::media_provider_create_core(&state.db.conn, wrapper.params).await?,
    ))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProviderIdParams {
    pub id: i32,
    #[serde(default)]
    pub params: Option<MediaProviderUpdate>,
}

pub async fn update_media_provider(
    Extension(state): Extension<Arc<AppState>>,
    Json(wrapper): Json<ProviderIdParams>,
) -> Result<Json<MediaProviderInfo>, AppCommandError> {
    let params = wrapper.params.unwrap_or(MediaProviderUpdate {
        name: None,
        api_url: None,
        api_key: None,
        model: None,
        extra_json: None,
        is_default: None,
    });
    Ok(Json(
        comic_commands::media_provider_update_core(&state.db.conn, wrapper.id, params).await?,
    ))
}

pub async fn delete_media_provider(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ProviderIdParams>,
) -> Result<Json<()>, AppCommandError> {
    comic_commands::media_provider_delete_core(&state.db.conn, params.id).await?;
    Ok(Json(()))
}

pub async fn test_media_provider(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ProviderIdParams>,
) -> Result<Json<Value>, AppCommandError> {
    Ok(Json(
        comic_commands::media_provider_test_core(&state.db.conn, params.id).await?,
    ))
}

/// Resolve the folder row's path for a comic request (the store opens on it).
async fn comic_folder_path(
    state: &AppState,
    folder_id: i32,
) -> Result<std::path::PathBuf, AppCommandError> {
    crate::db::service::folder_service::get_folder_by_id(&state.db.conn, folder_id)
        .await
        .map_err(AppCommandError::from)?
        .map(|f| std::path::PathBuf::from(f.path))
        .ok_or_else(|| AppCommandError::not_found(format!("folder not found: {folder_id}")))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn write_preconditions_are_optional_and_empty_board_is_an_explicit_snapshot() {
        let legacy: EpisodeParams = serde_json::from_value(serde_json::json!({
            "folderId": 7, "meta": {"id": "ep001", "title": "Pilot"}
        }))
        .unwrap();
        assert_eq!(legacy.create_only, None);
        assert_eq!(legacy.expected_meta, None);
        let guarded: EpisodeParams = serde_json::from_value(serde_json::json!({
            "folderId": 7, "meta": {"id": "ep001", "title": "Pilot"},
            "createOnly": true, "expectedMeta": {"id": "ep001", "title": "Original"}
        }))
        .unwrap();
        assert_eq!(guarded.create_only, Some(true));
        assert_eq!(guarded.expected_meta.unwrap().title, "Original");
        for create_only in [None, Some(true)] {
            let params: ScriptParams = serde_json::from_value(serde_json::json!({
                "folderId": 7, "episode": "ep001", "script": "Script", "createOnly": create_only
            }))
            .unwrap();
            assert_eq!(params.create_only, create_only);
        }
        let legacy: StoryboardParams = serde_json::from_value(serde_json::json!({
            "folderId": 7, "episode": "ep001", "shots": []
        }))
        .unwrap();
        assert_eq!(legacy.expected_storyboard, None);
        let guarded: StoryboardParams = serde_json::from_value(serde_json::json!({
            "folderId": 7, "episode": "ep001", "shots": [], "expectedStoryboard": {"shots": []}
        }))
        .unwrap();
        assert_eq!(guarded.expected_storyboard, Some(Storyboard::default()));
    }
}
