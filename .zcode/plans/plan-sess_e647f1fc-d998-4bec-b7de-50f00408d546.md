# 漫剧制作全流程（Comic Studio）实施方案

## Context

在 YoYoCode（codeg-plus）中新增「漫剧制作」能力：一个项目下规范管理**小说原文、剧集、剧本、分镜、角色三视图（含多服装）、场景、道具**，覆盖五段式管线（小说原文 → 剧本改编 → 人设三视图 → 分镜 → 关键帧出图 → 图生视频+配音+合成导出）。

**已确认决策**：① 生成能力一步到位全做；② 资源以**文件为事实源**，DB 只存生成任务与 provider 配置；③ 漫剧 MCP 走现有 yoyocode-mcp 伴生进程加 comic 工具组；④ 前端为漫剧工作台三栏；⑤ 多厂商 provider 采用**全局厂商池 + 项目级指针 + 任务级快照**三层模型；⑥ 交互以**会话为唯一生产驱动器**，工作台=全局观察+快捷操作（点击即发预置指令到主会话）。
**补充要求**：拿不到小说原文必须引导上传/指定路径；工具结果不进 LLM 上下文；成本护栏与失败降级。

## 交互设计（会话为中心）

**分流原则**：生产动作（改编/分镜/出图/选图/配音/合成）→ 会话（LLM+MCP 工具）；纯机械编辑（改台词/排序/改名/切 provider 指针）→ 工作台直写；工作台快捷按钮 → 预置指令发主会话。

**关键交互流**：
1. **改编**：无原文时 LLM 用 `ask_user_question` 引导 → 用户拖 txt 进会话 → `comic_set_source` 收纳+分章 → **分章统计预览回给用户确认**，不满意可重新切分（参数：正则模式/字数兜底）。
2. **出图 N 候选**：`comic_request_generation(count=4)` → 并行生成 → `comic_present_candidates` 阻塞式选择卡（图片走前端渲染，**工具返回只有 id/路径**）→ 点选回传 → `comic_set_shot_image` 绑定。超成本阈值（单轮张数/并发，可配）时宿主强制先 `ask_user_question` 确认规模。
3. **失败降级**：错误分类（审核拒绝/网络/配额/参数）随 `comic_check_generation` 返回，LLM 据此改 prompt 或换厂商重试。
4. **厂商切换**：`comic_get_project` 返回项目指针+全局池；「后面都换可灵」→ `comic_set_project_provider`；「这张用 ComfyUI」→ 单次覆盖；工作台下拉直改。切换只影响新任务，生成记录快照 provider/model/prompt/seed，可追溯可对比。
5. **断点续作**：`comic_get_project` 返回各集/镜状态机位置 + `next_action` 建议（无原文→要原文；有原文无剧本→改编；有分镜未出图→出图…）；会话崩溃后新会话从文件状态自然续作。
6. **资产更新联动**：三视图换版后 `comic_upsert_character` 返回受影响镜头列表，提示可批量重出。

**主会话**：项目可多会话，工作台指定主会话（默认最近活跃，无则自动创建），快捷指令发主会话。

## 总体架构

```
用户 ◄── 漫剧工作台三栏（资源树[含原文]|观察区[分镜/资产/任务]|会话）
  │        ▲ comic://changed 实时刷新          │ 快捷按钮=预置指令→主会话
  ▼        │                                    ▼
会话(folder=漫剧项目) ──MCP stdio──► yoyocode-mcp（comic 组，仅 story/ 项目注入）
                                                     │ UDS/命名管道（现有 delegation 通道）
                                                     ▼
宿主 DelegationListener::serve_one → ComicAccess（含阻塞选择卡）→ comic/ 领域模块
                                                     ▼
store(per-folder 写互斥+story/ jail) │ 生成任务(DB)→worker(限流排队)→产物落盘→N 候选
                                                     ▼
MediaGenerationProvider 全局池适配层: OpenAI 兼容生图 / ComfyUI / 可灵·即梦视频 / TTS / ffmpeg
```

## 资源模型（文件为事实源，story 内一律相对路径）

```
story/
  series.json        # 剧名/来源/风格bible引用/全局seed/候选数默认/成本阈值
                     # providers:{image:{id,name},video:{},tts:{}} 项目指针（跨机按名兜底解析）
  bible/*.md
  source/            # novel.txt + source.json(mode|外部路径|original) + chapters.json 分章索引
  episodes/ep001/
    episode.json     # 标题/梗概/状态机(draft|scripted|storyboarded|rendering|done)/source_chapters
    script.md  storyboard.json   # shots[]：镜号/描述/台词/时长/运镜/景别/转场/引用/状态/选中候选
    shots/shot-0001/ # shot.json（状态机 draft→prompted→generating→candidate→selected→rendering→done
                     #   + 生成记录[]：provider/model/prompt/seed/参数快照 + 引用 character@outfit）
                     #   + candidates/*.png + first-frame.png + clip.mp4 + audio/*.mp3
    export/ep001.mp4
  characters/<slug>/ # character.json（外形/性格/提示词片段/seed + outfits[]：每套服装独立
                     #   三视图与提示词）+ turnaround/{front,side-45,side}.png + expressions/
  scenes/<slug>/ props/<slug>/   # 各自 json + ref/*.png
```

原文获取三选一（缺失必须引导）：会话拖入/工作台上传（复用 `handlers/files.rs`）/ 指定路径（校验后复制入项目）/ 原创跳过。Rust 端分章（`第[一二…百千\d]+[章节回卷集]`、`Chapter \d+`、卷/番外处理、字数兜底；GBK→UTF-8）。

**DB 新表**：`media_provider`（全局厂商池：kind/name/api_url/api_key/model/extra_json/is_default，照 `model_provider` 结构，设置页「媒体服务」页签+连通性测试）；`comic_generation_task`（kind/status/provider_id/model/request/result/artifact/target/candidates_json，任务级快照+断线恢复）。

## MCP comic 工具组（约 23 个，description 即行为规范）

- 原文：`comic_set_source`（登记+分章+返回预览统计）、`comic_read_source`（章节区间/字数窗口，**强制限流**）、`comic_get_source_outline`
- 读取：`comic_get_project`（总览+状态机位置+next_action+provider 配置+has_source 提示）、`comic_list_assets`（分页）、`comic_get_asset`、`comic_search_assets`
- 写入：`comic_upsert_series`、`comic_upsert_episode`、`comic_write_script`、`comic_upsert_storyboard`（整集批量，schema 拒回照 `cli_run/output_schema.rs`）、`comic_upsert_shot`、`comic_upsert_character`（outfits+受影响镜头提示）/`comic_upsert_scene`/`comic_upsert_prop`
- 生成/画面：`comic_build_shot_prompt`（bible+角色卡@outfit+场景+道具+镜头+seed → 中立 prompt+参考图集，适配层转厂商格式）、`comic_request_generation`（count、provider 单次覆盖、超阈值强制确认）、`comic_present_candidates`（阻塞选择卡，照 `ask` 组长持连接+peer-close 撤单）、`comic_set_shot_image`、`comic_check_generation`（长轮询+错误分类）、`comic_cancel_generation`、`comic_attach_asset_image`、`comic_set_project_provider`
- 合成：`comic_compose_episode`（分辨率模板/竖屏 9:16 默认/转场/ken-burns/BGM/srt）、`comic_check_compose`

**纪律**：工具结果永不携带图片/视频二进制；列表分页；文本窗口限流。

**接线点**：① `companion.rs:225/280` features 加 `"comic"` + `build_tools_call_spawn` + `render_comic_*`；② 新 `acp/comic_tools.rs`：`ComicAccess` trait+Outcome（照 `browser_tools.rs` 值语义拒绝）；③ `listener.rs` `serve_one` 加 comic arm；④ `connection.rs:7854/7894` flags + 注入时**检测工作目录存在 `story/` 才启用 comic 组** + `DelegationInjection` comic runtime config（hot-swap+设置持久化+`COMIC_TOOLS_SETTINGS_CHANGED_EVENT`）；⑤ `lib.rs` 与 `bin/codeg_server.rs` 组装。

## 宿主侧领域模块

`src-tauri/src/comic/`：`paths.rs`（story 根，照 `codeg_*_root()`）、`model.rs`/`store.rs`（serde schema、**per-folder 写互斥、story/ 写入 jail（照 `upload_jail`）、slug、引用校验（shot→character@outfit/scene/prop、episode→chapters）、原子写**）、`source.rs`（登记/转码/分章/重切/窗口读取）、`task.rs`（入库→tokio worker 按厂商并发上限排队轮询→产物落盘→emit_event；失败分类）、`prompt.rs`（拼装器）、`provider/`（trait `kind()/submit()/poll()`；OpenAI 兼容 images、ComfyUI、可灵/即梦任务式视频、`audio/speech` TTS；HTTP 照 `acp/provider_models.rs`：短超时、响应上限、错误体洗 key）、`compose.rs`（ffmpeg 检测 PATH→无则引导指定路径（sidecar 打包列为后续）；逐镜静帧 ken-burns 或视频片段+转场+BGM 音轨+台词 srt→竖屏 1080x1920 mp4）。

产物治理：候选图清理策略（series.json 配置保留张数/天数）；存储占用在工作台任务面板显示。

前端数据 API 双模式：`commands/comic.rs`（`*_core`+`#[tauri::command]`）+ `web/handlers/comic.rs` + `web/router.rs`（含 provider 池 CRUD/连通性测试）。事件：`COMIC_CHANGED_EVENT="comic://changed"`，payload `{kind,id,action}` 照 `WorkTaskChange`。

## 前端工作台

- 三栏照 `workbench-content.tsx`/`aux-panel.tsx`；`story/` 存在即漫剧项目；新建向导含「改编来源」+「生成厂商」两步
- 左栏：原文（章节树）/剧集→分镜/角色/场景/道具
- 中栏：分镜表（候选缩略/选中态/状态机徽标/行内快捷按钮）、剧本（原文章节对照）、资产详情（三视图画廊+outfits）、生成任务面板（失败原因/厂商标注/provider 下拉/成本汇总）
- 右栏：复用 `conversation-detail-panel` + 主会话切换器；候选选择卡（多选/换一批/保留多张）
- 快捷操作 → 预置指令发主会话；复用 `image-preview`、`generated-images-block`、`markdown-local-image`、上传链路；新写：候选选择卡、原文阅读器、`video-player.tsx`、`stores/comic-store.ts`+`comic://changed`；i18n 10 语言 `comic.*`

## 里程碑（垂直切片）

- **M1 骨架**：story 模型/校验/写互斥/jail、原文登记+分章+窗口读取、`media_provider` 表+设置页基础、comic 工具组（原文+资源读写+检索+provider 查询/切换）、`story/` 条件注入、comic://changed、工作台三栏骨架（向导两步）、主会话绑定与快捷指令发送、端到端
- **M2 分镜与资产**：分镜表编辑、剧本编辑（原文对照）、资产库（三视图+outfits）、编排工具补全、资产更新联动
- **M3 生图与候选选择**：provider 抽象+OpenAI 兼容+ComfyUI、`comic_build_shot_prompt`、N 候选+选择卡、成本护栏+失败分类、逐镜/批量出图、任务面板
- **M4 视频/TTS/合成**：可灵/即梦视频 provider、TTS、逐镜视频（首尾帧）、ffmpeg 检测+合成导出、视频播放器

## 验证

- 后端：`cargo check`；`cargo clippy -D warnings`（默认/`server-bin`/`mcp-bin` 三套）；`cargo test --features test-utils`
- 前端：`pnpm lint` / `pnpm test` / `pnpm build`
- 端到端：新建项目（选厂商）→ 无原文引导 → 拖入 txt 分章确认 → 「把 1-3 章改编成 2 集」→ 工具链观察 → 双厂商出 4 候选 → 会话卡点选 → 「换 ComfyUI」验证指针/单次覆盖 → 超阈值触发规模确认 → 审核拒绝错误分类呈现 → 分镜行重生成快捷指令 → 图生视频 → 合成竖屏 mp4 → 断会话重开验证断点续作
- 单测：分章正则（真实网文 fixture）/转码/窗口边界、store 引用校验+jail+并发写、指针解析（id 优先名称兜底/缺失拒绝）、prompt 拼装、分镜 schema 拒回、provider 轮询状态机+失败分类（mock HTTP）、选择卡阻塞/取消语义、成本阈值拦截、companion 分发与条件注入

## 关键文件清单

**新建**：`src-tauri/src/comic/{mod,paths,model,store,source,task,prompt,compose}.rs`、`comic/provider/*`、`acp/comic_tools.rs`、`commands/comic.rs`、`web/handlers/comic.rs`、`db/entities/{media_provider,comic_generation_task}.rs`+migration、`src/stores/comic-store.ts`、`src/components/comic/*`（工作台三栏/新建向导/原文阅读器/资源树/分镜表/候选选择卡/任务面板/视频播放器）
**修改**：`tool_schema.json`、`delegation/companion.rs`、`delegation/listener.rs`、`acp/connection.rs`（含 story/ 条件注入）、`web/event_bridge.rs`、`web/router.rs`、`lib.rs` 与 `bin/codeg_server.rs`、`db/migration/mod.rs`、`lib/api.ts`+`lib/types.ts`、`i18n/messages/*`、设置页（媒体服务）