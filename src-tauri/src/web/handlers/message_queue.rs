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
    Json(serde_json::json!({ "backend_drain": true, "pause_recovery": true }))
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
