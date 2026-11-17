//! The ZCode bridge's provider list, for the settings page's provider dropdown.
//!
//! The zcode-acp bridge resolves its GLM provider at process start: it scans
//! the ZCode desktop app's `~/.zcode/v2/config.json` provider table — "first
//! enabled provider wins", with keyless builtins skipped — optionally overlaid
//! with the accounts saved by `zcode-acp setup` (`acp-accounts.json`), and the
//! `ZCODE_PROVIDER` bootstrap env pins one of those ids instead. The pin must
//! ride the launch env: the bridge reads it before any config file is
//! loadable, so codeg's ZCode provider dropdown writes it into the agent's
//! `env_json`, and a new value takes effect on the next connection only.
//!
//! This module mirrors the bridge's scan (upstream `credentials.ts` +
//! `providerSelectable`) so the dropdown offers the same ids the bridge can
//! pin — WITHOUT the keys themselves: only whether one is set. The list has no
//! canonical order (the bridge scans config.json in file order, which
//! `serde_json::Map` cannot reproduce without `preserve_order`), so nothing
//! here claims to know which provider the bridge picks by default; the UI's
//! "auto" entry leaves the pin unset and the bridge keeps its own rule.
//!
//! Files that are missing or unreadable yield an empty list rather than an
//! error: a settings dropdown degrades to the "auto" entry plus the panel's
//! existing install/sign-in hint, and the bridge itself fails loudly later if
//! the desktop credentials are genuinely broken.

use std::collections::BTreeMap;
use std::path::PathBuf;

use serde::Deserialize;
use serde::Serialize;
use url::Url;

#[cfg(feature = "tauri-runtime")]
use tauri::State;

use crate::acp::error::AcpError;
use crate::db::AppDatabase;
use crate::db::service::agent_setting_service;
use crate::models::agent::AgentType;

/// One provider the ZCode bridge can pin through `ZCODE_PROVIDER`.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct ZcodeProviderOption {
    /// The `ZCODE_PROVIDER` value: config.json's provider key verbatim
    /// (`ai-switch-codex`, `builtin:zai-coding-plan`, ...).
    pub id: String,
    pub name: String,
    pub enabled: bool,
    pub has_api_key: bool,
    /// The entry declares `apiKeyRequired: false` — a custom provider that
    /// authenticates keyless (e.g. a hosted gateway with no auth).
    pub key_optional: bool,
    /// The endpoint is localhost-style (127.0.0.1/localhost/[::1]) — usable
    /// keyless, like an Ollama or llama.cpp provider.
    pub local_base_url: bool,
    pub model_count: usize,
    /// Whether the bridge's default scan would offer this entry to a client
    /// (`providerSelectable`): enabled builtins with a key, and customs that
    /// are not disabled and have a key, an explicit key waiver, or a local
    /// endpoint. An explicit `ZCODE_PROVIDER` pin bypasses this and is
    /// honored regardless.
    pub selectable: bool,
}

#[derive(Debug, Deserialize)]
struct ZcodeCredentialConfig {
    /// Raw entries: `serde_json::Map` only iterates over `Value`, so each
    /// provider row is converted separately in the scan.
    #[serde(default)]
    provider: serde_json::Map<String, serde_json::Value>,
}

#[derive(Debug, Deserialize)]
struct ZcodeProviderEntry {
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    enabled: Option<bool>,
    #[serde(default)]
    options: Option<ZcodeProviderOptions>,
    #[serde(default)]
    models: Option<serde_json::Map<String, serde_json::Value>>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ZcodeProviderOptions {
    #[serde(default)]
    api_key: Option<String>,
    #[serde(default)]
    api_key_required: Option<bool>,
    // config.json spells it `baseURL` (all caps URL); `baseUrl` keeps the
    // provider_config.json spelling accepted too.
    #[serde(default, rename = "baseURL", alias = "baseUrl")]
    base_url: Option<String>,
}

/// `~/.zcode/v2/acp-accounts.json` — the keys `zcode-acp setup` saves. One
/// entry per coding-plan family; the bridge overlays it onto config.json as a
/// forced `builtin:<family>-coding-plan` entry.
#[derive(Debug, Deserialize)]
struct ZcodeBridgeAccounts {
    #[serde(default)]
    accounts: serde_json::Map<String, serde_json::Value>,
}

/// Resolve the ZCode data root the way the bridge does: a non-empty
/// `ZCODE_HOME` is used verbatim (the agent's own env wins — it reaches the
/// bridge through the launch env), falling back to `~/.zcode`.
fn zcode_home_dir(agent_env: &BTreeMap<String, String>) -> PathBuf {
    let from_process = std::env::var("ZCODE_HOME").ok();
    let configured = agent_env
        .get("ZCODE_HOME")
        .map(String::as_str)
        .or(from_process.as_deref())
        .filter(|raw| !raw.is_empty());
    match configured {
        Some(explicit) => PathBuf::from(explicit),
        None => dirs::home_dir()
            .unwrap_or_else(|| PathBuf::from("~"))
            .join(".zcode"),
    }
}

fn non_empty(value: &Option<String>) -> bool {
    value.as_deref().is_some_and(|raw| !raw.is_empty())
}

/// Upstream `isLocalBaseURL`: localhost-style endpoints run keyless.
fn is_local_base_url(base_url: Option<&String>) -> bool {
    base_url
        .and_then(|raw| Url::parse(raw.trim()).ok())
        .is_some_and(|url| {
            matches!(
                url.host_str(),
                Some("localhost") | Some("127.0.0.1") | Some("[::1]")
            )
        })
}

/// Upstream `providerSelectable`: whether the bridge's default scan offers the
/// entry. An explicit pin (`ZCODE_PROVIDER`) is honored even when this is
/// false — the scan's keyless-builtin skip is what a pin overrides.
fn provider_selectable(id: &str, entry: &ZcodeProviderEntry) -> bool {
    let options = entry.options.as_ref();
    if id.starts_with("builtin:") {
        return entry.enabled == Some(true)
            && options.is_some_and(|options| non_empty(&options.api_key));
    }
    if entry.enabled == Some(false) {
        return false;
    }
    options.is_some_and(|options| {
        non_empty(&options.api_key)
            || options.api_key_required == Some(false)
            || is_local_base_url(options.base_url.as_ref())
    })
}

fn fill_row(row: &mut ZcodeProviderOption, entry: &ZcodeProviderEntry) {
    if let Some(options) = entry.options.as_ref() {
        row.has_api_key = non_empty(&options.api_key);
        row.key_optional = options.api_key_required == Some(false);
        row.local_base_url = is_local_base_url(options.base_url.as_ref());
    }
    row.enabled = entry.enabled == Some(true);
    row.model_count = entry.models.as_ref().map_or(0, |models| models.len());
    row.selectable = provider_selectable(&row.id, entry);
}

fn row(id: &str, name: Option<&String>) -> ZcodeProviderOption {
    ZcodeProviderOption {
        id: id.to_string(),
        name: name.cloned().unwrap_or_else(|| id.to_string()),
        enabled: false,
        has_api_key: false,
        key_optional: false,
        local_base_url: false,
        model_count: 0,
        selectable: false,
    }
}

/// List the providers the bridge can pin, config.json first (its keys are the
/// `ZCODE_PROVIDER` targets), then the `acp-accounts.json` overlays. The
/// desktop entry's fields win for overlaid families — the bridge keeps the
/// desktop's per-model data and only owns the key, endpoint and enablement.
fn list_zcode_providers(agent_env: &BTreeMap<String, String>) -> Vec<ZcodeProviderOption> {
    let home = zcode_home_dir(agent_env);
    let mut rows: Vec<ZcodeProviderOption> = Vec::new();
    let config = std::fs::read_to_string(home.join("v2").join("config.json"))
        .ok()
        .and_then(|raw| serde_json::from_str::<ZcodeCredentialConfig>(&raw).ok());
    if let Some(config) = config.as_ref() {
        for (id, raw_entry) in config.provider.iter() {
            let Ok(entry) = serde_json::from_value::<ZcodeProviderEntry>(raw_entry.clone())
            else {
                continue;
            };
            let mut entry_row = row(id, entry.name.as_ref());
            fill_row(&mut entry_row, &entry);
            rows.push(entry_row);
        }
    }
    let accounts = std::fs::read_to_string(home.join("v2").join("acp-accounts.json"))
        .ok()
        .and_then(|raw| serde_json::from_str::<ZcodeBridgeAccounts>(&raw).ok());
    if let Some(accounts) = accounts {
        for family in accounts.accounts.keys() {
            let id = format!("builtin:{family}-coding-plan");
            if let Some(existing) = rows.iter_mut().find(|existing| existing.id == id) {
                // The bridge overrides the desktop entry's key, endpoint and
                // enablement in memory — reflect that on the shared row.
                existing.enabled = true;
                existing.has_api_key = true;
                existing.selectable = true;
                continue;
            }
            let mut overlay_row = row(&id, None);
            overlay_row.enabled = true;
            overlay_row.has_api_key = true;
            overlay_row.selectable = true;
            rows.push(overlay_row);
        }
    }
    rows
}

/// The shared body of the provider-list command (Tauri) and handler (web).
pub async fn acp_zcode_list_providers_core(
    db: &AppDatabase,
) -> Result<Vec<ZcodeProviderOption>, AcpError> {
    let setting = agent_setting_service::get_by_agent_type(&db.conn, AgentType::ZCode)
        .await
        .map_err(|e| AcpError::protocol(e.to_string()))?;
    let agent_env = setting
        .as_ref()
        .and_then(|model| model.env_json.as_deref())
        .and_then(|raw| serde_json::from_str::<BTreeMap<String, String>>(raw).ok())
        .unwrap_or_default();
    Ok(list_zcode_providers(&agent_env))
}

/// The provider ids the ZCode bridge can pin through `ZCODE_PROVIDER`, for the
/// settings page's dropdown. Never carries a key — only whether one is set.
/// Desktop command; server mode exposes the same body through the web handler.
#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn acp_zcode_list_providers(
    db: State<'_, AppDatabase>,
) -> Result<Vec<ZcodeProviderOption>, AcpError> {
    acp_zcode_list_providers_core(&db).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::path::Path;
    use std::time::{SystemTime, UNIX_EPOCH};

    /// A throwaway home directory with a `v2` layout, as a standalone temp dir
    /// keeps the tests independent of the developer's real `~/.zcode`.
    fn temp_home(tag: &str) -> PathBuf {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|since| since.as_nanos())
            .unwrap_or_default();
        let home = std::env::temp_dir().join(format!(
            "codeg-zcode-providers-{tag}-{}-{unique}",
            std::process::id()
        ));
        fs::create_dir_all(home.join("v2")).expect("temp v2 dir");
        home
    }

    fn write_config(home: &Path, provider: &str) {
        fs::write(
            home.join("v2").join("config.json"),
            format!(r#"{{"provider": {provider}}}"#),
        )
        .expect("config.json");
    }

    fn write_accounts(home: &Path, accounts: &str) {
        fs::write(
            home.join("v2").join("acp-accounts.json"),
            format!(r#"{{"version": 1, "accounts": {accounts}}}"#),
        )
        .expect("acp-accounts.json");
    }

    fn env_with_home(home: &Path) -> BTreeMap<String, String> {
        BTreeMap::from([(
            "ZCODE_HOME".to_string(),
            home.to_string_lossy().into_owned(),
        )])
    }

    fn ids(rows: &[ZcodeProviderOption]) -> Vec<&str> {
        rows.iter().map(|row| row.id.as_str()).collect()
    }

    #[test]
    fn builtin_rows_need_enabled_and_a_key() {
        let home = temp_home("builtin");
        write_config(
            &home,
            r#"{
                "builtin:zai-coding-plan": {
                    "name": "Z.ai - Coding Plan", "enabled": true,
                    "options": {"apiKey": "k", "baseURL": "https://api.z.ai/api/anthropic"},
                    "models": {"glm-5.3": {}, "glm-5.3-flash": {}}
                },
                "builtin:bigmodel-start-plan": {
                    "name": "BigModel", "enabled": false,
                    "options": {"baseURL": "https://zcode.z.ai/api/v1/zcode-plan/anthropic"}
                },
                "builtin:zai": {
                    "name": "Z.ai - API Key", "enabled": true,
                    "options": {"baseURL": "https://api.z.ai/api/anthropic"}
                }
            }"#,
        );
        let rows = list_zcode_providers(&env_with_home(&home));
        assert_eq!(
            ids(&rows),
            ["builtin:zai-coding-plan", "builtin:bigmodel-start-plan", "builtin:zai"]
        );
        let plan = &rows[0];
        assert!(plan.selectable && plan.has_api_key && plan.enabled && plan.model_count == 2);
        // The desktop marks a keyless builtin 未启用; the scan skips it.
        assert!(!rows[1].selectable);
        assert!(
            !rows[2].selectable,
            "enabled builtin with no key is not selectable"
        );
    }

    #[test]
    fn custom_rows_run_keyless_only_locally_or_waived() {
        let home = temp_home("custom");
        write_config(
            &home,
            r#"{
                "ai-switch": {
                    "name": "AI Switch", "enabled": true,
                    "options": {"apiKey": "sk-1", "baseURL": "http://127.0.0.1:19527/v1"},
                    "models": {"gpt-6-astra": {}}
                },
                "local-keyless": {
                    "name": "Ollama", "enabled": true,
                    "options": {"baseURL": "http://localhost:11434/v1"},
                    "models": {"qwen3.8": {}}
                },
                "remote-keyless": {
                    "name": "Gateway", "enabled": true,
                    "options": {"baseURL": "https://gw.example/v1"}
                },
                "waived": {
                    "name": "Keyless Gateway", "enabled": true,
                    "options": {"baseURL": "https://api.example/v1", "apiKeyRequired": false}
                },
                "disabled": {
                    "name": "Off", "enabled": false,
                    "options": {"apiKey": "k", "baseURL": "http://127.0.0.1:1/v1"}
                }
            }"#,
        );
        let rows = list_zcode_providers(&env_with_home(&home));
        let selectable_of = |id: &str| {
            rows.iter()
                .find(|row| row.id == id)
                .map(|row| row.selectable)
                .unwrap_or(false)
        };
        // All five stay listable — an explicit pin overrides the scan's skips.
        assert_eq!(rows.len(), 5);
        assert!(selectable_of("ai-switch"));
        assert!(selectable_of("local-keyless"));
        assert!(!selectable_of("remote-keyless"));
        assert!(selectable_of("waived"));
        assert!(!selectable_of("disabled"), "disabled custom never runs");
        let local = rows
            .iter()
            .find(|row| row.id == "local-keyless")
            .unwrap();
        assert!(local.local_base_url && !local.has_api_key && local.model_count == 1);
    }

    #[test]
    fn bridge_accounts_force_their_family_entry() {
        let home = temp_home("overlay");
        write_config(
            &home,
            r#"{
                "builtin:zai-coding-plan": {
                    "name": "Z.ai - Coding Plan", "enabled": false,
                    "options": {"apiKey": "old"}, "models": {"glm-5.3": {}}
                },
                "ai-switch": {"name": "AI Switch", "options": {"apiKey": "sk"}}
            }"#,
        );
        write_accounts(&home, r#"{"bigmodel": {"apiKey": "new"}}"#);
        let rows = list_zcode_providers(&env_with_home(&home));
        assert_eq!(
            ids(&rows),
            [
                "builtin:zai-coding-plan",
                "ai-switch",
                "builtin:bigmodel-coding-plan"
            ]
        );
        // The overlay owns key, endpoint and enablement IN MEMORY.
        assert!(rows[2].enabled && rows[2].has_api_key && rows[2].selectable);
        assert_eq!(rows[2].name, "builtin:bigmodel-coding-plan");
        assert!(rows[0].has_api_key && rows[0].model_count == 1);
    }

    #[test]
    fn missing_or_broken_files_yield_an_empty_list() {
        let home = temp_home("empty");
        assert!(list_zcode_providers(&env_with_home(&home)).is_empty());
        fs::write(home.join("v2").join("config.json"), "{not json").expect("broken config");
        assert!(list_zcode_providers(&env_with_home(&home)).is_empty());
    }

    #[test]
    fn agent_env_zcode_home_wins_over_the_process_env() {
        let home = temp_home("pin");
        write_config(
            &home,
            r#"{"only": {"name": "Only", "enabled": true, "options": {"apiKey": "k"}}}"#,
        );
        // A stale process-level override must not shadow the agent's own. The
        // other tests in this module always pass ZCODE_HOME in the agent env,
        // so the process mutation below cannot race one of their reads.
        std::env::set_var(
            "ZCODE_HOME",
            home.join("elsewhere").to_string_lossy().as_ref(),
        );
        let rows = list_zcode_providers(&env_with_home(&home));
        std::env::remove_var("ZCODE_HOME");
        assert_eq!(ids(&rows), ["only"]);
    }

    #[test]
    fn rows_never_carry_the_key() {
        let home = temp_home("nosecret");
        write_config(
            &home,
            r#"{"p": {"name": "P", "enabled": true, "options": {"apiKey": "super-secret"}}}"#,
        );
        let rows = list_zcode_providers(&env_with_home(&home));
        assert!(rows[0].has_api_key);
        assert!(
            !serde_json::to_string(&rows)
                .unwrap()
                .contains("super-secret")
        );
    }
}
