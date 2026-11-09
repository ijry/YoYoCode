use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ModelSwitchStatus {
    Confirmed,
    Accepted,
}

/// An explicit selection and what its correlated agent response proved.
/// Nullable fields deliberately serialize as null: this is the shared wire contract.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ModelSwitchInfo {
    pub id: String,
    pub connection_id: String,
    pub session_id: Option<String>,
    pub sequence: u64,
    pub config_id: String,
    pub previous_model_id: Option<String>,
    pub previous_model_name: Option<String>,
    pub model_id: String,
    pub model_name: Option<String>,
    pub requested_model_id: String,
    pub status: ModelSwitchStatus,
    pub created_at: DateTime<Utc>,
    pub after_turn_id: Option<String>,
    pub after_turn_started_at: Option<DateTime<Utc>>,
}
