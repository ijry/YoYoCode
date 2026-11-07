# 截图捕获与标注 实施计划（Screenshot Capture & Annotation Implementation Plan）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在对话输入框"+"右侧提供截图按钮（悬停可选截图 / 隐藏窗口截图），选区后进入标注编辑器，成图走现有图片附件链路。

**Architecture:** Rust 侧用 xcap 捕获所有显示器冻结帧存入 `AppState` 内存帧仓库，为每屏创建全屏覆盖窗做选区；选区结果只以元数据事件回传主窗，编辑器经 invoke 从帧仓库拉图；标注画布从 `browser/screenshot-markup.ts` 模式泛化为独立 `lib/screenshot/annotation.ts`。

**Tech Stack:** Tauri 2 + xcap 0.9 + image/base64（已有）、Next.js 16 + React 19 + Radix UI + lucide-react + sonner。

**Spec:** `docs/screenshot-capture-design.md`（已审批）

## Global Constraints

- 静态导出：覆盖窗必须是平铺路由 `src/app/screenshot-overlay/page.tsx`（查询参数），禁止动态路由
- `server-bin` / `mcp-bin` 不得引入 xcap：依赖挂 `tauri-runtime` feature（`xcap = { version = "0.9", optional = true }`），帧仓库结构体本身不依赖 xcap
- 仅桌面可用：按钮只在 `isLocalDesktop()` 时渲染（`src/lib/platform.ts`）；无 web handler、无 `_core` 变体
- 事件 `screenshot://captured` 只带元数据，禁止携带 base64 字节
- 导出预算：PNG 优先，超过 `MARKED_IMAGE_MAX_BYTES`（3_500_000，从 `src/lib/browser/screenshot-markup.ts` 导入）改 JPEG q0.85
- 所有 Rust payload 结构体加 `#[serde(rename_all = "camelCase")]`；错误一律 `AppCommandError`（`src-tauri/src/app_error.rs`）
- i18n：新增文案必须同步全部 10 个语言文件（`src/i18n/messages/`）
- 前端 Prettier 无分号、TS strict、lucide-react 图标；Rust 2021 edition
- 每个任务完成即跑对应测试并提交一次

## Review Focus

最容易咬人的五类输入（每条已在对应任务钉了测试或手工项）：

1. **Windows 缩放 125%/150%（DPI）**：选区必须换算成物理像素提交，否则裁剪错位。→ `cssRectToPhysical` 单测（Task 5，scale≠1 断言）
2. **退化选区**：单击/1px 拖拽必须被拒。→ 覆盖层 `selectionTooSmall` 单测（Task 5）+ Rust `validate_selection_rect` 单测（Task 1）双保险
3. **双重截图竞态**：上一轮覆盖窗未关又开始新一轮。→ `begin_capture` 清空旧条目 + Rust 关闭旧覆盖窗，store 单测（Task 1）+ 命令步骤断言（Task 2）
4. **取消/完成时序**：完成后覆盖窗卸载再触发 cancel 不得清掉已裁剪的图。→ `cancel` 在 `cropped` 已存在时 no-op，store 单测（Task 1）
5. **4K 大图**：PNG 超预算必须落 JPEG 兜底、负面坐标显示器（x<0）窗口定位不得偏移。→ JPEG 兜底 mock `toBlob` 单测（Task 6）；负坐标定位列入 Task 7 手工验收清单

---

### Task 1: Rust 基础 — xcap 依赖、帧仓库、裁剪纯函数

**Files:**
- Modify: `src-tauri/Cargo.toml`（dependencies + features）
- Create: `src-tauri/src/commands/screenshot.rs`
- Modify: `src-tauri/src/commands/mod.rs`（若有 `pub mod` 列表）、`src-tauri/src/app_state.rs`、AppState 构造点（`src-tauri/src/lib.rs` 与 `src-tauri/src/bin/codeg_server.rs`）
- Test: `src-tauri/src/commands/screenshot.rs` 内 `#[cfg(test)]`

**Interfaces:**
- Produces（Task 2 依赖，全部不加 cfg，保证两种运行时都能编译）:

```rust
pub const OVERLAY_WINDOW_PREFIX: &str = "screenshot-overlay-"

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotRect { pub x: f64, pub y: f64, pub width: f64, pub height: f64 }

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct NormalizedRect { pub x: u32, pub y: u32, pub width: u32, pub height: u32 }

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MonitorFramePayload { pub monitor_index: u32, pub png_base64: String, pub width: u32, pub height: u32 }

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CroppedPayload { pub png_base64: String, pub width: u32, pub height: u32, pub mime: String }

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScreenshotCapturedMeta { pub request_id: String, pub width: u32, pub height: u32 }

pub struct ScreenshotFrameStore { /* std::sync::Mutex<HashMap<String, ScreenshotCapture>> */ }
impl Default for ScreenshotFrameStore
impl ScreenshotFrameStore {
    pub fn begin_capture(&self, request_id: String, hide_main: bool) -> String   // 清空全部旧条目，返回新 capture_id（uuid::Uuid::new_v4）
    pub fn insert_frames(&self, capture_id: &str, frames: Vec<(u32, MonitorFramePayload)>)
    pub fn get_frame(&self, capture_id: &str, monitor_index: u32) -> Option<MonitorFramePayload>
    pub fn store_cropped(&self, capture_id: &str, cropped: CroppedPayload) -> Option<ScreenshotCapturedMeta>  // 返回 request_id 供事件回发
    pub fn take_cropped(&self, capture_id: &str) -> Option<CroppedPayload>       // 一次性取出并删除条目
    pub fn cancel(&self, capture_id: &str) -> bool                               // 仅当 cropped 未生成时移除并返回 true
}

pub fn validate_selection_rect(rect: &ScreenshotRect, frame_width: u32, frame_height: u32) -> Result<NormalizedRect, AppCommandError>
pub fn crop_png(png_base64: &str, rect: &NormalizedRect) -> Result<CroppedPayload, AppCommandError>
```

- `ScreenshotFrameStore` 内部结构体 `ScreenshotCapture { request_id: String, hide_main: bool, frames: HashMap<u32, MonitorFramePayload>, cropped: Option<(CroppedPayload, ScreenshotCapturedMeta)> }` 保持私有。
- AppState 增加字段：`pub screenshot_frames: crate::commands::screenshot::ScreenshotFrameStore`（无条件编译，构造点用 `Default::default()`）。

- [ ] **Step 1: 写失败测试**（`screenshot.rs` 底部 `#[cfg(test)] mod tests`）：
  - `validate_selection_rect`：负坐标/越界被钳制到边界（x=-3.6 → 0；width 超出帧宽 → 截到边界）；四舍五入后宽或高 <1 → 报错（`AppCommandError` 用 `invalid_input` 类构造，若无此构造器选 app_error.rs 里最贴切的一个）；非有限值（NaN/inf）→ 报错
  - `crop_png`：`image::RgbImage::from_fn(100, 80, …)` 生成已知图案 → 编码 PNG → base64 → 裁 (5,5) 10×10 → 解码结果断言 `width==10 && height==10` 且首像素颜色与源 (5,5) 一致
  - store：`begin_capture` 两次 → 第一次的 capture_id 取不到帧（旧条目被清）；`store_cropped` 后 `cancel` 返回 false 且 `take_cropped` 仍能取出；未 `store_cropped` 时 `cancel` 返回 true
- [ ] **Step 2: 运行确认失败**：`cd src-tauri && cargo test screenshot`，期望编译错误（模块不存在）
- [ ] **Step 3: 实现**：Cargo.toml 加 `xcap = { version = "0.9", optional = true }`（`[dependencies]`），`tauri-runtime = [...]` 列表追加 `"dep:xcap"`；实现上述全部类型与函数（`crop_png` 用 `base64::engine::general_purpose::STANDARD` + `image::load_from_memory` + `crop_imm` + `write_to(Cursor, ImageFormat::Png)`；store 用 `std::sync::Mutex`，锁 poisoning 映射为 `AppCommandError`）；`mod.rs` 导出模块，`app_state.rs` 加字段，两个构造点补 `Default::default()`
- [ ] **Step 4: 运行确认通过**：`cargo test screenshot` PASS；再跑 `cargo check --no-default-features --features server-bin --bin codeg-server` 确认 server 模式未引入 xcap
- [ ] **Step 5: Commit**：`git add -A src-tauri && git commit -m "feat(screenshot): 帧仓库与选区裁剪纯函数（xcap 依赖）"`

### Task 2: Rust 命令、覆盖窗创建、注册与 ACL

**Files:**
- Modify: `src-tauri/src/commands/screenshot.rs`（追加命令，`#[cfg(feature = "tauri-runtime")]`）
- Modify: `src-tauri/src/lib.rs`（`generate_handler!` 注册，1910 行处）、`src-tauri/capabilities/default.json`
- Create: `src/app/screenshot-overlay/page.tsx`（**占位页**，Task 5 替换为真实 UI）

**Interfaces:**
- Consumes: Task 1 的 store/纯函数、`AppCommandError::window(...)` / `.with_detail(...)`（`commands/windows.rs` 同款）、`WebviewWindowBuilder` 模式（`commands/windows.rs:676-700`）
- Produces（Task 3/4/5 依赖的 invoke 契约）:

```rust
#[tauri::command]
pub async fn screenshot_begin_selection(app: AppHandle, state: State<'_, AppState>, request_id: String, hide_main: bool) -> Result<ScreenshotBeginOutcome, AppCommandError>
#[tauri::command]
pub async fn screenshot_get_frame(state: State<'_, AppState>, capture_id: String, monitor_index: u32) -> Result<MonitorFramePayload, AppCommandError>
#[tauri::command]
pub async fn screenshot_finish_selection(app: AppHandle, state: State<'_, AppState>, capture_id: String, monitor_index: u32, rect: ScreenshotRect) -> Result<(), AppCommandError>
#[tauri::command]
pub async fn screenshot_take_cropped(state: State<'_, AppState>, capture_id: String) -> Result<CroppedPayload, AppCommandError>
#[tauri::command]
pub async fn screenshot_cancel_selection(app: AppHandle, state: State<'_, AppState>, capture_id: String) -> Result<(), AppCommandError>

#[derive(Clone, Serialize)] #[serde(rename_all = "camelCase")]
pub struct ScreenshotBeginOutcome { pub capture_id: String, pub request_id: String, pub monitors: Vec<ScreenshotMonitorInfo> }
#[derive(Clone, Serialize)] #[serde(rename_all = "camelCase")]
pub struct ScreenshotMonitorInfo { pub index: u32, pub name: String, pub width: u32, pub height: u32 }
```

- [ ] **Step 1: 占位覆盖页**：`src/app/screenshot-overlay/page.tsx`，client 组件渲染固定文案（`"capturing…"`），Task 5 重写
- [ ] **Step 2: 实现 `screenshot_begin_selection`**：
  - `state.screenshot_frames.begin_capture(request_id, hide_main)` 拿 capture_id；先关闭残留覆盖窗（`app.webview_windows()` 过滤 `OVERLAY_WINDOW_PREFIX` 前缀逐个 close，忽略错误）
  - `hide_main` 为 true：`app.get_webview_window("main")` 执行 `hide()`；随后 `tokio::time::sleep(Duration::from_millis(300))`
  - 用 `tauri::async_runtime::spawn_blocking` 包裹 xcap 捕获：`xcap::Monitor::all()` 逐个 `capture_image()` → PNG → base64（失败时若已隐藏主窗必须先恢复再返回错误）
  - 帧插入 store；按 monitor 索引为每屏建窗（label `format!("{OVERLAY_WINDOW_PREFIX}{index}")`，url `format!("screenshot-overlay?captureId={capture_id}&monitorIndex={index}")`，builder 链 `.decorations(false).transparent(true).always_on_top(true).skip_taskbar(true).shadow(false).visible(false).resizable(false)`，build 后 `set_position(Position::Physical(PhysicalPosition::new(x, y)))` + `set_size(Size::Physical(PhysicalSize::new(w, h)))`（用 xcap monitor 的物理坐标与尺寸，负坐标显示器直接生效）+ `show()` + `set_focus()`；位置/尺寸设置失败忽略但不中断）
  - 返回 `ScreenshotBeginOutcome`（含各屏物理宽高，不含帧数据）
- [ ] **Step 3: 实现 `screenshot_get_frame` / `screenshot_finish_selection` / `screenshot_take_cropped` / `screenshot_cancel_selection`**：
  - get_frame：store 取帧，缺失 → `AppCommandError::not_found(...).with_detail(...)`（覆盖窗自行关闭）
  - finish：`validate_selection_rect` → `spawn_blocking(crop_png)` → `store_cropped` → 关闭全部覆盖窗 → `app.emit_to("main", "screenshot://captured", meta_json)`（payload 为 `{captureId, requestId, width, height, mime:"image/png"}`，camelCase JSON，**无字节**）→ 恢复主窗（`unminimize()` + `show()` + `set_focus()`，错误忽略）
  - take_cropped：一次性取出，缺失报 not_found
  - cancel：`cancel()` 返回 true 才关覆盖窗 + 恢复主窗；false（已完成）时直接返回 Ok
- [ ] **Step 4: 注册与 ACL**：`lib.rs` `generate_handler![…]` 追加 5 个命令（跟随现有 tauri-runtime 块，无需额外 cfg，若该列表本身在 cfg 内）；`capabilities/default.json` 的 `windows` 数组追加 `"screenshot-overlay-*"`
- [ ] **Step 5: 验证**：`cargo check` 与 `cargo check --no-default-features --features server-bin --bin codeg-server` 均通过；`cargo clippy --all-targets --features test-utils -- -D warnings` 无警告
- [ ] **Step 6: Commit**：`git add -A && git commit -m "feat(screenshot): 截图/选区/取图命令与覆盖窗管理"`

### Task 3: 前端类型、invoke 封装、标注纯函数库

**Files:**
- Create: `src/lib/screenshot/types.ts`、`src/lib/screenshot/annotation.ts`
- Modify: `src/lib/tauri.ts`（追加 5 个封装）
- Test: `src/lib/screenshot/annotation.test.ts`

**Interfaces:**
- Consumes: Task 2 的 invoke 契约（camelCase payload）、`MARKED_IMAGE_MAX_BYTES`（`@/lib/browser/screenshot-markup`）、`readFileBase64` 同款的 invoke 封装写法（`src/lib/tauri.ts:1216`）
- Produces（Task 4/5/6 依赖）:

```ts
// types.ts —— 与 Rust serde camelCase 一一对应
export interface ScreenshotRect { x: number; y: number; width: number; height: number }
export interface MonitorFramePayload { monitorIndex: number; pngBase64: string; width: number; height: number }
export interface CroppedPayload { pngBase64: string; width: number; height: number; mime: string }
export interface ScreenshotMonitorInfo { index: number; name: string; width: number; height: number }
export interface ScreenshotBeginOutcome { captureId: string; requestId: string; monitors: ScreenshotMonitorInfo[] }
export interface ScreenshotCapturedEvent { captureId: string; requestId: string; width: number; height: number; mime: string }

// tauri.ts
export async function screenshotBeginSelection(requestId: string, hideMain: boolean): Promise<ScreenshotBeginOutcome>
export async function screenshotGetFrame(captureId: string, monitorIndex: number): Promise<MonitorFramePayload>
export async function screenshotFinishSelection(captureId: string, monitorIndex: number, rect: ScreenshotRect): Promise<void>
export async function screenshotTakeCropped(captureId: string): Promise<CroppedPayload>
export async function screenshotCancelSelection(captureId: string): Promise<void>

// annotation.ts
export type AnnotationTool = "select" | "pen" | "rect" | "ellipse" | "arrow" | "text" | "mosaic" | "step"
export interface AnnotationPoint { x: number; y: number }
export type AnnotationMark =
  | { id: string; kind: "pen"; color: string; points: AnnotationPoint[] }
  | { id: string; kind: "rect" | "ellipse"; color: string; x: number; y: number; width: number; height: number }
  | { id: string; kind: "arrow"; color: string; from: AnnotationPoint; to: AnnotationPoint }
  | { id: string; kind: "text"; color: string; x: number; y: number; text: string }
  | { id: string; kind: "mosaic"; x: number; y: number; width: number; height: number }
  | { id: string; kind: "step"; color: string; x: number; y: number; seq: number }
export const ANNOTATION_COLORS = ["#ff3b30", "#ff9500", "#34c759", "#0a84ff", "#1d1d1f"] as const

export function shapeFromDrag(tool: "rect" | "ellipse" | "arrow", start: AnnotationPoint, end: AnnotationPoint, min: number): AnnotationMark | null
  // 语义同 browser/screenshot-markup.ts 的 markFromDrag：rect/ellipse 取规范化矩形，arrow 指向拖拽终点；任一边长 < min 返回 null
export function shiftMarks(marks: readonly AnnotationMark[], dx: number, dy: number): AnnotationMark[]
export function marksOutside(marks: readonly AnnotationMark[], size: { width: number; height: number }): AnnotationMark[]
  // 平移裁剪后完全落在画布外的标记（rect/ellipse/mosaic 按 x,y 判定，其余按锚点）
export function hitTestMark(mark: AnnotationMark, point: AnnotationPoint, slack: number): boolean
  // select 工具的命中测试：rect/ellipse/mosaic 在 x,y,w,h 外扩 slack 内；arrow/pen 任一线段距 point ≤ slack；text/step 徽标圆心距 ≤ badge+slack
export function annotationStyle(size: { width: number; height: number }): { line: number; halo: number; head: number; badge: number; font: number }
  // line/halo/head/badge 同 markupStyle 的 clamp 公式；font = clamp(max(w,h)/40, 16, 48)
export function mosaicBlockSize(size: { width: number; height: number }): number  // clamp(max(w,h)/60, 4, 24)
export function drawAnnotations(ctx: CanvasRenderingContext2D, image: CanvasImageSource, mosaicSource: CanvasImageSource | null, marks: readonly AnnotationMark[], size: { width: number; height: number }): void
  // 底图 → mosaic 标记（从 mosaicSource 取区域 drawImage）→ 三遍绘制（白晕/彩色/徽标与文字），风格沿用 screenshot-markup 的分层防切割策略
export async function exportAnnotated(image: CanvasImageSource, mosaicSource: CanvasImageSource | null, marks: readonly AnnotationMark[], size: { width: number; height: number }): Promise<Blob>
  // 离屏 canvas 按 image 原始尺寸绘制 → toBlob PNG；> MARKED_IMAGE_MAX_BYTES → JPEG q0.85（编码写法同 renderMarkedScreenshot）
```

- [ ] **Step 1: 写失败测试**（`annotation.test.ts`，纯几何不碰 canvas）：
  - `shapeFromDrag`：rect/ellipse 返回规范化矩形（负向拖拽同框）；arrow 指向终点；拖距 < min 返回 null
  - `shiftMarks`：各类 kind 坐标平移 dx/dy；`marksOutside`：平移后锚点完全出界的标记被剔除、部分出界的保留
  - `hitTestMark`：rect 命中内部与外扩 slack 边缘、远离时 miss；arrow 命中中段；step 徽标命中圆心附近
  - `annotationStyle`：小图 line≥2.5、大图 ≤10、font 在 16–48
  - `mosaicBlockSize`：4K 图 ≈ 24 上限、小图 4 下限
- [ ] **Step 2: 运行确认失败**：`pnpm test src/lib/screenshot`，期望 FAIL（模块不存在）
- [ ] **Step 3: 实现**：types.ts 全量；tauri.ts 封装（`invoke<T>("screenshot_begin_selection", { requestId, hideMain })` 形式，注释注明仅桌面调用）；annotation.ts（`drawAnnotations`/`exportAnnotated` 里 canvas 为 null 时抛错，行为对齐 renderMarkedScreenshot）
- [ ] **Step 4: 运行确认通过**：`pnpm test src/lib/screenshot` PASS；`pnpm lint .` 无新告警
- [ ] **Step 5: Commit**：`git add src/lib/screenshot src/lib/tauri.ts && git commit -m "feat(screenshot): 前端类型、invoke 封装与标注纯函数库"`

### Task 4: ComposerScreenshotButton 与 MessageInput 接线

**Files:**
- Create: `src/components/chat/composer/composer-screenshot-button.tsx`
- Modify: `src/components/chat/message-input.tsx:2293-2300`（`<ComposerAddMenu …/>` 之后插入一行）
- Test: `src/components/chat/composer/composer-screenshot-button.test.tsx`

**Interfaces:**
- Consumes: Task 3 全部导出、`subscribe`（`@/lib/platform`）、`isLocalDesktop()`、`toast`（sonner，`use-composer-attachments.ts:15` 同款）、DropdownMenu（`@/components/ui/dropdown-menu`，`composer-add-menu.tsx` 同款）、Dialog（`@/components/ui/dialog`，Task 6 组件）
- Produces:

```tsx
export function ComposerScreenshotButton(props: {
  disabled?: boolean
  onAttachImage: (file: File) => Promise<void>
}): ReactNode | null   // isLocalDesktop() 为 false 时渲染 null
```

- [ ] **Step 1: 写失败测试**（mock `@/lib/platform` 的 `isLocalDesktop`/`subscribe` 与 `@/lib/tauri` 的 5 个封装）：
  - 非 desktop → 返回 null（容器内无按钮）
  - desktop → 渲染按钮；点击第一项触发 `screenshotBeginSelection(requestId, false)`，第二项触发 `(requestId, true)`
  - `subscribe` 的 handler 收到匹配 requestId 的 `ScreenshotCapturedEvent` → 调 `screenshotTakeCropped` 并打开编辑器（编辑器先 mock 成探针组件）；requestId 不匹配 → 不调用
- [ ] **Step 2: 运行确认失败**：`pnpm test src/components/chat/composer/composer-screenshot-button`
- [ ] **Step 3: 实现**：
  - 门控：`if (!isLocalDesktop()) return null`（组件渲染期判断）
  - 按钮：`Button variant="ghost" size="icon-xs"` + `Camera`（lucide），`title/aria-label = t("screenshot")`，位于加号右侧
  - 悬停菜单：受控 `DropdownMenu`，`onMouseEnter` 打开（200ms 定时器防误触，卸载清理）；`side="top" align="start"`；两项：`t("screenshot")`（Camera）→ `begin(false)`，`t("screenshotHideWindow")`（Monitor）→ `begin(true)`；`disabled` 透传
  - 流程：点击项 → `setBusy(true)` → `requestId = crypto.randomUUID()` → `screenshotBeginSelection` → 关菜单；失败 `toast.error(t("failed"))` 并复位（错误文案含 `permission` / `权限` / `screen recording` 时改用 `t("permissionDenied")`，覆盖 macOS TCC 拒绝场景）
  - 事件：`useEffect` 内 `subscribe<ScreenshotCapturedEvent>("screenshot://captured", …)`，`requestId` 与最近一次发起一致才处理：`screenshotTakeCropped(payload.captureId)` → 存 payload → 打开编辑器；`takeCropped` 失败 toast
  - 编辑器确认：`onConfirm(file)` → `await onAttachImage(file)` → 关闭；取消仅关闭
  - 编辑器本体 Task 6 之前先用最小内联 Dialog 占位（本任务测试用探针断言打开/关闭，且探针 `onConfirm(mock File)` 后断言 `onAttachImage` 被调用；Task 6 替换 import）
- [ ] **Step 4: 接线 MessageInput**：`message-input.tsx` 在 `<ComposerAddMenu …/>`（2293–2300）后插入 `<ComposerScreenshotButton disabled={disabled} onAttachImage={(file) => attach.appendFilesFromInput([file])} />`
- [ ] **Step 5: 运行确认通过**：`pnpm test src/components/chat/composer/composer-screenshot-button` PASS；`pnpm test src/components/chat` 既有用例不回归
- [ ] **Step 6: Commit**：`git add -A src/components/chat && git commit -m "feat(screenshot): 输入框截图按钮与捕获流程接线"`

### Task 5: 覆盖窗页面与选区组件

**Files:**
- Rewrite: `src/app/screenshot-overlay/page.tsx`（替换 Task 2 占位）
- Create: `src/components/screenshot/region-select-overlay.tsx`、`src/lib/screenshot/overlay-geometry.ts`
- Test: `src/lib/screenshot/overlay-geometry.test.ts`

**Interfaces:**
- Consumes: Task 3 的 `screenshotGetFrame/screenshotFinishSelection/screenshotCancelSelection`、`MonitorFramePayload`；pet 页的 `Suspense + useSearchParams` 模式（`src/app/pet/page.tsx`）
- Produces:

```tsx
// src/lib/screenshot/overlay-geometry.ts
export interface CssRect { x: number; y: number; width: number; height: number }
export function cssRectToPhysical(rect: CssRect, viewport: { width: number; height: number }, frame: { width: number; height: number }): ScreenshotRect
  // scaleX = frame.width / viewport.width，逐值相乘后四舍五入
export function selectionTooSmall(rect: CssRect, minPx = 8): boolean

// src/components/screenshot/region-select-overlay.tsx
export function RegionSelectOverlay(props: {
  frame: MonitorFramePayload
  onConfirm: (rect: ScreenshotRect) => void
  onCancel: () => void
}): ReactNode
```

- [ ] **Step 1: 写失败测试**（`overlay-geometry.test.ts`）：
  - `cssRectToPhysical`：viewport 800×600、frame 1600×1200（scale 2）时各值 ×2 并取整；frame 2560×1440 / viewport 1280×720 同理；非整数结果四舍五入
  - `selectionTooSmall`：8px 以下 true；宽或高达 8px 的细长条 false（与 markup 的语义一致）
- [ ] **Step 2: 运行确认失败**：`pnpm test src/lib/screenshot/overlay-geometry`
- [ ] **Step 3: 实现 overlay-geometry**
- [ ] **Step 4: 实现 RegionSelectOverlay**：
  - `<img src={`data:image/png;base64,${frame.pngBase64}`} className="absolute inset-0 h-full w-full" />` 铺满（物理像素图自动缩放，不模糊选区基准）
  - 暗罩：选中区域外四块 `bg-black/40`（或 box-shadow 技法），选区边框 1px 白 + 尺寸角标 `${w}×${h}`（CSS px 即可）
  - 交互：`cursor-crosshair` 全屏拖拽；`pointerdown/move/up` 维护 `CssRect`；`Escape` → `onCancel`；`Enter`/双击 → 未 `selectionTooSmall` 时 `onConfirm(cssRectToPhysical(rect, window.innerWidth/innerHeight, frame))`
- [ ] **Step 5: 重写 page.tsx**：`Suspense` 包裹的 client 组件读 `useSearchParams()` 的 `captureId`/`monitorIndex` → `screenshotGetFrame` 载入帧 → 渲染 RegionSelectOverlay；`onConfirm` → `screenshotFinishSelection(captureId, monitorIndex, rect)`；`onCancel` → `screenshotCancelSelection(captureId)`；`GetFrame` 失败（陈旧窗）→ 静默调用 `getCurrentWindow().close()`（`@tauri-apps/api/window`，`core:window:allow-close` 已在 ACL）
- [ ] **Step 6: 运行确认通过**：`pnpm test src/lib/screenshot` PASS；`pnpm lint .`；`pnpm build`（静态导出含新路由）
- [ ] **Step 7: Commit**：`git add -A src/app/screenshot-overlay src/components/screenshot src/lib/screenshot && git commit -m "feat(screenshot): 全屏选区覆盖窗与坐标换算"`

### Task 6: 标注编辑器 Dialog

**Files:**
- Create: `src/components/screenshot/screenshot-editor-dialog.tsx`
- Modify: `src/components/chat/composer/composer-screenshot-button.tsx`（占位 Dialog 换成本组件）
- Test: `src/components/screenshot/screenshot-editor-dialog.test.tsx`

**Interfaces:**
- Consumes: Task 3 的 `annotation.ts` 全部、`CroppedPayload`；`Dialog`（`@/components/ui/dialog`，`message-schedule-dialog.tsx` 的 props 风格）；`Button`、lucide 图标、sonner toast
- Produces:

```tsx
export function ScreenshotEditorDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  payload: CroppedPayload | null
  onConfirm: (file: File) => void
}): ReactNode
```

- [ ] **Step 1: 写失败测试**（jsdom 无 2d context：`vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(fakeCtx)`，`toBlob` mock 出固定 Blob）：
  - 打开时渲染工具栏 8 个工具按钮（select/pen/rect/ellipse/arrow/text/mosaic/step）+ 撤销/重做/清空/裁剪应用
  - 画一个 rect 标记（pointer 事件序列）→ 撤销后画布标记数为 0、重做恢复为 1；清空后为 0 且重做不可用
  - select 工具下拖动已有 rect 标记 → 标记坐标平移（断言 marks 状态经重绘调用参数反映）
  - 确认按钮 → `onConfirm` 收到 `File`（type 为 `image/png`）
  - 预算兜底：`toBlob` mock 成先返回超大 PNG Blob（size > `MARKED_IMAGE_MAX_BYTES`）→ 断言二次以 `image/jpeg` 编码且最终 File type 为 `image/jpeg`（Review Focus #5）
  - `payload` 为 null 时不渲染画布
- [ ] **Step 2: 运行确认失败**：`pnpm test src/components/screenshot`
- [ ] **Step 3: 实现**：
  - 底图：`payload.pngBase64` → data URL → `HTMLImageElement`（onLoad 后记录原始尺寸）
  - 马赛克源：底图加载后按 `mosaicBlockSize` 生成像素化离屏 canvas（缩到 1/block 再放大）
  - 预览 canvas 尺寸 = 适配 Dialog 的缩放尺寸；指针坐标 ×(原图/预览) 换算到图像像素后调 `shapeFromDrag`（pen 按 move 采样累加 points，text 点击定位后弹出小输入框 Enter 提交，step 点击即落 `seq` 递增徽标，mosaic 拖拽出区域）
  - select/移动：select 工具下 `pointerdown` 自顶向下 `hitTestMark` 选中标记，拖动对该标记单独 `shiftMarks`（push 历史），其余工具忽略命中
  - 裁剪微调：进入 crop 模式拖出新矩形 → 应用 = 离屏 canvas 按矩形重切底图与马赛克源 + `shiftMarks(-x,-y)` + `marksOutside` 剔除 + 尺寸状态更新
  - 历史栈：每次标记变更前 push 当前 marks 到 undo 栈并清空 redo 栈；撤销/重做互换栈顶
  - 确认：`exportAnnotated(...)` → `new File([blob], "screenshot.png"|"screenshot.jpg", { type: blob.type })` → `onConfirm(file)`；失败 toast `t("failed")`
  - 工具栏文案全部走 i18n（Task 7 的 keys，本任务先把 en/zh-CN 两个文件补上，其余 8 个语言 Task 7 同步）
- [ ] **Step 4: 运行确认通过**：`pnpm test src/components/screenshot` PASS；`pnpm test` 全量不回归
- [ ] **Step 5: Commit**：`git add -A src/components/screenshot src/components/chat src/i18n && git commit -m "feat(screenshot): 标注编辑器（画笔/形状/文字/马赛克/序号/裁剪/撤销）"`

### Task 7: i18n 全量、用户文档与全量校验

**Files:**
- Modify: `src/i18n/messages/{ar,de,en,es,fr,ja,ko,pt,zh-CN,zh-TW}.json`（`Folder.chat.messageInput` 加 `screenshot`/`screenshotHideWindow`；新增 `Folder.chat.screenshotEditor` 组：`title, pen, rect, ellipse, arrow, text, mosaic, step, crop, applyCrop, cancelCrop, undo, redo, clear, color, confirm, cancel, textPlaceholder, failed, permissionDenied`）
- Create: `docs/screenshot-capture.md`（用户向说明 ≤40 行：按钮位置、两种模式、编辑器工具、平台限制）

**Interfaces:**
- Consumes: 前 6 个任务的全部产物
- Produces: 无（收尾）

- [ ] **Step 1: 补齐 8 个语言文件**（en/zh-CN 已在 Task 6 完成；其余 8 个按 en 结构直译，对照 `zh-TW.json` 既有 chat 组术语风格）
- [ ] **Step 2: 写 `docs/screenshot-capture.md`**（含 macOS 屏幕录制授权与 Linux Wayland 限制说明）
- [ ] **Step 3: 全量校验**：
  - `pnpm lint . && pnpm test && pnpm build`
  - `cd src-tauri && cargo test --features test-utils && cargo clippy --all-targets --features test-utils -- -D warnings && cargo check --no-default-features --features server-bin --bin codeg-server && cargo clippy --no-default-features --features server-bin --bin codeg-server --lib -- -D warnings && cargo check --no-default-features --features mcp-bin --bin codeg-mcp`
- [ ] **Step 4: 手工验收清单**（桌面跑 `pnpm tauri dev`，记录到 PR 描述）：
  - Windows 缩放 125% 与 150%：选区与成图像素对齐（Review Focus #1）
  - 双显示器含负坐标屏（主屏左侧的屏）：覆盖窗各就各位、任一屏确认生效（Review Focus #5）
  - 隐藏窗口截图：主窗隐藏 → 300ms → 选区 → 编辑 → 附加成功 → 窗口恢复
  - 4K 截图全屏导出：PNG 超预算时落 JPEG（Review Focus #5）
  - Esc 取消、连开两次截图、完成后旧覆盖窗无残留
- [ ] **Step 5: Commit**：`git add -A && git commit -m "feat(screenshot): i18n 全量与用户文档"`
