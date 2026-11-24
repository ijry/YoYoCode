//! Multi-agent delegation: the parent agent's LLM can call the built-in MCP
//! tool `delegate_to_agent` to spawn a fresh ACP session of any (possibly
//! different) agent type, wait for its first turn to finish, and receive the
//! sub-agent's final assistant text as the MCP tool_result.
//!
//! The high-level wiring is:
//!
//! ```text
//!   parent LLM ─┐
//!               │ ToolUse(delegate_to_agent, ...)
//!               ▼
//!   parent CLI ──stdio──► yoyocode-mcp (per-launch companion binary)
//!                                 │
//!                                 │ UDS / named pipe (token-authed)
//!                                 ▼
//!                       DelegationBroker (this module)
//!                                 │
//!                                 │ ConnectionSpawner trait
//!                                 ▼
//!                       ConnectionManager.spawn_agent / send_prompt_linked
//!                                 │
//!                                 ▼
//!                       child ACP session  ── TurnComplete ──┐
//!                                                            │
//!   parent LLM ◄── MCP tool_result ◄── DelegationOutcome ◄───┘
//! ```
//!
//! v1 is one-shot (function-call semantics): after the child's first
//! `TurnComplete`, the broker resolves the pending call, sends `disconnect`
//! to the child, and returns. v2 will introduce `continue_with_session` /
//! `close_session` tools without protocol breakage.
//!
//! One deliberate exception to one-shot: `resume_delegation` revives a task
//! that was INTERRUPTED (canceled, or stranded by a crash) by re-spawning its
//! child connection with the recorded agent session id and re-arming the same
//! `delegation_call_id` routing — strictly a continuation of the original
//! task, never a second iteration on it (the tool takes no task text).

pub mod assistants;
pub mod broker;
pub mod companion;
pub mod depth;
pub mod event_emitter;
pub mod listener;
pub mod live_reply;
pub mod meta_writer;
pub mod parent_watcher;
pub mod service;
pub mod spawner;
pub mod transport;
pub mod types;

/// Canonical titles written onto a parent tool call that was announced
/// identity-less (Cursor's `"MCP: tool"` — see
/// `acp::lifecycle::CURSOR_IDENTITYLESS_MCP_TITLE`) once the companion
/// round-trip reveals which yoyocode-mcp tool it actually is. Two writers must
/// agree on these strings: the broker/listener call-time rewrite
/// (`DelegationBroker::rewrite_identityless_tool_call`) and the
/// completion-time result sniff in `acp::connection`
/// (`cursor_companion_title_from_content`). The `yoyocode-mcp__<tool>` shape is
/// what the frontend's tool-name normalizer already resolves to the dedicated
/// delegation / status cards.
pub const DELEGATE_TOOL_REWRITE_TITLE: &str = "yoyocode-mcp__delegate_to_agent";
pub const STATUS_TOOL_REWRITE_TITLE: &str = "yoyocode-mcp__get_delegation_status";
pub const CANCEL_TOOL_REWRITE_TITLE: &str = "yoyocode-mcp__cancel_delegation";
pub const RESUME_TOOL_REWRITE_TITLE: &str = "yoyocode-mcp__resume_delegation";

/// Whether a host's name for a tool is our companion MCP server's own `tool`,
/// in whatever spelling the host composed it — `mcp__yoyocode-mcp__ask_user_question`
/// (the raw name claude-agent-acp sent as the title until 0.109.1),
/// `yoyocode-mcp: ask_user_question` (its title since), `yoyocode-mcp__…`,
/// `yoyocode-mcp/…`, `mcp.yoyocode-mcp.…`, any case. Both brands are recognised,
/// since saved histories carry both: `yoyocode-mcp` / `yoyocode_mcp` and the
/// legacy `codeg-mcp` / `codeg_mcp`. The name is cut at the first separator
/// between server and tool (`__`, `/`, `:` or `.`), and each half must be
/// exactly the server's and the tool's: a server named `other-yoyocode-mcp` or
/// `codeg` (whose `mcp_submit_result` would read the same with the boundary
/// gone), or a tool named `ask_user_question_twice`, is someone else's. It
/// decides auto-approvals, so a near miss keeps its approval card.
///
/// A leading `mcp` namespace is set aside only when it is spelled with the
/// same separator as the boundary after it (`mcp__yoyocode-mcp__…`,
/// `mcp.yoyocode-mcp.…`). Otherwise `mcp` is the server: since
/// claude-agent-acp 0.109.1 a third-party server named `mcp` whose tool is
/// called `yoyocode-mcp__ask_user_question` is titled
/// `mcp: yoyocode-mcp__ask_user_question`, and reading that `mcp:` as a
/// namespace would hand our shortcut to it.
pub fn is_codeg_mcp_tool(name: &str, tool: &str) -> bool {
    const BOUNDARIES: [&str; 4] = ["__", "/", ":", "."];
    // The first boundary in `text`: (server, tool, separator).
    fn split(text: &str) -> Option<(&str, &str, &'static str)> {
        let (at, sep) = BOUNDARIES
            .iter()
            .filter_map(|sep| text.find(sep).map(|at| (at, *sep)))
            .min_by_key(|(at, _)| *at)?;
        Some((text[..at].trim(), text[at + sep.len()..].trim(), sep))
    }
    let names_it = |server: &str, named_tool: &str| {
        // Both brands occur in saved histories, and adapters use underscores.
        matches!(
            server,
            "yoyocode-mcp" | "yoyocode_mcp" | "codeg-mcp" | "codeg_mcp"
        ) && named_tool == tool
    };
    let name = name.trim().to_ascii_lowercase();
    if split(&name).is_some_and(|(server, named_tool, _)| names_it(server, named_tool)) {
        return true;
    }
    BOUNDARIES.iter().any(|sep| {
        name.strip_prefix(&format!("mcp{sep}"))
            .and_then(split)
            .is_some_and(|(server, named_tool, inner)| {
                inner == *sep && names_it(server, named_tool)
            })
    })
}

#[cfg(test)]
mod tests {
    use super::is_codeg_mcp_tool;

    #[test]
    fn a_codeg_mcp_tool_is_known_by_its_whole_name_only() {
        for server in ["codeg-mcp", "codeg_mcp", "yoyocode-mcp", "yoyocode_mcp"] {
            for name in [
                format!("mcp__{server}__submit_result"),
                format!("{server}__submit_result"),
                format!("{server}/submit_result"),
                format!("{server}: submit_result"),
                format!("mcp.{server}.submit_result"),
                format!("mcp:{server}:submit_result"),
                format!("  MCP__{}__Submit_Result  ", server.to_ascii_uppercase()),
            ] {
                assert!(is_codeg_mcp_tool(&name, "submit_result"), "{name}");
            }
            for name in [
                format!("mcp__other-{server}__submit_result"),
                format!("mcp__{server}-x__submit_result"),
                format!("mcp__{server}__submit_result_twice"),
                format!("mcp__{server}__ask_user_question"),
                // A server named `mcp` whose tool borrows the companion's
                // whole name: `mcp` is the server there, not a namespace.
                format!("mcp: {server}__submit_result"),
                format!("mcp: {server}.submit_result"),
                format!("mcp/{server}__submit_result"),
                format!("mcp__mcp__{server}__submit_result"),
            ] {
                assert!(!is_codeg_mcp_tool(&name, "submit_result"), "{name}");
            }
        }
        for name in [
            // A server named `codeg` with a tool `mcp_submit_result`: the same
            // words, another boundary.
            "mcp__codeg__mcp_submit_result",
            "codeg/mcp_submit_result",
            // A server named `mcp` whose tool borrows codeg's whole name, as
            // claude-agent-acp 0.109.1+ titles it (`<server>: <tool>`) and as
            // other hosts join it: `mcp` is the server there, not a namespace.
            "mcp: codeg-mcp__submit_result",
            "mcp: codeg-mcp.submit_result",
            "mcp/codeg-mcp__submit_result",
            "mcp__mcp__codeg-mcp__submit_result",
            "submit_result",
            "yoyocode-mcp",
            "codeg-mcp",
            "",
        ] {
            assert!(!is_codeg_mcp_tool(name, "submit_result"), "{name}");
        }
    }
}
