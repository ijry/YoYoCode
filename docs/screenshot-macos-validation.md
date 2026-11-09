# macOS 截图：构建与实机验收

## 状态与范围

macOS 的多显示器截图、悬停选窗、就地标注、剪贴板复制、PNG 保存和桌面钉图已通过合并提交 `2ad79c2f3` 进入 `main`。原 `feat/screenshot-macos` 分支和工作树已清理；这些改动尚未发布。完整开发交接见 [screenshot-development-handoff.md](screenshot-development-handoff.md)。

当前开发主机为 Windows。平台无关的协议、边界换算和前端测试可以在这里运行；原生捕获、TCC、窗口层级及 DMG 必须在 Mac 上构建和验收。Apple 目标的局部类型检查不能替代完整原生构建。

- macOS 14+：helper 动态加载系统 ScreenCaptureKit，使用 SCScreenshotManager。
- 较旧 macOS：helper 使用 CoreGraphics 兼容路径；具体系统版本需验证。
- 不要求开启 Computer Use，不下载 cua-driver；不增加智能体 MCP 工具。
- 屏幕录制权限属于 `codeg-computer-helper`。主程序及 CLI 不应链接捕获 API。
- 受保护内容可能无法捕获，HDR 会归一化为 8 位 sRGB；各显示器按序冻结，并非原子桌面快照。

## 已执行的验证（当前 Windows 开发机）

- 前端相关回归：38 个文件、617 项通过；完整 TypeScript 检查通过。
- 静态前端构建：41 个路由成功导出。隔离工作目录复用依赖时，临时扩大了 Turbopack 根目录，完成后恢复原配置，没有将环境配置纳入提交。
- Rust server-bin 的平台无关测试：`composer_capture` 17 项、`composer_helper` 3 项、`computer::protocol` 7 项、`commands::screenshot` 37 项全部通过；过滤器之间有重叠，不相加统计。
- Windows 桌面 `cargo check --bin codeg` 通过；只做编译检查，未在这个 macOS 工作目录重打 Windows 安装包。
- Apple Silicon 局部类型检查：实际的原生捕获、ScreenCaptureKit 和几何模块通过 `cargo check --target aarch64-apple-darwin`。临时检查工程使用少量 DTO/外部环境桩；生产 Foundation feature 图另经核对。因此这只证明这些真实源文件在 Apple cfg 下的 Rust 类型与 Objective-C 宏声明可编译，不证明完整 App 链接、TCC、运行时截图或真实 IPC。
- 独立静态审查：helper 身份校验沿用、分块边界、进程回收及原生回调所有权未发现剩余阻塞项。
- **尚未执行**：Mac 完整 desktop/helper 构建、DMG、发布符号门禁及下面的实机交互清单。未触发发布 CI。

## 在 Mac 上准备与构建

需要 Xcode Command Line Tools、项目要求的 Node/pnpm、Rust，以及本分支代码。命令均从仓库根目录执行；无需 GitHub 发布权限或签名私钥。

```bash
pnpm install --frozen-lockfile
pnpm build
# 先单独完成配套程序编译，避免 tauri dev 等待前端超时。
pnpm tauri:prepare-sidecars
pnpm tauri dev
```

开发实例运行时不要同时对同一 target 目录进行另一个 Tauri build。测试首次权限时先退出其他 Codeg/YoYoCode 实例，以免单实例逻辑复用旧进程。

只生成本机 DMG，可在临时目录写配置并运行：

```bash
local_config="$(mktemp -t yoyocode-screenshot).json"
cat > "$local_config" <<'JSON'
{
  "productName": "YoYoCode",
  "identifier": "net.lingyun.yoyocode",
  "bundle": {
    "createUpdaterArtifacts": false,
    "macOS": { "signingIdentity": "-" }
  },
  "plugins": { "updater": { "endpoints": [] } }
}
JSON
pnpm tauri build --bundles dmg --config "$local_config"
```

这是 ad-hoc 本机验证包，不做公证、不发布，并关闭在线更新源（检查更新可能提示未配置来源），避免测试包更新到官方版本。不要将此本机配置用于正式发布；正式构建仍应走项目的 PLUS 身份注入、更新源、公钥及签名流程。

## 自动检查

在 Mac 的 `src-tauri/` 下：

```bash
cargo check
cargo check --no-default-features --features computer-helper --bin codeg-computer-helper
cargo test --no-default-features --features server-bin --lib composer_capture
cargo test --no-default-features --features server-bin --lib composer_helper
cargo test --no-default-features --features server-bin --lib computer::protocol
cargo test --features test-utils --lib commands::screenshot
```

正式包还需执行 `.github/workflows/release.yml` 中 macOS 结构与符号检查：主程序、`yoyocode-mcp`、`yoyo-cli` 不能引入 CG 截图、AX 操作或 ScreenCaptureKit 捕获符号；helper 的 app 包必须在 `Contents/Helpers`。不要为了让检查通过而放宽主程序的捕获符号限制。

## 实机验收

1. **首次授权**：关闭 Computer Use 后点截图。未授权时得到权限提示，点“去授权”后系统提示归属 helper。拒绝时主窗口恢复；授权后重新点截图可用。申请授权不等于已获准，不自动重试抓屏。
2. **新旧系统路径**：至少 macOS 14/15+ 验证 ScreenCaptureKit。若宣称支持旧系统，另验证兼容路径；现代接口失败不得静默退回旧 API。
3. **图像朝向/颜色**：用上红下蓝、左右文字不同的画面验证无上下翻转、偏色或黑边；选区输出尺寸与原生像素一致。
4. **多屏/Retina**：1×外接屏和 Retina 同时使用，分别放在主屏左侧、右侧和上方。覆盖窗铺满对应屏幕，窗口边缘高亮和实际画面一致；跨屏窗口候选只覆盖当前显示器部分。
5. **交互**：悬停只预览，单击选整窗，按住拖动自由框选；选区可移动、缩放和标注。
6. **复制/保存/钉图**：复制后粘贴到预览或办公应用，不自动进 AI 会话；保存 PNG 画面正确；钉图位置/尺寸正确、可拖动且关闭后删除临时文件。
7. **退出与资源**：Esc、关闭覆盖窗、连续开始截图、helper 异常退出和权限撤销后均恢复窗口；没有残留截图 helper 或影响智能体既有 Computer Use 会话。
8. **大图**：高分辨率多屏 PNG 分块传输无 16MiB 帧超限；32 显示器、单屏 64M 像素、总 PNG 256MiB 为明确上限，超过时报错而非静默缩图。

ad-hoc 版本更换可能使系统要求重新授权；是否保留授权须以目标 Mac 的实际签名和 TCC 行为为准。
