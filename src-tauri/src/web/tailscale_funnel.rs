//! Tailscale Funnel management for the running Web service.

use super::WebServerState;
use crate::{app_error::AppCommandError, db::service::app_metadata_service};
use sea_orm::DatabaseConnection;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio_util::sync::CancellationToken;

mod cli;

const METADATA_KEY: &str = "web_service_tailscale_funnel";
type AuthCallback = Arc<dyn Fn(String) + Send + Sync>;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct Failure(&'static str);

#[async_trait::async_trait]
trait Executor: Send + Sync {
    fn installed(&self) -> bool;
    async fn run(
        &self,
        args: Vec<String>,
        cancel: CancellationToken,
        auth: AuthCallback,
        timeout: Duration,
    ) -> Result<String, Failure>;
}

use cli::SystemExecutor;

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FunnelStatus {
    pub state: String,
    pub installed: bool,
    pub enabled: bool,
    pub desired_enabled: bool,
    pub owned: bool,
    pub url: Option<String>,
    pub auth_url: Option<String>,
    pub error: Option<String>,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Preference {
    desired_enabled: bool,
    owned: Option<OwnedMapping>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
struct OwnedMapping {
    hostname: String,
    port: u16,
}

#[derive(Default)]
struct Runtime {
    loaded: bool,
    conn: Option<DatabaseConnection>,
    preference: Preference,
    error: Option<String>,
    auth_url: Option<String>,
    generation: u64,
    operation: Option<Operation>,
}

struct Operation {
    cancel: CancellationToken,
    task: tokio::task::JoinHandle<()>,
}

pub struct FunnelManager {
    executor: Arc<dyn Executor>,
    runtime: Mutex<Runtime>,
}

impl Default for FunnelManager {
    fn default() -> Self {
        Self {
            executor: Arc::new(SystemExecutor),
            runtime: Mutex::new(Runtime::default()),
        }
    }
}

fn command_error(code: &'static str) -> AppCommandError {
    AppCommandError::new(crate::app_error::AppErrorCode::ExternalCommandFailed, code)
}

impl FunnelManager {
    async fn initialize(&self, conn: &DatabaseConnection) -> Result<(), AppCommandError> {
        if self.runtime.lock().unwrap().loaded {
            return Ok(());
        }
        let preference = app_metadata_service::get_value(conn, METADATA_KEY)
            .await?
            .map(|text| {
                serde_json::from_str::<Preference>(&text)
                    .map_err(|_| command_error("invalidResponse"))
            })
            .transpose()?
            .unwrap_or_default();
        let mut runtime = self.runtime.lock().unwrap();
        runtime.preference = preference;
        runtime.conn = Some(conn.clone());
        runtime.loaded = true;
        Ok(())
    }

    async fn save(
        &self,
        conn: &DatabaseConnection,
        preference: Preference,
    ) -> Result<(), AppCommandError> {
        let text = serde_json::to_string(&preference).map_err(|_| command_error("failed"))?;
        app_metadata_service::upsert_value(conn, METADATA_KEY, &text).await?;
        self.runtime.lock().unwrap().preference = preference;
        Ok(())
    }

    fn preference(&self) -> Preference {
        self.runtime.lock().unwrap().preference.clone()
    }

    fn active(&self) -> bool {
        self.runtime
            .lock()
            .unwrap()
            .operation
            .as_ref()
            .is_some_and(|op| !op.task.is_finished())
    }

    fn set_error(&self, error: Option<&str>) {
        self.runtime.lock().unwrap().error = error.map(str::to_string);
    }

    fn base_status(&self, state: &str) -> FunnelStatus {
        let runtime = self.runtime.lock().unwrap();
        FunnelStatus {
            state: state.into(),
            installed: self.executor.installed(),
            enabled: false,
            desired_enabled: runtime.preference.desired_enabled,
            owned: runtime.preference.owned.is_some(),
            url: None,
            auth_url: runtime.auth_url.clone(),
            error: runtime.error.clone(),
        }
    }

    async fn json(&self, args: &[&str]) -> Result<Value, Failure> {
        let text = self
            .executor
            .run(
                args.iter().map(|v| (*v).to_string()).collect(),
                CancellationToken::new(),
                Arc::new(|_| {}),
                Duration::from_secs(8),
            )
            .await?;
        let value: Value = serde_json::from_str(&text).map_err(|_| Failure("invalidResponse"))?;
        if !value.is_object() {
            return Err(Failure("invalidResponse"));
        }
        Ok(value)
    }

    async fn node(&self) -> Result<(String, Option<String>), Failure> {
        let node = self.json(&["status", "--json"]).await?;
        let backend = node["BackendState"]
            .as_str()
            .ok_or(Failure("invalidResponse"))?
            .to_string();
        let hostname = node["Self"]["DNSName"]
            .as_str()
            .map(|name| name.trim_end_matches('.').to_ascii_lowercase())
            .filter(|name| valid_hostname(name));
        Ok((backend, hostname))
    }

    async fn refresh(&self, ws: &WebServerState) -> FunnelStatus {
        let mut status = self.base_status("ready");
        if !status.installed {
            status.state = "notInstalled".into();
            return status;
        }
        if self.active() {
            status.state = if status.auth_url.is_some() {
                "authorizing"
            } else {
                "starting"
            }
            .into();
            return status;
        }
        let (backend, hostname) = match self.node().await {
            Ok(value) => value,
            Err(Failure(code)) => {
                status.state = "unavailable".into();
                status.error = Some(code.into());
                return status;
            }
        };
        if backend != "Running" {
            status.state = match backend.as_str() {
                "NeedsLogin" | "NeedsMachineAuth" | "NoState" => "needsLogin",
                "Stopped" => "stopped",
                _ => "unavailable",
            }
            .into();
            return status;
        }
        let Some(hostname) = hostname else {
            status.state = "error".into();
            status.error = Some("invalidResponse".into());
            return status;
        };
        let config = match self.json(&["funnel", "status", "--json"]).await {
            Ok(value) => value,
            Err(Failure(code)) => {
                status.state = "error".into();
                status.error = Some(code.into());
                return status;
            }
        };
        // The service may have stopped during a CLI query; never publish stale addresses.
        let Some(port) = live_port(ws) else {
            status.state = "stopped".into();
            return status;
        };
        match inspect_mapping(&config, &hostname, port) {
            Mapping::Public => {
                status.state = "enabled".into();
                status.enabled = true;
                status.url = Some(format!("https://{hostname}"));
                status.owned = self
                    .preference()
                    .owned
                    .is_some_and(|own| own.hostname == hostname && own.port == port);
            }
            Mapping::Conflict => {
                status.state = "conflict".into();
            }
            Mapping::Empty if status.error.is_some() => {
                status.state = "error".into();
            }
            Mapping::Empty => {}
        }
        status
    }

    async fn cancel_task(&self) {
        let operation = {
            let mut runtime = self.runtime.lock().unwrap();
            runtime.generation += 1;
            runtime.auth_url = None;
            runtime.operation.take()
        };
        if let Some(operation) = operation {
            operation.cancel.cancel();
            // The executor owns its child until kill + wait has completed.
            let _ = operation.task.await;
        }
    }

    async fn cleanup(&self, conn: &DatabaseConnection) -> Result<(), Failure> {
        let mut preference = self.preference();
        let Some(owned) = preference.owned.clone() else {
            return Ok(());
        };
        if !valid_hostname(&owned.hostname) || owned.port == 0 {
            return Err(Failure("conflict"));
        }
        let config = self.json(&["funnel", "status", "--json"]).await?;
        match inspect_mapping(&config, &owned.hostname, owned.port) {
            Mapping::Conflict => return Err(Failure("conflict")),
            Mapping::Empty => {}
            Mapping::Public => {
                self.executor
                    .run(
                        vec!["serve".into(), "--https=443".into(), "off".into()],
                        CancellationToken::new(),
                        Arc::new(|_| {}),
                        Duration::from_secs(8),
                    )
                    .await?;
                let after = self.json(&["funnel", "status", "--json"]).await?;
                if occupies_https_port(&after) {
                    return Err(Failure("cleanupFailed"));
                }
            }
        }
        preference.owned = None;
        self.save(conn, preference)
            .await
            .map_err(|_| Failure("cleanupFailed"))
    }

    fn spawn_operation(
        self: &Arc<Self>,
        ws: WebServerState,
        args: Vec<String>,
        publishing: bool,
        interactive: bool,
    ) {
        let cancel = CancellationToken::new();
        let worker_cancel = cancel.clone();
        let mut runtime = self.runtime.lock().unwrap();
        runtime.generation += 1;
        let generation = runtime.generation;
        runtime.error = None;
        runtime.auth_url = None;
        let manager = self.clone();
        let auth_manager = self.clone();
        let auth_cancel = cancel.clone();
        let auth: AuthCallback = Arc::new(move |text| {
            if let Some(url) = authorization_url(&text) {
                let mut runtime = auth_manager.runtime.lock().unwrap();
                if runtime.generation == generation {
                    runtime.auth_url = Some(url);
                    if !interactive {
                        auth_cancel.cancel();
                    }
                }
            }
        });
        let task = tokio::spawn(async move {
            let mut result = manager
                .executor
                .run(
                    args,
                    worker_cancel.clone(),
                    auth,
                    Duration::from_secs(if interactive { 120 } else { 15 }),
                )
                .await;
            if result.is_ok() && publishing {
                result = async {
                    let (backend, hostname) = manager.node().await?;
                    let config = manager.json(&["funnel", "status", "--json"]).await?;
                    let port = live_port(&ws).ok_or(Failure("stopped"))?;
                    if backend != "Running"
                        || hostname.as_deref().is_none_or(|host| {
                            inspect_mapping(&config, host, port) != Mapping::Public
                        })
                    {
                        return Err(Failure("failed"));
                    }
                    Ok(String::new())
                }
                .await;
            }
            let mut runtime = manager.runtime.lock().unwrap();
            if runtime.generation == generation {
                runtime.error = result.err().map(|Failure(code)| code.to_string());
                if runtime.error.is_none() {
                    runtime.auth_url = None;
                }
            }
        });
        runtime.operation = Some(Operation { cancel, task });
    }
}

fn valid_hostname(hostname: &str) -> bool {
    hostname.ends_with(".ts.net")
        && hostname.len() <= 253
        && hostname.split('.').all(|label| {
            !label.is_empty()
                && label.len() <= 63
                && !label.starts_with('-')
                && !label.ends_with('-')
                && label
                    .bytes()
                    .all(|ch| ch.is_ascii_alphanumeric() || ch == b'-')
        })
}

fn live_port(ws: &WebServerState) -> Option<u16> {
    super::do_get_web_server_status(ws)
        .filter(|info| info.port != 0 && !info.token.trim().is_empty())
        .map(|info| info.port)
}

pub async fn status_core(
    ws: &WebServerState,
    conn: &DatabaseConnection,
) -> Result<FunnelStatus, AppCommandError> {
    let _guard = ws.lifecycle.lock().await;
    ws.funnel.initialize(conn).await?;
    Ok(ws.funnel.refresh(ws).await)
}

pub async fn login_core(
    ws: &WebServerState,
    conn: &DatabaseConnection,
) -> Result<FunnelStatus, AppCommandError> {
    let _guard = ws.lifecycle.lock().await;
    let manager = &ws.funnel;
    manager.initialize(conn).await?;
    if manager.active() {
        return Ok(manager.refresh(ws).await);
    }
    if !manager.executor.installed() {
        return Ok(manager.base_status("notInstalled"));
    }
    match manager.node().await {
        Ok((backend, _)) if backend == "Running" => return Ok(manager.refresh(ws).await),
        Err(Failure(code)) => {
            manager.set_error(Some(code));
            return Ok(manager.base_status("unavailable"));
        }
        _ => {}
    }
    manager.spawn_operation(
        ws.clone(),
        vec!["up".into(), "--timeout=120s".into()],
        false,
        true,
    );
    Ok(manager.base_status("starting"))
}

async fn enable_locked(
    ws: &WebServerState,
    conn: &DatabaseConnection,
    interactive: bool,
) -> Result<FunnelStatus, AppCommandError> {
    let manager = &ws.funnel;
    if manager.active() {
        return Ok(manager.refresh(ws).await);
    }
    let Some(port) = live_port(ws) else {
        manager.set_error(Some("stopped"));
        return Ok(manager.base_status("stopped"));
    };
    if !matches!(ws.host.lock().unwrap().as_str(), "0.0.0.0" | "127.0.0.1") {
        manager.set_error(Some("unsupportedBind"));
        return Ok(manager.base_status("error"));
    }
    if !manager.executor.installed() {
        return Ok(manager.base_status("notInstalled"));
    }
    let hostname = match manager.node().await {
        Ok((backend, Some(hostname))) if backend == "Running" => hostname,
        Ok(_) => {
            manager.set_error(Some("needsLogin"));
            return Ok(manager.base_status("needsLogin"));
        }
        Err(Failure(code)) => {
            manager.set_error(Some(code));
            return Ok(manager.base_status("unavailable"));
        }
    };
    let config = match manager.json(&["funnel", "status", "--json"]).await {
        Ok(config) => config,
        Err(Failure(code)) => {
            manager.set_error(Some(code));
            return Ok(manager.base_status("error"));
        }
    };
    let mut preference = manager.preference();
    if let Some(owned) = &preference.owned {
        if owned.hostname == hostname
            && owned.port == port
            && inspect_mapping(&config, &hostname, port) == Mapping::Public
        {
            preference.desired_enabled = true;
            manager.save(conn, preference).await?;
            manager.set_error(None);
            return Ok(manager.refresh(ws).await);
        }
        if let Err(Failure(code)) = manager.cleanup(conn).await {
            manager.set_error(Some(code));
            return Ok(manager.base_status("conflict"));
        }
    } else if inspect_mapping(&config, &hostname, port) != Mapping::Empty {
        manager.set_error(Some("conflict"));
        return Ok(manager.base_status("conflict"));
    }
    manager
        .save(
            conn,
            Preference {
                desired_enabled: true,
                owned: Some(OwnedMapping { hostname, port }),
            },
        )
        .await?;
    manager.spawn_operation(
        ws.clone(),
        vec![
            "funnel".into(),
            "--bg".into(),
            "--https=443".into(),
            "--yes".into(),
            format!("http://127.0.0.1:{port}"),
        ],
        true,
        interactive,
    );
    Ok(manager.base_status("starting"))
}

pub async fn enable_core(
    ws: &WebServerState,
    conn: &DatabaseConnection,
) -> Result<FunnelStatus, AppCommandError> {
    let _guard = ws.lifecycle.lock().await;
    ws.funnel.initialize(conn).await?;
    enable_locked(ws, conn, true).await
}

pub async fn disable_core(
    ws: &WebServerState,
    conn: &DatabaseConnection,
) -> Result<FunnelStatus, AppCommandError> {
    let _guard = ws.lifecycle.lock().await;
    let manager = &ws.funnel;
    manager.initialize(conn).await?;
    manager.cancel_task().await;
    let mut preference = manager.preference();
    let was_owned = preference.owned.is_some();
    preference.desired_enabled = false;
    manager.save(conn, preference).await?;
    manager.set_error(None);
    if let Err(Failure(code)) = manager.cleanup(conn).await {
        manager.set_error(Some(code));
    }
    let mut status = manager.refresh(ws).await;
    if status.enabled && !was_owned {
        status.error = Some("notOwned".into());
    }
    Ok(status)
}

pub async fn cancel_core(
    ws: &WebServerState,
    conn: &DatabaseConnection,
) -> Result<FunnelStatus, AppCommandError> {
    disable_core(ws, conn).await
}

pub async fn initialize(
    ws: &WebServerState,
    conn: &DatabaseConnection,
) -> Result<(), AppCommandError> {
    let _guard = ws.lifecycle.lock().await;
    ws.funnel.initialize(conn).await?;
    if live_port(ws).is_none() {
        if let Err(Failure(code)) = ws.funnel.cleanup(conn).await {
            ws.funnel.set_error(Some("cleanupFailed"));
            return Err(command_error(code));
        }
    }
    Ok(())
}

pub fn restore(ws: WebServerState, conn: DatabaseConnection) {
    // Store synchronously so an immediate stop can load/clean persisted ownership
    // even if the restore task has not acquired the lifecycle lock yet.
    ws.funnel.runtime.lock().unwrap().conn = Some(conn.clone());
    tokio::spawn(async move {
        let _guard = ws.lifecycle.lock().await;
        let manager = &ws.funnel;
        if manager.initialize(&conn).await.is_err() {
            manager.set_error(Some("failed"));
            return;
        }
        if live_port(&ws).is_none() || !manager.preference().desired_enabled {
            if let Err(Failure(code)) = manager.cleanup(&conn).await {
                manager.set_error(Some(code));
            }
            return;
        }
        if enable_locked(&ws, &conn, false).await.is_err() {
            manager.set_error(Some("failed"));
        }
    });
}

pub async fn shutdown(ws: &WebServerState) -> Result<(), AppCommandError> {
    let manager = &ws.funnel;
    let conn = manager.runtime.lock().unwrap().conn.clone();
    let Some(conn) = conn else {
        return Ok(());
    };
    manager.initialize(&conn).await?;
    manager.cancel_task().await;
    match manager.cleanup(&conn).await {
        Ok(()) => {
            manager.set_error(None);
            Ok(())
        }
        Err(_) => {
            manager.set_error(Some("cleanupFailed"));
            Err(command_error("cleanupFailed"))
        }
    }
}

#[cfg(test)]
#[path = "tailscale_funnel/tests.rs"]
mod manager_tests;

#[derive(Debug, PartialEq, Eq)]
enum Mapping {
    Empty,
    Public,
    Conflict,
}

fn occupies_https_port(config: &Value) -> bool {
    config.get("TCP").and_then(|v| v.get("443")).is_some()
        || ["Web", "AllowFunnel"].iter().any(|key| {
            config
                .get(key)
                .and_then(Value::as_object)
                .is_some_and(|map| map.keys().any(|host| host.ends_with(":443")))
        })
}

fn inspect_mapping(config: &Value, hostname: &str, port: u16) -> Mapping {
    if config
        .get("Foreground")
        .and_then(Value::as_object)
        .is_some_and(|map| map.values().any(occupies_https_port))
    {
        return Mapping::Conflict;
    }
    if !occupies_https_port(config) {
        return Mapping::Empty;
    }
    let host = format!("{hostname}:443");
    let target = format!("http://127.0.0.1:{port}");
    let tcp = &config["TCP"]["443"];
    let handlers = config["Web"][&host]["Handlers"].as_object();
    let own_host_only = ["Web", "AllowFunnel"].iter().all(|key| {
        config[*key]
            .as_object()
            .is_none_or(|map| map.keys().all(|key| !key.ends_with(":443") || key == &host))
    });
    if own_host_only
        && tcp["HTTPS"] == true
        && tcp.get("TCPForward").is_none_or(|v| v == "")
        && config["AllowFunnel"][&host] == true
        && handlers.is_some_and(|map| {
            map.len() == 1
                && map.get("/").is_some_and(|handler| {
                    handler["Proxy"]
                        .as_str()
                        .is_some_and(|proxy| proxy.trim_end_matches('/') == target)
                        && ["Path", "Text", "Redirect"]
                            .iter()
                            .all(|key| handler.get(*key).is_none_or(|v| v == ""))
                })
        })
    {
        Mapping::Public
    } else {
        Mapping::Conflict
    }
}

fn authorization_url(text: &str) -> Option<String> {
    text.split_whitespace().find_map(|word| {
        let candidate = word.trim_matches(|c: char| matches!(c, '\"' | '\'' | ',' | '(' | ')'));
        let url = url::Url::parse(candidate).ok()?;
        (url.scheme() == "https"
            && url.username().is_empty()
            && url.password().is_none()
            && url.port().is_none()
            && matches!(
                url.host_str(),
                Some("login.tailscale.com" | "tailscale.com")
            )
            && (url.path().starts_with("/a/")
                || url.path().starts_with("/f/")
                || url.path().starts_with("/admin/")
                || url.path().starts_with("/s/")))
        .then(|| url.to_string())
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn public_config() -> serde_json::Value {
        json!({
            "TCP": {"443": {"HTTPS": true}},
            "Web": {"workstation.example.ts.net:443": {"Handlers": {
                "/": {"Proxy": "http://127.0.0.1:3080"}
            }}},
            "AllowFunnel": {"workstation.example.ts.net:443": true}
        })
    }

    #[test]
    fn recognizes_only_public_mapping_to_current_service() {
        let config = public_config();
        assert_eq!(
            inspect_mapping(&config, "workstation.example.ts.net", 3080),
            Mapping::Public
        );
        assert_eq!(
            inspect_mapping(&config, "workstation.example.ts.net", 3090),
            Mapping::Conflict
        );
        let mut private = config.clone();
        private["AllowFunnel"] = json!({});
        assert_eq!(
            inspect_mapping(&private, "workstation.example.ts.net", 3080),
            Mapping::Conflict
        );
        assert_eq!(
            inspect_mapping(&json!({}), "workstation.example.ts.net", 3080),
            Mapping::Empty
        );
    }

    #[test]
    fn refuses_mixed_handlers_and_foreground_configuration() {
        let mut config = public_config();
        config["Web"]["workstation.example.ts.net:443"]["Handlers"]["/other"] =
            json!({"Proxy":"http://127.0.0.1:9000"});
        assert_eq!(
            inspect_mapping(&config, "workstation.example.ts.net", 3080),
            Mapping::Conflict
        );
        assert_eq!(
            inspect_mapping(
                &json!({"Foreground":{"session":public_config()}}),
                "workstation.example.ts.net",
                3080
            ),
            Mapping::Conflict
        );
    }

    #[test]
    fn ignores_other_ports_without_removing_them() {
        let config = json!({"TCP":{"8443":{"HTTPS":true}},"Web":{"workstation.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9000"}}}}});
        assert_eq!(
            inspect_mapping(&config, "workstation.example.ts.net", 3080),
            Mapping::Empty
        );
    }

    #[test]
    fn accepts_only_tailscale_https_authorization_links() {
        assert_eq!(
            authorization_url("To authenticate: https://login.tailscale.com/a/example\n"),
            Some("https://login.tailscale.com/a/example".to_string())
        );
        for bad in [
            "http://login.tailscale.com/a/example",
            "https://login.tailscale.com.evil.test/a/x",
            "https://evil.test/?next=https://login.tailscale.com/a/x",
            "https://user@login.tailscale.com/a/x",
        ] {
            assert_eq!(authorization_url(bad), None);
        }
    }
}
