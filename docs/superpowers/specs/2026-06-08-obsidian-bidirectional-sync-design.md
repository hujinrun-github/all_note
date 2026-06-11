# FlowSpace Obsidian 双向同步设计

日期：2026-06-08

## 背景

当前 FlowSpace 已支持把笔记导出到 Obsidian Vault：用户配置 `vault_path` 和 `base_folder` 后，可以同步单篇、文件夹或全部 FlowSpace 笔记到 Obsidian。现有同步是单向的，FlowSpace 作为主数据源，Obsidian 文件会被覆盖。

本设计在现有同步目标、Markdown 渲染、路径校验和 `note_sync_state` 映射基础上扩展双向同步。第一版目标是可控、安全地支持 Obsidian 笔记导入、Obsidian 修改回写 FlowSpace，以及 Obsidian 删除后的手动确认删除。

## 目标

- 支持从 Obsidian 同步目录导入新增 Markdown 笔记到 FlowSpace。
- 支持 Obsidian 中修改过的已同步笔记覆盖 FlowSpace。
- 支持 FlowSpace 中新增或修改过的笔记写入 Obsidian。
- 当两边都修改过同一篇笔记时，采用 Obsidian 优先策略。
- 当 Obsidian 中删除已同步文件时，FlowSpace 标记为“Obsidian 已删除”，用户确认后再删除 FlowSpace 笔记。
- 同步范围只限配置的 `vault_path/base_folder`。
- 保留现有单向同步入口，新增一个明确的双向同步入口。

## 非目标

- 不同步整个 Obsidian Vault。
- 不支持多个 Obsidian 文件夹配置。
- 不做后台常驻文件监听。
- 不做定时自动扫描。
- 不实现复杂三方合并或逐段合并。
- 不同步 Obsidian 附件、图片、Canvas、插件数据或非 Markdown 文件。
- 不自动删除 FlowSpace 笔记。
- 不把 FlowSpace 删除反向删除 Obsidian 文件，第一版只处理 Obsidian 删除后 FlowSpace 的确认删除。

## 产品规则

### 同步范围

双向同步只扫描配置的 `vault_path/base_folder`。扫描会递归读取 `.md` 文件，并跳过隐藏目录、`.obsidian` 目录和非 Markdown 文件。

后端必须继续使用绝对路径和真实路径校验，确保读取和写入都不会逃出 base folder。前端不允许传入任意文件路径作为同步目标。

### 笔记识别

FlowSpace 导出的 Markdown 继续使用 YAML frontmatter：

```md
---
id: note-id
source: flowspace
folder: "__work"
created: 2026-06-01T10:20:00Z
updated: 2026-06-08T11:30:00Z
tags:
  - product
---

# 标题

正文
```

导入规则：

- 如果 Markdown frontmatter 有 `id`，且 FlowSpace 存在该笔记，则更新这篇笔记。
- 如果 Markdown frontmatter 有 `id`，但 FlowSpace 不存在该笔记，则创建新笔记并尽量保留该外部文件映射；笔记 ID 可以复用 frontmatter 中的 ID，前提是不冲突且格式合法。
- 如果 Markdown 没有 `id`，则作为 Obsidian 原生笔记导入 FlowSpace，并建立 `note_sync_state` 映射。
- 导入时标题优先取 Markdown 第一个一级标题；如果没有一级标题，则取文件名。
- 导入时正文去掉 YAML frontmatter；如果第一个一级标题和标题一致，可以从正文中去掉该标题，避免编辑器里重复显示标题。
- Obsidian 没有 FlowSpace 文件夹信息时，默认导入到 `__uncategorized`。

### 冲突策略

第一版采用 Obsidian 优先。

- 只改 FlowSpace：写入 Obsidian。
- 只改 Obsidian：更新 FlowSpace。
- 两边都改：用 Obsidian 文件覆盖 FlowSpace，并更新同步状态。
- Obsidian 新增文件：创建 FlowSpace 笔记。
- FlowSpace 新增笔记：创建 Obsidian 文件。
- 同步失败时不阻塞其他笔记，最终返回批量摘要。

判断“是否修改”的基础：

- `note_sync_state.content_hash` 保存上次同步时 FlowSpace 渲染出的 Markdown hash。
- 新增 `external_hash` 保存上次同步时 Obsidian 文件内容 hash。
- 新增 `external_mtime` 保存上次同步时 Obsidian 文件修改时间。
- 当前 FlowSpace 渲染 hash 不等于 `content_hash`，表示 FlowSpace 有变化。
- 当前 Obsidian 文件 hash 不等于 `external_hash`，表示 Obsidian 有变化。

当 `content_hash` 或 `external_hash` 缺失时，系统按保守规则处理：如果外部文件存在，优先拉取 Obsidian 内容；如果外部文件不存在，只标记状态，不自动删除。

### 删除规则

删除同步只处理已经建立过映射的笔记。

如果 `note_sync_state.external_path` 指向的文件在 Obsidian 中不存在，且对应 FlowSpace 笔记仍存在：

- 不直接删除 FlowSpace 笔记。
- 将状态标记为 `external_deleted`。
- 在同步结果和同步面板中展示待确认删除列表。

用户可以对每条待确认删除执行：

- 确认删除：删除 FlowSpace 笔记，并删除对应同步状态。
- 保留并重新导出：把 FlowSpace 笔记重新写回 Obsidian，并把状态恢复为 `synced`。

如果 base folder 不存在、Vault 不可访问或路径校验失败，不能把任何笔记标记为删除，避免路径异常导致误判。

## 数据模型

扩展 `note_sync_state`：

```sql
ALTER TABLE note_sync_state ADD COLUMN external_hash TEXT;
ALTER TABLE note_sync_state ADD COLUMN external_mtime INTEGER;
ALTER TABLE note_sync_state ADD COLUMN last_direction TEXT;
```

字段含义：

- `content_hash`：上次同步时 FlowSpace 渲染出的完整 Markdown hash。
- `external_hash`：上次同步时 Obsidian 文件的完整 Markdown hash。
- `external_mtime`：上次同步时 Obsidian 文件的修改时间 Unix 秒。
- `last_direction`：最近一次成功动作，取值为 `push`、`pull`、`import`、`delete_detected`、`restore`。
- `status`：扩展为 `synced`、`pending`、`failed`、`external_deleted`。

由于当前 schema 通过 `CREATE TABLE IF NOT EXISTS` 初始化数据库，需要新增一个轻量迁移流程，确保老数据库启动时可以补齐新增字段。

## 后端设计

### 新增模型

新增批量双向同步返回结构：

```json
{
  "pushed": 3,
  "pulled": 5,
  "imported": 2,
  "external_deleted": 1,
  "failed": 0,
  "items": [
    {
      "note_id": "n01",
      "status": "pulled",
      "external_path": "D:\\Vault\\FlowSpace Notes\\Demo.md"
    }
  ]
}
```

新增待确认删除结构：

```json
{
  "items": [
    {
      "note_id": "n01",
      "title": "Demo",
      "external_path": "D:\\Vault\\FlowSpace Notes\\Demo.md",
      "last_synced_at": 1780890000
    }
  ]
}
```

### 新增接口

```http
POST /api/sync/obsidian/bidirectional
GET  /api/sync/obsidian/deletions
POST /api/sync/obsidian/deletions/:note_id/confirm
POST /api/sync/obsidian/deletions/:note_id/restore
```

接口行为：

- `POST /api/sync/obsidian/bidirectional`：执行一次手动双向扫描和同步。
- `GET /api/sync/obsidian/deletions`：返回 `external_deleted` 状态的笔记。
- `POST /api/sync/obsidian/deletions/:note_id/confirm`：确认删除 FlowSpace 笔记。
- `POST /api/sync/obsidian/deletions/:note_id/restore`：保留 FlowSpace 笔记并重新写回 Obsidian。

### 服务拆分

现有 `obsidian_sync.go` 已经承担路径校验、Markdown 渲染、文件写入和状态记录。双向同步会让它变大，因此建议拆出以下内部函数或文件：

- `obsidian_markdown.go`：Markdown frontmatter 解析、标题提取、正文规范化、Markdown 渲染。
- `obsidian_paths.go`：base folder 解析、递归扫描、路径安全校验、文件名清洗。
- `obsidian_bidirectional.go`：双向同步决策、pull/import/delete-detect/restore 流程。

这些文件仍放在 `backend/internal/service`，保持对 handler 和 repository 的公开入口简单。

### 同步流程

一次双向同步分为五步：

1. 加载默认 Obsidian target，并执行路径测试。
2. 扫描 base folder 内所有 `.md` 文件，计算外部 hash 和 mtime，解析 frontmatter。
3. 读取 FlowSpace 笔记和现有 `note_sync_state` 映射。
4. 对每个外部文件执行导入、拉取或覆盖判断。
5. 对每个已映射但外部文件不存在的 FlowSpace 笔记标记 `external_deleted`。
6. 对 FlowSpace 新增或只在 FlowSpace 修改的笔记执行 push。

处理顺序优先保证 Obsidian：

1. import/pull Obsidian 文件。
2. detect Obsidian deletions。
3. push FlowSpace-only changes。

这样如果同一篇笔记两边都变了，Obsidian 内容会先落到 FlowSpace，再记录为最新同步状态。

## 前端设计

### 同步设置面板

在现有 Obsidian 同步面板中新增：

- 主按钮：`双向同步`
- 同步摘要：导入、从 Obsidian 更新、写入 Obsidian、待确认删除、失败
- 待确认删除列表

待确认删除列表每行展示：

- 笔记标题
- 原 Obsidian 路径
- 上次同步时间
- `确认删除`
- `保留并重新导出`

### 编辑器同步卡片

当前笔记同步卡片新增状态展示：

- `已同步`
- `同步失败`
- `Obsidian 已删除，待确认`
- `尚未同步`

如果当前笔记是 `external_deleted`：

- 主操作显示 `保留并重新导出`
- 次操作可以跳转或打开同步面板进行确认删除

## 错误处理

需要明确展示的错误：

- 未配置 Obsidian target。
- Vault 路径不存在或不是目录。
- base folder 创建失败。
- base folder 真实路径逃出 Vault。
- Markdown 文件读取失败。
- Markdown frontmatter 解析失败。
- FlowSpace 笔记创建或更新失败。
- Obsidian 文件写入失败。
- 确认删除时笔记不存在。

批量同步遇到单篇失败时继续处理其他笔记，最终通过 `failed` 数量和 `items` 明细返回。

## 测试计划

后端单元测试：

- 解析带 FlowSpace frontmatter 的 Markdown。
- 解析无 frontmatter 的 Obsidian 原生 Markdown。
- 从一级标题提取标题。
- 去掉重复一级标题正文。
- 扫描只包含 base folder 内 `.md` 文件。
- 路径逃逸被拒绝。
- Obsidian 新增文件导入 FlowSpace。
- Obsidian 修改覆盖 FlowSpace。
- 两边都修改时 Obsidian 优先。
- FlowSpace 新增笔记写入 Obsidian。
- Obsidian 删除已映射文件后状态变为 `external_deleted`。
- 确认删除后 FlowSpace 笔记被删除。
- 保留并重新导出后 Obsidian 文件恢复。
- Vault 不可访问时不标记删除。

前端验证：

- 同步面板显示双向同步入口。
- 同步摘要数量正确。
- 待确认删除列表可展示、确认删除、保留并重新导出。
- 当前笔记卡片能显示 `Obsidian 已删除，待确认`。
- 同步失败时错误信息可见。

手动验证：

- 在临时 Vault 的 base folder 中创建新 `.md`，执行双向同步后 FlowSpace 出现新笔记。
- 在 Obsidian 修改已同步文件，执行双向同步后 FlowSpace 内容更新。
- 在 FlowSpace 修改已同步笔记，执行双向同步后 Obsidian 文件更新。
- 两边同时修改，执行双向同步后 FlowSpace 使用 Obsidian 内容。
- 删除 Obsidian 文件，执行双向同步后 FlowSpace 只显示待确认删除；确认后才真正删除。
- 选择保留并重新导出后，Obsidian 文件重新生成。

## 分阶段实现

### 第一阶段：后端基础

- 增加 schema 迁移。
- 扩展 `note_sync_state` 字段。
- 抽出 Markdown 解析和路径扫描工具。
- 增加双向同步服务入口。
- 增加删除确认和恢复接口。
- 覆盖核心后端测试。

### 第二阶段：前端入口

- 扩展 sync API 类型。
- 同步面板新增双向同步按钮和摘要展示。
- 待确认删除列表接入 confirm/restore。
- 编辑器同步卡片展示 `external_deleted` 状态。

### 第三阶段：验证与收敛

- 用测试数据库和临时 Vault 做端到端验证。
- 保留现有单向同步能力，避免影响旧流程。
- 更新 README 中 Obsidian 同步说明。

## 后续扩展

- 支持多个 Obsidian 文件夹。
- 支持定时自动扫描。
- 支持文件监听实时同步。
- 支持 FlowSpace 删除后标记 Obsidian 待删除。
- 支持附件复制和 Markdown 图片链接处理。
- 支持更细粒度的冲突预览和人工合并。
