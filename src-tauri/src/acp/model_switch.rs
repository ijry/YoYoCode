//! Explicit model selection journal and timeline projection.

use crate::acp::connection::SetterOutcome;
use crate::acp::types::{SessionConfigKindInfo, SessionConfigOptionInfo};
use crate::acp::{AcpEvent, SessionState};
use crate::db::{error::DbError, service::model_switch_service as journal};
use crate::models::{ContentBlock, MessageTurn, ModelSwitchInfo, ModelSwitchStatus, TurnRole};
use crate::web::event_bridge::{emit_with_state, EventEmitter};
use chrono::{DateTime, Utc};
use std::collections::{BTreeMap, HashSet};
use std::sync::Arc;
use tokio::sync::{Mutex, RwLock};

fn select<'a>(
    options: Option<&'a [SessionConfigOptionInfo]>,
    id: &str,
) -> Option<&'a crate::acp::types::SessionConfigSelectInfo> {
    options?
        .iter()
        .find(|option| option.id == id)
        .and_then(|option| match &option.kind {
            SessionConfigKindInfo::Select(select) => Some(select),
            _ => None,
        })
}

fn label(options: Option<&[SessionConfigOptionInfo]>, id: &str, value: &str) -> Option<String> {
    let select = select(options, id)?;
    select
        .options
        .iter()
        .chain(select.groups.iter().flat_map(|g| &g.options))
        .find(|item| item.value == value)
        .map(|item| item.name.clone())
        .filter(|name| !name.trim().is_empty())
}

/// Captured before the request: an optimistic legacy selector echo must never
/// be mistaken for either the previous model or confirmation of the new one.
pub(crate) struct SelectionBefore {
    previous_id: Option<String>,
    previous_name: Option<String>,
    options: Option<Vec<SessionConfigOptionInfo>>,
    after_turn_id: Option<String>,
    after_turn_started_at: Option<DateTime<Utc>>,
}

impl SelectionBefore {
    pub(crate) fn capture(state: &SessionState, id: &str) -> Option<Self> {
        // Before a conversation's first real prompt this only selects its
        // initial configuration; there is no conversation event to preserve.
        state.model_switch_after_turn_id.as_ref()?;
        let option = state
            .config_options
            .as_ref()
            .and_then(|opts| opts.iter().find(|opt| opt.id == id));
        if id != "model"
            && (select(state.config_options.as_deref(), "model").is_some()
                || option.is_none_or(|opt| opt.category.as_deref() != Some("model")))
        {
            return None;
        }
        if option.is_some_and(|opt| !matches!(opt.kind, SessionConfigKindInfo::Select(_))) {
            return None;
        }
        let previous_id = select(state.config_options.as_deref(), id)
            .map(|opt| opt.current_value.clone())
            .filter(|value| !value.is_empty());
        let previous_name = previous_id
            .as_deref()
            .and_then(|value| label(state.config_options.as_deref(), id, value));
        Some(Self {
            previous_id,
            previous_name,
            options: state.config_options.clone(),
            after_turn_id: state.model_switch_after_turn_id.clone(),
            after_turn_started_at: state.model_switch_after_turn_started_at,
        })
    }

    pub(crate) fn record(
        &self,
        state: &SessionState,
        config_id: &str,
        requested: &str,
        outcome: &SetterOutcome,
    ) -> Option<ModelSwitchInfo> {
        let (model, status) = match outcome {
            SetterOutcome::Confirmed { current } => {
                (current.as_str(), ModelSwitchStatus::Confirmed)
            }
            SetterOutcome::Accepted => (requested, ModelSwitchStatus::Accepted),
            _ => return None,
        };
        if model.is_empty() || self.previous_id.as_deref() == Some(model) {
            return None;
        }
        Some(ModelSwitchInfo {
            id: uuid::Uuid::new_v4().to_string(),
            connection_id: state.connection_id.clone(),
            session_id: state.external_id.clone(),
            sequence: state.model_switch_sequence + 1,
            config_id: config_id.into(),
            previous_model_id: self.previous_id.clone(),
            previous_model_name: self.previous_name.clone(),
            model_id: model.into(),
            model_name: label(state.config_options.as_deref(), config_id, model)
                .or_else(|| label(self.options.as_deref(), config_id, model)),
            requested_model_id: requested.into(),
            status,
            created_at: Utc::now(),
            after_turn_id: self.after_turn_id.clone(),
            after_turn_started_at: self.after_turn_started_at,
        })
    }
}

struct PendingWrite {
    state: Arc<RwLock<SessionState>>,
    emitter: EventEmitter,
    record: ModelSwitchInfo,
    conversation_id: Option<i32>,
    agent: crate::models::AgentType,
}

/// Shared by the manager and all connection loops. A failed write remains here
/// even if its caller or connection disappears. A worker retries without issuing
/// another agent setter; every attempt uses the original id and outcome.
#[derive(Clone)]
pub struct ModelSwitchJournal {
    db: sea_orm::DatabaseConnection,
    pending: Arc<Mutex<Vec<PendingWrite>>>,
    worker_running: Arc<std::sync::atomic::AtomicBool>,
}

impl std::fmt::Debug for ModelSwitchJournal {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("ModelSwitchJournal").finish_non_exhaustive()
    }
}

impl ModelSwitchJournal {
    pub fn new(db: sea_orm::DatabaseConnection) -> Self {
        Self {
            db,
            pending: Arc::new(Mutex::new(Vec::new())),
            worker_running: Arc::new(std::sync::atomic::AtomicBool::new(false)),
        }
    }

    pub(crate) async fn persist(
        &self,
        state: &Arc<RwLock<SessionState>>,
        emitter: &EventEmitter,
        record: ModelSwitchInfo,
    ) -> Result<(), DbError> {
        let (conversation_id, agent) = {
            let state = state.read().await;
            (state.conversation_id, state.agent_type)
        };
        self.pending.lock().await.push(PendingWrite {
            state: state.clone(),
            emitter: emitter.clone(),
            record,
            conversation_id,
            agent,
        });
        let result = self.flush().await;
        if result.is_err() {
            self.ensure_retry_worker();
        }
        result
    }

    pub(crate) async fn flush(&self) -> Result<(), DbError> {
        // Keep entries until both the durable write and publication complete.
        // This lock also orders concurrent setters on different connections.
        let mut pending = self.pending.lock().await;
        while let Some(write) = pending.first() {
            let session = write.record.session_id.as_deref();
            let owner = if let Some(session) = session {
                journal::owner_for_session(&self.db, write.agent, session)
                    .await?
                    .map(|o| o.0)
            } else {
                None
            };
            // Prefer the session's current owner: a fork may have moved the old
            // history to its sibling while this write was waiting for SQLite.
            let cid = owner.or(write.conversation_id);
            journal::append(&self.db, cid, write.agent, &write.record).await?;
            if let Some(cid) = cid {
                journal::bind_connection(&self.db, &write.record.connection_id, cid, session)
                    .await?;
            }
            let same_session = write.state.read().await.external_id == write.record.session_id;
            if same_session {
                emit_with_state(
                    &write.state,
                    &write.emitter,
                    AcpEvent::ModelSwitch {
                        record: write.record.clone(),
                    },
                )
                .await;
            }
            pending.remove(0);
        }
        Ok(())
    }

    fn ensure_retry_worker(&self) {
        use std::sync::atomic::Ordering;
        if self.worker_running.swap(true, Ordering::SeqCst) {
            return;
        }
        let journal = self.clone();
        tokio::spawn(async move {
            loop {
                tokio::time::sleep(std::time::Duration::from_secs(2)).await;
                if journal.flush().await.is_ok() {
                    journal.worker_running.store(false, Ordering::SeqCst);
                    // A concurrent enqueue may have observed the old running
                    // flag. Check after releasing it so that write cannot strand.
                    if !journal.pending.lock().await.is_empty() {
                        journal.ensure_retry_worker();
                    }
                    break;
                }
            }
        });
    }

    /// A resumed connection is useful before its first prompt. Bind its row
    /// before allowing a selector mutation so closing the tab cannot orphan it.
    pub(crate) async fn prelink(
        &self,
        state: &Arc<RwLock<SessionState>>,
        emitter: &EventEmitter,
    ) -> Result<(), DbError> {
        let (cid, agent, session, connection) = {
            let state = state.read().await;
            (
                state.conversation_id,
                state.agent_type,
                state.external_id.clone(),
                state.connection_id.clone(),
            )
        };
        let cid = if let Some(cid) = cid {
            journal::bind_connection(&self.db, &connection, cid, session.as_deref()).await?;
            Some(cid)
        } else if let Some(session) = session.as_deref() {
            if let Some((cid, folder)) =
                journal::owner_for_session(&self.db, agent, session).await?
            {
                journal::bind_connection(&self.db, &connection, cid, Some(session)).await?;
                emit_with_state(
                    state,
                    emitter,
                    AcpEvent::ConversationLinked {
                        conversation_id: cid,
                        folder_id: folder,
                        parent_conversation_id: None,
                        parent_tool_use_id: None,
                    },
                )
                .await;
                Some(cid)
            } else {
                None
            }
        } else {
            None
        };
        // A reopened conversation has no connection-local UserMessage yet.
        // Read actual native history, not the DB's possibly stale message_count.
        if state.read().await.model_switch_after_turn_id.is_none() {
            if let Some(cid) = cid {
                let (detail, _) =
                    crate::commands::conversations::get_folder_conversation_core(&self.db, cid)
                        .await
                        .map_err(|error| DbError::Validation(error.to_string()))?;
                if let Some(turn) = detail
                    .turns
                    .iter()
                    .rev()
                    .find(|turn| matches!(turn.role, TurnRole::User))
                {
                    let mut state = state.write().await;
                    if state.external_id == session && state.model_switch_after_turn_id.is_none() {
                        state.model_switch_after_turn_id = Some(turn.id.clone());
                        state.model_switch_after_turn_started_at = Some(turn.timestamp);
                    }
                }
            }
        }
        Ok(())
    }
}

/// Journal only the explicit dispatcher. Startup preference restoration never
/// enters this helper. Called from the connection loop, which owns the request
/// even if the HTTP/UI waiter has gone away.
pub(crate) async fn acknowledged(
    state: &Arc<RwLock<SessionState>>,
    emitter: &EventEmitter,
    before: Option<SelectionBefore>,
    config_id: &str,
    requested: &str,
    outcome: &SetterOutcome,
) -> Result<(), DbError> {
    let Some(before) = before else {
        return Ok(());
    };
    let (record, journal) = {
        let mut state = state.write().await;
        let Some(record) = before.record(&state, config_id, requested, outcome) else {
            return Ok(());
        };
        state.model_switch_sequence = record.sequence;
        (record, state.model_switch_journal.clone())
    };
    if let Some(journal) = journal {
        journal.persist(state, emitter, record).await?;
    }
    Ok(())
}

/// Slot after a complete user round. Native transcript order is authoritative;
/// timestamps only locate a round when the live prompt id is not persisted.
fn slot(turns: &[MessageTurn], record: &ModelSwitchInfo) -> usize {
    let anchor = record
        .after_turn_id
        .as_ref()
        .and_then(|id| {
            turns
                .iter()
                .position(|turn| &turn.id == id || turn.agent_message_id.as_ref() == Some(id))
        })
        .or_else(|| {
            record.after_turn_started_at.and_then(|at| {
                turns.iter().position(|turn| {
                    matches!(turn.role, TurnRole::User)
                        && turn.timestamp >= at
                        && turn.timestamp <= record.created_at
                })
            })
        });
    if let Some(anchor) = anchor {
        return turns
            .iter()
            .enumerate()
            .skip(anchor + 1)
            .find(|(_, turn)| matches!(turn.role, TurnRole::User))
            .map_or(turns.len(), |(index, _)| index);
    }
    turns
        .iter()
        .position(|turn| matches!(turn.role, TurnRole::User) && turn.timestamp > record.created_at)
        .unwrap_or(turns.len())
}

pub(crate) fn fork_record_ids(turns: &[MessageTurn], boundary: Option<&str>) -> Vec<String> {
    let end = boundary
        .and_then(|id| turns.iter().position(|turn| turn.id == id))
        .map_or(turns.len(), |index| index + 1);
    turns[..end]
        .iter()
        .flat_map(|turn| {
            turn.blocks.iter().filter_map(|block| match block {
                ContentBlock::ModelSwitch { record } => Some(record.id.clone()),
                _ => None,
            })
        })
        .collect()
}

pub fn inject(turns: &mut Vec<MessageTurn>, records: &[ModelSwitchInfo]) {
    let mut records: Vec<_> = records
        .iter()
        .cloned()
        .chain(turns.iter().flat_map(|turn| {
            turn.blocks.iter().filter_map(|block| match block {
                ContentBlock::ModelSwitch { record } => Some(record.clone()),
                _ => None,
            })
        }))
        .collect();
    turns.retain(|turn| {
        !turn
            .blocks
            .iter()
            .any(|block| matches!(block, ContentBlock::ModelSwitch { .. }))
    });
    records.sort_by(|a, b| {
        (a.created_at, &a.connection_id, a.sequence, &a.id).cmp(&(
            b.created_at,
            &b.connection_id,
            b.sequence,
            &b.id,
        ))
    });
    let mut seen = HashSet::new();
    let mut slots: BTreeMap<usize, Vec<ModelSwitchInfo>> = BTreeMap::new();
    for record in records {
        if seen.insert(record.id.clone()) {
            slots.entry(slot(turns, &record)).or_default().push(record);
        }
    }
    for (index, records) in slots.into_iter().rev() {
        turns.splice(
            index..index,
            records.into_iter().map(|record| MessageTurn {
                id: format!("model-switch-{}", record.id),
                role: TurnRole::System,
                timestamp: record.created_at,
                blocks: vec![ContentBlock::ModelSwitch { record }],
                usage: None,
                duration_ms: None,
                model: None,
                completed_at: None,
                agent_message_id: None,
            }),
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::acp::connection::SetterOutcome;
    use crate::acp::types::{
        SessionConfigKindInfo, SessionConfigOptionInfo, SessionConfigSelectInfo,
        SessionConfigSelectOptionInfo,
    };
    use crate::db::{service::model_switch_service as journal, test_helpers};
    use crate::models::{
        AgentType, ContentBlock, MessageTurn, ModelSwitchInfo, ModelSwitchStatus, TurnRole,
    };
    use chrono::{TimeZone, Utc};

    fn record(id: &str, sequence: u64) -> ModelSwitchInfo {
        ModelSwitchInfo {
            id: id.into(),
            connection_id: "connection".into(),
            session_id: Some("session".into()),
            sequence,
            config_id: "model".into(),
            previous_model_id: Some("vendor/A".into()),
            previous_model_name: None,
            model_id: "vendor/B".into(),
            model_name: Some("B".into()),
            requested_model_id: "vendor/B".into(),
            status: ModelSwitchStatus::Confirmed,
            created_at: Utc.timestamp_opt(30, 0).unwrap(),
            after_turn_id: Some("user-1".into()),
            after_turn_started_at: Some(Utc.timestamp_opt(10, 0).unwrap()),
        }
    }

    fn option(current: &str) -> SessionConfigOptionInfo {
        SessionConfigOptionInfo {
            id: "provider-model".into(),
            name: "Model".into(),
            description: None,
            category: Some("model".into()),
            recommended_value: None,
            kind: SessionConfigKindInfo::Select(SessionConfigSelectInfo {
                current_value: current.into(),
                groups: vec![],
                options: vec![SessionConfigSelectOptionInfo {
                    value: "vendor/B".into(),
                    name: "Model B".into(),
                    description: None,
                }],
            }),
        }
    }

    fn state() -> SessionState {
        let mut state = SessionState::new(
            "connection".into(),
            AgentType::Codex,
            None,
            "main".into(),
            None,
        );
        state.external_id = Some("session".into());
        state.config_options = Some(vec![option("vendor/A")]);
        state.model_switch_after_turn_id = Some("user-1".into());
        state.model_switch_after_turn_started_at = Some(Utc.timestamp_opt(10, 0).unwrap());
        state
    }

    fn turn(id: &str, role: TurnRole, at: i64) -> MessageTurn {
        MessageTurn {
            id: id.into(),
            role,
            blocks: vec![],
            timestamp: Utc.timestamp_opt(at, 0).unwrap(),
            usage: None,
            duration_ms: None,
            model: None,
            completed_at: None,
            agent_message_id: None,
        }
    }

    #[test]
    fn model_switch_canonical_selector_excludes_misclassified_effort() {
        let mut state = state();
        let mut canonical = option("vendor/A");
        canonical.id = "model".into();
        let mut effort = option("medium");
        effort.id = "reasoning_effort".into();
        state.config_options = Some(vec![canonical, effort]);
        assert!(SelectionBefore::capture(&state, "reasoning_effort").is_none());
        assert!(SelectionBefore::capture(&state, "model").is_some());
        state.config_options = Some(vec![option("vendor/A")]);
        assert!(SelectionBefore::capture(&state, "provider-model").is_some());
    }

    #[test]
    fn model_switch_before_first_prompt_is_initial_configuration() {
        let mut fresh = state();
        fresh.model_switch_after_turn_id = None;
        fresh.model_switch_after_turn_started_at = None;
        assert!(SelectionBefore::capture(&fresh, "provider-model").is_none());
    }

    #[test]
    fn model_switch_outcomes_preserve_ids_labels_and_accepted_uncertainty() {
        let state = state();
        let before = SelectionBefore::capture(&state, "provider-model").unwrap();
        let confirmed = before
            .record(
                &state,
                "provider-model",
                "vendor/B",
                &SetterOutcome::Confirmed {
                    current: "vendor/B".into(),
                },
            )
            .unwrap();
        assert_eq!(confirmed.previous_model_id.as_deref(), Some("vendor/A"));
        assert_eq!(confirmed.model_id, "vendor/B");
        assert_eq!(confirmed.model_name.as_deref(), Some("Model B"));
        assert_eq!(confirmed.status, ModelSwitchStatus::Confirmed);
        let accepted = before
            .record(
                &state,
                "provider-model",
                "vendor/B",
                &SetterOutcome::Accepted,
            )
            .unwrap();
        assert_eq!(accepted.status, ModelSwitchStatus::Accepted);
        assert!(before
            .record(
                &state,
                "provider-model",
                "vendor/B",
                &SetterOutcome::Confirmed {
                    current: "vendor/A".into()
                }
            )
            .is_none());
        assert!(before
            .record(
                &state,
                "provider-model",
                "vendor/B",
                &SetterOutcome::NotExecuted
            )
            .is_none());
        assert!(before
            .record(
                &state,
                "provider-model",
                "vendor/B",
                &SetterOutcome::Refused {
                    message: "no".into()
                }
            )
            .is_none());
        assert!(before
            .record(
                &state,
                "provider-model",
                "vendor/A",
                &SetterOutcome::Accepted
            )
            .is_none());
        assert!(SelectionBefore::capture(&state, "effort").is_none());
    }

    #[test]
    fn model_switch_projection_follows_complete_round_without_sorting_native_turns() {
        let mut turns = vec![
            turn("user-1", TurnRole::User, 10),
            turn("part-1", TurnRole::Assistant, 50),
            turn("part-2", TurnRole::Assistant, 20),
            turn("user-2", TurnRole::User, 60),
        ];
        let a = record("first", 1);
        let mut b = record("second", 2);
        b.previous_model_id = Some("vendor/B".into());
        b.model_id = "vendor/A".into();
        b.requested_model_id = "vendor/A".into();
        inject(&mut turns, &[b.clone(), a.clone(), a.clone()]);
        assert_eq!(
            turns.iter().map(|t| t.id.as_str()).collect::<Vec<_>>(),
            [
                "user-1",
                "part-1",
                "part-2",
                "model-switch-first",
                "model-switch-second",
                "user-2"
            ]
        );
        assert!(matches!(turns[3].role, TurnRole::System));
        assert!(turns[3].usage.is_none() && turns[3].model.is_none());
        inject(&mut turns, &[a, b]);
        assert_eq!(turns.len(), 6);
        assert_eq!(crate::parsers::message_turn_count(&turns), 4);
    }

    #[test]
    fn model_switch_anchor_survives_completion_and_snapshot_replay() {
        let mut state = state();
        state.apply_event(&AcpEvent::UserMessage {
            message_id: "optimistic-user".into(),
            blocks: vec![],
        });
        let started = state.pending_user_message_started_at;
        state.apply_event(&AcpEvent::TurnComplete {
            session_id: "session".into(),
            stop_reason: "end_turn".into(),
            agent_type: "codex".into(),
        });
        let before = SelectionBefore::capture(&state, "provider-model").unwrap();
        let record = before
            .record(
                &state,
                "provider-model",
                "vendor/B",
                &SetterOutcome::Accepted,
            )
            .unwrap();
        assert_eq!(record.after_turn_id.as_deref(), Some("optimistic-user"));
        assert_eq!(record.after_turn_started_at, started);
        state.apply_event(&AcpEvent::ModelSwitch {
            record: record.clone(),
        });
        state.apply_event(&AcpEvent::ModelSwitch {
            record: record.clone(),
        });
        assert_eq!(state.to_snapshot().model_switches, vec![record.clone()]);
        let wire = serde_json::to_value(ContentBlock::ModelSwitch { record }).unwrap();
        assert_eq!(wire["type"], "model_switch");
        assert!(wire["record"]["previous_model_name"].is_null());
        assert_eq!(wire["record"].as_object().unwrap().len(), 14);
    }

    #[test]
    fn model_switch_time_fallback_uses_round_boundaries_and_never_sorts() {
        let mut turns = vec![
            turn("user-1", TurnRole::User, 10),
            turn("part-1", TurnRole::Assistant, 50),
            turn("part-2", TurnRole::Assistant, 20),
            turn("user-2", TurnRole::User, 60),
        ];
        let mut a = record("a", 1);
        a.after_turn_id = Some("optimistic-missing".into());
        inject(&mut turns, &[a]);
        assert_eq!(turns[3].id, "model-switch-a");
        assert_eq!(turns[4].id, "user-2");
    }

    #[tokio::test]
    async fn model_switch_failed_write_is_retained_and_retried_without_a_second_selection() {
        use sea_orm::ConnectionTrait;
        let db = test_helpers::fresh_in_memory_db().await;
        db.conn
            .execute_unprepared("ALTER TABLE model_switch RENAME TO model_switch_unavailable")
            .await
            .unwrap();
        let journal = ModelSwitchJournal::new(db.conn.clone());
        let state = Arc::new(RwLock::new(state()));
        let a = record("retry", 1);
        assert!(journal
            .persist(&state, &EventEmitter::Noop, a.clone())
            .await
            .is_err());
        assert!(state.read().await.to_snapshot().model_switches.is_empty());
        db.conn
            .execute_unprepared("ALTER TABLE model_switch_unavailable RENAME TO model_switch")
            .await
            .unwrap();
        journal.flush().await.unwrap();
        assert_eq!(state.read().await.to_snapshot().model_switches, vec![a]);
        journal.flush().await.unwrap();
        assert_eq!(state.read().await.to_snapshot().model_switches.len(), 1);
    }

    #[test]
    fn model_switch_fork_boundary_excludes_later_events_and_accepts_tail_metadata() {
        let mut turns = vec![
            turn("user-1", TurnRole::User, 10),
            turn("answer-1", TurnRole::Assistant, 20),
            turn("user-2", TurnRole::User, 60),
        ];
        inject(&mut turns, &[record("between", 1)]);
        assert!(fork_record_ids(&turns, Some("answer-1")).is_empty());
        assert_eq!(fork_record_ids(&turns, None), vec!["between"]);
    }

    #[tokio::test]
    async fn model_switch_session_split_moves_history_to_preserved_row() {
        let db = test_helpers::fresh_in_memory_db().await;
        let folder = test_helpers::seed_folder(&db, "/tmp/model-switch-split").await;
        let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
        crate::db::service::conversation_service::bind_external_id(&db.conn, cid, "session", &[])
            .await
            .unwrap();
        journal::append(&db.conn, Some(cid), AgentType::Codex, &record("old", 1))
            .await
            .unwrap();
        let preserved = crate::db::service::conversation_service::bind_external_id(
            &db.conn,
            cid,
            "new-session",
            &[],
        )
        .await
        .unwrap()
        .unwrap();
        assert_eq!(
            journal::list_by_conversation(&db.conn, preserved)
                .await
                .unwrap()
                .len(),
            1
        );
        assert!(journal::list_by_conversation(&db.conn, cid)
            .await
            .unwrap()
            .is_empty());
    }

    #[tokio::test]
    async fn model_switch_history_is_injected_before_paging_without_reply_count_inflation() {
        let db = test_helpers::fresh_in_memory_db().await;
        let folder = test_helpers::seed_folder(&db, "/tmp/model-switch-page").await;
        let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
        journal::append(&db.conn, Some(cid), AgentType::Codex, &record("one", 1))
            .await
            .unwrap();
        journal::append(&db.conn, Some(cid), AgentType::Codex, &record("two", 2))
            .await
            .unwrap();
        let (detail, _) =
            crate::commands::conversations::get_folder_conversation_core(&db.conn, cid)
                .await
                .unwrap();
        assert_eq!(detail.turns.len(), 2);
        assert_eq!(detail.summary.message_count, 0);
        let page =
            crate::commands::conversations::get_folder_conversation_turns_core(&db.conn, cid, 2, 1)
                .await
                .unwrap();
        assert_eq!(page.turns_total, 2);
        // Pages align to a whole user round. With metadata only, that is
        // the start of the timeline, even when the requested limit is one.
        assert_eq!(page.turns_offset, 0);
        assert_eq!(page.turns[1].id, "model-switch-two");
        assert_eq!(page.assistant_turns_before_offset, 0);
        assert_eq!(page.user_turns_before_offset, 0);
    }

    #[tokio::test]
    async fn model_switch_reopened_history_is_linked_before_any_new_prompt() {
        use crate::db::service::{agent_setting_service, conversation_service};
        let db = test_helpers::fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("projects").join("-switch-history");
        std::fs::create_dir_all(&project).unwrap();
        let session = "acac1234-1234-4321-abcd-123456789abc";
        std::fs::write(project.join(format!("{session}.jsonl")), concat!(
            "{\"type\":\"user\",\"timestamp\":\"2026-10-14T01:00:00.000Z\",\"uuid\":\"u1\",\"cwd\":\"/tmp/switch-history\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"hello\"}]}}\n",
            "{\"type\":\"assistant\",\"timestamp\":\"2026-10-14T01:00:05.000Z\",\"uuid\":\"a1\",\"parentUuid\":\"u1\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"hi\"}]}}\n"
        )).unwrap();
        agent_setting_service::ensure_defaults(
            &db.conn,
            &[agent_setting_service::AgentDefaultInput {
                agent_type: AgentType::ClaudeCode,
                registry_id: "claude-code".into(),
                default_sort_order: 0,
            }],
        )
        .await
        .unwrap();
        agent_setting_service::update(
            &db.conn,
            AgentType::ClaudeCode,
            agent_setting_service::AgentSettingsUpdate {
                enabled: true,
                env_json: Some(serde_json::json!({"CLAUDE_CONFIG_DIR": dir.path()}).to_string()),
                model_provider_id: None,
            },
        )
        .await
        .unwrap();
        let folder = test_helpers::seed_folder(&db, "/tmp/switch-history").await;
        let cid = test_helpers::seed_conversation(&db, folder, AgentType::ClaudeCode).await;
        conversation_service::bind_external_id(&db.conn, cid, session, &[])
            .await
            .unwrap();
        let mut fresh = SessionState::new(
            "reopened".into(),
            AgentType::ClaudeCode,
            None,
            "main".into(),
            None,
        );
        fresh.external_id = Some(session.into());
        fresh.config_options = Some(vec![option("vendor/A")]);
        let state = Arc::new(RwLock::new(fresh));
        let journal = ModelSwitchJournal::new(db.conn.clone());
        journal.prelink(&state, &EventEmitter::Noop).await.unwrap();
        let captured = {
            let state = state.read().await;
            assert_eq!(state.conversation_id, Some(cid));
            assert!(state.pending_user_message.is_none());
            SelectionBefore::capture(&state, "provider-model")
                .unwrap()
                .record(
                    &state,
                    "provider-model",
                    "vendor/B",
                    &SetterOutcome::Accepted,
                )
                .unwrap()
        };
        journal
            .persist(&state, &EventEmitter::Noop, captured.clone())
            .await
            .unwrap();
        assert_eq!(
            journal::list_by_conversation(&db.conn, cid).await.unwrap(),
            vec![captured]
        );
    }

    #[tokio::test]
    async fn model_switch_journal_replays_without_overwriting_bound_records() {
        let db = test_helpers::fresh_in_memory_db().await;
        let folder = test_helpers::seed_folder(&db, "/tmp/model-switch").await;
        let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
        let a = record("a", 1);
        let mut b = record("b", 2);
        b.model_id = "vendor/A".into();
        journal::append(&db.conn, None, AgentType::Codex, &a)
            .await
            .unwrap();
        journal::append(&db.conn, None, AgentType::Codex, &b)
            .await
            .unwrap();
        journal::append(&db.conn, None, AgentType::Codex, &a)
            .await
            .unwrap();
        journal::bind_connection(&db.conn, "connection", cid, Some("session"))
            .await
            .unwrap();
        // A late retry of an unbound insert cannot detach the bound record.
        journal::append(&db.conn, None, AgentType::Codex, &a)
            .await
            .unwrap();
        assert_eq!(
            journal::list_by_conversation(&db.conn, cid).await.unwrap(),
            vec![a, b]
        );
    }
}
