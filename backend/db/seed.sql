INSERT OR IGNORE INTO folders (id, name, sort_order, created_at) VALUES
  ('__uncategorized', '未分类', 0, unixepoch()),
  ('__work', '工作', 1, unixepoch()),
  ('__personal', '个人', 2, unixepoch());

-- 10 notes
INSERT OR IGNORE INTO notes (id, title, body, folder_id, tags, created_at, updated_at) VALUES
  ('n01', '桌面端架构设计', 'Tiptap 自定义节点的能力是关联引擎的 UI 基础，需要优先验证…', '__work', '["技术","架构"]', unixepoch(), unixepoch()),
  ('n02', '产品规划文档', 'MVP 阶段聚焦快速捕获、Markdown 编辑、智能关联三个核心…', '__work', '["产品","规划"]', unixepoch()-86400, unixepoch()-86400),
  ('n03', '周报 5月第三周', '本周完成了设计规范合并和组件库初版，下周启动原型开发…', '__work', '["周报"]', unixepoch()-172800, unixepoch()-172800),
  ('n04', '读书笔记：深度工作', 'Cal Newport 提出的深度工作概念与 FlowSpace 的产品理念高度契合…', '__personal', '["阅读","笔记"]', unixepoch()-259200, unixepoch()-259200),
  ('n05', 'CalDAV 集成调研', '评估 iCloud、Google Calendar 的 CalDAV 接入方案…', '__work', '["技术","调研"]', unixepoch()-345600, unixepoch()-345600),
  ('n06', '用户访谈记录 #3', '与 3 位自由职业者进行了 45 分钟的一对一访谈…', '__work', '["用户研究","访谈"]', unixepoch()-432000, unixepoch()-432000),
  ('n07', 'React 19 迁移笔记', 'React 19 的 use() hook 和 actions API 可以简化数据获取流程…', '__work', '["技术","前端"]', unixepoch()-518400, unixepoch()-518400),
  ('n08', '个人年度目标', '2026 年目标：完成 FlowSpace v1、读 24 本书、跑一次马拉松…', '__personal', '["规划","个人"]', unixepoch()-604800, unixepoch()-604800),
  ('n09', 'SQLite FTS5 使用笔记', 'FTS5 的 content= 模式要求源表必须有 INTEGER rowid…', '__work', '["技术","数据库"]', unixepoch()-691200, unixepoch()-691200),
  ('n10', 'Go Gin 中间件最佳实践', 'Logger→Recovery→CORS 是 Gin 推荐的中间件链…', '__work', '["技术","后端"]', unixepoch()-777600, unixepoch()-777600);

-- 10 tasks
INSERT OR IGNORE INTO tasks (id, title, project, due, priority, done, scope, sort_order, created_at, updated_at) VALUES
  ('t01', '完成 Tiptap 原型验证', '项目A', unixepoch('2026-05-30'), 1, 0, 'daily', 0, unixepoch(), unixepoch()),
  ('t02', '审查桌面端架构文档', '技术', unixepoch('2026-05-30'), 0, 0, 'daily', 1, unixepoch(), unixepoch()),
  ('t03', '整理本周笔记', '个人', unixepoch('2026-05-29'), 0, 1, 'daily', 2, unixepoch(), unixepoch()),
  ('t04', '准备周五评审材料', '工作', unixepoch('2026-06-15'), 0, 0, 'monthly', 3, unixepoch(), unixepoch()),
  ('t05', '更新产品规划文档', '项目A', unixepoch('2026-06-20'), 0, 1, 'monthly', 4, unixepoch(), unixepoch()),
  ('t06', '完成本地优先数据层设计', '技术', unixepoch('2026-09-30'), 1, 0, 'yearly', 5, unixepoch(), unixepoch()),
  ('t07', '实现 Quick Capture 接口', '技术', unixepoch('2026-05-31'), 1, 0, 'daily', 6, unixepoch(), unixepoch()),
  ('t08', '编写 API 文档', '项目A', unixepoch('2026-05-22'), 0, 0, 'daily', 7, unixepoch(), unixepoch()),
  ('t09', '数据库 schema 评审', '技术', unixepoch('2026-05-23'), 0, 0, 'daily', 8, unixepoch(), unixepoch()),
  ('t10', '前端路由设计', '项目A', unixepoch('2026-05-24'), 1, 0, 'daily', 9, unixepoch(), unixepoch());

-- 5 events
INSERT OR IGNORE INTO events (id, title, start_time, end_time, location, kind, created_at, updated_at) VALUES
  ('e01', '产品评审会', unixepoch('2026-05-30 10:00:00'), unixepoch('2026-05-30 11:00:00'), '会议室 3F', 'work', unixepoch(), unixepoch()),
  ('e02', '团队周会', unixepoch('2026-05-30 14:00:00'), unixepoch('2026-05-30 14:30:00'), NULL, 'reminder', unixepoch(), unixepoch()),
  ('e03', '夜跑', unixepoch('2026-05-30 19:00:00'), unixepoch('2026-05-30 20:00:00'), NULL, 'personal', unixepoch(), unixepoch()),
  ('e04', '技术分享会', unixepoch('2026-05-20 15:00:00'), unixepoch('2026-06-05 16:00:00'), '线上', 'work', unixepoch(), unixepoch()),
  ('e05', '周末 hiking', unixepoch('2026-06-01 08:00:00'), unixepoch('2026-06-01 17:00:00'), '森林公园', 'personal', unixepoch(), unixepoch());

-- 3 inbox items
INSERT OR IGNORE INTO inbox (id, kind, title, body, source, created_at, updated_at) VALUES
  ('i01', 'task', '调研 CalDAV 集成方案', '评估 iCloud/Google Calendar 的 CalDAV 接入复杂度', 'quick-capture', unixepoch()-600, unixepoch()-600),
  ('i02', 'note', '会议记录 — 产品方向讨论', '讨论了 v1.1 日历视图的交互方案和拖拽调整时间的实现…', '编辑器', unixepoch()-3600, unixepoch()-3600),
  ('i03', 'event', '周五团队午餐', NULL, 'quick-capture', unixepoch()-86400, unixepoch()-86400);
