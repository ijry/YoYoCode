//! Listener-facing access for the comic (漫剧) production tools carried by
//! yoyocode-mcp (`comic_get_project`, `comic_set_source`, the resource
//! readers/writers, ... — see `delegation/tool_schema.json`).
//!
//! One trait method, [`ComicAccess::call`], carries every comic tool: the
//! tool name plus its raw arguments. The comic surface grows quickly and its
//! tools are resource CRUD with evolving shapes; a single stable envelope
//! (`tool` + `args` in, [`ComicOutcome`] out) keeps the wire, the listener
//! arm, and the companion dispatch from growing a variant per tool.
//!
//! Like every access trait here, this module owns **the shape of the answer,
//! not the decision**: whether a write lands is `crate::comic::store`'s (the
//! story jail, the per-folder write mutex, reference validation), and the
//! production impl routes through it so an MCP write mutates the same tree,
//! under the same lock, as a workbench edit.
//!
//! A refusal is a value, not a transport error — [`ComicOutcome::refused`]
//! gives the LLM something actionable ("this folder is not a comic project",
//! "unknown character slug …") instead of aborting the turn.

use std::path::Path;
use std::sync::Arc;

use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// The session's working directory is not a comic project — there is no
/// `story/` root. The uniform answer for every tool call, whether the folder
/// truly has none or the token did not check out (an unauthenticated caller
/// learns nothing about which it is).
pub const ERROR_NOT_COMIC_PROJECT: &str = "comic_not_a_comic_project";

/// The comic tool group is switched off, or this build has no comic backend
/// (a companion predating the setting).
pub const ERROR_UNAVAILABLE: &str = "comic_unavailable";

/// The tool name is not one this backend knows (a companion newer than its
/// parent, most likely).
pub const ERROR_UNKNOWN_TOOL: &str = "comic_unknown_tool";

/// What every comic tool answers with. `data` carries the tool-specific JSON
/// when `ok`; when not, `error` is a classified, actionable refusal. Tool
/// results never embed image/video bytes — artifacts are story-relative
/// paths the workbench renders.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ComicOutcome {
    pub ok: bool,
    #[serde(default)]
    pub data: Value,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<ComicError>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ComicError {
    pub code: String,
    pub message: String,
}

impl ComicOutcome {
    pub fn ok(data: Value) -> Self {
        Self {
            ok: true,
            data,
            error: None,
        }
    }

    pub fn refused(code: &str, message: impl Into<String>) -> Self {
        Self {
            ok: false,
            data: Value::Null,
            error: Some(ComicError {
                code: code.to_string(),
                message: message.into(),
            }),
        }
    }

    /// The uniform not-a-comic-project answer (also the invalid-token answer,
    /// so a caller off the street learns nothing).
    pub fn not_comic_project() -> Self {
        Self::refused(
            ERROR_NOT_COMIC_PROJECT,
            "This session's working directory is not a comic project (no story/ root). \
             Open the comic workbench to create one, or ask the user which project to work in.",
        )
    }
}

/// The host side of the comic tool surface. Implemented by the production
/// backend (`crate::commands::comic::McpComicTools`) and by server/desktop
/// stubs when the group is off.
#[async_trait]
pub trait ComicAccess: Send + Sync {
    /// Route one comic tool call. `parent_connection_id` scopes event
    /// emission (which session's UI learns about the change); `working_dir`
    /// is the project folder from the validated token entry.
    async fn call(
        &self,
        parent_connection_id: &str,
        working_dir: &Path,
        tool: &str,
        args: &Value,
        cancel: tokio_util::sync::CancellationToken,
    ) -> ComicOutcome;
}

/// Answers every call with the unavailable refusal — for builds where the
/// comic backend is absent, mirroring `NoBrowserTabs`.
pub struct NoComicAccess;

#[async_trait]
impl ComicAccess for NoComicAccess {
    async fn call(
        &self,
        _parent_connection_id: &str,
        _working_dir: &Path,
        _tool: &str,
        _args: &Value,
        _cancel: tokio_util::sync::CancellationToken,
    ) -> ComicOutcome {
        ComicOutcome::refused(
            ERROR_UNAVAILABLE,
            "Comic tools are not available in this build.",
        )
    }
}

/// Convenience alias matching the listener's field type.
pub type SharedComicAccess = Arc<dyn ComicAccess>;
