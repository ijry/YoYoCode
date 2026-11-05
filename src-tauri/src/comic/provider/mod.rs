//! Media-generation provider adapters for comic (漫剧) production.
//!
//! The [`MediaProvider`] trait is deliberately two-phase (`submit` → `poll`):
//! every vendor the pipeline targets is either synchronous behind one HTTP
//! call (OpenAI-compatible image relays) or a job queue polled by id (ComfyUI,
//! the video vendors of M4). The task runner (`crate::comic::task`) drives
//! both shapes through the same loop, so adding a vendor is adding a struct
//! here — never touching the queue.
//!
//! Requests are provider-agnostic: a neutral prompt + reference-image paths +
//! seed (see `crate::comic::prompt`). What a vendor cannot express (negative
//! prompts on some relays, extra workflow nodes on ComfyUI) comes from the
//! pool row's `extra_json` — configuration, not code.
//!
//! HTTP discipline mirrors `acp::provider_models`: fresh client per call (the
//! proxy env applies as it is now; loopback skips the proxy), no redirects
//! (headers carry the key), short timeouts for submits and generous ones for
//! downloads, the key scrubbed from every error text.

pub mod ark;
pub mod capabilities;
pub mod comfyui;
pub mod newapi;
pub mod openai_compat;

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// The media kinds a pool row may serve. M3 ships image; video and tts land
/// with M4 behind the same trait.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum MediaKind {
    Image,
    Video,
    Tts,
}

impl MediaKind {
    pub fn as_str(self) -> &'static str {
        match self {
            MediaKind::Image => "image",
            MediaKind::Video => "video",
            MediaKind::Tts => "tts",
        }
    }
}

#[derive(Clone, Default, Serialize, Deserialize)]
pub struct ReferenceImage {
    pub name: String,
    pub mime: String,
    pub data: Vec<u8>,
}

impl std::fmt::Debug for ReferenceImage {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("ReferenceImage")
            .field("name", &self.name)
            .field("bytes", &self.data.len())
            .finish()
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct SpeechOptions {
    pub voice: Option<String>,
    pub speed: f64,
    #[serde(alias = "response_format")]
    pub response_format: String,
    pub instructions: Option<String>,
}

impl Default for SpeechOptions {
    fn default() -> Self {
        Self {
            voice: None,
            speed: 1.0,
            response_format: "mp3".into(),
            instructions: None,
        }
    }
}

/// A provider-agnostic generation request.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct GenerationRequest {
    pub prompt: String,
    pub negative_prompt: Option<String>,
    pub seed: Option<u64>,
    /// Candidate count (image runs; other kinds ignore it).
    pub count: u32,
    /// Story-relative reference images (character turnarounds, scene refs).
    /// The host validates and reads them, then adapters upload those bytes.
    pub reference_paths: Vec<String>,
    /// Loaded by the host just before submission; persisted records keep paths,
    /// never the media bytes. Tests may deserialize bytes at this boundary.
    #[serde(skip_serializing)]
    pub reference_images: Vec<ReferenceImage>,
    /// E.g. `1080x1920`.
    pub size: Option<String>,
    pub speech: Option<SpeechOptions>,
    /// Story-relative first-frame image for video jobs (image-to-video).
    /// Persisted as a path; bytes load just before submission like references.
    pub first_frame_path: Option<String>,
    /// Loaded by the host just before submission; persisted records keep the
    /// path, never the bytes. Tests may deserialize bytes at this boundary.
    #[serde(skip_serializing)]
    pub first_frame: Option<ReferenceImage>,
    /// Vendor-specific knobs beyond the pool row's `extra_json`.
    pub extra: Value,
}

impl Default for GenerationRequest {
    fn default() -> Self {
        Self {
            prompt: String::new(),
            negative_prompt: None,
            seed: None,
            count: 1,
            reference_paths: Vec::new(),
            reference_images: Vec::new(),
            size: None,
            speech: None,
            first_frame_path: None,
            first_frame: None,
            extra: Value::Null,
        }
    }
}

/// Load the referenced story images without persisting their bytes in task
/// records. Missing or escaping paths refuse before any provider submission.
pub fn load_references(
    store: &crate::comic::store::StoryStore,
    request: &mut GenerationRequest,
) -> Result<(), GenerationError> {
    use std::io::Read;

    const MAX_REFERENCE_BYTES: u64 = 32 * 1024 * 1024;
    if request.reference_paths.len() > 8 {
        return Err(GenerationError::new(
            "bad_request",
            "at most eight reference images are supported",
        ));
    }
    let mut remaining = MAX_REFERENCE_BYTES;
    let mut images = Vec::new();
    for relative in &request.reference_paths {
        let path = store
            .resolve(relative)
            .map_err(|e| GenerationError::new("bad_request", e.message()))?;
        let file = std::fs::File::open(&path).map_err(|e| {
            GenerationError::new(
                "bad_request",
                format!("cannot read reference {relative}: {e}"),
            )
        })?;
        if !file
            .metadata()
            .map_err(|e| GenerationError::new("bad_request", e.to_string()))?
            .is_file()
        {
            return Err(GenerationError::new(
                "bad_request",
                "reference is not a regular file",
            ));
        }
        let mut bytes = Vec::new();
        file.take(remaining + 1)
            .read_to_end(&mut bytes)
            .map_err(|e| GenerationError::new("bad_request", e.to_string()))?;
        if bytes.len() as u64 > remaining {
            return Err(GenerationError::new(
                "bad_request",
                "reference images exceed 32 MiB in total",
            ));
        }
        remaining -= bytes.len() as u64;
        let format = image::guess_format(&bytes).map_err(|e| {
            GenerationError::new("bad_request", format!("invalid reference image: {e}"))
        })?;
        let mime = match format {
            image::ImageFormat::Png => "image/png",
            image::ImageFormat::Jpeg => "image/jpeg",
            image::ImageFormat::WebP => "image/webp",
            _ => {
                return Err(GenerationError::new(
                    "bad_request",
                    "unsupported reference image format",
                ))
            }
        };
        images.push(ReferenceImage {
            name: path
                .file_name()
                .map(|s| s.to_string_lossy().into_owned())
                .unwrap_or_else(|| "reference.png".into()),
            mime: mime.into(),
            data: bytes,
        });
    }
    request.reference_images = images;
    Ok(())
}

/// Classified failure — the task row stores it and the LLM acts on it:
/// `content_policy` asks for a rewritten prompt, `quota`/`auth` for the user,
/// `network` for a retry, `bad_request` for the agent's own parameters.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GenerationError {
    pub kind: String,
    pub message: String,
}

impl GenerationError {
    pub fn new(kind: &str, message: impl Into<String>) -> Self {
        Self {
            kind: kind.to_string(),
            message: message.into(),
        }
    }
}

impl std::fmt::Display for GenerationError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.kind, self.message)
    }
}

impl std::error::Error for GenerationError {}

/// Where a submitted job stands. `progress` is a free-form vendor payload the
/// task row keeps in `result_json` for the workbench.
#[derive(Debug, Clone)]
pub enum PollState {
    Running(Value),
    Succeeded(Vec<Vec<u8>>),
    Failed(GenerationError),
}

/// One vendor adapter. `submit` starts a job and returns an opaque handle
/// (JSON) the task row persists; `poll` advances it. A synchronous vendor
/// returns `Succeeded` (or `Failed`) from its very first `poll`.
#[async_trait::async_trait]
pub trait MediaProvider: Send + Sync {
    fn kind(&self) -> MediaKind;

    fn capabilities(&self) -> capabilities::MediaCapabilities;

    fn prepare(&self, req: &mut GenerationRequest) -> Result<(), GenerationError> {
        self.capabilities().prepare(req)
    }

    async fn submit(&self, req: &GenerationRequest) -> Result<Value, GenerationError>;

    async fn poll(
        &self,
        handle: &Value,
        req: &GenerationRequest,
    ) -> Result<PollState, GenerationError>;
}

/// List model IDs for an endpoint before it is saved. OpenAI-compatible
/// relays and new-api gateways expose `GET /v1/models`; Ark's "models" are
/// per-account inference endpoints (`ep-…`) with no public list, so it
/// refuses with guidance instead of returning an empty lie.
pub async fn list_models(
    kind: MediaKind,
    api_url: &str,
    api_key: &str,
    extra_json: &str,
) -> Result<Vec<String>, GenerationError> {
    let extra: Value = serde_json::from_str(extra_json).map_err(|error| {
        GenerationError::new("bad_request", format!("invalid provider options: {error}"))
    })?;
    let protocol = extra
        .get("protocol")
        .and_then(Value::as_str)
        .unwrap_or_else(|| default_protocol(kind));
    match (kind, protocol) {
        (MediaKind::Image | MediaKind::Tts, "openai_compat") | (MediaKind::Video, "newapi") => {}
        (MediaKind::Video, "ark") => {
            return Err(GenerationError::new(
                "bad_request",
                "Ark models are per-account inference endpoints (ep-…); copy the \
                 endpoint ID from the Volcengine console and enter it manually",
            ))
        }
        _ => {
            return Err(GenerationError::new(
                "bad_request",
                "model listing is not supported for this protocol; enter the model ID manually",
            ))
        }
    }
    let mut url = http::http_url(api_url, "bad_request")?;
    let base = url.path().trim_end_matches('/');
    url.set_path(&if base.ends_with("/v1") {
        format!("{base}/models")
    } else {
        format!("{base}/v1/models")
    });
    let client = http::client(http::SUBMIT_TIMEOUT)?;
    let response = client
        .get(url)
        .bearer_auth(api_key)
        .send()
        .await
        .map_err(|e| http::transport_error(api_key, &e))?;
    let status = response.status().as_u16();
    let payload = http::read_capped(api_key, response, 4 * 1024 * 1024).await?;
    if !(200..300).contains(&status) {
        let text = String::from_utf8_lossy(&payload);
        return Err(http::status_error(api_key, status, &text));
    }
    let parsed: Value = serde_json::from_slice(&payload)
        .map_err(|e| GenerationError::new("bad_response", format!("decode: {e}")))?;
    let mut ids: Vec<String> = parsed
        .get("data")
        .and_then(Value::as_array)
        .map(|data| {
            data.iter()
                .filter_map(|model| model.get("id").and_then(Value::as_str))
                .map(str::to_owned)
                .collect()
        })
        .unwrap_or_default();
    if ids.is_empty() {
        return Err(GenerationError::new(
            "bad_response",
            "the endpoint returned no models",
        ));
    }
    ids.sort();
    ids.dedup();
    ids.truncate(500);
    Ok(ids)
}

/// Build the adapter for a pool row. `kind` is validated by the caller
/// (`media_provider_service::validate_kind`); an unknown `protocol` in
/// `extra_json` names the adapter. An omitted protocol falls back to the
/// kind's implemented default — `ark` for video, `openai_compat` for
/// image/tts — while an explicitly configured unsupported protocol stays
/// visible as unavailable rather than being silently rewritten.
pub fn default_protocol(kind: MediaKind) -> &'static str {
    match kind {
        MediaKind::Image | MediaKind::Tts => "openai_compat",
        MediaKind::Video => "ark",
    }
}

pub fn build_provider(
    kind: MediaKind,
    api_url: &str,
    api_key: &str,
    model: &str,
    extra_json: &str,
) -> Result<Box<dyn MediaProvider>, GenerationError> {
    let extra: Value = serde_json::from_str(extra_json).map_err(|error| {
        GenerationError::new("bad_request", format!("invalid provider options: {error}"))
    })?;
    let protocol = extra
        .get("protocol")
        .and_then(Value::as_str)
        .unwrap_or_else(|| default_protocol(kind));
    match (kind, protocol) {
        (MediaKind::Image | MediaKind::Tts, "openai_compat") => {
            Ok(Box::new(openai_compat::OpenAiCompatProvider {
                kind,
                api_url: api_url.to_string(),
                api_key: api_key.to_string(),
                model: model.to_string(),
                extra,
            }))
        }
        (MediaKind::Image, "comfyui") => Ok(Box::new(comfyui::ComfyUiProvider {
            api_url: api_url.to_string(),
            api_key: api_key.to_string(),
            extra,
        })),
        (MediaKind::Video, "ark") => Ok(Box::new(ark::ArkVideoProvider {
            api_url: api_url.to_string(),
            api_key: api_key.to_string(),
            model: model.to_string(),
            extra,
        })),
        (MediaKind::Video, "newapi") => Ok(Box::new(newapi::NewApiVideoProvider {
            api_url: api_url.to_string(),
            api_key: api_key.to_string(),
            model: model.to_string(),
            extra,
        })),
        (MediaKind::Image, other) => Err(GenerationError::new(
            "bad_request",
            format!("unknown image provider protocol: {other}"),
        )),
        (kind, other) => Err(GenerationError::new(
            "bad_request",
            format!("no {protocol} adapter for {kind:?} yet: {other}"),
        )),
    }
}

#[cfg(test)]
mod list_models_tests {
    use super::*;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    async fn mock_models(listener: tokio::net::TcpListener, reply: &'static str) -> String {
        let (mut socket, _) = listener.accept().await.unwrap();
        let mut buffer = vec![0_u8; 4096];
        let read = socket.read(&mut buffer).await.unwrap();
        let request = String::from_utf8_lossy(&buffer[..read]).into_owned();
        socket
            .write_all(
                format!(
                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{reply}",
                    reply.len()
                )
                .as_bytes(),
            )
            .await
            .unwrap();
        request
    }

    #[tokio::test]
    async fn openai_compat_and_newapi_list_models_from_v1_models() {
        for (kind, extra) in [
            (MediaKind::Image, r#"{"protocol":"openai_compat"}"#),
            (MediaKind::Video, r#"{"protocol":"newapi"}"#),
        ] {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let addr = listener.local_addr().unwrap();
            let server = tokio::spawn(mock_models(
                listener,
                r#"{"data":[{"id":"b-model"},{"id":"a-model"}]}"#,
            ));
            let models = list_models(kind, &format!("http://{addr}/v1"), "test-key", extra)
                .await
                .unwrap();
            let request = server.await.unwrap();
            assert!(request.starts_with("GET /v1/models "), "{request}");
            assert!(request
                .to_ascii_lowercase()
                .contains("authorization: bearer test-key"));
            assert_eq!(models, vec!["a-model".to_string(), "b-model".to_string()]);
        }
    }

    #[tokio::test]
    async fn ark_refuses_listing_and_points_at_the_console_endpoint_id() {
        let error = list_models(MediaKind::Video, "http://ark", "k", r#"{"protocol":"ark"}"#)
            .await
            .unwrap_err();
        assert_eq!(error.kind, "bad_request");
        assert!(error.message.contains("endpoint"), "{error}");
    }
}

/// Shared HTTP helpers for the adapters — the `provider_models` discipline,
/// without its reply type.
pub(crate) mod http {
    use std::time::Duration;

    use reqwest::Client;

    use super::GenerationError;

    pub const SUBMIT_TIMEOUT: Duration = Duration::from_secs(30);
    /// Image downloads can be slow on relays; still bounded.
    pub const DOWNLOAD_TIMEOUT: Duration = Duration::from_secs(120);

    pub fn client(timeout: Duration) -> Result<Client, GenerationError> {
        let mut builder = Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .connect_timeout(Duration::from_secs(10))
            .timeout(timeout)
            .user_agent(concat!("codeg/", env!("CARGO_PKG_VERSION")));
        builder = builder.no_proxy();
        builder
            .build()
            .map_err(|e| GenerationError::new("network", format!("HTTP client: {e}")))
    }

    /// Classify a reqwest transport failure; the key never appears (it is not
    /// in transport errors), but scrub anyway for query-string paranoia.
    pub fn transport_error(secret: &str, e: &reqwest::Error) -> GenerationError {
        let text = if e.is_timeout() {
            "request timed out".to_string()
        } else {
            format!("request failed: {e}")
        };
        GenerationError::new("network", scrub(&text, secret))
    }

    /// Read a response body with a size cap, scrubbing the key from any error.
    pub async fn read_capped(
        secret: &str,
        response: reqwest::Response,
        cap: usize,
    ) -> Result<bytes::Bytes, GenerationError> {
        use futures::StreamExt;

        let status = response.status();
        let cap = if status.is_success() {
            cap
        } else {
            cap.min(64 * 1024)
        };
        if response
            .content_length()
            .is_some_and(|length| length > cap as u64)
        {
            return Err(GenerationError::new(
                "too_large",
                format!("response exceeds {cap} bytes"),
            ));
        }
        let mut stream = response.bytes_stream();
        let mut body = bytes::BytesMut::new();
        while let Some(chunk) = stream.next().await {
            let chunk = chunk.map_err(|e| transport_error(secret, &e))?;
            if chunk.len() > cap.saturating_sub(body.len()) {
                return Err(GenerationError::new(
                    "too_large",
                    format!("response exceeds {cap} bytes"),
                ));
            }
            body.extend_from_slice(&chunk);
        }
        if !status.is_success() {
            return Err(status_error(
                secret,
                status.as_u16(),
                &String::from_utf8_lossy(&body),
            ));
        }
        Ok(body.freeze())
    }

    /// HTTP status → classified GenerationError (quota / auth / policy / other).
    pub fn status_error(secret: &str, status: u16, body: &str) -> GenerationError {
        let snippet: String = body.chars().take(400).collect();
        let snippet = scrub(&snippet, secret);
        let kind = match status {
            401 | 403 => "auth",
            429 => "quota",
            400 | 422 => {
                // Content-policy rejections ride 400s on most vendors; the
                // message decides.
                let lower = snippet.to_lowercase();
                if lower.contains("policy")
                    || lower.contains("safety")
                    || lower.contains("content_filter")
                    || lower.contains("审核")
                    || lower.contains("敏感")
                {
                    "content_policy"
                } else {
                    "bad_request"
                }
            }
            _ => "network",
        };
        GenerationError::new(kind, format!("HTTP {status}: {snippet}"))
    }

    #[cfg(test)]
    mod tests {
        use super::*;
        use tokio::io::{AsyncReadExt, AsyncWriteExt};

        #[tokio::test]
        async fn response_size_cap_is_enforced_before_waiting_for_the_full_body() {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let addr = listener.local_addr().unwrap();
            let server = tokio::spawn(async move {
                let (mut socket, _) = listener.accept().await.unwrap();
                let mut request = [0_u8; 1024];
                let _ = socket.read(&mut request).await.unwrap();
                socket
                    .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 1000000\r\n\r\n")
                    .await
                    .unwrap();
                tokio::time::sleep(Duration::from_secs(5)).await;
            });
            let response = client(Duration::from_secs(5))
                .unwrap()
                .get(format!("http://{addr}"))
                .send()
                .await
                .unwrap();
            let result =
                tokio::time::timeout(Duration::from_millis(500), read_capped("", response, 4))
                    .await;
            server.abort();
            assert!(
                result.is_ok(),
                "the reader buffered an oversized body instead of rejecting its length"
            );
            assert!(result.unwrap().is_err());
        }
    }

    /// Remove the secret from `text` (case-tolerant best effort, like
    /// `provider_models::scrub`).
    pub fn scrub(text: &str, secret: &str) -> String {
        if secret.is_empty() {
            return text.to_string();
        }
        text.replace(secret, "***")
    }

    /// An absolute HTTP(S) URL without credentials, a query, or a fragment —
    /// the only remote shape any comic adapter will fetch.
    pub(crate) fn http_url(value: &str, error_kind: &str) -> Result<reqwest::Url, GenerationError> {
        let invalid = || {
            GenerationError::new(
                error_kind,
                "URL must be absolute HTTP(S), without credentials, a query, or a fragment",
            )
        };
        let url = reqwest::Url::parse(value).map_err(|_| invalid())?;
        if !matches!(url.scheme(), "http" | "https")
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.fragment().is_some()
        {
            return Err(invalid());
        }
        Ok(url)
    }
}
