# FlowSpace Notion 双向同步设计

日期：2026-06-13

## 背景

FlowSpace 已经实现 Obsidian 本地 Markdown 同步，并通过 `sync_targets` 保存同步目标，通过 `note_sync_state` 保存每篇笔记与外部系统的映射、hash、最近同步方向和错误状态。Obsidian 双向同步已经形成了可复用的基本模式：手动触发、批量处理、单项失败不阻断整批、外部删除先标记再确认。

Notion 同步不能复用 Obsidian 的文件写入逻辑，但可以复用同步目标、同步状态和同步决策框架。第一版 Notion 同步采用一个用户指定的 Notion Data Source 作为个人笔记库，每个 Notion page 对应一条 FlowSpace note。

Notion 官方 API 的几个约束会影响设计：

- Data Source 查询用于列出笔记页，需要集成被分享到对应 Notion 数据源。
- Page 的正文由 block 组成，读取正文需要递归获取 block children。
- 创建 page 后可以追加 children，更新已有 page 正文需要先清空或逐块删除后再追加。
- Page update 支持 `erase_content`，但这是破坏性操作，必须只用于 FlowSpace 明确管理且可安全覆盖的页面。
- Notion API 有请求限流，官方说明每个 connection 平均约 3 requests/second，并会通过 429 和 `Retry-After` 表达退避要求。

参考文档：

- https://developers.notion.com/reference/query-a-data-source
- https://developers.notion.com/reference/get-block-children
- https://developers.notion.com/reference/patch-block-children
- https://developers.notion.com/reference/patch-page
- https://developers.notion.com/reference/request-limits

## 目标

- 支持一个个人 Notion Data Source 与 FlowSpace notes 双向同步。
- 冲突时固定采用 Notion 优先：只要 Notion 自上次同步后发生变化，就用 Notion 覆盖 FlowSpace。
- 支持 Notion 新页面导入 FlowSpace。
- 支持 FlowSpace 新笔记创建 Notion page。
- 支持 Notion 已同步页面修改后回写 FlowSpace。
- 支持 FlowSpace 已同步笔记修改后写回 Notion，前提是 Notion 自上次同步后没有变化。
- 支持 Notion 页面归档、进回收站或不可访问后，将 FlowSpace 笔记标记为待确认删除。
- 复用现有同步状态展示和批量同步结果模式。

## 非目标

- 第一版不做 Notion workspace 全局扫描，只同步用户配置的一个专用 Data Source。
- 第一版不做 Notion webhook 实时同步。
- 第一版不支持多 Notion Data Source。
- 第一版不支持图片、附件、文件上传、嵌入、数据库嵌套、同步块和复杂布局的无损双向编辑。
- 第一版不做复杂三方合并或逐段合并。
- 第一版不自动删除 FlowSpace 笔记。Notion 删除只标记 `external_deleted`，用户确认后才删除 FlowSpace。
- 第一版不把 Notion token 明文保存到 SQLite。

## 产品规则

### 同步范围

用户需要在 Notion 中准备一个专用 Data Source，例如 `FlowSpace Notes`，并把它分享给 Notion integration。FlowSpace 只同步这个 Data Source 中的页面。

第一版默认同步该 Data Source 下的所有未归档、未进回收站页面。FlowSpace 创建的页面会写入一个 `FlowSpace ID` 属性，Notion 原生页面导入后也会补写该属性，便于后续稳定映射。

### 冲突策略

Notion 是冲突胜出方。

- 只改 Notion：拉取 Notion 到 FlowSpace。
- 只改 FlowSpace：写入 Notion。
- 两边都改：拉取 Notion 到 FlowSpace，并记录结果为 `conflict_pulled`。
- Notion 页面不存在、进回收站或归档：FlowSpace 标记为 `external_deleted`，等待用户确认。
- FlowSpace 删除笔记：依赖 `note_sync_state` 的外键级联删除本地同步状态；第一版不主动删除 Notion page。

判断是否修改：

- `content_hash` 保存上次同步时 FlowSpace 规范化 Markdown 的 hash。
- `external_hash` 保存上次同步时 Notion blocks 转换为规范化 Markdown 后的 hash。
- `external_mtime` 保存 Notion page `last_edited_time`。
- 当前 FlowSpace hash 不等于 `content_hash`，表示 FlowSpace 有变化。
- 当前 Notion hash 不等于 `external_hash`，或 Notion `last_edited_time` 晚于 `external_mtime` 且 hash 不一致，表示 Notion 有变化。

### 删除确认

Notion 删除包括三类情况：

- Notion page `in_trash = true`。
- Notion page `is_archived = true`。
- 之前已同步的 `external_id` 在 Data Source 查询和 page retrieve 中都不可访问。

这些情况不会自动删除 FlowSpace note。同步服务会把对应 `note_sync_state.status` 标记为 `external_deleted`，前端展示待确认列表。用户可执行：

- 确认删除：删除 FlowSpace note，并删除该 target 下的同步状态。
- 保留并重新导出：如果原 page 可恢复则先恢复并覆盖；如果不可访问，则创建新的 Notion page 并更新 `external_id`。

## Notion 数据映射

### 推荐 Data Source 属性

第一版要求或推荐以下属性。属性名可配置，默认值如下：

```json
{
  "title_property": "Name",
  "flowspace_id_property": "FlowSpace ID",
  "folder_property": "Folder",
  "tags_property": "Tags",
  "flowspace_updated_property": "FlowSpace Updated At"
}
```

字段含义：

- `Name`：Notion title 属性，对应 FlowSpace `notes.title`。
- `FlowSpace ID`：rich text 属性，保存 FlowSpace note id。
- `Folder`：select 或 rich text 属性，对应 FlowSpace folder 名称或 folder id。
- `Tags`：multi_select 属性，对应 FlowSpace `tags`。
- `FlowSpace Updated At`：date 属性，记录 FlowSpace 写入 Notion 时的更新时间。

第一版不自动修改 Notion Data Source schema。测试连接时检查必要属性是否存在；缺失时给出明确错误，提示用户在 Notion 中补齐。

### Sync target

现有 `sync_targets` 表保留 Obsidian 字段以兼容旧功能，新增通用配置字段：

```sql
ALTER TABLE sync_targets ADD COLUMN config_json TEXT NOT NULL DEFAULT '{}';
```

Notion target 示例：

```json
{
  "type": "notion",
  "name": "Personal Notion",
  "vault_path": "",
  "base_folder": "",
  "config_json": {
    "data_source_id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "token_env": "FLOWSPACE_NOTION_TOKEN",
    "title_property": "Name",
    "flowspace_id_property": "FlowSpace ID",
    "folder_property": "Folder",
    "tags_property": "Tags",
    "flowspace_updated_property": "FlowSpace Updated At"
  }
}
```

`token_env` 默认固定为 `FLOWSPACE_NOTION_TOKEN`。后端只从环境变量读取 token，不把 token 返回给前端，不写入日志。

### Sync state

扩展 `note_sync_state`：

```sql
ALTER TABLE note_sync_state ADD COLUMN external_id TEXT;
ALTER TABLE note_sync_state ADD COLUMN external_url TEXT;
```

字段约定：

- `external_id`：Notion page id。
- `external_url`：Notion page URL，用于前端展示和打开。
- `external_path`：为兼容现有接口保留。Notion 下可存 `external_url` 或 `notion:{page_id}`。
- `content_hash`：上次同步时 FlowSpace 规范化 Markdown hash。
- `external_hash`：上次同步时 Notion blocks 规范化 Markdown hash。
- `external_mtime`：Notion `last_edited_time` 的 Unix 秒。
- `last_direction`：`push`、`pull`、`import`、`conflict_pulled`、`delete_detected`、`restore`。
- `status`：`synced`、`failed`、`external_deleted`、`unsupported_remote`。

## 内容转换

### Notion 到 FlowSpace

读取 page properties 得到标题、folder、tags。正文通过递归读取 page block children 得到，转换为 FlowSpace Markdown。

第一版支持：

- paragraph
- heading_1、heading_2、heading_3
- bulleted_list_item
- numbered_list_item
- to_do
- quote
- code
- divider
- callout 的纯文本内容
- toggle 的标题和子内容
- rich text 中的粗体、斜体、删除线、inline code 和链接

转换规则：

- Notion title 属性作为 FlowSpace title。
- Notion blocks 转成 Markdown body，不重复写入一级标题。
- unsupported block 转为稳定占位文本，例如 `[Unsupported Notion block: table]`。
- 记录 unsupported block 类型，用于后续 push 安全判断。

### FlowSpace 到 Notion

FlowSpace body 使用 Markdown 解析为 block 列表，写入 Notion page。

第一版支持写入：

- paragraph
- heading_1、heading_2、heading_3
- bulleted_list_item
- numbered_list_item
- to_do
- quote
- code
- divider

写回安全规则：

- 仅当 Notion 自上次同步后未变化时，才允许 FlowSpace 覆盖 Notion。
- 仅当当前 Notion page 没有 unsupported block 时，才允许使用 `erase_content` 重建正文。
- 如果 Notion page 有 unsupported block，且只有 FlowSpace 有变化，则该项标记为 `unsupported_remote`，不覆盖 Notion，避免擦掉复杂内容。
- 如果 Notion page 有 unsupported block 且 Notion 也变化，则仍以 Notion 为准拉回 FlowSpace。

## 后端设计

### 新增 Notion client

新增 `backend/internal/service/notion_client.go`，封装 Notion HTTP API：

- `TestNotionTarget(target)`
- `QueryDataSource(target)`
- `RetrievePage(pageID)`
- `RetrievePageBlocks(pageID)`
- `CreatePage(target, note)`
- `UpdatePageProperties(pageID, note)`
- `ReplacePageBlocks(pageID, blocks)`
- `RestorePage(pageID)`

client 负责：

- 设置 Authorization 和 Notion-Version header。
- 处理分页。
- 处理 429 和 529，按 `Retry-After` 或指数退避重试。
- 把 401、403、404、409、429 等错误转换为可展示错误。
- 限制请求速率，默认不超过 3 requests/second。

### 新增内容转换模块

新增 `backend/internal/service/notion_blocks.go`：

- Notion block JSON 到规范化 Markdown。
- Markdown 到 Notion block JSON。
- rich text 到 Markdown inline。
- Markdown inline 到 rich text。
- unsupported block 类型收集。
- 内容 hash 计算。

### 双向同步服务

新增 `backend/internal/service/notion_bidirectional.go`。

一次同步流程：

1. 加载默认 `type = 'notion'` target。
2. 读取 `FLOWSPACE_NOTION_TOKEN`。
3. 验证 Data Source 可访问，必要属性存在。
4. 查询 Data Source 页面，过滤不可用页面，并记录 in_trash/is_archived。
5. 对每个可访问页面读取 properties 和 blocks，转换成 `notionRemoteNote`。
6. 读取 FlowSpace notes 和该 target 下的 `note_sync_state`。
7. 先处理 Notion 页面：
   - 可通过 `FlowSpace ID` 匹配本地 note：按冲突规则 pull、skip 或等待 push。
   - 可通过 `external_id` 匹配本地 note：按冲突规则 pull、skip 或等待 push。
   - 无匹配：导入为新 FlowSpace note，并写入同步状态。
8. 处理之前已同步但本轮 Notion 不存在的状态：标记 `external_deleted`。
9. 处理 FlowSpace 新增或只在 FlowSpace 修改的笔记：创建或更新 Notion page。
10. 返回批量结果摘要和明细。

结果结构：

```json
{
  "pushed": 3,
  "pulled": 5,
  "conflict_pulled": 1,
  "imported": 2,
  "external_deleted": 1,
  "unsupported": 1,
  "failed": 0,
  "items": [
    {
      "note_id": "note-id",
      "status": "pulled",
      "external_id": "notion-page-id",
      "external_url": "https://www.notion.so/..."
    }
  ]
}
```

### API

新增接口：

```http
POST /api/sync/notion/test
POST /api/sync/notion/bidirectional
POST /api/sync/notion/notes/:id
GET  /api/sync/notion/deletions
POST /api/sync/notion/deletions/:note_id/confirm
POST /api/sync/notion/deletions/:note_id/restore
```

扩展现有接口：

```http
GET /api/notes/:id/sync-state?target=obsidian|notion
GET /api/notes/:id/sync-states
```

兼容规则：

- 不带 `target` 的 `GET /api/notes/:id/sync-state` 继续返回 Obsidian 状态，避免破坏现有前端。
- 新的 Notion 卡片使用 `target=notion` 或 `sync-states`。

### 错误处理

需要明确返回的错误：

- 未配置 Notion target。
- `FLOWSPACE_NOTION_TOKEN` 为空。
- Notion token 无效。
- Data Source 未分享给 integration。
- Data Source ID 无效。
- 必要属性缺失。
- Notion API rate limited。
- Notion API service overload。
- 读取 blocks 失败。
- 内容转换失败。
- Notion page 包含 unsupported block，不能安全覆盖。
- FlowSpace note 创建或更新失败。

批量同步中单篇失败不阻断其他笔记，最终通过 `failed` 和 `items` 明细返回。

## 前端设计

### 同步设置面板

将现有 Obsidian 同步入口扩展为“同步设置”面板，内部提供两个目标：

- Obsidian
- Notion

Notion 面板字段：

- 目标名称
- Data Source ID
- Token 来源提示：`FLOWSPACE_NOTION_TOKEN`
- Title 属性名，默认 `Name`
- FlowSpace ID 属性名，默认 `FlowSpace ID`
- Folder 属性名，默认 `Folder`
- Tags 属性名，默认 `Tags`
- FlowSpace Updated At 属性名，默认 `FlowSpace Updated At`
- 自动同步开关，第一版默认关闭

操作：

- 测试连接
- 保存设置
- 双向同步
- 查看同步结果摘要
- 查看待确认删除列表

### 编辑器同步卡片

编辑器右侧同步卡片从单一 Obsidian 卡片扩展为多目标卡片，或拆成 Obsidian 与 Notion 两张卡片。

Notion 卡片展示：

- 同步状态
- Notion 页面链接
- 上次同步时间
- 错误信息
- 同步当前笔记
- 如果状态为 `external_deleted`，展示“确认删除”和“保留并重新导出”

## 安全与隐私

- Notion token 只从后端环境变量读取。
- API 响应和前端状态不返回 token。
- 后端日志不打印 Authorization header。
- Notion Data Source ID 可保存到 SQLite，因为它不是密钥。
- 所有 Notion 写入只发生在配置的 Data Source 或已映射 page 上。
- 对 Notion 复杂页面默认保守处理，避免用 FlowSpace Markdown 擦掉复杂 block。

## 测试计划

### 后端单元测试

- Notion target 配置解析。
- 缺少 token 时返回明确错误。
- Data Source 属性校验。
- Notion blocks 转 Markdown。
- Markdown 转 Notion blocks。
- unsupported block 收集。
- content hash 和 external hash 稳定。
- 只改 Notion 时 pull。
- 只改 FlowSpace 时 push。
- 两边都改时 conflict pull。
- Notion 新页面导入 FlowSpace。
- FlowSpace 新笔记创建 Notion page。
- Notion 删除后标记 `external_deleted`。
- unsupported remote page 阻止 FlowSpace 覆盖。
- rate limit retry。

### 后端集成测试

使用 fake Notion HTTP server 覆盖：

- query data source 分页。
- retrieve block children 分页和递归。
- create page。
- update page properties。
- erase content + append blocks。
- 401、403、404、409、429、529 错误。

### 前端验证

- Notion 设置面板可保存配置。
- 连接测试失败时错误可见。
- 双向同步后摘要数量正确。
- 待确认删除列表可操作。
- 编辑器 Notion 卡片显示页面链接和状态。
- Notion 同步失败不会破坏 Obsidian 同步入口。

### 手动验证

- 在 Notion Data Source 新建页面，同步后 FlowSpace 出现新笔记。
- 在 Notion 修改已同步页面，同步后 FlowSpace 更新。
- 在 FlowSpace 修改已同步笔记，Notion 未改时同步后 Notion 更新。
- 两边同时修改，同步后 FlowSpace 使用 Notion 内容。
- Notion 页面进回收站，同步后 FlowSpace 显示待确认删除。
- Notion 页面包含复杂 block 时，FlowSpace 修改不会擦掉 Notion 复杂内容。

## 分阶段实现

### 第一阶段：同步模型泛化

- 为 `sync_targets` 增加 `config_json`。
- 为 `note_sync_state` 增加 `external_id` 和 `external_url`。
- 扩展 model、repository 和 API 类型，保持 Obsidian 兼容。
- 增加按 target 查询 note sync state 的能力。

### 第二阶段：Notion client 与转换器

- 实现 Notion HTTP client。
- 实现 Data Source 校验。
- 实现 Notion blocks 到 Markdown。
- 实现 Markdown 到 Notion blocks。
- 覆盖转换和错误处理测试。

### 第三阶段：Notion 双向同步

- 实现 Notion 优先的同步决策。
- 实现 import、pull、conflict pull、push、delete detect。
- 实现 Notion 删除确认和重新导出。
- 覆盖 fake Notion server 集成测试。

### 第四阶段：前端入口

- 扩展同步设置面板。
- 增加 Notion API hooks。
- 扩展编辑器同步卡片。
- 展示同步摘要、错误和待确认删除。

### 第五阶段：文档与人工验证

- 更新 README 的 Notion 同步说明。
- 补充 Notion integration 创建和 Data Source 分享步骤。
- 使用真实 Notion Data Source 做端到端人工验证。

## 后续扩展

- 支持多个 Notion Data Source。
- 支持 webhook 增量同步。
- 支持 OAuth 或本地加密保存 token。
- 支持更多 Notion block 类型。
- 支持附件和图片上传。
- 支持更细粒度的冲突预览。
- 支持后台定时同步。
