# 漫剧工作台可用流程修复计划

目标：在 main 修复创建时错误对象提示、原创项目误读小说、缺少第一集/分镜操作入口、会话产出无法落入工作台的问题，支持整部剧集的多个分集管理。用户已要求继续完成此前漫剧工作，不发布 CI。

## 已核实的现状

- story/series.json + episodes/<id> 已支持整剧多集，前端只是把分集混在资源下拉中，缺少创建分集及导入会话产出的操作。
- 工作台多处 String(err) 丢失 AppCommandError.message/detail，出现 [object Object]。
- 原创 source.mode=original 仍 has_source=true，页面误请求不存在的 chapters.json/novel。
- write_script 不保证 episode.json 存在；upsert/replace storyboard 按 id 排序会覆盖用户顺序。
- 已有 comic_* MCP → token 工作目录 → 共享 core → story 原子写入 → comic://changed → UI refetch；无需新建另一套 MCP。
- 当前 comic MCP 只在会话启动时 story/ 存在才启用，项目初始化后原会话没有工具，缺少引导 skill。
- 首页 Office 大卡/小卡本质为 skill 引用和提示词注入，不是普通网页链接。

## 实施约定

1. **后端一致性**：保持现有文件模型、锁和事件；写入剧本/分镜前补齐分集元数据，读取旧目录可恢复。原创项目与小说项目分流；修改镜头保留手动顺序，替换分镜保留输入顺序。
2. **工作台操作**：显式分集管理区（列表/切换、新建、编辑标题）；创建时可粘贴已写好的剧本；支持导入结构化分镜并显示解析错误。新建分集只写本地文件，不调用模型。完整剧集仍由一个项目目录管理，不重建已有项目。
3. **skill 入口**：依托 bundled experts 增加 comic-production skill；在“日常办公”区域提供显眼的漫剧入口，相关设置跳转 Experts 而非 OfficeCLI。添加入口只准备会话/打开工作台，不自动生成素材。
4. **MCP 自举**：项目会话具备漫剧发现与显式初始化能力，工具调用仍严格限于 token 绑定工作目录；不存在项目时只有发现/初始化可执行，其余保持拒绝。会话初始化后的后续调用无需靠重启才发现工具。没有 MCP 注入能力的 agent 给出明确提示。
5. **工作流指导**：skill 串联 inspect/init → 整剧规划 → upsert episode → write script → cards → storyboard → 用户审核 → 明确授权后图片/视频/配音/合成。以落盘状态和 get_project/get_asset 回读作为完成依据，不把聊天文字当成已保存成果。

## 文件分工

- 后端：comic/store.rs 与测试、commands/comic.rs/shared core 和工具分派、delegation schema/feature 注入测试。
- 工作台：components/comic/*、stores/comic-store.ts、lib/comic.ts/相关导入解析辅助与测试。
- 入口/skill：experts/skills/comic-production/SKILL.md、experts.toml、quick-actions/composer shortcuts 和相关元数据；新增入口事件供工作台打开。
- 集成：i18n 十语言、文档、主会话桥接与回归验证。

## 验证重点

- 原创项目初始化后不读小说文件；失败显示后端真实错误，用户输入保留可重试。
- 空项目可创建第一集、切换第二集、修改标题/剧本，重新加载仍存在。
- MCP 先写剧本再读取分集成功；修改分镜不重排已有顺序。
- 既有会话在项目初始化后可调用已注入的 comic 工具；非项目/错误工作目录不能越权写别处。
- 工作台手动导入会话中的剧本/分镜后立即可见；AI 修改通过 comic://changed 同步。
- 入口不自动发消息/收费；付费生成保留现有显式动作与审核。
- 前端定向测试、类型检查/静态构建；Rust 缓存已按用户要求清理，必要编译统一串行在最终验证阶段进行并说明耗时。

## 实施结果

上述功能已在 main 工作区实现。复审另外补齐了创建分集的 create-only 保护、重命名与分镜导入的快照冲突检查、默认镜头时长/服装兼容，以及宿主工具策略对应的 MCP 令牌授权检查。

用户进一步明确：不应要求普通用户寻找或编写 JSON。主流程调整为「当前会话成果整理落盘 → 选择已保存剧本的分集 → 生成并保存分镜草稿 → 工作台审核编辑」，JSON 导入移入更多操作，仅作高级迁移/能力受限的备用入口。系统继续使用既有 story 文件规范，不引入第二种聊天解析格式。

自动保存主流程调整后，最终统一前端回归为 16 个文件 150 项通过；Rust 相关测试 55 项通过，包含真实 MCP stdio、FFmpeg 和 Windows 委托集成。TypeScript、静态导出构建、Windows 桌面 `cargo check --bin codeg -j2` 通过。临时 Rust target 验证后删除，日志与截图安装包保留。

操作方法与验证边界见 [comic-production-guide.md](comic-production-guide.md)。本机详细日志归档在被 Git 忽略的 `dist/local-validation/comic-workflow-repair/`，不包含编译产物。未调用付费媒体服务、未发布 CI。
