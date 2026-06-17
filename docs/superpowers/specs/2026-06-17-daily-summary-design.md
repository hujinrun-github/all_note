# 每日总结页面设计

**日期**: 2026-06-17  
**分支**: feat/note-project-links  
**目标**: 提供一个按周/月回顾已完成任务的汇总页面，支持查看任务详情、关联项目和笔记。

---

## 需求摘要

- 侧边栏新增「每日总结」入口
- 默认展示本周已完成的全部任务，按日期分组
- 用户可通过日期选择器切换时间范围
- 点击任务查看项目信息、跳转关联笔记
- 顶部展示统计摘要：完成数、活跃天数、参与项目数

---

## 架构总览

```
新增页面路由
  /summary → DailySummary.tsx

前端改动（5 个文件）
  ├── router.tsx            — 新增 lazy route
  ├── Sidebar.tsx           — 新增导航项 + SummaryIcon SVG
  ├── TopBar.tsx            — 新增 pageMeta['/summary']
  ├── DailySummary.tsx      — 核心页面组件（新建）
  └── index.css             — 新增 .summary-grid 等样式

后端改动（6 个文件）
  ├── migration             — 新增 tasks.completed_at INTEGER 列
  ├── model/task.go         — 新增 CompletedAt *int64 字段
  ├── handler/tasks.go      — UpdateTask 在 done 切换时设/清 completed_at
  ├── handler/summary.go    — 新建，GET /api/summary
  ├── service/summary.go    — 新建，查询+分组逻辑
  └── router/router.go      — 注册 /api/summary 路由

测试（2 个文件）
  ├── DailySummary.test.tsx — Vitest + testing-library 单元测试
  └── daily-summary.spec.ts — Playwright e2e 测试
```

---

## 后端设计

### 数据库迁移

```sql
ALTER TABLE tasks ADD COLUMN completed_at INTEGER;
-- NULL = 未完成；Unix 时间戳 = 被勾选完成的时间
```

### UpdateTask 逻辑

- 当 `done` 从 0→1 时：`completed_at = now()`
- 当 `done` 从 1→0（重新打开）时：`completed_at = NULL`

### `GET /api/summary`

```
GET /api/summary?from=2026-06-10&to=2026-06-17
```

返回 JSON:

```json
{
  "groups": [
    {
      "date": "2026-06-17",
      "tasks": [
        {
          "id": "xxx",
          "title": "完成 PR 审查",
          "project": { "id": "p1", "name": "FlowSpace", "type": "regular" },
          "linked_notes": [{ "id": "n1", "title": "review 笔记" }],
          "completed_at": 1718572800,
          "planned_date": "2026-06-16"
        }
      ],
      "count": 3
    }
  ],
  "total": 12,
  "range": { "from": "2026-06-10", "to": "2026-06-17" }
}
```

实现要点：
- 新建 `handler/summary.go` + `service/summary.go`
- 通过 `repository.GetCompletedTasksByRange(from, to)` 查询
- LEFT JOIN `task_projects` 和 `notes` 获取关联数据
- 按 `completed_at` 降序排列，service 层负责按日期分组

---

## 前端设计

### 布局

桌面端两栏布局（`grid-template-columns: 1fr 2fr`），移动端堆叠：

```
┌────────────────────────────────────────────────┐
│  统计卡片 (1/3)   │  已完成任务列表 (2/3)        │
│                    │  按 completed_at 日期分组   │
│  12 项已完成       │  📅 6/17                   │
│  5 天有产出        │  ├─ ✓ 任务标题 [项目badge]  │
│  3 个项目参与      │  │   📎 关联笔记 → 可跳转   │
│                    │  └─ ...                    │
│                    │  📅 6/16                   │
│                    │  └─ ...                    │
└────────────────────────────────────────────────┘
```

### 交互

| 交互 | 行为 |
|------|------|
| 日期选择器 | 预设「本周」范围，可点击自定义起止日期 |
| 任务卡片点击 | 展开/折叠，显示关联笔记列表（可跳转到 `/editor/:id`）和项目信息 |
| 项目 badge 点击 | 跳转到对应任务项目视图 |
| 统计卡片 | 纯展示，无交互 |

### 日期选择器

- 使用 HTML `<input type="date">` 起止日期双输入
- 页面加载时默认设置为本周一至今天
- 变更时触发 `useQuery` 重新请求 `/api/summary`

### 三态处理

| 状态 | 展示 |
|------|------|
| Loading | 骨架卡片（复用 Dashboard `SkeletonCol` 模式） |
| Error | "加载失败" + 重试按钮 |
| Empty | "这个时间段还没有完成的任务" 空状态提示 |
| Data | 统计卡片 + 分组任务列表 |

---

## 文件清单

### 新建文件

| 文件 | 说明 |
|------|------|
| `frontend/src/routes/DailySummary.tsx` | 核心页面组件 |
| `frontend/src/routes/DailySummary.test.tsx` | 组件单元测试 |
| `backend/internal/handler/summary.go` | `GET /api/summary` handler |
| `backend/internal/service/summary.go` | 查询和分组逻辑 |
| `e2e/daily-summary.spec.ts` | Playwright e2e 测试 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `frontend/src/router.tsx` | lazy import + `/summary` route |
| `frontend/src/components/layout/Sidebar.tsx` | 新增 navItem + SummaryIcon SVG |
| `frontend/src/components/layout/TopBar.tsx` | 新增 `'/summary'` pageMeta |
| `frontend/src/styles/index.css` | `.summary-grid`, `.summary-stat-card`, `.summary-task-group` |
| `backend/internal/model/task.go` | `CompletedAt *int64` 字段 |
| `backend/internal/handler/tasks.go` | UpdateTask 中处理 completed_at |
| `backend/internal/router/router.go` | 注册 `/api/summary` |
| `backend/internal/repository/tasks.go` | 新增 `GetCompletedTasksByRange` 方法 |

---

## 样式设计要点

- 复用现有设计系统变量：`--color-fs-surface`, `--color-fs-accent`, `--shadow-card`, `--radius-lg`
- 统计卡片使用 `.metric-tile` 模式，数字用等宽字体
- 任务列表按日期分组，每组有日期分隔线
- 已完成任务左侧绿色 checkmark（复用 `--color-fs-success`）
- 项目 badge 复用 `.task-project-tag` 样式
- 笔记链接用 accent 色文字 + hover 下划线

---

## 验证计划

1. **后端**：`go test ./internal/handler/... ./internal/service/...` 确认 summary handler 正常
2. **前端**：`npx vitest run` 确认 DailySummary 三态渲染正确
3. **E2E**：`npx playwright test daily-summary.spec.ts`
   - 导航到 `/summary`，验证默认展示本周数据
   - 切换日期范围，验证筛选生效
   - 点击任务展开笔记列表，点击笔记跳转到编辑器
   - 点击项目 badge 跳转到任务页面
