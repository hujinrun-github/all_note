# FlowSpace 单笔记单同步目标设计

日期：2026-06-18

## 背景

当前 FlowSpace 已经支持 Obsidian 和 Notion 同步配置，底层通过 `sync_targets` 保存同步目标，通过 `note_sync_state` 保存笔记与外部资源的同步状态。现有实现的问题是产品层仍按“每种类型一个默认目标”工作：编辑器同步卡片只展示一个 Obsidian 和一个 Notion 入口，服务层也大量使用 `GetDefaultSyncTarget(type)`。

新的产品目标是允许用户维护多个同步配置，例如多个 Notion Data Source 或多个 Obsidian Vault/目录，但每篇 FlowSpace 笔记只能归属一个同步配置。编辑器中的笔记绑定位置应直接选择同步目标名，而不是先选择 Notion/Obsidian 类型；默认行为仍然是不同步。

## 目标

- 支持多个 `sync_targets` 配置，并在同步设置页按目标名称管理。
- 一篇 FlowSpace 笔记最多只能绑定一个同步目标。
- 编辑器同步卡片通过目标名称下拉框选择当前笔记的同步目标。
- 一个外部资源当前只能被一个同步配置管理：Obsidian 文件按规范化绝对路径识别，Notion page 按 page id 识别。
- 没有绑定同步目标的笔记不参与自动或批量 push。
- 单篇同步按当前绑定目标执行，不再默认同时尝试 Obsidian 和 Notion。
- 保留旧同步状态作为历史记录，但 UI 和同步执行只读取当前绑定目标的状态。

## 非目标

- 不实现一篇笔记同步到多个目标。
- 不做跨目标内容合并，也不做两个外部系统之间的三方冲突解决。
- 不自动删除旧同步目标中的外部文件或 Notion page。
- 不在第一版实现后台定时同步调度。
- 不改变 Notion 优先、Obsidian 优先等既有双向同步冲突策略。

## 推荐方案

采用“多个配置、单个绑定、外部资源声明”的模型：

1. `sync_targets` 继续作为同步配置表，允许同一用户拥有多个 Obsidian 和 Notion 配置。
2. 新增 `note_sync_bindings`，记录每篇笔记当前绑定的唯一目标。
3. 新增 `sync_external_claims`，记录当前被 FlowSpace 管理的外部资源，防止同一个外部文件或 page 被多个同步配置同时接管。
4. `note_sync_state` 继续保存每个 `(note_id, target_id)` 的同步状态、hash、外部链接和错误信息，不承担“当前归属”的唯一性判断。

这个方案比直接在 `note_sync_state` 上加唯一约束更稳：`note_sync_state` 可以保留历史状态，绑定和外部资源占用则表达当前事实。

## 数据模型

### sync_targets

`sync_targets` 保持现有主体结构。PostgreSQL 和 SQLite provider 都必须提供同等约束：`type` 只允许 `obsidian`、`notion`，并且同一类型下 `name` 唯一。当前 PostgreSQL migration 已经有 `UNIQUE(type, name)`，SQLite schema 需要补同等 unique index，避免可插拔 storage provider 的行为分叉。

后续可选字段：

```sql
ALTER TABLE sync_targets
  ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT false;
```

`is_default` 只用于兼容旧入口，不影响编辑器绑定。每种 `type` 最多一个默认目标，可通过 partial unique index 约束：

```sql
CREATE UNIQUE INDEX sync_targets_one_default_per_type_idx
  ON sync_targets (type)
  WHERE is_default = true;
```

如果第一版不需要保留旧默认入口，也可以暂不增加 `is_default`，继续用 `updated_at DESC` 兼容旧 API。

### note_sync_bindings

```sql
CREATE TABLE note_sync_bindings (
  note_id TEXT PRIMARY KEY REFERENCES notes(id) ON DELETE CASCADE,
  target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (note_id, target_id)
);

CREATE INDEX note_sync_bindings_target_idx
  ON note_sync_bindings (target_id, updated_at DESC);
```

规则：

- `note_id PRIMARY KEY` 是一篇笔记只能绑定一个同步目标的硬约束。
- `UNIQUE(note_id, target_id)` 是给 `sync_external_claims` 组合外键使用的数据库约束；虽然业务上被 `PRIMARY KEY(note_id)` 覆盖，仍然需要显式存在。
- 没有绑定记录表示这篇笔记不同步。
- 更换同步目标时更新同一行的 `target_id`，不是新增绑定。
- 更换同步目标前必须先释放旧 claim；如果旧 claim 仍存在，数据库应拒绝直接把 binding 更新到新 target。
- 删除绑定时不删除 `note_sync_state`，只停止后续同步。
- 删除 `sync_targets` 时如果仍有绑定，应返回冲突提示，而不是级联删除绑定。

第一版不在 binding 上保存 `sync_mode`。绑定即表示这篇笔记允许被 push 到该 target；pull/import 是 target 维度的手动动作，不作为单篇笔记绑定属性。这样可以避免 `push`、`pull`、`bidirectional` 在单篇同步和批量同步里的解释不一致。

### sync_external_claims

```sql
CREATE TABLE sync_external_claims (
  external_key TEXT PRIMARY KEY,
  note_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  external_type TEXT NOT NULL CHECK (external_type IN ('obsidian_file', 'notion_page')),
  external_id TEXT NOT NULL DEFAULT '',
  external_path TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (note_id),
  FOREIGN KEY (note_id, target_id)
    REFERENCES note_sync_bindings(note_id, target_id)
    ON DELETE CASCADE
);

CREATE INDEX sync_external_claims_target_idx
  ON sync_external_claims (target_id, updated_at DESC);
```

`external_key` 由同步服务生成：

- Obsidian：`obsidian:` + 规范化真实绝对路径。路径需要解析符号链接、统一大小写策略，并保证在目标 base folder 内。
- Notion：`notion:` + Notion page id。page id 需要统一成无连字符或有连字符的单一格式。

规则：

- 同一个 `external_key` 只能存在一条 active claim，因此同一个外部文件或 page 不能同时属于两个配置。
- 同一篇笔记只能有一个 active claim，和“单绑定”保持一致。
- claim 的 `(note_id, target_id)` 必须引用当前 `note_sync_bindings`。解除笔记绑定时 claim 由数据库级联删除，服务层漏删不会留下 active claim。
- 更换目标后，旧外部文件或 Notion page 不会自动删除，也不会继续被当前笔记管理。

## API 设计

### 同步目标管理

保留现有接口：

```http
GET   /api/sync/targets
POST  /api/sync/targets
PATCH /api/sync/targets/:id
```

新增删除接口：

```http
DELETE /api/sync/targets/:id
```

删除规则：

- 如果目标存在 `note_sync_bindings`，返回 `409 Conflict`，提示先迁移或解除绑定。
- 如果目标没有绑定，可以删除，并级联清理该目标下的历史 `note_sync_state` 和 active claims。

### 笔记绑定

新增：

```http
GET    /api/notes/:id/sync-binding
PUT    /api/notes/:id/sync-binding
DELETE /api/notes/:id/sync-binding
```

`GET` 返回当前绑定、可选历史候选和当前状态：

```json
{
  "binding": {
    "note_id": "note-1",
    "target_id": "target-1",
    "updated_at": 1781740000
  },
  "target": {
    "id": "target-1",
    "type": "notion",
    "name": "个人 Notion 知识库"
  },
  "state": {
    "note_id": "note-1",
    "target_id": "target-1",
    "status": "synced"
  },
  "candidates": []
}
```

`PUT` 请求：

```json
{
  "target_id": "target-1",
  "expected_target_id": "old-target",
  "confirm_changed_target": true
}
```

行为：

- `target_id` 必须存在且 `enabled=true`。
- 如果笔记已有绑定，则替换原绑定。
- 如果笔记已有绑定且 `target_id` 不同，前端必须先展示确认提示，再提交 `confirm_changed_target=true`。没有确认时返回 `409 Conflict`，避免误点后已经释放旧 claim。
- `expected_target_id` 用于乐观并发校验；如果当前绑定和请求里的旧 target 不一致，返回 `409 Conflict`，前端重新加载绑定状态。
- 如果原绑定存在 active claim，更换目标必须在同一事务里先释放旧 claim，再更新 binding。
- API 可以返回 `changed_target=true` 供前端更新提示文案，但提示不能只发生在 PUT 成功之后。

`DELETE` 行为：

- 删除当前绑定。
- 释放 active claim。
- 不删除历史 `note_sync_state`。

### 同步执行

新增统一单篇同步入口：

```http
POST /api/sync/notes/:id
```

行为：

- 如果笔记没有绑定，返回 `400 Bad Request`，提示“当前笔记未选择同步目标”。
- 根据绑定目标的 `type` 分发到 Obsidian 或 Notion 单篇同步服务。
- 单篇同步固定执行 push 语义：以 FlowSpace 当前笔记内容写入绑定目标。pull/import 只能通过 target 维度的手动接口执行。
- 已显式绑定的笔记不再被 `required_tags` 二次过滤。`required_tags` 只用于推荐/发现可同步笔记或限制外部导入范围，不能让用户手动绑定的笔记在 push 时被静默跳过。
- 同步状态写入当前绑定目标对应的 `note_sync_state`。
- `sync_external_claims` 按 provider 的外部副作用规则预留、更新或恢复，不能只在外部写入成功后才尝试声明。

批量目标同步建议新增目标维度接口：

```http
POST /api/sync/targets/:target_id/push
POST /api/sync/targets/:target_id/pull
POST /api/sync/targets/:target_id/bidirectional
GET  /api/sync/targets/:target_id/deletions
POST /api/sync/targets/:target_id/deletions/:note_id/confirm
POST /api/sync/targets/:target_id/deletions/:note_id/restore
```

行为：

- push 只处理绑定到该 `target_id` 且启用同步的笔记。
- pull/import 可以从外部目标导入新笔记，导入后自动创建 `note_sync_bindings`。
- 如果外部资源已经被其他 target claim，跳过并返回 `external_claim_conflict`。
- confirm/restore 必须显式带 `target_id`，不能再通过类型默认目标查找删除候选。多目标存在时，旧的 `/api/sync/obsidian/deletions/:note_id/...` 和 `/api/sync/notion/deletions/:note_id/...` 只能作为默认目标兼容入口。

兼容旧接口：

```http
GET  /api/notes/:id/sync-state
POST /api/sync/obsidian/notes/:id
POST /api/sync/notion/notes/:id
```

兼容规则：

- `GET /api/notes/:id/sync-state` 默认返回当前绑定目标的状态；如果没有绑定且传了 `target=notion|obsidian`，才按旧默认目标逻辑查询。
- 旧的类型单篇同步接口如果没有 `target_id`，先检查笔记当前绑定是否为对应类型；不匹配则返回 `409 Conflict`。
- 如果传入 `target_id`，必须和当前绑定一致，否则返回 `409 Conflict`。

## 后端服务设计

新增同步绑定 repository 能力：

- `GetNoteSyncBinding(noteID)`
- `SaveNoteSyncBinding(noteID, targetID)`
- `DeleteNoteSyncBinding(noteID)`
- `ListBoundNotes(targetID)`
- `ClaimExternalResource(noteID, targetID, externalType, externalID, externalPath)`
- `ReleaseExternalClaim(noteID)`
- `GetExternalClaim(externalKey)`

服务层分工：

- `sync_binding_service`：处理绑定、解绑、更换目标、释放 claim。
- `sync_dispatch_service`：根据绑定目标类型分发单篇同步。
- Obsidian/Notion 现有服务只负责对应 provider 的读写和冲突规则，不再自己选择默认目标。

事务要求：

- 更换绑定和释放旧 claim 必须同事务。
- 单篇同步的 `note_sync_state` 写入和 `sync_external_claims` 更新必须同事务，避免状态成功但资源声明缺失。
- 导入外部笔记时，创建 note、创建 binding、写 sync state、写 claim 必须同事务。

### 外部副作用和 claim 顺序

数据库事务不能包住 Obsidian 文件写入或 Notion HTTP 请求，因此不能只写“同步成功后写 claim”。第一版必须按 provider 区分处理：

- Obsidian 新建或覆盖文件前，服务必须先计算最终输出路径和 `external_key`，在数据库事务里确认 binding 存在并预留 claim。预留失败时不允许写文件。文件写入失败时，如果 claim 是本次新建的，必须释放该 claim 并记录失败状态；如果 claim 已存在，只记录失败状态，不释放既有归属。
- Obsidian 写文件成功后，再在数据库事务里更新 `note_sync_state`。如果状态落库失败，下一次同步仍能通过已预留 claim 找回外部文件，不会再次创建重复文件。
- Notion 更新已有 page 前，page id 已知，服务必须先检查或预留 `notion:<page_id>` claim，再发起远端更新。
- Notion 新建 page 前 page id 不可知，无法预先 claim。服务必须在创建 page 时写入可恢复的 FlowSpace ID 属性；page 创建成功后立即在数据库事务里写入 binding、state 和 claim。如果落库失败，返回可重试错误，并在下一次同步时先通过 FlowSpace ID 查询并收养该 page，而不是再创建一个新 page。
- Notion 新建 page 后如果发现 claim 冲突，不自动删除远端 page；返回 `external_claim_conflict`，并把 page id/url 放入结果明细，便于用户或后续重试恢复。

这套规则把“外部已写、本地未记录”的半成功压到可恢复状态，而不是假设数据库事务可以覆盖外部副作用。

## 前端设计

### 同步设置页

同步设置页从“Obsidian / Notion 单配置表单”升级为“目标管理”：

- 顶部保留 Obsidian / Notion 分组或筛选。
- 每个目标以配置行展示：目标名称、类型、启用状态、自动同步状态、最近更新时间。
- 支持新增、编辑、测试连接、停用、删除。
- 删除有绑定笔记的目标时，显示冲突提示和绑定数量。

第一版可以保留现有 Obsidian/Notion 表单作为编辑抽屉或弹窗，不需要重做全部表单样式。

### 编辑器同步卡片

笔记绑定位置改为单选下拉：

```text
同步目标
[ 不同步                           v ]
[ 个人 Notion 知识库 · Notion       ]
[ 工作资料库 · Notion              ]
[ 本地 Obsidian Vault · Obsidian    ]
```

规则：

- 下拉选项来自 `GET /api/sync/targets` 中 `enabled=true` 的目标。
- 展示文案优先使用目标名称，后缀显示目标类型。
- 只能选择一个目标。
- 选择“不 同步”调用 `DELETE /api/notes/:id/sync-binding`。
- 选择目标调用 `PUT /api/notes/:id/sync-binding`。
- 已有绑定时更换目标，前端展示确认提示：旧外部文件或 Notion 页面不会自动删除，后续只同步到新目标。
- 同步状态区只展示当前绑定目标的状态。
- 同步按钮只调用 `POST /api/sync/notes/:id`。

空状态：

- 没有任何同步目标：提示“还没有同步配置”，提供打开同步设置入口。
- 有同步目标但当前笔记未绑定：提示“当前笔记未开启同步”。
- 当前绑定目标被停用：显示“同步目标已停用”，同步按钮禁用。

## 外部冲突规则

### FlowSpace ID 绑定冲突

Obsidian frontmatter 中的 FlowSpace `id` 和 Notion `FlowSpace ID` 属性只能用于定位同一个绑定目标下的笔记。pull/import 时如果外部资源声明了某个本地 note id，服务必须先读取该 note 的当前 binding：

- note 未绑定：可以把该 note 绑定到当前 target，并继续 pull/import。
- note 已绑定到当前 target：可以按既有冲突规则更新。
- note 已绑定到其他 target：必须返回 `binding_conflict` 并跳过，不得用目标 B 的外部内容更新目标 A 管理的笔记。
- note 不存在：按 provider 原有导入规则创建新 note，并绑定当前 target。

这个检查必须发生在内容写入 FlowSpace note 之前，避免带有旧 FlowSpace ID 的外部文件或 page 覆盖错误笔记。

### Obsidian

扫描文件时生成 `external_key = obsidian:<canonical_abs_path>`。

- claim 不存在：可以导入或绑定。
- claim 属于当前 note + target：正常同步。
- claim 属于当前 target 的另一个 note：跳过，返回文件重复绑定冲突。
- claim 属于另一个 target：跳过，返回“该文件已由其他同步配置管理”。

如果两个 Obsidian 配置目录重叠，同一个文件只会被第一个已 claim 的配置管理，另一个配置扫描时必须跳过并报告冲突。

### Notion

读取 page 时生成 `external_key = notion:<page_id>`。

- claim 不存在：可以导入或绑定。
- claim 属于当前 note + target：正常同步。
- claim 属于当前 target 的另一个 note：跳过，返回 page 重复绑定冲突。
- claim 属于另一个 target：跳过，返回“该 Notion 页面已由其他同步配置管理”。

## 迁移策略

新增表后，从历史 `note_sync_state` 尝试生成初始绑定：

- 如果一篇笔记只有一个 `note_sync_state` 且目标仍存在，自动创建 `note_sync_bindings`。
- 如果一篇笔记有多个历史同步状态，不自动选择目标，保持未绑定；`GET /api/notes/:id/sync-binding` 返回这些历史状态作为 `candidates`，让用户手动选择。
- 对有明确 `external_path` 或 `external_id` 的历史状态，只有自动绑定成功时才创建 active claim。
- 迁移过程不删除任何历史 `note_sync_state`。

SQLite 和 PostgreSQL 都需要同等 schema 支持，保持可插拔 storage provider 的契约一致。

### Storage provider 契约

PostgreSQL 和 SQLite 必须同时满足：

- `sync_targets(type, name)` 唯一。
- `note_sync_bindings(note_id)` 主键唯一。
- `note_sync_bindings(note_id, target_id)` 唯一，用于 claim 组合外键。
- `sync_external_claims.external_key` 主键唯一。
- `sync_external_claims(note_id)` 唯一。
- `sync_external_claims(note_id, target_id)` 外键引用 `note_sync_bindings(note_id, target_id)`，并 `ON DELETE CASCADE`。

SQLite 的 `PRAGMA foreign_keys` 必须在测试和运行时开启。storage contract tests 要同时跑 PostgreSQL 和 SQLite provider，不能只依赖 PostgreSQL 约束。

### SQLite 到 PostgreSQL 迁移

`sqlite_to_pg` 迁移清单必须加入：

- `note_sync_bindings`
- `sync_external_claims`

迁移前预检需要新增：

- SQLite 中 `sync_targets(type,name)` 是否重复。
- 历史 `note_sync_state.last_direction` 是否包含 PostgreSQL CHECK 不允许的值。
- 当前代码使用 `delete_detected`，PostgreSQL CHECK 和迁移预检必须允许 `delete_detected`，或者在迁移时统一清洗成同一个枚举值。第一版建议统一允许并保留 `delete_detected`，避免历史状态丢语义。
- 自动生成 binding 时，如果同一 note 有多个历史 target，不创建 binding，也不创建 claim。
- 自动生成 claim 时，必须先成功创建对应 binding，依赖组合外键兜底。

## 错误处理

- `400 Bad Request`：笔记未绑定同步目标、目标类型不支持、目标已停用。
- `404 Not Found`：笔记或同步目标不存在。
- `409 Conflict`：传入目标和当前绑定不一致、删除仍有绑定的目标、外部资源已被其他配置 claim。
- `500 Internal Server Error`：存储故障或 provider 内部错误。

错误信息必须能在前端直接展示，避免只返回泛化的“请求失败”。

## 测试计划

### 后端 TDD

先写失败测试，再实现：

- repository/storage contract：创建、读取、替换、删除 `note_sync_bindings`。
- repository/storage contract：`note_id` 唯一约束阻止一篇笔记绑定多个目标。
- repository/storage contract：`sync_external_claims.external_key` 唯一约束阻止一个外部资源被多个 target 管理。
- repository/storage contract：删除 binding 后，组合外键级联删除对应 claim。
- repository/storage contract：没有当前 binding 时不能插入 claim。
- repository/storage contract：SQLite 和 PostgreSQL 都拒绝重复的 `sync_targets(type,name)`。
- handler：`GET/PUT/DELETE /api/notes/:id/sync-binding` 的成功、未找到、目标停用和更换目标场景。
- handler：更换目标时缺少 `confirm_changed_target=true` 返回 409。
- handler：`expected_target_id` 和当前绑定不一致时返回 409。
- handler：`POST /api/sync/notes/:id` 在未绑定时返回 400。
- handler：target-scoped deletion confirm/restore 使用指定 `target_id`，不走默认 target。
- service：单篇同步按当前绑定目标分发到 Obsidian 或 Notion。
- service：已绑定笔记执行 push 时不被 `required_tags` 过滤跳过。
- service：传入不匹配的 `target_id` 返回 409。
- service：pull/import 遇到已绑定到其他 target 的 FlowSpace ID 时返回 `binding_conflict`，不得更新该 note。
- service：Obsidian 写文件前必须先 reserve claim；reserve 失败时不写文件。
- service：Notion 新建 page 后本地落库失败时，下一次同步能通过 FlowSpace ID 收养已创建 page，不重复创建。
- Obsidian：重叠目录扫描时，对已被其他 target claim 的文件返回冲突并跳过。
- Notion：导入 page 时，对已被其他 target claim 的 page 返回冲突并跳过。
- migration：单历史状态自动生成绑定，多历史状态不自动绑定并保留 candidates。
- migration：`note_sync_bindings` 和 `sync_external_claims` 进入 SQLite 到 PostgreSQL 迁移清单。
- migration：`delete_detected` 在 PostgreSQL CHECK 和预检中保持一致。

### 前端测试

- 同步卡片展示目标名下拉框，而不是多个类型卡片。
- 下拉框只允许选择一个同步目标。
- 选择目标后调用 `PUT /api/notes/:id/sync-binding`。
- 选择“不 同步”后调用 `DELETE /api/notes/:id/sync-binding`。
- 更换已绑定目标时显示确认提示。
- 取消更换确认时不调用 `PUT /api/notes/:id/sync-binding`。
- 未绑定时同步按钮提示先选择同步目标。
- 绑定目标后，同步按钮调用 `POST /api/sync/notes/:id`。
- 同步设置页可以显示多个 Notion 和多个 Obsidian 配置。
- 删除有绑定笔记的目标时显示冲突提示。

### 手动验收

- 创建两个 Notion 同步目标，编辑同一篇笔记时只能选择其中一个。
- 创建两个 Obsidian 同步目标，编辑同一篇笔记时只能选择其中一个。
- 未绑定笔记不会被目标批量 push。
- 绑定到目标 A 后执行单篇同步，只写入目标 A。
- 切换到目标 B 后再次同步，只写入目标 B；目标 A 的旧外部资源不被删除。
- 两个 Obsidian 配置目录重叠时，同一文件不会被两个配置同时导入。
- 同一个 Notion page 不会被两个 Data Source 配置重复导入管理。

## 分阶段实现

### 阶段一：数据模型和 API

- 增加 `note_sync_bindings` 和 `sync_external_claims`。
- 为 PostgreSQL 和 SQLite 同时补齐 `sync_targets(type,name)`、binding、claim 约束。
- 增加 storage/repository contract。
- 增加绑定 API。
- 增加统一单篇同步 API。
- 增加 target-scoped deletion confirm/restore API。

### 阶段二：服务接入

- 改造 Obsidian/Notion 单篇同步为显式 target。
- 批量 push 只处理绑定到目标的笔记。
- 单篇 push 不再被 `required_tags` 过滤。
- pull/import 成功后自动创建绑定和 claim。
- pull/import 遇到跨 target FlowSpace ID 时返回 binding conflict。
- Obsidian/Notion 按 provider 实现 reserve claim 或可重试恢复策略。
- 外部资源冲突时跳过并返回明确结果。

### 阶段三：前端交互

- 同步设置页支持多个目标配置管理。
- 编辑器同步卡片改成目标名单选下拉。
- 同步状态只展示当前绑定目标。
- 添加更换目标确认和未绑定空状态。

### 阶段四：迁移和兼容

- 从历史 `note_sync_state` 迁移可确定的绑定。
- 多历史状态保留为候选，不自动选择。
- SQLite 到 PostgreSQL 迁移清单加入 binding 和 claim 表。
- 统一 `last_direction` 的 `delete_detected` 枚举约束。
- 保留旧接口兼容，但对不匹配的目标返回 409。

## 验收标准

- 数据库层保证一篇笔记最多一个 active 同步绑定。
- 数据库层保证一个外部资源最多一个 active claim。
- 前端编辑器只能选择一个同步目标名。
- 未绑定笔记不会被同步任务误处理。
- 单篇同步不再依赖类型默认目标，而是依赖当前笔记绑定。
- 所有新增行为都有后端和前端测试覆盖。
