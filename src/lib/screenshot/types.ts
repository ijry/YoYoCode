// Mirror of the payload types the Rust side serializes for the screenshot
// commands (`src-tauri/src/commands/screenshot.rs`). Every struct there is
// `#[serde(rename_all = "camelCase")]`, and the names here have to match it
// field for field — a typo is not an error anywhere, just `undefined` where
// data should be.

/** A selection rectangle, in the physical pixels of the frame it was drawn
 *  on. The overlay window's CSS coordinates are scaled by the device pixel
 *  ratio before they get here. */
export interface ScreenshotRect {
  x: number
  y: number
  width: number
  height: number
}

/** Native window bounds in local monitor frame physical pixels. */
export type ScreenshotWindowBounds = ScreenshotRect

/** One monitor's frozen frame, as the overlay window reads it back. The
 *  picture is the whole monitor, at the monitor's own pixel size. */
export interface MonitorFramePayload {
  monitorIndex: number
  pngBase64: string
  width: number
  height: number
  /** Front-to-back native z-order, frozen with the frame. Older payloads
   *  omit this; treat them as an empty list. */
  windows?: ScreenshotWindowBounds[]
}

/** The cropped picture the editor edits, straight out of the frame store.
 *  Taking it releases the frame — there is no second read. */
export interface CroppedPayload {
  pngBase64: string
  width: number
  height: number
  mime: string
}

/** One monitor a capture covers, as `screenshot_begin_selection` reported
 *  it. Width and height are physical pixels; the frame is this big. */
export interface ScreenshotMonitorInfo {
  index: number
  name: string
  width: number
  height: number
}

/** What `screenshot_begin_selection` answers: the capture the frames are
 *  filed under, the request that started it, and the monitors it covers. */
export interface ScreenshotBeginOutcome {
  captureId: string
  requestId: string
  monitors: ScreenshotMonitorInfo[]
}

/** The `screenshot://captured` event: metadata only, never image bytes — a
 *  Tauri event is injected into the webview, and tens of megabytes of base64
 *  would freeze the UI for seconds. The editor pulls the picture with
 *  `screenshot_take_cropped` instead. */
export interface ScreenshotCapturedEvent {
  captureId: string
  requestId: string
  width: number
  height: number
  mime: string
}
