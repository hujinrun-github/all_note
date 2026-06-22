# Note Project Links Design

**Goal:** 让一篇笔记可以归属到多个项目，并让项目、任务、笔记之间形成清晰、可查询、可测试的关系。

**Status:** 设计待审。本文档只定义方案，不包含实现代码。

**Review decision:** 方案可进入实现，但必须同时覆盖现有 SQLite provider、PostgreSQL provider、SQLite 到 PostgreSQL 迁移、同步导入路径和前端创建入口，避免只改常规 notes API 后留下数据不一致。

## 当前状态

FlowSpace 现在已经有结构化项目表 `task_projects`，任务通过 `tasks.project_id` 归属到项目。学习项目通过 `learning_roadmaps.project_id` 关联 Roadmap，Roadmap 节点生成的任务仍然通过 `tasks.project_id` 属于对应学习项目。

笔记当前没有项目归属字段。`notes` 只通过 `folder_id` 归属到文件夹，并通过 `tags` 表达主题或同步筛选条件。`tasks.note_id` 和 `events.note_id` 在数据库中存在，但当前产品入口没有完整暴露，不能承担“项目下有哪些笔记”的主关系。

## 设计结论

使用独立关联表表达笔记和项目的多对多关系：

- 一个项目可以有多篇笔记。
- 一篇笔记可以属于多个项目。
- 笔记可以不属于任何项目，显示为“未归属项目”。
- 删除项目时只删除归属关系，不删除笔记。
- 删除笔记时自动删除归属关系。
- 文件夹和标签继续保留原语义，不和项目归属互相替代。

## 数据模型

### PostgreSQL

新增表：

```sql
CREATE TABLE note_project_links (
  note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL REFERENCES task_projects(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, project_id)
);

CREATE INDEX note_project_links_project_note_idx
  ON note_project_links (project_id, note_id);
```

说明：

- `PRIMARY KEY (note_id, project_id)` 防止重复归属。
- `project_id, note_id` 索引用于项目页、笔记列表按项目筛选。
- 不在 `notes` 表上增加 `project_id`，避免单项目限制。

### SQLite

SQLite provider 保持同构表：

```sql
CREATE TABLE IF NOT EXISTS note_project_links (
  note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL REFERENCES task_projects(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (note_id, project_id)
);

CREATE INDEX IF NOT EXISTS note_project_links_project_note_idx
  ON note_project_links (project_id, note_id);
```

SQLite `created_at` 使用 Unix 秒，和现有 SQLite schema 保持一致。

### 迁移文件影响

实现时需要同时修改：

- `backend/db/schema.sql`：为 SQLite 新库加入 `note_project_links`。
- `backend/db/migrations/postgres/0001_init_postgres.sql`：为 PostgreSQL 新库加入 `note_project_links`。
- `backend/internal/repository/db.go`：如果 legacy SQLite 初始化仍在使用，需要加入同构表。
- `backend/internal/migration/sqlite_to_pg.go`：SQLite 到 PostgreSQL 迁移需要复制已存在的 `note_project_links`，不能只创建空表。

SQLite 到 PostgreSQL 的迁移顺序：

1. 迁移 `notes`。
2. 迁移 `task_projects`。
3. 迁移 `note_project_links`。
4. 继续迁移 tasks、events、sync state 和 search index。

`note_project_links` 的迁移应在 notes 和 task_projects 都存在后执行，依赖外键校验暴露脏数据。如果 SQLite 中存在孤儿 link，迁移应该失败并报告具体 `note_id` / `project_id`，不要静默丢弃。

## 后端模型

新增轻量响应模型：

```go
type NoteProject struct {
    ID   string `json:"id"`
    Name string `json:"name"`
    Type string `json:"type"`
}
```

扩展 `model.Note` 响应字段：

```go
type Note struct {
    ID        string        `json:"id"`
    Title     string        `json:"title"`
    Body      string        `json:"body"`
    FolderID  string        `json:"folder_id"`
    Tags      string        `json:"tags"`
    Projects  []NoteProject `json:"projects"`
    CreatedAt int64         `json:"created_at"`
    UpdatedAt int64         `json:"updated_at"`
}
```

扩展创建和更新请求：

```go
type CreateNoteRequest struct {
    Title      string   `json:"title" binding:"required"`
    Body       string   `json:"body"`
    FolderID   string   `json:"folder_id"`
    Tags       string   `json:"tags"`
    ProjectIDs []string `json:"project_ids"`
}

type UpdateNoteRequest struct {
    Title      *string  `json:"title"`
    Body       *string  `json:"body"`
    FolderID   *string  `json:"folder_id"`
    Tags       *string  `json:"tags"`
    ProjectIDs *[]string `json:"project_ids"`
}
```

更新语义：

- `CreateNoteRequest.ProjectIDs` 为空或缺省：创建未归属项目的笔记。
- `UpdateNoteRequest.ProjectIDs == nil`：不修改项目归属。
- `UpdateNoteRequest.ProjectIDs` 是空数组：清空项目归属。
- `UpdateNoteRequest.ProjectIDs` 有值：采用 merge 策略——INSERT 新增的归属、DELETE 被移除的归属、保留已有的（不动 `created_at`），避免编辑正文不改项目归属时时间戳重置导致 UI 排序抖动。

`UpdateNoteRequest.ProjectIDs` 必须使用指针形式，避免 PATCH 省略字段时被误判为清空归属。

`project_ids` 处理规则：

- 写入前先去重，保留请求中的第一次出现顺序。
- 去重后校验项目存在性：不存在的项目 ID 静默丢弃（warn 日志），不拒绝整个请求。这样当项目在用户编辑期间被删除时，正文修改仍能成功保存。
- `personal`、`regular`、`learning` 三类项目都允许被选为笔记归属；前端显示名由 `frontend/src/utils/taskProjects.ts` 中的项目类型文案决定。
- 响应中的 `projects` 按 `task_projects.name ASC` 排序，保证 UI 展示稳定。

## Repository Contract

Notes repository 增加这些能力：

```go
type NoteFilter struct {
    FolderID   string
    ProjectID  string
    Unassigned bool
    Query      string
    Sort       string // "recent" | "az"
    Page       int
    PageSize   int
}

type NoteRepository interface {
    List(ctx context.Context, filter NoteFilter) ([]model.Note, int, error)
    GetByID(ctx context.Context, id string) (*model.Note, error)
    Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error)
    CreateWithID(ctx context.Context, note *model.Note) error
    Update(ctx context.Context, id string, req *model.UpdateNoteRequest) (*model.Note, error)
    Delete(ctx context.Context, id string) error
    ListAll(ctx context.Context) ([]model.Note, error)
    Recent(ctx context.Context, limit int) ([]model.Note, error)
}
```

实现要求：

- `Create` 必须在同一事务中写入 `notes` 和 `note_project_links`。PostgreSQL 同事务刷新 `search_index`；SQLite 继续依赖 `notes_fts` trigger，不新增 SQLite `search_index` 表。
- `CreateWithID` 用于 Obsidian / Notion 导入和测试种子，默认创建无项目归属的笔记；后续如果外部同步支持项目字段，再单独扩展请求模型。
- `Update` 必须在同一事务中更新 `notes`、按 merge 策略更新 `note_project_links`（INSERT 新增、DELETE 移除、保留已有）。PostgreSQL 同事务刷新 `search_index`；SQLite 继续依赖 `notes_fts` trigger。
- `List`、`GetByID`、`Recent` 返回每篇笔记的 `projects`。
- `ListAll` 默认不需要返回 `projects`，用于同步 diff 时保持轻量；如果后续同步导出需要项目字段，再扩展单独方法，避免现阶段给全量同步引入额外查询成本。
- `ProjectID` 和 `Unassigned` 同时出现时返回 400，避免语义冲突。
- `project_ids` 中不存在的 ID 静默丢弃；合法部分正常写入笔记和归属关系。

查询实现要求：

- 列表页先分页得到 notes，再用 note ids 批量查询 `note_project_links` + `task_projects` 并组装 `projects`，避免每篇笔记一次查询。
- `project_id` 筛选使用 `EXISTS` 或内连接到 `note_project_links`，总数查询和列表查询必须使用同一过滤语义。
- `unassigned=true` 使用 `NOT EXISTS`，避免后续 join 扩展时误判。
- `folder_id`、`project_id`、`unassigned` 可以和分页、排序组合；排序继续沿用现有 `recent` / `az` 语义。

## API 设计

### List Notes

现有：

```http
GET /api/notes?folder_id=__work&sort=recent&page=1&page_size=20
```

新增：

```http
GET /api/notes?project_id=project-1
GET /api/notes?unassigned=true
```

约束：

- `folder_id` 和 `project_id` 可以同时使用，表示取某个文件夹下属于某项目的笔记。
- `folder_id` 和 `unassigned=true` 可以同时使用，表示取某个文件夹下未归属任何项目的笔记。
- `project_id` 和 `unassigned=true` 不能同时使用。
- `unassigned=true` 表示没有任何项目归属的笔记。
- `unassigned=false` 或缺省都不启用未归属筛选。
- `project_id` 为空字符串时按缺省处理，不启用项目筛选。
- `project_id` 指向不存在的项目时返回空结果集，不返回 400。

### Create Note

```http
POST /api/notes
Content-Type: application/json

{
  "title": "PostgreSQL 迁移笔记",
  "body": "...",
  "folder_id": "__uncategorized",
  "tags": "[\"db\"]",
  "project_ids": ["project-1", "learning-1"]
}
```

响应：

```json
{
  "note": {
    "id": "note-1",
    "title": "PostgreSQL 迁移笔记",
    "body": "...",
    "folder_id": "__uncategorized",
    "tags": "[\"db\"]",
    "projects": [
      { "id": "project-1", "name": "AI Infra", "type": "regular" },
      { "id": "learning-1", "name": "AI Infra学习", "type": "learning" }
    ],
    "created_at": 1781625600,
    "updated_at": 1781625600
  }
}
```

### Update Note

替换项目归属：

```http
PATCH /api/notes/note-1
Content-Type: application/json

{
  "project_ids": ["project-1"]
}
```

清空项目归属：

```http
PATCH /api/notes/note-1
Content-Type: application/json

{
  "project_ids": []
}
```

不修改项目归属：

```http
PATCH /api/notes/note-1
Content-Type: application/json

{
  "title": "新的标题"
}
```

### API 错误分类

Handler 不能把所有 repository/service 错误都映射成 404 或 500。实现需要定义可判定错误类型，至少覆盖：

- `ErrNoteNotFound`：`GET/PATCH /api/notes/:id` 返回 404。
- `ErrInvalidNoteFilter`：`GET /api/notes` 返回 400，消息为 `project_id and unassigned cannot be used together`。
- tag JSON 非法：沿用当前 notes 行为，如果请求需要校验则返回 400。

非法 `project_ids` 在事务开始前静默丢弃（warn 日志），合法部分写入。更新笔记时如果 note 不存在，也不应创建任何 link。

## 前端设计

### 笔记编辑页

在编辑页右侧或元信息区增加“所属项目”控件：

- 使用多选 chip。
- 数据源为 `/api/task-projects`（第一阶段假设项目数量有限，后续需支持搜索/分页）。
- 已选项目显示为 `项目名 · 项目类型`。
- 点击 chip 的删除按钮移除项目。
- 下拉选择项目后立即加入本地表单状态。
- 保存笔记时提交完整 `project_ids`。
- 编辑器自动保存时也必须提交当前完整 `project_ids`，否则用户只改项目不改正文时不会保存。
- 进入已有笔记时，用响应里的 `projects` 初始化本地选中状态。
- 如果项目在用户编辑期间被删除，服务端静默丢弃无效 ID，保存成功；前端下次加载笔记时该 chip 自动消失。

### 新建项目笔记入口

第一阶段不新增独立 `/notes/new` 草稿页。任务页点击“新建项目笔记”时直接调用：

```http
POST /api/notes
Content-Type: application/json

{
  "title": "未命名笔记",
  "body": "",
  "folder_id": "__uncategorized",
  "tags": "[]",
  "project_ids": ["<activeProjectID>"]
}
```

创建成功后跳转到 `/editor/<note-id>`。这样可以复用当前“先创建、再编辑”的前端模式，并避免引入草稿状态。

### 笔记列表页

新增项目筛选：

- 全部笔记。
- 未归属项目。
- 每个项目一个筛选项，显示 `项目名 · 项目类型`。

笔记卡片展示：

- 显示最多 2 个项目 chip。
- 超过 2 个显示 `+N`。
- 没有归属时不显示项目 chip，避免卡片噪音。

### 任务页项目视角

在任务页选中项目时增加“项目笔记”入口：

- 对普通项目和学习项目都显示。
- 读取 `GET /api/notes?project_id=<activeProjectID>&page_size=6`。
- 只显示标题、更新时间、项目 chip。
- 点击进入编辑页。
- 提供“新建项目笔记”按钮，按上面的创建 API 直接创建并带入当前项目。
- 项目笔记模块不影响任务筛选状态；切换项目时重新请求对应项目笔记。

第一阶段不做任务详情直接绑定笔记，避免把“任务-笔记一对一引用”和“项目-笔记多对多归属”混在一起。

## 搜索和同步

### 搜索

搜索结果可继续使用现有搜索逻辑。第一阶段不要求按项目搜索全文。搜索结果模型当前不是完整 `Note` 响应，第一阶段不强制在搜索结果中展示项目 chip，避免把搜索 repository 也卷入本次改造。

后续可以扩展：

```http
GET /api/search?q=postgres&project_id=project-1
```

### Notion / Obsidian 同步

第一阶段不把项目归属同步到外部系统，避免破坏当前“按同步标签筛选”的逻辑。

同步路径要求：

- Obsidian / Notion 导入新笔记时通过 `CreateWithID` 或等价路径创建无项目归属笔记。
- Obsidian / Notion 更新已有笔记时，如果请求省略 `project_ids`，必须保留原有项目归属。
- 批量同步使用 `ListAll` 时不需要读取项目归属，避免改变当前同步性能和外部文件内容。

保留后续扩展点：

- Notion：把 FlowSpace 项目同步成 multi-select 属性。
- Obsidian：把项目同步成 frontmatter，例如 `flowspace_projects: ["AI Infra"]`。

同步导入外部笔记时，默认 `project_ids` 为空，除非未来用户在导入选项中指定默认项目。

## 迁移策略

新表上线后不自动给历史笔记分配项目。原因：

- 当前历史笔记没有可靠的项目来源。
- 用文件夹或标签猜项目会引入错误归属。
- 未归属状态比错误归属更容易让用户批量整理。

可提供后续整理入口：

- 笔记列表筛选“未归属项目”。
- 用户批量选择笔记后设置项目归属。

已有用户从 SQLite 切换到 PostgreSQL 时，如果 SQLite 已经包含 `note_project_links`，迁移工具必须复制这些显式归属关系。这里“不自动分配历史笔记”只表示不基于 folder/tag 猜测生成新关系，不表示丢弃用户已经设置的关系。

## 错误处理

- `project_ids` 中存在不存在的项目：静默丢弃无效 ID（warn 日志），不拒绝请求。
- `project_id` 和 `unassigned=true` 同时用于列表查询：返回 400，消息为 `project_id and unassigned cannot be used together`。
- 更新笔记时部分项目非法：静默丢弃无效 ID，合法部分正常写入。
- 创建笔记时部分项目非法：静默丢弃无效 ID，合法部分正常写入。
- `project_ids` 中重复 ID：服务端去重，不返回错误。
- 删除项目后再打开笔记：该项目 chip 自动消失，因为关联行已级联删除。

## TDD 验收范围

### 后端 contract tests

- 创建笔记时可以写入多个 `project_ids`。
- 获取笔记时返回完整 `projects`。
- 最近笔记 `Recent` 返回完整 `projects`。
- 全量同步列表 `ListAll` 不要求返回 `projects`，并且不因新增字段失败。
- 更新笔记时可以替换项目归属。
- 更新笔记时省略 `project_ids` 不改变项目归属。
- 更新笔记时传空数组会清空项目归属。
- 按 `project_id` 列表筛选只返回对应项目的笔记。
- `unassigned=true` 只返回没有项目归属的笔记。
- `project_id` 和 `unassigned=true` 同时出现时返回可映射到 400 的错误。
- 删除项目后笔记仍存在，项目归属被移除。
- 删除笔记后 `note_project_links` 被清理。
- 非法项目 ID 被静默丢弃，合法部分正常写入，不产生半写入。
- 重复 `project_ids` 被去重，最终只产生一条归属关系。
- SQLite provider 创建和更新笔记后，现有 FTS 搜索仍可命中新正文。
- PostgreSQL provider 创建和更新笔记后，`search_index` 仍被同事务刷新。

### API tests

- `POST /api/notes` 支持 `project_ids`。
- `PATCH /api/notes/:id` 支持替换、清空、保留项目归属。
- `GET /api/notes?project_id=<id>` 支持项目筛选。
- `GET /api/notes?unassigned=true` 支持未归属筛选。
- 冲突查询参数返回 400。
- 非法 `project_ids` 被静默丢弃，保存成功（非 400/404/500）。

### Migration tests

- SQLite 新 schema 包含 `note_project_links`。
- PostgreSQL 新 schema 包含 `note_project_links`。
- SQLite 到 PostgreSQL 迁移会复制已有 `note_project_links`。
- 迁移后的 link 仍满足删除项目只删除归属、不删除笔记。
- 迁移目标库非空校验包含 `note_project_links`，避免重复迁移产生脏数据。

### Sync tests

- Obsidian 导入新笔记时默认没有项目归属。
- Notion 导入新笔记时默认没有项目归属。
- Obsidian 更新已有笔记时保留原项目归属。
- Notion 更新已有笔记时保留原项目归属。

### Frontend tests

- 编辑页显示已归属项目 chip。
- 编辑页可以添加项目 chip。
- 编辑页可以移除项目 chip。
- 保存笔记时提交完整 `project_ids`。
- 自动保存正文时不会丢失项目归属。
- 只修改项目归属并保存时可以成功更新。
- 笔记列表可以按项目筛选。
- 笔记列表可以筛选未归属项目。
- 任务页选中项目后显示项目笔记。
- “新建项目笔记”会创建带当前项目归属的笔记并跳转编辑页。

## 实施边界

第一阶段包含：

- 数据库关联表。
- 后端 API 和 repository 支持。
- 笔记编辑页项目多选。
- 笔记列表项目筛选。
- 任务页项目笔记只读入口。
- SQLite 和 PostgreSQL provider 同步支持。
- SQLite 到 PostgreSQL 迁移支持。
- Obsidian / Notion 导入和更新路径保留项目归属边界。

第一阶段不包含：

- 外部同步项目字段。
- 任务详情直接绑定笔记。
- 项目笔记批量整理。
- 搜索按项目过滤。
- 项目统计报表。
- 搜索结果卡片展示项目 chip。
- 独立新建笔记草稿页。

这些能力可以在基础多对多关系稳定后单独设计。

## 自检结果

- 无单项目 `notes.project_id` 设计，满足“一篇笔记可以归属多个项目”。
- 删除项目不会删除笔记，只删除关联行。
- 文件夹、标签、项目三个概念边界清晰。
- PATCH 语义明确区分“省略字段”和“清空数组”。
- SQLite 和 PostgreSQL 的搜索索引机制差异已明确，不会误要求 SQLite 写入 `search_index`。
- 现有 `CreateWithID`、`ListAll`、`Recent` 路径已纳入边界，不会破坏同步导入和仪表盘最近笔记。
- SQLite 到 PostgreSQL 迁移会保留用户已设置的项目归属，但不会为历史笔记自动猜测归属。
- 第一阶段范围可独立实现和测试，没有依赖未来同步扩展。
