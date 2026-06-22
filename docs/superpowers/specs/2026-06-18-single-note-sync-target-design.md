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
4. 新增 `note_sync_suppressions`，记录用户显式解除过的 note + target，防止旧外部文件或 Notion page 在下一次 pull/import 时自动把笔记绑回去。
5. 新增 `sync_import_tombstones`，记录已经被用户解绑、切换或删除的外部资源，防止 FlowSpace note 删除后旧外部资源又被自动导入。
6. `note_sync_state` 继续保存每个 `(note_id, target_id)` 的同步状态、hash、外部链接和错误信息，不承担“当前归属”的唯一性判断。

这个方案比直接在 `note_sync_state` 上加唯一约束更稳：`note_sync_state` 可以保留历史状态，绑定和外部资源占用则表达当前事实。

## 数据模型

### sync_targets

`sync_targets` 保持现有主体结构。PostgreSQL 和 SQLite provider 都必须提供同等约束：`type` 只允许 `obsidian`、`notion`，并且同一类型下 `name` 唯一。当前 PostgreSQL migration 已经有 `UNIQUE(type, name)`，SQLite schema 需要补同等 unique index，避免可插拔 storage provider 的行为分叉。

第一版必须增加默认目标字段：

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

规则：

- 旧的 `GetDefaultTarget(type)` 和类型兼容接口只能使用 `is_default=true AND enabled=true` 的 target。
- 不允许用 `updated_at DESC` 作为默认目标 fallback，避免编辑名称、启停、测试保存后默认目标漂移。
- 创建某个类型的第一个 enabled target 时，如果该类型还没有默认目标，可以自动设为 `is_default=true`。
- 把某个 target 设置为默认目标时，必须在同一事务里取消同类型其他 target 的默认标记。
- 默认目标必须是 enabled target；停用默认目标时，需要同时清空默认标记，旧兼容执行接口在无默认目标时返回明确配置错误。
- `is_default` 属于兼容元数据，不是外部身份字段；已有 binding/claim/history 的 target 仍允许修改该字段。

目标的外部身份字段必须受保护：

- Obsidian target 的外部身份是 `type + canonical(vault_path/base_folder)`。
- Notion target 的外部身份是 `type + normalized(config.data_source_id)`。
- 如果 target 已存在 binding、claim 或历史 state，`PATCH /api/sync/targets/:id` 只允许修改 `name`、`enabled`、`auto_sync`，以及不改变外部空间身份的配置项，例如 Notion `token_env` 和属性名映射。
- 如果请求修改 Obsidian `vault_path/base_folder` 或 Notion `data_source_id`，且 target 已被使用，返回 `409 Conflict`，错误码建议为 `target_identity_locked`。
- 修改外部身份必须通过新建 target，或后续单独设计“迁移 target”流程；不能复用原 `target_id` 指向另一个外部空间。

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

### note_sync_suppressions

```sql
CREATE TABLE note_sync_suppressions (
  note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
  reason TEXT NOT NULL DEFAULT 'user_unbound'
    CHECK (reason IN ('user_unbound', 'target_changed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, target_id)
);

CREATE INDEX note_sync_suppressions_target_idx
  ON note_sync_suppressions (target_id, updated_at DESC);
```

规则：

- 用户选择“不同步”时，删除 active binding，并为原 `(note_id, target_id)` 写入 suppression。
- 用户把笔记从目标 A 切换到目标 B 时，为目标 A 写入 suppression，然后创建或更新目标 B 的 active binding。
- 用户显式重新选择某个被 suppression 的 target 时，先删除该 suppression，再创建 active binding。
- pull/import 遇到外部资源带 FlowSpace ID，且本地 note 未绑定但存在 `(note_id, current_target_id)` suppression 时，返回 `binding_required`，不得自动重新绑定。
- suppression 不影响用户手动重新绑定，只阻止外部扫描自动绑回。

### sync_import_tombstones

```sql
CREATE TABLE sync_import_tombstones (
  external_key TEXT PRIMARY KEY,
  target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
  former_note_id TEXT NOT NULL,
  external_type TEXT NOT NULL CHECK (external_type IN ('obsidian_file', 'notion_page')),
  external_id TEXT NOT NULL DEFAULT '',
  external_path TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL CHECK (reason IN ('user_unbound', 'target_changed', 'note_deleted')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (target_id, former_note_id, external_type)
);

CREATE INDEX sync_import_tombstones_target_idx
  ON sync_import_tombstones (target_id, updated_at DESC);

CREATE INDEX sync_import_tombstones_former_note_idx
  ON sync_import_tombstones (former_note_id, external_type, updated_at DESC);
```

规则：

- tombstone 不引用 `notes(id)`，因为它必须在 FlowSpace note 被删除后继续存在。
- 用户解绑或切换 target 时，如果存在 active claim，释放 claim 前必须把该 claim 的 `external_key` 写入 tombstone。
- 用户删除 FlowSpace note 时，如果存在 active claim，删除 note 前必须在同一事务里写入 tombstone，再删除 note。删除 note 的 handler 不能直接调用底层 note delete 绕过该流程。
- tombstone 查询不能只按当前 `external_key`。pull/import 扫描外部资源时，先查精确 `external_key`；如果资源带 FlowSpace ID，再查 `(target_id, former_note_id, external_type)`；如果仍未命中，再查 `(former_note_id, external_type)` 的跨 target tombstone 并返回需要用户确认的结果。
- pull/import 扫描到 tombstone 命中的外部资源时，返回 `import_tombstoned` 或 `binding_required`，不得自动创建 note 或恢复 binding。这样 Obsidian 文件改名、移动或复制后，只要 frontmatter 里的 FlowSpace ID 仍在，也不会绕过删除记录。
- 用户在 UI 中显式选择“重新导入/重新绑定该外部资源”时，才允许删除 tombstone 并继续导入或绑定。

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

编辑规则：

- `PATCH /api/sync/targets/:id` 必须先读取旧 target，并计算旧/新外部身份是否变化。
- 如果外部身份变化，且 `CountBindingsByTarget`、`CountClaimsByTarget` 或 `CountStatesByTarget` 任一大于 0，返回 `409 Conflict target_identity_locked`。
- 有使用痕迹的 target 只允许更新 `name`、`enabled`、`auto_sync` 和不改变外部空间身份的配置项。

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
- 如果原绑定存在 active claim，更换目标必须在同一事务里先写入 `sync_import_tombstones(reason='target_changed')`，再释放旧 claim，最后更新 binding。
- 如果更换目标，必须为旧 `(note_id, target_id)` 写入 `note_sync_suppressions(reason='target_changed')`。
- 如果新 target 对当前 note 存在 suppression，说明用户正在显式重新启用该 target，保存 binding 时必须删除这条 suppression。
- 如果新 target 对当前 note 存在 `sync_import_tombstones`，保存 binding 时必须删除该 `(target_id, former_note_id, external_type)` 相关 tombstone。显式重新绑定是唯一可以自动清理该 tombstone 的路径；pull/import 不能自行清理。
- 如果用户选择的是某个具体外部资源候选，服务还必须删除该资源 `external_key` 对应的 tombstone，避免旧 tombstone 和新 claim 语义冲突。
- API 可以返回 `changed_target=true` 供前端更新提示文案，但提示不能只发生在 PUT 成功之后。

`DELETE` 行为：

- 请求必须带 `expected_target_id` 和 `expected_updated_at`，可通过 query string 或 JSON body 表达；如果当前 binding 不匹配，返回 `409 Conflict`，前端重新加载。
- 删除当前 active binding。
- 如果存在 active claim，释放 claim 前先写入 `sync_import_tombstones(reason='user_unbound')`。
- 通过数据库级联释放 active claim。
- 为原 `(note_id, target_id)` 写入 `note_sync_suppressions(reason='user_unbound')`，表示用户显式选择不再让这个 target 管理该笔记。
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
- pull/import 必须先检查 `sync_import_tombstones`：先用外部资源当前 `external_key` 精确匹配；如果资源带 FlowSpace ID，再用 `(target_id, former_note_id, external_type)` 匹配；如果仍未命中，再用 `(former_note_id, external_type)` 查跨 target tombstone。任一路径命中时，跳过自动导入/绑定并返回 `import_tombstoned` 或 `binding_required`。
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
- 如果笔记已经绑定到某个 target，但请求传入的 `target=notion|obsidian` 与当前绑定 target 类型不一致，返回 `200 OK` 和 `{ "state": null, "binding_mismatch": true, "binding_target_id": "...", "binding_target_type": "notion" }`。这样旧前端的双卡片查询不会变成请求失败，同时不会误读另一个默认目标状态。
- 旧默认目标逻辑只读取 `is_default=true AND enabled=true` 的 target；不得 fallback 到 `updated_at DESC`。如果没有默认 target，查询类接口返回 `{ "state": null, "default_target_missing": true }`，执行类接口返回 `409 Conflict default_target_missing`。
- 旧的类型单篇同步接口如果没有 `target_id`，先检查笔记当前绑定是否为对应类型；不匹配则返回 `409 Conflict`。
- 如果传入 `target_id`，必须和当前绑定一致，否则返回 `409 Conflict`。

## 后端服务设计

新增同步绑定 repository 能力：

- `GetSyncTarget(targetID)`
- `DeleteSyncTarget(targetID)`
- `CountBindingsByTarget(targetID)`
- `CountClaimsByTarget(targetID)`
- `CountStatesByTarget(targetID)`
- `GetNoteSyncBinding(noteID)`
- `SaveNoteSyncBinding(noteID, targetID)`
- `DeleteNoteSyncBinding(noteID, expectedTargetID, expectedUpdatedAt)`
- `ListBoundNotes(targetID)`
- `ClaimExternalResource(noteID, targetID, externalType, externalID, externalPath)`
- `ReleaseExternalClaim(noteID)`
- `GetExternalClaim(externalKey)`
- `GetExternalClaimByNote(noteID)`
- `GetExternalClaimForBinding(noteID, targetID)`
- `SaveSyncSuppression(noteID, targetID, reason)`
- `DeleteSyncSuppression(noteID, targetID)`
- `HasSyncSuppression(noteID, targetID)`
- `SaveImportTombstone(externalKey, targetID, formerNoteID, externalType, externalID, externalPath, reason)`
- `DeleteImportTombstone(externalKey)`
- `GetImportTombstone(externalKey)`
- `GetImportTombstoneByFormerNote(targetID, formerNoteID, externalType)`
- `FindImportTombstonesByFormerNote(formerNoteID, externalType)`
- `DeleteImportTombstonesByFormerNote(targetID, formerNoteID, externalType)`

服务层分工：

- `sync_binding_service`：处理绑定、解绑、更换目标、释放 claim。
- `sync_dispatch_service`：根据绑定目标类型分发单篇同步。
- Obsidian/Notion 现有服务只负责对应 provider 的读写和冲突规则，不再自己选择默认目标。

事务要求：

- 更换绑定和释放旧 claim 必须同事务。
- 单篇同步的 `note_sync_state` 写入和 `sync_external_claims` 更新必须同事务，避免状态成功但资源声明缺失。
- 导入外部笔记时，创建 note、创建 binding、写 sync state、写 claim 必须同事务。
- 删除 FlowSpace note 时，如果该 note 有 active claim，必须在同一事务内先写入 `sync_import_tombstones(reason='note_deleted')`，再删除 note。用户删除入口必须走 service 层，不能直接调用底层 `Notes().Delete` 绕过 tombstone。
- 所有跨 note/sync 的原子流程必须通过 `storage.Store.Transact(ctx, func(txStore storage.Store) error { ... })` 执行，并在事务回调内使用 `txStore.Notes()` 和 `txStore.Sync()`。不得在这些流程里混用 package-level `repository.*` facade 或全局 store，否则 note 创建成功但 binding/state/claim 失败时无法回滚。
- handler/service 新代码必须传入 `context.Context`，事务内 repository 方法都使用同一个 ctx。

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

- note 未绑定且没有当前 target 的 suppression：可以把该 note 绑定到当前 target，并继续 pull/import。
- note 未绑定但存在当前 target 的 suppression：必须返回 `binding_required`，不得自动绑定。前端或结果明细提示用户需要手动重新选择该同步目标。
- note 已绑定到当前 target：可以按既有冲突规则更新。
- note 已绑定到其他 target：必须返回 `binding_conflict` 并跳过，不得用目标 B 的外部内容更新目标 A 管理的笔记。
- note 不存在且外部资源没有 tombstone：按 provider 原有导入规则创建新 note，并绑定当前 target。
- note 不存在但外部资源命中 tombstone：返回 `import_tombstoned`，不得自动重新创建 note。

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
- `sync_targets(type) WHERE is_default=true` 最多一条，旧默认目标查询只允许使用该字段。
- `note_sync_bindings(note_id)` 主键唯一。
- `note_sync_bindings(note_id, target_id)` 唯一，用于 claim 组合外键。
- `sync_external_claims.external_key` 主键唯一。
- `sync_external_claims(note_id)` 唯一。
- `sync_external_claims(note_id, target_id)` 外键引用 `note_sync_bindings(note_id, target_id)`，并 `ON DELETE CASCADE`。
- `note_sync_suppressions(note_id, target_id)` 主键唯一。
- `sync_import_tombstones.external_key` 主键唯一，且不引用 `notes(id)`。
- `sync_import_tombstones(target_id, former_note_id, external_type)` 唯一，用于 Obsidian 改名/移动后的 fallback 命中。

SQLite 的 `PRAGMA foreign_keys` 必须在测试和运行时开启。storage contract tests 要同时跑 PostgreSQL 和 SQLite provider，不能只依赖 PostgreSQL 约束。

### SQLite 到 PostgreSQL 迁移

`sqlite_to_pg` 迁移清单必须加入：

- `note_sync_bindings`
- `sync_external_claims`
- `note_sync_suppressions`
- `sync_import_tombstones`

迁移前预检需要新增：

- SQLite 中 `sync_targets(type,name)` 是否重复。
- 历史 `note_sync_state.last_direction` 是否包含 PostgreSQL CHECK 不允许的值。
- 当前代码使用 `delete_detected`，PostgreSQL CHECK 和迁移预检必须允许 `delete_detected`，或者在迁移时统一清洗成同一个枚举值。第一版建议统一允许并保留 `delete_detected`，避免历史状态丢语义。
- 自动生成 binding 时，如果同一 note 有多个历史 target，不创建 binding，也不创建 claim。
- 自动生成 claim 时，必须先成功创建对应 binding，依赖组合外键兜底。
- 迁移不会自动生成 suppression；suppression 只来自迁移后用户显式解绑或切换目标。

历史 claim 生成规则必须保守：

- 只有 `note_sync_state.status = 'synced'` 且 `last_direction` 不是 `delete_detected` 的历史状态，才允许生成 active claim。
- `failed`、`pending`、`external_deleted` 或 `last_direction='delete_detected'` 的状态只保留为历史 state/candidate，不生成 active claim。
- Obsidian 历史 `external_path` 必须能解析为存在的真实路径，并且真实路径仍在该 target 的 base folder 内；否则跳过 active claim。符号链接解析失败、路径不存在、路径逃逸都不能词法生成 claim。
- Notion 历史 `external_id` 必须能规范化为 page id。迁移阶段不调用远端 Notion API 校验 page 是否存在；首次同步时再验证远端状态。如果状态是 `external_deleted`，即使有 `external_id` 也不生成 active claim。
- 无法生成 active claim 不代表迁移失败；它只表示该笔记需要用户在编辑器中重新选择同步目标或通过 pull/import 重新确认。

## 错误处理

- `400 Bad Request`：笔记未绑定同步目标、目标类型不支持、目标已停用。
- `404 Not Found`：笔记或同步目标不存在。
- `409 Conflict`：传入目标和当前绑定不一致、删除仍有绑定的目标、外部资源已被其他配置 claim、外部资源因用户显式解绑/删除而需要重新确认绑定、已有使用痕迹的 target 外部身份被修改。
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
- repository/storage contract：target 按 ID 查询、删除和绑定计数分别返回正确的 404/409 前置条件。
- repository/storage contract：`note_sync_suppressions(note_id,target_id)` 可创建、查询、删除，并阻止自动绑定路径。
- repository/storage contract：`sync_import_tombstones.external_key` 可创建、查询、删除，且删除 note 后仍保留。
- repository/storage contract：`sync_import_tombstones(target_id,former_note_id,external_type)` fallback 查询能命中已改名/移动的 Obsidian 资源。
- repository/storage contract：可通过 noteID 或 noteID+targetID 读取 active claim，用于解绑、切换 target 和删除 note 前写 tombstone。
- repository/storage contract：每种 type 只能有一个 `is_default=true` target，默认目标查询不会受 `updated_at` 变化影响。
- handler：`GET/PUT/DELETE /api/notes/:id/sync-binding` 的成功、未找到、目标停用和更换目标场景。
- handler：target 已有 binding/claim/history state 时，修改 Obsidian `vault_path/base_folder` 或 Notion `data_source_id` 返回 `409 target_identity_locked`。
- handler：更换目标时缺少 `confirm_changed_target=true` 返回 409。
- handler：`expected_target_id` 和当前绑定不一致时返回 409。
- handler：删除 binding 时缺少或不匹配 `expected_target_id/expected_updated_at` 返回 409。
- handler：`POST /api/sync/notes/:id` 在未绑定时返回 400。
- handler：target-scoped deletion confirm/restore 使用指定 `target_id`，不走默认 target。
- handler：已有绑定但 `GET /api/notes/:id/sync-state?target=其他类型` 返回 `binding_mismatch=true`，不读取默认 target。
- service：单篇同步按当前绑定目标分发到 Obsidian 或 Notion。
- service：导入、绑定切换、state/claim 写入使用 `Store.Transact` 内的 `txStore.Notes()` 和 `txStore.Sync()`，测试用失败注入证明 note 成功但 binding/state/claim 失败时会回滚。
- service：已绑定笔记执行 push 时不被 `required_tags` 过滤跳过。
- service：传入不匹配的 `target_id` 返回 409。
- service：pull/import 遇到已绑定到其他 target 的 FlowSpace ID 时返回 `binding_conflict`，不得更新该 note。
- service：pull/import 遇到未绑定但已 suppression 的 FlowSpace ID 时返回 `binding_required`，不得自动重建 binding。
- service：pull/import 遇到 external_key 或 former note fallback 命中 tombstone 时返回 `import_tombstoned` 或 `binding_required`，不得创建 note。
- service：用户显式重新绑定同一 target 时，删除该 note/target 相关 suppression 和 tombstone。
- service：删除有 active claim 的 note 时先写 tombstone，再删除 note；失败注入证明任一步失败都会回滚。
- service：Obsidian 写文件前必须先 reserve claim；reserve 失败时不写文件。
- service：Notion 新建 page 后本地落库失败时，下一次同步能通过 FlowSpace ID 收养已创建 page，不重复创建。
- Obsidian：重叠目录扫描时，对已被其他 target claim 的文件返回冲突并跳过。
- Notion：导入 page 时，对已被其他 target claim 的 page 返回冲突并跳过。
- migration：单历史状态自动生成绑定，多历史状态不自动绑定并保留 candidates。
- migration：`note_sync_bindings` 和 `sync_external_claims` 进入 SQLite 到 PostgreSQL 迁移清单。
- migration：`note_sync_suppressions` 进入 SQLite 到 PostgreSQL 迁移清单。
- migration：`sync_import_tombstones` 进入 SQLite 到 PostgreSQL 迁移清单。
- migration：`is_default` 进入 SQLite 和 PostgreSQL schema，旧默认目标迁移不使用 `updated_at DESC` 作为运行时 fallback。
- migration：只有 `status='synced'` 且外部资源可验证/可规范化的历史状态生成 active claim；`external_deleted/failed/pending/delete_detected` 不生成 claim。
- migration：初始 binding/claim 迁移在服务切换到 binding-only 批量 push 前完成。
- migration：`delete_detected` 在 PostgreSQL CHECK 和预检中保持一致。

### 前端测试

- 同步卡片展示目标名下拉框，而不是多个类型卡片。
- 下拉框只允许选择一个同步目标。
- 选择目标后调用 `PUT /api/notes/:id/sync-binding`。
- 选择“不 同步”后调用 `DELETE /api/notes/:id/sync-binding`。
- 删除绑定请求携带当前 `expected_target_id/expected_updated_at`。
- 更换已绑定目标时显示确认提示。
- 取消更换确认时不调用 `PUT /api/notes/:id/sync-binding`。
- 未绑定时同步按钮提示先选择同步目标。
- 绑定目标后，同步按钮调用 `POST /api/sync/notes/:id`。
- 同步设置页可以显示多个 Notion 和多个 Obsidian 配置。
- 删除有绑定笔记的目标时显示冲突提示。
- 编辑已有绑定/claim 的 target 外部身份字段时，前端展示 `target_identity_locked` 冲突提示，引导用户新建 target 或走迁移流程。
- 设置默认 target 后，编辑其他 target 的名称或开关不会改变旧兼容接口使用的默认目标。
- 显式重新绑定被解绑过的 target 后，前端能再次触发同步，后端不再被旧 tombstone 拦截。

### 手动验收

- 创建两个 Notion 同步目标，编辑同一篇笔记时只能选择其中一个。
- 创建两个 Obsidian 同步目标，编辑同一篇笔记时只能选择其中一个。
- 未绑定笔记不会被目标批量 push。
- 绑定到目标 A 后执行单篇同步，只写入目标 A。
- 切换到目标 B 后再次同步，只写入目标 B；目标 A 的旧外部资源不被删除。
- 解除绑定后保留旧 Obsidian 文件或 Notion page，再执行 pull/import，系统提示需要用户重新选择同步目标，不会自动恢复旧绑定。
- 删除已同步的 FlowSpace 笔记后保留旧 Obsidian 文件或 Notion page，再执行 pull/import，系统不会自动重新创建该笔记。
- 删除已同步笔记后，把旧 Obsidian 文件改名或移动，再执行 pull/import，系统仍通过 FlowSpace ID tombstone fallback 阻止自动重新导入。
- 用户显式重新选择同一个同步目标后，旧 suppression/tombstone 被清理，后续同步按新绑定继续。
- 已有绑定的 target 不能把 Obsidian 目录或 Notion Data Source 改成另一个外部空间。
- 两个 Obsidian 配置目录重叠时，同一文件不会被两个配置同时导入。
- 同一个 Notion page 不会被两个 Data Source 配置重复导入管理。

## 分阶段实现

### 阶段一：数据模型和 API

- 增加 `note_sync_bindings`、`sync_external_claims`、`note_sync_suppressions` 和 `sync_import_tombstones`。
- 为 PostgreSQL 和 SQLite 同时补齐 `sync_targets(type,name)`、binding、claim、suppression、tombstone 约束。
- 增加 `sync_targets.is_default` 和每类型唯一默认目标约束。
- 与 schema 一起执行历史迁移：从历史 `note_sync_state` 生成可确定的初始 binding/claim，多历史状态保留为候选。
- 服务切换到“批量 push 只处理绑定笔记”之前，必须保证初始 binding/claim 迁移已经完成。
- 增加 storage/repository contract。
- 增加 target 按 ID 查询、删除和绑定计数能力。
- 增加 target 外部身份锁校验。
- 增加绑定 API。
- 增加统一单篇同步 API。
- 增加 target-scoped deletion confirm/restore API。

### 阶段二：服务接入

- 改造 Obsidian/Notion 单篇同步为显式 target。
- 批量 push 只处理绑定到目标的笔记。
- 单篇 push 不再被 `required_tags` 过滤。
- pull/import 成功后自动创建绑定和 claim。
- pull/import 遇到跨 target FlowSpace ID 时返回 binding conflict。
- pull/import 遇到用户显式解绑 suppression 时返回 binding required。
- pull/import 遇到 tombstone 时跳过自动导入。
- pull/import tombstone 查询支持 `external_key` 和 former note fallback。
- 删除 note 前为 active claim 写入 tombstone。
- 跨 note/sync 原子流程全部改为 `Store.Transact` 内 txStore repository。
- Obsidian/Notion 按 provider 实现 reserve claim 或可重试恢复策略。
- 外部资源冲突时跳过并返回明确结果。

### 阶段三：前端交互

- 同步设置页支持多个目标配置管理。
- 编辑器同步卡片改成目标名单选下拉。
- 同步状态只展示当前绑定目标。
- 添加更换目标确认和未绑定空状态。

### 阶段四：兼容和收敛

- SQLite 到 PostgreSQL 迁移清单加入 binding 和 claim 表。
- SQLite 到 PostgreSQL 迁移清单加入 suppression 表。
- SQLite 到 PostgreSQL 迁移清单加入 import tombstone 表。
- SQLite 到 PostgreSQL 迁移包含 `is_default` 字段和默认目标唯一约束。
- 历史 claim 只从已同步且外部资源可验证/可规范化的状态生成。
- 统一 `last_direction` 的 `delete_detected` 枚举约束。
- 保留旧接口兼容：查询类 mismatch 返回 `200 binding_mismatch=true`，执行类 mismatch 返回 `409 Conflict`。

## 验收标准

- 数据库层保证一篇笔记最多一个 active 同步绑定。
- 数据库层保证一个外部资源最多一个 active claim。
- 用户显式解绑后，旧外部资源带 FlowSpace ID 再被 pull/import 时不会自动重建绑定。
- 用户删除已同步笔记后，旧外部资源不会自动重新导入成新笔记。
- Obsidian 外部文件改名或移动后，只要 FlowSpace ID 仍在，tombstone fallback 仍能阻止自动重新导入。
- 旧兼容接口的默认目标由 `is_default` 决定，不会因编辑 target 导致默认目标漂移。
- 已有 binding/claim/history 的 target 不能被修改为另一个 Obsidian 目录或 Notion Data Source。
- 前端编辑器只能选择一个同步目标名。
- 未绑定笔记不会被同步任务误处理。
- 单篇同步不再依赖类型默认目标，而是依赖当前笔记绑定。
- 所有新增行为都有后端和前端测试覆盖。
