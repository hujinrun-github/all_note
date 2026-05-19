## Variant: 结构风 (Structured Light)

### Design stance
Notion 的美学 + 三栏结构化仪表盘。笔记、任务、日历三个模块在首屏同时可见，不需要切换页面。底部浮动快捷栏提供品类入口。

### Key choices
- **Layout:** 12 列网格 — 任务 5 列 | 日历 4 列 | 笔记 3 列。信息密度更高，一屏看全
- **Typography:** 同 Notion 系统字体，但层级更分明（h1 26px, 正文 13-15px）
- **Color:** Notion 色板基础上加入语义色 — 高优任务琥珀色 #f5a623，完成绿色 #0f9d58
- **Interaction:** 卡片 hover 微阴影 + 边框变深；任务 checkbox 点击完成动画
- **Mini Calendar:** 右上角嵌入小型月历，有事件的天显示小圆点
- **Quick Bar:** 底部浮动条 — 一键跳转创建笔记/任务/事件，减少菜单层级

### Trade-offs
- **Strong at:** 三合一"仪表盘感"强，适合每天打开看全局的用户；信息架构清晰
- **Weak at:** 空间紧凑，笔记区只能看到摘要，不适合大量笔记浏览；移动端适配需重构

### Best for
- 以任务和日程为核心的用户，笔记作为知识沉淀。工作流以"今天要做什么"为起点
