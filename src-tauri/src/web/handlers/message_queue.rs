use std::sync::Arc;

use axum::{extract::Extension, Json};
use serde::Deserialize;

use crate::app_error::AppCommandError;
use crate::app_state::AppState;
use crate::db::service::message_queue_service::{self, QueueItemInput};
use crate::models::MessageQueueItem;
use crate::web::event_bridge::{emit_event, MessageQueueChange, MESSAGE_QUEUE_CHANGED_EVENT};

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ListMessageQueueParams {
    pub owner_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SaveMessageQueueParams {
    pub owner_id: String,
    pub items: Vec<QueueItemInput>,
    pub origin: String,
}

pub async fn message_queue_capabilities() -> Json<serde_json::Value> {
    Json(serde_json::json!({ "backend_drain": true, "pause_recovery": true, "scheduled_send": true }))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RetryMessageQueueParams {
    pub owner_id: String,
    pub item_id: String,
}

pub async fn message_queue_state(
    Extension(state): Extension<Arc<AppState>>, Json(params): Json<ListMessageQueueParams>,
) -> Result<Json<crate::db::service::message_queue_control_service::MessageQueueState>, AppCommandError> {
    Ok(Json(crate::db::service::message_queue_control_service::get_state(&state.db.conn, &params.owner_id).await.map_err(AppCommandError::from)?))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ContinueQueueParams { pub owner_id: String, pub failed_attempt_id: String }

pub async fn message_queue_continue(
    Extension(state): Extension<Arc<AppState>>, Json(params): Json<ContinueQueueParams>,
) -> Result<Json<()>, AppCommandError> {
    let control = crate::db::service::message_queue_control_service::request_continue(&state.db.conn, &params.owner_id, &params.failed_attempt_id).await.map_err(AppCommandError::from)?;
    emit_event(&state.emitter, crate::web::event_bridge::MESSAGE_QUEUE_STATE_EVENT, control);
    state.message_queue_drain.notify(params.owner_id);
    Ok(Json(()))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReleaseHoldParams {
    pub owner_id: String,
}

/// Lift the user-stop hold so the queue drains again (mirrors the Tauri
/// command: CAS the hold away, broadcast the new barrier state, wake the
/// drainer).
pub async fn message_queue_release_hold(
    Extension(state): Extension<Arc<AppState>>, Json(params): Json<ReleaseHoldParams>,
) -> Result<Json<()>, AppCommandError> {
    let control = crate::db::service::message_queue_control_service::release_hold(&state.db.conn, &params.owner_id).await.map_err(AppCommandError::from)?;
    emit_event(&state.emitter, crate::web::event_bridge::MESSAGE_QUEUE_STATE_EVENT, control);
    state.message_queue_drain.notify(params.owner_id);
    Ok(Json(()))
}

/// Send ONE queued item immediately at the user's explicit request. The
/// drainer owns the connection setup and broadcasts the row's delivery state,
/// so a failure surfaces on the row rather than this response.
pub async fn message_queue_send_now(
    Extension(state): Extension<Arc<AppState>>, Json(params): Json<RetryMessageQueueParams>,
) -> Result<Json<()>, AppCommandError> {
    state
        .message_queue_drain
        .send_now(&params.owner_id, &params.item_id);
    Ok(Json(()))
}

pub async fn message_queue_retry(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<RetryMessageQueueParams>,
) -> Result<Json<()>, AppCommandError> {
    message_queue_service::retry(&state.db.conn, &params.owner_id, &params.item_id)
        .await
        .map_err(AppCommandError::from)?;
    let items = message_queue_service::list(&state.db.conn, &params.owner_id)
        .await
        .map_err(AppCommandError::from)?;
    emit_event(
        &state.emitter,
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: params.owner_id.clone(),
            origin: "backend-retry".into(),
            items,
        },
    );
    state.message_queue_drain.notify(params.owner_id);
    Ok(Json(()))
}

pub async fn message_queue_list(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ListMessageQueueParams>,
) -> Result<Json<Vec<MessageQueueItem>>, AppCommandError> {
    let result = message_queue_service::list(&state.db.conn, &params.owner_id)
        .await
        .map_err(AppCommandError::from)?;
    Ok(Json(result))
}

pub async fn message_queue_save(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<SaveMessageQueueParams>,
) -> Result<Json<()>, AppCommandError> {
    let saved = message_queue_service::save(&state.db.conn, &params.owner_id, params.items)
        .await
        .map_err(AppCommandError::from)?;
    emit_event(
        &state.emitter,
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: params.owner_id.clone(),
            origin: params.origin,
            items: saved,
        },
    );
    // A save onto an idle conversation is a drain trigger of its own: the
    // first item of a fresh queue should go out without waiting for the
    // periodic sweep.
    state.message_queue_drain.notify(params.owner_id);
    Ok(Json(()))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EnqueueQueueParams {
    pub owner_id: String,
    pub item: QueueItemInput,
    pub scheduled_at: Option<String>,
    pub origin: String,
}

pub async fn message_queue_enqueue(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<EnqueueQueueParams>,
) -> Result<Json<Vec<MessageQueueItem>>, AppCommandError> {
    let at = message_queue_service::parse_schedule_input(params.scheduled_at)
        .map_err(AppCommandError::from)?;
    message_queue_service::enqueue(&state.db.conn, &params.owner_id, params.item, at)
        .await
        .map_err(AppCommandError::from)?;
    let items = message_queue_service::list(&state.db.conn, &params.owner_id)
        .await
        .map_err(AppCommandError::from)?;
    emit_event(
        &state.emitter,
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: params.owner_id.clone(),
            origin: params.origin,
            items: items.clone(),
        },
    );
    state.message_queue_drain.notify(params.owner_id);
    Ok(Json(items))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScheduleQueueParams {
    pub owner_id: String,
    pub item_id: String,
    pub scheduled_at: Option<String>,
}

pub async fn message_queue_set_schedule(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<ScheduleQueueParams>,
) -> Result<Json<Vec<MessageQueueItem>>, AppCommandError> {
    let at = message_queue_service::parse_schedule_input(params.scheduled_at)
        .map_err(AppCommandError::from)?;
    message_queue_service::set_schedule(&state.db.conn, &params.owner_id, &params.item_id, at)
        .await
        .map_err(AppCommandError::from)?;
    let items = message_queue_service::list(&state.db.conn, &params.owner_id)
        .await
        .map_err(AppCommandError::from)?;
    emit_event(
        &state.emitter,
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: params.owner_id.clone(),
            origin: "backend-schedule".into(),
            items: items.clone(),
        },
    );
    state.message_queue_drain.notify(params.owner_id);
    Ok(Json(items))
}

pub async fn message_queue_remove(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<RetryMessageQueueParams>,
) -> Result<Json<Vec<MessageQueueItem>>, AppCommandError> {
    message_queue_service::remove(&state.db.conn, &params.owner_id, &params.item_id)
        .await
        .map_err(AppCommandError::from)?;
    let items = message_queue_service::list(&state.db.conn, &params.owner_id)
        .await
        .map_err(AppCommandError::from)?;
    emit_event(
        &state.emitter,
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: params.owner_id.clone(),
            origin: "backend-remove".into(),
            items: items.clone(),
        },
    );
    state.message_queue_drain.notify(params.owner_id);
    Ok(Json(items))
}
