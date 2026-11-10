# 使用 Tailscale 公网访问 Codeg

> 已实现 CLI 集成并通过定向自动化测试和构建检查；真实 Tailscale 授权、手机蜂窝网络和主机重启尚未实测。自动化测试不代表已验证公网可达。

Tailscale Funnel 给运行中的 Codeg Web 服务提供稳定的公网 HTTPS 地址。手机只需浏览器，无需安装 Tailscale，也不需要 Tailscale 账号。运行 Codeg 的电脑需要安装 Tailscale 并完成一次登录和公网授权。

## 首次开启

1. 打开 **设置 → Web 服务**，启动 Web 服务。缺省本地端口为 **3080**，也可以在启动前修改。
2. 在 **Tailscale 公网访问** 区域检查状态。若未安装，使用安装入口前往 [Tailscale 下载页](https://tailscale.com/download)，在运行 Codeg 的主机完成安装，回来刷新。
3. 若未登录，点击登录并打开返回的授权页面，完成 Tailscale 账号登录。Codeg 连接当前账号配置，不主动切换到新 profile。
4. 点击开启公网访问。首次可能需要打开授权链接，允许 HTTPS/Funnel。MagicDNS、账号管理权限或设备审批未满足时，根据 Tailscale 页面完成设置后重试。
5. 配置确认成功后，访问地址列表最前面显示 `https://机器名.tailnet名.ts.net`，并自动选中。使用现有复制、二维码和打开按钮即可访问。
6. 手机打开地址后，使用 Web 服务页显示的 Codeg **访问令牌**登录。二维码和 URL 不包含令牌，请单独保管。

不需要购买域名或配置路由器端口转发。Tailscale 安装和首次账号/公网授权仍由用户确认，Codeg 不会代替用户注册账号。

## 地址选择与运行状态

- 确认有效的公网地址优先展示；仍可手动切换本地或局域网地址。
- 普通状态刷新不会覆盖当前页面里手动选定的地址；再次进入页面优先显示有效公网地址。
- 登录授权地址不属于服务访问地址，不会放进访问地址列表。
- “已启用”表示 Tailscale 配置已核验，代理目标是当前 Codeg 服务。不同地区的网络可达性需要实际访问确认。

## 停用和恢复

- **停用公网访问**：移除 Codeg 管理的映射，清除自动恢复偏好，保留本地 Web 服务。
- **停止 Web 服务/正常退出 Codeg**：取消待处理操作，尝试清理本应用映射，保留偏好。下次 Web 服务成功启动后尝试恢复。
- **取消进行中的操作**：结束等待中的命令并核对是否需要撤销本应用刚创建的配置。
- 清理失败会提示错误；请检查 Tailscale 后台状态，必要时按下方方法处理。强制结束进程、断电和系统崩溃无法保证执行清理。
- 主机关机或休眠期间服务不可访问。固定链接不代表永久在线；节点被删除重建、机器 DNS 名称或 tailnet 域名变化可能改变地址。

## 常见问题

| 状态或问题 | 处理方法 |
|---|---|
| 未检测到 Tailscale | 在运行 Codeg 的主机安装；确认 CLI 可用，重新刷新 |
| Tailscale 服务不可用 | 启动 Tailscale 应用/系统服务，检查本机权限 |
| 未登录/设备待审批 | 完成登录，必要时由 tailnet 管理员批准设备 |
| 等待公网授权 | 打开授权链接；缺少权限时请 tailnet 管理员启用 HTTPS/Funnel |
| 权限不足 | 依平台配置 Tailscale 的本机管理权限；应用不会自动提权 |
| 443 配置冲突 | 已有其他 Serve/Funnel 服务占用，不自动覆盖或删除；自行确认后调整 |
| 网页打不开但显示已启用 | 检查主机、Codeg Web 服务、DNS 和访问网络；不要把配置成功等同于所有网络可达 |
| 地址由外部配置管理 | 可以展示匹配的地址，但 Codeg 不应删除不属于本应用的配置 |
| 代理绑定地址不受支持 | 当前集成代理到 IPv4 loopback；使用 Web 服务默认绑定或 127.0.0.1 |

Funnel 公网监听使用 443，Codeg 本地仍使用所设置的端口。Funnel 支持的其他公网端口和嵌入式 tsnet 不在本次 UI 集成范围内。

## 手动检查

在运行 Codeg 的主机执行：

```bash
tailscale status --json
tailscale funnel status --json
```

本次使用的配置目标示例：

```bash
tailscale funnel --bg --https=443 --yes http://127.0.0.1:3080
```

不要仅根据退出码判断配置成功。状态中必须同时包含公网 443 的 HTTPS listener、代理到当前 Codeg 本地端口的 handler 和对应的 `AllowFunnel`。

仅在确认 443 上所有 handler 都属于应关闭的服务时，手动清理：

```bash
tailscale serve --https=443 off
```

该命令会移除该端口所有 handler，不要在混有其他服务时盲目执行。不要使用全局 `serve reset`/`funnel reset` 来处理 Codeg 的单个映射。

Codeg 会在操作前核验配置，但 CLI 的查询与修改不是跨进程事务。使用界面启停期间，避免同时在终端或其他应用修改同一台主机的 443 映射；检测到冲突时应先刷新确认，不能反复强制覆盖。

## 验证记录

构建、测试及已知全仓检查失败见 [tailscale-funnel-verification.md](tailscale-funnel-verification.md)。

## 官方资料

- [安装 Tailscale](https://tailscale.com/download)
- [Funnel 使用条件与限制](https://tailscale.com/docs/features/tailscale-funnel)
- [Funnel CLI](https://tailscale.com/docs/reference/tailscale-cli/funnel)

Funnel 当前官方文档标为 beta，并有不可自行调整的带宽限制。可用性和套餐条件以官方文档为准。
