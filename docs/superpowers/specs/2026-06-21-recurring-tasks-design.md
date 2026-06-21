# 可重复执行任务设计

## 背景

FlowSpace 当前任务模型主要靠 `horizon` 区分本周任务和长期任务，靠 `planned_date` / `due` 把任务放进今日流和日历。这个模型适合“一次性完成”的任务，也能表示长期目标，但不适合“一段时间内反复执行”的任务。

典型例子：

- 每天背 50 个单词，持续 60 天。
- 每周一、三、五跑步。
- 每月 1 号整理预算。
- Roadmap 节点下，每天完成一次 Anki 复习。

这些任务不应该被复制成很多条普通 `tasks` 行，否则后续修改标题、调整项目、暂停、统计完成率都会变得混乱。

## 目标

1. 增加任务类型：单次任务和可重复执行任务。
2. 可重复任务在指定日期范围内按规则出现在今日、任务页和日历中。
3. 完成可重复任务时，只完成某一天的执行实例，不把整个任务模板关闭。
4. Roadmap 节点可以创建关联的可重复学习任务。
5. 每日总结能统计重复任务的实际完成情况。
6. SQLite 和 PostgreSQL storage provider 行为保持一致。

## 非目标

第一版不实现复杂日程规则：

- 不支持完整 RRULE。
- 不支持“每月最后一个工作日”。
- 不支持节假日自动跳过。
- 不支持一天内多个提醒时段。
- 不做习惯连续天数、成就徽章等扩展统计。

这些能力可以在当前模型上继续扩展，但不进入第一版。

## 核心模型

采用“任务模板 + 执行实例”的模型。

`tasks` 表继续表示任务模板。新增字段：

```text
execution_type TEXT NOT NULL DEFAULT 'single'
  CHECK (execution_type IN ('single', 'recurring'))
```

含义：

- `single`：现有普通任务。
- `recurring`：重复任务模板。

新增 `task_recurrence_rules` 表保存重复规则。

```sql
CREATE TABLE task_recurrence_rules (
  task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
  start_date DATE NOT NULL,
  end_date DATE,
  frequency TEXT NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly')),
  interval INTEGER NOT NULL DEFAULT 1 CHECK (interval >= 1),
  weekdays INTEGER[] NOT NULL DEFAULT '{}',
  month_days INTEGER[] NOT NULL DEFAULT '{}',
  timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (end_date IS NULL OR end_date >= start_date)
);
```

SQLite 中 `weekdays` / `month_days` 使用 JSON array 字符串保存，并由 repository 层统一解析。

新增 `task_occurrences` 表保存某一天的执行状态。

```sql
CREATE TABLE task_occurrences (
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  occurrence_date DATE NOT NULL,
  status TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'done', 'skipped')),
  completed_at TIMESTAMPTZ,
  note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (task_id, occurrence_date),
  CHECK (
    (status = 'done' AND completed_at IS NOT NULL)
    OR (status <> 'done')
  )
);
```

实例行只在用户完成、跳过、恢复或写备注时落库。今日和日历查询会根据规则临时展开应出现的日期，再左连接已有 occurrence 状态。

## 规则语义

### 每天

```json
{
  "frequency": "daily",
  "interval": 1,
  "start_date": "2026-06-21",
  "end_date": "2026-08-21"
}
```

从 `start_date` 开始，每 1 天出现一次，包含 `start_date` 和 `end_date`。

### 每 N 天

```json
{
  "frequency": "daily",
  "interval": 2,
  "start_date": "2026-06-21"
}
```

从开始日期起每 2 天出现一次。

### 每周指定星期

```json
{
  "frequency": "weekly",
  "interval": 1,
  "weekdays": [1, 3, 5],
  "start_date": "2026-06-21"
}
```

`weekdays` 使用 ISO weekday：周一是 1，周日是 7。`interval=2` 表示隔周。

### 每月指定日期

```json
{
  "frequency": "monthly",
  "interval": 1,
  "month_days": [1, 15],
  "start_date": "2026-06-21"
}
```

每月 1 号和 15 号出现。若某月没有对应日期，例如 2 月 30 日，则跳过该日期。

## 后端 API

### 创建任务

扩展现有 `POST /api/tasks`。

普通任务：

```json
{
  "title": "写迁移方案",
  "execution_type": "single",
  "planned_date": "2026-06-21",
  "horizon": "week",
  "scope": "daily"
}
```

重复任务：

```json
{
  "title": "背 50 个单词",
  "content": "复习 Anki，记录错词",
  "project_id": "learning-1",
  "execution_type": "recurring",
  "horizon": "week",
  "scope": "daily",
  "recurrence": {
    "start_date": "2026-06-21",
    "end_date": "2026-08-21",
    "frequency": "daily",
    "interval": 1,
    "weekdays": [],
    "month_days": [],
    "timezone": "Asia/Shanghai",
    "enabled": true
  }
}
```

校验规则：

- `execution_type=single` 时忽略 `recurrence`。
- `execution_type=recurring` 时必须提供 `recurrence.start_date`、`frequency`、`interval`。
- `weekly` 必须提供非空 `weekdays`。
- `monthly` 必须提供非空 `month_days`。
- `weekdays` 只能是 1 到 7。
- `month_days` 只能是 1 到 31。
- `end_date` 不得早于 `start_date`。

### 更新任务

扩展现有 `PATCH /api/tasks/:id`。

允许更新：

- 标题、内容、项目、Roadmap 节点关联。
- 重复规则。
- `recurrence.enabled`。
- `recurrence.end_date`。

限制：

- `single` 可以切换为 `recurring`，但必须同时提交合法 recurrence。
- 第一版禁止 `recurring -> single`，返回 409，并提示用户通过设置 `end_date` 或关闭 `enabled` 结束重复任务。这样可以避免已完成 occurrence 与模板删除之间的数据一致性问题。

### 查询任务

扩展现有 `GET /api/tasks`。

新增参数：

```text
execution_type=single|recurring
from=2026-06-01
to=2026-06-30
include_occurrences=true|false
```

行为：

- 默认兼容旧行为：返回 `tasks` 模板行。
- `execution_type=recurring` 返回重复任务模板。
- `include_occurrences=true` 且提供 `from/to` 时，额外返回范围内展开后的 occurrences。

第一版前端任务页可以只用模板列表管理重复任务，今日页和日历使用专门 occurrence API。

### 日期范围实例

新增：

```http
GET /api/task-occurrences?from=2026-06-01&to=2026-06-30
```

响应：

```json
{
  "occurrences": [
    {
      "task_id": "task-1",
      "occurrence_date": "2026-06-21",
      "status": "open",
      "completed_at": null,
      "title": "背 50 个单词",
      "content": "复习 Anki，记录错词",
      "project_id": "learning-1",
      "project": "日语N2学习",
      "roadmap_node_id": "node-1",
      "recurrence_label": "每天"
    }
  ]
}
```

日期范围最大允许 370 天，防止一次展开过多。

### 完成、恢复、跳过实例

新增：

```http
POST /api/tasks/:id/occurrences/:date/complete
POST /api/tasks/:id/occurrences/:date/reopen
POST /api/tasks/:id/occurrences/:date/skip
```

行为：

- `complete`：upsert occurrence，状态为 `done`，写入 `completed_at`。
- `reopen`：upsert occurrence，状态为 `open`，清空 `completed_at`。
- `skip`：upsert occurrence，状态为 `skipped`，清空 `completed_at`。
- 若 task 不是 `recurring`，返回 400。
- 若日期不在规则范围内，返回 400。
- 若规则已 disabled，返回 409。

## 今日任务流

`GET /api/today` 继续返回 `todayTasks` 和 `overdueTasks`。

变化：

- `todayTasks` 增加当天应执行的 recurring occurrence。
- 重复任务不进入 `overdueTasks`，第一版只关心当天执行。是否补做过期重复任务以后再设计，避免今日流被历史重复任务刷屏。
- 今日流里重复任务项使用稳定 key：`task_id + occurrence_date`。

重复任务返回字段示例：

```json
{
  "id": "task-1",
  "title": "背 50 个单词",
  "execution_type": "recurring",
  "occurrence_date": "2026-06-21",
  "occurrence_status": "open",
  "recurrence_label": "每天",
  "done": 0,
  "planned_date": "2026-06-21"
}
```

前端勾选时：

- `execution_type=single`：继续 `PATCH /api/tasks/:id`。
- `execution_type=recurring`：调用 occurrence complete/reopen API。

## 任务页 UX

任务页新增一个任务类型维度：

- 单次任务。
- 可重复任务。

第一版新增一个 `重复任务` tab，和现有 `本周`、`长期任务`、`学习 Roadmap` 平级。

### 重复任务 tab

用于管理模板：

- 标题。
- 项目。
- 内容。
- 开始日期。
- 结束日期。
- 频率。
- 每周几或每月几号。
- 启用 / 暂停。

列表显示：

- 任务标题。
- 项目标签。
- 重复规则标签，例如 `每天`、`每 2 天`、`每周一三五`、`每月 1/15 日`。
- 有效期，例如 `2026-06-21 至 2026-08-21`。
- 今日状态：今日已完成、今日未完成、今日不执行。

### 本周任务 tab

本周视图显示本周范围内展开的重复任务实例，按日期分组。实例使用 `task_id + occurrence_date` 作为前端 key，勾选时调用 occurrence 完成/恢复接口。

### 长期任务 tab

长期任务不混入重复任务。长期目标和重复动作概念不同，重复动作应通过独立 tab 管理。

## Roadmap 关联任务

Roadmap 节点详情里的“新增关联学习任务”增加类型选择：

- 单次推进。
- 可重复练习。

当选择“可重复练习”时显示重复规则字段，创建 task 时写入：

```json
{
  "execution_type": "recurring",
  "project_id": "<当前学习项目>",
  "roadmap_node_id": "<当前节点>",
  "recurrence": { ... }
}
```

节点进度第一版分开展示：

- 单次关联任务：按 `done/status` 统计完成数。
- 重复练习：显示近 7 天应执行次数、已完成次数和跳过次数。

不把两类任务强行折算成一个总百分比，避免“一个长期重复练习”压过普通节点任务。

## 日历页

日历页通过 `GET /api/task-occurrences?from=<monthStart>&to=<monthEnd>` 获取当月重复任务实例。

展示：

- 月视图某天有重复任务时增加 dot。
- 选中某天后，右侧 inspector 显示该日重复任务。
- 勾选重复任务只完成选中日期 occurrence。

现有 `/api/today` 仍用于今日 inspector。非今日日期使用 occurrences API。

## 每日总结

每日总结统计需要合并：

- 普通任务：`tasks.completed_at` 落在范围内。
- 重复任务：`task_occurrences.completed_at` 落在范围内。

展示时标记：

```text
重复任务 · 背 50 个单词 · 2026-06-21
```

这样重复任务不会污染普通 task 完成记录，但会进入真实完成统计。

## 搜索

搜索任务模板，不搜索每一天 occurrence。

原因：

- 用户搜索“背单词”通常想找到这个任务模板。
- 如果搜索 occurrence，结果会出现几十条重复项。

搜索结果里增加标签：

```text
重复任务
```

搜索索引维护：

- `tasks.execution_type` 加入结果 metadata。
- `task_recurrence_rules` 不单独进入全文索引。

## 存储与迁移

### PostgreSQL

新增迁移：

- 给 `tasks` 增加 `execution_type`。
- 创建 `task_recurrence_rules`。
- 创建 `task_occurrences`。
- 为今日和日历查询建索引。

建议索引：

```sql
CREATE INDEX task_recurrence_rules_enabled_idx
  ON task_recurrence_rules (enabled, start_date, end_date);

CREATE INDEX task_occurrences_date_idx
  ON task_occurrences (occurrence_date, status);

CREATE INDEX task_occurrences_task_date_idx
  ON task_occurrences (task_id, occurrence_date);
```

### SQLite

同步新增表和字段。

SQLite 中：

- `execution_type` 使用 TEXT CHECK。
- `weekdays`、`month_days` 使用 JSON array 字符串。
- 日期字段继续使用 `YYYY-MM-DD` 文本。

### SQLite -> PostgreSQL 迁移

迁移清单增加：

- `tasks.execution_type`。
- `task_recurrence_rules`。
- `task_occurrences`。

预检增加：

- `execution_type IN ('single','recurring')`。
- recurring task 必须有 recurrence rule。
- single task 不应有 recurrence rule。
- occurrence 日期必须在对应 rule 范围内。
- weekly weekdays 合法。
- monthly month_days 合法。

## Repository / Service 设计

新增领域模型：

```go
type RecurrenceRule struct {
	TaskID    string
	StartDate string
	EndDate   *string
	Frequency string
	Interval  int
	Weekdays  []int
	MonthDays []int
	Timezone  string
	Enabled   bool
}

type TaskOccurrence struct {
	TaskID         string
	OccurrenceDate string
	Status         string
	CompletedAt    *int64
	Note           string
	Task           Task
	RecurrenceLabel string
}
```

Storage contract 增加：

```go
type RecurrenceRepository interface {
	UpsertRule(ctx context.Context, rule *model.RecurrenceRule) error
	GetRule(ctx context.Context, taskID string) (*model.RecurrenceRule, error)
	DeleteRule(ctx context.Context, taskID string) error
	ListActiveRules(ctx context.Context, from, to string) ([]model.RecurrenceRule, error)
	ListOccurrences(ctx context.Context, from, to string) ([]model.TaskOccurrence, error)
	CompleteOccurrence(ctx context.Context, taskID, date string, completedAt int64) (*model.TaskOccurrence, error)
	ReopenOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error)
	SkipOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error)
}
```

Service 层负责：

- 校验 recurrence request。
- 生成日期范围内的 expected occurrences。
- 合并数据库中已有 occurrence 状态。
- 生成中文 `recurrence_label`。
- 今日流和日历使用同一套展开逻辑。

## 错误处理

用户可见错误：

- 重复任务缺少开始日期：`请选择开始日期`。
- 每周重复但没选星期：`请选择每周执行的日期`。
- 结束日期早于开始日期：`结束日期不能早于开始日期`。
- 日期范围过大：`最多一次查看 370 天的重复任务`。
- 已暂停的重复任务不能完成：`这个重复任务已暂停`。

后端返回：

- 400：请求字段非法。
- 404：任务不存在。
- 409：任务状态不允许当前操作。
- 500：存储或未知错误。

## 测试计划

### 后端单元测试

1. `ExpandDailyRuleIncludesStartAndEnd`
2. `ExpandEveryTwoDaysUsesStartDateAsAnchor`
3. `ExpandWeeklyRuleUsesISOWeekdays`
4. `ExpandMonthlyRuleSkipsMissingMonthDay`
5. `CompleteOccurrenceDoesNotMarkTaskDone`
6. `TodayIncludesRecurringOccurrenceForToday`
7. `TodayDoesNotIncludeRecurringOccurrenceWhenRuleDisabled`
8. `RecurringOccurrencesDoNotBecomeOverdueByDefault`

### Storage contract tests

在 SQLite 和 PostgreSQL provider 上都跑：

1. 创建 recurring task 和 rule 后能 round-trip。
2. occurrence complete/reopen/skip upsert 行为一致。
3. `ListOccurrences(from,to)` 合并 task/project/roadmap metadata。
4. 删除 task 后 cascade 删除 rule 和 occurrences。

### 前端测试

1. 今日页显示重复任务标签并调用 occurrence complete API。
2. 任务页重复任务 tab 可以创建 daily rule。
3. 任务页 weekly rule 未选星期时禁用提交。
4. 日历选中某天显示该日重复任务。
5. Roadmap 节点创建可重复练习时携带 `roadmap_node_id`。
6. 每日总结渲染重复任务完成记录。

### E2E 验证

1. 创建“每天背单词”重复任务。
2. 回到今日页，确认任务出现。
3. 勾选完成，刷新后仍保持当天已完成。
4. 打开日历，确认今天有重复任务标识。
5. 进入每日总结，确认完成记录出现。

## 分阶段落地

### Phase 1: 数据模型与后端展开

- 新增 schema。
- 新增 model。
- 新增 recurrence service。
- 后端测试先覆盖规则展开和 occurrence 状态。

### Phase 2: 今日流

- `/api/today` 合并今日重复任务。
- `TaskRow` 支持 `execution_type` 和 occurrence 完成动作。
- 今日页、日历今日 inspector 复用。

### Phase 3: 任务页管理

- 增加重复任务 tab。
- 支持创建、暂停、编辑规则。
- 列表显示规则标签和今日状态。

### Phase 4: 日历

- 日历按月加载 occurrences。
- 非今日日期 inspector 显示当天重复任务。

### Phase 5: Roadmap 与总结

- Roadmap 节点关联任务支持重复类型。
- 每日总结纳入 occurrence 完成记录。
- 搜索结果标记重复任务模板。

## 决策记录

1. 不把重复任务复制成普通任务行。
2. 不复用 `scope` 表达重复规则。
3. 不让重复任务进入逾期列表，第一版只显示当天应执行项。
4. 搜索任务模板，不搜索 occurrence。
5. 第一版不支持复杂 RRULE，先覆盖 daily / weekly / monthly。
