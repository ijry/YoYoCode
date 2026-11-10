use super::*;
use sea_orm::{ConnectionTrait, Database, Schema};
use serde_json::json;
use std::sync::atomic::{AtomicBool, Ordering};
use tokio::sync::Notify;

const DNS: &str = "workstation.example.ts.net";

fn public_config(port: u16) -> Value {
    json!({"TCP":{"443":{"HTTPS":true}}, "Web":{format!("{DNS}:443"):{"Handlers":{"/":{"Proxy":format!("http://127.0.0.1:{port}")}}}}, "AllowFunnel":{format!("{DNS}:443"):true}})
}

#[derive(Default)]
struct FakeCli {
    data: Mutex<FakeData>,
    wait: AtomicBool,
    entered: Notify,
    release: Notify,
}

struct FakeData {
    installed: bool,
    backend: String,
    hostname: String,
    config: Value,
    apply: bool,
    fail: Option<Failure>,
    calls: Vec<Vec<String>>,
}

impl Default for FakeData {
    fn default() -> Self {
        Self {
            installed: true,
            backend: "Running".into(),
            hostname: format!("{DNS}."),
            config: json!({}),
            apply: true,
            fail: None,
            calls: vec![],
        }
    }
}

#[async_trait::async_trait]
impl Executor for FakeCli {
    fn installed(&self) -> bool {
        self.data.lock().unwrap().installed
    }

    async fn run(
        &self,
        args: Vec<String>,
        cancel: CancellationToken,
        auth: AuthCallback,
        _timeout: Duration,
    ) -> Result<String, Failure> {
        self.data.lock().unwrap().calls.push(args.clone());
        if args == ["status", "--json"] {
            let data = self.data.lock().unwrap();
            return Ok(
                json!({"BackendState":data.backend, "Self":{"DNSName":data.hostname}}).to_string(),
            );
        }
        if args == ["funnel", "status", "--json"] {
            return Ok(self.data.lock().unwrap().config.to_string());
        }
        if args.first().is_some_and(|arg| arg == "serve") {
            assert_eq!(
                args,
                ["serve", "--https=443", "off"],
                "cleanup must not bypass confirmation for newly added handlers"
            );
            let mut data = self.data.lock().unwrap();
            if let Some(failure) = data.fail {
                return Err(failure);
            }
            for key in ["TCP", "Web", "AllowFunnel"] {
                if let Some(map) = data.config[key].as_object_mut() {
                    map.retain(|key, _| {
                        if key == "443" {
                            false
                        } else {
                            !key.ends_with(":443")
                        }
                    });
                }
            }
            return Ok(String::new());
        }
        let is_up = args == ["up", "--timeout=120s"];
        if !is_up {
            assert_eq!(&args[..4], &["funnel", "--bg", "--https=443", "--yes"]);
            let port = args[4]
                .strip_prefix("http://127.0.0.1:")
                .unwrap()
                .parse()
                .unwrap();
            let mut data = self.data.lock().unwrap();
            if data.apply {
                data.config = public_config(port);
            }
        }
        if self.wait.load(Ordering::SeqCst) {
            auth("https://login.tailscale.com/f/test-authorization".into());
            self.entered.notify_one();
            tokio::select! {
                _ = cancel.cancelled() => return Err(Failure("cancelled")),
                _ = self.release.notified() => {}
            }
        }
        if let Some(failure) = self.data.lock().unwrap().fail {
            return Err(failure);
        }
        if is_up {
            self.data.lock().unwrap().backend = "Running".into();
        }
        Ok(String::new())
    }
}

async fn database() -> DatabaseConnection {
    let conn = Database::connect("sqlite::memory:").await.unwrap();
    let schema = Schema::new(conn.get_database_backend());
    let table = schema.create_table_from_entity(crate::db::entities::app_metadata::Entity);
    conn.execute(conn.get_database_backend().build(&table))
        .await
        .unwrap();
    conn
}

fn server(cli: Arc<FakeCli>, port: u16) -> WebServerState {
    let mut ws = WebServerState::new();
    ws.funnel = Arc::new(FunnelManager {
        executor: cli,
        ..Default::default()
    });
    ws.mark_externally_running("127.0.0.1".into(), port, "test-token".into());
    ws
}

async fn finished(ws: &WebServerState, conn: &DatabaseConnection) -> FunnelStatus {
    tokio::time::timeout(Duration::from_secs(3), async {
        loop {
            let status = status_core(ws, conn).await.unwrap();
            if !matches!(status.state.as_str(), "starting" | "authorizing") {
                return status;
            }
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("operation did not settle")
}

#[tokio::test]
async fn setup_reports_distinct_prerequisites_and_can_connect_a_stopped_node() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    let ws = server(cli.clone(), 3080);
    for (backend, prerequisite) in [
        ("NeedsLogin", "login"),
        ("NeedsMachineAuth", "approveDevice"),
        ("Stopped", "connect"),
    ] {
        cli.data.lock().unwrap().backend = backend.into();
        let status = serde_json::to_value(status_core(&ws, &conn).await.unwrap()).unwrap();
        assert_eq!(status["prerequisite"], prerequisite);
    }
    login_core(&ws, &conn).await.unwrap();
    assert_eq!(finished(&ws, &conn).await.state, "ready");
    assert!(cli
        .data
        .lock()
        .unwrap()
        .calls
        .iter()
        .any(|args| args == &["up", "--timeout=120s"]));
    ws.running.store(false, Ordering::Release);
    let status = serde_json::to_value(status_core(&ws, &conn).await.unwrap()).unwrap();
    assert_eq!(status["prerequisite"], "startWebService");
    cli.data.lock().unwrap().installed = false;
    let status = serde_json::to_value(status_core(&ws, &conn).await.unwrap()).unwrap();
    assert_eq!(status["prerequisite"], "install");
}

#[tokio::test]
async fn running_without_dns_does_not_request_login() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    cli.data.lock().unwrap().hostname.clear();
    let ws = server(cli.clone(), 3080);
    for status in [
        status_core(&ws, &conn).await.unwrap(),
        enable_core(&ws, &conn).await.unwrap(),
    ] {
        let wire = serde_json::to_value(status).unwrap();
        assert_eq!(wire["state"], "error");
        assert_eq!(wire["prerequisite"], "configureDns");
        assert_eq!(wire["error"], "dnsNotReady");
    }
    assert!(!cli
        .data
        .lock()
        .unwrap()
        .calls
        .iter()
        .any(|args| args.contains(&"up".into()) || args.contains(&"--bg".into())));
}

#[tokio::test]
async fn device_approval_does_not_repeat_login() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    cli.data.lock().unwrap().backend = "NeedsMachineAuth".into();
    let ws = server(cli.clone(), 3080);
    let login = login_core(&ws, &conn).await.unwrap();
    assert_eq!(
        serde_json::to_value(login).unwrap()["prerequisite"],
        "approveDevice"
    );
    let enable = enable_core(&ws, &conn).await.unwrap();
    assert_eq!(
        serde_json::to_value(enable).unwrap()["prerequisite"],
        "approveDevice"
    );
    assert!(!cli
        .data
        .lock()
        .unwrap()
        .calls
        .iter()
        .any(|args| args.contains(&"up".into()) || args.contains(&"--bg".into())));
}

#[tokio::test]
async fn reports_backend_and_missing_installation_without_exposing_private_serve() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    let ws = server(cli.clone(), 3080);
    for (backend, expected) in [
        ("NeedsLogin", "needsLogin"),
        ("Stopped", "stopped"),
        ("Running", "ready"),
    ] {
        cli.data.lock().unwrap().backend = backend.into();
        assert_eq!(status_core(&ws, &conn).await.unwrap().state, expected);
    }
    cli.data.lock().unwrap().installed = false;
    assert_eq!(status_core(&ws, &conn).await.unwrap().state, "notInstalled");
    cli.data.lock().unwrap().installed = true;
    cli.data.lock().unwrap().config = public_config(3080);
    cli.data.lock().unwrap().config["AllowFunnel"] = json!({});
    let status = status_core(&ws, &conn).await.unwrap();
    assert!(!status.enabled);
    assert_eq!(status.state, "conflict");
}

#[tokio::test]
async fn never_adopts_or_removes_unowned_matching_mapping() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    cli.data.lock().unwrap().config = public_config(3080);
    let ws = server(cli.clone(), 3080);
    let initial = status_core(&ws, &conn).await.unwrap();
    assert!(initial.enabled && !initial.owned);
    enable_core(&ws, &conn).await.unwrap();
    let status = finished(&ws, &conn).await;
    assert_eq!(status.error.as_deref(), Some("conflict"));
    let status = disable_core(&ws, &conn).await.unwrap();
    assert_eq!(status.error.as_deref(), Some("notOwned"));
    assert_eq!(cli.data.lock().unwrap().config, public_config(3080));
    assert!(!cli
        .data
        .lock()
        .unwrap()
        .calls
        .iter()
        .any(|args| args.contains(&"--bg".into()) || args.contains(&"off".into())));
}

#[tokio::test]
async fn streams_authorization_and_cancels_with_cleanup_and_persistence() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    cli.wait.store(true, Ordering::SeqCst);
    let ws = server(cli.clone(), 3080);
    assert_eq!(enable_core(&ws, &conn).await.unwrap().state, "starting");
    tokio::time::timeout(Duration::from_secs(3), cli.entered.notified())
        .await
        .unwrap();
    let status = status_core(&ws, &conn).await.unwrap();
    assert_eq!(status.state, "authorizing");
    assert_eq!(
        status.auth_url.as_deref(),
        Some("https://login.tailscale.com/f/test-authorization")
    );
    let saved: Value = serde_json::from_str(
        &app_metadata_service::get_value(&conn, METADATA_KEY)
            .await
            .unwrap()
            .unwrap(),
    )
    .unwrap();
    assert_eq!(saved["desiredEnabled"], true);
    assert_eq!(saved["owned"]["port"], 3080);
    let status = cancel_core(&ws, &conn).await.unwrap();
    assert!(!status.enabled && !status.owned && !status.desired_enabled);
    assert!(status.auth_url.is_none());
    assert_eq!(
        inspect_mapping(&cli.data.lock().unwrap().config, DNS, 3080),
        Mapping::Empty
    );
}

#[tokio::test]
async fn successful_exit_without_configuration_stays_failed() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    cli.data.lock().unwrap().apply = false;
    let ws = server(cli, 3080);
    enable_core(&ws, &conn).await.unwrap();
    let status = finished(&ws, &conn).await;
    assert!(!status.enabled);
    assert_eq!(status.error.as_deref(), Some("failed"));
    assert!(status.desired_enabled);
    assert_eq!(status_core(&ws, &conn).await.unwrap().state, "error");
}

#[tokio::test]
async fn login_does_not_change_a_running_profile() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    let ws = server(cli.clone(), 3080);
    login_core(&ws, &conn).await.unwrap();
    assert_eq!(finished(&ws, &conn).await.state, "ready");
    assert!(!cli
        .data
        .lock()
        .unwrap()
        .calls
        .iter()
        .any(|args| args.first().is_some_and(|arg| arg == "up")));
    cli.data.lock().unwrap().backend = "NeedsLogin".into();
    login_core(&ws, &conn).await.unwrap();
    assert_eq!(finished(&ws, &conn).await.state, "ready");
    assert!(cli
        .data
        .lock()
        .unwrap()
        .calls
        .iter()
        .any(|args| args == &["up", "--timeout=120s"]));
}

#[tokio::test]
async fn shutdown_keeps_preference_and_restore_cleans_old_port() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    let ws = server(cli.clone(), 3080);
    enable_core(&ws, &conn).await.unwrap();
    assert!(finished(&ws, &conn).await.enabled);
    let ws = server(cli.clone(), 3090);
    restore(ws.clone(), conn.clone());
    tokio::time::timeout(Duration::from_secs(3), async {
        loop {
            let status = status_core(&ws, &conn).await.unwrap();
            if status.enabled
                && status.owned
                && cli.data.lock().unwrap().config["Web"][format!("{DNS}:443")]["Handlers"]["/"]
                    ["Proxy"]
                    == "http://127.0.0.1:3090"
            {
                break;
            }
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    let guard = ws.lifecycle.lock().await;
    shutdown(&ws).await.unwrap();
    drop(guard);
    let status = status_core(&ws, &conn).await.unwrap();
    assert!(!status.enabled && !status.owned && status.desired_enabled);
}

#[tokio::test]
async fn refuses_cleanup_after_external_edit_and_preserves_other_ports() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    let ws = server(cli.clone(), 3080);
    enable_core(&ws, &conn).await.unwrap();
    assert!(finished(&ws, &conn).await.enabled);
    cli.data.lock().unwrap().config["Web"][format!("{DNS}:443")]["Handlers"]["/foreign"] =
        json!({"Text":"external"});
    let before = cli.data.lock().unwrap().config.clone();
    assert_eq!(
        disable_core(&ws, &conn).await.unwrap().error.as_deref(),
        Some("conflict")
    );
    assert_eq!(cli.data.lock().unwrap().config, before);
    cli.data.lock().unwrap().config = public_config(3080);
    cli.data.lock().unwrap().config["TCP"]["8443"] = json!({"HTTPS":true});
    assert!(!disable_core(&ws, &conn).await.unwrap().enabled);
    assert_eq!(
        cli.data.lock().unwrap().config["TCP"]["8443"]["HTTPS"],
        true
    );
}

#[tokio::test]
async fn startup_reconciles_owned_mapping_without_starting_web_service() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    let ws = server(cli.clone(), 3080);
    enable_core(&ws, &conn).await.unwrap();
    assert!(finished(&ws, &conn).await.enabled);
    let mut restarted = WebServerState::new();
    restarted.funnel = Arc::new(FunnelManager {
        executor: cli.clone(),
        ..Default::default()
    });
    initialize(&restarted, &conn).await.unwrap();
    assert_eq!(
        inspect_mapping(&cli.data.lock().unwrap().config, DNS, 3080),
        Mapping::Empty
    );
    let status = status_core(&restarted, &conn).await.unwrap();
    assert!(!status.enabled && !status.owned && status.desired_enabled);
}

#[tokio::test]
async fn rejects_missing_service_or_unsafe_bind_before_starting_cli() {
    let conn = database().await;
    let cli = Arc::new(FakeCli::default());
    let ws = server(cli.clone(), 3080);
    *ws.host.lock().unwrap() = "192.0.2.10".into();
    assert_eq!(
        enable_core(&ws, &conn).await.unwrap().error.as_deref(),
        Some("unsupportedBind")
    );
    *ws.host.lock().unwrap() = "127.0.0.1".into();
    *ws.token.lock().unwrap() = String::new();
    assert_eq!(
        enable_core(&ws, &conn).await.unwrap().error.as_deref(),
        Some("stopped")
    );
    assert!(cli.data.lock().unwrap().calls.is_empty());
}
