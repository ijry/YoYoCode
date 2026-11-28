use super::*;
use crate::db::service::message_queue_service::{self, QueueItemInput};
use crate::db::test_helpers;
use sea_orm::DatabaseConnection;

fn draft() -> String {
    serde_json::json!({"blocks":[{"type":"text","text":"task A"}],"displayText":"task A"})
        .to_string()
}

async fn begin(db: &DatabaseConnection, recovery_of: Option<&str>) -> QueueTurnAttempt {
    begin_attempt(db, "42", "connection-A", "message-A", &draft(), recovery_of)
        .await
        .unwrap()
}

fn completed(attempt: &QueueTurnAttempt, success: bool, cancelled: bool) -> QueueTurnCompletion {
    QueueTurnCompletion {
        attempt_id: attempt.id.clone(),
        owner_id: attempt.owner_id.clone(),
        success,
        cancelled,
        user_cancelled: false,
        error: (!success).then(|| "provider error".into()),
    }
}

/// A completion for a turn a person stopped with the Stop button.
fn user_cancelled(attempt: &QueueTurnAttempt) -> QueueTurnCompletion {
    QueueTurnCompletion {
        attempt_id: attempt.id.clone(),
        owner_id: attempt.owner_id.clone(),
        success: false,
        cancelled: true,
        user_cancelled: true,
        error: Some("provider error".into()),
    }
}

async fn save_one(db: &DatabaseConnection, id: &str) {
    message_queue_service::save(
        db,
        "42",
        vec![QueueItemInput {
            id: id.into(),
            draft_json: draft(),
            mode_id: None,
            adopt_send_time_mode: false,
        }],
    )
    .await
    .unwrap();
}

#[tokio::test]
async fn a_failed_turn_blocks_later_queue_even_after_save_and_reload() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    complete_attempt(&db.conn, &completed(&attempt, false, false))
        .await
        .unwrap();
    message_queue_service::save(
        &db.conn,
        "42",
        vec![QueueItemInput {
            id: "B".into(),
            draft_json: draft(),
            mode_id: None,
            adopt_send_time_mode: false,
        }],
    )
    .await
    .unwrap();
    assert!(message_queue_service::claim_next(&db.conn, "42", "claim-B")
        .await
        .unwrap()
        .is_none());
    let state = get_state(&db.conn, "42").await.unwrap();
    assert_eq!(state.status, "paused");
    assert_eq!(
        state.failed_attempt_id.as_deref(),
        Some(attempt.id.as_str())
    );
    assert_eq!(state.error.as_deref(), Some("provider error"));
}

async fn save_draft(db: &DatabaseConnection, id: &str, draft_json: &str) {
    message_queue_service::save(
        db,
        "42",
        vec![QueueItemInput {
            id: id.into(),
            draft_json: draft_json.into(),
            mode_id: None,
            adopt_send_time_mode: false,
        }],
    )
    .await
    .unwrap();
}

fn draft_with_text(text: &str) -> String {
    serde_json::json!({"blocks":[{"type":"text","text":text}],"displayText":text})
        .to_string()
}

#[tokio::test]
async fn a_failed_turn_auto_resumes_when_the_queued_head_is_a_continue_instruction() {
    let db = test_helpers::fresh_in_memory_db().await;
    save_draft(
        &db.conn,
        "B",
        &draft_with_text("继续之前因错误中断的任务。从中断处接着完成尚未完成的部分。"),
    )
    .await;
    let attempt = begin(&db.conn, None).await;
    let state = complete_attempt(&db.conn, &completed(&attempt, false, false))
        .await
        .unwrap();

    // 队首就是一条「继续」指令：报错不 pause，队列照常排空（发的是这条队列消息，
    // 不是宿主的固定续跑话术）。
    assert_eq!(state.status, "open");
    assert!(state.failed_attempt_id.is_none());
    assert!(state.error.is_none());
    assert!(state.hold.is_none());
    let claim = message_queue_service::claim_next(&db.conn, "42", "claim-C")
        .await
        .unwrap()
        .expect("continue head must keep draining");
    assert!(claim.item.draft_json.contains("继续之前"));
}

#[tokio::test]
async fn a_failed_turn_keeps_the_queue_paused_for_a_plain_head() {
    let db = test_helpers::fresh_in_memory_db().await;
    save_draft(&db.conn, "B", &draft_with_text("task A")).await;
    let attempt = begin(&db.conn, None).await;
    let state = complete_attempt(&db.conn, &completed(&attempt, false, false))
        .await
        .unwrap();

    assert_eq!(state.status, "paused");
    assert!(message_queue_service::claim_next(&db.conn, "42", "claim-B")
        .await
        .unwrap()
        .is_none());
}

#[tokio::test]
async fn a_failed_turn_with_an_empty_queue_stays_paused() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    let state = complete_attempt(&db.conn, &completed(&attempt, false, false))
        .await
        .unwrap();
    assert_eq!(state.status, "paused");
}

#[tokio::test]
async fn a_future_scheduled_head_is_transparent_to_the_continue_check() {
    let db = test_helpers::fresh_in_memory_db().await;
    // 队首是一条未到时间的定时消息（普通文本）：按 claim 的透明规则跳过它，
    // 看下一条 —— 下一条是继续指令，照样自动放行。
    message_queue_service::save(
        &db.conn,
        "42",
        vec![QueueItemInput {
            id: "S".into(),
            draft_json: draft_with_text("task A"),
            mode_id: None,
            adopt_send_time_mode: false,
        }],
    )
    .await
    .unwrap();
    message_queue_service::set_schedule(
        &db.conn,
        "42",
        "S",
        Some(Utc::now() + chrono::Duration::hours(1)),
    )
    .await
    .unwrap();
    save_draft(&db.conn, "B", &draft_with_text("继续。")).await;
    let attempt = begin(&db.conn, None).await;
    let state = complete_attempt(&db.conn, &completed(&attempt, false, false))
        .await
        .unwrap();

    assert_eq!(state.status, "open");
    let claim = message_queue_service::claim_next(&db.conn, "42", "claim-B")
        .await
        .unwrap()
        .expect("scheduled head is transparent");
    assert!(claim.item.draft_json.contains("继续。"));
}

#[tokio::test]
async fn a_user_stop_still_holds_the_queue_even_with_a_continue_head() {
    let db = test_helpers::fresh_in_memory_db().await;
    save_draft(&db.conn, "B", &draft_with_text("继续。")).await;
    let attempt = begin(&db.conn, None).await;
    let state = complete_attempt(&db.conn, &user_cancelled(&attempt))
        .await
        .unwrap();

    assert_eq!(state.status, "open");
    assert_eq!(state.hold.as_deref(), Some(HOLD_MANUAL_STOP));
    assert!(message_queue_service::claim_next(&db.conn, "42", "claim-B")
        .await
        .unwrap()
        .is_none());
}

#[tokio::test]
async fn a_broken_draft_json_is_treated_as_not_a_continue_instruction() {
    let db = test_helpers::fresh_in_memory_db().await;
    save_draft(&db.conn, "B", "{not json").await;
    let attempt = begin(&db.conn, None).await;
    let state = complete_attempt(&db.conn, &completed(&attempt, false, false))
        .await
        .unwrap();
    assert_eq!(state.status, "paused");
}

#[tokio::test]
async fn only_a_successful_continuation_releases_the_paused_queue() {
    let db = test_helpers::fresh_in_memory_db().await;
    let failed = begin(&db.conn, None).await;
    complete_attempt(&db.conn, &completed(&failed, false, false))
        .await
        .unwrap();
    request_continue(&db.conn, "42", &failed.id).await.unwrap();
    assert!(
        request_continue(&db.conn, "42", &failed.id).await.is_err(),
        "duplicate continue must not issue a second prompt"
    );
    let resume = begin(&db.conn, Some(&failed.id)).await;
    assert_eq!(get_state(&db.conn, "42").await.unwrap().status, "resuming");
    assert!(begin_attempt(&db.conn, "42", "other", "B", &draft(), None)
        .await
        .is_err());
    let state = complete_attempt(&db.conn, &completed(&resume, true, false))
        .await
        .unwrap();
    assert_eq!(state.status, "open");
    assert!(state.failed_attempt_id.is_none());
}

#[tokio::test]
async fn a_continuation_error_or_cancellation_keeps_the_queue_paused() {
    for cancelled in [false, true] {
        let db = test_helpers::fresh_in_memory_db().await;
        let failed = begin(&db.conn, None).await;
        complete_attempt(&db.conn, &completed(&failed, false, false))
            .await
            .unwrap();
        request_continue(&db.conn, "42", &failed.id).await.unwrap();
        let resume = begin(&db.conn, Some(&failed.id)).await;
        let state = complete_attempt(&db.conn, &completed(&resume, false, cancelled))
            .await
            .unwrap();
        assert_eq!(state.status, "paused");
        assert!(state.failed_attempt_id.is_some());
    }
}

/// A DIRECT prompt (the user typing into the composer while the queue is
/// paused) is their own recovery: it is admitted, the pause lifts in the same
/// transaction, and the queue itself stays shut until that turn succeeds.
/// `"继续未完成任务"` is then only one of two ways back — not the only one.
#[tokio::test]
async fn a_direct_prompt_is_admitted_through_the_pause() {
    let db = test_helpers::fresh_in_memory_db().await;
    let failed = begin(&db.conn, None).await;
    complete_attempt(&db.conn, &completed(&failed, false, false))
        .await
        .unwrap();
    save_one(&db.conn, "B").await;

    let direct = begin(&db.conn, None).await;
    assert!(direct.pause_lifted, "admission must report the lifted pause");
    let state = get_state(&db.conn, "42").await.unwrap();
    assert_eq!(state.status, "open");
    assert_eq!(
        state.active_attempt_id.as_deref(),
        Some(direct.id.as_str()),
        "the direct turn owns the barrier"
    );
    assert!(
        message_queue_service::claim_next(&db.conn, "42", "claim-B")
            .await
            .unwrap()
            .is_none(),
        "the queue must stay shut while the direct turn runs"
    );

    // The direct turn failing re-pauses — on the user's own message now.
    let state = complete_attempt(&db.conn, &completed(&direct, false, false))
        .await
        .unwrap();
    assert_eq!(state.status, "paused");
    assert_eq!(
        state.failed_attempt_id.as_deref(),
        Some(direct.id.as_str())
    );

    // Its success opens the queue for the rows already waiting.
    request_continue(&db.conn, "42", &direct.id).await.unwrap();
    let resume = begin(&db.conn, Some(&direct.id)).await;
    assert!(!resume.pause_lifted, "a recovery attempt lifts nothing new");
    let state = complete_attempt(&db.conn, &completed(&resume, true, false))
        .await
        .unwrap();
    assert_eq!(state.status, "open");
    assert!(state.failed_attempt_id.is_none());
    assert!(
        message_queue_service::claim_next(&db.conn, "42", "claim-B")
            .await
            .unwrap()
            .is_some(),
        "draining resumes once a direct send succeeded"
    );
}

#[tokio::test]
async fn old_duplicate_completion_cannot_clear_a_new_failed_attempt() {
    let db = test_helpers::fresh_in_memory_db().await;
    let first = begin(&db.conn, None).await;
    let success = completed(&first, true, false);
    complete_attempt(&db.conn, &success).await.unwrap();
    let failed = begin(&db.conn, None).await;
    complete_attempt(&db.conn, &completed(&failed, false, false))
        .await
        .unwrap();
    assert_eq!(
        complete_attempt(&db.conn, &success).await.unwrap().status,
        "paused"
    );
}

#[tokio::test]
async fn an_ordinary_user_cancel_is_not_reported_as_an_execution_error() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    assert_eq!(
        complete_attempt(&db.conn, &completed(&attempt, false, true))
            .await
            .unwrap()
            .status,
        "open"
    );
}

/// The user stopped a running task with items still queued: the barrier goes
/// back to `"open"` (nothing failed) BUT the hold parks the drain until a
/// later turn succeeds or the user releases it. A non-user (automated)
/// cancel never holds.
#[tokio::test]
async fn a_user_cancel_holds_a_non_empty_queue_but_an_automated_one_does_not() {
    for by_user in [true, false] {
        let db = test_helpers::fresh_in_memory_db().await;
        let attempt = begin(&db.conn, None).await;
        save_one(&db.conn, "B").await;
        let mut completion = user_cancelled(&attempt);
        completion.user_cancelled = by_user;
        let state = complete_attempt(&db.conn, &completion).await.unwrap();
        assert_eq!(state.status, "open");
        assert_eq!(
            state.hold.is_some(),
            by_user,
            "only a USER stop may hold the queue"
        );
        assert_eq!(
            message_queue_service::claim_next(&db.conn, "42", "claim-B")
                .await
                .unwrap()
                .is_none(),
            by_user,
            "the hold must gate the drain"
        );
    }
}

/// Nothing is queued when the user stops: nothing to hold.
#[tokio::test]
async fn a_user_cancel_with_an_empty_queue_does_not_hold() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    let state = complete_attempt(&db.conn, &user_cancelled(&attempt))
        .await
        .unwrap();
    assert_eq!(state.status, "open");
    assert!(state.hold.is_none());
}

/// The next SUCCESSFUL turn lifts the hold; a failed one keeps it (paused
/// blocks the drain anyway, and resuming from the failure must not silently
/// start draining a queue the user froze).
#[tokio::test]
async fn the_next_turn_outcome_updates_the_hold() {
    let db = test_helpers::fresh_in_memory_db().await;
    let stopped = begin(&db.conn, None).await;
    save_one(&db.conn, "B").await;
    let state = complete_attempt(&db.conn, &user_cancelled(&stopped))
        .await
        .unwrap();
    assert_eq!(state.hold.as_deref(), Some("manual_stop"));

    // A failing turn afterwards: still held (and paused by its own failure).
    let failed = begin(&db.conn, None).await;
    let state = complete_attempt(&db.conn, &completed(&failed, false, false))
        .await
        .unwrap();
    assert_eq!(state.status, "paused");
    assert_eq!(state.hold.as_deref(), Some("manual_stop"));

    // Recover from the failure, then succeed: hold lifts.
    request_continue(&db.conn, "42", &failed.id).await.unwrap();
    let resume = begin(&db.conn, Some(&failed.id)).await;
    let state = complete_attempt(&db.conn, &completed(&resume, true, false))
        .await
        .unwrap();
    assert_eq!(state.status, "open");
    assert!(state.hold.is_none());
    assert!(
        message_queue_service::claim_next(&db.conn, "42", "claim-B")
            .await
            .unwrap()
            .is_some(),
        "draining resumes after the hold lifts"
    );
}

/// A cancelled (user-stopped) RECOVERY attempt re-pauses with the failure
/// recorded and must not write a hold of its own — the paused state already
/// blocks the drain, and `request_continue` still targets the failed attempt.
#[tokio::test]
async fn a_user_cancelled_recovery_does_not_hold() {
    let db = test_helpers::fresh_in_memory_db().await;
    let failed = begin(&db.conn, None).await;
    complete_attempt(&db.conn, &completed(&failed, false, false))
        .await
        .unwrap();
    save_one(&db.conn, "B").await;
    request_continue(&db.conn, "42", &failed.id).await.unwrap();
    let resume = begin(&db.conn, Some(&failed.id)).await;
    let state = complete_attempt(&db.conn, &user_cancelled(&resume))
        .await
        .unwrap();
    assert_eq!(state.status, "paused");
    assert!(state.hold.is_none());
}

/// The explicit release lifts the hold; releasing without one conflicts.
#[tokio::test]
async fn release_hold_clears_the_park_and_conflicts_without_one() {
    let db = test_helpers::fresh_in_memory_db().await;
    assert!(release_hold(&db.conn, "42").await.is_err());
    let stopped = begin(&db.conn, None).await;
    save_one(&db.conn, "B").await;
    complete_attempt(&db.conn, &user_cancelled(&stopped))
        .await
        .unwrap();
    let state = release_hold(&db.conn, "42").await.unwrap();
    assert!(state.hold.is_none());
    assert!(
        message_queue_service::claim_next(&db.conn, "42", "claim-B")
            .await
            .unwrap()
            .is_some()
    );
    assert!(
        release_hold(&db.conn, "42").await.is_err(),
        "a second release has nothing to lift"
    );
}

#[tokio::test]
async fn manual_pause_blocks_automatic_claims_until_explicit_release() {
    let db = test_helpers::fresh_in_memory_db().await;
    save_one(&db.conn, "B").await;

    let paused = pause_queue(&db.conn, "42").await.unwrap();
    assert_eq!(paused.status, "open");
    assert_eq!(paused.hold.as_deref(), Some(HOLD_MANUAL_PAUSE));
    assert!(!can_drain(&db.conn, "42").await.unwrap());
    assert!(
        message_queue_service::claim_next(&db.conn, "42", "claim-B")
            .await
            .unwrap()
            .is_none(),
        "manual pause must block the autonomous drainer"
    );

    let resumed = release_hold(&db.conn, "42").await.unwrap();
    assert!(resumed.hold.is_none());
    assert!(
        message_queue_service::claim_next(&db.conn, "42", "claim-B")
            .await
            .unwrap()
            .is_some(),
        "explicit resume must unblock the autonomous drainer"
    );
}

#[tokio::test]
async fn manual_pause_survives_a_successful_turn_until_released() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    save_one(&db.conn, "B").await;
    pause_queue(&db.conn, "42").await.unwrap();

    let state = complete_attempt(&db.conn, &completed(&attempt, true, false))
        .await
        .unwrap();
    assert_eq!(state.status, "open");
    assert_eq!(state.hold.as_deref(), Some(HOLD_MANUAL_PAUSE));
    assert!(
        message_queue_service::claim_next(&db.conn, "42", "claim-B")
            .await
            .unwrap()
            .is_none(),
        "a successful explicit send must not resume the remaining queue"
    );
}

#[tokio::test]
async fn unfinished_attempts_remain_paused_across_host_restart() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    recover_interrupted_attempts(&db.conn).await.unwrap();
    let state = get_state(&db.conn, "42").await.unwrap();
    assert_eq!(state.status, "paused");
    assert_eq!(
        state.failed_attempt_id.as_deref(),
        Some(attempt.id.as_str())
    );
    assert!(state.active_attempt_id.is_none());
}

#[test]
fn continue_instructions_recognize_the_ten_ui_languages() {
    // 精确 / 带句尾标点 / 短语开头，全部算「继续」指令。
    for text in [
        "继续",
        "继续。",
        "继续吧",
        "繼續之前的任務",
        "接着完成任务",
        "接著做",
        // 桌面端「继续未完成任务」按钮发出的固定话术也在队列里出现过。
        "继续之前因错误中断的任务。从中断处接着完成尚未完成的部分，不要重新执行已完成操作，也不要开始待发送队列中的后续任务。",
        "continue",
        "Continue.",
        "continue the task from where it stopped",
        "resume the task",
        "carry on with the work",
        "go on",
        "続行します",
        "続けてください",
        "続きをお願い",
        "계속 진행하세요",
        "이어서 진행해 주세요",
        "continúa con la tarea",
        "continuar el trabajo",
        "sigue adelante",
        "fortsetzen",
        "Weitermachen",
        "continuez la tâche",
        "reprends le travail",
        "continuar a tarefa",
        "retoma o trabalho",
        "متابعة المهمة",
        "تابع العمل",
        "استمر من حيث توقفت",
    ] {
        assert!(is_continue_instruction(text), "should match: {text}");
    }
}

#[test]
fn continue_instructions_reject_everything_else() {
    for text in [
        "",                // 空
        "      ",          // 纯空白
        "task A",          // 普通任务
        "完成登录模块的重构",       // 没有「继续」关键词
        "先写文档，然后继续其他任务", // 关键词不在开头
        "继续阅读并总结这本书的每一章内容，整理出核心观点、论证结构和可执行的改进建议，同时对比作者在不同章节中的论述方式，最后给出一份完整的读书报告，包括引言、正文和结论三个部分，并附上每章的关键引文和页码，方便后续核对原始材料，另外请把所有引用的术语都翻译成英文并保留原文对照，方便跨语言的读者理解这些概念在不同语境下的含义差异。", // 超过 120 字符的长任务说明（开头贴关键词也不放行）
        "继续\n然后清点剩余工作",    // 多行
        "。。。",
    ] {
        assert!(!is_continue_instruction(text), "should NOT match: {text}");
    }
}

#[test]
fn continue_instructions_are_case_insensitive_for_latin_scripts() {
    assert!(is_continue_instruction("  CONTINUE.  "));
    assert!(is_continue_instruction("Resume"));
}
