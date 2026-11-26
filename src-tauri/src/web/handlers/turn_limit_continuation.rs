//! HTTP mirror of `commands::turn_limit_continuation` settings endpoints.

use std::sync::Arc;

use axum::{extract::Extension, Json};
use serde::Deserialize;

use crate::app_error::AppCommandError;
use crate::app_state::AppState;
use crate::commands::turn_limit_continuation::{
    load_settings, set_settings_core, TurnLimitContinuationSettings,
};

pub async fn get_turn_limit_continuation_settings(
    Extension(state): Extension<Arc<AppState>>,
) -> Result<Json<TurnLimitContinuationSettings>, AppCommandError> {
    Ok(Json(load_settings(&state.db.conn).await))
}

#[derive(Deserialize)]
pub struct SetTurnLimitContinuationSettingsParams {
    pub settings: TurnLimitContinuationSettings,
}

pub async fn set_turn_limit_continuation_settings(
    Extension(state): Extension<Arc<AppState>>,
    Json(params): Json<SetTurnLimitContinuationSettingsParams>,
) -> Result<Json<TurnLimitContinuationSettings>, AppCommandError> {
    let saved = set_settings_core(
        &state.db.conn,
        &state.turn_limit_continuation_config,
        &state.emitter,
        params.settings,
    )
    .await?;
    Ok(Json(saved))
}
