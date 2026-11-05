//! Comic (漫剧) commands: the host side of the story tree.
//!
//! Three consumers share one set of `_core` functions here — Tauri commands,
//! web handlers, and the MCP comic tools ([`McpComicTools`]) — so a session's
//! tool write, the workbench's direct edit, and a browser fetch all mutate
//! `story/` through the same [`StoryStore`] path: same jail, same per-folder
//! write mutex, same `comic://changed` emission. Nothing here decides media
//! generation yet — that arrives with the provider layer — the M1 surface is
//! resources, source ingestion, and provider pointers.

use std::path::{Path, PathBuf};

use async_trait::async_trait;
use base64::{engine::general_purpose::STANDARD, Engine as _};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::acp::comic_tools::{ComicAccess, ComicOutcome};
use crate::acp::question::{QuestionOption, QuestionSpec, SessionQuestionAccess};
use crate::app_error::AppCommandError;
pub use crate::comic::model::SeriesConfig;
use crate::comic::model::{
    CharacterCard, EpisodeMeta, ProjectOverview, PropCard, SceneCard, ShotMeta, StoryShot,
    Storyboard,
};
pub use crate::comic::source::SourceReport;
use crate::comic::source::{self, ReadWindow, WindowRead};
use crate::comic::store::{StoreError, StoryStore};
use crate::db::service::{folder_service, media_provider_service};
#[cfg(feature = "tauri-runtime")]
use crate::db::AppDatabase;
use crate::web::event_bridge::{emit_event, ComicChange, EventEmitter, COMIC_CHANGED_EVENT};

// ---------------------------------------------------------------------------
// Shared plumbing
// ---------------------------------------------------------------------------

/// A media-provider pool entry as the LLM and the workbench see it — never
/// the api_key.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaProviderInfo {
    pub id: i32,
    pub kind: String,
    pub name: String,
    pub api_url: String,
    pub model: String,
    pub extra_json: String,
    pub is_default: bool,
    pub capabilities: crate::comic::provider::capabilities::MediaCapabilities,
}

impl From<crate::db::entities::media_provider::Model> for MediaProviderInfo {
    fn from(row: crate::db::entities::media_provider::Model) -> Self {
        let kind = match row.kind.as_str() {
            "tts" => crate::comic::provider::MediaKind::Tts,
            "video" => crate::comic::provider::MediaKind::Video,
            _ => crate::comic::provider::MediaKind::Image,
        };
        let options = serde_json::from_str(&row.extra_json).unwrap_or(Value::Null);
        let capabilities = crate::comic::provider::capabilities::describe(kind, &options);
        Self {
            id: row.id,
            kind: row.kind,
            name: row.name,
            api_url: row.api_url,
            model: row.model,
            extra_json: row.extra_json,
            is_default: row.is_default,
            capabilities,
        }
    }
}

/// `comic_get_project` / workbench overview: file-state overview plus the
/// provider pool the pointers may target.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ComicOverview {
    pub folder_id: i32,
    #[serde(flatten)]
    pub overview: ProjectOverview,
    pub provider_pool: Vec<MediaProviderInfo>,
}

async fn folder_path(
    conn: &sea_orm::DatabaseConnection,
    folder_id: i32,
) -> Result<PathBuf, AppCommandError> {
    folder_service::get_folder_by_id(conn, folder_id)
        .await
        .map_err(AppCommandError::from)?
        .map(|f| PathBuf::from(f.path))
        .ok_or_else(|| AppCommandError::not_found(format!("folder not found: {folder_id}")))
}

fn store_for(folder_path: &Path) -> Result<StoryStore, StoreError> {
    StoryStore::open(folder_path)
}

fn emit_upsert(emitter: &EventEmitter, folder_id: i32, resource: &str, id: &str) {
    emit_event(
        emitter,
        COMIC_CHANGED_EVENT,
        &ComicChange::Upsert {
            folder_id,
            resource: resource.to_string(),
            id: id.to_string(),
        },
    );
}

/// The provider pool the workbench and `comic_get_project` show alongside the
/// project's pointers.
async fn provider_pool(
    conn: &sea_orm::DatabaseConnection,
) -> Result<Vec<MediaProviderInfo>, AppCommandError> {
    Ok(media_provider_service::list_all(conn)
        .await
        .map_err(AppCommandError::from)?
        .into_iter()
        .map(MediaProviderInfo::from)
        .collect())
}

// ---------------------------------------------------------------------------
// Core functions (Tauri commands + web handlers + MCP tools)
// ---------------------------------------------------------------------------

pub async fn comic_overview_core(
    conn: &sea_orm::DatabaseConnection,
    folder_id: i32,
) -> Result<ComicOverview, AppCommandError> {
    let path = folder_path(conn, folder_id).await?;
    let store = store_for(&path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let overview = store
        .overview()
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    Ok(ComicOverview {
        folder_id,
        overview,
        provider_pool: provider_pool(conn).await?,
    })
}

/// One of the three registration paths, as the workbench wizard and the MCP
/// tool both send it.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SourceIngest {
    /// Full novel text, base64 (workbench upload / session attachment).
    #[serde(default, alias = "upload_base64")]
    pub upload_base64: Option<String>,
    /// Absolute local path to copy in.
    #[serde(default)]
    pub path: Option<String>,
    /// Mark as an original screenplay; clears any source.
    #[serde(default)]
    pub original: bool,
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default, alias = "max_heading_len")]
    pub max_heading_len: Option<usize>,
    #[serde(default, alias = "fallback_window_chars")]
    pub fallback_window_chars: Option<usize>,
}

pub async fn comic_ingest_source_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: SourceIngest,
) -> Result<SourceReport, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let input = if params.original {
        source::SourceInput::Original
    } else if let Some(bytes_b64) = params.upload_base64 {
        let bytes = STANDARD
            .decode(bytes_b64.trim())
            .map_err(|e| AppCommandError::invalid_input(format!("upload_base64: {e}")))?;
        source::SourceInput::Upload {
            title: params.title,
            bytes,
        }
    } else if let Some(path) = params.path {
        source::SourceInput::ExternalPath {
            path,
            title: params.title,
        }
    } else {
        return Err(AppCommandError::invalid_input(
            "one of uploadBase64 / path / original is required",
        ));
    };
    let options = source::SplitOptions {
        max_heading_len: params
            .max_heading_len
            .unwrap_or(source::SplitOptions::default().max_heading_len),
        fallback_window_chars: params
            .fallback_window_chars
            .unwrap_or(source::SplitOptions::default().fallback_window_chars),
    };
    // Ingest does file IO + regex splitting over a possibly multi-MB novel —
    // keep it off the async workers.
    let report = tokio::task::block_in_place(|| source::ingest(&store, input, &options))
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_event(
        emitter,
        COMIC_CHANGED_EVENT,
        &ComicChange::Upsert {
            folder_id,
            resource: "source".into(),
            id: String::new(),
        },
    );
    Ok(report)
}

pub async fn comic_read_source_core(
    folder_path: &Path,
    window: ReadWindow,
) -> Result<WindowRead, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    tokio::task::block_in_place(|| source::read_window(&store, &window))
        .map_err(|e| AppCommandError::invalid_input(e.message()))
}

pub async fn comic_outline_core(
    folder_path: &Path,
    limit: usize,
) -> Result<Vec<crate::comic::model::Chapter>, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    source::outline(&store, limit).map_err(|e| AppCommandError::invalid_input(e.message()))
}

/// `comic_list_assets(kind, offset, limit)` — one paginated view per kind.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AssetList {
    pub kind: String,
    pub total: usize,
    pub items: Vec<Value>,
}

fn page<T: Serialize>(items: Vec<T>, offset: usize, limit: usize) -> (usize, Vec<Value>) {
    let total = items.len();
    let page_items: Vec<Value> = items
        .into_iter()
        .skip(offset)
        .take(limit.max(1))
        .filter_map(|item| serde_json::to_value(&item).ok())
        .collect();
    (total, page_items)
}

pub async fn comic_list_assets_core(
    folder_path: &Path,
    kind: &str,
    offset: usize,
    limit: usize,
) -> Result<AssetList, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let computed = tokio::task::block_in_place(|| -> Result<(usize, Vec<Value>), StoreError> {
        match kind {
            "episode" => {
                let mut metas = Vec::new();
                for ep in store.list_episode_ids()? {
                    let meta = store.read_episode(&ep).unwrap_or(EpisodeMeta {
                        id: ep.clone(),
                        ..Default::default()
                    });
                    let (shots_total, shots_done) = store.episode_progress(&ep).unwrap_or((0, 0));
                    metas.push(serde_json::json!({
                        "id": meta.id,
                        "title": meta.title,
                        "status": meta.status,
                        "shotsTotal": shots_total,
                        "shotsDone": shots_done,
                    }));
                }
                Ok(page(metas, offset, limit))
            }
            "character" => Ok(page(store.list_characters()?, offset, limit)),
            "scene" => Ok(page(store.list_scenes()?, offset, limit)),
            "prop" => Ok(page(store.list_props()?, offset, limit)),
            other => Err(StoreError::Invalid(format!("unknown asset kind: {other}"))),
        }
    });
    let (total, items) = computed.map_err(|e| AppCommandError::invalid_input(e.message()))?;
    Ok(AssetList {
        kind: kind.to_string(),
        total,
        items,
    })
}

/// Read one resource document in full. An episode carries its storyboard and
/// script alongside the meta; assets return their card.
pub async fn comic_get_asset_core(
    folder_path: &Path,
    kind: &str,
    id: &str,
) -> Result<Value, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    tokio::task::block_in_place(|| match kind {
        "episode" => {
            let meta = store
                .read_episode(id)
                .map_err(|e| AppCommandError::invalid_input(e.message()))?;
            let storyboard = store
                .read_storyboard(id)
                .map_err(|e| AppCommandError::invalid_input(e.message()))
                .ok();
            let script = store.read_script(id).ok();
            let mut value = serde_json::to_value(&meta)
                .map_err(|e| AppCommandError::io_error(e.to_string()))?;
            if let (Some(board), Some(obj)) = (storyboard, value.as_object_mut()) {
                obj.insert(
                    "storyboard".into(),
                    serde_json::to_value(&board).unwrap_or_default(),
                );
            }
            if let (Some(script), Some(obj)) = (script, value.as_object_mut()) {
                obj.insert("script".into(), Value::String(script));
            }
            Ok(value)
        }
        "character" => {
            let card: CharacterCard = store
                .read_character(id)
                .map_err(|e| AppCommandError::invalid_input(e.message()))?;
            serde_json::to_value(&card).map_err(|e| AppCommandError::io_error(e.to_string()))
        }
        "scene" => {
            let card: SceneCard = store
                .read_scene(id)
                .map_err(|e| AppCommandError::invalid_input(e.message()))?;
            serde_json::to_value(&card).map_err(|e| AppCommandError::io_error(e.to_string()))
        }
        "prop" => {
            let card: PropCard = store
                .read_prop(id)
                .map_err(|e| AppCommandError::invalid_input(e.message()))?;
            serde_json::to_value(&card).map_err(|e| AppCommandError::io_error(e.to_string()))
        }
        other => Err(AppCommandError::invalid_input(format!(
            "unknown asset kind: {other}"
        ))),
    })
}

pub async fn comic_search_assets_core(
    folder_path: &Path,
    query: &str,
) -> Result<Vec<crate::comic::model::AssetSummary>, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .search_assets(query)
        .map_err(|e| AppCommandError::invalid_input(e.message()))
}

/// Patch the series config — fields the caller sent change, the rest stay.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SeriesPatch {
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default, alias = "adapted_from")]
    pub adapted_from: Option<String>,
    #[serde(default, alias = "style_bible")]
    pub style_bible: Option<String>,
    #[serde(default, alias = "bible_refs")]
    pub bible_refs: Option<Vec<String>>,
    #[serde(default, alias = "global_seed")]
    pub global_seed: Option<u64>,
    #[serde(default, alias = "default_candidate_count")]
    pub default_candidate_count: Option<u32>,
    #[serde(default, alias = "max_images_per_request")]
    pub max_images_per_request: Option<u32>,
    #[serde(default, alias = "max_concurrent_tasks")]
    pub max_concurrent_tasks: Option<u32>,
}

pub async fn comic_upsert_series_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    patch: SeriesPatch,
) -> Result<SeriesConfig, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let mut config = store
        .read_series()
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    if let Some(v) = patch.title {
        config.title = v;
    }
    if let Some(v) = patch.adapted_from {
        config.adapted_from = Some(v);
    }
    if let Some(v) = patch.style_bible {
        config.style_bible = Some(v);
    }
    if let Some(v) = patch.bible_refs {
        config.bible_refs = v;
    }
    if let Some(v) = patch.global_seed {
        config.global_seed = Some(v);
    }
    if let Some(v) = patch.default_candidate_count {
        config.default_candidate_count = v.clamp(1, 8);
    }
    if let Some(v) = patch.max_images_per_request {
        config.max_images_per_request = v.clamp(1, 64);
    }
    if let Some(v) = patch.max_concurrent_tasks {
        config.max_concurrent_tasks = v.clamp(1, 8);
    }
    store
        .write_series(&config)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "series", "");
    Ok(config)
}

pub async fn comic_upsert_episode_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    meta: EpisodeMeta,
) -> Result<EpisodeMeta, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .upsert_episode(&meta)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "episode", &meta.id);
    Ok(meta)
}

pub async fn comic_write_script_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    script: &str,
) -> Result<(), AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .write_script(episode, script)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "episode", episode);
    Ok(())
}

pub async fn comic_replace_storyboard_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    shots: Vec<StoryShot>,
) -> Result<Storyboard, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    // Validates every reference first; nothing lands on any bad shot.
    let board = store
        .replace_storyboard(episode, shots)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "episode", episode);
    Ok(board)
}

pub async fn comic_upsert_shot_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    shot: StoryShot,
) -> Result<(), AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .upsert_shot(episode, &shot)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "episode", episode);
    Ok(())
}

/// Remove one shot (and its artifacts) from an episode.
pub async fn comic_delete_shot_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    shot: &str,
) -> Result<(), AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .delete_shot(episode, shot)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "episode", episode);
    Ok(())
}

/// Persist the board's display order (a permutation of the shot ids).
pub async fn comic_reorder_shots_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    order: Vec<String>,
) -> Result<Storyboard, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let board = store
        .reorder_shots(episode, &order)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "episode", episode);
    Ok(board)
}

/// Save an uploaded reference image into a card slot; returns the
/// story-relative path written into the card JSON.
pub async fn comic_save_asset_image_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    kind: &str,
    slug: &str,
    slot: &str,
    bytes: Vec<u8>,
) -> Result<String, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let rel = tokio::task::block_in_place(|| {
        store
            .save_asset_image(kind, slug, slot, &bytes)
            .map_err(|e| AppCommandError::invalid_input(e.message()))
    })?;
    emit_upsert(emitter, folder_id, kind, slug);
    Ok(rel)
}

/// Write a shot's per-shot meta (prompt/seed/negative prompt) — used by the
/// prompt builder; exposed to the workbench for hand tuning.
pub async fn comic_write_shot_meta_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    shot: &str,
    meta: ShotMeta,
) -> Result<(), AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .write_shot_meta(episode, shot, &meta)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "episode", episode);
    Ok(())
}

pub async fn comic_upsert_character_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    card: CharacterCard,
) -> Result<Value, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .upsert_character(&card)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "character", &card.slug);
    // Shots referencing this character — the "look changed, offer a re-render"
    // list. Empty on first creation.
    let affected = shots_referencing_character(&store, &card.slug);
    Ok(serde_json::json!({
        "slug": card.slug,
        "affectedShots": affected,
    }))
}

/// All (episode, shot) pairs across the project referencing a character slug.
fn shots_referencing_character(store: &StoryStore, slug: &str) -> Vec<Value> {
    let mut hits = Vec::new();
    let Ok(eps) = store.list_episode_ids() else {
        return hits;
    };
    for ep in eps {
        let Ok(board) = store.read_storyboard(&ep) else {
            continue;
        };
        for shot in board.shots {
            if shot.refs.characters.iter().any(|c| c.slug == slug) {
                hits.push(serde_json::json!({
                    "episode": ep,
                    "shotId": shot.id,
                }));
            }
        }
    }
    hits
}

pub async fn comic_upsert_scene_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    card: SceneCard,
) -> Result<(), AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .upsert_scene(&card)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "scene", &card.slug);
    Ok(())
}

pub async fn comic_upsert_prop_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    card: PropCard,
) -> Result<(), AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    store
        .upsert_prop(&card)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "prop", &card.slug);
    Ok(())
}

/// Point a project's `kind` generation at a pool entry (or clear it with
/// `None`). Resolves id first, name fallback; refuses an unknown pointer.
pub async fn comic_set_project_provider_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    kind: &str,
    provider_id: Option<i32>,
    provider_name: Option<String>,
) -> Result<SeriesConfig, AppCommandError> {
    media_provider_service::validate_kind(kind)
        .map_err(|e| AppCommandError::invalid_input(e.to_string()))?;
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let mut config = store
        .read_series()
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;

    let next = match (provider_id, provider_name) {
        (None, None) => None,
        (id, name) => {
            let row = media_provider_service::resolve_ref(
                conn,
                kind,
                id.unwrap_or(0),
                name.as_deref().unwrap_or(""),
            )
            .await
            .map_err(AppCommandError::from)?
            .ok_or_else(|| {
                AppCommandError::invalid_input(format!(
                    "no media provider of kind {kind} matches the given id/name — \
                         check settings → 媒体服务"
                ))
            })?;
            Some(crate::comic::model::ProviderRef {
                id: row.id,
                name: row.name,
            })
        }
    };
    config
        .providers
        .set(kind, next)
        .map_err(AppCommandError::invalid_input)?;
    store
        .write_series(&config)
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    emit_upsert(emitter, folder_id, "series", "");
    Ok(config)
}

// ---------------------------------------------------------------------------
// Media generation (M3): prompt assembly → task submission → check/bind
// ---------------------------------------------------------------------------

pub async fn comic_build_shot_prompt_core(
    folder_path: &Path,
    episode: &str,
    shot: &str,
) -> Result<Value, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let prompt = tokio::task::block_in_place(|| {
        crate::comic::prompt::build_shot_prompt(&store, episode, shot)
            .map_err(|e| AppCommandError::invalid_input(e.message()))
    })?;
    serde_json::to_value(&prompt).map_err(|e| AppCommandError::io_error(e.to_string()))
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct GenerationRequestParams {
    pub episode: String,
    pub shot: String,
    #[serde(default)]
    pub count: Option<u32>,
    #[serde(default)]
    pub seed: Option<u64>,
    #[serde(default, alias = "provider_id")]
    pub provider_id: Option<i32>,
    #[serde(default, alias = "provider_name")]
    pub provider_name: Option<String>,
    /// Only the host sets this after a real user answer. A model-provided
    /// boolean cannot approve its own spending request.
    #[serde(skip)]
    pub confirmed: bool,
}

pub async fn comic_request_generation_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: GenerationRequestParams,
) -> Result<Value, AppCommandError> {
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let series = store
        .read_series()
        .map_err(|e| AppCommandError::invalid_input(e.message()))?;
    let count = params
        .count
        .unwrap_or(series.default_candidate_count)
        .max(1);
    if count > series.max_images_per_request.max(1) && !params.confirmed {
        // Cost guardrail: a batch beyond the project's own ceiling needs the
        // user's explicit nod — the LLM asks (ask_user_question) and re-sends
        // with confirmed=true.
        return Err(AppCommandError::invalid_input(format!(
            "comic_confirm_batch: {count} images exceeds this project's ceiling of {}. \
             Ask the user to confirm the batch size, then retry with confirmed=true.",
            series.max_images_per_request
        )));
    }
    let target_id = format!("episodes/{}/shots/{}", params.episode, params.shot);
    let (req, _assembled) = tokio::task::block_in_place(|| {
        crate::comic::prompt::request_for_shot(
            &store,
            &params.episode,
            &params.shot,
            count,
            params.seed,
            Value::Null,
        )
        .map_err(|e| AppCommandError::invalid_input(e.message()))
    })?;
    let task_id = crate::comic::task::submit_generation(
        conn,
        emitter,
        folder_id,
        folder_path,
        crate::comic::provider::MediaKind::Image,
        "shot",
        &target_id,
        req,
        params.provider_id,
        params.provider_name,
    )
    .await
    .map_err(|e| AppCommandError::invalid_input(e.to_string()))?;
    Ok(serde_json::json!({
        "taskId": task_id,
        "count": count,
        "target": target_id,
        "hint": "Poll with comic_check_generation (wait_ms); candidates land in the shot's                  candidates/ dir and the workbench live-updates.",
    }))
}

pub async fn comic_check_generation_core(
    conn: &sea_orm::DatabaseConnection,
    task_id: i32,
    wait_ms: u64,
) -> Result<Value, AppCommandError> {
    let view = crate::comic::task::check_task(conn, task_id, wait_ms)
        .await
        .ok_or_else(|| AppCommandError::not_found(format!("task not found: {task_id}")))?;
    serde_json::to_value(&view).map_err(|e| AppCommandError::io_error(e.to_string()))
}

pub async fn comic_cancel_generation_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    task_id: i32,
) -> Result<Value, AppCommandError> {
    let view = crate::comic::task::cancel_task(conn, emitter, task_id)
        .await
        .ok_or_else(|| AppCommandError::not_found(format!("task not found: {task_id}")))?;
    serde_json::to_value(&view).map_err(|e| AppCommandError::io_error(e.to_string()))
}

pub async fn comic_set_shot_image_core(
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    shot: &str,
    candidate: &str,
) -> Result<Value, AppCommandError> {
    let board = crate::comic::task::bind_candidate(
        emitter,
        folder_id,
        folder_path,
        episode,
        shot,
        candidate,
    )
    .await
    .map_err(|e| AppCommandError::invalid_input(e.to_string()))?;
    serde_json::to_value(&board).map_err(|e| AppCommandError::io_error(e.to_string()))
}

/// Render the pick-one card for a shot's candidates through the blocking ask
/// machinery, bind the choice, and report it. A decline returns without
/// binding — the LLM regenerates or adjusts.
pub struct CandidatePresentation<'a> {
    pub parent_connection_id: &'a str,
    pub folder_id: i32,
    pub folder_path: &'a Path,
    pub episode: &'a str,
    pub shot: &'a str,
    pub candidates: Vec<String>,
}

pub async fn comic_present_candidates_core(
    questions: &std::sync::Arc<dyn SessionQuestionAccess>,
    emitter: &EventEmitter,
    presentation: CandidatePresentation<'_>,
    cancel: tokio_util::sync::CancellationToken,
) -> Result<Value, AppCommandError> {
    let CandidatePresentation {
        parent_connection_id,
        folder_id,
        folder_path,
        episode,
        shot,
        candidates,
    } = presentation;
    if candidates.is_empty() {
        return Err(AppCommandError::invalid_input(
            "candidates is empty — nothing to present",
        ));
    }
    if candidates.len() > 8 {
        return Err(AppCommandError::invalid_input(
            "candidates exceeds 8 — present the best 8 and note the rest",
        ));
    }
    let store = store_for(folder_path).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    crate::comic::paths::validate_slug(episode).map_err(AppCommandError::invalid_input)?;
    crate::comic::paths::validate_slug(shot).map_err(AppCommandError::invalid_input)?;
    let prefix = format!("episodes/{episode}/shots/{shot}/candidates/");
    let mut unique = std::collections::HashSet::new();
    for candidate in &candidates {
        if !candidate.starts_with(&prefix) || !unique.insert(candidate) {
            return Err(AppCommandError::invalid_input(
                "candidates must be distinct images belonging to this shot",
            ));
        }
        let path = store
            .resolve(candidate)
            .map_err(|e| AppCommandError::invalid_input(e.message()))?;
        if !path.is_file() {
            return Err(AppCommandError::invalid_input("candidate file is missing"));
        }
    }
    if cancel.is_cancelled() {
        return Ok(serde_json::json!({"cancelled": true, "bound": false}));
    }
    let spec = QuestionSpec {
        id: uuid::Uuid::new_v4().to_string(),
        question: format!("选择镜头 {episode}/{shot} 的定稿图"),
        header: "选图".to_string(),
        multi_select: false,
        options: candidates
            .iter()
            .map(|rel| QuestionOption {
                label: rel.rsplit('/').next().unwrap_or(rel).to_string(),
                description: rel.clone(),
                image: Some(crate::acp::question::QuestionImageRef {
                    folder_id,
                    path: rel.clone(),
                }),
            })
            .collect(),
        is_secret: false,
    };
    let registered = questions
        .register_question(parent_connection_id, vec![spec])
        .await
        .ok_or_else(|| {
            AppCommandError::invalid_input(
                "the session's UI is gone — the user closed this conversation",
            )
        })?;
    let outcome = tokio::select! {
        biased;
        _ = cancel.cancelled() => {
            questions.cancel_question(parent_connection_id, &registered.question_id).await;
            return Ok(serde_json::json!({"cancelled": true, "bound": false}));
        }
        answer = registered.answer_rx => {
            answer.map_err(|_| AppCommandError::invalid_input("the question was cancelled"))?
        }
    };
    if cancel.is_cancelled() {
        return Ok(serde_json::json!({"cancelled": true, "bound": false}));
    }
    if outcome.declined {
        return Ok(serde_json::json!({
            "declined": true,
            "hint": "The user dismissed the card without choosing. Adjust the prompt or \
                     regenerate, then present again.",
        }));
    }
    let chosen_label = outcome
        .answers
        .first()
        .and_then(|a| a.selected.first())
        .cloned()
        .ok_or_else(|| AppCommandError::invalid_input("the answer carried no selection"))?;
    // The label is the candidate's file name — or a free-typed "Other" the
    // card always offers. A free-text answer that matches no candidate is a
    // refusal of the offered set, not a pick: report it unbound instead of
    // silently binding the first candidate.
    let candidate = candidates
        .iter()
        .find(|rel| rel.rsplit('/').next() == Some(chosen_label.as_str()))
        .ok_or_else(|| {
            AppCommandError::invalid_input(format!(
                "the user's answer \"{chosen_label}\" names none of the candidates —                  treat it as feedback (e.g. regenerate with a different prompt)"
            ))
        })?
        .clone();
    comic_set_shot_image_core(emitter, folder_id, folder_path, episode, shot, &candidate).await?;
    Ok(serde_json::json!({
        "selected": candidate,
        "bound": true,
        "declined": false,
    }))
}

pub async fn comic_list_tasks_core(
    conn: &sea_orm::DatabaseConnection,
    folder_id: i32,
    episode: Option<&str>,
    limit: u64,
) -> Result<Vec<crate::comic::task::TaskView>, AppCommandError> {
    Ok(crate::comic::task::list_tasks(conn, folder_id, episode, limit).await)
}

pub async fn comic_request_speech_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: crate::comic::speech::SpeechRequest,
) -> Result<Value, AppCommandError> {
    crate::comic::speech::request(conn, emitter, folder_id, folder_path, params)
        .await
        .map_err(|error| AppCommandError::invalid_input(error.to_string()))
}

pub async fn comic_set_shot_audio_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: ShotAudioParams,
    cancel: &tokio_util::sync::CancellationToken,
) -> Result<Value, AppCommandError> {
    crate::comic::speech::bind(
        conn,
        emitter,
        folder_id,
        folder_path,
        &params.episode,
        &params.shot,
        params.task_id,
        cancel,
    )
    .await
    .map_err(|error| AppCommandError::invalid_input(error.to_string()))
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ShotAudioParams {
    pub episode: String,
    pub shot: String,
    #[serde(alias = "task_id")]
    pub task_id: i32,
}

pub async fn comic_request_video_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: crate::comic::video::VideoRequest,
) -> Result<Value, AppCommandError> {
    crate::comic::video::request(conn, emitter, folder_id, folder_path, params)
        .await
        .map_err(|error| AppCommandError::invalid_input(error.to_string()))
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ShotVideoParams {
    pub episode: String,
    pub shot: String,
    #[serde(alias = "task_id")]
    pub task_id: i32,
    #[serde(
        alias = "expected_current_task_id",
        deserialize_with = "expected_task_id"
    )]
    pub expected_current_task_id: Option<i32>,
}

fn expected_task_id<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<Option<i32>, D::Error> {
    let value = Option::<i32>::deserialize(deserializer)?;
    if value.is_some_and(|id| id <= 0) {
        return Err(serde::de::Error::custom(
            "expected task id must be positive or null",
        ));
    }
    Ok(value)
}

pub async fn comic_set_shot_video_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: ShotVideoParams,
    cancel: &tokio_util::sync::CancellationToken,
) -> Result<Value, AppCommandError> {
    crate::comic::video::bind(
        conn,
        emitter,
        folder_id,
        folder_path,
        &params.episode,
        &params.shot,
        params.task_id,
        params.expected_current_task_id,
        cancel,
    )
    .await
    .map_err(|error| {
        if error.kind == "conflict" {
            AppCommandError::new(crate::app_error::AppErrorCode::Conflict, error.message)
        } else {
            AppCommandError::invalid_input(error.to_string())
        }
    })
}

/**
 * Mint a short-lived, task-scoped preview grant for a succeeded video
 * artifact. Every consumer of the token re-validates the task, folder, and
 * story jail per byte request; the grant never names a client-supplied path.
 */
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ClearShotVideoParams {
    pub episode: String,
    pub shot: String,
    #[serde(
        alias = "expected_current_task_id",
        deserialize_with = "expected_task_id"
    )]
    pub expected_current_task_id: Option<i32>,
}

pub async fn comic_clear_shot_video_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: ClearShotVideoParams,
) -> Result<Value, AppCommandError> {
    crate::comic::video::unbind(
        conn,
        emitter,
        folder_id,
        folder_path,
        &params.episode,
        &params.shot,
        params.expected_current_task_id,
    )
    .await
    .map_err(|error| {
        if error.kind == "conflict" {
            AppCommandError::new(crate::app_error::AppErrorCode::Conflict, error.message)
        } else {
            AppCommandError::invalid_input(error.to_string())
        }
    })
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ListProviderModelsParams {
    pub kind: String,
    pub api_url: String,
    pub api_key: String,
    #[serde(default)]
    pub extra_json: String,
}

fn media_kind(value: &str) -> Result<crate::comic::provider::MediaKind, AppCommandError> {
    match value {
        "image" => Ok(crate::comic::provider::MediaKind::Image),
        "video" => Ok(crate::comic::provider::MediaKind::Video),
        "tts" => Ok(crate::comic::provider::MediaKind::Tts),
        other => Err(AppCommandError::invalid_input(format!(
            "unknown media kind: {other}"
        ))),
    }
}

/// Fetch the endpoint's model IDs for the provider editor. UI-only: the agent
/// never needs a catalog, it takes the model the user configured.
pub async fn comic_list_provider_models_core(
    params: ListProviderModelsParams,
) -> Result<Value, AppCommandError> {
    crate::db::service::media_provider_service::validate_kind(&params.kind)
        .map_err(|error| AppCommandError::invalid_input(error.to_string()))?;
    let extra = if params.extra_json.trim().is_empty() {
        "{}"
    } else {
        params.extra_json.trim()
    };
    let models = crate::comic::provider::list_models(
        media_kind(&params.kind)?,
        params.api_url.trim(),
        &params.api_key,
        extra,
    )
    .await
    .map_err(|error| AppCommandError::invalid_input(error.to_string()))?;
    Ok(serde_json::json!({ "models": models }))
}

pub async fn comic_request_video_preview_core(
    conn: &sea_orm::DatabaseConnection,
    folder_id: i32,
    task_id: i32,
) -> Result<Value, AppCommandError> {
    let (token, _grant) = crate::comic::preview::issue(conn, folder_id, task_id)
        .await
        .map_err(|_| AppCommandError::not_found("video preview is not available"))?;
    Ok(serde_json::json!({ "token": token, "url": Value::Null }))
}

#[derive(Debug, Clone, Deserialize)]
pub struct ComposeRequestParams {
    pub episode: String,
    #[serde(flatten)]
    pub options: crate::comic::compose::ComposeOptions,
}

pub async fn comic_compose_episode_core(
    conn: &sea_orm::DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: ComposeRequestParams,
) -> Result<Value, AppCommandError> {
    let id = crate::comic::compose_task::submit(
        conn,
        emitter,
        folder_id,
        folder_path,
        &params.episode,
        params.options,
    )
    .await
    .map_err(|error| AppCommandError::invalid_input(error.to_string()))?;
    Ok(
        serde_json::json!({ "taskId": id, "kind": "compose", "target": format!("episodes/{}", params.episode), "hint": "Poll comic_check_compose for status and exported MP4/SRT paths. Prior exports remain intact." }),
    )
}

pub async fn comic_check_compose_core(
    conn: &sea_orm::DatabaseConnection,
    folder_id: i32,
    task_id: i32,
    wait_ms: u64,
) -> Result<Value, AppCommandError> {
    let task = crate::comic::task::check_task(conn, task_id, 0)
        .await
        .filter(|task| task.folder_id == folder_id && task.kind == "compose")
        .ok_or_else(|| AppCommandError::not_found("composition task not found in this project"))?;
    let task = if matches!(task.status.as_str(), "queued" | "running") {
        crate::comic::task::check_task(conn, task_id, wait_ms.min(60_000))
            .await
            .unwrap_or(task)
    } else {
        task
    };
    serde_json::to_value(task).map_err(|error| AppCommandError::io_error(error.to_string()))
}

// ---------------------------------------------------------------------------
// Media provider pool CRUD (settings → 媒体服务)
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaProviderCreate {
    pub kind: String,
    pub name: String,
    pub api_url: String,
    pub api_key: String,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub extra_json: String,
    #[serde(default)]
    pub is_default: bool,
}

pub async fn media_provider_list_core(
    conn: &sea_orm::DatabaseConnection,
) -> Result<Vec<MediaProviderInfo>, AppCommandError> {
    provider_pool(conn).await
}

pub async fn media_provider_create_core(
    conn: &sea_orm::DatabaseConnection,
    params: MediaProviderCreate,
) -> Result<MediaProviderInfo, AppCommandError> {
    if params.name.trim().is_empty() {
        return Err(AppCommandError::invalid_input("name is required"));
    }
    if !params.api_url.starts_with("http://") && !params.api_url.starts_with("https://") {
        return Err(AppCommandError::invalid_input(
            "API URL must start with http:// or https://",
        ));
    }
    let row = media_provider_service::create(
        conn,
        media_provider_service::NewMediaProvider {
            kind: params.kind,
            name: params.name.trim().to_string(),
            api_url: params.api_url.trim().to_string(),
            api_key: params.api_key,
            model: params.model,
            extra_json: params.extra_json,
            is_default: params.is_default,
        },
    )
    .await
    .map_err(AppCommandError::from)?;
    Ok(MediaProviderInfo::from(row))
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaProviderUpdate {
    #[serde(default)]
    pub name: Option<String>,
    #[serde(default)]
    pub api_url: Option<String>,
    #[serde(default)]
    pub api_key: Option<String>,
    #[serde(default)]
    pub model: Option<String>,
    #[serde(default)]
    pub extra_json: Option<String>,
    #[serde(default)]
    pub is_default: Option<bool>,
}

pub async fn media_provider_update_core(
    conn: &sea_orm::DatabaseConnection,
    id: i32,
    params: MediaProviderUpdate,
) -> Result<MediaProviderInfo, AppCommandError> {
    let row = media_provider_service::update(
        conn,
        id,
        media_provider_service::MediaProviderPatch {
            name: params.name,
            api_url: params.api_url,
            api_key: params.api_key,
            model: params.model,
            extra_json: params.extra_json,
            is_default: params.is_default,
        },
    )
    .await
    .map_err(AppCommandError::from)?;
    Ok(MediaProviderInfo::from(row))
}

pub async fn media_provider_delete_core(
    conn: &sea_orm::DatabaseConnection,
    id: i32,
) -> Result<(), AppCommandError> {
    media_provider_service::delete(conn, id)
        .await
        .map_err(AppCommandError::from)
}

/// Connectivity probe for the settings page. M1 validates the URL shape and
/// that the row resolves; real vendor handshakes land with the provider
/// adapters (M3), which reuse the same endpoint.
pub async fn media_provider_test_core(
    conn: &sea_orm::DatabaseConnection,
    id: i32,
) -> Result<Value, AppCommandError> {
    let row = media_provider_service::get_by_id(conn, id)
        .await
        .map_err(AppCommandError::from)?
        .ok_or_else(|| AppCommandError::not_found(format!("media provider not found: {id}")))?;
    let url_ok = row.api_url.starts_with("http://") || row.api_url.starts_with("https://");
    Ok(serde_json::json!({
        "ok": url_ok && !row.api_key.is_empty(),
        "urlOk": url_ok,
        "hasKey": !row.api_key.is_empty(),
        "kind": row.kind,
        "model": row.model,
    }))
}

// ---------------------------------------------------------------------------
// MCP comic tools
// ---------------------------------------------------------------------------

/// The production [`ComicAccess`]: routes each `comic_*` tool to the same
/// `_core` functions the workbench uses, against the token entry's
/// `working_dir`. DB access resolves provider pointers; events ride the same
/// `comic://changed` broadcast the workbench edits emit.
pub struct McpComicTools {
    pub conn: sea_orm::DatabaseConnection,
    pub emitter: EventEmitter,
    /// The host's question registry — `comic_present_candidates` renders its
    /// pick-one image card through the same blocking `ask` machinery, so
    /// peer-close cancellation and the client card come for free.
    pub questions: std::sync::Arc<dyn SessionQuestionAccess>,
}

impl McpComicTools {
    pub fn new(
        conn: sea_orm::DatabaseConnection,
        emitter: EventEmitter,
        questions: std::sync::Arc<dyn SessionQuestionAccess>,
    ) -> Self {
        Self {
            conn,
            emitter,
            questions,
        }
    }

    async fn owns_task(&self, folder_id: i32, task_id: i32) -> bool {
        use sea_orm::EntityTrait;
        crate::db::entities::comic_generation_task::Entity::find_by_id(task_id)
            .one(&self.conn)
            .await
            .ok()
            .flatten()
            .is_some_and(|row| row.folder_id == folder_id && folder_id > 0)
    }

    async fn folder_id(&self, working_dir: &Path) -> Option<i32> {
        // The MCP path works directly on the session's working directory; the
        // folder row (for event payloads) is best-effort — events still fire
        // with folder_id 0 when the project is not a registered folder.
        use crate::db::entities::folder;
        use sea_orm::{ColumnTrait, EntityTrait, QueryFilter};
        folder::Entity::find()
            .filter(folder::Column::Path.eq(working_dir.to_string_lossy().as_ref()))
            .one(&self.conn)
            .await
            .ok()
            .flatten()
            .map(|row| row.id)
    }
}

#[async_trait]
impl ComicAccess for McpComicTools {
    async fn call(
        &self,
        parent_connection_id: &str,
        working_dir: &Path,
        tool: &str,
        args: &Value,
        cancel: tokio_util::sync::CancellationToken,
    ) -> ComicOutcome {
        // Every tool operates on the session's own project; the store is the
        // gate. A not-comic folder refuses uniformly (also what an invalid
        // token already produced at the listener).
        if !StoryStore::exists(working_dir) {
            return ComicOutcome::not_comic_project();
        }
        let parse = |outcome: Result<Value, AppCommandError>| match outcome {
            Ok(data) => ComicOutcome::ok(data),
            Err(e) => ComicOutcome::refused(
                if matches!(e.code, crate::app_error::AppErrorCode::Conflict) {
                    "comic_conflict"
                } else {
                    "comic_invalid"
                },
                e.message.clone(),
            ),
        };
        match tool {
            "comic_get_project" => {
                match self.folder_id(working_dir).await {
                    Some(folder_id) => parse(
                        comic_overview_core(&self.conn, folder_id)
                            .await
                            .map(|o| serde_json::to_value(&o).unwrap_or_default()),
                    ),
                    // Not a registered folder: answer from the files alone.
                    None => {
                        let outcome = store_for(working_dir).and_then(|store| store.overview());
                        match outcome {
                            Ok(overview) => ComicOutcome::ok(serde_json::json!({
                                "folderId": null,
                                "overview": overview,
                                "providerPool": [],
                            })),
                            Err(e) => ComicOutcome::refused("comic_invalid", e.message()),
                        }
                    }
                }
            }
            "comic_set_source" => {
                let Ok(params) = serde_json::from_value::<SourceIngest>(args.clone()) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "comic_set_source needs one of upload_base64 / path / original",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_ingest_source_core(&self.emitter, folder_id, working_dir, params)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_read_source" => {
                let window: ReadWindow = serde_json::from_value(args.clone()).unwrap_or_default();
                parse(
                    comic_read_source_core(working_dir, window)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_get_source_outline" => {
                let limit = args.get("limit").and_then(Value::as_u64).unwrap_or(500) as usize;
                parse(
                    comic_outline_core(working_dir, limit)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_list_assets" => {
                let Some(kind) = args.get("kind").and_then(Value::as_str) else {
                    return ComicOutcome::refused("comic_invalid", "`kind` is required");
                };
                let offset = args.get("offset").and_then(Value::as_u64).unwrap_or(0) as usize;
                let limit = args.get("limit").and_then(Value::as_u64).unwrap_or(100) as usize;
                parse(
                    comic_list_assets_core(working_dir, kind, offset, limit)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_get_asset" => {
                let (Some(kind), Some(id)) = (
                    args.get("kind").and_then(Value::as_str),
                    args.get("id").and_then(Value::as_str),
                ) else {
                    return ComicOutcome::refused("comic_invalid", "`kind` and `id` are required");
                };
                comic_get_asset_core(working_dir, kind, id)
                    .await
                    .map_or_else(
                        |e| {
                            let msg = e.message.clone();
                            let code = if msg.contains("not found") {
                                "comic_not_found"
                            } else {
                                "comic_invalid"
                            };
                            ComicOutcome::refused(code, msg)
                        },
                        ComicOutcome::ok,
                    )
            }
            "comic_search_assets" => {
                let Some(query) = args.get("query").and_then(Value::as_str) else {
                    return ComicOutcome::refused("comic_invalid", "`query` is required");
                };
                parse(
                    comic_search_assets_core(working_dir, query)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_upsert_series" => {
                let Ok(patch) = serde_json::from_value::<SeriesPatch>(args.clone()) else {
                    return ComicOutcome::refused("comic_invalid", "invalid series patch");
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_upsert_series_core(&self.emitter, folder_id, working_dir, patch)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_upsert_episode" => {
                let Ok(meta) = serde_json::from_value::<EpisodeMeta>(args.clone()) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "comic_upsert_episode needs id + title",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_upsert_episode_core(&self.emitter, folder_id, working_dir, meta)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_write_script" => {
                let (Some(episode), Some(script)) = (
                    args.get("episode").and_then(Value::as_str),
                    args.get("script").and_then(Value::as_str),
                ) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "`episode` and `script` are required",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_write_script_core(&self.emitter, folder_id, working_dir, episode, script)
                        .await
                        .map(|_| serde_json::json!({ "written": episode })),
                )
            }
            "comic_upsert_storyboard" => {
                let (Some(episode), Some(shots)) = (
                    args.get("episode").and_then(Value::as_str),
                    args.get("shots").cloned(),
                ) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "`episode` and `shots` are required",
                    );
                };
                let Ok(shots) = serde_json::from_value::<Vec<StoryShot>>(shots) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "`shots` is not a valid shot array (each needs id + description)",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_replace_storyboard_core(
                        &self.emitter,
                        folder_id,
                        working_dir,
                        episode,
                        shots,
                    )
                    .await
                    .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_upsert_shot" => {
                let (Some(episode), Some(shot)) = (
                    args.get("episode").and_then(Value::as_str),
                    args.get("shot").cloned(),
                ) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "`episode` and `shot` are required",
                    );
                };
                let Ok(shot) = serde_json::from_value::<StoryShot>(shot) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "`shot` is not a valid shot object (needs id + description)",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_upsert_shot_core(&self.emitter, folder_id, working_dir, episode, shot)
                        .await
                        .map(|_| serde_json::json!({ "upserted": true })),
                )
            }
            "comic_upsert_character" => {
                let Ok(card) = serde_json::from_value::<CharacterCard>(args.clone()) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "comic_upsert_character needs slug + name",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_upsert_character_core(&self.emitter, folder_id, working_dir, card).await,
                )
            }
            "comic_upsert_scene" => {
                let Ok(card) = serde_json::from_value::<SceneCard>(args.clone()) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "comic_upsert_scene needs slug + name",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_upsert_scene_core(&self.emitter, folder_id, working_dir, card)
                        .await
                        .map(|_| serde_json::json!({ "upserted": true })),
                )
            }
            "comic_upsert_prop" => {
                let Ok(card) = serde_json::from_value::<PropCard>(args.clone()) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "comic_upsert_prop needs slug + name",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_upsert_prop_core(&self.emitter, folder_id, working_dir, card)
                        .await
                        .map(|_| serde_json::json!({ "upserted": true })),
                )
            }
            "comic_set_project_provider" => {
                let Some(kind) = args.get("kind").and_then(Value::as_str) else {
                    return ComicOutcome::refused("comic_invalid", "`kind` is required");
                };
                let provider_id = args
                    .get("provider_id")
                    .and_then(Value::as_i64)
                    .map(|v| v as i32);
                let provider_name = args
                    .get("provider_name")
                    .and_then(Value::as_str)
                    .map(str::to_string);
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_set_project_provider_core(
                        &self.conn,
                        &self.emitter,
                        folder_id,
                        working_dir,
                        kind,
                        provider_id,
                        provider_name,
                    )
                    .await
                    .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_build_shot_prompt" => {
                let (Some(episode), Some(shot)) = (
                    args.get("episode").and_then(Value::as_str),
                    args.get("shot").and_then(Value::as_str),
                ) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "`episode` and `shot` are required",
                    );
                };
                parse(
                    comic_build_shot_prompt_core(working_dir, episode, shot)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_request_generation" => {
                let Ok(mut params) =
                    serde_json::from_value::<GenerationRequestParams>(args.clone())
                else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "comic_request_generation needs `episode` and `shot`",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                let series = match StoryStore::open(working_dir)
                    .and_then(|store| store.read_series())
                {
                    Ok(series) => series,
                    Err(error) => return ComicOutcome::refused("comic_invalid", error.message()),
                };
                let count = params
                    .count
                    .unwrap_or(series.default_candidate_count)
                    .max(1);
                if count > 64 {
                    return ComicOutcome::refused("comic_invalid", "at most 64 images per request");
                }
                if count > series.max_images_per_request.max(1) {
                    let spec = QuestionSpec {
                        id: uuid::Uuid::new_v4().to_string(),
                        question: format!("Generate {count} images for {}/{}? This exceeds the project limit and may incur additional provider charges.", params.episode, params.shot),
                        header: "Generation".into(),
                        multi_select: false,
                        options: vec![
                            QuestionOption { label: "Approve batch".into(), description: format!("Generate {count} candidate images"), image: None },
                            QuestionOption { label: "Cancel".into(), description: "Do not submit or charge for this batch".into(), image: None },
                        ],
                        is_secret: false,
                    };
                    let Some(registered) = self
                        .questions
                        .register_question(parent_connection_id, vec![spec])
                        .await
                    else {
                        return ComicOutcome::refused(
                            "comic_confirmation_unavailable",
                            "could not register a cost confirmation; no request was submitted",
                        );
                    };
                    let answer = tokio::select! {
                        biased;
                        _ = cancel.cancelled() => {
                            self.questions.cancel_question(parent_connection_id, &registered.question_id).await;
                            return ComicOutcome::refused("comic_cancelled", "generation cancelled before submission");
                        }
                        answer = registered.answer_rx => answer.ok(),
                    };
                    params.confirmed = answer.is_some_and(|answer| {
                        !answer.declined
                            && answer
                                .answers
                                .first()
                                .and_then(|a| a.selected.first())
                                .is_some_and(|label| label == "Approve batch")
                    });
                    if !params.confirmed || cancel.is_cancelled() {
                        return ComicOutcome::refused(
                            "comic_declined",
                            "generation not approved; no request was submitted",
                        );
                    }
                }
                match comic_request_generation_core(
                    &self.conn,
                    &self.emitter,
                    folder_id,
                    working_dir,
                    params,
                )
                .await
                {
                    Ok(data) => ComicOutcome::ok(data),
                    Err(e) => {
                        let msg = e.message.clone();
                        if let Some(rest) = msg.strip_prefix("comic_confirm_batch:") {
                            ComicOutcome::refused("comic_confirm_batch", rest.trim())
                        } else {
                            ComicOutcome::refused("comic_invalid", msg)
                        }
                    }
                }
            }
            "comic_request_speech" => {
                let params = match serde_json::from_value::<crate::comic::speech::SpeechRequest>(
                    args.clone(),
                ) {
                    Ok(params) => params,
                    Err(error) => return ComicOutcome::refused("comic_invalid", error.to_string()),
                };
                if cancel.is_cancelled() {
                    return ComicOutcome::refused(
                        "comic_cancelled",
                        "speech cancelled before submission",
                    );
                }
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_request_speech_core(
                        &self.conn,
                        &self.emitter,
                        folder_id,
                        working_dir,
                        params,
                    )
                    .await,
                )
            }
            "comic_set_shot_audio" => {
                let params = match serde_json::from_value::<ShotAudioParams>(args.clone()) {
                    Ok(params) => params,
                    Err(error) => return ComicOutcome::refused("comic_invalid", error.to_string()),
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_set_shot_audio_core(
                        &self.conn,
                        &self.emitter,
                        folder_id,
                        working_dir,
                        params,
                        &cancel,
                    )
                    .await,
                )
            }
            "comic_request_video" => {
                let params =
                    match serde_json::from_value::<crate::comic::video::VideoRequest>(args.clone())
                    {
                        Ok(params) => params,
                        Err(error) => {
                            return ComicOutcome::refused("comic_invalid", error.to_string())
                        }
                    };
                if cancel.is_cancelled() {
                    return ComicOutcome::refused(
                        "comic_cancelled",
                        "video cancelled before submission",
                    );
                }
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_request_video_core(
                        &self.conn,
                        &self.emitter,
                        folder_id,
                        working_dir,
                        params,
                    )
                    .await,
                )
            }
            "comic_set_shot_video" => {
                let params = match serde_json::from_value::<ShotVideoParams>(args.clone()) {
                    Ok(params) => params,
                    Err(error) => return ComicOutcome::refused("comic_invalid", error.to_string()),
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_set_shot_video_core(
                        &self.conn,
                        &self.emitter,
                        folder_id,
                        working_dir,
                        params,
                        &cancel,
                    )
                    .await,
                )
            }
            "comic_clear_shot_video" => {
                let params = match serde_json::from_value::<ClearShotVideoParams>(args.clone()) {
                    Ok(params) => params,
                    Err(error) => return ComicOutcome::refused("comic_invalid", error.to_string()),
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_clear_shot_video_core(
                        &self.conn,
                        &self.emitter,
                        folder_id,
                        working_dir,
                        params,
                    )
                    .await,
                )
            }
            "comic_compose_episode" => {
                let params = match serde_json::from_value::<ComposeRequestParams>(args.clone()) {
                    Ok(params) => params,
                    Err(error) => return ComicOutcome::refused("comic_invalid", error.to_string()),
                };
                if cancel.is_cancelled() {
                    return ComicOutcome::refused(
                        "comic_cancelled",
                        "composition cancelled before submission",
                    );
                }
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_compose_episode_core(
                        &self.conn,
                        &self.emitter,
                        folder_id,
                        working_dir,
                        params,
                    )
                    .await,
                )
            }
            "comic_check_compose" => {
                let Some(task_id) = args
                    .get("task_id")
                    .and_then(Value::as_i64)
                    .and_then(|id| i32::try_from(id).ok())
                else {
                    return ComicOutcome::refused("comic_invalid", "`task_id` is required");
                };
                let wait_ms = args
                    .get("wait_ms")
                    .and_then(Value::as_u64)
                    .unwrap_or(0)
                    .min(60_000);
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(comic_check_compose_core(&self.conn, folder_id, task_id, wait_ms).await)
            }
            "comic_check_generation" => {
                let Some(task_id) = args
                    .get("task_id")
                    .and_then(Value::as_i64)
                    .map(|v| v as i32)
                else {
                    return ComicOutcome::refused("comic_invalid", "`task_id` is required");
                };
                let wait_ms = args
                    .get("wait_ms")
                    .and_then(Value::as_u64)
                    .unwrap_or(0)
                    .min(60_000);
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                if !self.owns_task(folder_id, task_id).await {
                    return ComicOutcome::refused(
                        "comic_not_found",
                        "task not found in this project",
                    );
                }
                parse(
                    comic_check_generation_core(&self.conn, task_id, wait_ms)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_cancel_generation" => {
                let Some(task_id) = args
                    .get("task_id")
                    .and_then(Value::as_i64)
                    .map(|v| v as i32)
                else {
                    return ComicOutcome::refused("comic_invalid", "`task_id` is required");
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                if !self.owns_task(folder_id, task_id).await {
                    return ComicOutcome::refused(
                        "comic_not_found",
                        "task not found in this project",
                    );
                }
                parse(
                    comic_cancel_generation_core(&self.conn, &self.emitter, task_id)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_set_shot_image" => {
                let (Some(episode), Some(shot), Some(candidate)) = (
                    args.get("episode").and_then(Value::as_str),
                    args.get("shot").and_then(Value::as_str),
                    args.get("candidate").and_then(Value::as_str),
                ) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "`episode`, `shot` and `candidate` are required",
                    );
                };
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_set_shot_image_core(
                        &self.emitter,
                        folder_id,
                        working_dir,
                        episode,
                        shot,
                        candidate,
                    )
                    .await
                    .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_present_candidates" => {
                let (Some(episode), Some(shot)) = (
                    args.get("episode").and_then(Value::as_str),
                    args.get("shot").and_then(Value::as_str),
                ) else {
                    return ComicOutcome::refused(
                        "comic_invalid",
                        "`episode` and `shot` are required",
                    );
                };
                let candidates: Vec<String> = args
                    .get("candidates")
                    .and_then(Value::as_array)
                    .map(|arr| {
                        arr.iter()
                            .filter_map(Value::as_str)
                            .map(str::to_string)
                            .collect()
                    })
                    .unwrap_or_default();
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                parse(
                    comic_present_candidates_core(
                        &self.questions,
                        &self.emitter,
                        CandidatePresentation {
                            parent_connection_id,
                            folder_id,
                            folder_path: working_dir,
                            episode,
                            shot,
                            candidates,
                        },
                        cancel,
                    )
                    .await
                    .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            "comic_list_tasks" => {
                let folder_id = self.folder_id(working_dir).await.unwrap_or(0);
                let episode = args.get("episode").and_then(Value::as_str);
                let limit = args.get("limit").and_then(Value::as_u64).unwrap_or(50);
                parse(
                    comic_list_tasks_core(&self.conn, folder_id, episode, limit)
                        .await
                        .map(|r| serde_json::to_value(&r).unwrap_or_default()),
                )
            }
            other => ComicOutcome::refused(
                crate::acp::comic_tools::ERROR_UNKNOWN_TOOL,
                format!("unknown comic tool: {other}"),
            ),
        }
    }
}

// ---------------------------------------------------------------------------
// Tauri commands
// ---------------------------------------------------------------------------

/// Create the `story/` skeleton for a folder (the workbench "new comic
/// project" wizard). Idempotent.
#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_init_project(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    title: String,
) -> Result<ComicOverview, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    let store =
        StoryStore::init(&path, &title).map_err(|e| AppCommandError::invalid_input(e.message()))?;
    drop(store);
    emit_event(
        &emitter,
        COMIC_CHANGED_EVENT,
        &ComicChange::Refresh { folder_id },
    );
    comic_overview_core(&db.conn, folder_id).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_get_overview(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
) -> Result<ComicOverview, AppCommandError> {
    comic_overview_core(&db.conn, folder_id).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_ingest_source(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    params: SourceIngest,
) -> Result<SourceReport, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_ingest_source_core(&emitter, folder_id, &path, params).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_read_source(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
    window: ReadWindow,
) -> Result<WindowRead, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_read_source_core(&path, window).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_list_chapters(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
    limit: Option<usize>,
) -> Result<Vec<crate::comic::model::Chapter>, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_outline_core(&path, limit.unwrap_or(500)).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_list_assets(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
    kind: String,
    offset: Option<usize>,
    limit: Option<usize>,
) -> Result<AssetList, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_list_assets_core(&path, &kind, offset.unwrap_or(0), limit.unwrap_or(200)).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_get_asset(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
    kind: String,
    id: String,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_get_asset_core(&path, &kind, &id).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_upsert_series(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    patch: SeriesPatch,
) -> Result<SeriesConfig, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_upsert_series_core(&emitter, folder_id, &path, patch).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_set_provider(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    kind: String,
    provider_id: Option<i32>,
    provider_name: Option<String>,
) -> Result<SeriesConfig, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_set_project_provider_core(
        &db.conn,
        &emitter,
        folder_id,
        &path,
        &kind,
        provider_id,
        provider_name,
    )
    .await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn list_media_providers(
    db: tauri::State<'_, AppDatabase>,
) -> Result<Vec<MediaProviderInfo>, AppCommandError> {
    media_provider_list_core(&db.conn).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn create_media_provider(
    db: tauri::State<'_, AppDatabase>,
    params: MediaProviderCreate,
) -> Result<MediaProviderInfo, AppCommandError> {
    media_provider_create_core(&db.conn, params).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn update_media_provider(
    db: tauri::State<'_, AppDatabase>,
    id: i32,
    params: MediaProviderUpdate,
) -> Result<MediaProviderInfo, AppCommandError> {
    media_provider_update_core(&db.conn, id, params).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn delete_media_provider(
    db: tauri::State<'_, AppDatabase>,
    id: i32,
) -> Result<(), AppCommandError> {
    media_provider_delete_core(&db.conn, id).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn test_media_provider(
    db: tauri::State<'_, AppDatabase>,
    id: i32,
) -> Result<Value, AppCommandError> {
    media_provider_test_core(&db.conn, id).await
}

// ---------------------------------------------------------------------------
// M2 workbench editing commands (episode / script / storyboard / assets)
// ---------------------------------------------------------------------------

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_upsert_episode(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    meta: EpisodeMeta,
) -> Result<EpisodeMeta, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_upsert_episode_core(&emitter, folder_id, &path, meta).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_write_script(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    episode: String,
    script: String,
) -> Result<(), AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_write_script_core(&emitter, folder_id, &path, &episode, &script).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_replace_storyboard(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    episode: String,
    shots: Vec<StoryShot>,
) -> Result<Storyboard, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_replace_storyboard_core(&emitter, folder_id, &path, &episode, shots).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_upsert_shot(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    episode: String,
    shot: StoryShot,
) -> Result<(), AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_upsert_shot_core(&emitter, folder_id, &path, &episode, shot).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_delete_shot(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    episode: String,
    shot: String,
) -> Result<(), AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_delete_shot_core(&emitter, folder_id, &path, &episode, &shot).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_reorder_shots(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    episode: String,
    order: Vec<String>,
) -> Result<Storyboard, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_reorder_shots_core(&emitter, folder_id, &path, &episode, order).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_upsert_character(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    card: CharacterCard,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_upsert_character_core(&emitter, folder_id, &path, card).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_upsert_scene(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    card: SceneCard,
) -> Result<(), AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_upsert_scene_core(&emitter, folder_id, &path, card).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_upsert_prop(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    card: PropCard,
) -> Result<(), AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_upsert_prop_core(&emitter, folder_id, &path, card).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_list_tasks(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
    episode: Option<String>,
    limit: Option<u64>,
) -> Result<Vec<crate::comic::task::TaskView>, AppCommandError> {
    comic_list_tasks_core(&db.conn, folder_id, episode.as_deref(), limit.unwrap_or(50)).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_request_speech(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    params: crate::comic::speech::SpeechRequest,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_request_speech_core(
        &db.conn,
        &EventEmitter::Tauri(app),
        folder_id,
        &path,
        params,
    )
    .await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_set_shot_audio(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    episode: String,
    shot: String,
    task_id: i32,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_set_shot_audio_core(
        &db.conn,
        &EventEmitter::Tauri(app),
        folder_id,
        &path,
        ShotAudioParams {
            episode,
            shot,
            task_id,
        },
        &tokio_util::sync::CancellationToken::new(),
    )
    .await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_request_video(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    params: crate::comic::video::VideoRequest,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_request_video_core(
        &db.conn,
        &EventEmitter::Tauri(app),
        folder_id,
        &path,
        params,
    )
    .await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_set_shot_video(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    episode: String,
    shot: String,
    task_id: i32,
    expected_current_task_id: Option<i32>,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_set_shot_video_core(
        &db.conn,
        &EventEmitter::Tauri(app),
        folder_id,
        &path,
        ShotVideoParams {
            episode,
            shot,
            task_id,
            expected_current_task_id,
        },
        &tokio_util::sync::CancellationToken::new(),
    )
    .await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_clear_shot_video(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    params: ClearShotVideoParams,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_clear_shot_video_core(
        &db.conn,
        &EventEmitter::Tauri(app),
        folder_id,
        &path,
        params,
    )
    .await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_list_provider_models(
    params: ListProviderModelsParams,
) -> Result<Value, AppCommandError> {
    comic_list_provider_models_core(params).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_request_video_preview(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
    task_id: i32,
) -> Result<Value, AppCommandError> {
    let issued = comic_request_video_preview_core(&db.conn, folder_id, task_id).await?;
    // WebView2 serves custom schemes from `http://<scheme>.localhost/…`;
    // every other engine keeps the scheme spelling (see `browser::doc_guest`).
    let token = issued["token"].as_str().unwrap_or_default().to_string();
    let url = if token.is_empty() {
        None
    } else if cfg!(windows) {
        Some(format!("http://codeg-media.localhost/{token}"))
    } else {
        Some(format!("codeg-media://localhost/{token}"))
    };
    Ok(serde_json::json!({ "token": token, "url": url }))
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_compose_episode(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    params: ComposeRequestParams,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    comic_compose_episode_core(
        &db.conn,
        &EventEmitter::Tauri(app),
        folder_id,
        &path,
        params,
    )
    .await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_check_compose(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
    task_id: i32,
    wait_ms: Option<u64>,
) -> Result<Value, AppCommandError> {
    comic_check_compose_core(&db.conn, folder_id, task_id, wait_ms.unwrap_or(0)).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_bind_candidate(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    episode: String,
    shot: String,
    candidate: String,
) -> Result<Value, AppCommandError> {
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_set_shot_image_core(&emitter, folder_id, &path, &episode, &shot, &candidate).await
}

/// Save an uploaded reference image (base64) into a card slot. `slot` follows
/// the store's grammar: turnaround/front|side_45|side, expressions, refs,
/// outfit/<slug>/front|side_45|side.
#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_save_asset_image(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    folder_id: i32,
    kind: String,
    slug: String,
    slot: String,
    image_base64: String,
) -> Result<String, AppCommandError> {
    use base64::Engine as _;
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(image_base64.trim())
        .map_err(|e| AppCommandError::invalid_input(format!("image_base64: {e}")))?;
    let path = folder_path(&db.conn, folder_id).await?;
    let emitter = EventEmitter::Tauri(app);
    comic_save_asset_image_core(&emitter, folder_id, &path, &kind, &slug, &slot, bytes).await
}

/// Image bytes are returned only to the workbench, never as an MCP tool result.
#[derive(Serialize)]
pub struct ComicImageData {
    pub mime: String,
    pub data: String,
}

pub async fn comic_read_image_core(
    conn: &sea_orm::DatabaseConnection,
    folder_id: i32,
    relative: String,
) -> Result<ComicImageData, AppCommandError> {
    let folder = folder_path(conn, folder_id).await?;
    tokio::task::spawn_blocking(move || {
        use std::io::Read;
        let store = store_for(&folder).map_err(|e| AppCommandError::invalid_input(e.message()))?;
        let path = store
            .resolve(&relative)
            .map_err(|e| AppCommandError::invalid_input(e.message()))?;
        let root = store
            .root()
            .canonicalize()
            .map_err(|e| AppCommandError::io_error(e.to_string()))?;
        let path = path
            .canonicalize()
            .map_err(|e| AppCommandError::io_error(e.to_string()))?;
        if !path.starts_with(root) {
            return Err(AppCommandError::invalid_input(
                "image path escapes the comic project",
            ));
        }
        const MAX_IMAGE_BYTES: u64 = 20 * 1024 * 1024;
        let file =
            std::fs::File::open(path).map_err(|e| AppCommandError::io_error(e.to_string()))?;
        let mut bytes = Vec::new();
        file.take(MAX_IMAGE_BYTES + 1)
            .read_to_end(&mut bytes)
            .map_err(|e| AppCommandError::io_error(e.to_string()))?;
        if bytes.len() as u64 > MAX_IMAGE_BYTES {
            return Err(AppCommandError::invalid_input("image exceeds 20 MiB"));
        }
        let format = image::guess_format(&bytes)
            .map_err(|e| AppCommandError::invalid_input(e.to_string()))?;
        let mime = match format {
            image::ImageFormat::Png => "image/png",
            image::ImageFormat::Jpeg => "image/jpeg",
            image::ImageFormat::WebP => "image/webp",
            _ => {
                return Err(AppCommandError::invalid_input(
                    "only PNG/JPEG/WebP previews are supported",
                ))
            }
        };
        Ok(ComicImageData {
            mime: mime.into(),
            data: STANDARD.encode(bytes),
        })
    })
    .await
    .map_err(|e| AppCommandError::io_error(e.to_string()))?
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn comic_read_image(
    db: tauri::State<'_, AppDatabase>,
    folder_id: i32,
    path: String,
) -> Result<ComicImageData, AppCommandError> {
    comic_read_image_core(&db.conn, folder_id, path).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn video_unbind_requires_an_explicit_nullable_expected_binding() {
        // The field is required and explicitly nullable, exactly like the
        // bind confirmation: absence is not a silent null.
        assert!(
            serde_json::from_value::<ClearShotVideoParams>(serde_json::json!({
                "episode": "ep001",
                "shot": "shot-0001"
            }))
            .is_err()
        );
        for (args, expected) in [
            (
                serde_json::json!({"episode":"ep001","shot":"shot-0001","expected_current_task_id":serde_json::Value::Null}),
                None,
            ),
            (
                serde_json::json!({"episode":"ep001","shot":"shot-0001","expected_current_task_id":42}),
                Some(42),
            ),
        ] {
            let parsed: ClearShotVideoParams = serde_json::from_value(args).unwrap();
            assert_eq!(parsed.expected_current_task_id, expected);
        }
        assert!(
            serde_json::from_value::<ClearShotVideoParams>(serde_json::json!({
                "episode":"ep001","shot":"shot-0001","expected_current_task_id":-1
            }))
            .is_err()
        );
    }

    #[test]
    fn video_confirmation_requires_an_explicit_nullable_expected_binding() {
        let mut args = serde_json::json!({
            "episode": "ep001", "shot": "shot-0001", "task_id": 42
        });
        assert!(serde_json::from_value::<ShotVideoParams>(args.clone()).is_err());
        args["expected_current_task_id"] = Value::Null;
        assert_eq!(
            serde_json::from_value::<ShotVideoParams>(args.clone())
                .unwrap()
                .expected_current_task_id,
            None
        );
        args["expected_current_task_id"] = serde_json::json!(41);
        assert_eq!(
            serde_json::from_value::<ShotVideoParams>(args.clone())
                .unwrap()
                .expected_current_task_id,
            Some(41)
        );
        args["expected_current_task_id"] = serde_json::json!(-1);
        assert!(serde_json::from_value::<ShotVideoParams>(args).is_err());
    }

    #[test]
    fn mcp_source_arguments_preserve_upload_and_split_options() {
        let params: SourceIngest = serde_json::from_value(serde_json::json!({
            "upload_base64": "dGV4dA==",
            "max_heading_len": 72,
            "fallback_window_chars": 12000
        }))
        .unwrap();
        assert_eq!(params.upload_base64.as_deref(), Some("dGV4dA=="));
        assert_eq!(params.max_heading_len, Some(72));
        assert_eq!(params.fallback_window_chars, Some(12000));
    }

    #[test]
    fn a_model_supplied_confirmed_flag_is_not_user_approval() {
        let params: GenerationRequestParams = serde_json::from_value(serde_json::json!({
            "episode": "ep001", "shot": "s1", "count": 64, "confirmed": true
        }))
        .unwrap();
        assert!(!params.confirmed);
    }

    #[test]
    fn mcp_generation_arguments_keep_single_call_provider_override() {
        let params: GenerationRequestParams = serde_json::from_value(serde_json::json!({
            "episode": "ep001", "shot": "shot-0001",
            "provider_id": 12, "provider_name": "Local", "count": 4
        }))
        .unwrap();
        assert_eq!(params.provider_id, Some(12));
        assert_eq!(params.provider_name.as_deref(), Some("Local"));
    }

    #[test]
    fn mcp_series_patch_keeps_style_seed_and_limits() {
        let patch: SeriesPatch = serde_json::from_value(serde_json::json!({
            "style_bible": "ink", "global_seed": 42,
            "max_images_per_request": 4, "default_candidate_count": 2
        }))
        .unwrap();
        assert_eq!(patch.style_bible.as_deref(), Some("ink"));
        assert_eq!(patch.global_seed, Some(42));
        assert_eq!(patch.max_images_per_request, Some(4));
        assert_eq!(patch.default_candidate_count, Some(2));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn candidate_question_is_removed_on_call_cancellation() {
        use crate::acp::question::{QuestionOutcome, RegisteredQuestion};
        use tokio::sync::{Mutex, Notify};
        struct Questions {
            registered: Notify,
            pending: Mutex<Option<tokio::sync::oneshot::Sender<QuestionOutcome>>>,
        }
        #[async_trait]
        impl SessionQuestionAccess for Questions {
            async fn register_question(
                &self,
                _: &str,
                _: Vec<QuestionSpec>,
            ) -> Option<RegisteredQuestion> {
                let (tx, rx) = tokio::sync::oneshot::channel();
                *self.pending.lock().await = Some(tx);
                self.registered.notify_one();
                Some(RegisteredQuestion {
                    question_id: "pick".into(),
                    answer_rx: rx,
                })
            }
            async fn cancel_question(&self, _: &str, id: &str) {
                assert_eq!(id, "pick");
                self.pending.lock().await.take();
            }
            async fn cancel_questions_by_parent(&self, _: &str) {
                self.pending.lock().await.take();
            }
        }
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Test").unwrap();
        let rel = "episodes/ep001/shots/s1/candidates/task-1-01.png";
        store
            .write_artifact(rel, &[0x89, b'P', b'N', b'G'])
            .unwrap();
        let questions = std::sync::Arc::new(Questions {
            registered: Notify::new(),
            pending: Mutex::new(None),
        });
        let access: std::sync::Arc<dyn SessionQuestionAccess> = questions.clone();
        let cancel = tokio_util::sync::CancellationToken::new();
        let wait = comic_present_candidates_core(
            &access,
            &EventEmitter::Noop,
            CandidatePresentation {
                parent_connection_id: "parent",
                folder_id: 7,
                folder_path: dir.path(),
                episode: "ep001",
                shot: "s1",
                candidates: vec![rel.into()],
            },
            cancel.clone(),
        );
        let stop = async {
            questions.registered.notified().await;
            cancel.cancel()
        };
        let (outcome, _) = tokio::join!(wait, stop);
        assert_eq!(outcome.unwrap()["bound"], false);
        assert!(questions.pending.lock().await.is_none());
    }

    #[test]
    fn web_source_requests_use_the_same_nested_envelope_as_desktop() {
        use crate::web::handlers::comic::{IngestParams, ReadSourceParams};
        let ingest: IngestParams = serde_json::from_value(serde_json::json!({
            "folderId": 7, "params": {"original": true}
        }))
        .unwrap();
        assert!(ingest.params.original);
        let read: ReadSourceParams = serde_json::from_value(serde_json::json!({
            "folderId": 7,
            "window": {"from_chapter": 3, "offset_chars": 8000, "max_chars": 2000}
        }))
        .unwrap();
        assert_eq!(read.window.from_chapter, Some(3));
        assert_eq!(read.window.offset_chars, 8000);
    }
}
