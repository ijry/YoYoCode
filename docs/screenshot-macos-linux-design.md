# 截图功能 macOS/Linux 支持（helper 捕获通道 + xcap 0.4.1）设计

日期：2026-11-08
前置：`docs/screenshot-capture-design.md`（Windows 版已发布于 v0.42.0-plus.3）
状态：历史提议，已由 `docs/screenshot-macos-plan.md` 替代。下文 M1（主屏 driver）和 L1（Linux 降级 xcap）未实施，不能当作当前能力说明。用户随后要求直接实现 macOS，多屏捕获改走独立 helper 的 ScreenCaptureKit/兼容通道；Linux 继续暂不支持。

## 背景与意图

v0.42.0-plus.3 发布的截图功能仅 Windows 可用：macOS 撞仓库安全门禁（codeg 本体禁止引用屏幕捕获 API，只有 helper sidecar 可持有），Linux 撞 xcap 0.9 的 pipewire 头版本要求（Ubuntu 22.04 CI 编不过）。本设计在不破坏门禁与 CI 的前提下把支持面扩到 macOS（主显示器）与 Linux（X11），并加能力位让前端按平台真实状态渲染。

## 目标 / 非目标

**目标**

- macOS：主显示器截图全流程（选区覆盖窗、标注编辑器、附件链路全部复用）
- Linux：X11 会话下多显示器截图全流程
- `screenshot_capabilities` 能力位命令；不支持平台隐藏按钮（替代"点了才报错"）
- composer 截图与 Computer Use 开关解耦（用户主动行为不被 agent 授权开关挡住）

**非目标**

- macOS 多显示器（M2：helper 内逐屏 CG/SCK 捕获，按需另立项）
- Linux Wayland（GNOME/KDE portal 流；X11-only 明示）
- 不改动 ComputerService 的授权语义（agent 保护机制原样保留）

## 方案对比

| | 说明 | 结论 |
| --- | --- | --- |
| **M1：helper 复用 driver 主屏捕获（选定）** | 新增 helper op，走现有 `get_desktop_state` 工具链 | helper 不引入新捕获面、门禁天然合规、~300 行 |
| M2：helper 内自写逐屏 CG/SCK | 多显示器对齐 Windows | 新增像素捕获面 + TCC/缩放细节，延后 |
| **L1：xcap 0.4.1 全平台（选定）** | Linux 走纯 Rust xcb/x11rb，X11 可用 | 一个 crate 版本无法按 target 分叉，Windows 捕获随之回 0.4.1 实现——需本机回归验证 |
| L2：24.04 第二产物轨道 | Wayland 全支持 | glibc 分叉 + 双倍产物矩阵，比例失衡，延后 |
| 捕获要求开启 Computer Use | 复用 LocalBackend 现有连接门 | 语义错误：用户截图不该被 agent 授权开关挡住 → 走专用会话路径 |

## 架构设计

### macOS：helper 捕获通道（M1）

- **协议**（`computer/protocol.rs`）：新增 `HelperOp::CaptureComposerScreen { max_dimension }` → 回复 `RawCapture` 同款结构（png_base64/width/height/native_*）；`PROTOCOL_VERSION` 14→15（helper 与 codeg 同包发布，无跨版本兼容负担）
- **helper 侧**（`computer/helper/mod.rs` + `screen.rs`）：新 op 直接调 driver `get_desktop_state`（与现有 `screen::capture` 同工具链），但**跳过 ScreenRules/窗口过滤**（用户主动捕获整屏，无 agent 保护语义）；保留 `OsPermission::ScreenRecording` 探测 → 无权限返回结构化 `PermissionMissing`
- **codeg 侧会话**（`computer/` 新增轻量入口，`commands/screenshot.rs` 调用）：
  - 专用轻量 helper 会话：复用 `protocol.rs` 帧读写与 spawn 逻辑；若 ComputerService 的 helper 已在运行则复用其连接（op 与授权 op 正交），否则按需拉起
  - **不经过** `ComputerService::capture_screen` 的 turn 锁/票据/blocklist（那是 agent 保护）
  - `LocalBackend::connection()` 的"Computer Use 需开启"门不适用于此路径
- **平台分派**（`commands/screenshot.rs::screenshot_begin_selection`）：Windows 保持 xcap；macOS 走 helper 捕获 → 同一 `ScreenshotFrameStore` + 单个覆盖窗（主显示器物理边界）；帧 >16MiB 协议上限时按 driver 现有缩径逻辑收缩
- **TCC**：首用无权限时返回 `PermissionMissing`，前端 toast 引导到现有 `computer_request_permission` 授权 UI（prompt 由专用 `--request-permission` 进程发起，归属 helper，门禁合规）

### Linux：xcap 0.4.1（L1）

- `Cargo.toml`：`xcap = { version = "0.4", optional = true }`（windows-only target 门改为 windows+linux；macOS 仍排除）
- API 兼容已核对（docs.rs 0.4.1）：`Monitor::all/name/x/y/width/height/capture_image` 与现用法一一对应，返回类型一致
- **Windows 回归验证**：0.4.1 Windows 后端为 GDI 路径，需在本机实跑一次捕获回归（小 cargo test 抓屏断言非空+尺寸>0）后才能进 CI
- `release.yml` 两个 Linux apt 步骤加 `libxcb1-dev libxcb-randr0-dev`（xcb 1.5 randr 所需；dbus 已被 webkit2gtk-dev 拉齐——plus.2 日志证实 libdbus-sys 编译通过）
- **回滚预案**：若 xcb 构建在 CI 失败，Linux 部分单独回退到「暂不支持」，不阻塞 macOS 上车（两平台改动文件不相交）

### 能力位（双平台通用）

- `screenshot_capabilities()` 命令（`message_queue_capabilities` 同款）：返回 `{ capture: bool, reason?: string }`——Windows/macOS/Linux 各按编译期+运行期事实回答
- 前端 `composer-screenshot-button.tsx`：能力查询失败或 `capture:false` → 不渲染按钮（替代现状的"点击后 toast 报错"）

## 测试策略

- 协议 op 编解码单测；helper handle_op 分支单测（mock driver）
- `screenshot_capabilities` 门控测试；Windows 本机 xcap 0.4.1 实捕获回归
- CI 即最终验证（macOS helper 编译、Linux xcb 构建）；xcb 失败走回滚预案
- 手工验收：macOS 单屏全流程 + TCC 首次授权流；Linux X11 双屏；Windows 无回归

## 评审定案（builder check 后，均为实现决策）

1. **能力位语义**：`capture` 反映"点击后能否完成"的编译期+运行期复合事实——Windows/macOS 恒 `true`（macOS 首次点击的 driver 下载是延迟而非不可用，菜单项有现成 busy 态展示下载过程）；Linux 按 `XDG_SESSION_TYPE` 运行期判定，Wayland 会话 → `capture:false, reason:"wayland"` → 按钮隐藏（不重新引入"点击才报错"）。
2. **TCC 恢复不依赖 Computer Use 开关**：composer 报 `PermissionMissing` 时，前端 toast 附「去授权」动作，直接调新建的轻量命令触发 `codeg-computer-helper --request-permission screen-recording`（该机制本就不读开关，`LocalBackend::request_permission` 的开关门只挡 agent 路径）。i18n 文案按此写。
3. **会话架构：总是专用会话，不复用 ComputerService 连接**。复用不可行——helper 会 `fetch_max` 停止计数并拒绝 `stop < stopped` 的请求，composer 会话发 `stop:0` 在用户第一次于 Computer 面板点停止后即永久 `Stopped`。专用会话 stop 恒 0，代价是短生命周期双进程（驱动并发安全，空闲超时回收）。
4. **新 op 保留 `state.require(ScreenRecording)` 前置检查**：首用失败必须返回结构化 `PermissionMissing`（整个首次 UX 依赖它），而不是 driver 的泛化错误。

## 风险与备注

- xcb crate 构建慢/挑剔是 L1 最大未知——已备回滚预案
- ad-hoc 签名下 helper 的 TCC 授权随每次重签名重置（release.yml 已记录的同款限制），macOS 用户升级后需重新授权一次
- 16MiB 帧上限：超限收缩已在 driver 链路存在，复用即可
- helper 会话生命周期：composer 会话空闲超时自动回收，避免常驻进程

## 交付物清单

1. `computer/protocol.rs`（op + 版本）、`computer/helper/{mod,screen}.rs`（op 分支）、`computer/` 轻量会话入口
2. `commands/screenshot.rs`（平台分派 + capabilities 命令）、`lib.rs` 注册
3. `Cargo.toml`（xcap 0.4.1 target 门）、`release.yml`（apt 包）
4. `composer-screenshot-button.tsx` 能力位接线、`lib/screenshot/types.ts`
5. i18n（macOS 权限引导文案）、`docs/screenshot-capture.md` 平台说明更新
