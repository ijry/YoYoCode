//! Backend-owned continuation when a provider rejects a turn for exceeding
//! the session's model turn limit.
//!
//! The provider error is a JSON envelope whose `error.code` is
//! `session_turn_limit_exceeded`. `connection.rs` extracts that code onto the
//! `AcpEvent::Error` and hands a [`ContinuationRequest`] to this module through
//! [`ContinuationInjection`]. The worker here does what the UI's "continue in
//! a new session" button does, but without a client: settle the old row,
//! create a fresh session with the OLD selector state (so the model is the
//! same), and auto-send the continuation prompt. Both frontends only render
//! the events this flow already emits.

use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use tokio::sync::{broadcast, mpsc, RwLock};

use crate::acp::error::AcpError;
use crate::acp::manager::ConnectionManager;
use crate::acp::session_state::SessionState;
use crate::acp::types::{AcpEvent, ConnectionStatus, EventEnvelope, PromptInputBlock};
use crate::acp::InternalEventBus;
use crate::db::entities::conversation::ConversationStatus;
use crate::db::service::conversation_service;
use crate::db::AppDatabase;
use crate::models::agent::AgentType;
use crate::models::ConversationSelection;
use crate::web::event_bridge::{
    emit_event, ConversationChange, EventEmitter, CONVERSATION_CHANGED_EVENT,
};

/// The provider code this module reacts to.
pub const TURN_LIMIT_EXCEEDED_CODE: &str = "session_turn_limit_exceeded";

/// Same wording as the UI's `continuePromptText` (zh-CN) and mobile's
/// `CONTINUE_PROMPT_SUFFIX`: the backend is display-agnostic and the user's
/// locale is not available here, so this is the canonical continuation text.
const CONTINUATION_PROMPT: &str = "请继续上面会话的工作。";

/// What to do when a provider rejects a turn for hitting the session's turn
/// limit. `Compact` keeps the same session and auto-sends the continuation
/// after the compaction turn; it is offered as unverified because compaction
/// has no mechanism here to reset a provider-side turn counter.
#[derive(
    Debug, Clone, Copy, Default, PartialEq, Eq, serde::Serialize, serde::Deserialize,
)]
#[serde(rename_all = "snake_case")]
pub enum TurnLimitContinuationMode {
    Off,
    #[default]
    NewSession,
    Compact,
}

/// Hot-swappable recovery mode. Default to a new session: hitting the limit
/// otherwise leaves the user with a dead session and no automatic recovery.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct TurnLimitContinuationConfig {
    pub mode: TurnLimitContinuationMode,
}

/// Shared handle read by the worker on every request, so a settings save
/// reaches the next error without restarting a session.
#[derive(Clone, Default)]
pub struct TurnLimitContinuationRuntimeConfig {
    inner: Arc<RwLock<TurnLimitContinuationConfig>>,
}

impl TurnLimitContinuationRuntimeConfig {
    pub fn new() -> Self {
        Self::default()
    }

    pub async fn snapshot(&self) -> TurnLimitContinuationConfig {
        self.inner.read().await.clone()
    }

    pub async fn set(&self, config: TurnLimitContinuationConfig) {
        *self.inner.write().await = config;
    }

    pub async fn mode(&self) -> TurnLimitContinuationMode {
        self.inner.read().await.mode
    }
}

/// Everything the manager-side worker needs to continue one failed session.
#[derive(Clone)]
pub struct ContinuationRequest {
    pub conversation_id: i32,
    pub connection_id: String,
    pub agent_type: AgentType,
    pub working_dir: String,
    pub folder_id: i32,
    pub owner_window_label: String,
    /// The OLD session's live selector state, so the new session runs on the
    /// same model. Passed as one-shot client values to the new session.
    pub selection: ConversationSelection,
    pub emitter: EventEmitter,
}

/// Injected into every ACP connection so its error path can hand off to the
/// worker without holding a `ConnectionManager` or a database handle.
#[derive(Clone, Debug)]
pub struct ContinuationInjection {
    tx: mpsc::UnboundedSender<ContinuationRequest>,
}

impl ContinuationInjection {
    pub fn new(tx: mpsc::UnboundedSender<ContinuationRequest>) -> Self {
        Self { tx }
    }

    /// Never block the error path: the event must still be emitted and the
    /// turn recorded even when the worker is gone.
    pub fn try_send(&self, request: ContinuationRequest) {
        if let Err(e) = self.tx.send(request) {
            tracing::warn!("[turn-limit-continuation] worker unavailable: {e}");
        }
    }
}

/// The prompt text the new session receives: the old-session link is the same
/// text the UI composer sends, and the injected `get_session_info` tool is
/// what lets the agent read the old session's work from it.
pub fn build_continuation_prompt(title: &str, conversation_id: i32) -> String {
    format!("[{title}](codeg://session/{conversation_id}) {CONTINUATION_PROMPT}")
}

/// Consume continuation requests for the lifetime of the process.
pub async fn worker(
    mut rx: mpsc::UnboundedReceiver<ContinuationRequest>,
    db: AppDatabase,
    manager: ConnectionManager,
    data_dir: PathBuf,
    bus: Arc<InternalEventBus>,
    config: TurnLimitContinuationRuntimeConfig,
) {
    // One continuation per failed conversation: a provider may emit the same
    // rejection more than once for one turn. Never removed — a compaction that
    // fails to reset the provider counter must not loop forever.
    let mut handled = HashSet::<i32>::new();
    while let Some(request) = rx.recv().await {
        let mode = config.mode().await;
        if mode == TurnLimitContinuationMode::Off {
            tracing::debug!(
                "[turn-limit-continuation] off, skipping conversation {}",
                request.conversation_id
            );
            continue;
        }
        if !handled.insert(request.conversation_id) {
            tracing::debug!(
                "[turn-limit-continuation] already recovered conversation {}",
                request.conversation_id
            );
            continue;
        }
        let result = match mode {
            TurnLimitContinuationMode::NewSession => {
                handle_continuation(&request, &db, &manager, &data_dir).await
            }
            TurnLimitContinuationMode::Compact => {
                handle_compact_continuation(&request, &db, &manager, &data_dir, &bus).await
            }
            TurnLimitContinuationMode::Off => unreachable!("handled above"),
        };
        if let Err(e) = result {
            tracing::warn!(
                "[turn-limit-continuation] failed for conversation {}: {e}",
                request.conversation_id
            );
        }
    }
}

enum CompactTurnOutcome {
    Completed,
    Failed,
}

/// Wait for the compaction turn on `connection_id` to settle. Only events
/// strictly newer than `watermark` count — the rejected turn that triggered
/// this request publishes its own `TurnComplete` AFTER the handoff, and that
/// one must not be mistaken for the compaction's ending.
async fn wait_for_compaction_turn(
    rx: &mut broadcast::Receiver<Arc<EventEnvelope>>,
    state: &Arc<RwLock<SessionState>>,
    connection_id: &str,
    watermark: u64,
) -> CompactTurnOutcome {
    const LIVENESS_TICK: Duration = Duration::from_millis(500);
    const HARD_DEADLINE: Duration = Duration::from_secs(600);

    // The state fallback must not fire on the burst of events right after the
    // send (before the turn is admitted): seq rises while turn_in_flight is
    // still false. Require a turn we actually saw as in flight first.
    let mut saw_in_flight = false;
    tokio::time::timeout(HARD_DEADLINE, async {
        loop {
            tokio::select! {
                received = rx.recv() => match received {
                    Ok(env) if env.connection_id == connection_id && env.seq > watermark => {
                        match &env.payload {
                            AcpEvent::TurnComplete { stop_reason, .. } => {
                                return if stop_reason == "end_turn" || stop_reason.is_empty() {
                                    CompactTurnOutcome::Completed
                                } else {
                                    CompactTurnOutcome::Failed
                                };
                            }
                            AcpEvent::Error { terminal: true, .. } => {
                                return CompactTurnOutcome::Failed;
                            }
                            AcpEvent::StatusChanged {
                                status: ConnectionStatus::Disconnected | ConnectionStatus::Error,
                            } => return CompactTurnOutcome::Failed,
                            _ => {}
                        }
                    }
                    Ok(_) => {}
                    Err(broadcast::error::RecvError::Lagged(_)) => {
                        // A lagging receiver may have dropped the TurnComplete;
                        // the live state still records whether the turn ended.
                        let s = state.read().await;
                        if s.turn_in_flight {
                            saw_in_flight = true;
                        } else if saw_in_flight && s.event_seq > watermark {
                            return CompactTurnOutcome::Completed;
                        }
                    }
                    Err(broadcast::error::RecvError::Closed) => {
                        return CompactTurnOutcome::Failed;
                    }
                },
                _ = tokio::time::sleep(LIVENESS_TICK) => {
                    let s = state.read().await;
                    if matches!(s.status, ConnectionStatus::Disconnected | ConnectionStatus::Error) {
                        return CompactTurnOutcome::Failed;
                    }
                    if s.turn_in_flight {
                        saw_in_flight = true;
                    } else if saw_in_flight && s.event_seq > watermark {
                        return CompactTurnOutcome::Completed;
                    }
                }
            }
        }
    })
    .await
    .unwrap_or(CompactTurnOutcome::Failed)
}

/// The `compact` mode: send the agent's compact command on the SAME session,
/// wait for that turn, then auto-send the continuation prompt. Compaction here
/// does not reset a provider-side turn counter, so a failed/absent command
/// falls back to the `new_session` path rather than sending into a full context.
async fn handle_compact_continuation(
    request: &ContinuationRequest,
    db: &AppDatabase,
    manager: &ConnectionManager,
    data_dir: &Path,
    bus: &InternalEventBus,
) -> Result<(), AcpError> {
    let state = manager
        .get_state(&request.connection_id)
        .await
        .ok_or(AcpError::ProcessExited)?;
    // The rejected turn is still settling when the handoff is made. Wait for
    // its own TurnComplete before reading the event watermark, otherwise that
    // event would be mistaken for the compaction's ending.
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            let s = state.read().await;
            if !s.turn_in_flight {
                return;
            }
            drop(s);
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
    })
    .await
    .map_err(|_| AcpError::protocol("rejected turn did not settle before compaction"))?;

    let (advertised, watermark) = {
        let s = state.read().await;
        (s.available_commands.clone(), s.event_seq)
    };
    let Some(command) = crate::work_task::compact::resolve_compact_command(
        None,
        request.agent_type,
        &advertised,
    ) else {
        tracing::warn!(
            "[turn-limit-continuation] no compact command for {:?}; falling back to a new session",
            request.agent_type
        );
        return handle_continuation(request, db, manager, data_dir).await;
    };

    // Subscribe BEFORE the send: the turn can finish the instant it is queued.
    let mut rx = bus.subscribe();
    manager
        .send_prompt_linked(
            db,
            &request.connection_id,
            vec![PromptInputBlock::Text {
                text: command.clone(),
            }],
            Some(request.folder_id),
            Some(request.conversation_id),
            None,
        )
        .await?;
    tracing::info!(
        "[turn-limit-continuation] compacting conversation {} with {command}",
        request.conversation_id
    );

    if matches!(
        wait_for_compaction_turn(&mut rx, &state, &request.connection_id, watermark).await,
        CompactTurnOutcome::Failed
    ) {
        tracing::warn!(
            "[turn-limit-continuation] compaction failed; falling back to a new session"
        );
        return handle_continuation(request, db, manager, data_dir).await;
    }

    let title = conversation_service::get_by_id(&db.conn, request.conversation_id)
        .await
        .map_err(|e| AcpError::protocol(format!("read old conversation: {e}")))?
        .title
        .unwrap_or_else(|| format!("session {}", request.conversation_id));
    manager
        .send_prompt_linked(
            db,
            &request.connection_id,
            vec![PromptInputBlock::Text {
                text: build_continuation_prompt(&title, request.conversation_id),
            }],
            Some(request.folder_id),
            Some(request.conversation_id),
            None,
        )
        .await?;
    Ok(())
}

async fn handle_continuation(
    request: &ContinuationRequest,
    db: &AppDatabase,
    manager: &ConnectionManager,
    data_dir: &Path,
) -> Result<(), AcpError> {
    // Stop the rejected turn first. `cancel` already flips the row to
    // `Cancelled` and broadcasts the status change, so no UI is left showing
    // a session that can no longer make progress.
    if let Err(e) = manager.cancel(&db.conn, &request.connection_id, false).await {
        tracing::warn!(
            "[turn-limit-continuation] could not cancel {}: {e}",
            request.connection_id
        );
    }
    conversation_service::update_status(
        &db.conn,
        request.conversation_id,
        ConversationStatus::Completed,
    )
    .await
    .map_err(|e| AcpError::protocol(format!("mark old conversation completed: {e}")))?;
    emit_event(
        &request.emitter,
        CONVERSATION_CHANGED_EVENT,
        ConversationChange::Status {
            id: request.conversation_id,
            status: "completed".to_string(),
        },
    );

    let title = conversation_service::get_by_id(&db.conn, request.conversation_id)
        .await
        .map_err(|e| AcpError::protocol(format!("read old conversation: {e}")))?
        .title
        .unwrap_or_else(|| format!("session {}", request.conversation_id));
    let new_id = crate::commands::conversations::create_conversation_core(
        &db.conn,
        request.folder_id,
        request.agent_type,
        Some(title.clone()),
    )
    .await
    .map_err(|e| AcpError::protocol(format!("create continuation conversation: {e}")))?;
    crate::commands::conversations::emit_conversation_upsert(
        &request.emitter,
        &db.conn,
        new_id,
    )
    .await;

    let runtime_env = crate::commands::acp::build_session_runtime_env(
        db,
        request.agent_type,
        None,
        data_dir,
    )
    .await?;
    crate::commands::acp::verify_agent_installed(request.agent_type).await?;
    let spawned = crate::commands::acp::spawn_and_bind_conversation_selection(
        manager,
        &db.conn,
        request.agent_type,
        Some(request.working_dir.clone()),
        None, // a fresh session: resuming the old one would just hit the limit again
        runtime_env,
        request.owner_window_label.clone(),
        request.emitter.clone(),
        Some(new_id),
        request.selection.mode_id.clone(),
        request.selection.config_values.clone(),
    )
    .await?;
    spawned
        .bound
        .map_err(|e| AcpError::protocol(format!("bind continuation conversation: {e}")))?;

    // A fresh spawn returns before `session/new` finishes. Wait for the agent
    // session so the auto-sent prompt cannot be dropped on a half-open one.
    let state = manager
        .get_state(&spawned.connection_id)
        .await
        .ok_or(AcpError::ProcessExited)?;
    tokio::time::timeout(Duration::from_secs(70), async {
        loop {
            {
                let state = state.read().await;
                if matches!(
                    state.status,
                    ConnectionStatus::Error | ConnectionStatus::Disconnected
                ) {
                    return Err(AcpError::ProcessExited);
                }
                if state.external_id.is_some() && state.status == ConnectionStatus::Connected {
                    return Ok(());
                }
            }
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
    })
    .await
    .map_err(|_| AcpError::InitializeTimeout)??;

    manager
        .send_prompt_linked(
            db,
            &spawned.connection_id,
            vec![PromptInputBlock::Text {
                text: build_continuation_prompt(&title, request.conversation_id),
            }],
            Some(request.folder_id),
            Some(new_id),
            None,
        )
        .await?;
    tracing::info!(
        "[turn-limit-continuation] conversation {} -> {} ({})",
        request.conversation_id,
        new_id,
        spawned.connection_id
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn continuation_prompt_links_the_old_session_and_says_to_continue() {
        assert_eq!(
            build_continuation_prompt("My task", 42),
            "[My task](codeg://session/42) 请继续上面会话的工作。"
        );
    }

    #[tokio::test]
    async fn runtime_config_defaults_to_new_session_and_is_hot_swappable() {
        let config = TurnLimitContinuationRuntimeConfig::new();
        assert_eq!(config.mode().await, TurnLimitContinuationMode::NewSession);
        config
            .set(TurnLimitContinuationConfig {
                mode: TurnLimitContinuationMode::Compact,
            })
            .await;
        assert_eq!(config.mode().await, TurnLimitContinuationMode::Compact);
        assert_eq!(
            config.snapshot().await,
            TurnLimitContinuationConfig {
                mode: TurnLimitContinuationMode::Compact
            }
        );
    }

    #[test]
    fn mode_serializes_as_the_dropdown_value() {
        assert_eq!(
            serde_json::to_string(&TurnLimitContinuationMode::Off).unwrap(),
            "\"off\""
        );
        assert_eq!(
            serde_json::to_string(&TurnLimitContinuationMode::Compact).unwrap(),
            "\"compact\""
        );
        assert_eq!(
            serde_json::to_string(&TurnLimitContinuationMode::NewSession).unwrap(),
            "\"new_session\""
        );
        assert_eq!(
            serde_json::from_str::<TurnLimitContinuationMode>("\"compact\"").unwrap(),
            TurnLimitContinuationMode::Compact
        );
    }
}
