# FlowSpace / all_note

轻量 All-in-one 效率工具的产品预研仓库。当前阶段是产品定义、架构设计和 UI 原型验证，还不是可运行应用。

一句话定位：给不想被工具绑架的人，一个秒开、本地优先、笔记 + 任务 + 日历自然打通的日常效率中枢。

## 当前结论

Reddit 调研显示，目标用户的主要痛点不是“缺一个更强大的 Notion”，而是：

- 工具太碎：日程、任务、笔记、协作分散在不同产品里。
- 产品太重：Notion、Evernote、ClickUp 等被反复吐槽慢、臃肿、难维护。
- 订阅疲劳：用户更愿意接受买断、本地优先、可迁移的数据模型。
- AI 疲劳：用户接受有用的辅助，不接受强塞式 AI。

因此 v1 不追求大而全，先验证“快捕获 + 本地笔记任务 + 今日视图”的核心闭环。

## 仓库结构

```text
.
├── product-plan.md                 # 产品定位、MVP 范围、路线图、风险和推广策略
├── desktop-architecture.md         # Tauri/Rust/React 架构、数据模型、窗口和性能设计
├── Reddit/
│   ├── crawl_reddit.py             # Reddit 调研爬虫
│   ├── analyze.py                  # 调研数据分析脚本
│   ├── reddit_analysis.md          # 调研报告
│   └── reddit_raw.json             # 原始采集数据
└── sketches/
    ├── THEME.md                    # 合并后的 UI 设计规范
    ├── 001-notion-editorial/       # 编辑风静态原型
    └── 002-notion-structured/      # 结构风静态原型
```

## MVP 范围

v1 应该只包含能证明核心价值的功能：

- Quick Capture：全局快捷键唤起 mini 窗口，快速写入笔记、任务或日程意图。
- 笔记：Markdown 编辑、标题/正文、标签、最近笔记列表。
- 任务：收件箱、今日任务、完成状态、优先级。
- 今日视图：今日任务、今日日程占位、最近笔记聚合。
- 智能关联：笔记和任务的手动关联、从笔记中提取待办。
- 本地存储：Markdown 文件作为真实源，SQLite 作为索引和查询缓存。

明确延后：Web 端、iOS、云同步、多人协作、知识地图、完整插件系统、完整日历视图。

## 技术方向

- 桌面壳：Tauri v2
- 后端核心：Rust
- 前端：React + TypeScript + Tailwind CSS
- 编辑器：Tiptap
- 状态管理：Zustand
- 本地数据：Markdown + SQLite
- 搜索：v1 先用 SQLite FTS5，Tantivy 作为 Phase 0/1.1 验证项

## 下一步

Phase 0 的目标是做技术 spike，而不是继续写更多规划：

1. 创建 Tauri + React 最小工程。
2. 验证冷启动时间，release build 取 10 次中位数。
3. 做 Quick Capture 独立窗口，验证全局快捷键唤起和写入反馈。
4. 打通 SQLite + Markdown 的创建、编辑、删除、重建索引。
5. 在 Tiptap 中实现一个最小 `TaskNode`，验证编辑器内任务状态同步。
6. 用 1000 篇中文 Markdown 对比 SQLite FTS5 和 Tantivy 的搜索效果。

通过 Phase 0 后，再进入桌面 MVP 实现。
