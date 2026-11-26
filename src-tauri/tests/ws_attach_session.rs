//! Transport-independent attach regression against the real codeg_lib implementation.
//! A separate integration harness avoids unrelated library-unit-test compile blockers.

use std::sync::Arc;

use tokio::sync::RwLock;

use codeg_lib::acp::{
    internal_bus::InternalEventBus, session_state::SessionState, AcpEvent, EventBusMetrics,
};
use codeg_lib::models::agent::AgentType;
use codeg_lib::web::event_bridge::{emit_with_state, EventEmitter, WebEventBroadcaster};

use codeg_lib::web::ws_attach::{attach_session, ServerMsg};

fn fixture() -> (
    Arc<RwLock<SessionState>>,
    Arc<EventBusMetrics>,
    EventEmitter,
) {
    let metrics = Arc::new(EventBusMetrics::default());
    let state = Arc::new(RwLock::new(SessionState::new(
        "synthetic-connection".into(),
        AgentType::ClaudeCode,
        None,
        "nsh-probe".into(),
        None,
    )));
    let emitter = EventEmitter::web_only(
        Arc::new(WebEventBroadcaster::new()),
        Arc::new(InternalEventBus::new(metrics.clone())),
    );
    (state, metrics, emitter)
}

async fn emit(state: &Arc<RwLock<SessionState>>, emitter: &EventEmitter, count: u64) {
    for _ in 0..count {
        let seq = state.read().await.event_seq + 1;
        emit_with_state(
            state,
            emitter,
            AcpEvent::ContentDelta {
                text: format!("event-{seq}"),
                parent_tool_use_id: None,
            },
        )
        .await;
    }
}

#[tokio::test]
async fn shared_attach_session_covers_snapshot_replay_and_fallbacks() {
    let (state, metrics, emitter) = fixture();

    let mut cold = attach_session(state.clone(), &metrics, "cold".into(), None)
        .await
        .unwrap();
    assert!(matches!(
        cold.initial_msg,
        ServerMsg::Snapshot { event_seq: 0, .. }
    ));
    emit(&state, &emitter, 1).await;
    let live = tokio::time::timeout(std::time::Duration::from_secs(1), cold.receiver.recv())
        .await
        .expect("live event must arrive after the attach response")
        .unwrap();
    assert_eq!(live.seq, 1);

    emit(&state, &emitter, 2).await;
    let replay = attach_session(state.clone(), &metrics, "replay".into(), Some(1))
        .await
        .unwrap();
    match replay.initial_msg {
        ServerMsg::Replay {
            events,
            high_water_seq,
            ..
        } => {
            assert_eq!(
                events.iter().map(|event| event.seq).collect::<Vec<_>>(),
                [2, 3]
            );
            assert_eq!(high_water_seq, 3);
        }
        other => panic!("expected replay, got {other:?}"),
    }

    emit(&state, &emitter, 33).await;
    let large_gap = attach_session(state.clone(), &metrics, "large-gap".into(), Some(3))
        .await
        .unwrap();
    match large_gap.initial_msg {
        ServerMsg::Snapshot {
            snapshot,
            event_seq,
            ..
        } => {
            assert_eq!(event_seq, 36);
            assert_eq!(snapshot.connection_id, "synthetic-connection");
            let message = snapshot
                .live_message
                .expect("snapshot must recover missed text");
            let expected = (1..=36)
                .map(|seq| format!("event-{seq}"))
                .collect::<String>();
            assert!(matches!(
                message.content.as_slice(),
                [codeg_lib::acp::session_state::LiveContentBlock::Text { text, .. }] if text == &expected
            ));
        }
        other => panic!("expected fallback snapshot, got {other:?}"),
    }

    emit(&state, &emitter, 130).await;
    let expired = attach_session(state.clone(), &metrics, "expired".into(), Some(0))
        .await
        .unwrap();
    assert!(matches!(
        expired.initial_msg,
        ServerMsg::Snapshot { event_seq: 166, .. }
    ));
    let future = attach_session(state, &metrics, "future".into(), Some(200))
        .await
        .unwrap();
    assert!(matches!(
        future.initial_msg,
        ServerMsg::Snapshot { event_seq: 166, .. }
    ));

    let counters = metrics.snapshot();
    assert_eq!(counters.snapshot_cold_count, 2);
    assert_eq!(counters.replay_count, 1);
    assert_eq!(counters.replay_event_total, 2);
    assert_eq!(counters.snapshot_fallback_count, 2);
}
