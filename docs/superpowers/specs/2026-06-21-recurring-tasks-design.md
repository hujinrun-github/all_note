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

- 第一版**不支持补做逾期重复任务**。重复任务不进入 `overdueTasks`，用户不能通过 occurrence API 完成一个早于今天的 expected date（除非该 date 是当天的 expected occurrence）。是否/如何补做过期重复任务留到后续设计。
- 第一版**不暴露 `task_occurrences.note` 字段的 API 和 UI**。note 列保留在 schema 中供后续扩展，但创建/编辑/展示路径均不涉及。

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
  timezone TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (end_date IS NULL OR end_date >= start_date)
);
```

SQLite 中 `weekdays` / `month_days` 使用 JSON array 字符串保存，并由 repository 层统一解析。JSON 格式为 `[1,3,5]`（标准 JSON number array，无空格）。PG 端使用 `INTEGER[]`（PostgreSQL 原生数组类型）。Repository 层统一转为 Go `[]int`，屏蔽两个 provider 的存储差异。

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

**`completed_at` 存储类型：** `task_occurrences.completed_at` 的物理存储因 provider 不同：
- PostgreSQL：`TIMESTAMPTZ`。
- SQLite：`INTEGER`（Unix 时间戳，秒），与现有 `tasks.completed_at` 一致。
- Repository 层统一将 `completed_at` 转换为 Go `int64`（Unix 秒），`TaskOccurrence.CompletedAt *int64` 与 `TaskSummary.CompletedAt *int64` 类型一致。Service 层不感知 provider 差异。

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

**interval > 1 的锚点定义（重要）：** 展开以 `start_date` 所在的 ISO 周为第 0 周，然后每 `interval` 周重复。ISO 8601 周定义：周一开始，周日结束。`start_date` 所在的周始终是第 0 周（无论 `start_date` 是周几），下一轮从第 `interval` 周开始。

举例：
- `start_date=2026-06-20`（周六），`weekdays=[1]`（周一），`interval=2`（隔周）。
  - 2026-06-20 所在 ISO 周是 2026-W25（周一开始于 6 月 15 日）。
  - 第 0 周（W25）的周一：6 月 15 日 → 但 `start_date` 是 6 月 20 日，6 月 15 日在 start 之前，跳过。
  - 第 2 周（W27）的周一：6 月 29 日 ✓。
  - 第 4 周（W29）的周一：7 月 13 日 ✓。
- `start_date=2026-06-22`（周一），`weekdays=[1,3,5]`，`interval=2`。
  - 2026-06-22 所在 ISO 周是 W26（6 月 22 日是周一，即该周第一天）。
  - 第 0 周（W26）：周一（6/22）✓、周三（6/24）✓、周五（6/26）✓。
  - 第 2 周（W28）：周一（7/6）✓、周三（7/8）✓、周五（7/10）✓。

两个 provider 的展开算法必须使用相同的 ISO 周计数逻辑，通过共享测试用例（见"测试计划"）保证对齐。

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

**特殊情况：**
- `month_days=[31]`：只有 31 天的月份（1、3、5、7、8、10、12 月）出现。30 天的月份（4、6、9、11 月）和 2 月跳过。
- `month_days=[29]`：非闰年 2 月跳过；闰年 2 月 29 日出现。其他月份正常按 29 号处理。
- `month_days=[28]`：所有月份都有 28 号，正常出现。
- 展开算法实现：在目标月份中检查 day 是否 ≤ 该月天数，若大于则跳过该月。闰年判断使用标准规则（能被 4 整除但不能被 100 整除，或能被 400 整除）。

### recurrence_label 生成规则

`recurrence_label` 用于 UI 展示和搜索索引，由 Service 层统一生成，确保两个 provider 行为一致。生成规则表：

| frequency | interval | weekdays / month_days | end_date | label 示例 |
|-----------|----------|----------------------|----------|-----------|
| daily | 1 | — | 有 | `每天` |
| daily | 1 | — | NULL | `每天（长期）` |
| daily | 2 | — | 有 | `每 2 天` |
| daily | N | — | * | `每 N 天` |
| weekly | 1 | [1,3,5] | * | `每周一三五` |
| weekly | 1 | [1,2,3,4,5] | * | `每周一至五`（连续 3+ 天压缩为 `起止`） |
| weekly | 1 | [6,7] | * | `每周六日` |
| weekly | 1 | [1] | * | `每周一` |
| weekly | 2 | [1,3,5] | * | `隔周周一三五` |
| weekly | 2 | [1] | * | `隔周周一` |
| weekly | N (N>1) | * | * | `每 N 周...` |
| monthly | 1 | [1] | * | `每月 1 号` |
| monthly | 1 | [1, 15] | * | `每月 1/15 号` |
| monthly | 1 | [1,2,3] | * | `每月 1/2/3 号` |
| monthly | 2 | [1] | * | `每 2 个月 1 号` |
| monthly | N | * | * | `每 N 个月...` |

**生成逻辑伪代码：**

```go
func GenerateRecurrenceLabel(rule RecurrenceRule) string {
    var parts []string
    if rule.Interval > 1 {
        parts = append(parts, fmt.Sprintf("每 %d", rule.Interval))
        switch rule.Frequency {
        case "daily":   parts = append(parts, "天")
        case "weekly":  parts = append(parts, "周")
        case "monthly": parts = append(parts, "个月")
        }
    } else {
        switch rule.Frequency {
        case "daily":   parts = append(parts, "每天")
        case "weekly":  parts = append(parts, "每周")
        case "monthly": parts = append(parts, "每月")
        }
    }
    switch rule.Frequency {
    case "weekly":
        days := formatWeekdays(rule.Weekdays) // "一三五", "一至五", etc.
        parts = append(parts, days)
    case "monthly":
        days := formatMonthDays(rule.MonthDays) // "1/15 号", "1 号"
        parts = append(parts, days)
    }
    if rule.EndDate == nil {
        parts = append(parts, "（长期）")
    }
    return strings.Join(parts, "")
}
```

**Weekday 格式化规则：**
- 1 个 day：`周一`、`周二`...
- 2 个 days：`周一三`（空格分隔）、`周六日`
- 3+ 个 days：若连续（如 [1,2,3,4,5]）→ `一至五`；若不连续（如 [1,3,5]）→ `一三五`
- 映射表：`1→一, 2→二, 3→三, 4→四, 5→五, 6→六, 7→日`

**Month day 格式化规则：**
- 1 个 day：`1 号`、`15 号`
- 2+ 个 days：`1/15 号`（斜杠分隔，升序）

此 label 同时写入 search vector（见"搜索"节），因此在 Service 层生成后需同时传递给搜索索引更新路径。

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
- `timezone` 默认值从服务端配置读取（如 `FLOWSPACE_DEFAULT_TIMEZONE` 环境变量，未配置时回退到 `Asia/Shanghai`）。API 层允许客户端传入 `recurrence.timezone` 覆盖默认值。

事务要求：

- 创建 `recurring` 任务时，task 和 recurrence rule 必须在同一个数据库事务中写入。任何一个写入失败则整体回滚，防止出现 `execution_type=recurring` 但没有 rule 的坏数据。
- 同样，更新 recurrence rule 时，如果涉及 task 字段变更（如 `execution_type` 从 `single` 切到 `recurring`），task 和 rule 的更新必须在同一事务中。
- **实现约束：task 和 recurrence rule 的创建/更新必须在同一个 `Store.Transact(ctx, fn)` 回调内完成。** 当前任务创建路径是 `CreateTask` → `GetTaskByID`（service/tasks.go:32-48），如果 task 和 rule 分开调用独立 repository 方法，rule 写入失败会留下 `execution_type=recurring` 但无 rule 的坏数据。正确做法是 service 层在 `Store.Transact` 回调中依次调用 task insert 和 rule insert，任何一个失败都整体回滚。两个 provider 的 `RecurrenceRepository` 不应暴露独立事务方法——rule 的 Upsert/Delete 总是作为更大事务的一部分被调用。

recurring 模板的 planned_date：

- 创建 `execution_type=recurring` 时，`planned_date` 不自动设为今天。现有 `normalizeTaskDefaults` 对 recurring 模板跳过 `planned_date` 默认值设置。recurring 模板本身没有”执行日”概念，其出现日期由 recurrence rule 展开决定。
- **禁止 recurring 模板自动获得 `planned_date = today`**。这是”模板与 occurrence 重复出现”的第一道防线。如果 recurring 模板被写入了今天的 `planned_date`，即使查询侧加 `execution_type` 过滤，日志和调试仍会混乱。实现时必须在 `normalizeTaskDefaults` 中对 `execution_type='recurring'` 的分支跳过 `planned_date` 赋值。

  **⚠️ 三份实现必须同步修改：** 当前 `normalizeTaskDefaults` 有三份副本，逻辑完全相同但分布在三个文件中：
  - `internal/repository/tasks.go:684` — 旧路径版本（包级函数 `normalizeTaskDefaults(t *model.Task)`）
  - `internal/storage/postgres/tasks.go:466` — PostgreSQL Store 版本（方法 `(r taskRepository) normalizeTaskDefaults(...)`）
  - `internal/storage/sqlite/tasks.go:471` — SQLite Store 版本（方法 `(r taskRepository) normalizeTaskDefaults(...)`）

  三个函数都在 `PlannedDate == nil` 时赋值为 `time.Now().Format(“2006-01-02”)`。对 recurring 模板，必须在此赋值前增加 `if task.ExecutionType == “recurring” { return nil }`（或 `return` 对于旧路径的无返回值版本）。三个文件的行为必须一致，否则会出现 provider 相关的 bug（例如 PG 上 recurring 无 planned_date，SQLite 上却有）。建议在测试中为三个 provider 都加 “recurring template planned_date stays NULL” 用例。
- 迁移已有 recurring 任务时，应将其 `planned_date` 清空或设为 NULL。

### 更新任务

扩展现有 `PATCH /api/tasks/:id`。

允许更新：

- 标题、内容、项目、Roadmap 节点关联。
- 重复规则。
- `recurrence.enabled`。
- `recurrence.end_date`。

限制：

- `single` 可以切换为 `recurring`，但必须同时提交合法 recurrence。

  **转换前置条件与状态清理：** single 任务可能携带执行状态（`done`、`status`、`completed_at`、`planned_date`），转换为 recurring 模板时必须处理：

  | 条件 | 行为 | 错误码 |
  |------|------|--------|
  | `done=1` 或 `status='done'` | **禁止转换**，返回 409。消息："已完成的任务不能转换为重复任务，请创建新的重复任务。" | `CANNOT_CONVERT_COMPLETED_TASK` |
  | `done=0` 且 `status='open'` | 允许转换。清空 `planned_date=NULL`，确保 `done=0`、`status='open'`、`completed_at=NULL`（已是此状态则无操作） | — |

  转换时 Service 层写入逻辑（在 `Store.Transact` 中）：
  1. 更新 `tasks` 行：`SET execution_type = 'recurring', planned_date = NULL WHERE id = $1`（`planned_date` 必须清空，见"创建任务"节）。
  2. INSERT `task_recurrence_rules`。
  3. 无需创建初始 occurrence（recurring 模板的 occurrence 仅在用户完成/跳过时落库）。
- 第一版禁止 `recurring -> single`，返回 409，并提示用户通过设置 `end_date` 或关闭 `enabled` 结束重复任务。这样可以避免已完成 occurrence 与模板删除之间的数据一致性问题。
- **禁止通过 PATCH 标记 recurring 模板为完成。** 现有 update 逻辑会直接修改 `done`/`status`/`completed_at`（postgres/tasks.go:270-282），对 recurring 模板这将产生三类错误：
  1. 把模板本身标记为完成，实际应只完成某一天的 occurrence。
  2. 污染普通完成统计——recurring 模板从未真正"完成"却计入 `completed_at` 统计。
  3. 前端无法区分模板完成和 occurrence 完成，后续恢复逻辑混乱。
- 因此，当 `task.execution_type = 'recurring'` 且请求中包含 `done=true` 或 `status=done` 时，`PATCH /api/tasks/:id` 必须返回 **409 Conflict**，错误码 `CANNOT_COMPLETE_RECURRING_TEMPLATE`，消息："可重复任务不能直接完成，请使用每日勾选完成。" 同理，`status=open` + `done=false` 的"恢复"操作也不允许（recurring 模板从未真的 done，不需要恢复），返回 409，消息："可重复任务通过 recurrence.enabled 或 end_date 管理生命周期。"
- 允许通过 PATCH 更新 recurring 模板的：`recurrence.enabled`、`recurrence.end_date`、标题、内容、项目、Roadmap 节点关联。这些是纯元数据变更，不影响完成状态。

### 编辑 recurrence 后已有 occurrence 的处理

当用户修改 recurrence rule（频率、weekdays、month_days、end_date 等）后，已落库的 `task_occurrences` 的处理规则：

1. **历史 occurrence 永久保留。** 所有已完成的 occurrence（`status = 'done'` 或 `'skipped'`）不因规则变更而删除或修改。已完成的数据是用户的历史记录，删除会造成统计失真。
2. **不再符合新规则的 occurrence 不会出现在展开结果中。** `GET /api/task-occurrences` 和今日流 / 日历只按当前 rule 展开 expected dates，左连接已有 occurrence 状态。如果某个旧 occurrence 日期不再满足新规则（例如从"每周一三五"改成"每天"，旧周三的 occurrence 在新规则下仍被展开，因为每天包含周三；但从"每天"改成"每周一三五"后，旧周二的 occurrence 不再被展开，也就不会出现在查询结果中）。
3. **旧 occurrence 仍进入每日总结。** `GET /api/summary` 统计 `task_occurrences.completed_at` 时不依赖规则展开——它只按 `completed_at` 时间范围过滤。因此规则变更前完成的 occurrence 仍然会出现在对应日期的总结中。这是预期行为：总结反映的是"那天实际完成了什么"，不是"那天规则是否符合当前配置"。
4. **PATCH 缩短 `end_date` 不删除已完成的 occurrence。** 如果用户把 end_date 从 `2026-12-31` 提前到 `2026-08-31`，9月及之后的已完成 occurrence 保留但不被展开；未完成的（`status = 'open'`）同理保留。只有显式删除 task 时 cascade 删除所有 occurrences。

   **⚠️ 级联删除警告：** `task_occurrences` 的外键为 `ON DELETE CASCADE`（见 schema），删除 recurring 模板会级联删除该任务的所有 occurrence 历史记录。例如一个执行了 6 个月的"每天背单词"任务被删除时，约 180 条完成记录将全部消失，且不可恢复。

   **前端要求：** 删除 recurring 任务时，前端必须先查询 `SELECT COUNT(*) FROM task_occurrences WHERE task_id = $1` 获取将被级联删除的记录数，然后在确认对话框中明确显示："将同时删除 N 条历史完成记录，此操作不可撤销。是否继续？"

   **非目标：** 第一版不做软删除（`deleted_at` 标记）。若后续版本需要保留历史数据，可在 `tasks` 表增加 `deleted_at` 列，并将 cascade 改为 `ON DELETE SET NULL` 或 `ON DELETE RESTRICT`。
5. **不引入 `orphan` 标记。** 第一版保持简单：不引入额外的 orphan/legacy 状态字段。历史数据通过"不展开"自然隐藏，通过"总结按时间过滤"自然可见。若后续版本需要 UI 中标记"此 occurrence 源于已变更规则"，可在 occurrence 上增加 `rule_snapshot` JSON 字段记录完成时的规则快照。

**UI 确认提示：** 编辑 recurrence rule（频率、weekdays、month_days、end_date 等）时，前端应弹出确认提示："修改重复规则后，已完成的历史记录不会被重新计算，也不会被删除。是否继续？" 避免用户期望"改了规则后旧记录会自动更新"。

### end_date 到期后模板行为

当 `end_date` 到期（`end_date < today`）后：

- **模板保持不变：** `execution_type` 仍为 `'recurring'`，`recurrence.enabled` 不会自动变为 `false`。数据库不自动修改任何字段——到期是纯运行时判断。
- **不再展开新 occurrence：** 展开算法在 `end_date` 之后不产生新日期。今日流、日历、occurrence 列表在 `end_date` 之后的日期范围内返回空。
- **已有的 occurrence 不受影响：** `end_date` 之前的 occurrence 状态（done/open/skipped）照常保留和展示。
- **UI 显示"已结束"状态：** 重复任务 tab 中，`end_date < today` 的模板显示"已结束"标签（灰色），与"进行中"（绿色）和"已暂停"（黄色，`enabled=false`）区分。三种状态的判断逻辑：
  - 进行中：`enabled=true` 且（`end_date` 为 NULL 或 `end_date >= today`）。
  - 已暂停：`enabled=false`。
  - 已结束：`enabled=true` 且 `end_date < today`。
- **可以重新激活：** 用户可以通过 PATCH 延长 `end_date`（设为未来日期）让模板重新进入"进行中"状态。也可以设置 `enabled=true` 并清除 `end_date`（设为 NULL）让其永久执行。

### 查询任务

扩展现有 `GET /api/tasks`。

新增参数：

```text
execution_type=single|recurring|all
from=2026-06-01
to=2026-06-30
include_occurrences=true|false
```

行为：

- 默认兼容旧行为：只返回 `execution_type='single'` 的任务模板行。即 `GET /api/tasks` 不加 `execution_type` 参数时，等价于 `execution_type=single`，不返回 recurring 模板。
- `execution_type=recurring` 只返回重复任务模板。
- `execution_type=all` 返回全部类型（管理后台场景）。不使用 `*` 通配符（URL query string 中易引起编码问题）。
- `include_occurrences=true` 且提供 `from/to` 时，额外返回范围内展开后的 occurrences。

所有已有查询（`GET /api/tasks`、`/api/today`、overdue、本周任务）默认追加 `execution_type='single'` 过滤，确保 recurring 模板不会在这些视图中重复出现。此过滤同时应用于 PostgreSQL 和 SQLite provider 的 WHERE 子句。

**为什么这是必须的：** 今日页 `todayTasks` 由两个来源合并：（1）普通任务 `planned_date = today`；（2）recurrence rule 展开出的当天 occurrence。如果 recurring 模板也被 `planned_date = today` 拉入第一条路径，今日页就会同时出现"模板一条"和"occurrence 一条"的重复项。两层防御：
1. 创建时：recurring 模板不写 `planned_date`（见上节）。
2. 查询时：所有普通任务查询追加 `WHERE execution_type = 'single'`（或兼容旧数据的 `WHERE execution_type IS NULL OR execution_type = 'single'`），确保即使某个 recurring 模板意外被写入了 `planned_date`，也不会出现在今日、逾期、本周视图中。
3. SQLite 和 PostgreSQL provider 的 WHERE 子句必须包含相同的 `execution_type` 过滤，行为对齐。

**实现建议：提取 helper 函数。** 不要在 `Today`、`List`、`GetCompletedTasksByRange` 等每个查询中手写 `WHERE execution_type = 'single'`。在 query builder 层加一个集中式的 helper，例如 `addExecutionTypeFilter(where, args, "single")`，所有普通任务查询统一调用。这减少遗漏风险：未来新增查询路径时，reviewer 只需确认是否调用了 helper，无需逐个检查 WHERE 子句。旧 `internal/repository/` 路径的查询也复用同一逻辑（或等效的字符串拼接）。

**TaskFilter 接口变更：** `storage/store.go:72-82` 的 `TaskFilter` struct 需要新增 `ExecutionType` 字段：

```go
type TaskFilter struct {
    // ... existing fields ...
    ExecutionType string // "" (默认=single), "single", "recurring", "all"
}
```

`TaskRepository.List(ctx, TaskFilter)` 的所有实现（PostgreSQL 的 `postgresTaskWhere`、SQLite 的 `sqliteTaskWhere`、旧 repository 的 `buildTaskQuery`）都必须根据 `ExecutionType` 追加对应的 WHERE 条件：
- `""` 或 `"single"`：`WHERE execution_type = 'single'`（兼容旧行为）
- `"recurring"`：`WHERE execution_type = 'recurring'`
- `"all"`：不加 execution_type 过滤

此变更影响所有 `TaskRepository` 实现和所有调用 `List` 的代码路径。建议在 Phase 1 一并完成。

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

URL 中 `:date` 使用 `YYYY-MM-DD` 格式（如 `2026-06-21`），与 `planned_date` 格式一致。

行为：

- `complete`：upsert occurrence，状态为 `done`，写入 `completed_at`。
- `reopen`：upsert occurrence，状态为 `open`，清空 `completed_at`。
- `skip`：upsert occurrence，状态为 `skipped`，清空 `completed_at`。
- 若 task 不是 `recurring`，返回 400。
- **若日期不是当前规则的 expected occurrence，返回 400。** 校验逻辑：
  1. 日期必须在 `start_date` 到 `end_date` 范围内（若 `end_date` 为 NULL 则只检查下限）。
  2. 日期必须匹配 recurrence rule 的展开结果——即该日按 `frequency`/`interval`/`weekdays`/`month_days` 确实是一次应出现的 occurrence。展开逻辑参见"规则语义"节。
  3. 举例：
     - 每周一三五的任务，周二传 `date=2026-06-23`（周二）→ 400，消息 "2026-06-23 不是该重复任务的执行日"。
     - 每月 1/15 号的任务，传 `date=2026-06-10` → 400。
     - `start_date=2026-06-21` 的 daily 任务，传 `date=2026-06-20` → 400（在 start 之前）。
  - 实现：复用 `ExpandRuleOccurrences(rule, date, date)`（单日展开），如果结果集为空则拒绝。
      - **时区注意：** `ExpandRuleOccurrences` 的单日展开依赖 `rule.timezone` 计算日期边界。`occurrence_date` 是纯日期字符串（`YYYY-MM-DD`），不携带时区信息。展开算法必须使用 `rule.timezone`（而非服务器本地时区）来计算给定 date 是否匹配规则。这避免了用户在 `Asia/Shanghai` 时区创建规则但服务器运行在 UTC 时区时出现日期偏移。`rule.timezone` 在创建时已从 API 参数或 `FLOWSPACE_DEFAULT_TIMEZONE` 环境变量获取并持久化到 `task_recurrence_rules.timezone` 列。
  - 前端也应做校验：对于 weekly 规则，非选中 weekday 的日期不渲染勾选框或置灰。
- 若规则已 disabled，返回 409。

**鉴权：** Occurrence 端点沿用现有 task 的鉴权机制。`POST /api/tasks/:id/occurrences/...` 路径中的 `:id` 与 task 端点共享同一鉴权中间件——先查询 task 是否存在、当前用户是否有权限操作该 task，再执行 occurrence 操作。系统当前为单用户设计（model 中无 `user_id` 字段），因此无需额外的跨用户鉴权。若后续引入多用户，鉴权逻辑集中在 task 级别的 middleware 中，occurrence 端点无需单独修改。

## 今日任务流

`GET /api/today` 继续返回 `todayTasks` 和 `overdueTasks`。

核心规则：

- recurring 模板本身（`execution_type='recurring'` 的任务行）**不进入** `todayTasks`、`overdueTasks` 和本周任务查询。这些查询都追加 `execution_type='single'` WHERE 条件。
- 只有 recurrence rule 展开出的当天 occurrence 才进入 `todayTasks`。这样今日页不会出现“模板一条 + occurrence 一条”的重复。

变化：

- `todayTasks` 增加当天应执行的 recurring occurrence。
- 重复任务不进入 `overdueTasks`，第一版只关心当天执行。是否补做过期重复任务以后再设计，避免今日流被历史重复任务刷屏。
- 今日流里重复任务项使用稳定 key：`task_id + occurrence_date`。
- **排序规则：** 当前 today 查询排序为 `ORDER BY t.sort_order ASC, t.created_at DESC`（`postgres/tasks.go:361`）。recurring occurrence 没有独立的 `sort_order`，继承模板的 `sort_order`。合并后的今日任务列表统一按 `sort_order ASC, created_at DESC` 排序，single 任务和 recurring occurrence 在同一排序规则下混排。实现时在 occurrence 展开阶段将模板的 `sort_order` 和 `created_at` 复制到 occurrence 结果 struct 中。

重复任务返回字段示例：

```json
{
  "id": "task-1",
  "title": "背 50 个单词",
  "execution_type": "recurring",
  "occurrence_date": "2026-06-21",
  "occurrence_status": "open",
  "recurrence_label": "每天",
  "done": 0
}
```

**字段说明：** 今日/日历视图中 recurring 任务的 `occurrence_date` 是展开出的执行日期，不是模板的 `planned_date`。recurring 模板在 `tasks` 表中的 `planned_date` 始终为 NULL（见"创建任务"节）。前端通过 `occurrence_date` 字段获取当天 occurrence 的日期，不依赖 `planned_date`。两个字段职责不同，不可复用。

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

不把两类任务强行折算成一个总百分比，避免”一个长期重复练习”压过普通节点任务。

**Roadmap node 状态更新：** 当前 `syncRoadmapNodeStatus` 在 task 完成/重开时自动更新 node 的 `done`/`status`（postgres/tasks.go:325）。recurring task 的 **occurrence 完成不触发 `syncRoadmapNodeStatus`**。Roadmap node 的完成状态继续由关联的 single task 驱动。重复练习的进度通过”近 7 天应执行/已完成/跳过次数”独立展示，不等同于 node 完成。

## 日历页

日历页通过 `GET /api/task-occurrences?from=<monthStart>&to=<monthEnd>` 获取当月重复任务实例。

展示：

- 月视图某天有重复任务时增加 dot。
- 选中某天后，右侧 inspector 显示该日重复任务。
- 勾选重复任务只完成选中日期 occurrence。

现有 `/api/today` 仍用于今日 inspector。非今日日期使用 occurrences API。

## 每日总结

每日总结统计需要合并两个数据源：

- 普通任务：`tasks.completed_at` 落在范围内。
- 重复任务 occurrence：`task_occurrences.completed_at` 落在范围内。

### 数据获取

现有 summary 通过一次分页查询 `tasks.completed_at` 获取普通任务，再单独查 `COUNT` 得到 `active_days` 和 `project_count`（summary.go:11-50）。加入 occurrences 后改为两步查询再合并：

1. **普通任务：** `GetCompletedTasksByRange(from, to, page, pageSize)` — 保持不变，按 `tasks.completed_at DESC` 分页。
2. **重复任务 occurrence：** 新增 `GetCompletedOccurrencesByRange(from, to)` — 查 `task_occurrences` JOIN `tasks` 获取 title/project/roadmap metadata，返回 `[]TaskSummary`（带 `source: 'occurrence'` 标记）。
3. **合并排序：** Service 层将两个结果集按 `completed_at DESC` 合并排序。
4. **合并分页：** 因为两个数据源独立分页会丢失顺序（例如第 2 页可能包含前 20 条中的 15 条普通任务 + 5 条 occurrence，但普通任务分页只返回前 20 条普通任务，不一定包含那 15 条），分页策略如下：

   **方案 A（推荐，第一版）：全量合并再分页。** 两个查询在范围内都不分页（`from/to` 最多 370 天，数据量可控），Service 层合并排序后切页返回。`total = 普通任务 total + occurrence total`。此方案实现最简单，行为在两个 provider 上一致。

   **方案 B（备选，数据量大后）：游标合并。** 两个数据源各维护一个游标，按 `completed_at DESC` 归并取前 N 条。需要两个 provider 的查询都支持游标，复杂度高，不在第一版。

   **第一版采用方案 A。**

### 分页 total 计算

```go
total = tasks_completed_in_range + occurrences_completed_in_range
```

两个 COUNT 分别查询，相加得到 total。`page`/`pageSize` 在合并排序后的全量结果上切页，不分别在两个数据源上分页。

### active_days 和 project_count 计算

`GetSummaryStats(from, to)` 目前计算 `COUNT(DISTINCT DATE(completed_at))` 和 `COUNT(DISTINCT project_id)`（postgres/tasks.go:441-449）。合并后：

- **active_days：** `SELECT COUNT(DISTINCT completed_date) FROM (SELECT DATE(completed_at) AS completed_date FROM tasks WHERE completed_at >= $1 AND completed_at < $2 UNION ALL SELECT occurrence_date AS completed_date FROM task_occurrences WHERE completed_at IS NOT NULL AND completed_at >= $1 AND completed_at < $2)` — 对两个数据源的日期做 UNION，再 DISTINCT 计数。UNION ALL + 外层 DISTINCT 保证同一日在两边都出现也只计 1 天。
- **project_count：** `SELECT COUNT(DISTINCT project_id) FROM (SELECT COALESCE(t.project_id, 'personal') AS project_id FROM tasks t WHERE t.completed_at >= $1 AND completed_at < $2 UNION ALL SELECT COALESCE(t.project_id, 'personal') AS project_id FROM task_occurrences o JOIN tasks t ON t.id = o.task_id WHERE o.completed_at IS NOT NULL AND o.completed_at >= $1 AND o.completed_at < $2)` — 按参与完成的项目数取 DISTINCT，两个数据源 UNION。

### 数据源区分

返回的 `TaskSummary` 增加字段：

```go
type TaskSummary struct {
    // ... existing fields ...
    ExecutionType string `json:"execution_type,omitempty"` // "single" | "recurring"
    OccurrenceDate string `json:"occurrence_date,omitempty"` // 仅 recurring，格式 "2006-01-02"
}
```

展示时标记：

```text
重复任务 · 背 50 个单词 · 2026-06-21
单次任务 · 写迁移方案
```

这样重复任务不会污染普通 task 完成记录，但会进入真实完成统计。

### 两个 provider 对齐

SQLite 和 PostgreSQL provider 的总结查询必须返回相同的字段和排序。合并排序逻辑放在 Service 层而非 SQL 层，避免两个 provider 各自实现合并导致行为分叉。

**类型一致性：** `GetCompletedOccurrencesByRange` 返回的 `[]TaskSummary` 中，`CompletedAt *int64` 必须是 Unix 时间戳。两个 provider 在 repository 层各自完成类型转换（PG 的 `TIMESTAMPTZ → Unix`，SQLite 的 `INTEGER → int64`）。Service 层的合并排序直接比较 `*int64` 数值，不处理时间格式差异。

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
- 对 `execution_type='recurring'` 的任务，将 `recurrence_label`（如"每天"、"每周一三五"、"每月 1/15 日"）追加到 search vector 中。这样用户搜索"每天"或"每周"能找到对应的 recurring 任务。`recurrence_label` 在 Service 层生成后写入 search vector，不直接从 `task_recurrence_rules` 表读取。
- `task_recurrence_rules` 表本身不单独进入全文索引（字段级信息已通过 label 覆盖）。
- **搜索索引更新时机：** 当 recurrence rule 发生变更（PATCH 修改 frequency、weekdays、month_days、interval 等导致 `recurrence_label` 变化）时，必须触发 `upsertTaskSearchIndex`，将新的 `recurrence_label` 写入 search vector。具体触发点：
  - 创建 recurring task 时（POST /api/tasks）→ task insert + rule insert → upsertTaskSearchIndex。
  - 更新 recurrence rule 时（PATCH /api/tasks/:id）→ rule update → 若 label 可能变化则 upsertTaskSearchIndex。
  - 切换 `execution_type`（single → recurring）时 → rule insert → upsertTaskSearchIndex。
  - 删除 task 时 → cascade 删除 rule → deleteTaskSearchIndex（现有逻辑，无需改动）。
  - **实现建议：** 在 `RecurrenceService.UpdateRule()` 方法中，rule 写入成功后比较新旧 label，仅在 label 实际变化时调用 `upsertTaskSearchIndex`，避免不必要的搜索索引重写。

## 存储与迁移

### PostgreSQL

新增迁移：

- 给 `tasks` 增加 `execution_type` 列（`ALTER TABLE tasks ADD COLUMN execution_type TEXT NOT NULL DEFAULT 'single'`）。
- **回填已有行：** `UPDATE tasks SET execution_type = 'single' WHERE execution_type IS NULL OR execution_type = ''`。注意 `DEFAULT` 只对新插入的行生效，不会自动回填已有行。PostgreSQL 和 SQLite 都需要显式执行此 UPDATE。
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

- `execution_type` 使用 TEXT CHECK。迁移时同样需要 `UPDATE tasks SET execution_type = 'single'` 回填已有行。
- `weekdays`、`month_days` 使用 JSON array 字符串（格式 `[1,3,5]`，无空格，标准 JSON number array），repository 层校验 JSON 内容合法性（CHECK 约束在 SQLite 中无法校验 JSON）。两个 provider 的序列化/反序列化必须使用相同格式，跨 provider 迁移时直接 COPY 字符串即可。
- 日期字段继续使用 `YYYY-MM-DD` 文本。
- `task_occurrences.completed_at` 使用 `INTEGER`（Unix 时间戳），与 `tasks.completed_at` 一致。Repository 层统一转为 Go `int64`。
- **⚠️ 事务支持前置依赖：** 当前 `sqlite/tasks.go:211` 的 `Create` 方法直接调 `r.db.ExecContext()`，不开启事务。而 PostgreSQL 的 `Create`（`postgres/tasks.go:216`）使用 `r.withTx()` 包装。设计文档要求 task 和 recurrence rule 在同一事务中写入（见"创建任务"节事务要求）。**在实现 recurring task 创建之前，必须先给 SQLite 的 `taskRepository.Create`、`Update`、`Delete` 加上事务支持**（对齐 PG 的 `withTx` 模式）。`deleteTaskSearchIndex` 也需要在同一事务中执行。此改动影响范围：
  - `sqlite/tasks.go:211` Create → 包装 `withTx()`
  - `sqlite/tasks.go:228` Update → 已使用 `BeginTx`，需确保 search index upsert 在同一 tx 中
  - SQLite `Delete` → 需新增（当前可能委托给通用方法），确保 cascade 和 search index delete 在同一 tx 中
  - 同时需要在 `sqlite/tasks.go` 中新增 `withTx` helper（仿照 `postgres/tasks.go` 的模式）

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

### 路径决策：统一走 Store，旧 repository 路径不支持 recurring

当前代码有两条数据访问路径：

- `internal/repository/` — 旧路径，直接操作 SQLite DB（如 `repository/tasks.go` 中的 `GetTasks`、`GetTodayTasks`、`GetCompletedTasksByRange`）。
- `internal/storage/` — 新 Store 抽象，支持 PostgreSQL 和 SQLite 双 provider。

**决策：旧 `internal/repository/` 路径对 recurring task 返回空或不支持。所有 recurring 功能（创建、查询、occurrence 操作、总结合并）统一走 `internal/storage/` 的 Store 路径。**

理由：
1. 旧 repository 路径是单 SQLite 的，没有事务抽象（`Store.Transact`），无法满足 task + rule 同事务要求。
2. 在两个路径上分别实现 recurrence 展开逻辑会导致行为分叉，测试矩阵翻倍。
3. 旧路径已在逐步迁移到 Store 路径的过程中，recurring 功能可以作为迁移的加速器。

实现：
- `repository/tasks.go` 的 `GetTasks`、`GetTodayTasks` 等函数追加 `WHERE execution_type = 'single'`（或不加过滤但 service 层对 recurring 返回空切片）。推荐直接加 WHERE 过滤，行为明确。
- 新增的 `RecurrenceRepository` 只在 `internal/storage/` 下实现，不在 `internal/repository/` 下提供。
- API handler 层注入 Store 而非旧 repository 的 DB 连接。

### Service 层重构路径

recurring 功能会影响两个核心 service 文件：`service/today.go` 和 `service/summary.go`。当前这两个 service 直接调用旧 repository 路径或 Store 的单数据源方法，重构后需要合并两个数据源。

#### `service/today.go` — GetToday()

**当前实现：** `service/today.go:25` 调 `repository.GetTodayTasks()`（或等价的 Store 方法），返回 `[]model.Task`。

**重构后：**

1. 普通任务查询：追加 `WHERE execution_type = 'single'`，只返回单次任务（`planned_date = today`）。
2. Recurring occurrence 展开：调用 recurrence service 展开当天应出现的所有 occurrence，左连接 `task_occurrences` 获取完成状态。
3. 合并结果：两个数据源的结果合并为统一的返回列表。

**返回类型变化：** `GetToday()` 当前返回 `[]model.Task`，但 recurring occurrence 需要额外字段（`occurrence_date`、`occurrence_status`、`recurrence_label`）。有两个选项：

- **选项 A（推荐）：扩展 `Task` struct。** 在 `model.Task` 上增加 `OccurrenceDate *string`、`OccurrenceStatus *string`、`RecurrenceLabel *string` 三个可空字段。`single` 任务这些字段为 `nil`，前端按 `execution_type` 判断使用哪些字段。此方案不需要修改前端 TaskRow 组件的数据结构签名，影响最小。
- **选项 B：新增 `TodayItem` 联合类型。** 定义新 struct 包含 `Task` 嵌入 + occurrence 字段。需要修改前端类型定义和组件 props，改动范围更大。

**第一版采用选项 A。** 理由：现有 `Task` struct 已有多个可选字段（`Due`、`RoadmapNodeID` 等），增加三个可空字段不会破坏现有代码。前端通过 `execution_type` 条件渲染即可。

#### `service/summary.go` — GetSummary()

**当前实现：** `service/summary.go:12` 调用 repository 的 `GetCompletedTasksByRange()`，返回分页结果。

**重构后：**

1. 普通任务查询：`GetCompletedTasksByRange(from, to)` — 查询 `tasks.completed_at` 在范围内的已完成任务。使用 `WHERE (execution_type IS NULL OR execution_type = 'single')` 作为防御性过滤：迁移会回填所有已有行（`UPDATE tasks SET execution_type = 'single'`），新行有 `DEFAULT 'single'`，但 `IS NULL` 分支用于极端情况下迁移未完成的数据。recurring 模板的 `completed_at` 永远为 NULL（见"更新任务"节中的禁止完成规则），因此理论上即使不加过滤也不会拉到 recurring 模板，但显式过滤更健壮。
2. Occurrence 查询：新增 `GetCompletedOccurrencesByRange(from, to)` — 查询 `task_occurrences.completed_at` 在范围内的记录，JOIN `tasks` 获取 title/project metadata。
3. 合并排序：Service 层将两个 `[]TaskSummary` 按 `completed_at DESC` 全量合并排序（方案 A，见"每日总结"节）。
4. 分页切分：合并排序后在内存中按 `page`/`pageSize` 切页。

**返回类型变化：** `TaskSummary` 增加 `ExecutionType string` 和 `OccurrenceDate string` 字段（已在"每日总结"节定义）。前端按 `execution_type` 区分渲染。

**重构范围总结：**

| 文件 | 当前依赖 | 重构后依赖 | 返回类型变化 |
|------|---------|-----------|------------|
| `service/today.go` | `repository.GetTodayTasks()` | Store 普通任务查询 + RecurrenceService.ExpandForToday() | `[]model.Task`（扩展 3 个字段） |
| `service/summary.go` | `repository.GetCompletedTasksByRange()` | Store 普通任务查询 + Store.GetCompletedOccurrencesByRange() + Service 层合并排序 | `TaskSummary`（扩展 2 个字段） |
| `service/tasks.go` | `repository.CreateTask()` | Store.Transact(task insert + rule upsert) | 无变化（创建仍返回 `Task`） |

"每日总结"节已详细描述合并排序和分页逻辑，此处不重复。

**Handler 层依赖注入变更：**

当前 handler 层没有 Store 依赖注入——`service.GetToday()` 和 `service.GetSummary()` 是包级函数，内部直接调用 `repository.*` 包级函数。重构后需要：

1. **Handler 注入 Store：** API handler（如 `cmd/server/main.go` 的路由注册处）需要持有 `*storage.Store` 实例，并在处理请求时传递给 service 层。
2. **Service 函数签名变更：**
   ```go
   // 旧签名（包级函数，无依赖注入）
   func GetToday() (*TodayData, error)
   func GetSummary(params model.SummaryParams) (*model.SummaryData, error)

   // 新签名（接受 Store 和 RecurrenceService）
   func GetToday(ctx context.Context, store *storage.Store, recurrenceService *RecurrenceService) (*TodayData, error)
   func GetSummary(ctx context.Context, store *storage.Store, params model.SummaryParams) (*model.SummaryData, error)
   ```
3. **`service/tasks.go` 同步改造：** `CreateTask` 也需要接受 Store 参数（用于 `Store.Transact` 包装 task + rule 原子写入），不再调用 `repository.CreateTask()`。

**重构工作量评估：** 这不是"追加 recurring 支持"，而是"重写 Today/Summary 数据获取层"。两个 service 文件需要从旧的包级 `repository.*` 调用迁移到注入的 `Store` 接口。预期改动：
- `service/today.go`：~20 行 → 扩展 occurrence 展开 + 合并逻辑，约 60-80 行
- `service/summary.go`：~74 行 → 扩展两个数据源合并排序 + 分页逻辑，约 120-150 行
- Handler 层：路由注册处新增 Store 和 RecurrenceService 的创建与注入，约 20-30 行
- 旧 `internal/repository/tasks.go`：追加 `WHERE execution_type = 'single'` 过滤（短期兼容），或直接废弃 Today/Summary 相关函数（长期）

### 领域模型

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

**Store 接口扩展：** `storage/store.go` 的 `Store` 接口需新增 `Recurrence()` 方法，使 Service 层能通过 `store.Recurrence()` 获取 `RecurrenceRepository`，并在 `Store.Transact` 回调中通过事务包装后的 Store 调用 `UpsertRule` 等方法：

```go
type Store interface {
    // ... existing methods (Tasks, Events, Notes, etc.) ...
    Recurrence() RecurrenceRepository  // ← 新增
    Transact(context.Context, func(Store) error) error
}
```

**RecurrenceRepository 接口：**

```go
type RecurrenceRepository interface {
    // UpsertRule 和 DeleteRule 不暴露独立事务——总是作为 Store.Transact 回调内的一步被调用。
    // 调用方负责在 Transact 中先写 task 再写 rule，保证原子性。
    UpsertRule(ctx context.Context, rule *model.RecurrenceRule) error
    GetRule(ctx context.Context, taskID string) (*model.RecurrenceRule, error)
    DeleteRule(ctx context.Context, taskID string) error
    // ListActiveRules 返回所有 enabled=true 且在 [from, to] 范围内至少有一个 occurrence 的 recurrence rule。
    // 用途：今日/日历展开时先查出需要处理的 rule 列表，再逐个展开。不返回 disabled 的 rule 和完全在范围外的 rule。
    // 如果 rule.end_date 为 NULL，只用 start_date 判断（start_date <= to 即纳入）。
    // 如果 rule.end_date 不为 NULL，判断条件：start_date <= to AND end_date >= from。
    ListActiveRules(ctx context.Context, from, to string) ([]model.RecurrenceRule, error)
    // ListOccurrences 按 occurrence_date 展开 expected dates + LEFT JOIN 已有 task_occurrences 状态。
    // 用于今日流、日历、日期范围查询——这些场景需要知道"哪些日期应该有 occurrence，以及每个 occurrence 的状态"。
    ListOccurrences(ctx context.Context, from, to string) ([]model.TaskOccurrence, error)
    // GetCompletedOccurrencesByRange 按 completed_at 时间范围过滤已完成记录（JOIN tasks 获取 title/project metadata）。
    // 用于每日总结——总结关心"何时完成了什么"，不关心 occurrence_date 展开逻辑。
    // 返回 []model.TaskSummary（与普通任务的 GetCompletedTasksByRange 返回类型一致）。
    GetCompletedOccurrencesByRange(ctx context.Context, from, to int64) ([]model.TaskSummary, error)
    CompleteOccurrence(ctx context.Context, taskID, date string, completedAt int64) (*model.TaskOccurrence, error)
    ReopenOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error)
    SkipOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error)
}
```

**事务约定：** `RecurrenceRepository` 的方法不自行开启事务。`UpsertRule`、`DeleteRule`、`CompleteOccurrence`、`ReopenOccurrence`、`SkipOccurrence` 都接受调用方传入的事务上下文（通过 `ctx` 中绑定的 tx），由 Service 层在 `Store.Transact` 回调内编排多个 repository 调用。

`Store.Transact` 的签名（来自 `storage/store.go:33`）：

```go
Transact(context.Context, func(Store) error) error
```

回调接收的是**事务包装后的 Store**（而非 `context.Context`）。Repository 方法通过 `ctx` 参数获取事务上下文，而 `ctx` 由调用方传入（通常是原始的 `context.Context`，事务信息绑定在 `ctx` 中）。示例创建路径：

```go
func (s *Service) CreateRecurringTask(ctx context.Context, req *model.CreateTaskRequest) (*model.Task, error) {
    var task *model.Task
    err := s.store.Transact(ctx, func(txStore storage.Store) error {
        t, err := txStore.Tasks().Create(ctx, req)  // INSERT INTO tasks（在事务中）
        if err != nil { return err }
        if err := txStore.Recurrence().UpsertRule(ctx, &req.Recurrence); err != nil {  // INSERT INTO task_recurrence_rules（同一事务）
            return err  // 回滚 task insert
        }
        task = t
        return nil
    })
    return task, err
}
```

关键点：
- 回调内使用 `txStore`（事务版 Store），而非外部的 `s.store`。
- `txStore.Recurrence()` 返回的是事务上下文下的 `RecurrenceRepository`，其 `UpsertRule` 在事务中执行。
- 回调返回 error 时，整个事务自动回滚（包括 task insert 和 rule insert）。
- `ctx` 参数透传，事务绑定由 `txStore` 内部管理（PostgreSQL 的 `withTx` 模式）。

读操作（`GetRule`、`ListActiveRules`、`ListOccurrences`、`GetCompletedOccurrencesByRange`）可以在事务外独立调用，通过 `store.Recurrence()`（非事务版）访问。

Service 层负责：

- 校验 recurrence request。
- 生成日期范围内的 expected occurrences。
- 合并数据库中已有 occurrence 状态。
- 生成中文 `recurrence_label`。
- 今日流和日历使用同一套展开逻辑。
- **状态合并逻辑也共用：** 展开 expected dates 后，左连接 `task_occurrences` 获取已有状态（done/open/skipped）的操作也在同一 Service 方法中完成。今日流（`GET /api/today`）和日历/日期范围（`GET /api/task-occurrences`）都调用 `RecurrenceService.ExpandWithStatus(ctx, rule, from, to)` 或等效方法，确保同一天的同一 occurrence 在两个视图中显示相同的 `occurrence_status`。不可在两个端点分别实现状态合并——这会引入不一致的风险。

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
- 409：任务状态不允许当前操作（包括：`CANNOT_COMPLETE_RECURRING_TEMPLATE`——尝试 PATCH 完成 recurring 模板；`RECURRENCE_DISABLED`——尝试完成已暂停规则的 occurrence；`CANNOT_SWITCH_RECURRING_TO_SINGLE`——尝试将 recurring 切回 single）。
- 500：存储或未知错误。

## 已知限制（v1）

### 时区与日期边界

`task_occurrences.completed_at` 存储 Unix 时间戳（UTC），`occurrence_date` 存储 `YYYY-MM-DD` 字符串（按 `rule.timezone` 计算）。每日总结的 `from`/`to` 日期边界使用服务器时区（从 `FLOWSPACE_DEFAULT_TIMEZONE` 或回退 `Asia/Shanghai`）转换为 UTC 范围后查询 `completed_at`。

**边界情况：** 当用户所在时区与服务器时区不同时，`completed_at` 对应的日期可能与用户感知的"完成日"有 1 天偏差。例如：用户在 `Asia/Shanghai`（UTC+8）6 月 21 日 23:30 完成一个 daily 任务，`completed_at` 记录为 15:30 UTC。如果总结查询用 UTC 日期边界（`from=2026-06-21T00:00:00Z`），这条记录会被归到 6 月 20 日。

**v1 处理：** 第一版使用服务器时区（`FLOWSPACE_DEFAULT_TIMEZONE`）计算日期边界，该值默认与 `rule.timezone` 一致（默认 `Asia/Shanghai`）。在用户和服务器处于同一时区的常规部署下无问题。跨时区场景（用户通过远程客户端在不同时区操作）列为后续迭代，届时在请求中传递用户时区或从 `rule.timezone` 推导边界。

### 总结合并排序内存上限

方案 A 在内存中全量合并两个数据源的 `[]TaskSummary`。对于重度用户（10 个 daily 任务 × 365 天 = 3650 条 occurrence + 若干普通任务），数据量在 5000 条以内，Go 的 slice 排序和内存占用无压力。

**硬上限：** Service 层在合并前检查两个数据源的总行数，超过 **10,000 条** 时截断并返回警告响应头 `X-Truncated: true`。截断策略：按 `completed_at DESC` 排序后取前 10,000 条，超出部分丢弃。此上限远高于预期使用量（~5000 条），仅在极端场景触发。

## 测试计划

### 后端单元测试

1. `ExpandDailyRuleIncludesStartAndEnd`
2. `ExpandEveryTwoDaysUsesStartDateAsAnchor`
3. `ExpandWeeklyRuleUsesISOWeekdays`
4. `ExpandMonthlyRuleSkipsMissingMonthDay`
5. `CompleteOccurrenceDoesNotMarkTaskDone`
6. `PatchDoneOnRecurringTemplateReturns409`
7. `PatchStatusDoneOnRecurringTemplateReturns409`
8. `TodayIncludesRecurringOccurrenceForToday`
9. `TodayDoesNotIncludeRecurringOccurrenceWhenRuleDisabled`
10. `RecurringOccurrencesDoNotBecomeOverdueByDefault`
11. `CompleteWeeklyOccurrenceOnNonWeekdayReturns400`
12. `CompleteMonthlyOccurrenceOnNonMonthDayReturns400`

后续迭代测试（不进入第一版）：
- 跨时区完成 occurrence：用户在 UTC+8 创建任务，在 UTC-5 完成，`occurrence_date` 按 rule.timezone 计算"今天"。
- 并发 upsert：两个请求同时对同一 occurrence 做 complete，验证 upsert 竞态行为。

### Storage contract tests

在 SQLite 和 PostgreSQL provider 上都跑：

1. 创建 recurring task 和 rule 后能 round-trip（同一事务）。
2. occurrence complete/reopen/skip upsert 行为一致。
3. `ListOccurrences(from,to)` 合并 task/project/roadmap metadata。
4. 删除 task 后 cascade 删除 rule 和 occurrences。
5. 修改 recurrence rule 后，旧 occurrence 保留但不再被展开（除非仍符合新规则）。
6. 总结查询合并普通任务和 occurrence 后 active_days 正确（两个数据源 union 去重）。
7. 总结查询分页 total = 普通任务数 + occurrence 数。

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

- **前置依赖：给 SQLite taskRepository 加上事务支持。** `Create`/`Update`/`Delete` 需对齐 PG 的 `withTx()` 模式，否则无法满足 task + rule 原子写入要求。详见"存储与迁移 → SQLite"节。
- 新增 schema（`execution_type`、`task_recurrence_rules`、`task_occurrences`）。
- 新增 model（`RecurrenceRule`、`TaskOccurrence`、`TaskFilter.ExecutionType`）。
- 三份 `normalizeTaskDefaults` 同步修改：`repository/tasks.go:684`、`postgres/tasks.go:466`、`sqlite/tasks.go:471`，对 `execution_type='recurring'` 跳过 `planned_date` 默认值。
- 新增 recurrence service（展开算法、label 生成、状态合并）。
- 后端测试先覆盖规则展开和 occurrence 状态。

### Phase 2: 今日流

- **Handler 层注入 Store 和 RecurrenceService。** 路由注册处（`cmd/server/main.go`）创建 Store 实例并注入 handler/service。
- `service/today.go` 从 `repository.GetTodayTasks()` 迁移到 `Store.Tasks().Today()` + `RecurrenceService.ExpandForToday()`。
- `/api/today` 合并今日重复任务（occurrence 展开 + 状态左连接）。
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
3. 不让重复任务进入逾期列表，第一版只显示当天应执行项，不支持补做逾期重复任务。
4. 搜索任务模板，不搜索 occurrence；但 recurrence_label 进入 search vector。
5. 第一版不支持复杂 RRULE，先覆盖 daily / weekly / monthly。
6. 旧 `internal/repository/` 路径不支持 recurring，所有 recurring 功能统一走 `internal/storage/` Store 路径。
7. `execution_type` 迁移回填：`UPDATE tasks SET execution_type = 'single'`（DEFAULT 不自动回填已有行）。
8. `completed_at` 物理存储因 provider 不同（PG: TIMESTAMPTZ, SQLite: INTEGER），Repository 层统一转为 Go `int64`。
9. `timezone` 不从 schema 硬编码，从服务端配置读取，API 层可覆盖。
10. `task_occurrences.note` 第一版保留列但不暴露 API/UI。
11. 编辑 recurrence rule 时前端弹出确认提示，告知用户历史记录不会被重新计算。
12. occurrence 完成不触发 `syncRoadmapNodeStatus`，Roadmap node 状态由 single task 驱动。
13. 合并排序逻辑在 Service 层，不在 SQL 层，确保两个 provider 行为一致。
14. recurring 模板的 `planned_date` 始终为 NULL，今日视图中 recurring 任务使用 `occurrence_date` 字段，不复用 `planned_date`。
15. weekly interval > 1 的展开锚点：从 `start_date` 所在 ISO 周为第 0 周开始计数，每 `interval` 周重复。使用 ISO 8601 周定义（周一开始）。
16. 删除 recurring 模板时前端先查询级联删除的 occurrence 数量，确认对话框中明确展示数字。
17. `end_date` 到期后模板保持不变（不自动改 `enabled`），仅展开算法在 `end_date` 之后不产生新日期。UI 显示"已结束"状态。
18. `recurrence_label` 生成规则由 Service 层统一实现（见规则表），两个 provider 不各自生成。label 变化时触发 `upsertTaskSearchIndex`。
19. 总结合并排序硬上限 10,000 条，超出截断并返回 `X-Truncated: true` 响应头。
20. 第一版总结日期边界使用服务器时区（`FLOWSPACE_DEFAULT_TIMEZONE`），跨时区场景可能有 1 天偏差，列为后续迭代。
21. SQLite `taskRepository.Create` 当前无事务支持（直接 `ExecContext`），实现 recurring 前必须先加 `withTx()` 包装，对齐 PG 模式。
22. 三份 `normalizeTaskDefaults`（`repository/tasks.go:684`、`postgres/tasks.go:466`、`sqlite/tasks.go:471`）必须同步修改，对 `execution_type='recurring'` 跳过 `planned_date` 赋值。
23. `service/today.go` 和 `service/summary.go` 需从旧 `repository.*` 包级函数迁移到 Store 依赖注入，handler 层需要 Store 注入，属于"重写数据获取层"而非"追加功能"。
24. `storage/store.go` 的 `TaskFilter` 需新增 `ExecutionType` 字段，所有 `TaskRepository.List` 实现（PG/SQLite/旧 repository）同步追加 WHERE 条件。
25. 今日流和日历的 occurrence 展开 + 状态合并（LEFT JOIN `task_occurrences`）共用同一 Service 方法，不可在两个端点分别实现。
26. Occurrence 日期校验的 `ExpandRuleOccurrences` 必须使用 `rule.timezone`（非服务器时区）计算日期边界。
27. Monthly 规则展开算法需处理闰年：`month_days=[29]` 在非闰年 2 月跳过，闰年 2 月出现。
28. `Store` 接口新增 `Recurrence() RecurrenceRepository` 方法，使 Service 层可通过 Store 访问 recurrence 功能，并在 `Store.Transact` 回调中使用事务版 Store。
29. `RecurrenceRepository` 区分 `ListOccurrences`（按 `occurrence_date` 展开）和 `GetCompletedOccurrencesByRange`（按 `completed_at` 过滤），分别用于今日/日历和每日总结。
30. `single → recurring` 转换时，已完成任务（`done=1`）禁止转换返回 409；未完成任务清空 `planned_date=NULL`，确保模板无执行状态。
31. 今日流中 single 任务与 recurring occurrence 按统一的 `sort_order ASC, created_at DESC` 混排，occurrence 继承模板的 `sort_order`。
32. SQLite 中 `weekdays`/`month_days` JSON 格式为 `[1,3,5]`（无空格，标准 JSON number array），PG 用 `INTEGER[]`，Repository 层统一转 `[]int`。
33. `Store.Transact` 回调签名是 `func(Store) error`（非 `func(context.Context) error`），Service 层在回调内通过 `txStore.Recurrence()` 访问事务版 Repository。
34. 总结查询的 `GetCompletedTasksByRange` 使用 `WHERE (execution_type IS NULL OR execution_type = 'single')` 作为防御性过滤。
