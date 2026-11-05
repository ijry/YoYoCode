//! new-api aligned video contract client (`/v1/video/generations`), the
//! serving shape of the ai-switch multimodal gateway (and new-api itself).
//!
//! Wire shape per the gateway design (`ai-switch` `2026-10-09-multimodal-
//! video-gateway.md`):
//!
//! * `POST {base}/v1/video/generations`
//!   `{"model", "prompt", "duration"?, "metadata"?: {aspect_ratio, …}}`
//!   → 201 `{"id": "video-{task_id}", "task_id", "status": "processing"}`
//! * `GET {base}/v1/video/generations/{task_id}`
//!   → `{"task_id", "status": "processing"|"succeeded"|"failed", "url", "error"}`
//!
//! Vendor differences (kling / ark / minimax / zhipu / sora) are eaten by the
//! gateway upstream; this client only speaks the one contract. Statuses are
//! the new-api vocabulary, deliberately NOT ark's. Tests run against a local
//! mock speaking the documented shape — no live gateway or vendor is called.

use base64::Engine as _;
use serde_json::{json, Value};

use super::{
    capabilities, http, GenerationError, GenerationRequest, MediaKind, MediaProvider, PollState,
    ReferenceImage,
};

pub struct NewApiVideoProvider {
    pub api_url: String,
    pub api_key: String,
    pub model: String,
    pub extra: Value,
}

impl NewApiVideoProvider {
    fn endpoint(&self) -> Result<reqwest::Url, GenerationError> {
        let mut url = http::http_url(&self.api_url, "bad_request")?;
        if url.query().is_some() {
            return Err(GenerationError::new(
                "bad_request",
                "gateway API URL must not contain a query",
            ));
        }
        let base = url.path().trim_end_matches('/');
        let path = if base.ends_with("/v1/video/generations") {
            base.to_string()
        } else if base.ends_with("/v1") {
            format!("{base}/video/generations")
        } else {
            format!("{base}/v1/video/generations")
        };
        url.set_path(&path);
        Ok(url)
    }

    fn body(&self, req: &GenerationRequest) -> Value {
        let mut body = json!({ "model": self.model, "prompt": req.prompt });
        if let Some(frame) = &req.first_frame {
            body["image"] = json!(format!(
                "data:{};base64,{}",
                frame.mime,
                base64::engine::general_purpose::STANDARD.encode(&frame.data)
            ));
        }
        if let Some(duration) = req.extra.get("duration_secs").and_then(Value::as_f64) {
            body["duration"] = json!(duration.round() as i64);
        }
        let mut metadata = json!({});
        if let Some(ratio) = req.extra.get("ratio").and_then(Value::as_str) {
            metadata["aspect_ratio"] = json!(ratio);
        }
        if metadata
            .as_object()
            .is_some_and(|metadata| !metadata.is_empty())
        {
            body["metadata"] = metadata;
        }
        body
    }
}

fn valid_task_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 256
        && id
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
}

/// The 201 body names the task twice: `task_id` and `id` as `video-{task_id}`
/// (the gateway keeps the prefix so either contract's id round-trips). Prefer
/// `task_id`; fall back to a prefix-stripped `id`.
pub(super) fn task_id_from_submission(body: &Value) -> Option<String> {
    let id = match body.get("task_id") {
        Some(Value::String(id)) => Some(id.clone()),
        Some(Value::Number(number)) => number
            .as_u64()
            .filter(|number| *number > 0)
            .map(|number| number.to_string()),
        _ => body
            .get("id")
            .and_then(Value::as_str)
            .map(|id| id.strip_prefix("video-").unwrap_or(id).to_string()),
    };
    id.filter(|id| valid_task_id(id))
}

#[async_trait::async_trait]
impl MediaProvider for NewApiVideoProvider {
    fn kind(&self) -> MediaKind {
        MediaKind::Video
    }

    fn capabilities(&self) -> capabilities::MediaCapabilities {
        let mut extra = self.extra.clone();
        extra["protocol"] = json!("newapi");
        capabilities::describe(MediaKind::Video, &extra)
    }

    async fn submit(&self, req: &GenerationRequest) -> Result<Value, GenerationError> {
        let mut req = req.clone();
        self.prepare(&mut req)?;
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let response = client
            .post(self.endpoint()?)
            .bearer_auth(&self.api_key)
            .json(&self.body(&req))
            .send()
            .await
            .map_err(|e| http::transport_error(&self.api_key, &e))?;
        let status = response.status().as_u16();
        let payload = http::read_capped(&self.api_key, response, 4 * 1024 * 1024).await?;
        if !(200..300).contains(&status) {
            let text = String::from_utf8_lossy(&payload);
            return Err(http::status_error(&self.api_key, status, &text));
        }
        let parsed: Value = serde_json::from_slice(&payload)
            .map_err(|e| GenerationError::new("bad_response", format!("decode: {e}")))?;
        let task_id = task_id_from_submission(&parsed).ok_or_else(|| {
            GenerationError::new("bad_response", "task response has no task_id or id")
        })?;
        Ok(json!({ "task_id": task_id }))
    }

    async fn poll(
        &self,
        handle: &Value,
        _req: &GenerationRequest,
    ) -> Result<PollState, GenerationError> {
        let id = handle
            .get("task_id")
            .and_then(Value::as_str)
            .filter(|id| valid_task_id(id))
            .ok_or_else(|| GenerationError::new("bad_request", "handle has no valid task_id"))?;
        let mut endpoint = self.endpoint()?;
        endpoint
            .path_segments_mut()
            .map_err(|_| {
                GenerationError::new("bad_request", "gateway API URL cannot contain paths")
            })?
            .push(id);
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let response = client
            .get(endpoint)
            .bearer_auth(&self.api_key)
            .send()
            .await
            .map_err(|e| http::transport_error(&self.api_key, &e))?;
        let status = response.status().as_u16();
        let payload = http::read_capped(&self.api_key, response, 4 * 1024 * 1024).await?;
        if !(200..300).contains(&status) {
            let text = String::from_utf8_lossy(&payload);
            return Err(http::status_error(&self.api_key, status, &text));
        }
        let parsed: Value = serde_json::from_slice(&payload)
            .map_err(|e| GenerationError::new("bad_response", format!("decode: {e}")))?;
        match parsed.get("status").and_then(Value::as_str).unwrap_or("") {
            "succeeded" => {
                let url = parsed.get("url").and_then(Value::as_str).ok_or_else(|| {
                    GenerationError::new("bad_response", "succeeded task has no url")
                })?;
                let url = http::http_url(url, "bad_response")?;
                let download = http::client(http::DOWNLOAD_TIMEOUT)?;
                let response = download
                    .get(url)
                    .send()
                    .await
                    .map_err(|e| http::transport_error(&self.api_key, &e))?;
                let status = response.status().as_u16();
                let data = http::read_capped(&self.api_key, response, 256 * 1024 * 1024).await?;
                if !(200..300).contains(&status) {
                    let text = String::from_utf8_lossy(&data);
                    return Err(http::status_error(&self.api_key, status, &text));
                }
                Ok(PollState::Succeeded(vec![data.to_vec()]))
            }
            "failed" => {
                let message = match parsed.get("error") {
                    Some(Value::String(message)) => message.clone(),
                    Some(error) => error
                        .get("message")
                        .and_then(Value::as_str)
                        .unwrap_or("gateway reported task failure")
                        .to_string(),
                    None => "gateway reported task failure".to_string(),
                };
                Ok(PollState::Failed(GenerationError::new(
                    "internal",
                    http::scrub(&message, &self.api_key),
                )))
            }
            "processing" => Ok(PollState::Running(json!({ "state": "processing" }))),
            _ => Err(GenerationError::new(
                "bad_response",
                "gateway returned an unknown or missing video task status",
            )),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::super::build_provider;
    use super::*;
    use serde_json::json;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    fn provider(addr: std::net::SocketAddr) -> Box<dyn MediaProvider> {
        build_provider(
            MediaKind::Video,
            &format!("http://{addr}"),
            "test-key",
            "seedance-via-gateway",
            r#"{"protocol":"newapi"}"#,
        )
        .unwrap()
    }

    async fn mock_exchange(listener: tokio::net::TcpListener, reply: String) -> (Value, String) {
        let (mut socket, _) = listener.accept().await.unwrap();
        let request = read_request(&mut socket).await;
        socket
            .write_all(&http_reply("application/json", reply.as_bytes()))
            .await
            .unwrap();
        request
    }

    async fn read_request(socket: &mut tokio::net::TcpStream) -> (Value, String) {
        let mut data = Vec::new();
        loop {
            let mut buf = [0_u8; 4096];
            let n = socket.read(&mut buf).await.unwrap();
            assert!(n > 0);
            data.extend_from_slice(&buf[..n]);
            if let Some(end) = data.windows(4).position(|v| v == b"\r\n\r\n") {
                let head = String::from_utf8_lossy(&data[..end]).into_owned();
                let length: usize = head
                    .lines()
                    .find_map(|line| {
                        let (key, value) = line.split_once(':')?;
                        key.eq_ignore_ascii_case("content-length")
                            .then(|| value.trim().parse().unwrap())
                    })
                    .unwrap_or(0);
                if data.len() >= end + 4 + length {
                    let raw = &data[end + 4..end + 4 + length];
                    let body = if raw.is_empty() {
                        Value::Null
                    } else {
                        serde_json::from_slice(raw).unwrap()
                    };
                    return (body, head);
                }
            }
        }
    }

    fn http_reply(content_type: &str, payload: &[u8]) -> Vec<u8> {
        http_reply_with_status("200 OK", content_type, payload)
    }

    fn http_reply_with_status(status: &str, content_type: &str, payload: &[u8]) -> Vec<u8> {
        format!(
            "HTTP/1.1 {status}\r\nContent-Type: {content_type}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
            payload.len()
        )
        .into_bytes()
        .into_iter()
        .chain(payload.iter().copied())
        .collect()
    }

    #[tokio::test]
    async fn submit_posts_the_new_api_contract_and_accepts_both_id_shapes() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let provider = provider(addr);
        let request: GenerationRequest = serde_json::from_value(json!({
            "prompt": "A paper boat crosses a pond",
            "extra": { "duration_secs": 5.0, "ratio": "9:16" },
        }))
        .unwrap();
        let server = tokio::spawn(mock_exchange(
            listener,
            r#"{"id":"video-42","task_id":42,"status":"processing"}"#.into(),
        ));
        let handle = provider.submit(&request).await.unwrap();
        let (body, headers) = server.await.unwrap();
        assert!(headers.starts_with("POST /v1/video/generations HTTP/1.1"));
        assert!(headers
            .to_ascii_lowercase()
            .contains("authorization: bearer test-key"));
        assert_eq!(body["model"], "seedance-via-gateway");
        assert_eq!(body["prompt"], "A paper boat crosses a pond");
        assert_eq!(body["duration"], 5);
        assert_eq!(body["metadata"]["aspect_ratio"], "9:16");
        assert_eq!(handle["task_id"], "42");

        // A submission body that only carries the prefixed `id` also resolves.
        let parsed: Value =
            serde_json::from_str(r#"{"id":"video-77","status":"processing"}"#).unwrap();
        assert_eq!(task_id_from_submission(&parsed), Some("77".into()));
    }

    #[tokio::test]
    async fn poll_downloads_from_a_succeeded_new_api_task() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let provider = provider(addr);
        let payload = b"MP4BYTES";
        let server = tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let (body, _) = read_request(&mut socket).await;
            assert_eq!(body, Value::Null);
            let reply = format!(
                r#"{{"task_id":"42","status":"succeeded","url":"http://{addr}/clip.mp4"}}"#
            );
            socket
                .write_all(&http_reply("application/json", reply.as_bytes()))
                .await
                .unwrap();
            let (mut socket, _) = listener.accept().await.unwrap();
            let (body, _) = read_request(&mut socket).await;
            assert_eq!(body, Value::Null);
            socket
                .write_all(&http_reply("video/mp4", payload))
                .await
                .unwrap();
        });
        let state = provider
            .poll(&json!({ "task_id": "42" }), &GenerationRequest::default())
            .await
            .unwrap();
        server.await.unwrap();
        match state {
            PollState::Succeeded(artifacts) => {
                assert_eq!(artifacts, vec![payload.to_vec()]);
            }
            other => panic!("expected succeeded artifacts, got {other:?}"),
        }
    }

    #[tokio::test]
    async fn poll_reports_failed_with_the_gateway_message() {
        let api = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let provider = provider(api.local_addr().unwrap());
        let server = tokio::spawn(mock_exchange(
            api,
            r#"{"task_id":"42","status":"failed","error":{"message":"pool exhausted"}}"#.into(),
        ));
        let state = provider
            .poll(&json!({ "task_id": "42" }), &GenerationRequest::default())
            .await
            .unwrap();
        server.await.unwrap();
        match state {
            PollState::Failed(error) => {
                assert_eq!(error.kind, "internal");
                assert!(error.message.contains("pool exhausted"), "{error}");
            }
            other => panic!("expected a failed state, got {other:?}"),
        }
    }

    #[tokio::test]
    async fn gateway_endpoint_accepts_root_v1_and_full_endpoint_for_submit_and_poll() {
        for (suffix, path) in [
            ("", "/v1/video/generations"),
            ("/", "/v1/video/generations"),
            ("/v1", "/v1/video/generations"),
            ("/v1/", "/v1/video/generations"),
            ("/v1/video/generations/", "/v1/video/generations"),
            ("/relay/v1", "/relay/v1/video/generations"),
            ("/relay/v1/video/generations", "/relay/v1/video/generations"),
        ] {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let addr = listener.local_addr().unwrap();
            let provider = build_provider(
                MediaKind::Video,
                &format!("http://{addr}{suffix}"),
                "test-key",
                "seedance-via-gateway",
                r#"{"protocol":"newapi"}"#,
            )
            .unwrap();
            let server = tokio::spawn(async move {
                let (mut socket, _) = listener.accept().await.unwrap();
                let (_, submit) = read_request(&mut socket).await;
                socket
                    .write_all(&http_reply(
                        "application/json",
                        br#"{"id":"video-vg_42","task_id":"vg_42","status":"processing"}"#,
                    ))
                    .await
                    .unwrap();
                let (mut socket, _) = listener.accept().await.unwrap();
                let (_, poll) = read_request(&mut socket).await;
                socket
                    .write_all(&http_reply(
                        "application/json",
                        br#"{"task_id":"vg_42","status":"processing","url":null,"format":"mp4","metadata":null,"error":null}"#,
                    ))
                    .await
                    .unwrap();
                (submit, poll)
            });
            let handle = provider
                .submit(&GenerationRequest {
                    prompt: "boat".into(),
                    ..Default::default()
                })
                .await
                .unwrap();
            let state = provider
                .poll(&handle, &GenerationRequest::default())
                .await
                .unwrap();
            let (submit, poll) = server.await.unwrap();
            assert!(
                submit.starts_with(&format!("POST {path} HTTP/1.1")),
                "{suffix}: {submit}"
            );
            assert!(
                poll.starts_with(&format!("GET {path}/vg_42 HTTP/1.1")),
                "{suffix}: {poll}"
            );
            assert!(matches!(state, PollState::Running(_)));
        }
    }

    #[test]
    fn submission_ids_cannot_change_the_poll_path() {
        for id in [
            "",
            ".",
            "..",
            "../other",
            "a/b",
            "a?key=x",
            "a#x",
            "a%2Fb",
            "a\\b",
            "with space",
            "動画",
        ] {
            assert_eq!(
                task_id_from_submission(&json!({"task_id":id})),
                None,
                "{id}"
            );
            assert_eq!(
                task_id_from_submission(&json!({"id":format!("video-{id}")})),
                None,
                "{id}"
            );
        }
        assert_eq!(
            task_id_from_submission(&json!({"task_id":"x".repeat(257)})),
            None
        );
        for number in [json!(0), json!(-1), json!(2.5)] {
            assert_eq!(task_id_from_submission(&json!({"task_id":number})), None);
        }
        for body in [
            json!({"task_id":"vg_a-b_42"}),
            json!({"id":"video-vg_a-b_42"}),
        ] {
            assert_eq!(task_id_from_submission(&body), Some("vg_a-b_42".into()));
        }
        assert_eq!(
            task_id_from_submission(&json!({"task_id":42})),
            Some("42".into())
        );
    }

    #[tokio::test]
    async fn invalid_poll_handles_refuse_before_any_gateway_request() {
        let provider = provider("127.0.0.1:1".parse().unwrap());
        for id in ["", "..", "a/b", "a?key=x", "a#x", "a%2Fb"] {
            let error = provider
                .poll(&json!({"task_id":id}), &GenerationRequest::default())
                .await
                .unwrap_err();
            assert_eq!(error.kind, "bad_request", "{id}: {error}");
        }
    }

    #[tokio::test]
    async fn unknown_gateway_status_is_a_bad_response_not_infinite_running() {
        for status in [
            Value::Null,
            json!(""),
            json!("queued"),
            json!("completed"),
            json!(42),
        ] {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let provider = provider(listener.local_addr().unwrap());
            let server = tokio::spawn(mock_exchange(
                listener,
                json!({"task_id":"42","status":status,"url":null,"error":null}).to_string(),
            ));
            let result = provider
                .poll(&json!({"task_id":"42"}), &GenerationRequest::default())
                .await;
            server.await.unwrap();
            let error = result.expect_err("unknown status must not keep a job running");
            assert_eq!(error.kind, "bad_response");
        }
    }

    #[tokio::test]
    async fn gateway_task_failure_scrubs_the_key_before_persistence() {
        for error in [
            json!("pool exhausted: test-key"),
            json!({"message":"pool exhausted: test-key"}),
        ] {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let provider = provider(listener.local_addr().unwrap());
            let server = tokio::spawn(mock_exchange(
                listener,
                json!({"task_id":"42","status":"failed","url":null,"error":error}).to_string(),
            ));
            let state = provider
                .poll(&json!({"task_id":"42"}), &GenerationRequest::default())
                .await
                .unwrap();
            server.await.unwrap();
            let PollState::Failed(error) = state else {
                panic!("expected failed state")
            };
            assert!(!error.message.contains("test-key"), "{error}");
            assert!(error.message.contains("pool exhausted"));
        }
    }

    #[test]
    fn newapi_rejects_references_even_when_configuration_claims_support() {
        let provider = build_provider(
            MediaKind::Video,
            "http://127.0.0.1:1",
            "test-key",
            "seedance-via-gateway",
            r#"{"protocol":"newapi","capabilities":{"maxReferences":8}}"#,
        )
        .unwrap();
        for value in [
            json!({"prompt":"boat","reference_paths":["ref.png"]}),
            json!({"prompt":"boat","reference_images":[{"name":"ref.png","mime":"image/png","data":[82,69,70]}]}),
        ] {
            let mut request: GenerationRequest = serde_json::from_value(value).unwrap();
            let error = provider.prepare(&mut request).unwrap_err();
            assert_eq!(error.kind, "bad_request");
        }
        assert_eq!(provider.capabilities().max_references, 0);
    }

    #[tokio::test]
    async fn gateway_url_rejects_credentials_query_fragment_and_non_http_schemes() {
        for url in [
            "not a URL",
            "ftp://127.0.0.1:1",
            "http://user:password@127.0.0.1:1",
            "http://127.0.0.1:1/v1?key=test-key",
            "http://127.0.0.1:1/v1#fragment",
        ] {
            let provider = build_provider(
                MediaKind::Video,
                url,
                "test-key",
                "seedance-via-gateway",
                r#"{"protocol":"newapi"}"#,
            )
            .unwrap();
            let error = provider
                .submit(&GenerationRequest {
                    prompt: "boat".into(),
                    ..Default::default()
                })
                .await
                .unwrap_err();
            assert_eq!(error.kind, "bad_request", "{url}: {error}");
            assert!(!error.message.contains("test-key"));
        }
    }

    #[tokio::test]
    async fn artifact_url_rejects_embedded_credentials_before_download() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let provider = provider(listener.local_addr().unwrap());
        let server = tokio::spawn(mock_exchange(
            listener,
            r#"{"task_id":"42","status":"succeeded","url":"http://user:password@127.0.0.1:1/clip.mp4","error":null}"#.into(),
        ));
        let result = provider
            .poll(&json!({"task_id":"42"}), &GenerationRequest::default())
            .await;
        server.await.unwrap();
        assert_eq!(result.unwrap_err().kind, "bad_response");
    }

    #[tokio::test]
    async fn artifact_download_never_forwards_gateway_authorization() {
        let gateway = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let media = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = media.local_addr().unwrap();
        let provider = provider(gateway.local_addr().unwrap());
        let gateway_server = tokio::spawn(mock_exchange(
            gateway,
            format!(
                r#"{{"task_id":"42","status":"succeeded","url":"http://{addr}/clip.mp4?signature=cdn-token","format":"mp4","metadata":null,"error":null}}"#
            ),
        ));
        let media_server = tokio::spawn(async move {
            let (mut socket, _) = media.accept().await.unwrap();
            let (_, headers) = read_request(&mut socket).await;
            socket
                .write_all(&http_reply("video/mp4", b"MP4BYTES"))
                .await
                .unwrap();
            headers
        });
        let state = provider
            .poll(&json!({"task_id":"42"}), &GenerationRequest::default())
            .await
            .unwrap();
        let (_, gateway_headers) = gateway_server.await.unwrap();
        let media_headers = media_server.await.unwrap();
        assert!(gateway_headers
            .to_ascii_lowercase()
            .contains("authorization: bearer test-key"));
        assert!(media_headers.starts_with("GET /clip.mp4?signature=cdn-token HTTP/1.1"));
        assert!(!media_headers
            .to_ascii_lowercase()
            .contains("authorization:"));
        assert!(!media_headers.contains("test-key"));
        assert!(
            matches!(state, PollState::Succeeded(artifacts) if artifacts == vec![b"MP4BYTES".to_vec()])
        );
    }

    #[tokio::test]
    async fn artifact_redirect_is_not_followed() {
        let gateway = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let media = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let destination = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = media.local_addr().unwrap();
        let target = destination.local_addr().unwrap();
        let provider = provider(gateway.local_addr().unwrap());
        let gateway_server = tokio::spawn(mock_exchange(
            gateway,
            format!(
                r#"{{"task_id":"42","status":"succeeded","url":"http://{addr}/clip.mp4","error":null}}"#
            ),
        ));
        let media_server = tokio::spawn(async move {
            let (mut socket, _) = media.accept().await.unwrap();
            read_request(&mut socket).await;
            socket.write_all(format!(
                "HTTP/1.1 302 Found\r\nLocation: http://{target}/clip.mp4\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
            ).as_bytes()).await.unwrap();
        });
        let result = provider
            .poll(&json!({"task_id":"42"}), &GenerationRequest::default())
            .await;
        gateway_server.await.unwrap();
        media_server.await.unwrap();
        let error = result.unwrap_err();
        assert_eq!(error.kind, "network");
        assert!(error.message.contains("302"));
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(100), destination.accept())
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn artifact_http_errors_are_classified_and_scrubbed() {
        let gateway = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let media = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = media.local_addr().unwrap();
        let provider = provider(gateway.local_addr().unwrap());
        let gateway_server = tokio::spawn(mock_exchange(
            gateway,
            format!(
                r#"{{"task_id":"42","status":"succeeded","url":"http://{addr}/clip.mp4","error":null}}"#
            ),
        ));
        let media_server = tokio::spawn(async move {
            let (mut socket, _) = media.accept().await.unwrap();
            read_request(&mut socket).await;
            socket
                .write_all(&http_reply_with_status(
                    "403 Forbidden",
                    "text/plain",
                    b"expired video link: test-key",
                ))
                .await
                .unwrap();
        });
        let result = provider
            .poll(&json!({"task_id":"42"}), &GenerationRequest::default())
            .await;
        gateway_server.await.unwrap();
        media_server.await.unwrap();
        let error = result.unwrap_err();
        assert_eq!(error.kind, "auth");
        assert!(error.message.contains("expired video link"));
        assert!(!error.message.contains("test-key"));
    }

    #[tokio::test]
    async fn oversized_artifact_is_rejected_without_reading_its_body() {
        let gateway = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let media = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = media.local_addr().unwrap();
        let provider = provider(gateway.local_addr().unwrap());
        let gateway_server = tokio::spawn(mock_exchange(
            gateway,
            format!(
                r#"{{"task_id":"42","status":"succeeded","url":"http://{addr}/clip.mp4","error":null}}"#
            ),
        ));
        let media_server = tokio::spawn(async move {
            let (mut socket, _) = media.accept().await.unwrap();
            read_request(&mut socket).await;
            socket.write_all(b"HTTP/1.1 200 OK\r\nContent-Type: video/mp4\r\nContent-Length: 268435457\r\nConnection: close\r\n\r\n").await.unwrap();
        });
        let result = provider
            .poll(&json!({"task_id":"42"}), &GenerationRequest::default())
            .await;
        gateway_server.await.unwrap();
        media_server.await.unwrap();
        assert_eq!(result.unwrap_err().kind, "too_large");
    }

    #[test]
    fn submit_sends_the_bound_frame_in_the_image_field() {
        let provider = NewApiVideoProvider {
            api_url: "http://127.0.0.1:1".into(),
            api_key: "test-key".into(),
            model: "seedance-via-gateway".into(),
            extra: json!({}),
        };
        let body = provider.body(&GenerationRequest {
            prompt: "boat".into(),
            first_frame: Some(ReferenceImage {
                name: "frame.png".into(),
                mime: "image/png".into(),
                data: vec![1, 2, 3],
            }),
            ..Default::default()
        });
        assert_eq!(body["image"], "data:image/png;base64,AQID");
        let text_only = provider.body(&GenerationRequest {
            prompt: "boat".into(),
            ..Default::default()
        });
        assert!(text_only.get("image").is_none());
    }

    #[test]
    fn newapi_keeps_the_default_video_duration_window() {
        let caps = capabilities::describe(MediaKind::Video, &json!({ "protocol": "newapi" }));
        assert!(caps.available);
        assert_eq!(caps.protocol, "newapi");
        let video = caps.video.as_ref().unwrap();
        assert_eq!(video.min_duration_secs, 4.0);
        assert_eq!(video.max_duration_secs, 30.0);
    }
}
