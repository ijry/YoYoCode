# Tailscale Funnel 实现记录

用户已批准会话中的实施方案（2026-11-09）。目标：设置 → Web 服务接入已安装的 Tailscale CLI；手机免客户端公网访问；确认成功后公网 URL 排首位并默认选中。

## 范围

- Rust：有超时/取消/输出上限的 CLI 调用、登录与 Funnel 授权、配置核验、仅清理自己拥有的映射、偏好持久化、启停恢复。
- 桌面与 HTTP：共享 WebServerState 与核心函数；保留 Codeg token/WS 子协议认证。
- 前端：状态/安装/授权/启停/刷新；去重地址列表、公网地址优先、失败回退；十种语言。
- 验证：模拟 CLI 的 Rust 测试、React/Vitest 测试、lint/build、Rust feature 检查和独立最终审查。
- 不自动公开本机服务；不提交或推送。

## 执行记录

- 已完成现有代码探索。Web 服务缺省端口为 18989；当前桌面 HTTP router 持有占位 WebServerState，需共享真实状态。
- 已创建工作分支 `feat/tailscale-funnel`，保留原有未跟踪的 `.zcode/` 和调研文档。
- 实施方式：主会话负责共享状态、生命周期和适配层；独立代理在互不重叠的文件内实现 CLI 管理器和前端。Ruling：采用并行实现以减少等待，各任务共享明确 DTO，最后统一编译和审查；代价是必须复核边界一致性。
- 前端初始 4 个行为测试已观察 RED：公网 URL 优先、启停回退、旧后端失败、授权链接隔离。后端 4 个解析测试中 3 个断言 RED，随后补实现；共享状态测试发现当前状态不能克隆。
- 已连接共享 WebServerState、启动恢复钩子、停止清理钩子、Tauri/HTTP adapters；HTTP 认证及 WS 子协议认证保持原行为。
- 官方 CLI 合约：up 用于当前账号登录（不使用会切换 profile 的 login）；Funnel 退出 0 仍需核验配置；停用使用 serve --https=443 --yes off，避免权限不足时 funnel off 等待授权。
- 前端完成：24 个组件行为测试和 24 个 i18n/provider 测试通过；三份组件文件 ESLint 通过；生产静态构建在审查修复后通过。
- 后端初轮 14 个 Funnel 测试通过；桌面、server-bin、mcp-bin（实际二进制为 yoyocode-mcp）cargo check 通过。
- 独立审查发现两项：自动恢复后公网地址未默认选中（已修复，3 个 RED 回归转 GREEN）；Web 不自动启动时遗漏旧映射清理（新增 RED，正在验证修复）。
- Ruling：CLI 的独立读取/写入不能对其他进程提供跨调用事务锁；继续使用已批准的 CLI 方案，识别冲突时拒绝修改，清理去掉 --yes 防止误删新增多 handler 配置。代价是外部进程恰在检查和 CLI 启动之间替换同一映射仍有竞争窗口，文档注明避免并行管理。
- 全仓检查：前端 11647 通过/1 失败，另 2 个任务页 suite 加载失败；源码正则 LF 与工作区 CRLF 不匹配，任务页 mock 缺 getState，均不在变更范围。Rust 全库 6195 通过/7 失败/5 忽略，失败为旧 MCP 命名与 Codex 解析用例。严格 Clippy 发现 9 个未改模块既有问题；全仓 ESLint 有大量 CRLF/Prettier 和 2 个既有 use-message-queue hook 错误。
- 审查修复后 server-bin Web 回归 174/174，通过 desktop/server/mcp 的最终 cargo check。前端修复后 build 再次通过。桌面 Web 测试编译后启动失败 STATUS_ENTRYPOINT_NOT_FOUND，未执行断言；未重复桌面全库。
- 实现及文档收尾完成。详细检查与全部失败名称见 [tailscale-funnel-verification.md](tailscale-funnel-verification.md)。未真实发布隧道、未实测手机公网访问、未提交/推送。
