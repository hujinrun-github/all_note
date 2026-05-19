## Variant: 编辑风 (Editorial Light)

### Design stance
纯 Notion 编辑体验——把整个 App 当成一个排版精良的文档。仪表盘就是一篇自动聚合的"今日文档"。

### Key choices
- **Layout:** 窄内容区（740px），模仿 Notion 的阅读栏宽
- **Typography:** 系统字体栈，大标题 2.2rem，正文 15px，行高 1.8
- **Color:** Notion 配色 — bg #fbfbfa，surface #fff，accent #2383e2
- **Interaction:** 悬浮显示编辑工具条（editor-toolbar opacity trick），卡片 hover 微阴影
- **Link Panel:** 右侧浮出关联面板，类似 Notion 的反向链接面板

### Trade-offs
- **Strong at:** 沉浸式编辑、阅读体验、Notion 用户零学习成本
- **Weak at:** 任务/日历的"工具感"不够强，仪表盘信息密度偏低

### Best for
- 以笔记为中心的轻度用户，把任务和日历当作笔记的附属功能
