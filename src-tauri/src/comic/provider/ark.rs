//! Volcengine Ark (Seedance) style job-based video generation.
//!
//! Wire shape per the official create/get task docs:
//!
//! * `POST {base}/contents/generations/tasks` `{"model", "content": [...]}` → `{"id"}`
//! * `GET {base}/contents/generations/tasks/{id}` → `{"status", "content": {"video_url"}, "error"}`
//!
//! Statuses: queued / running / succeeded / failed / cancelled / expired.
//! These tests run against a local mock speaking the documented shape — live
//! vendor behavior is deliberately NOT claimed verified.

use base64::Engine as _;
use serde_json::{json, Value};

use super::{
    capabilities, http, GenerationError, GenerationRequest, MediaKind, MediaProvider, PollState,
    ReferenceImage,
};

pub struct ArkVideoProvider {
    pub api_url: String,
    pub api_key: String,
    pub model: String,
    pub extra: Value,
}

impl ArkVideoProvider {
    fn base(&self) -> String {
        self.api_url.trim_end_matches('/').to_string()
    }

    fn body(&self, req: &GenerationRequest) -> Value {
        let mut content = vec![json!({ "type": "text", "text": req.prompt })];
        if let Some(frame) = &req.first_frame {
            let data_url = format!(
                "data:{};base64,{}",
                frame.mime,
                base64::engine::general_purpose::STANDARD.encode(&frame.data)
            );
            content.push(json!({
                "type": "image_url",
                "image_url": { "url": data_url },
                "role": "first_frame",
            }));
        }
        for image in &req.reference_images {
            let data_url = format!(
                "data:{};base64,{}",
                image.mime,
                base64::engine::general_purpose::STANDARD.encode(&image.data)
            );
            content.push(json!({
                "type": "image_url",
                "image_url": { "url": data_url },
                "role": "reference_image",
            }));
        }
        let mut body = json!({ "model": self.model, "content": content });
        if let Some(duration) = req.extra.get("duration_secs").and_then(Value::as_f64) {
            body["duration"] = json!(duration.round() as i64);
        }
        if let Some(ratio) = req.extra.get("ratio").and_then(Value::as_str) {
            body["ratio"] = json!(ratio);
        }
        body
    }
}

#[async_trait::async_trait]
impl MediaProvider for ArkVideoProvider {
    fn kind(&self) -> MediaKind {
        MediaKind::Video
    }

    fn capabilities(&self) -> capabilities::MediaCapabilities {
        let mut extra = self.extra.clone();
        extra["protocol"] = json!("ark");
        capabilities::describe(MediaKind::Video, &extra)
    }

    async fn submit(&self, req: &GenerationRequest) -> Result<Value, GenerationError> {
        let mut req = req.clone();
        self.prepare(&mut req)?;
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let response = client
            .post(format!("{}/contents/generations/tasks", self.base()))
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
        let id = parsed
            .get("id")
            .and_then(Value::as_str)
            .ok_or_else(|| GenerationError::new("bad_response", "task response has no id"))?;
        Ok(json!({ "task_id": id }))
    }

    async fn poll(
        &self,
        handle: &Value,
        _req: &GenerationRequest,
    ) -> Result<PollState, GenerationError> {
        let id = handle
            .get("task_id")
            .and_then(Value::as_str)
            .ok_or_else(|| GenerationError::new("bad_request", "handle has no task_id"))?;
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let response = client
            .get(format!("{}/contents/generations/tasks/{id}", self.base()))
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
                let url = parsed
                    .pointer("/content/video_url")
                    .and_then(Value::as_str)
                    .ok_or_else(|| {
                        GenerationError::new("bad_response", "succeeded task has no video_url")
                    })?;
                let url = reqwest::Url::parse(url)
                    .map_err(|_| GenerationError::new("bad_response", "video url is not a URL"))?;
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
            "failed" | "expired" | "cancelled" => {
                let message = parsed
                    .pointer("/error/message")
                    .and_then(Value::as_str)
                    .unwrap_or("vendor reported task failure");
                Ok(PollState::Failed(GenerationError::new("internal", message)))
            }
            state => Ok(PollState::Running(json!({ "state": state }))),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    /// One mocked Ark API exchange: reads one request, replies `reply`.
    async fn mock_exchange(listener: tokio::net::TcpListener, reply: String) -> Value {
        let (mut socket, _) = listener.accept().await.unwrap();
        let (body, ()) = read_request(&mut socket).await;
        socket
            .write_all(&http_reply("application/json", reply.as_bytes()))
            .await
            .unwrap();
        body
    }

    /// Read one HTTP request; returns its (JSON or Null) body. The reply is
    /// sent separately so a test can answer a poll and then serve bytes.
    async fn read_request(socket: &mut tokio::net::TcpStream) -> (Value, ()) {
        let mut data = Vec::new();
        let body = loop {
            let mut buf = [0_u8; 4096];
            let n = socket.read(&mut buf).await.unwrap();
            assert!(n > 0);
            data.extend_from_slice(&buf[..n]);
            if let Some(end) = data.windows(4).position(|v| v == b"\r\n\r\n") {
                let head = String::from_utf8_lossy(&data[..end]);
                // A GET carries no body and typically no Content-Length.
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
                    break if raw.is_empty() {
                        Value::Null
                    } else {
                        serde_json::from_slice(raw).unwrap()
                    };
                }
            }
        };
        (body, ())
    }

    fn http_reply(content_type: &str, payload: &[u8]) -> Vec<u8> {
        format!(
            "HTTP/1.1 200 OK\r\nContent-Type: {content_type}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
            payload.len()
        )
        .into_bytes()
        .into_iter()
        .chain(payload.iter().copied())
        .collect()
    }

    #[test]
    fn submit_sends_the_bound_frame_as_the_first_frame_role() {
        let provider = ArkVideoProvider {
            api_url: "http://ark".into(),
            api_key: "k".into(),
            model: "m".into(),
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
        assert_eq!(body["content"][1]["role"], "first_frame");
        assert!(body["content"][1]["image_url"]["url"]
            .as_str()
            .unwrap()
            .starts_with("data:image/png;base64,"));
        let text_only = provider.body(&GenerationRequest {
            prompt: "boat".into(),
            ..Default::default()
        });
        assert!(text_only["content"]
            .as_array()
            .unwrap()
            .iter()
            .all(|part| part["role"] != "first_frame"));
    }

    #[tokio::test]
    async fn submit_posts_model_content_and_returns_the_durable_task_id() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let provider = ArkVideoProvider {
            api_url: format!("http://{addr}"),
            api_key: "test-key".into(),
            model: "doubao-seedance-2-5".into(),
            extra: json!({}),
        };
        let request: GenerationRequest = serde_json::from_value(json!({
            "prompt": "A paper boat crosses a pond",
            "extra": { "duration_secs": 5.0, "ratio": "9:16" },
        }))
        .unwrap();
        let server = tokio::spawn(mock_exchange(listener, r#"{"id":"task-1"}"#.into()));
        let handle = provider.submit(&request).await.unwrap();
        let body = server.await.unwrap();
        assert_eq!(body["model"], "doubao-seedance-2-5");
        assert_eq!(body["content"][0]["type"], "text");
        assert_eq!(body["content"][0]["text"], "A paper boat crosses a pond");
        assert_eq!(body["duration"], 5);
        assert_eq!(body["ratio"], "9:16");
        assert_eq!(handle["task_id"], "task-1");
    }

    #[tokio::test]
    async fn poll_downloads_the_video_from_a_succeeded_task() {
        // One server plays the whole vendor: the poll GET answers "succeeded"
        // with a video_url on the SAME host, then serves the clip bytes.
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let provider = ArkVideoProvider {
            api_url: format!("http://{addr}"),
            api_key: "test-key".into(),
            model: "m".into(),
            extra: json!({}),
        };
        let payload = b"MP4BYTES";
        let server = tokio::spawn(async move {
            // 1. poll GET → succeeded + video_url on this host.
            let (mut socket, _) = listener.accept().await.unwrap();
            let (body, _) = read_request(&mut socket).await;
            assert_eq!(body, Value::Null);
            let reply = format!(
                r#"{{"status":"succeeded","content":{{"video_url":"http://{addr}/clip.mp4"}},"error":null}}"#
            );
            socket
                .write_all(&http_reply("application/json", reply.as_bytes()))
                .await
                .unwrap();
            // 2. download GET → the clip bytes.
            let (mut socket, _) = listener.accept().await.unwrap();
            let (body, _) = read_request(&mut socket).await;
            assert_eq!(body, Value::Null);
            socket
                .write_all(&http_reply("video/mp4", payload))
                .await
                .unwrap();
        });
        let state = provider
            .poll(
                &json!({ "task_id": "task-1" }),
                &GenerationRequest::default(),
            )
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
    async fn poll_reports_a_failed_task_with_the_vendor_message() {
        let api = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let provider = ArkVideoProvider {
            api_url: format!("http://{}", api.local_addr().unwrap()),
            api_key: "test-key".into(),
            model: "m".into(),
            extra: json!({}),
        };
        let server = tokio::spawn(mock_exchange(
            api,
            r#"{"status":"failed","error":{"code":"OutputVideoSensitiveContentDetected","message":"detected sensitive content"}}"#.into(),
        ));
        // A vendor failure surfaces as PollState::Failed (a value, not a
        // transport error), so the task row stores the classified message.
        let state = provider
            .poll(
                &json!({ "task_id": "task-1" }),
                &GenerationRequest::default(),
            )
            .await
            .unwrap();
        server.await.unwrap();
        match state {
            PollState::Failed(error) => {
                assert_eq!(error.kind, "internal");
                assert!(error.message.contains("sensitive content"), "{error}");
            }
            other => panic!("expected a failed state, got {other:?}"),
        }
    }

    #[tokio::test]
    async fn prepare_rejects_unsupported_seed_and_out_of_range_durations() {
        let provider = ArkVideoProvider {
            api_url: "http://ark".into(),
            api_key: "k".into(),
            model: "m".into(),
            extra: json!({ "protocol": "ark", "capabilities": { "maxReferences": 1 } }),
        };
        assert!(provider.capabilities().available);
        assert!(!provider.capabilities().seed);
        assert_eq!(provider.capabilities().max_references, 1);
        let mut ok: GenerationRequest = serde_json::from_value(json!({
            "prompt": "clip", "extra": { "duration_secs": 4.0 },
        }))
        .unwrap();
        provider.prepare(&mut ok).unwrap();
        for value in [
            json!({ "prompt": "clip", "seed": 42 }),
            json!({ "prompt": "clip", "count": 2 }),
            json!({ "prompt": "clip", "extra": { "duration_secs": 2.0 } }),
            json!({ "prompt": "clip", "extra": { "duration_secs": 45.0 } }),
            json!({ "prompt": "clip", "extra": { "duration_secs": 5.0, "temperature": 1 } }),
        ] {
            let mut request: GenerationRequest = serde_json::from_value(value).unwrap();
            assert!(provider.prepare(&mut request).is_err());
        }
    }
}
