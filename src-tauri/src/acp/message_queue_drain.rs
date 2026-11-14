//! Rust owns the persisted send-later queue in desktop and server mode.
//! Saves and completed lifecycle writes wake it; boot and periodic scans
//! cover abandoned clients, and the nearest future schedule wakes it for
//! timed messages. Per-owner workers claim atomically in SQLite, keep
//! content until prompt admission, and retain uncertain deliveries.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::fs::{File, OpenOptions, TryLockError};
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use sea_orm::DatabaseConnection;
use tokio::sync::mpsc;
use tokio::task::JoinSet;
use tokio::time::Instant;

use crate::acp::error::AcpError;
use crate::acp::manager::ConnectionManager;
use crate::acp::types::{AcpEvent, ConnectionStatus, EventEnvelope, PromptInputBlock};
use crate::db::service::message_queue_service::{self, QueueClaim};
use crate::db::AppDatabase;
use crate::web::event_bridge::{
    emit_event, EventEmitter, MessageQueueChange, MESSAGE_QUEUE_CHANGED_EVENT,
};

const SWEEP_INTERVAL_SECS: u64 = 30;
const BACKOFF_INITIAL_MS: u64 = 1_000;
const BACKOFF_MAX_MS: u64 = 60_000;
const MAX_WORKERS: usize = 8;
const DRAIN_SPAWN_OWNER: &str = "message-queue-drain";

/// Work handed to the drain loop. [`DrainJob::Drain`] is the ordinary wake —
/// the worker re-reads persisted state and claims the FIFO head.
/// [`DrainJob::SendNow`] is a user-initiated send of ONE specific item, which
/// must bypass the user-stop hold; it rides the same channel so the drainer's
/// own db/manager/emitter/data_dir serve it without re-plumbing commands.
enum DrainJob {
    Drain { owner_id: String },
    SendNow { owner_id: String, item_id: String },
}

#[derive(Clone)]
pub struct DrainHandle {
    tx: mpsc::Sender<DrainJob>,
}

impl DrainHandle {
    pub fn detached() -> Self {
        let (tx, _) = mpsc::channel(64);
        Self { tx }
    }

    pub fn notify(&self, owner_id: impl Into<String>) {
        // Overflow is healed by scanning persisted pending owners.
        let _ = self.tx.try_send(DrainJob::Drain { owner_id: owner_id.into() });
    }

    /// Queue a user-requested send of one item (`message_queue_send_now`).
    /// Fire-and-forget like `notify`: the worker broadcasts the row's
    /// delivery state, so a failure surfaces on the row, not the caller.
    pub fn send_now(&self, owner_id: impl Into<String>, item_id: impl Into<String>) {
        let _ = self.tx.try_send(DrainJob::SendNow {
            owner_id: owner_id.into(),
            item_id: item_id.into(),
        });
    }
}

/// Subscribe before spawning. Tauri setup has no current Tokio runtime;
/// use its runtime there rather than panicking in `tokio::spawn`.
pub fn spawn_drain_task(
    db: DatabaseConnection,
    manager: ConnectionManager,
    emitter: EventEmitter,
    data_dir: Arc<Path>,
    bus: Arc<crate::acp::InternalEventBus>,
) -> DrainHandle {
    let (tx, rx) = mpsc::channel(64);
    let handle = DrainHandle { tx };
    manager.install_message_queue_drain(handle.clone());
    let future = run(db, manager, emitter, data_dir, rx, bus.subscribe());
    if let Ok(runtime) = tokio::runtime::Handle::try_current() {
        runtime.spawn(future);
    } else {
        #[cfg(feature = "tauri-runtime")]
        tauri::async_runtime::spawn(future);
        #[cfg(not(feature = "tauri-runtime"))]
        tracing::error!("[message-queue] no runtime available for the drainer");
    }
    handle
}

/// Desktop and server may share a data directory. Only the process holding
/// this OS lock recovers claims and drives them; a standby retries election
/// on each scan so it can take over after the elected process exits.
fn elect(data_dir: &Path) -> std::io::Result<Option<File>> {
    std::fs::create_dir_all(data_dir)?;
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(data_dir.join(format!(
            "{}.message-queue.lock",
            crate::db::database_file_name()
        )))?;
    match file.try_lock() {
        Ok(()) => Ok(Some(file)),
        Err(TryLockError::WouldBlock) => Ok(None),
        Err(TryLockError::Error(error)) => Err(error),
    }
}

async fn run(
    db: DatabaseConnection,
    manager: ConnectionManager,
    emitter: EventEmitter,
    data_dir: Arc<Path>,
    mut rx: mpsc::Receiver<DrainJob>,
    mut bus_rx: tokio::sync::broadcast::Receiver<Arc<EventEnvelope>>,
) {
    let mut tick = tokio::time::interval(Duration::from_secs(SWEEP_INTERVAL_SECS));
    tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    let mut election: Option<File> = None;
    let mut backoff: HashMap<String, (u32, Instant)> = HashMap::new();
    let mut pending = HashSet::new();
    let mut active = HashSet::new();
    let mut tasks = JoinSet::new();
    let mut bus_open = true;

    loop {
        let next_retry = backoff
            .iter()
            .filter(|(owner, _)| !active.contains(*owner))
            .map(|(_, (_, at))| *at)
            .min()
            .unwrap_or_else(|| Instant::now() + Duration::from_secs(SWEEP_INTERVAL_SECS));
        // The 30s sweep is only a safety net: the drainer wakes exactly for
        // the nearest future schedule so a timed message goes out on time
        // without busy-spinning on owners that are not due yet.
        let schedule_wake = match message_queue_service::next_schedule(&db).await {
            Ok(next) => next.and_then(|at| {
                (at - chrono::Utc::now())
                    .to_std()
                    .ok()
                    .map(|delta| Instant::now() + delta)
            }),
            Err(error) => {
                tracing::warn!(%error, "[message-queue] schedule scan failed");
                None
            }
        };
        let mut scan = false;
        tokio::select! {
            _ = tick.tick() => scan = true,
            _ = tokio::time::sleep_until(next_retry), if !backoff.is_empty() => {
                let now = Instant::now();
                let due: Vec<_> = backoff.iter().filter(|(_, (_, at))| *at <= now)
                    .map(|(owner, _)| owner.clone()).collect();
                for owner in due {
                    pending.insert(owner);
                }
            }
            _ = tokio::time::sleep_until(
                schedule_wake.unwrap_or_else(|| Instant::now() + Duration::from_secs(SWEEP_INTERVAL_SECS)),
            ), if schedule_wake.is_some() => scan = true,
            job = rx.recv() => {
                let Some(job) = job else { return; };
                match job {
                    DrainJob::Drain { owner_id } => {
                        pending.insert(owner_id);
                    }
                    DrainJob::SendNow { owner_id, item_id } => {
                        spawn_send_now_job(
                            &mut tasks,
                            &mut active,
                            DrainContext { db: &db, manager: &manager, emitter: &emitter, data_dir: &data_dir },
                            owner_id,
                            item_id,
                        );
                    }
                }
            }
            event = bus_rx.recv(), if bus_open => {
                match event {
                    Ok(event) => {
                        // Lifecycle publishes this AFTER writing the old turn's
                        // status. Raw TurnComplete would race that write against
                        // the new prompt's InProgress transition.
                        if let AcpEvent::ConversationStatusChanged { conversation_id, status } = &event.payload {
                            if *status != crate::db::entities::conversation::ConversationStatus::InProgress {
                                pending.insert(conversation_id.to_string());
                            }
                        }
                    }
                    Err(tokio::sync::broadcast::error::RecvError::Lagged(_)) => scan = true,
                    Err(tokio::sync::broadcast::error::RecvError::Closed) => bus_open = false,
                }
            }
            completed = tasks.join_next(), if !tasks.is_empty() => {
                if let Some(Ok((owner, outcome))) = completed {
                    active.remove(&owner);
                    match outcome {
                        DrainOutcome::Retry { transient } => {
                            let failures = backoff.get(&owner).map_or(0, |(count, _)| *count);
                            let count = if transient { 0 } else { failures.saturating_add(1) };
                            backoff.insert(owner, (count, Instant::now() + Duration::from_millis(backoff_delay_ms(count))));
                        }
                        DrainOutcome::Sent | DrainOutcome::Waiting | DrainOutcome::Idle => {
                            backoff.remove(&owner);
                        }
                    }
                }
            }
        }
        while let Ok(job) = rx.try_recv() {
            match job {
                DrainJob::Drain { owner_id } => {
                    pending.insert(owner_id);
                }
                DrainJob::SendNow { owner_id, item_id } => {
                    spawn_send_now_job(
                        &mut tasks,
                        &mut active,
                        DrainContext { db: &db, manager: &manager, emitter: &emitter, data_dir: &data_dir },
                        owner_id,
                        item_id,
                    );
                }
            }
        }
        if election.is_none() {
            match elect(&data_dir) {
                Ok(Some(lock)) => {
                    if let Err(error) = message_queue_service::recover_abandoned_claims(&db).await {
                        tracing::error!(%error, "[message-queue] claim recovery failed");
                        continue;
                    }
                    if let Err(error) = crate::db::service::message_queue_control_service::recover_interrupted_attempts(&db).await {
                        tracing::error!(%error, "[message-queue] interrupted turn recovery failed");
                        continue;
                    }
                    election = Some(lock);
                    scan = true;
                }
                Ok(None) => continue,
                Err(error) => {
                    tracing::error!(%error, "[message-queue] could not acquire the drain lock");
                    continue;
                }
            }
        }
        if scan {
            match crate::db::service::message_queue_control_service::pending_recoveries(&db).await {
                Ok(owners) => pending.extend(owners),
                Err(error) => tracing::warn!(%error, "[message-queue] recovery scan failed"),
            }
            match message_queue_service::pending_owners(&db).await {
                Ok(owners) => pending.extend(owners),
                Err(error) => tracing::warn!(%error, "[message-queue] persisted queue scan failed"),
            }
        }
        let now = Instant::now();
        let ready: Vec<_> = pending
            .iter()
            .filter(|owner| {
                !active.contains(*owner) && backoff.get(*owner).is_none_or(|(_, at)| *at <= now)
            })
            .take(MAX_WORKERS.saturating_sub(active.len()))
            .cloned()
            .collect();
        for owner in ready {
            pending.remove(&owner);
            active.insert(owner.clone());
            let worker_db = db.clone();
            let worker_manager = manager.clone_ref();
            let worker_emitter = emitter.clone();
            let worker_data = data_dir.clone();
            tasks.spawn(async move {
                let outcome = try_drain_owner(
                    &worker_db,
                    &worker_manager,
                    &worker_emitter,
                    &worker_data,
                    &owner,
                )
                .await;
                (owner, outcome)
            });
        }
    }
}

fn backoff_delay_ms(failures: u32) -> u64 {
    BACKOFF_INITIAL_MS
        .saturating_mul(1u64 << failures.saturating_sub(1).min(16))
        .min(BACKOFF_MAX_MS)
}

/// What a spawned worker needs, cloned out of the run loop once. Bundled so
/// the spawner stays under the argument-count lint.
struct DrainContext<'a> {
    db: &'a DatabaseConnection,
    manager: &'a ConnectionManager,
    emitter: &'a EventEmitter,
    data_dir: &'a Arc<Path>,
}

/// Run a user-initiated `send_item_now` as a drain worker. Tracked in
/// `active` like an ordinary owner worker so the join arm's bookkeeping
/// stays single-sourced; a failure deliberately takes NO backoff — the user
/// sees the row's error and can click again.
fn spawn_send_now_job(
    tasks: &mut JoinSet<(String, DrainOutcome)>,
    active: &mut HashSet<String>,
    ctx: DrainContext<'_>,
    owner_id: String,
    item_id: String,
) {
    active.insert(owner_id.clone());
    let worker_db = ctx.db.clone();
    let worker_manager = ctx.manager.clone_ref();
    let worker_emitter = ctx.emitter.clone();
    let worker_data = ctx.data_dir.clone();
    tasks.spawn(async move {
        let outcome = match send_item_now(
            &worker_db,
            &worker_manager,
            &worker_emitter,
            &worker_data,
            &owner_id,
            &item_id,
        )
        .await
        {
            Ok(()) => DrainOutcome::Sent,
            Err(error) => {
                tracing::warn!(%error, owner_id, "[message-queue] send-now failed");
                DrainOutcome::Waiting
            }
        };
        (owner_id, outcome)
    });
}

enum DrainOutcome {
    Idle,
    Sent,
    Waiting,
    Retry { transient: bool },
}

async fn broadcast_queue(db: &DatabaseConnection, emitter: &EventEmitter, owner_id: &str) {
    match message_queue_service::list(db, owner_id).await {
        Ok(items) => emit_event(
            emitter,
            MESSAGE_QUEUE_CHANGED_EVENT,
            MessageQueueChange {
                owner_id: owner_id.into(),
                origin: "backend-drain".into(),
                items,
            },
        ),
        Err(error) => {
            tracing::warn!(%error, owner_id, "[message-queue] could not publish the queue")
        }
    }
}

async fn try_drain_owner(
    db: &DatabaseConnection,
    manager: &ConnectionManager,
    emitter: &EventEmitter,
    data_dir: &Path,
    owner_id: &str,
) -> DrainOutcome {
    let control = match crate::db::service::message_queue_control_service::get_state(db, owner_id).await {
        Ok(control) => control,
        Err(_) => return DrainOutcome::Retry { transient: false },
    };
    if control.status == "resume_requested" && control.active_attempt_id.is_none() {
        return try_continue_owner(db, manager, emitter, data_dir, &control).await;
    }
    if control.status != "open" || control.active_attempt_id.is_some() { return DrainOutcome::Waiting; }
    let claim =
        match message_queue_service::claim_next(db, owner_id, &uuid::Uuid::new_v4().to_string())
            .await
        {
            Ok(Some(claim)) => claim,
            Ok(None) => return DrainOutcome::Idle,
            Err(error) => {
                tracing::warn!(%error, owner_id, "[message-queue] claim failed");
                return DrainOutcome::Retry { transient: false };
            }
        };
    broadcast_queue(db, emitter, owner_id).await;
    let blocks = match parse_draft_json(&claim.item.draft_json) {
        Ok(blocks) => blocks,
        Err(error) => {
            if let Err(error) = message_queue_service::fail(db, &claim, &error).await {
                tracing::error!(%error, owner_id, "[message-queue] failed to retain the invalid draft");
            }
            broadcast_queue(db, emitter, owner_id).await;
            return DrainOutcome::Idle;
        }
    };
    let cid = match owner_id.parse::<i32>() {
        Ok(cid) if cid > 0 => cid,
        _ => return release_claim(db, emitter, &claim, "invalid conversation id", false).await,
    };
    let conversation = match crate::db::service::conversation_service::get_by_id(db, cid).await {
        Ok(conversation) => conversation,
        Err(error) => return release_claim(db, emitter, &claim, &error.to_string(), false).await,
    };
    let connection_id = match live_connection(manager, &conversation).await {
        Some(id) => id,
        None => {
            match spawn_connection_for_conversation(manager, db, emitter, data_dir, &conversation)
                .await
            {
                Ok(id) => id,
                Err(error) => {
                    return release_claim(db, emitter, &claim, &error.to_string(), false).await
                }
            }
        }
    };
    let Some(state) = manager.get_state(&connection_id).await else {
        return release_claim(db, emitter, &claim, "connection disappeared", false).await;
    };
    {
        let state = state.read().await;
        if state.turn_in_flight
            || state.agent_initiated_turn
            || state.status != ConnectionStatus::Connected
        {
            let _ = message_queue_service::release(db, &claim, None).await;
            broadcast_queue(db, emitter, owner_id).await;
            return DrainOutcome::Waiting;
        }
    }
    // The lifecycle writes PendingReview before waking the next item. A
    // sweep that sees the falling edge slightly earlier waits for that write.
    if conversation.status == "in_progress" && state.read().await.turns_completed > 0 {
        let _ = message_queue_service::release(db, &claim, None).await;
        broadcast_queue(db, emitter, owner_id).await;
        return DrainOutcome::Waiting;
    }
    if let Err(error) = message_queue_service::mark_sending(db, &claim).await {
        tracing::error!(%error, owner_id, "[message-queue] dispatch marker could not be persisted");
        return release_claim(db, emitter, &claim, &error.to_string(), false).await;
    }
    let mode_id = if claim.item.adopt_send_time_mode {
        None
    } else {
        claim.item.mode_id.clone()
    };
    let result = manager
        .send_queued_prompt(
            &AppDatabase { conn: db.clone() },
            &connection_id,
            blocks,
            conversation.folder_id,
            cid,
            mode_id,
            format!("queue-{}", claim.item.id),
        )
        .await;
    match result {
        Ok(_) => {
            // Failure to acknowledge leaves the sending row protected. Never
            // auto-retry a prompt already admitted by the manager.
            if let Err(error) = message_queue_service::acknowledge(db, &claim).await {
                tracing::error!(%error, owner_id, "[message-queue] prompt admitted but acknowledgement failed");
            }
            broadcast_queue(db, emitter, owner_id).await;
            DrainOutcome::Sent
        }
        Err(error) => {
            let transient = matches!(error, AcpError::TurnInProgress);
            release_claim(db, emitter, &claim, &error.to_string(), transient).await
        }
    }
}

async fn try_continue_owner(
    db: &DatabaseConnection, manager: &ConnectionManager, emitter: &EventEmitter,
    data_dir: &Path, control: &crate::db::service::message_queue_control_service::MessageQueueState,
) -> DrainOutcome {
    let Some(failed) = control.failed_attempt_id.as_deref() else { return DrainOutcome::Waiting; };
    let Ok(cid) = control.owner_id.parse::<i32>() else { return DrainOutcome::Waiting; };
    let Ok(conversation) = crate::db::service::conversation_service::get_by_id(db, cid).await else { return DrainOutcome::Retry { transient: false }; };
    let connection = match live_connection(manager, &conversation).await {
        Some(connection) => connection,
        None => match spawn_connection_for_conversation(manager, db, emitter, data_dir, &conversation).await {
            Ok(connection) => connection,
            Err(error) => { tracing::warn!(%error, "[message-queue] failed to resume previous task"); return DrainOutcome::Retry { transient: false }; }
        },
    };
    let result = manager.send_queue_continuation(&AppDatabase { conn: db.clone() }, &connection, conversation.folder_id, cid, failed).await;
    let state = crate::db::service::message_queue_control_service::get_state(db, &control.owner_id).await;
    if let Ok(state) = state { emit_event(emitter, crate::web::event_bridge::MESSAGE_QUEUE_STATE_EVENT, state); }
    match result {
        Ok(_) => DrainOutcome::Sent,
        Err(error) => { tracing::warn!(%error, "[message-queue] continuation submit failed"); DrainOutcome::Retry { transient: matches!(error, AcpError::TurnInProgress) } }
    }
}

/// Send ONE queued item right now at the user's explicit request
/// (`message_queue_send_now`). The whole point of this path is to bypass the
/// user-stop hold — the user is choosing what goes out — so unlike
/// [`try_drain_owner`] it never consults `can_drain`; it still respects the
/// queue barrier itself (`open`, no active attempt, one claim at a time), so
/// it cannot interleave with a paused recovery or a running turn. Mirrors the
/// drain's admission sequence: claim → parse → live/spawn connection →
/// send → acknowledge/release.
pub async fn send_item_now(
    db: &DatabaseConnection,
    manager: &ConnectionManager,
    emitter: &EventEmitter,
    data_dir: &Path,
    owner_id: &str,
    item_id: &str,
) -> Result<(), AcpError> {
    let control = crate::db::service::message_queue_control_service::get_state(db, owner_id)
        .await
        .map_err(|error| AcpError::protocol(error.to_string()))?;
    if control.status != "open" || control.active_attempt_id.is_some() {
        return Err(AcpError::protocol(
            "queue is paused or a previous turn is not settled",
        ));
    }
    let claim = message_queue_service::claim_item(db, owner_id, item_id, &uuid::Uuid::new_v4().to_string())
        .await
        .map_err(|error| AcpError::protocol(error.to_string()))?
        .ok_or_else(|| AcpError::protocol("queue item is not sendable right now"))?;
    broadcast_queue(db, emitter, owner_id).await;
    let send = async {
        let blocks = parse_draft_json(&claim.item.draft_json)
            .map_err(AcpError::protocol)?;
        let cid = claim
            .owner_id
            .parse::<i32>()
            .ok()
            .filter(|cid| *cid > 0)
            .ok_or_else(|| AcpError::protocol("invalid conversation id"))?;
        let conversation = crate::db::service::conversation_service::get_by_id(db, cid)
            .await
            .map_err(|error| AcpError::protocol(error.to_string()))?;
        let connection_id = match live_connection(manager, &conversation).await {
            Some(id) => id,
            None => {
                spawn_connection_for_conversation(manager, db, emitter, data_dir, &conversation)
                    .await?
            }
        };
        let state = manager
            .get_state(&connection_id)
            .await
            .ok_or(AcpError::ProcessExited)?;
        {
            let state = state.read().await;
            if state.turn_in_flight
                || state.agent_initiated_turn
                || state.status != ConnectionStatus::Connected
            {
                return Err(AcpError::TurnInProgress);
            }
        }
        message_queue_service::mark_sending(db, &claim)
            .await
            .map_err(|error| AcpError::protocol(error.to_string()))?;
        // Same send-time mode rule as the drain: an item queued before its tab
        // knew the modes adopts the mode in effect now; `mode_id: None` from
        // the answer / plan-notes retry paths means "leave the agent's mode".
        let mode_id = if claim.item.adopt_send_time_mode {
            None
        } else {
            claim.item.mode_id.clone()
        };
        manager
            .send_queued_prompt(
                &AppDatabase { conn: db.clone() },
                &connection_id,
                blocks,
                conversation.folder_id,
                cid,
                mode_id,
                format!("queue-{}", claim.item.id),
            )
            .await
    }
    .await;
    match send {
        Ok(_) => {
            // Failure to acknowledge leaves the sending row protected. Never
            // auto-retry a prompt already admitted by the manager.
            if let Err(error) = message_queue_service::acknowledge(db, &claim).await {
                tracing::error!(%error, owner_id, "[message-queue] send-now admitted but acknowledgement failed");
            }
            broadcast_queue(db, emitter, owner_id).await;
            Ok(())
        }
        Err(error) => {
            if let Err(release_error) =
                message_queue_service::release(db, &claim, Some(&error.to_string())).await
            {
                tracing::error!(%release_error, owner_id, "[message-queue] send-now could not release claim");
            }
            broadcast_queue(db, emitter, owner_id).await;
            Err(error)
        }
    }
}

async fn release_claim(
    db: &DatabaseConnection,
    emitter: &EventEmitter,
    claim: &QueueClaim,
    error: &str,
    transient: bool,
) -> DrainOutcome {
    if let Err(error) = message_queue_service::release(db, claim, Some(error)).await {
        tracing::error!(%error, owner_id = %claim.owner_id, "[message-queue] could not release claim; original body retained");
    }
    broadcast_queue(db, emitter, &claim.owner_id).await;
    DrainOutcome::Retry { transient }
}

async fn live_connection(
    manager: &ConnectionManager,
    conversation: &crate::models::conversation::DbConversationSummary,
) -> Option<String> {
    let connections = manager.connections.lock().await;
    for (id, connection) in connections.iter() {
        if connection.agent_type != conversation.agent_type {
            continue;
        }
        let state = connection.state.read().await;
        if matches!(
            state.status,
            ConnectionStatus::Error | ConnectionStatus::Disconnected
        ) {
            continue;
        }
        if state.conversation_id == Some(conversation.id)
            || (state.conversation_id.is_none()
                && conversation.external_id.is_some()
                && state.external_id == conversation.external_id)
        {
            return Some(id.clone());
        }
    }
    None
}

async fn spawn_connection_for_conversation(
    manager: &ConnectionManager,
    db: &DatabaseConnection,
    emitter: &EventEmitter,
    data_dir: &Path,
    conversation: &crate::models::conversation::DbConversationSummary,
) -> Result<String, AcpError> {
    let working_dir = match conversation
        .origin_cwd
        .as_ref()
        .filter(|cwd| !cwd.is_empty())
    {
        Some(cwd) => cwd.clone(),
        None => {
            crate::db::service::folder_service::get_folder_by_id(db, conversation.folder_id)
                .await
                .map_err(|error| AcpError::protocol(error.to_string()))?
                .ok_or_else(|| AcpError::protocol("conversation folder no longer exists"))?
                .path
        }
    };
    if !Path::new(&working_dir).is_dir() {
        return Err(AcpError::protocol(
            "conversation working directory no longer exists",
        ));
    }
    let app_db = AppDatabase { conn: db.clone() };
    let runtime_env = crate::commands::acp::build_session_runtime_env(
        &app_db,
        conversation.agent_type,
        conversation.external_id.as_deref(),
        data_dir,
    )
    .await?;
    crate::commands::acp::verify_agent_installed(conversation.agent_type).await?;
    let id = manager
        .spawn_agent(
            conversation.agent_type,
            Some(working_dir),
            conversation.external_id.clone(),
            runtime_env,
            DRAIN_SPAWN_OWNER.into(),
            emitter.clone(),
            None,
            BTreeMap::new(),
        )
        .await?;
    let state = manager
        .get_state(&id)
        .await
        .ok_or(AcpError::ProcessExited)?;
    // A fresh spawn returns before initialize/session-new finish. Wait for
    // SessionStarted, not merely Connected (initialize publishes it earlier).
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
                    return Ok(id.clone());
                }
            }
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
    })
    .await
    .map_err(|_| AcpError::InitializeTimeout)?
}

#[derive(serde::Deserialize)]
struct PersistedDraft {
    blocks: Vec<PromptInputBlock>,
}

fn parse_draft_json(json: &str) -> Result<Vec<PromptInputBlock>, String> {
    let draft: PersistedDraft = serde_json::from_str(json).map_err(|error| error.to_string())?;
    if draft.blocks.is_empty() {
        return Err("draft has no blocks".into());
    }
    Ok(draft.blocks)
}

#[cfg(test)]
#[path = "message_queue_drain_tests.rs"]
mod tests;
