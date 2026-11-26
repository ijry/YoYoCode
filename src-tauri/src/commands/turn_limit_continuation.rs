//! Persistence and command surface for the "what to do when the provider hits
//! the session turn limit" setting. Mirrors `commands::feedback`: one dotted
//! `app_metadata` key, a shared runtime handle, and the same set / apply /
//! broadcast chain for the Tauri command and the web handler.

use sea_orm::DatabaseConnection;
use serde::{Deserialize, Serialize};

use crate::acp::turn_limit_continuation::{
    TurnLimitContinuationConfig, TurnLimitContinuationMode, TurnLimitContinuationRuntimeConfig,
};
use crate::app_error::AppCommandError;
use crate::db::service::app_metadata_service;
use crate::web::event_bridge::{
    emit_event, EventEmitter, TURN_LIMIT_CONTINUATION_SETTINGS_CHANGED_EVENT,
};

/// The current three-way setting.
pub const KEY_AUTO_CONTINUE_ON_TURN_LIMIT_MODE: &str =
    "turn_limit_continuation.auto_continue.mode";
/// Read-only migration source from the earlier on/off toggle.
pub const KEY_AUTO_CONTINUE_ON_TURN_LIMIT: &str =
    "turn_limit_continuation.auto_continue.enabled";

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TurnLimitContinuationSettings {
    pub mode: TurnLimitContinuationMode,
}

impl Default for TurnLimitContinuationSettings {
    fn default() -> Self {
        Self {
            mode: TurnLimitContinuationMode::NewSession,
        }
    }
}

impl TurnLimitContinuationSettings {
    fn into_config(self) -> TurnLimitContinuationConfig {
        TurnLimitContinuationConfig {
            mode: self.mode,
        }
    }
}

/// Read the persisted key from `app_metadata`. The old boolean key is still
/// read once for users upgrading from the on/off toggle (`true` → new session,
/// `false` → off); a valid new key always wins. Corrupt or missing values fall
/// back to the default instead of failing — corrupt persistence is treated as
/// "no preference yet" (matches the feedback/delegation settings).
pub async fn load_settings(conn: &DatabaseConnection) -> TurnLimitContinuationSettings {
    if let Ok(Some(raw)) =
        app_metadata_service::get_value(conn, KEY_AUTO_CONTINUE_ON_TURN_LIMIT_MODE).await
    {
        if let Ok(settings) = serde_json::from_str::<TurnLimitContinuationSettings>(&raw) {
            return settings;
        }
    }
    if let Ok(Some(raw)) =
        app_metadata_service::get_value(conn, KEY_AUTO_CONTINUE_ON_TURN_LIMIT).await
    {
        if let Ok(enabled) = raw.parse::<bool>() {
            return TurnLimitContinuationSettings {
                mode: if enabled {
                    TurnLimitContinuationMode::NewSession
                } else {
                    TurnLimitContinuationMode::Off
                },
            };
        }
    }
    TurnLimitContinuationSettings::default()
}

/// Push the persisted value onto the shared runtime handle. Idempotent — safe
/// on startup, after a save, or after any external write to `app_metadata`.
pub async fn apply_persisted_config(
    conn: &DatabaseConnection,
    config: &TurnLimitContinuationRuntimeConfig,
) {
    let settings = load_settings(conn).await;
    config.set(settings.into_config()).await;
}

/// Persist + apply + broadcast, shared by the Tauri command and HTTP handler.
/// The legacy key is deleted on save so a later config snapshot cannot bring
/// the old on/off value back.
pub async fn set_settings_core(
    conn: &DatabaseConnection,
    config: &TurnLimitContinuationRuntimeConfig,
    emitter: &EventEmitter,
    desired: TurnLimitContinuationSettings,
) -> Result<TurnLimitContinuationSettings, AppCommandError> {
    let serialized = serde_json::to_string(&desired)
        .map_err(|e| AppCommandError::configuration_invalid(e.to_string()))?;
    app_metadata_service::upsert_value(
        conn,
        KEY_AUTO_CONTINUE_ON_TURN_LIMIT_MODE,
        &serialized,
    )
    .await
    .map_err(AppCommandError::from)?;
    let _ = app_metadata_service::delete_value(conn, KEY_AUTO_CONTINUE_ON_TURN_LIMIT).await;
    config.set(desired.clone().into_config()).await;
    emit_event(
        emitter,
        TURN_LIMIT_CONTINUATION_SETTINGS_CHANGED_EVENT,
        &desired,
    );
    Ok(desired)
}

#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn get_turn_limit_continuation_settings(
    #[cfg(feature = "tauri-runtime")] db: tauri::State<'_, crate::db::AppDatabase>,
) -> Result<TurnLimitContinuationSettings, AppCommandError> {
    #[cfg(feature = "tauri-runtime")]
    {
        Ok(load_settings(&db.conn).await)
    }
    #[cfg(not(feature = "tauri-runtime"))]
    {
        Err(AppCommandError::configuration_invalid("tauri-only command"))
    }
}

#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn set_turn_limit_continuation_settings(
    #[cfg(feature = "tauri-runtime")] app: tauri::AppHandle,
    #[cfg(feature = "tauri-runtime")] db: tauri::State<'_, crate::db::AppDatabase>,
    #[cfg(feature = "tauri-runtime")] config: tauri::State<
        '_,
        TurnLimitContinuationRuntimeConfig,
    >,
    settings: TurnLimitContinuationSettings,
) -> Result<TurnLimitContinuationSettings, AppCommandError> {
    #[cfg(feature = "tauri-runtime")]
    {
        let emitter = EventEmitter::from_app(&app);
        set_settings_core(&db.conn, &config, &emitter, settings).await
    }
    #[cfg(not(feature = "tauri-runtime"))]
    {
        let _ = settings;
        Err(AppCommandError::configuration_invalid("tauri-only command"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn default_is_new_session_and_round_trips_through_app_metadata() {
        let db = crate::db::test_helpers::fresh_in_memory_db().await;
        let config = TurnLimitContinuationRuntimeConfig::new();
        assert_eq!(load_settings(&db.conn).await.mode, TurnLimitContinuationMode::NewSession);
        apply_persisted_config(&db.conn, &config).await;
        assert_eq!(config.mode().await, TurnLimitContinuationMode::NewSession);

        let saved = set_settings_core(
            &db.conn,
            &config,
            &EventEmitter::Noop,
            TurnLimitContinuationSettings {
                mode: TurnLimitContinuationMode::Compact,
            },
        )
        .await
        .unwrap();
        assert_eq!(saved.mode, TurnLimitContinuationMode::Compact);
        assert_eq!(load_settings(&db.conn).await.mode, TurnLimitContinuationMode::Compact);
        assert_eq!(config.mode().await, TurnLimitContinuationMode::Compact);
        assert!(app_metadata_service::get_value(&db.conn, KEY_AUTO_CONTINUE_ON_TURN_LIMIT)
            .await
            .unwrap()
            .is_none());
    }

    #[tokio::test]
    async fn legacy_boolean_key_migrates_and_new_key_wins() {
        let db = crate::db::test_helpers::fresh_in_memory_db().await;
        app_metadata_service::upsert_value(&db.conn, KEY_AUTO_CONTINUE_ON_TURN_LIMIT, "false")
            .await
            .unwrap();
        assert_eq!(load_settings(&db.conn).await.mode, TurnLimitContinuationMode::Off);
        app_metadata_service::upsert_value(&db.conn, KEY_AUTO_CONTINUE_ON_TURN_LIMIT, "true")
            .await
            .unwrap();
        assert_eq!(load_settings(&db.conn).await.mode, TurnLimitContinuationMode::NewSession);

        app_metadata_service::upsert_value(
            &db.conn,
            KEY_AUTO_CONTINUE_ON_TURN_LIMIT_MODE,
            r#"{"mode":"compact"}"#,
        )
        .await
        .unwrap();
        assert_eq!(load_settings(&db.conn).await.mode, TurnLimitContinuationMode::Compact);
    }
}
