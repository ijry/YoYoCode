# macOS 多屏截图实施计划

**目标：** macOS 支持与 Windows 相同的多屏框选、悬停选窗、标注、复制、保存、钉图。当前 Windows 实现保持不变。

**范围：** 用户已要求实施 macOS 支持；沿用子代理驱动。Linux 暂不扩展，不更改发布流水线、不推送、不自动发布。

**架构：** 原生屏幕捕获只编入 computer-helper 的 macOS 实现。主程序每次截图使用独立、带现有身份校验的 helper 会话；不依赖 Computer Use 开关、agent grant 或 cua-driver 下载。图片经现有 16MiB 上限协议分块返回。此方案替代 screenshot-macos-linux-design.md 中尚未实施的 M1/L1 提议。

## 接口约定

在 computer/protocol.rs 增加：
- HelperOp::ComposerCaptureBegin { excluded_window_ids: Vec<u64> }
- HelperOp::ComposerCaptureChunk { monitor_index: u32, offset: u64 }
- ComposerDisplay { index: u32, display_id: u32, name: String, bounds: Rect, width: u32, height: u32, windows: Vec<Rect>, png_bytes: u64 }，Serialize/Deserialize camelCase。bounds 是 CoreGraphics 桌面逻辑点；windows 是该帧内像素坐标，保留前后顺序。
- ComposerCaptureManifest { displays: Vec<ComposerDisplay> }
- ComposerCaptureChunk { monitor_index: u32, offset: u64, data: String, eof: bool }，data 是不超过 1MiB 原始 PNG 字节的 base64。
- PROTOCOL_VERSION 14→15。

computer/composer_capture.rs 定义平台无关 ComposerCapturedMonitor { display: ComposerDisplay, png: Vec<u8> }、有界缓存/分块与验证辅助函数；最大 32 屏、每屏最多 64M 像素、总 PNG 最多 256MiB，超限返回明确错误，不静默降画质。

computer/local.rs 新入口：capture_composer_screens(excluded_window_ids: Vec<u64>) -> Result<Vec<ComposerCapturedMonitor>, BackendError>；request_composer_screen_permission() -> Result<(), BackendError>。使用现有 helper_to_run/launch/Connection；成功、失败及取消均回收独立会话。

helper/composer_capture_macos.rs：capture(&[u64]) -> Result<Vec<ComposerCapturedMonitor>, HelperError>，仅 cfg(all(target_os="macos", feature="computer-helper"))。macOS 14+ 动态加载 ScreenCaptureKit/SCScreenshotManager，旧系统才使用 CoreGraphics 兼容路径；现代接口报错不静默降级。屏幕录制只做 helper 侧 preflight；申请通过现有专用权限进程由用户明确触发。CG 元数据函数可复用，像素 API 不进入主程序。

主程序：screenshot_begin_selection 支持 Windows/macOS；窗口摆放 macOS 使用逻辑点，裁剪/候选仍用实际像素。新增 screenshot_request_permission，错误 permission_denied 与 dependency_missing 结构化返回。ScreenshotFrameStore 继续独立 managed state。

## 任务和验证

- [x] 1. 协议、缓存和 helper client：先测分块边界、乱序偏移、数据上限、serde 及失败清理，再实现；helper dispatch 保留已有鉴权。无需 Configure/driver。
- [x] 2. 原生 macOS：显示器列表、权限、图像编码、窗口候选；测试负原点、1×/2×比例、跨屏和分数边缘换算。原生调用需 Mac 构建与实机验收，不伪称 Windows 可验证。
- [x] 3. 主程序接线与前端授权：权限拒绝能恢复隐藏窗口；去授权仅请求 screen recording；重试创建新 helper。钉图位置按逻辑点/实际比例转换，负原点仍正确。
- [x] 4. WebKit 复制：点击时同步开始 clipboard.write，ClipboardItem 接收待合成 PNG 的 Promise；延迟合成、失败、取消都有测试。
- [ ] 5. 回归：Windows 上跑相关 Rust/TS 测试与前端 build；审核条件编译与 main 捕获符号边界。Mac 上另跑桌面/helper 双架构构建、nm 门禁和多屏/TCC/复制/钉图验收。

## 实机验收边界

当前主机为 Windows，无法产生已验证的 macOS DMG，也不能验证 TCC/Retina/多显示器的真实系统行为。交付时明确区分代码实现、自动测试通过、Mac 尚待验收；不自动触发 GitHub Actions。
