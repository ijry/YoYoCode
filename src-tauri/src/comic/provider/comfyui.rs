//! ComfyUI HTTP adapter: submit a workflow template, poll `/history`, download
//! outputs from `/view`.
//!
//! The pool row's `extra_json` carries the workflow contract:
//!
//! ```json
//! {
//!   "protocol": "comfyui",
//!   "workflow": { ...ComfyUI workflow graph, API-format JSON... },
//!   "prompt_node": "6",          // node whose `inputs.text` gets the prompt
//!   "seed_node": "3",            // optional node whose `inputs.seed` is set
//!   "negative_node": "7"         // optional node for the negative prompt
//! }
//! ```
//!
//! Placeholders also work inside any string in the workflow: `{{PROMPT}}`,
//! `{{NEGATIVE}}`, `{{SEED}}` are text-replaced before submission. `batch_node`
//! maps candidate count to an image batch node; `reference_nodes` lists the
//! LoadImage node ids that receive uploaded character/scene references.
//! Missing mappings refuse before a generation job is submitted.

use serde_json::{json, Value};

use super::{http, GenerationError, GenerationRequest, MediaKind, MediaProvider, PollState};

pub struct ComfyUiProvider {
    pub api_url: String,
    pub api_key: String,
    pub extra: Value,
}

impl ComfyUiProvider {
    fn base(&self) -> String {
        self.api_url.trim_end_matches('/').to_string()
    }

    fn workflow(&self) -> Result<Value, GenerationError> {
        let wf = self.extra.get("workflow").cloned().ok_or_else(|| {
            GenerationError::new(
                "bad_request",
                "ComfyUI provider needs extra_json.workflow (API-format graph)",
            )
        })?;
        Ok(wf)
    }

    fn inject(&self, wf: &Value, req: &GenerationRequest) -> Value {
        let mut wf = wf.clone();
        let seed = req.seed.unwrap_or_else(rand_seed);
        // Replace string values, not serialized JSON: quotes, newlines and
        // backslashes in the prompt are data, never graph syntax.
        fn replace(value: &mut Value, req: &GenerationRequest, seed: u64) {
            match value {
                Value::String(text) => {
                    *text = text
                        .replace("{{PROMPT}}", &req.prompt)
                        .replace(
                            "{{NEGATIVE}}",
                            req.negative_prompt.as_deref().unwrap_or_default(),
                        )
                        .replace("{{SEED}}", &seed.to_string());
                }
                Value::Array(values) => values.iter_mut().for_each(|v| replace(v, req, seed)),
                Value::Object(values) => values.values_mut().for_each(|v| replace(v, req, seed)),
                _ => {}
            }
        }
        replace(&mut wf, req, seed);
        // Node-field injection when the extra names the nodes.
        if let Some(obj) = wf.as_object_mut() {
            let mut apply = |node_id: &str, field: &str, value: Value| {
                if let Some(node) = obj.get_mut(node_id).and_then(Value::as_object_mut) {
                    if let Some(inputs) = node.get_mut("inputs").and_then(Value::as_object_mut) {
                        inputs.insert(field.to_string(), value);
                    }
                }
            };
            if let Some(node) = self.extra.get("prompt_node").and_then(Value::as_str) {
                apply(node, "text", json!(req.prompt));
            }
            if let Some(node) = self.extra.get("negative_node").and_then(Value::as_str) {
                apply(
                    node,
                    "text",
                    json!(req.negative_prompt.as_deref().unwrap_or_default()),
                );
            }
            if let Some(node) = self.extra.get("seed_node").and_then(Value::as_str) {
                apply(node, "seed", json!(seed));
            }
            if let Some(node) = self.extra.get("batch_node").and_then(Value::as_str) {
                apply(node, "batch_size", json!(req.count.clamp(1, 64)));
            }
        }
        wf
    }

    async fn upload_reference(
        &self,
        image: &super::ReferenceImage,
    ) -> Result<String, GenerationError> {
        let name = format!("codeg-{}-{}", uuid::Uuid::new_v4(), image.name);
        let part = reqwest::multipart::Part::bytes(image.data.clone())
            .file_name(name)
            .mime_str(&image.mime)
            .map_err(|e| GenerationError::new("bad_request", e.to_string()))?;
        let form = reqwest::multipart::Form::new()
            .part("image", part)
            .text("type", "input");
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let mut request = client
            .post(format!("{}/upload/image", self.base()))
            .multipart(form);
        if !self.api_key.is_empty() {
            request = request.bearer_auth(&self.api_key)
        }
        let response = request
            .send()
            .await
            .map_err(|e| http::transport_error(&self.api_key, &e))?;
        let bytes = http::read_capped(&self.api_key, response, 1024 * 1024).await?;
        let result: Value = serde_json::from_slice(&bytes)
            .map_err(|e| GenerationError::new("bad_response", e.to_string()))?;
        let name = result.get("name").and_then(Value::as_str).ok_or_else(|| {
            GenerationError::new("bad_response", "ComfyUI upload response has no name")
        })?;
        let folder = result
            .get("subfolder")
            .and_then(Value::as_str)
            .unwrap_or_default();
        Ok(if folder.is_empty() {
            name.into()
        } else {
            format!("{folder}/{name}")
        })
    }

    async fn download(
        &self,
        filename: &str,
        subfolder: &str,
        folder_type: &str,
    ) -> Result<Vec<u8>, GenerationError> {
        let url = reqwest::Url::parse_with_params(
            &format!("{}/view", self.base()),
            &[
                ("filename", filename),
                ("subfolder", subfolder),
                ("type", folder_type),
            ],
        )
        .map_err(|e| GenerationError::new("bad_request", format!("view url: {e}")))?;
        let client = http::client(http::DOWNLOAD_TIMEOUT)?;
        let mut req = client.get(url);
        if !self.api_key.is_empty() {
            req = req.bearer_auth(&self.api_key);
        }
        let response = req
            .send()
            .await
            .map_err(|e| http::transport_error(&self.api_key, &e))?;
        let status = response.status().as_u16();
        let data = http::read_capped(&self.api_key, response, 64 * 1024 * 1024).await?;
        if !(200..300).contains(&status) {
            let text = String::from_utf8_lossy(&data);
            return Err(http::status_error(&self.api_key, status, &text));
        }
        Ok(data.to_vec())
    }
}

fn rand_seed() -> u64 {
    // No rng crate in the tree: derive a seed from the clock + a pointer hint.
    use std::time::{SystemTime, UNIX_EPOCH};
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos() as u64)
        .unwrap_or(0);
    nanos ^ (std::ptr::from_ref(&nanos) as u64)
}

#[async_trait::async_trait]
impl MediaProvider for ComfyUiProvider {
    fn kind(&self) -> MediaKind {
        MediaKind::Image
    }

    fn capabilities(&self) -> super::capabilities::MediaCapabilities {
        let mut extra = self.extra.clone();
        extra["protocol"] = json!("comfyui");
        super::capabilities::describe(MediaKind::Image, &extra)
    }

    async fn submit(&self, req: &GenerationRequest) -> Result<Value, GenerationError> {
        self.prepare(&mut req.clone())?;
        if req.count > 1
            && self
                .extra
                .get("batch_node")
                .and_then(Value::as_str)
                .is_none()
        {
            return Err(GenerationError::new(
                "bad_request",
                "ComfyUI candidate batches need a batch_node mapping; no job was submitted",
            ));
        }
        let mut wf = self.inject(&self.workflow()?, req);
        if !req.reference_images.is_empty() {
            let nodes = self
                .extra
                .get("reference_nodes")
                .and_then(Value::as_array)
                .ok_or_else(|| {
                    GenerationError::new(
                        "bad_request",
                        "ComfyUI reference images need reference_nodes mappings",
                    )
                })?;
            if nodes.len() < req.reference_images.len() {
                return Err(GenerationError::new(
                    "bad_request",
                    "not enough ComfyUI reference_nodes mappings",
                ));
            }
            for (index, image) in req.reference_images.iter().enumerate() {
                let node = nodes[index].as_str().ok_or_else(|| {
                    GenerationError::new("bad_request", "reference_nodes entries must be node ids")
                })?;
                let name = self.upload_reference(image).await?;
                let inputs = wf
                    .get_mut(node)
                    .and_then(|node| node.get_mut("inputs"))
                    .and_then(Value::as_object_mut)
                    .ok_or_else(|| {
                        GenerationError::new(
                            "bad_request",
                            "reference node is missing from workflow",
                        )
                    })?;
                inputs.insert("image".into(), Value::String(name));
            }
        }
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let mut submit = client
            .post(format!("{}/prompt", self.base()))
            .json(&json!({ "prompt": wf, "client_id": format!("codeg-{}", std::process::id()) }));
        if !self.api_key.is_empty() {
            submit = submit.bearer_auth(&self.api_key);
        }
        let response = submit
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
            .map_err(|e| GenerationError::new("bad_request", format!("decode: {e}")))?;
        let prompt_id = parsed
            .get("prompt_id")
            .and_then(Value::as_str)
            .ok_or_else(|| {
                GenerationError::new("bad_request", "ComfyUI response has no prompt_id")
            })?;
        Ok(json!({ "prompt_id": prompt_id }))
    }

    async fn poll(
        &self,
        handle: &Value,
        _req: &GenerationRequest,
    ) -> Result<PollState, GenerationError> {
        let prompt_id = handle
            .get("prompt_id")
            .and_then(Value::as_str)
            .ok_or_else(|| GenerationError::new("bad_request", "handle has no prompt_id"))?;
        let client = http::client(http::SUBMIT_TIMEOUT)?;
        let mut history = client.get(format!("{}/history/{prompt_id}", self.base()));
        if !self.api_key.is_empty() {
            history = history.bearer_auth(&self.api_key);
        }
        let response = history
            .send()
            .await
            .map_err(|e| http::transport_error(&self.api_key, &e))?;
        let status = response.status().as_u16();
        let payload = http::read_capped(&self.api_key, response, 16 * 1024 * 1024).await?;
        if !(200..300).contains(&status) {
            let text = String::from_utf8_lossy(&payload);
            return Err(http::status_error(&self.api_key, status, &text));
        }
        let parsed: Value = serde_json::from_slice(&payload)
            .map_err(|e| GenerationError::new("bad_request", format!("decode: {e}")))?;
        let entry = parsed.get(prompt_id).cloned().unwrap_or(Value::Null);
        if entry.is_null() {
            return Ok(PollState::Running(json!({ "state": "queued" })));
        }
        // A job either produced outputs or carries a status with an error.
        let status_str = entry
            .get("status")
            .and_then(|s| s.get("status_str"))
            .and_then(Value::as_str)
            .unwrap_or("");
        if status_str == "error" {
            let messages = entry
                .get("status")
                .and_then(|s| s.get("messages"))
                .cloned()
                .unwrap_or(Value::Null);
            return Ok(PollState::Failed(GenerationError::new(
                "internal",
                format!("ComfyUI reported an error: {messages}"),
            )));
        }
        let outputs = entry.get("outputs").and_then(Value::as_object);
        let Some(outputs) = outputs else {
            return Ok(PollState::Running(json!({ "state": "running" })));
        };
        let mut artifacts = Vec::new();
        for node in outputs.values() {
            let Some(images) = node.get("images").and_then(Value::as_array) else {
                continue;
            };
            for image in images {
                let Some(filename) = image.get("filename").and_then(Value::as_str) else {
                    continue;
                };
                let subfolder = image
                    .get("subfolder")
                    .and_then(Value::as_str)
                    .unwrap_or_default();
                let folder_type = image
                    .get("type")
                    .and_then(Value::as_str)
                    .unwrap_or("output");
                artifacts.push(self.download(filename, subfolder, folder_type).await?);
            }
        }
        if artifacts.is_empty() {
            return Ok(PollState::Running(json!({ "state": "running" })));
        }
        Ok(PollState::Succeeded(artifacts))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn candidate_count_is_applied_to_the_configured_batch_node() {
        let provider = ComfyUiProvider {
            api_url: "http://127.0.0.1:8188".into(),
            api_key: String::new(),
            extra: json!({"batch_node": "5"}),
        };
        let wf = json!({"5": {"inputs": {"batch_size": 1}}});
        let result = provider.inject(
            &wf,
            &GenerationRequest {
                count: 4,
                ..Default::default()
            },
        );
        assert_eq!(result["5"]["inputs"]["batch_size"], 4);
    }

    #[test]
    fn prompt_quotes_and_newlines_are_preserved_in_workflow_strings() {
        let provider = ComfyUiProvider {
            api_url: "http://127.0.0.1:8188".into(),
            api_key: String::new(),
            extra: Value::Null,
        };
        let prompt = "A character says \"hello\"\nnext line: C:\\studio";
        let workflow = json!({"6": {"inputs": {"text": "scene: {{PROMPT}}"}}});
        let injected = provider.inject(
            &workflow,
            &GenerationRequest {
                prompt: prompt.into(),
                seed: Some(42),
                ..Default::default()
            },
        );
        assert_eq!(injected["6"]["inputs"]["text"], format!("scene: {prompt}"));
    }
}
