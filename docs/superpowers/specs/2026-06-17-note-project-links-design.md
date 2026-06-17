# Note Project Links Design

**Goal:** 让一篇笔记可以归属到多个项目，并让项目、任务、笔记之间形成清晰、可查询、可测试的关系。

**Status:** 设计待审。本文档只定义方案，不包含实现代码。

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
    ProjectIDs []string `json:"project_ids"`
}
```

更新语义：

- `CreateNoteRequest.ProjectIDs` 为空或缺省：创建未归属项目的笔记。
- `UpdateNoteRequest.ProjectIDs == nil`：不修改项目归属。
- `UpdateNoteRequest.ProjectIDs` 是空数组：清空项目归属。
- `UpdateNoteRequest.ProjectIDs` 有值：用新集合替换旧集合。

为了区分“字段缺省”和“显式空数组”，Go 实现时需要自定义请求结构或使用指针：

```go
ProjectIDs *[]string `json:"project_ids"`
```

最终实现应采用指针形式，避免 PATCH 时误清空项目归属。

## Repository Contract

Notes repository 增加这些能力：

```go
type NoteFilter struct {
    FolderID   string
    ProjectID  string
    Unassigned bool
    Query      string
    Page       int
    PageSize   int
}

type NoteRepository interface {
    List(ctx context.Context, filter NoteFilter) ([]model.Note, int, error)
    GetByID(ctx context.Context, id string) (*model.Note, error)
    Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error)
    Update(ctx context.Context, id string, req *model.UpdateNoteRequest) (*model.Note, error)
    Delete(ctx context.Context, id string) error
}
```

实现要求：

- `Create` 必须在同一事务中写入 `notes`、`note_project_links`、`search_index`。
- `Update` 必须在同一事务中更新 `notes`、按需替换 `note_project_links`、刷新 `search_index`。
- `List` 和 `GetByID` 返回每篇笔记的 `projects`。
- `ProjectID` 和 `Unassigned` 同时出现时返回 400，避免语义冲突。
- 所有 `project_ids` 必须先校验存在；任何一个非法都不写入笔记或归属关系。

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
- `project_id` 和 `unassigned=true` 不能同时使用。
- `unassigned=true` 表示没有任何项目归属的笔记。

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

## 前端设计

### 笔记编辑页

在编辑页右侧或元信息区增加“所属项目”控件：

- 使用多选 chip。
- 数据源为 `/api/task-projects`。
- 已选项目显示为 `项目名 · 项目类型`。
- 点击 chip 的删除按钮移除项目。
- 下拉选择项目后立即加入本地表单状态。
- 保存笔记时提交完整 `project_ids`。
- 新建笔记时如果 URL 携带 `project_id`，默认选中该项目。

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
- 提供“新建项目笔记”按钮，跳转到新建笔记并带 `project_id=<activeProjectID>`。

第一阶段不做任务详情直接绑定笔记，避免把“任务-笔记一对一引用”和“项目-笔记多对多归属”混在一起。

## 搜索和同步

### 搜索

搜索结果可继续使用现有搜索逻辑。第一阶段不要求按项目搜索全文，但返回 note 结果时应包含项目归属，便于结果卡片显示项目 chip。

后续可以扩展：

```http
GET /api/search?q=postgres&project_id=project-1
```

### Notion / Obsidian 同步

第一阶段不把项目归属同步到外部系统，避免破坏当前“按同步标签筛选”的逻辑。

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

## 错误处理

- `project_ids` 中存在不存在的项目：返回 400，消息为 `project not found: <id>`。
- `project_id` 和 `unassigned=true` 同时用于列表查询：返回 400，消息为 `project_id and unassigned cannot be used together`。
- 更新笔记时部分项目非法：整个请求失败，不修改笔记和归属。
- 删除项目后再打开笔记：该项目 chip 自动消失，因为关联行已级联删除。

## TDD 验收范围

### 后端 contract tests

- 创建笔记时可以写入多个 `project_ids`。
- 获取笔记时返回完整 `projects`。
- 更新笔记时可以替换项目归属。
- 更新笔记时省略 `project_ids` 不改变项目归属。
- 更新笔记时传空数组会清空项目归属。
- 按 `project_id` 列表筛选只返回对应项目的笔记。
- `unassigned=true` 只返回没有项目归属的笔记。
- 删除项目后笔记仍存在，项目归属被移除。
- 删除笔记后 `note_project_links` 被清理。
- 非法项目 ID 返回明确错误，且不产生半写入。

### API tests

- `POST /api/notes` 支持 `project_ids`。
- `PATCH /api/notes/:id` 支持替换、清空、保留项目归属。
- `GET /api/notes?project_id=<id>` 支持项目筛选。
- `GET /api/notes?unassigned=true` 支持未归属筛选。
- 冲突查询参数返回 400。

### Frontend tests

- 编辑页显示已归属项目 chip。
- 编辑页可以添加项目 chip。
- 编辑页可以移除项目 chip。
- 保存笔记时提交完整 `project_ids`。
- 笔记列表可以按项目筛选。
- 笔记列表可以筛选未归属项目。
- 任务页选中项目后显示项目笔记。
- “新建项目笔记”会把当前项目带入新建笔记表单。

## 实施边界

第一阶段包含：

- 数据库关联表。
- 后端 API 和 repository 支持。
- 笔记编辑页项目多选。
- 笔记列表项目筛选。
- 任务页项目笔记只读入口。
- SQLite 和 PostgreSQL provider 同步支持。

第一阶段不包含：

- 外部同步项目字段。
- 任务详情直接绑定笔记。
- 项目笔记批量整理。
- 搜索按项目过滤。
- 项目统计报表。

这些能力可以在基础多对多关系稳定后单独设计。

## 自检结果

- 无单项目 `notes.project_id` 设计，满足“一篇笔记可以归属多个项目”。
- 删除项目不会删除笔记，只删除关联行。
- 文件夹、标签、项目三个概念边界清晰。
- PATCH 语义明确区分“省略字段”和“清空数组”。
- 第一阶段范围可独立实现和测试，没有依赖未来同步扩展。
