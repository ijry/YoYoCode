# Tailscale Funnel 验证记录

初版 Tailscale 集成已随 `5f70cc35c` 合并到本地 main。下方“通过”和“未通过的全仓检查”保留初版验证记录。

## 后续流程简化与 MCode 配置码

- MCode v2 配置码与实际 mcode 编码器逐字节一致，四种地址可通过实际解析器导入（仅用测试凭据）。新增编码/二维码/国际化定向测试 63 项通过，生产构建通过。
- MCode 改动后的完整前端测试：11,668 项通过，仍有下述任务页两组 mock 加载失败及源码换行断言失败，无新增失败。
- 流程简化新增可选 prerequisite 字段：区分安装、启动 Tailscale 服务、连接、登录、设备审批、DNS 和启动 Web 服务。
- 后端三个新回归均观察到 RED，修复后 Funnel 测试 18/18、Web 范围 177/177 通过。desktop/server-bin 编译检查通过，保留原有未使用变量/导入警告。
- 一键开启定向前端验证 86/86 通过（Web 服务组件 51、国际化 22、MCode 编码 13）；修改的三份 TSX 文件 ESLint 通过。最终 `pnpm build` 在所有交互修复完成后通过。
- 独立复核指出继承的后台授权不应自动打开；现已只对本次流程发起操作后的链接自动打开。顺序流程暂停普通状态轮询，并消耗自身启动 Web 后的刷新信号，避免旧状态覆盖成功地址。
- 本地模拟页面显示首次配置入口和前置提示；浏览器自动化两次点击定位超时，未记为端到端成功。账号授权、真实公网访问仍未实测；自动衔接、取消和失败回退由组件测试验证。
- 流程优化后完整前端测试：11,691 通过、1 个断言失败，另 2 个 suite 加载失败（784 文件通过/3 失败）；仍是下述任务页 mock 缺 getState 和源码 CRLF 匹配失败，无新增失败。最终组件/编码工具 lint 通过。

## 通过

- 前端：`pnpm build`，包括 TypeScript、41 个静态页面导出。自动恢复地址选择修复后重新构建通过。
- 定向前端：`pnpm exec vitest run src/components/settings/web-service-settings.test.tsx src/i18n/messages.test.ts src/components/i18n-provider.test.tsx --reporter=dot`：**48/48**（24 个组件测试、24 个国际化/provider 测试）。
- 修改组件的 ESLint：`web-service-settings.tsx`、`tailscale-funnel-settings.tsx`、`web-service-settings.test.tsx` 通过；十种语言 key/ICU 校验通过。
- Rust：`cargo test --no-default-features --features server-bin --lib web:: --quiet`：**174/174**，包括 15 个 Funnel/CLI/管理器测试及共享状态测试。审查修复后通过。
- Rust 编译：desktop 默认 feature、server-bin/codeg-server、mcp-bin/yoyocode-mcp 均通过，审查修复后重新检查通过。
- `git diff --check` 通过。
- 本地模拟浏览器检查：设置中入口位置正确，点击启用后选中公网 URL，下拉列表公网 URL 为第一项，本地/LAN 地址仍保留。使用的是实际组件和模拟 API，没有发布真实服务。最后追加 GUI 操作遇到浏览器定位超时，未将其记为成功；完整交互由组件测试覆盖。

## 未通过的全仓检查

以下失败涉及未改动模块或当前工作区环境，未为了本次功能改动这些模块。没有在干净 HEAD 的独立工作区重跑全部检查，因此记录实际现象及不在变更范围的事实，不声称完整基线已独立复现。

### 前端全量

`pnpm test --maxWorkers=2`：**11,647 通过、1 个断言失败，另 2 个 suite 加载失败**；783 个测试文件通过，3 个失败。

- `src/components/tasks/tasks-page.focus.test.tsx`
- `src/components/tasks/tasks-page.search.test.tsx`
  - 二者错误：`TypeError: useAppWorkspaceStore.getState is not a function`，加载 `src/stores/tab-store.ts:442` 时失败。
- `src/components/message/use-transcript-known-invocations.test.tsx`
  - `MessageListView > is told the list is unknown, not empty, until the agent advertises`：源码正则要求 LF，而被读取文件是 CRLF。

首轮默认并发还出现超时及翻译写入过程中的 key parity 失败；翻译完成后定向检查通过，降低并发复跑后超时项通过。最终新增恢复选择回归另以 48 项定向检查通过。

### Rust 全库

`cargo test --no-default-features --features server-bin --bin codeg-server --lib --quiet`：lib **6,195 通过、7 失败、5 忽略**。lib 失败后独立 binary 测试未继续，不记为通过。

失败名称：

- `acp::delegation::tests::a_codeg_mcp_tool_is_known_by_its_whole_name_only`
- `acp::question::tests::is_codeg_ask_tool_name_accepts_every_host_spelling_of_codegs_own_tool`
- `cli_run::output_schema::tests::the_submit_tool_is_known_by_both_halves_of_its_name`
- `parsers::codex::tests::a_deferred_script_completed_mcp_item_replaces_its_wrapper_card`
- `parsers::codex::tests::a_failed_semantic_mcp_item_settles_its_card_as_failed`
- `parsers::codex::tests::completed_mcp_items_split_a_two_call_one_chunk_script`
- `parsers::codex::tests::mixed_native_collaboration_and_semantic_delegation_keep_their_identities`

审查修复后重新运行 Web 范围 174 项通过，未重复整套未改动模块测试。

### 桌面运行测试

`cargo test --features test-utils --lib web:: --quiet` 编译完成，但测试进程启动失败：

```text
exit code: 0xc0000139, STATUS_ENTRYPOINT_NOT_FOUND
```

这次没有执行测试断言，具体缺失入口/依赖仍需在桌面运行环境排查。默认 feature 的 `cargo check` 通过。未继续运行桌面全库测试。

### Lint / Clippy

- `pnpm lint . --format json`：工作区大量 CRLF 导致 Prettier 错误；另有未修改的 `src/hooks/use-message-queue.ts:356,393` 的 `react-hooks/set-state-in-effect`。新增组件错误已修复并以定向 lint 验证。
- `src/lib/api.ts` 的 6 条已有格式错误位于消息队列方法约 3999–4029 行，新增 Funnel API 没有对应 lint 诊断。仅将此已修改文件换行归一为 HEAD 的 LF，未重排无关代码。
- server/mcp strict Clippy 失败：未使用 `ReferenceImage`、未使用 `mut`/`conn`（漫画模块），以及既有 `nonminimal_bool`/`collapsible_if`。
- desktop/all-targets strict Clippy 还涉及既有截图模块属性空行/参数数/unwrap，以及漫画测试 bool 断言、测试模块后的函数。
- Clippy 输出未指向新增 Funnel 模块；不把有失败的全仓 Clippy 报告为通过。

## 审查处理

独立代码审查发现并已修复：

1. 初次读取到 starting/stopped 时，自动恢复完成不选中公网 URL。保留初始选择意图，尊重此期间用户手动选择；添加 RED→GREEN 回归。
2. Web 不自动启动或绑定失败时，上次异常退出遗留映射未清理。桌面启动和独立服务器绑定前初始化并核对已拥有映射；添加 RED→GREEN 回归。

另外，清理去掉 `--yes`，防止检查后新增多个 handler 时直接跳过确认；查询与修改仍不是跨进程原子事务。操作期间不要用其他客户端同时修改同一 443 映射。没有扩展成新的 LocalAPI 管理架构。

## 尚未实测

- 真实 Tailscale 安装、首次账号和 HTTPS/Funnel 授权。
- 手机不安装 Tailscale、使用蜂窝网络访问并验证 WebSocket、终端和文件传输。
- 真实主机重启、休眠恢复及各运营商网络稳定性。
- macOS/Linux 实机 CLI 行为（当前运行平台为 Windows）。

本次未自动公开本机服务，也未将配置存在等同于真实公网连通性。
