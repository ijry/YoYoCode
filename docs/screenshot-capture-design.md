# 截图捕获与标注（Screenshot Capture & Annotation）设计

日期：2026-11-07
分支：`feat/screenshot-capture`
状态：待审批

## 背景与意图

用户希望在对话输入框中直接截取屏幕、标注后随消息发给智能体（例如把报错弹窗、UI 稿、日志截图发给 agent）。要求：

1. 截图按钮位于输入框"+"号（`ComposerAddMenu`）右侧；
2. 鼠标移过按钮展开两项：**截图** / **隐藏窗口截图**（隐藏 Codeg 主窗口后截取其背后的内容，完成后自动恢复窗口）；
3. 截图后进入编辑器，支持丰富标注，完成的图片作为图片附件进入现有发送链路。

## 目标 / 非目标 / 反目标

**目标**

- 桌面模式（tauri-runtime）下可用的全流程：按钮 → 选区 → 标注 → 附加到输入框
- 多显示器：每个显示器一个全屏覆盖选区窗，任一屏完成即取该屏选区
- 编辑工具：选择/移动、画笔、矩形、椭圆、箭头、文字、马赛克、序号步骤标、选区二次微调（裁剪）、撤销/重做、清空、颜色选择
- 10 种语言 i18n

**非目标**

- 网页/远程服务器模式不支持（Webview 里无法截取用户本地屏幕），按钮直接隐藏
- 任务输入框（`task-message-composer.tsx`，自建加号菜单）不加截图按钮；按钮覆盖所有共用 `MessageInput` 的输入框（会话详情面板、canvas 会话节点/抽屉、quick-actions）
- 不做窗口级截图（只截屏幕区域）、不做滚动截图、不做录屏
- 不做截图历史库、不做全局热键（后续可加）
- 不做"复制到剪贴板"独立出口（附件链路已覆盖复制/下载）

**反目标（要避免的失败）**

- 要求用户安装第三方外挂软件（Flameshot/ShareX/Snipaste）
- 选区期间屏幕内容"活着"（必须冻结帧，避免拖动选区时画面变化）
- 标注后无法撤销或导出图与预览不一致

## 约束

- 仅静态导出（`output: "export"`），覆盖窗必须是查询参数式平铺路由，不能用动态路由
- Rust 双模式编译：桌面 feature `tauri-runtime` 才编入截图代码；`server-bin` / `mcp-bin` 不得引入 xcap
- Tauri 2 ACL：`capabilities/default.json` 的 `windows` 需加覆盖窗标签 glob；主窗隐藏/恢复逻辑放在 Rust 命令内，避免给前端加 `core:window:allow-hide` 权限
- 前端 strict TS、Prettier 无分号风格、ESLint next 规则
- Windows 为主目标平台；macOS 需处理屏幕录制权限提示；Linux Wayland 屏幕捕获受限（xcap 标注 ⛔ 部分场景不可用），失败时给出可读错误

## 方案对比（如何集成）

| 方案 | 说明 | 结论 |
| --- | --- | --- |
| **A. xcap crate + 自绘覆盖窗 + 自有标注画布（选定）** | [xcap](https://github.com/nashaofu/xcap)（0.9.8，Rust，跨平台 Win/macOS/Linux X11，Tauri 生态截图应用主流底层）负责捕获；全屏透明置顶窗做选区；扩展现有 `screenshot-markup.ts` 画布做标注 | 选定。零外部软件依赖、UX 可控、仓库已有一半积木（画布标注先例 `browser-screenshot-markup.tsx`、附件链路、能力位/命令模式） |
| B. tauri-plugin-screenshots 插件 | 薄封装 xcap，仅省一行依赖 | 落选：选区/编辑 UI 反正要自建，多一层抽象无收益 |
| C. screenshots-rs（pot-app） | 自带覆盖层 UI | 落选：覆盖层与我们的 Tauri 窗口体系/附件链路难整合，维护活跃度弱于 xcap |
| D. 外挂 Flameshot/ShareX/Snipaste | shell out 到第三方工具 | 反目标：要求用户另装软件，跨平台打包与体验割裂 |

## 架构设计

### 数据流

```
[输入框] 截图按钮(悬停菜单) → 生成 requestId → invoke screenshot_begin_selection(requestId, hide_main)
  Rust: (可选)隐藏主窗 → 等 300ms → xcap 捕获所有显示器(物理像素 PNG)
        → 帧存入 AppState 内存帧仓库 → 每显示器创建覆盖窗 screenshot-overlay-<n>
  [覆盖窗] 加载 /screenshot-overlay?captureId=..&monitorId=.. → invoke screenshot_get_frame
        → 全屏显示冻结帧 + 暗罩 + 拖拽选区（W×H 提示，Esc 取消，Enter/双击确认）
        → invoke screenshot_finish_selection(capture_id, monitor_id, rect 物理像素)
  Rust: capture_region 裁剪 → PNG base64 存帧仓库 → emit_to("main", "screenshot://captured",
        {captureId, requestId, width, height})  ← 事件只带元数据，不带图片字节
        → 关闭所有覆盖窗 → 恢复主窗
  [主窗] 各输入框实例监听事件、按自身 requestId 过滤（多实例不重复触发）
        → 发起方打开编辑器 Dialog → invoke screenshot_take_cropped(captureId) 拉取 PNG
        → 拉取后 Rust 释放该帧仓库条目
  [编辑器] 标注完成 → 编码（预算见下）→ File → appendFilesFromInput([file])
```

### Rust 侧（新文件 `src-tauri/src/commands/screenshot.rs`）

- 依赖：`xcap = { version = "0.9", optional = true }`，挂进 `tauri-runtime` feature（跟随现有 `dep:` 写法）；`image`/`base64` 已有
- 状态：`AppState` 新增 `screenshot_frames: Mutex<HashMap<String, ScreenshotCapture>>`（capture_id → 各显示器冻结帧与元数据），会话结束/取消即清理
- 命令（`#[tauri::command]`，仅 tauri-runtime 编译，模式跟随 `commands/computer.rs`）：
  - `screenshot_begin_selection(request_id, hide_main: bool) -> Result<ScreenshotCaptureInfo>`：创建 capture_id；hide_main 为 true 时 `main.hide()` → sleep 300ms → 捕获；为每个 `Monitor::all()` 建覆盖窗（`WebviewWindowBuilder`，transparent、always_on_top、decor(false)、skip_taskbar、全屏定位到该显示器物理边界）；沿用 `commands/windows.rs` 的建窗模式
  - `screenshot_get_frame(capture_id, monitor_id) -> FramePayload`：覆盖窗前端取冻结帧（base64 PNG + 物理宽高 + scale_factor）
  - `screenshot_finish_selection(capture_id, monitor_id, rect: ScreenshotRect)`：用 `monitor.capture_region()` 或内存帧裁剪（物理像素），PNG 存入帧仓库，`emit_to("main", "screenshot://captured", {captureId, requestId, width, height})`——事件只带元数据（Tauri 2 事件经 eval 注入，大 payload 会阻塞 UI 数秒）；关闭覆盖窗，`main.show()` + focus
  - `screenshot_take_cropped(capture_id) -> CroppedPayload`：编辑器拉取裁剪图（base64 PNG + 物理宽高），成功即从帧仓库释放该条目
  - `screenshot_cancel_selection(capture_id)`：清帧仓库、关覆盖窗、恢复主窗
- 裁剪/缩放纯函数（入参校验、坐标换算）拆出可测模块，风格同 `browser/capture.rs`
- 不走 `codeg-computer-helper` sidecar：截图是核心聊天功能，不与可选的计算机使用特性耦合；Windows 无权限问题，macOS 由系统弹 TCC 授权

### 前端

- **按钮**：新组件 `src/components/chat/composer/composer-screenshot-button.tsx`，渲染在 `message-input.tsx` 动作行 `<ComposerAddMenu>` 右侧（约 2293 行处）——即所有共用 `MessageInput` 的输入框自动获得；lucide `Camera` 图标，`ghost icon-xs` 与加号同规格；`mouseenter`/click 打开 `DropdownMenu`（side="top"），两项：截图、隐藏窗口截图；仅 `isLocalDesktop()` 时渲染（模式同现有桌面判定）
- **覆盖窗路由**：新页面 `src/app/screenshot-overlay/page.tsx`（查询参数接收 captureId/monitorId），显示冻结帧铺满全窗、暗罩、拖拽矩形、尺寸角标、边缘吸附提示；组件放 `src/components/screenshot/region-select-overlay.tsx`
- **DPI 换算**：覆盖窗内选区用 CSS 逻辑坐标，提交前 `× devicePixelRatio` 换算为物理像素；冻结帧按物理像素铺满，避免模糊
- **编辑器**：新组件 `src/components/screenshot/screenshot-editor-dialog.tsx`（Radix Dialog，模式同 `message-schedule-dialog.tsx`）；核心画布逻辑扩展 `src/lib/screenshot/annotation.ts`（从 `browser/screenshot-markup.ts` 泛化）：
  - 标记模型：`Mark = { id, type: 'pen'|'rect'|'ellipse'|'arrow'|'text'|'mosaic'|'step', color, points/rect, text?, seq? }`，存原图坐标系；渲染时统一 `drawMarks`，导出=离屏 canvas 合成
  - 马赛克：对原图预生成一张像素化画布，标记渲染时从像素化画布取区域，天然可撤销
  - 序号标：自动递增计数，红底白字圆形徽标
  - 裁剪微调：拖出新矩形 → 应用后基图与全部标记坐标平移 `-x,-y`
  - 撤销/重做：标记数组快照栈；清空即重置
  - 颜色：预设 5 色小面板（默认 `#ff3b30`）
- **附加**：编辑完成 → 编码 → `File` → `appendFilesFromInput([file])`（进入现有缩略图条/base64 prompt block 链路，含 `caps.image`/`embedded_context` 分派）；Dialog 在主窗内，无需跨窗事件附加

### 已定案的实现决策（builder 评审后）

1. **导出编码预算**：沿用仓库既有约定（同 `src/lib/browser/screenshot-markup.ts` 的 marked-screenshot 预算：限宽缩放、PNG 优先、超限时 JPEG q85 兜底），复用其编码辅助函数；不按 20MB 附件上限发原图——截图是发给视觉模型的，大图只浪费上下文
2. **大图不走事件**：`screenshot://captured` 事件只带 `{captureId, requestId, width, height}` 元数据，编辑器用 `screenshot_take_cropped` invoke 拉图（Tauri 2 事件经 eval 注入 webview，10–30MB payload 会阻塞 UI 数秒）
3. **多输入框实例寻址**：发起方输入框生成 `requestId` 传入 `screenshot_begin_selection`，事件原样带回；每个实例只响应自己的 requestId，避免 canvas/quick-actions 等多处 `MessageInput` 同时挂载时重复触发
4. **按钮范围**：所有共用 `MessageInput` 的输入框；任务输入框（`task-message-composer.tsx`）不加（用户已确认）
- **i18n**：`Folder.chat.messageInput` 下加截图按钮/菜单 keys，`Folder.screenshot*` 新组（编辑器工具、尺寸提示、错误文案）；10 个语言文件同步

### 权限与窗口

- `capabilities/default.json`：`windows` 增加 `screenshot-overlay-*`；覆盖窗前端仅用 core 默认权限 + 自定义命令（跟随现有主窗权限集）
- 覆盖窗标签 `screenshot-overlay-<monitor_index>`，Rust 侧统一创建/销毁

## 测试策略

- Rust：坐标校验/裁剪换算/帧仓库增删的纯函数单测（`cargo test --features test-utils`）；xcap 实际捕获不进 CI（命令保持薄）
- 前端：`annotation.ts` 标记几何/马赛克/裁剪平移/撤销栈 vitest 单测；按钮桌面/网页模式渲染门控测试；覆盖窗坐标换算测试；编辑器交互浅测（模式同 `message-input.test.tsx`）
- 手工验收：Windows 双屏 + 缩放 125%/150% 场景走通全流程

## 风险与备注

- **macOS TCC**：首次截图系统弹"屏幕录制"授权；拒绝时 xcap 返回错误/黑帧 → 命令返回可读错误，前端 toast 引导到系统设置
- **Linux Wayland**：捕获可能失败 → 错误提示降级，不阻塞主流程
- **覆盖窗加载延迟**：覆盖窗加载完整 Next 静态包，首开可能慢 → 冻结帧由 Rust 先捕获（时点在覆盖窗创建前），选区所见即按下时画面；后续可预建常驻隐藏窗优化（不在本期）
- **大图内存**：4K 屏 PNG base64 约 10–30MB，帧仓库仅存最近一次 capture，确认/取消即释放
- **隐藏窗口截图时序**：300ms 等待可配；Windows 下 `hide()` 后 DWM 合成即生效，无需更长等待

## 交付物清单

1. `src-tauri/Cargo.toml` + `src-tauri/src/commands/screenshot.rs`（含纯函数模块与单测）+ `lib.rs` 注册 + `app_state.rs` 帧仓库
2. `capabilities/default.json` glob
3. `src/app/screenshot-overlay/page.tsx` + `region-select-overlay.tsx`
4. `composer-screenshot-button.tsx` + `message-input.tsx` 接线
5. `screenshot-editor-dialog.tsx` + `lib/screenshot/annotation.ts`（含测试）
6. i18n × 10
7. 文档：`docs/screenshot-capture.md`（用户向说明，简短）