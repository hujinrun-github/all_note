# 日历项目来源配置设计

**Goal:** 让日历来源和任务项目体系对齐：个人短期项目默认进入日历，长期项目和学习项目由用户配置后进入日历，并且配置保存到后端。

**Status:** 设计待审。本文档只定义方案，不包含实现代码。

## 背景

当前日历页左侧使用硬编码来源：`工作`、`个人`、`提醒`。这些来源不是完整的数据模型，只是基于 `events.kind` 做前端筛选。

现有数据里：

- `events` 表只有 `kind` 字段，默认值是 `work`。
- 快速捕获创建日程时，会根据所选任务项目类型把 `kind` 写成 `personal` 或 `work`。
- `reminder` 没有独立创建入口，也没有真实提醒模型。
- 日历页新增的自定义来源只是前端临时状态，不会保存到后端。

这导致 UI 看起来像 Apple Calendar 的日历来源列表，但实际语义不清：用户无法理解 `工作 / 个人 / 提醒` 和任务项目之间的关系，也无法把长期项目或学习项目稳定地加入日历。

## 设计结论

日历来源改为“项目驱动”：

- `个人短期项目` 默认进入日历，不需要用户配置。
- `长期项目` 和 `学习项目` 需要用户主动加入日历。
- 加入日历是用户级配置，保存到后端；同一 workspace 中不同用户可以有不同日历来源。
- 日程本身通过 `project_id` 归属到任务项目，`kind` 只保留兼容和旧数据迁移用途。
- 快速捕获、日历页新增日程都必须写入 `project_id`。

## 非目标

第一版不实现完整日历账户系统：

- 不接入外部 CalDAV、Google Calendar 或系统日历。
- 不做共享日历权限模型。
- 不做提醒通知、重复日程、全天日程。
- 不做拖拽调度。
- 不把任务直接自动转换成日程。任务仍然通过任务日期出现在任务流，日程通过 `events` 表管理。

## 数据模型

### events 增加 project_id

给 SQLite 和 PostgreSQL 的 `events` 表增加 `project_id`：

```sql
project_id TEXT NULL
```

`task_projects` 会迁移为 workspace-scoped 复合主键，因此事件归属必须用 `(workspace_id, project_id)` 复合外键，而不是单列 `project_id`：

```sql
FOREIGN KEY (workspace_id, project_id)
  REFERENCES task_projects(workspace_id, id)
  ON UPDATE CASCADE
  ON DELETE NO ACTION
```

含义：

- 日程归属到一个任务项目。
- `project_id` 允许为空，用于兼容历史数据或无法确定项目的日程。
- 删除项目时，日程不删除。删除流程必须先通过 service transaction 或数据库 trigger 执行 `UPDATE events SET project_id = NULL WHERE workspace_id = ? AND project_id = ?`，只清空 `events.project_id`，保留 `events.workspace_id`。
- 不要在 `events` 的复合外键上使用 `ON DELETE SET NULL`。SQLite 对复合引用列执行 SET NULL 时可能同时影响 `workspace_id`，这会破坏 workspace 隔离。

索引：

```sql
CREATE INDEX events_workspace_project_start_idx ON events (workspace_id, project_id, start_time);
```

PostgreSQL 使用 `start_at`：

```sql
CREATE INDEX events_workspace_project_start_idx ON events (workspace_id, project_id, start_at);
```

### calendar_project_sources

新增用户级配置表 `calendar_project_sources`。

PostgreSQL：

```sql
CREATE TABLE calendar_project_sources (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  color TEXT NOT NULL DEFAULT '',
  order_index INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, user_id, project_id),
  FOREIGN KEY (workspace_id, user_id)
    REFERENCES workspace_members(workspace_id, user_id)
    ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, project_id)
    REFERENCES task_projects(workspace_id, id)
    ON UPDATE CASCADE
    ON DELETE CASCADE
);

CREATE INDEX calendar_project_sources_user_idx
  ON calendar_project_sources (workspace_id, user_id, enabled, order_index);
```

SQLite：

```sql
CREATE TABLE IF NOT EXISTS calendar_project_sources (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  color TEXT NOT NULL DEFAULT '',
  order_index INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (workspace_id, user_id, project_id),
  FOREIGN KEY (workspace_id, user_id)
    REFERENCES workspace_members(workspace_id, user_id)
    ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, project_id)
    REFERENCES task_projects(workspace_id, id)
    ON UPDATE CASCADE
    ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS calendar_project_sources_user_idx
  ON calendar_project_sources (workspace_id, user_id, enabled, order_index);
```

说明：

- `user_id` 表示这是用户个人配置。
- `workspace_id` 保证多用户 workspace 隔离。
- `project_id` 必须通过 `(workspace_id, project_id)` 指向当前 workspace 内的任务项目。
- `enabled` 表示是否显示在日历来源中。
- `color` 预留给日历来源颜色，第一版可以让前端给默认色，也可以允许用户选择。
- `order_index` 用于后续拖拽排序；第一版按创建顺序或项目名排序均可。
- `(workspace_id, user_id)` 外键保证配置只属于当前 workspace 的成员。即使数据库层已经约束，service 仍然要用当前 session 的 `workspace_id` 和 `user_id` 写入，不能信任请求体。

## 默认来源规则

日历来源由两部分组成：

1. 默认来源：所有 `type = personal` 的个人短期项目。
2. 用户配置来源：当前用户在 `calendar_project_sources` 中启用的 `regular` 和 `learning` 项目。

个人短期项目不需要写入 `calendar_project_sources` 也会显示。这样可以保证 `Personal · 个人` 在日历中始终可用。

如果用户手动把个人短期项目加入配置表，后端返回时需要去重，避免左侧重复显示。

## API 设计

### GET /api/calendar/project-sources

返回日历来源和可配置项目。

响应：

```json
{
  "sources": [
    {
      "project_id": "personal",
      "name": "Personal",
      "type": "personal",
      "enabled": true,
      "default": true,
      "color": "#c4742f",
      "order_index": 0
    },
    {
      "project_id": "n2-learning",
      "name": "日语N2考级",
      "type": "learning",
      "enabled": true,
      "default": false,
      "color": "#2e90fa",
      "order_index": 10
    }
  ],
  "available_projects": [
    {
      "project_id": "long-1",
      "name": "知识库重构",
      "type": "regular",
      "enabled": false
    }
  ]
}
```

语义：

- `sources` 是当前应该显示在日历左侧的来源。
- `available_projects` 是可加入日历但当前未启用的长期项目和学习项目。
- `default: true` 表示默认来源，用户不能移除，但可以在前端临时隐藏显示。

### PUT /api/calendar/project-sources

保存当前用户的日历项目配置。第一版采用配置面板的全量保存语义，避免取消勾选后旧配置仍然保留。

请求：

```json
{
  "sources": [
    {
      "project_id": "n2-learning",
      "enabled": true,
      "color": "#2e90fa",
      "order_index": 10
    },
    {
      "project_id": "long-1",
      "enabled": false,
      "color": "",
      "order_index": 20
    }
  ]
}
```

处理规则：

- 只允许保存 `type = regular` 和 `type = learning` 的项目配置。
- `type = personal` 的项目由默认规则管理，不需要通过 PUT 保存。
- 配置面板保存时必须提交所有可配置项目的状态：包括 `sources` 中 `default = false` 的长期/学习项目，以及 `available_projects` 中尚未启用的长期/学习项目。取消勾选必须显式发送 `enabled: false`。
- 服务端以请求体为准 upsert 当前用户配置。请求体中 `enabled: false` 的项目必须从 `sources` 中消失；可以保留禁用行，也可以删除配置行，但 `GET /api/calendar/project-sources` 的结果必须一致。
- 如果未来要支持局部保存，需要新增 PATCH 接口或 `operation` 字段，不能复用这个 PUT 语义。
- 如果项目不存在或不属于当前 workspace，忽略该项并记录 warn 日志。
- 返回最新的 `GET /api/calendar/project-sources` 结构。

## 日程 API 变更

### Event 模型

扩展 `model.Event`：

```go
ProjectID *string `json:"project_id"`
Project   *string `json:"project,omitempty"`
ProjectType *string `json:"project_type,omitempty"`
```

`Project` 和 `ProjectType` 用于前端展示，可由 repository join `task_projects` 填充。第一版如果为了控制范围，也可以只返回 `project_id`，前端再通过项目来源接口补展示名。

### CreateEventRequest

扩展创建请求：

```go
ProjectID *string `json:"project_id"`
```

创建规则：

- 前端传了有效 `project_id` 时，保存到 `events.project_id`。
- 前端没有传 `project_id` 但传了旧 `kind` 时，保留旧行为，用 `kind` 兼容。
- 如果 `project_id` 无效，返回 400，而不是静默创建到默认项目，避免用户以为日程已经进了某项目。

### UpdateEventRequest

扩展更新请求：

```go
ProjectID *string `json:"project_id,omitempty"`
```

更新规则：

- `project_id` 字段省略：不修改项目归属。
- `project_id` 为 JSON null：第一版按省略处理，不修改项目归属。当前 Go 的 `*string` 无法区分 omitted 和 null，因此不要把 null 设计成清空语义。
- `project_id` 为有效项目 ID：移动日程到该项目。
- `project_id` 为空字符串：清空项目归属。

如果后续产品需要 `null` 表示清空，后端需要先引入能记录字段是否出现的 patch 类型，例如 `PatchString{Set bool, Value *string}`，再调整 API 语义。

## 前端设计

### 日历左侧来源

左侧从硬编码的 `工作 / 个人 / 提醒` 改为：

```text
日历来源

个人短期项目
  Personal
  其它个人短期项目

已加入日历
  日语N2考级
  知识库重构

+ 配置项目
```

交互：

- 点击来源切换当前筛选。
- 第一版使用单选筛选，避免一次改太大；后续可扩展为多选显示。
- `个人短期项目` 分组默认存在。
- `已加入日历` 只显示用户配置启用的长期/学习项目。
- `+ 配置项目` 打开配置面板。

### 配置项目面板

面板按项目类型分组：

```text
配置日历项目

长期项目
  [ ] 知识库重构
  [x] 工作复盘

学习项目
  [x] 日语N2考级
  [ ] AI Infra 工程师

取消  保存配置
```

交互：

- 勾选长期项目或学习项目后保存。
- 保存调用 `PUT /api/calendar/project-sources`。
- 保存成功后刷新左侧来源和日历内容。
- 个人短期项目不出现在配置面板中，因为它们默认显示。

### 日历内容筛选

日历页加载：

1. `GET /api/calendar/project-sources`
2. `GET /api/events?month=YYYY-MM`

前端筛选逻辑：

- 当前选中个人短期项目：显示该 `project_id` 的日程。
- 当前选中已加入日历项目：显示该 `project_id` 的日程。
- 没有 `project_id` 的历史日程显示在“未归属日程” fallback 中，避免旧数据消失。第一版可以在左侧追加一个低优先级来源 `未归属日程`，仅当本月存在未归属日程时显示。

后端可在后续扩展 `GET /api/events` 支持 `project_id` 参数，把筛选下推到后端。第一版为了降低风险，可以继续按月份取回后前端筛选。

## 快速捕获

快速捕获选择项目时：

- 创建任务：继续写 `project_id` 到任务。
- 创建笔记：继续写 `project_ids`。
- 创建日程：改为写 `project_id` 到日程。

旧逻辑：

```ts
kind: selectedProject?.type === 'personal' ? 'personal' : 'work'
```

新逻辑：

```ts
project_id: selectedProjectID
kind: selectedProject?.type === 'personal' ? 'personal' : 'work'
```

`kind` 继续传是为了兼容旧后端、旧搜索展示和历史 UI，但日历来源不再依赖它。

如果快速捕获选择了一个未加入日历的长期/学习项目：

- 日程仍然创建成功，并归属到该项目。
- 日历页如果当前用户没有把该项目加入日历，默认不显示在左侧来源中。
- 日历配置面板中可以看到该项目并加入日历。

## 历史数据迁移

`events.project_id` 新增后，历史数据需要可见：

- `kind = 'personal'` 的旧日程：只按事件自己的 `workspace_id` 查找 canonical 项目 `task_projects(workspace_id, id = 'personal')`。存在则回填 `project_id = 'personal'`；不存在则保持未归属。
- `kind = 'work'` 的旧日程：不强行回填，因为无法判断属于哪个长期项目或学习项目。
- `kind = 'reminder'` 的旧日程：不强行回填，显示在 `未归属日程`。
- 不允许用“第一个 `type = personal` 项目”作为回填目标。多 workspace、多个人短期项目下这个规则不稳定，容易把历史日程归到错误项目。

迁移策略：

1. Schema 迁移新增 `project_id`。
2. 尝试回填个人项目。
3. 保留未归属 fallback。

这样不会误把历史工作日程塞到错误项目里。

## 搜索和每日视图影响

搜索：

- 事件搜索结果继续返回 `kind`。
- 后续可以增加 `project_id` / `project` 展示，但不阻塞本设计第一版。

今日页和每日总结：

- 如果它们只展示今日日程，不做来源筛选，可以先不改 UI。
- 如果需要展示项目标签，后端事件响应应补 `project` 名称。

## 权限和隔离

所有配置接口必须基于当前登录用户和当前 workspace：

- 用户只能读取自己的 `calendar_project_sources`。
- 用户只能配置当前 workspace 内的项目。
- 普通用户可以配置自己的日历来源。
- 管理员不应默认修改别人的日历来源，除非后续新增专门管理入口。
- 写入前必须确认 `(workspace_id, user_id)` 是有效 `workspace_members` 成员。数据库层使用复合外键约束；service 层仍然必须基于 session 校验，避免通过请求体伪造 workspace。

## 测试计划

### 后端

- Migration 测试：
  - SQLite 新库包含 `events.project_id` 和 `calendar_project_sources`。
  - PostgreSQL 新库包含同构字段和表。
  - SQLite 到 PostgreSQL 迁移复制 `calendar_project_sources`。

- Repository / service 测试：
  - 个人短期项目默认出现在来源列表。
  - 长期/学习项目只有启用后出现在来源列表。
  - `PUT /api/calendar/project-sources` 只保存当前用户配置。
  - 配置面板取消勾选项目并保存后，该项目从 `sources` 中消失。
  - `PUT /api/calendar/project-sources` 不允许跨 workspace 写入别人的项目来源。
  - 非 workspace member 不能读写该 workspace 的 project sources。
  - 不同用户配置互不影响。
  - 创建日程写入有效 `project_id`。
  - 无效 `project_id` 创建日程返回错误。
  - 更新日程时 `project_id` 省略和 JSON null 都不修改归属，空字符串会清空归属。
  - 删除项目后，关联 event 仍然可见，`workspace_id` 保留，只有 `project_id` 被清空。
  - 历史 `kind = 'personal'` 日程只回填同 workspace 的 canonical `personal` 项目；缺少 canonical 项目时保持未归属。

### 前端

- 日历页：
  - 左侧显示个人短期项目。
  - 配置面板显示长期项目和学习项目。
  - 保存配置后新项目出现在左侧来源。
  - 切换来源后只显示对应项目日程。
  - 未归属日程存在时显示 fallback 来源。

- 快速捕获：
  - 选择 `Personal · 个人` 创建日程时发送 `project_id: personal`。
  - 选择长期项目创建日程时发送对应 `project_id`。
  - 继续传兼容字段 `kind`。

## 实施顺序

1. 后端 schema 和 migration：`events.project_id`、`calendar_project_sources`。
2. 后端模型、repository、service、handler 和路由。
3. 前端 API：新增 calendar project sources API，扩展 event request/response 类型。
4. 快速捕获：创建日程时写 `project_id`。
5. 日历页：替换硬编码来源，新增配置项目面板。
6. 测试和回归验证。

## 风险和取舍

- 第一版使用单选来源筛选，而不是多选，是为了降低 UI 和数据状态复杂度。
- `kind` 保留兼容，不立即删除，避免影响搜索、旧数据和已有测试。
- 历史 `work` 日程不自动回填项目，避免错误归类。
- 用户配置保存到后端，会比 localStorage 多一些后端工作，但这是平台级配置的正确边界。
