//! OpenAI-compatible image generation (the `POST /images/generations` shape
//! every relay and most aggregators speak). Synchronous: the first `poll`
//! carries the finished artifacts.

use bytes::Bytes;
use serde_json::{json, Value};

use super::{http, GenerationError, GenerationRequest, MediaKind, MediaProvider, PollState};

pub struct OpenAiCompatProvider {
    pub kind: MediaKind,
    pub api_url: String,
    pub api_key: String,
    pub model: String,
    pub extra: Value,
}

impl OpenAiCompatProvider {
    fn endpoint(&self, path: &str) -> String {
        let base = self.api_url.trim_end_matches('/');
        // Accept both a bare base ("https://relay/v1") and a full endpoint
        // ("https://relay/v1/images/generations") — relays vary.
        if base.ends_with(path) {
            base.to_string()
        } else {
            format!("{base}{path}")
        }
    }

    async fn generate_image(
        &self,
        req: &GenerationRequest,
    ) -> Result<Vec<Vec<u8>>, GenerationError> {
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let mut body = json!({
            "model": self.model,
            "prompt": req.prompt,
            "n": req.count.max(1),
            "response_format": "b64_json",
        });
        if let Some(size) = &req.size {
            body["size"] = Value::String(size.clone());
        }
        if let Some(negative) = &req.negative_prompt {
            // Not in the official shape; relays that support it read this.
            body["negative_prompt"] = Value::String(negative.clone());
        }
        if let Some(seed) = req.seed {
            body["seed"] = json!(seed);
        }
        if let Ok(custom) =
            serde_json::from_value::<Value>(self.extra.get("body").cloned().unwrap_or(Value::Null))
        {
            if let (Some(base), Some(over)) = (body.as_object_mut(), custom.as_object()) {
                for (k, v) in over {
                    if !matches!(
                        k.as_str(),
                        "model" | "prompt" | "n" | "seed" | "negative_prompt" | "response_format"
                    ) {
                        base.insert(k.clone(), v.clone());
                    }
                }
            }
        }

        let request = if req.reference_images.is_empty() {
            client
                .post(self.endpoint("/images/generations"))
                .json(&body)
        } else {
            let mut form = reqwest::multipart::Form::new();
            for (key, value) in body.as_object().into_iter().flatten() {
                let text = value
                    .as_str()
                    .map(str::to_owned)
                    .unwrap_or_else(|| value.to_string());
                form = form.text(key.clone(), text);
            }
            let field = if req.reference_images.len() == 1 {
                "image"
            } else {
                "image[]"
            };
            for image in &req.reference_images {
                let part = reqwest::multipart::Part::bytes(image.data.clone())
                    .file_name(image.name.clone())
                    .mime_str(&image.mime)
                    .map_err(|e| GenerationError::new("bad_request", e.to_string()))?;
                form = form.part(field, part);
            }
            client.post(self.endpoint("/images/edits")).multipart(form)
        };
        let response = request
            .bearer_auth(&self.api_key)
            .send()
            .await
            .map_err(|e| http::transport_error(&self.api_key, &e))?;
        let status = response.status().as_u16();
        let payload = http::read_capped(&self.api_key, response, 64 * 1024 * 1024).await?;
        if !(200..300).contains(&status) {
            let text = String::from_utf8_lossy(&payload);
            return Err(http::status_error(&self.api_key, status, &text));
        }

        let parsed: Value = serde_json::from_slice(&payload)
            .map_err(|e| GenerationError::new("bad_request", format!("decode: {e}")))?;
        let items = parsed
            .get("data")
            .and_then(Value::as_array)
            .ok_or_else(|| GenerationError::new("bad_request", "response has no `data` array"))?;
        let mut artifacts = Vec::new();
        for item in items {
            let b64 = item
                .get("b64_json")
                .and_then(Value::as_str)
                .or_else(|| item.get("url").and_then(Value::as_str))
                .ok_or_else(|| {
                    GenerationError::new("bad_request", "image entry has neither b64_json nor url")
                })?;
            let bytes: Bytes = if item.get("b64_json").is_some() {
                use base64::Engine as _;
                base64::engine::general_purpose::STANDARD
                    .decode(b64.trim())
                    .map_err(|e| GenerationError::new("bad_request", format!("b64_json: {e}")))?
                    .into()
            } else {
                // A URL: download it (short timeout, capped).
                let url = reqwest::Url::parse(b64)
                    .map_err(|_| GenerationError::new("bad_request", "image url is not a URL"))?;
                let dl = http::client(http::DOWNLOAD_TIMEOUT)?;
                let resp = dl
                    .get(url)
                    .send()
                    .await
                    .map_err(|e| http::transport_error(&self.api_key, &e))?;
                let st = resp.status().as_u16();
                let data = http::read_capped(&self.api_key, resp, 64 * 1024 * 1024).await?;
                if !(200..300).contains(&st) {
                    let text = String::from_utf8_lossy(&data);
                    return Err(http::status_error(&self.api_key, st, &text));
                }
                data
            };
            artifacts.push(bytes.to_vec());
        }
        if artifacts.is_empty() {
            return Err(GenerationError::new(
                "bad_request",
                "response carried no images",
            ));
        }
        Ok(artifacts)
    }

    async fn generate_speech(
        &self,
        req: &GenerationRequest,
    ) -> Result<Vec<Vec<u8>>, GenerationError> {
        let mut req = req.clone();
        self.prepare(&mut req)?;
        let speech = req
            .speech
            .as_ref()
            .ok_or_else(|| GenerationError::new("bad_request", "speech needs voice parameters"))?;
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let mut body = json!({
            "model": self.model,
            "input": req.prompt,
            "voice": speech.voice,
            "speed": speech.speed,
            "response_format": speech.response_format,
        });
        if let Some(instructions) = &speech.instructions {
            body["instructions"] = json!(instructions);
        }
        let response = client
            .post(self.endpoint("/audio/speech"))
            .bearer_auth(&self.api_key)
            .json(&body)
            .send()
            .await
            .map_err(|e| http::transport_error(&self.api_key, &e))?;
        let status = response.status().as_u16();
        let payload = http::read_capped(&self.api_key, response, 64 * 1024 * 1024).await?;
        if !(200..300).contains(&status) {
            let text = String::from_utf8_lossy(&payload);
            return Err(http::status_error(&self.api_key, status, &text));
        }
        Ok(vec![payload.to_vec()])
    }
}

#[async_trait::async_trait]
impl MediaProvider for OpenAiCompatProvider {
    fn kind(&self) -> MediaKind {
        self.kind
    }

    fn capabilities(&self) -> super::capabilities::MediaCapabilities {
        super::capabilities::describe(self.kind, &self.extra)
    }

    async fn submit(&self, req: &GenerationRequest) -> Result<Value, GenerationError> {
        self.prepare(&mut req.clone())?;
        // Synchronous vendor: nothing to pre-stage. The handle just records
        // the request was sent (the runner carries the request itself).
        Ok(json!({ "mode": "inline" }))
    }

    async fn poll(
        &self,
        _handle: &Value,
        req: &GenerationRequest,
    ) -> Result<PollState, GenerationError> {
        let artifacts = match self.kind {
            MediaKind::Image => self.generate_image(req).await?,
            MediaKind::Tts => self.generate_speech(req).await?,
            MediaKind::Video => {
                return Err(GenerationError::new(
                    "bad_request",
                    "video generation needs a job-based vendor (M4)",
                ))
            }
        };
        Ok(PollState::Succeeded(artifacts))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    async fn endpoint() -> (String, tokio::task::JoinHandle<Value>) {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let mut data = Vec::new();
            let body = loop {
                let mut buf = [0_u8; 4096];
                let n = socket.read(&mut buf).await.unwrap();
                assert!(n > 0);
                data.extend_from_slice(&buf[..n]);
                if let Some(end) = data.windows(4).position(|v| v == b"\r\n\r\n") {
                    let head = String::from_utf8_lossy(&data[..end]);
                    let length: usize = head
                        .lines()
                        .find_map(|line| {
                            let (key, value) = line.split_once(':')?;
                            key.eq_ignore_ascii_case("content-length")
                                .then(|| value.trim().parse().unwrap())
                        })
                        .unwrap();
                    if data.len() >= end + 4 + length {
                        break data[end + 4..end + 4 + length].to_vec();
                    }
                }
            };
            let reply = r#"{"data":[{"b64_json":"aW1hZ2U="}]}"#;
            socket.write_all(format!("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{reply}", reply.len()).as_bytes()).await.unwrap();
            serde_json::from_slice(&body).unwrap()
        });
        (format!("http://{addr}/v1"), server)
    }

    #[tokio::test]
    async fn reference_images_are_sent_as_edit_uploads_not_discarded() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let mut data = Vec::new();
            loop {
                let mut buf = [0_u8; 4096];
                let n = socket.read(&mut buf).await.unwrap();
                assert!(n > 0);
                data.extend_from_slice(&buf[..n]);
                if let Some(end) = data.windows(4).position(|v| v == b"\r\n\r\n") {
                    let headers = String::from_utf8_lossy(&data[..end]);
                    let length: usize = headers
                        .lines()
                        .find_map(|line| {
                            let (key, value) = line.split_once(':')?;
                            key.eq_ignore_ascii_case("content-length")
                                .then(|| value.trim().parse().unwrap())
                        })
                        .unwrap();
                    if data.len() >= end + 4 + length {
                        break;
                    }
                }
            }
            let reply = r#"{"data":[{"b64_json":"aW1hZ2U="}]}"#;
            socket.write_all(format!("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{reply}", reply.len()).as_bytes()).await.unwrap();
            String::from_utf8_lossy(&data).into_owned()
        });
        let provider = OpenAiCompatProvider {
            kind: MediaKind::Image,
            api_url: format!("http://{addr}/v1"),
            api_key: "test-key".into(),
            model: "gpt-image-1".into(),
            extra: Value::Null,
        };
        let req: GenerationRequest = serde_json::from_value(json!({
            "prompt": "same character", "count": 1,
            "reference_images": [{"name": "front.png", "mime": "image/png", "data": [82, 69, 70]}]
        }))
        .unwrap();
        provider.generate_image(&req).await.unwrap();
        let wire = server.await.unwrap();
        assert!(
            wire.starts_with("POST /v1/images/edits "),
            "references were not sent to the edit endpoint"
        );
        assert!(wire.contains("multipart/form-data"));
        assert!(wire.contains("front.png"));
        assert!(wire.contains("REF"));
    }

    #[tokio::test]
    async fn speech_request_keeps_voice_speed_format_and_vendor_defaults() {
        let (api_url, server) = endpoint().await;
        let provider = OpenAiCompatProvider {
            kind: MediaKind::Tts,
            api_url,
            api_key: "test-key".into(),
            model: "tts-model".into(),
            extra: json!({"voice":"alloy","body":{"input":"wrong text","voice":"wrong voice","speed":3,"response_format":"pcm"}}),
        };
        let request: GenerationRequest = serde_json::from_value(json!({
            "prompt":"Read the approved line",
            "speech":{"voice":"nova","speed":1.25,"responseFormat":"wav"},
        }))
        .unwrap();
        provider.generate_speech(&request).await.unwrap();
        let body = server.await.unwrap();
        assert_eq!(body["input"], "Read the approved line");
        assert_eq!(body["voice"], "nova");
        assert_eq!(body["speed"], 1.25);
        assert_eq!(body["response_format"], "wav");
    }

    #[tokio::test]
    async fn speech_without_a_voice_refuses_before_any_request() {
        let provider = OpenAiCompatProvider {
            kind: MediaKind::Tts,
            api_url: "http://127.0.0.1:1/v1".into(),
            api_key: "test-key".into(),
            model: "tts-model".into(),
            extra: Value::Null,
        };
        let error = provider
            .generate_speech(&GenerationRequest {
                prompt: "Hello".into(),
                ..Default::default()
            })
            .await
            .unwrap_err();
        assert_eq!(error.kind, "bad_request");
        assert!(error.message.contains("voice"));
    }

    #[tokio::test]
    async fn provider_extras_cannot_override_approved_count_prompt_or_model() {
        let (api_url, server) = endpoint().await;
        let provider = OpenAiCompatProvider {
            kind: MediaKind::Image,
            api_url,
            api_key: "test-key".into(),
            model: "image-model".into(),
            extra: json!({"body": {"n": 99, "prompt": "different", "model": "expensive", "quality": "high"}}),
        };
        provider
            .generate_image(&GenerationRequest {
                prompt: "approved".into(),
                count: 1,
                ..Default::default()
            })
            .await
            .unwrap();
        let body = server.await.unwrap();
        assert_eq!(body["n"], 1);
        assert_eq!(body["prompt"], "approved");
        assert_eq!(body["model"], "image-model");
        assert_eq!(body["quality"], "high");
    }
}
