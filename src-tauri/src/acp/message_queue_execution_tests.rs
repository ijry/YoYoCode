use crate::acp::session_state::SessionState;
use crate::acp::types::AcpEvent;
use crate::db::service::message_queue_control_service::QueueTurnAttempt;
use crate::models::agent::AgentType;

fn state() -> SessionState {
    let mut state = SessionState::new("c".into(), AgentType::Codex, None, "test".into(), None);
    state.queue_attempt = Some(QueueTurnAttempt {
        id: "attempt-a".into(),
        owner_id: "42".into(),
        recovery_of: None,
    });
    state.turn_in_flight = true;
    state
}

fn complete() -> AcpEvent {
    AcpEvent::TurnComplete {
        session_id: "s".into(),
        stop_reason: "end_turn".into(),
        agent_type: "codex".into(),
    }
}

#[test]
fn completed_attempt_is_captured_before_pending_prompt_and_gate_are_cleared() {
    let mut state = state();
    state.capture_queue_completion(&complete(), 12);
    state.apply_event(&complete());
    let completed = &state.queue_completions[&12];
    assert_eq!(completed.attempt_id, "attempt-a");
    assert!(completed.success);
    assert!(state.queue_attempt.is_none());
    assert!(!state.turn_in_flight);
}

#[test]
fn terminal_air_failure_is_failure_even_when_stop_reason_says_end_turn() {
    let mut state = state();
    let event = serde_json::from_value::<AcpEvent>(serde_json::json!({
      "type":"session_failure", "record":{"id":"failed","revision":1,"category":"limit","severity":"error","title":"Quota error","details":"Quota exceeded","actions":[],"resolved":false}
    })).unwrap();
    state.apply_event(&event);
    state.capture_queue_completion(&complete(), 12);
    assert!(!state.queue_completions[&12].success);
    assert!(state.queue_completions[&12].error.is_some());
}

#[test]
fn nonterminal_setter_error_does_not_finish_or_pause_a_task() {
    let mut state = state();
    let event = AcpEvent::Error {
        message: "mode failed".into(),
        code: None,
        details: None,
        agent_type: "codex".into(),
        terminal: false,
    };
    state.capture_queue_completion(&event, 12);
    assert!(state.queue_completions.is_empty());
    assert!(state.queue_attempt.is_some());
}

#[test]
fn a_terminal_connection_error_finishes_the_active_task_as_failure() {
    let mut state = state();
    let event = AcpEvent::Error {
        message: "process exited".into(),
        code: None,
        details: None,
        agent_type: "codex".into(),
        terminal: true,
    };
    state.capture_queue_completion(&event, 12);
    assert!(!state.queue_completions[&12].success);
    assert_eq!(
        state.queue_completions[&12].error.as_deref(),
        Some("process exited")
    );
    state.capture_queue_completion(&event, 13);
    assert_eq!(state.queue_completions.len(), 1);
}
