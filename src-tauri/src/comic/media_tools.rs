//! Required local media programs, checked before any paid submission.

use super::compose::{run_process, ComposeError};
use super::provider::MediaKind;
use tokio_util::sync::CancellationToken;

pub(super) struct MediaTools {
    pub ffmpeg: &'static str,
    pub ffprobe: &'static str,
}

pub(super) const HOST_TOOLS: MediaTools = MediaTools {
    ffmpeg: "ffmpeg",
    ffprobe: "ffprobe",
};

impl MediaTools {
    pub async fn check(&self, kind: MediaKind) -> Result<(), ComposeError> {
        if kind == MediaKind::Image {
            return Ok(());
        }
        let cancel = CancellationToken::new();
        let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(10);
        for program in [self.ffmpeg, self.ffprobe] {
            run_process(
                program,
                &["-version".into()],
                std::path::Path::new("."),
                &cancel,
                deadline,
            )
            .await?;
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn images_do_not_require_local_audio_or_video_tools() {
        let tools = MediaTools {
            ffmpeg: "codeg-missing-ffmpeg-test",
            ffprobe: "codeg-missing-ffprobe-test",
        };
        assert!(tools.check(MediaKind::Image).await.is_ok());
        assert!(matches!(
            tools.check(MediaKind::Tts).await,
            Err(ComposeError::Unavailable)
        ));
        assert!(matches!(
            tools.check(MediaKind::Video).await,
            Err(ComposeError::Unavailable)
        ));
    }
}
