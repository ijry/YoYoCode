//! Parameters an adapter can actually transport, narrowed by model configuration.

use serde::Serialize;
use serde_json::Value;

use super::{default_protocol, GenerationError, GenerationRequest, MediaKind};

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MediaCapabilities {
    pub kind: MediaKind,
    pub protocol: String,
    pub available: bool,
    pub max_candidates: u32,
    pub max_references: usize,
    pub seed: bool,
    pub negative_prompt: bool,
    pub size: bool,
    pub speech: Option<SpeechCapabilities>,
    pub video: Option<VideoCapabilities>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct VideoCapabilities {
    pub min_duration_secs: f64,
    pub max_duration_secs: f64,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SpeechCapabilities {
    pub formats: Vec<String>,
    pub voices: Vec<String>,
    pub default_voice: Option<String>,
    pub min_speed: f64,
    pub max_speed: f64,
    pub max_chars: usize,
    pub instructions: bool,
}

pub fn describe(kind: MediaKind, extra: &Value) -> MediaCapabilities {
    let protocol = extra
        .get("protocol")
        .and_then(Value::as_str)
        .unwrap_or_else(|| default_protocol(kind));
    let constraints = extra.get("capabilities").unwrap_or(&Value::Null);
    let mut result = MediaCapabilities {
        kind,
        protocol: protocol.into(),
        available: false,
        max_candidates: 1,
        max_references: 0,
        seed: false,
        negative_prompt: false,
        size: false,
        speech: None,
        video: None,
    };
    match (kind, protocol) {
        (MediaKind::Image, "openai_compat") => {
            result.available = true;
            result.max_candidates = bounded_u32(constraints, "maxCandidates", 64, 64);
            result.max_references = bounded_u32(constraints, "maxReferences", 8, 8) as usize;
            result.seed = constraints
                .get("seed")
                .and_then(Value::as_bool)
                .unwrap_or(false);
            result.negative_prompt = constraints
                .get("negativePrompt")
                .and_then(Value::as_bool)
                .unwrap_or(false);
            result.size = constraints
                .get("size")
                .and_then(Value::as_bool)
                .unwrap_or(true);
        }
        (MediaKind::Image, "comfyui") => {
            let workflow = extra.get("workflow").unwrap_or(&Value::Null);
            let mapped = |key: &str| {
                extra
                    .get(key)
                    .and_then(Value::as_str)
                    .is_some_and(|id| node_inputs(workflow, id))
            };
            result.available = workflow.is_object()
                && (mapped("prompt_node") || contains_placeholder(workflow, "{{PROMPT}}"));
            result.max_candidates = if mapped("batch_node") {
                bounded_u32(constraints, "maxCandidates", 64, 64)
            } else {
                1
            };
            result.seed = mapped("seed_node") || contains_placeholder(workflow, "{{SEED}}");
            result.negative_prompt =
                mapped("negative_node") || contains_placeholder(workflow, "{{NEGATIVE}}");
            result.max_references = extra
                .get("reference_nodes")
                .and_then(Value::as_array)
                .map(|ids| {
                    ids.iter()
                        .take_while(|id| id.as_str().is_some_and(|id| node_inputs(workflow, id)))
                        .count()
                        .min(8)
                })
                .unwrap_or(0);
        }
        (MediaKind::Tts, "openai_compat") => {
            result.available = true;
            let voices = constraints
                .get("voices")
                .and_then(Value::as_array)
                .map(|voices| {
                    voices
                        .iter()
                        .filter_map(Value::as_str)
                        .map(str::to_owned)
                        .collect()
                })
                .unwrap_or_default();
            let default_voice = extra
                .get("voice")
                .or_else(|| extra.get("body").and_then(|body| body.get("voice")))
                .and_then(Value::as_str)
                .filter(|voice| !voice.trim().is_empty())
                .map(str::to_owned);
            let formats = constraints
                .get("formats")
                .and_then(Value::as_array)
                .map(|formats| {
                    formats
                        .iter()
                        .filter_map(Value::as_str)
                        .filter(|format| matches!(*format, "mp3" | "wav"))
                        .map(str::to_owned)
                        .collect()
                })
                .unwrap_or_else(|| vec!["mp3".into(), "wav".into()]);
            result.speech = Some(SpeechCapabilities {
                voices,
                formats,
                default_voice,
                min_speed: constraints
                    .get("minSpeed")
                    .and_then(Value::as_f64)
                    .unwrap_or(0.25)
                    .clamp(0.25, 4.0),
                max_speed: constraints
                    .get("maxSpeed")
                    .and_then(Value::as_f64)
                    .unwrap_or(4.0)
                    .clamp(0.25, 4.0),
                max_chars: bounded_u32(constraints, "maxChars", 4096, 4096) as usize,
                instructions: constraints
                    .get("instructions")
                    .and_then(Value::as_bool)
                    .unwrap_or(false),
            });
        }
        (MediaKind::Video, "ark" | "newapi") => {
            result.available = true;
            result.max_references = if protocol == "newapi" {
                0
            } else {
                bounded_u32(constraints, "maxReferences", 0, 8) as usize
            };
            result.video = Some(VideoCapabilities {
                min_duration_secs: constraints
                    .get("minDurationSecs")
                    .and_then(Value::as_f64)
                    .unwrap_or(4.0)
                    .clamp(1.0, 120.0),
                max_duration_secs: constraints
                    .get("maxDurationSecs")
                    .and_then(Value::as_f64)
                    .unwrap_or(30.0)
                    .clamp(1.0, 120.0),
            });
        }
        _ => {}
    }
    result
}

fn bounded_u32(value: &Value, key: &str, default: u32, hard_max: u32) -> u32 {
    value
        .get(key)
        .and_then(Value::as_u64)
        .and_then(|n| u32::try_from(n).ok())
        .unwrap_or(default)
        .min(hard_max)
}

fn node_inputs(workflow: &Value, id: &str) -> bool {
    workflow
        .get(id)
        .and_then(|node| node.get("inputs"))
        .is_some_and(Value::is_object)
}

fn contains_placeholder(value: &Value, placeholder: &str) -> bool {
    match value {
        Value::String(text) => text.contains(placeholder),
        Value::Array(values) => values
            .iter()
            .any(|value| contains_placeholder(value, placeholder)),
        Value::Object(values) => values
            .values()
            .any(|value| contains_placeholder(value, placeholder)),
        _ => false,
    }
}

impl MediaCapabilities {
    pub fn prepare(&self, request: &mut GenerationRequest) -> Result<(), GenerationError> {
        let invalid = |message| GenerationError::new("bad_request", message);
        if !self.available {
            return Err(invalid(
                "this media protocol is not implemented for the selected kind",
            ));
        }
        if request.prompt.trim().is_empty() {
            return Err(invalid("media prompt/text must not be blank"));
        }
        if request.count == 0 || request.count > self.max_candidates {
            return Err(invalid(
                "candidate count exceeds this provider's capability",
            ));
        }
        let references = request
            .reference_paths
            .len()
            .max(request.reference_images.len());
        if references > self.max_references {
            return Err(invalid(
                "reference images exceed this provider's capability",
            ));
        }
        if request.seed.is_some() && !self.seed {
            return Err(invalid(
                "this provider does not support a seed; remove it or configure a supported adapter",
            ));
        }
        if request.negative_prompt.is_some() && !self.negative_prompt {
            return Err(invalid("this provider does not support a negative prompt"));
        }
        if request.size.is_some() && !self.size {
            return Err(invalid("this provider does not support the requested size"));
        }
        // The shot's bound frame rides a dedicated first-frame field, not the
        // reference list, so text-only vendors and reference-less configs are
        // unaffected.
        if request.first_frame.is_some() || request.first_frame_path.is_some() {
            if self.kind != MediaKind::Video || !self.available {
                return Err(invalid(
                    "a first-frame image is only supported by video providers",
                ));
            }
        }
        if self.kind == MediaKind::Video {
            // A job-based video vendor consumes a small closed set of per-call
            // options; anything else would be silently dropped downstream.
            if let Some(extra) = request.extra.as_object() {
                if !extra.is_empty() {
                    if extra
                        .keys()
                        .any(|key| !matches!(key.as_str(), "duration_secs" | "ratio"))
                    {
                        return Err(invalid("unhandled per-call vendor options would be ignored; configure them on the provider instead"));
                    }
                    if let Some(video) = &self.video {
                        if let Some(duration) = extra.get("duration_secs").and_then(Value::as_f64) {
                            if !duration.is_finite()
                                || duration < video.min_duration_secs
                                || duration > video.max_duration_secs
                            {
                                return Err(GenerationError::new(
                                    "bad_request",
                                    format!(
                                        "video duration must be {}–{} seconds for this provider",
                                        video.min_duration_secs, video.max_duration_secs
                                    ),
                                ));
                            }
                        }
                    }
                }
            }
        } else if !request.extra.is_null()
            && request
                .extra
                .as_object()
                .is_none_or(|extra| !extra.is_empty())
        {
            return Err(invalid("unhandled per-call vendor options would be ignored; configure them on the provider instead"));
        }
        match (&self.speech, self.kind) {
            (Some(capabilities), MediaKind::Tts) => {
                if request.prompt.chars().count() > capabilities.max_chars {
                    return Err(invalid("speech text exceeds this model's limit"));
                }
                let speech = request.speech.get_or_insert_with(Default::default);
                if speech
                    .voice
                    .as_deref()
                    .is_none_or(|voice| voice.trim().is_empty())
                {
                    speech.voice = capabilities.default_voice.clone();
                }
                let voice = speech
                    .voice
                    .as_deref()
                    .filter(|voice| !voice.trim().is_empty())
                    .ok_or_else(|| {
                        invalid("speech needs a voice (per call or provider default)")
                    })?;
                if voice.len() > 256
                    || (!capabilities.voices.is_empty()
                        && !capabilities.voices.iter().any(|item| item == voice))
                {
                    return Err(invalid("speech voice is not supported by this provider"));
                }
                if !speech.speed.is_finite()
                    || speech.speed < capabilities.min_speed
                    || speech.speed > capabilities.max_speed
                {
                    return Err(invalid(
                        "speech speed is outside this provider's capability",
                    ));
                }
                if !capabilities.formats.contains(&speech.response_format) {
                    return Err(invalid(
                        "speech response format is not supported (use mp3/wav)",
                    ));
                }
                if speech.instructions.is_some() && !capabilities.instructions {
                    return Err(invalid("this speech model does not support instructions"));
                }
            }
            (_, _) if request.speech.is_some() => {
                return Err(invalid(
                    "speech parameters cannot be sent to an image/video provider",
                ))
            }
            _ => {}
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn request(value: Value) -> GenerationRequest {
        serde_json::from_value(value).unwrap()
    }

    #[test]
    fn first_frame_images_are_video_only_and_are_not_references() {
        for (kind, extra) in [
            (MediaKind::Image, json!({"protocol":"openai_compat"})),
            (MediaKind::Tts, json!({})),
        ] {
            let caps = describe(kind, &extra);
            let mut request = request(json!({
                "prompt": "x",
                "first_frame_path": "episodes/ep001/shots/shot-0001/first-frame.png",
            }));
            let error = caps.prepare(&mut request).unwrap_err();
            assert_eq!(error.kind, "bad_request");
        }
        // The first frame rides a dedicated field: a provider that refuses
        // references still accepts exactly the shot's bound frame.
        for protocol in ["ark", "newapi"] {
            let caps = describe(MediaKind::Video, &json!({ "protocol": protocol }));
            let mut ok = request(json!({
                "prompt": "boat",
                "first_frame_path": "episodes/ep001/shots/shot-0001/first-frame.png",
            }));
            caps.prepare(&mut ok).unwrap();
            let mut still_refuses_references = request(json!({
                "prompt": "boat",
                "reference_paths": ["episodes/ep001/shots/shot-0001/first-frame.png"],
            }));
            assert!(caps.prepare(&mut still_refuses_references).is_err());
        }
    }

    #[test]
    fn unsupported_video_and_unhandled_image_options_refuse_before_submission() {
        // An omitted protocol resolves to the kind's implemented default, so
        // a bare video row is Ark — available, with its duration window.
        let video = describe(MediaKind::Video, &Value::Null);
        assert!(video.available);
        assert_eq!(video.protocol, "ark");
        assert_eq!(video.video.as_ref().unwrap().min_duration_secs, 4.0);
        assert_eq!(video.video.as_ref().unwrap().max_duration_secs, 30.0);
        // An explicitly configured unsupported protocol stays named and
        // unavailable; it is never rewritten into a working default.
        let unsupported = describe(MediaKind::Video, &json!({"protocol":"veo"}));
        assert!(!unsupported.available);
        assert_eq!(unsupported.protocol, "veo");
        assert!(unsupported
            .prepare(&mut request(json!({"prompt":"video"})))
            .is_err());
        let image = describe(
            MediaKind::Image,
            &json!({"capabilities":{"maxCandidates":2,"maxReferences":1}}),
        );
        assert!(image.available);
        for value in [
            json!({"prompt":"image","count":3}),
            json!({"prompt":"image","seed":42}),
            json!({"prompt":"image","negative_prompt":"bad"}),
            json!({"prompt":"image","reference_paths":["a.png","b.png"]}),
            json!({"prompt":"image","extra":{"ignored":true}}),
        ] {
            assert!(image.prepare(&mut request(value)).is_err());
        }
        assert!(image
            .prepare(&mut request(json!({"prompt":"image"})))
            .is_ok());
    }

    #[test]
    fn speech_defaults_are_normalized_and_image_fields_are_rejected() {
        let speech = describe(
            MediaKind::Tts,
            &json!({"voice":"alloy","capabilities":{"voices":["alloy","nova"]}}),
        );
        assert!(speech.available);
        let mut req = request(json!({"prompt":"Hello"}));
        speech.prepare(&mut req).unwrap();
        let normalized = serde_json::to_value(req).unwrap();
        assert_eq!(normalized["speech"]["voice"], "alloy");
        assert_eq!(normalized["speech"]["responseFormat"], "mp3");
        assert_eq!(normalized["speech"]["speed"], 1.0);
        for value in [
            json!({"prompt":"Hello","seed":1}),
            json!({"prompt":"Hello","size":"square"}),
            json!({"prompt":"Hello","count":2}),
            json!({"prompt":"Hello","speech":{"speed":5}}),
            json!({"prompt":"Hello","speech":{"voice":"unknown"}}),
            json!({"prompt":"Hello","speech":{"responseFormat":"pcm"}}),
            json!({"prompt":"Hello","speech":{"instructions":"sing"}}),
        ] {
            assert!(speech.prepare(&mut request(value)).is_err());
        }
        assert!(describe(MediaKind::Tts, &Value::Null)
            .prepare(&mut request(json!({"prompt":"Hello"})))
            .is_err());
    }

    #[test]
    fn comfyui_capabilities_require_real_workflow_mappings() {
        let caps = describe(
            MediaKind::Image,
            &json!({"protocol":"comfyui","workflow":{"3":{"inputs":{"seed":1}},"4":{"inputs":{"text":"{{PROMPT}}"}},"5":{"inputs":{"batch_size":1}},"6":{"inputs":{"image":"old.png"}}},"seed_node":"3","batch_node":"5","reference_nodes":["6"]}),
        );
        assert!(caps.available && caps.seed);
        assert_eq!(caps.max_references, 1);
        assert!(caps.max_candidates > 1);
        let missing = describe(
            MediaKind::Image,
            &json!({"protocol":"comfyui","workflow":{},"batch_node":"missing","reference_nodes":["missing"]}),
        );
        assert_eq!(missing.max_references, 0);
        assert_eq!(missing.max_candidates, 1);
        assert!(!missing.seed);
    }
}
