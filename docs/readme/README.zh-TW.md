<h1 align="center">
  <a href="https://ijry.github.io/yoyocode-docs"><img src="../../public/icon.svg" alt="YoYoCode logo" width="64" align="absmiddle" /></a> YoYoCode
</h1>

<p align="center">
  <a href="https://github.com/ijry/YoYoCode"><img src="https://img.shields.io/github/stars/ijry/YoYoCode?style=flat&color=e91e63" alt="GitHub stars" /></a>
  <a href="https://github.com/ijry/YoYoCode/releases/latest"><img src="https://img.shields.io/github/v/release/ijry/YoYoCode?style=flat&color=e91e63" alt="Latest release" /></a>
  <a href="https://github.com/ijry/YoYoCode/releases"><img src="https://img.shields.io/github/downloads/ijry/YoYoCode/total?style=flat&color=e91e63" alt="Total downloads" /></a>
  <a href="../../LICENSE"><img src="https://img.shields.io/github/license/ijry/YoYoCode?style=flat&color=e91e63" alt="License" /></a>
  <a href="https://ijry.github.io/yoyocode-docs"><img src="https://img.shields.io/badge/docs-yoyocode--docs-3451b2?style=flat" alt="Documentation" /></a>
  <a href="https://ijry.github.io/yoyocode-docs/zh/getting-started/installation"><img src="https://img.shields.io/badge/macOS%20%7C%20Windows%20%7C%20Linux%20%7C%20Docker%20%7C%20iOS%20%7C%20Android-03a9f4?style=flat" alt="Platforms: macOS, Windows, Linux, Docker, iOS, Android" /></a>
</p>

<p align="center">
  <sub><a href="../../README.md">English</a> · <a href="./README.zh-CN.md">简体中文</a> · <strong>繁體中文</strong> · <a href="./README.ja.md">日本語</a> · <a href="./README.ko.md">한국어</a> · <a href="./README.es.md">Español</a> · <a href="./README.de.md">Deutsch</a> · <a href="./README.fr.md">Français</a> · <a href="./README.pt.md">Português</a> · <a href="./README.ar.md">العربية</a></sub>
</p>

<p align="center">
  <strong>多智慧體編碼工作台。</strong><br/>
  把所有 AI 編碼智慧體收進同一個地方——並讓它們協同工作。
</p>

<h3 align="center"><a href="https://github.com/ijry/YoYoCode/releases/latest"><ins>下載 YoYoCode</ins></a> · <a href="https://ijry.github.io/yoyocode-docs/zh"><ins>說明文件</ins></a> · <a href="https://getmcode.lingyun.net"><ins>App · getmcode.lingyun.net</ins></a></h3>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="../images/workspace-dark.png" />
    <img src="../images/workspace-light.png" alt="YoYoCode 工作區：與智慧體的對話，旁邊是它的即時 diff 與專案檔案" width="960" />
  </picture>
</p>

<table>
  <tr>
    <td width="50%" valign="top">🧩 <strong>一個介面，容納所有智慧體</strong><br/>內建二十個智慧體，任何相容 ACP 的智慧體也都能加入——全部呈現為同一套結構化對話，而不是一個終端機。</td>
    <td width="50%" valign="top">🔎 <strong>工作階段自由流轉</strong><br/>匯入、搜尋、接續每個智慧體留在磁碟上的歷史，還能把它交給另一個智慧體接手。</td>
  </tr>
  <tr>
    <td width="50%" valign="top">🤝 <strong>智慧體協同工作</strong><br/>一個 <code>@</code> 就能跨智慧體委派，也可以把待辦任務排進佇列，在各自的工作樹裡無人看管地執行。</td>
    <td width="50%" valign="top">🌍 <strong>無論你在哪裡工作</strong><br/>桌面應用、自架伺服器或 Docker，iPhone、iPad 與 Android——還有 Telegram、飛書或微信。</td>
  </tr>
</table>

## 🆚 與上游 Codeg 的差異

[YoYoCode](https://github.com/ijry/YoYoCode) 是 [spacering-net/codeg](https://github.com/spacering-net/codeg) 的分支。上游的全部功能這裡都有，此外還新增：

| 功能 | 說明 |
|---|---|
| **📬 持久化、由後端接管的訊息佇列** | 佇列的認領與傳送完全由 Rust 後端控制——原子認領 + 傳送確認、陳舊儲存防復活，啟動 / 每 30 秒掃描 / 儲存與回合狀態變化時觸發投遞。即使沒有視窗開啟、沒有連線，它也會自行續工作階段連線，排隊任務照常無人值守地跑完。 |
| **⏸️ 停止時扣住、出錯後續跑** | 手動停止回合會把排隊訊息扣住，不讓它們尾隨到下一輪；執行失敗的回合停在持久化的恢復點上，可在原上下文繼續，只有真正成功後才釋放後續訊息。 |
| **⏰ 定時傳送工作階段訊息** | 給訊息設好時間，到點自動傳送，內建 30 分鐘 / 2 小時預設。尤其適合額度重置後無人值守自動傳送工作階段——定時訊息在到點前不會阻擋佇列裡的其他訊息。 |
| **🌐 Tailscale Funnel 公網存取** | 一鍵把 Web 服務發佈為公網 HTTPS 位址——含登入授權、裝置審核與 DNS 問題區分，以及自有對應的清理。**MCode v2** 配對 QR 碼跟隨目前位址並攜帶存取權杖。 |
| **📷 新增完善的截圖工具** | 凍結螢幕後，可按疊放順序從螢幕上的視窗中挑選選區（懸停預覽），也可自由框選；選定後可移動、縮放、標註、釘在螢幕上、儲存或複製。macOS 上透過短生命週期的授權 helper 使用 ScreenCaptureKit 原生擷取。 |
| **🧠 模型切換與 ZCode 供應商** | 工作階段內的模型切換會持久化並顯示在對話記錄中；ZCode 設定新增供應商下拉，可將 `ZCODE_PROVIDER` 固定到指定帳號。 |
| **📚 漫畫工作台** | 漫畫工作台的創作流程改進。 |

分支側改動的完整清單及與上游的同步方式，見 [PLUS-PATCHES.md](../../docs/PLUS-PATCHES.md)。

## ✨ 功能特色

<table>
<tr>
<td width="50%" valign="middle">

### 一個介面，容納所有智慧體

二十個內建智慧體，相容任意 ACP 智慧體。工具卡片、即時 diff 與互動式回覆，盡在同一介面。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/guide/supported-agents) · [自訂智慧體 →](https://ijry.github.io/yoyocode-docs/zh/guide/custom-agents) · [介面渲染 →](https://ijry.github.io/yoyocode-docs/zh/guide/skills#cards-tables-and-charts-in-replies)

</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../images/features/agents-dark.gif" />
  <img src="../images/features/agents-light.gif" alt="一個介面，容納所有智慧體" width="100%" />
</picture>
</td>
</tr>
<tr>
<td width="50%" valign="middle">

### 每個工作階段，都搜得到

一鍵匯入、搜尋並繼續歷史對話。用 `@` 跨智慧體分享工作階段，用標籤整理對話。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/guide/aggregation) · [標籤 →](https://ijry.github.io/yoyocode-docs/zh/guide/workspace#tag-conversations)

</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../images/features/sessions-dark.gif" />
  <img src="../images/features/sessions-light.gif" alt="每個工作階段，都搜得到" width="100%" />
</picture>
</td>
</tr>
<tr>
<td width="50%" valign="middle">

### 多智慧體協作

用 `@` 委派任務，讓多個智慧體平行協作，進度即時匯入目前的對話。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/guide/multi-agent)

</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../images/features/multi-agent-dark.gif" />
  <img src="../images/features/multi-agent-light.gif" alt="多智慧體協作" width="100%" />
</picture>
</td>
</tr>
<tr>
<td width="50%" valign="middle">

### 無人看管也能跑的待辦任務

任務擁有獨立工作樹，可立即執行或排程啟動。智慧體完成工作，由你驗收變更。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/guide/tasks) · [自動處理 →](https://ijry.github.io/yoyocode-docs/zh/guide/repository#let-it-pick-items-up-by-itself)

</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../images/features/todos-dark.gif" />
  <img src="../images/features/todos-light.gif" alt="無人看管也能跑的待辦任務" width="100%" />
</picture>
</td>
</tr>
<tr>
<td width="50%" valign="middle">

### 無限畫布

在無限畫布上擺放對話、檔案、終端機與便箋，讓多個智慧體的工作一目了然。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/guide/canvas)

</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../images/features/canvas-dark.gif" />
  <img src="../images/features/canvas-light.gif" alt="無限畫布" width="100%" />
</picture>
</td>
</tr>
<tr>
<td width="50%" valign="middle">

### 桌面、伺服器、行動裝置與聊天軟體

支援桌面、伺服器、Docker 與行動裝置，也能透過 Telegram、飛書和微信與智慧體對話。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/getting-started/deployment) · [遠端工作區 →](https://ijry.github.io/yoyocode-docs/zh/guide/workspace#desktop-and-browser) · [getmcode.lingyun.net →](https://getmcode.lingyun.net) · [訊息頻道 →](https://ijry.github.io/yoyocode-docs/zh/guide/chat-channels)

</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../images/features/anywhere-dark.gif" />
  <img src="../images/features/anywhere-light.gif" alt="桌面、伺服器、行動裝置與聊天軟體" width="100%" />
</picture>
</td>
</tr>
<tr>
<td width="50%" valign="middle">

### Office 文件與科學研究

產生 Office 文件並即時預覽，直接檢視 PDF。內建科研技能，任意智慧體都能使用。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/guide/office) · [科學研究 →](https://ijry.github.io/yoyocode-docs/zh/guide/research)

</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../images/features/office-dark.gif" />
  <img src="../images/features/office-light.gif" alt="Office 文件與科學研究" width="100%" />
</picture>
</td>
</tr>
<tr>
<td width="50%" valign="middle">

### 瀏覽器與電腦操作

從桌面內建瀏覽器分享頁面與錯誤，用電腦操作（預覽版）讓智慧體操作共享視窗。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/guide/browser) · [電腦操作 →](https://ijry.github.io/yoyocode-docs/zh/guide/computer-use)

</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../images/features/browser-dark.gif" />
  <img src="../images/features/browser-light.gif" alt="瀏覽器與電腦操作" width="100%" />
</picture>
</td>
</tr>
<tr>
<td width="50%" valign="middle">

### 命令列與 CI

`yoyo-cli` 將腳本與 CI 接入執行中的 YoYoCode，輸出智慧體回覆或 JSON 事件。

[文件 →](https://ijry.github.io/yoyocode-docs/zh/guide/command-line)

</td>
<td width="50%" valign="middle">

```bash
yoyo-cli -a codex \
  -p "Review the latest commit"

git diff | yoyo-cli -a codex \
  -p "Review this diff"
```

</td>
</tr>
</table>

**還有這些：**

- **[完整的工程閉環](https://ijry.github.io/yoyocode-docs/zh/guide/workspace)** — 程式碼編輯器、即時 diff、[Git 與工作樹](https://ijry.github.io/yoyocode-docs/zh/guide/git)、三欄合併編輯器、終端機與分割檢視
- **[儲存庫面板](https://ijry.github.io/yoyocode-docs/zh/guide/repository)** — GitHub、GitLab、Gitea 與 Forgejo 的議題、拉取請求、檢查項與合併；任何議題或 PR 都能轉成待辦任務
- **[自動化](https://ijry.github.io/yoyocode-docs/zh/guide/automations)** — 把設定好的輸入框存下來，依 cron 排程或手動觸發，以無介面方式執行
- **[Token 用量](https://ijry.github.io/yoyocode-docs/zh/guide/token-usage)** — 趨勢、快取命中率、活躍熱力圖、依資料夾、智慧體、模型與工作階段劃分的用量，以及按 API 標價估算的費用
- **[分叉與插話](https://ijry.github.io/yoyocode-docs/zh/guide/workspace#follow-along-%E2%80%94-the-conversation)** — 在支援的智慧體上，從已完成的回覆分叉對話，或把訊息插入仍在進行中的回合
- **[技能](https://ijry.github.io/yoyocode-docs/zh/guide/skills) 與 [MCP](https://ijry.github.io/yoyocode-docs/zh/guide/mcp)** — 依智慧體分別啟用的技能包、本機 MCP 掃描，以及註冊表搜尋與安裝
- **[專案啟動器](https://ijry.github.io/yoyocode-docs/zh/guide/project-boot)** — 視覺化建立新專案，並即時預覽
- **[通知與提醒](https://ijry.github.io/yoyocode-docs/zh/reference/settings/general#desktop-notifications)** — 回合結束或智慧體需要你時，發出桌面通知與提示音，並讓工作列按鈕閃爍、Dock 圖示跳動
- **[外觀自訂](https://ijry.github.io/yoyocode-docs/zh/reference/settings/appearance)** — 十二套主題都能逐個色彩 token 重新調色，還有桌布、圓角、聊天區域寬度、自訂 CSS 與桌面寵物——介面支援十種語言
- **[備份與同步](https://ijry.github.io/yoyocode-docs/zh/reference/settings/system#backup-restore)** — 加密備份，以及透過檔案或你自己的 WebDAV 伺服器在多台機器之間同步設定
- **[URL Scheme](../../docs/url-scheme.md)** — 用 `codeg://session/<id>` 從其他應用程式開啟對話（桌面端）
- **還有更多** — 幾乎每個版本都會加入新東西；完整清單見 [發行說明](https://github.com/ijry/YoYoCode/releases)

## 🤖 支援的智慧體

YoYoCode 透過 [Agent Client Protocol](https://agentclientprotocol.com) 與每個智慧體溝通，所以每一個都能用上同一套結構化介面。內建二十個，其中大部分 YoYoCode 都能替你安裝、鎖定版本並更新：

<p>
  <a href="https://www.anthropic.com/claude-code"><kbd><img src="../images/agents/claude-code.svg" alt="" width="16" valign="middle" /> Claude Code</kbd></a> &nbsp;
  <a href="https://github.com/openai/codex"><kbd><img src="../images/agents/codex.svg" alt="" width="16" valign="middle" /> Codex</kbd></a> &nbsp;
  <a href="https://github.com/google-gemini/gemini-cli"><kbd><img src="../images/agents/gemini-cli.svg" alt="" width="16" valign="middle" /> Gemini CLI</kbd></a> &nbsp;
  <a href="https://openclaw.ai"><kbd><img src="../images/agents/openclaw.svg" alt="" width="16" valign="middle" /> OpenClaw</kbd></a> &nbsp;
  <a href="https://opencode.ai"><kbd><picture><source media="(prefers-color-scheme: dark)" srcset="../images/agents/opencode-dark.svg" /><img src="../images/agents/opencode.svg" alt="" width="16" valign="middle" /></picture> OpenCode</kbd></a> &nbsp;
  <a href="https://cline.bot"><kbd><picture><source media="(prefers-color-scheme: dark)" srcset="../images/agents/cline-dark.svg" /><img src="../images/agents/cline.svg" alt="" width="16" valign="middle" /></picture> Cline</kbd></a> &nbsp;
  <a href="https://hermes-agent.nousresearch.com"><kbd><picture><source media="(prefers-color-scheme: dark)" srcset="../images/agents/hermes-agent-dark.svg" /><img src="../images/agents/hermes-agent.svg" alt="" width="16" valign="middle" /></picture> Hermes Agent</kbd></a> &nbsp;
  <a href="https://www.codebuddy.ai"><kbd><picture><source media="(prefers-color-scheme: dark)" srcset="../images/agents/codebuddy-dark.svg" /><img src="../images/agents/codebuddy.svg" alt="" width="16" valign="middle" /></picture> CodeBuddy</kbd></a> &nbsp;
  <a href="https://www.kimi.com/code"><kbd><img src="../images/agents/kimi-code.svg" alt="" width="16" valign="middle" /> Kimi Code</kbd></a> &nbsp;
  <a href="../agents/minimax-code.md"><kbd><img src="../images/agents/minimax-code.svg" alt="" width="16" valign="middle" /> MiniMax Code</kbd></a> &nbsp;
  <a href="https://pi.dev"><kbd><img src="../images/agents/pi.svg" alt="" width="16" valign="middle" /> Pi</kbd></a> &nbsp;
  <a href="https://x.ai/cli"><kbd><picture><source media="(prefers-color-scheme: dark)" srcset="../images/agents/grok-dark.svg" /><img src="../images/agents/grok.svg" alt="" width="16" valign="middle" /></picture> Grok</kbd></a> &nbsp;
  <a href="https://cursor.com/cli"><kbd><picture><source media="(prefers-color-scheme: dark)" srcset="../images/agents/cursor-dark.svg" /><img src="../images/agents/cursor.svg" alt="" width="16" valign="middle" /></picture> Cursor</kbd></a> &nbsp;
  <a href="https://deepseek-harness.github.io/deepseek-harness/"><kbd><img src="../images/agents/deepseek-harness.svg" alt="" width="16" valign="middle" /> DeepSeek Harness</kbd></a> &nbsp;
  <a href="https://qoder.com"><kbd><picture><source media="(prefers-color-scheme: dark)" srcset="../images/agents/qoder-dark.svg" /><img src="../images/agents/qoder.svg" alt="" width="16" valign="middle" /></picture> Qoder</kbd></a> &nbsp;
  <a href="https://antigravity.google"><kbd><picture><source media="(prefers-color-scheme: dark)" srcset="../images/agents/google-antigravity-dark.svg" /><img src="../images/agents/google-antigravity.svg" alt="" width="16" valign="middle" /></picture> Google Antigravity</kbd></a> &nbsp;
  <a href="https://kiro.dev/cli/"><kbd><img src="../images/agents/kiro.svg" alt="" width="16" valign="middle" /> Kiro CLI</kbd></a> &nbsp;
  <a href="../agents/zcode.md"><kbd><img src="../images/agents/zcode.svg" alt="" width="16" valign="middle" /> ZCode</kbd></a> &nbsp;
  <a href="https://docs.trae.cn/cli_about-trae-code-cli-2"><kbd><img src="../images/agents/trae.svg" alt="" width="16" valign="middle" /> TRAE CLI</kbd></a> &nbsp;
  <a href="https://commandcode.ai"><kbd><img src="../images/agents/command-code.svg" alt="" width="16" valign="middle" /> Command Code</kbd></a> &nbsp;
  <a href="https://ijry.github.io/yoyocode-docs/zh/guide/custom-agents"><kbd>+ 任何 ACP 智慧體</kbd></a>
</p>

名單之外的呢？從公開的 ACP 註冊表裡挑一個，或貼上它的 distribution JSON——YoYoCode 會安裝它、預檢它能否啟動，然後像對待內建智慧體一樣對待它。各智慧體的執行環境需求以及工作階段的存放位置，見 [支援的智慧體](https://ijry.github.io/yoyocode-docs/zh/guide/supported-agents)。

## 📦 安裝

**桌面端** — 從 [Releases](https://github.com/ijry/YoYoCode/releases/latest) 下載 macOS、Windows 或 Linux 的安裝檔，再依 [安裝](https://ijry.github.io/yoyocode-docs/zh/getting-started/installation) 操作。

**伺服器** — 無介面執行 YoYoCode，用任意瀏覽器存取。Linux 或 macOS：

```bash
curl -fsSL https://raw.githubusercontent.com/ijry/YoYoCode/main/install.sh | bash
CODEG_STATIC_DIR=/usr/local/share/codeg/web yoyocode-server
```

Windows（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/ijry/YoYoCode/main/install.ps1 | iex
$env:CODEG_STATIC_DIR="$env:LOCALAPPDATA\yoyocode-server\web"; yoyocode-server
```

**Docker** — 同一個伺服器，裝進一個容器：

```bash
docker run -d -p 18989:18989 -v codeg-data:/data ghcr.io/ijry/YoYoCode:latest
```

無論哪種方式，都在瀏覽器中開啟 `http://<host>:18989`，輸入伺服器啟動時印出的存取權杖（容器可用 `docker logs` 查看）；也可以用 `CODEG_TOKEN` 自行指定。

**行動裝置** — 從 [getmcode.lingyun.net](https://getmcode.lingyun.net) 取得行動裝置應用程式，然後指向桌面應用的**Web 服務**或你自己的 `yoyocode-server`：填 URL 和 token 即可。配對步驟見[getmcode.lingyun.net](https://getmcode.lingyun.net)。

Compose、預編譯二進位檔、原始碼建置與就地升級見 [部署](https://ijry.github.io/yoyocode-docs/zh/getting-started/deployment)；環境變數見 [設定](https://ijry.github.io/yoyocode-docs/zh/getting-started/configuration)。

## 👥 社群與支援

- **微信** — 掃描下方 QR Code 加入我們的微信群，參與討論、回饋與更新：

  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="../images/weixin-dark.jpg" />
    <img src="../images/weixin-light.jpg" alt="WeChat" width="200" />
  </picture>

- **問題回報** — 發現 bug，或是缺了什麼功能？[開一個 issue](https://github.com/ijry/YoYoCode/issues)。
- **隱私** — 本機優先：解析、儲存與專案操作都留在你的機器上，Web 模式與伺服器模式皆以權杖驗證把關。詳見 [隱私與安全](https://ijry.github.io/yoyocode-docs/zh/reference/privacy)。
- **LinuxDO** — 感謝 [LinuxDO](https://linux.do) 社群的支持。
- **支持我們** — [幫儲存庫點個 Star](https://github.com/ijry/YoYoCode)，追蹤後續進展。

## 🤝 參與貢獻

歡迎提交 issue 與 pull request。想自己建置 YoYoCode，可以從 [開發](https://ijry.github.io/yoyocode-docs/zh/reference/development) 與 [架構](https://ijry.github.io/yoyocode-docs/zh/reference/architecture) 開始。

<a href="https://github.com/ijry/YoYoCode/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=ijry/YoYoCode" alt="YoYoCode 貢獻者" />
</a>

## ⭐ Star 歷史

<a href="https://www.star-history.com/?repos=ijry%2FYoYoCode&type=date&releases=&legend=bottom-right">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=ijry/YoYoCode&type=date&theme=dark&legend=bottom-right" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=ijry/YoYoCode&type=date&legend=bottom-right" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=ijry/YoYoCode&type=date&legend=bottom-right" />
 </picture>
</a>

## 🙏 致謝

- [Agent Client Protocol](https://agentclientprotocol.com)：YoYoCode 得以連接所有支援的智慧體的基礎
- [Superpowers](https://github.com/obra/superpowers)：為 YoYoCode 的專家技能模組提供支援
- [OfficeCLI](https://github.com/iOfficeAI/OfficeCLI)：為 YoYoCode 的 Office 文件工作流程提供支援
- [scientific-agent-skills](https://github.com/K-Dense-AI/scientific-agent-skills)：為 YoYoCode 的科學研究技能提供支援（MIT 授權子集）
- [json-render](https://github.com/vercel-labs/json-render)：為 YoYoCode 的介面渲染（卡片、表格與圖表）提供支援
- [cua-driver](https://github.com/trycua/cua)：為 YoYoCode 的電腦操作提供支援

## 📜 授權

Apache-2.0，詳見 [LICENSE](../../LICENSE)。
