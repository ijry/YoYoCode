# 内网穿透集成调研

初次调研：2026-11-07。需求及 CLI 合约复核：2026-11-09。

## 目标与结论

Codeg 的远程访问入口必须满足：固定 HTTPS 地址、手机公网浏览器直达、访问者不安装 VPN、不要求用户购买域名。随机链接方案不接受，因此 Cloudflare Quick Tunnel、免费随机 SSH 隧道不纳入本次集成。

已选择 **Tailscale Funnel + 主机已安装的 Tailscale CLI**。主机完成安装、账号登录及 Funnel 授权；手机无需 Tailscale 客户端或账号，仍需 Codeg 的访问 token。入口放在设置 → Web 服务，确认映射成功后公网地址排在访问地址列表首位并默认选中。

CLI 集成已实现，定向测试和构建检查通过。实现与测试记录见 [tailscale-funnel-implementation.md](tailscale-funnel-implementation.md)，操作说明见 [tailscale-funnel.md](tailscale-funnel.md)。本轮未实际开通公网隧道，不将模拟测试视作真实移动网络验证。

## 工具对比

| 方案 | 固定地址 | 手机免客户端 | 主机端前提 | 本次选择 |
|---|---|---|---|---|
| Tailscale Funnel | 保留节点/DNS/tailnet 时稳定 | 是 | 安装、登录、Funnel 权限 | 实施 |
| Tailscale Serve | 稳定的私网地址 | 否，需加入 tailnet | 安装、登录 | 不满足公网需求 |
| ngrok 分配的开发域名 | 账号分配的固定域名 | 是 | 注册、配置凭据 | 备选，当前额度另核验 |
| Cloudflare 命名隧道 | 自有主机名稳定 | 是 | 账号、域名及 DNS/隧道配置 | 有域名用户的备选 |
| Cloudflare Quick Tunnel | 每次创建随机 | 是 | cloudflared | 排除 |
| 自营 frp/rathole 等入口 | 产品方分配固定地址 | 可实现 | 产品方维护公网服务器及 HTTPS | 需要额外运维 |

“稳定”不是域名永久所有权或服务可用性保证。机器 DNS 名称、tailnet 域名、节点身份改变时地址可能变化；主机休眠、关机、断网时暂不可访问。国内 DNS、登录、Funnel relay 和手机网络体验需要分别实测，不能用 DERP 节点位置直接推断 Funnel 性能。

## Tailscale 官方条件

- Funnel 面向公网，Serve 面向 tailnet；两者不能混淆。
- 需要 MagicDNS、HTTPS 证书、允许 Funnel 的 node attribute。CLI 可引导开通 HTTPS/Funnel，但仍受账号权限约束，不能承诺全部无交互。
- 官方文档将 Funnel 标为 beta，并说明有不可自行配置的带宽限制；不承诺无限带宽或生产 SLA。
- 公网监听端口仅 443、8443、10000，本地 Codeg 端口不受此限制。本次使用公网 443，代理默认本地 3080。
- `tsnet` 是可嵌入的 Go 库，可实现主机免单独安装客户端，但仍需认证、节点持久化和权限管理；本次未实现。

## CLI 合约（基于官方 v1.104.5 源码）

```bash
# 登录/连接当前 profile，避免 login 创建新的 profile
tailscale up --timeout=120s

# 公开本地 Codeg。首次可能输出授权地址并等待用户完成
tailscale funnel --bg --https=443 --yes http://127.0.0.1:3080

# 必须核对实际配置，不能只看上一条命令的退出码
tailscale funnel status --json

# 仅当 443 全部配置都属于本应用时执行；不会清空其他端口
tailscale serve --https=443 off
```

- `up --json` 可用；`login --json` 不存在。交互输出必须边读边处理，不能等待进程退出才展示授权 URL。
- `--bg` 表示配置持久化到 tailscaled，`--yes` 不会绕过账号权限或授权。Funnel 可以退出 0 而没有建立映射。
- `serve status --json` 与 `funnel status --json` 返回完整 ServeConfig：`TCP`、`Web`、`AllowFunnel`，以及 `Foreground` 会话子配置。仅有 DNS 名称或 `AllowFunnel=true` 不代表 Codeg 已公开。
- 要同时匹配 HTTPS listener、根路径代理到当前 Codeg 端口和 AllowFunnel 标记；私有 Serve、其他目标和前台会话配置不能误判。
- 停用优先使用 `serve … off`，避免 `funnel … off` 在权限被撤销后再次要求授权。该命令会移除指定端口的所有 handler，必须先核验归属；不得使用全局 reset。
- 现有 Codeg WebSocket 通过子协议传 token，不使用 query token，无需因旧 issue 更改认证。

## 验证边界

自动化检查覆盖配置解析、状态转换、归属冲突、授权/取消、启停恢复及地址选择。以下仍需要真实账号和网络验证：

- 未安装 Tailscale 的手机通过蜂窝网络访问并输入 Codeg token。
- 会话流式输出、WebSocket 长连接重连、终端与文件操作。
- 首次 HTTPS/Funnel 权限开通、主机重启后恢复及地址稳定。
- 目标地区的 DNS、控制平面、relay 连通性和吞吐。

## 参考

- [Funnel 官方文档](https://tailscale.com/docs/features/tailscale-funnel)
- [Funnel CLI](https://tailscale.com/docs/reference/tailscale-cli/funnel)
- [tsnet](https://tailscale.com/docs/features/tsnet)
- [up 实现](https://github.com/tailscale/tailscale/blob/v1.104.5/cmd/tailscale/cli/up.go)
- [Funnel 配置与删除](https://github.com/tailscale/tailscale/blob/v1.104.5/cmd/tailscale/cli/serve_v2.go)
- [首次授权流程](https://github.com/tailscale/tailscale/blob/v1.104.5/cmd/tailscale/cli/serve_legacy.go)
- [ServeConfig 数据结构](https://github.com/tailscale/tailscale/blob/v1.104.5/ipn/serve.go)
- [ngrok 免费方案说明](https://ngrok.com/docs/pricing-limits/free-plan-limits)
- [Cloudflare Quick Tunnel](https://developers.cloudflare.com/tunnel/get-started/quick-tunnels/)
