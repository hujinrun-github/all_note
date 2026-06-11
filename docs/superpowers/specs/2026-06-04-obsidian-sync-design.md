# FlowSpace Obsidian 本地同步设计

日期：2026-06-04

## 目标

第一版先实现 FlowSpace 到 Obsidian Vault 的本地 Markdown 同步。FlowSpace 继续作为笔记主数据源，Obsidian 作为可阅读、可检索、可长期保存的本地 Markdown 副本。

本阶段不做 Notion 同步，也不做 Obsidian 到 FlowSpace 的回写。这样可以先把本地文件导出链路、状态记录、错误处理和 UI 操作路径做稳，再为后续双向同步留下扩展点。

## 非目标

- 不实现 Notion API 集成。
- 不实现 Obsidian 插件。
- 不监听 Vault 文件变化。
- 不把 Obsidian 中的修改合并回 FlowSpace。
- 不处理图片、附件、嵌入文件。
- 不做复杂冲突解决。
- 不把同步功能做成后台常驻文件监控服务。

## 用户体验

### 设置入口

在笔记模块增加“同步设置”入口。第一版可以放在笔记列表页右上角或编辑器右侧信息栏中。

用户需要配置：

- Obsidian Vault 路径，例如 `D:\Obsidian\MyVault`
- FlowSpace 同步目录，例如 `FlowSpace Notes`
- 是否保存后自动同步

配置保存后，系统提供“测试路径”操作。测试会确认：

- 路径存在
- 路径是目录
- 当前进程有写入权限
- 目标同步目录可创建

### 同步入口

第一版支持三种同步：

- 当前笔记同步
- 当前文件夹同步
- 全部笔记同步

编辑器中显示当前笔记的同步状态：

- 未同步
- 已同步
- 待同步
- 同步失败

失败状态需要展示简短原因，例如路径不存在、权限不足、文件写入失败。

### 自动同步

如果用户开启自动同步，笔记保存成功后触发一次单篇同步。自动同步失败不阻塞保存，只更新同步状态并显示失败提示。

## Markdown 输出格式

每篇 FlowSpace 笔记导出为一个 `.md` 文件。文件内容包含 YAML frontmatter：

```md
---
id: n01
source: flowspace
folder: __work
tags:
  - 产品
  - 规划
created: 2026-06-01T10:20:00+08:00
updated: 2026-06-04T11:30:00+08:00
---

# 笔记标题

正文内容...
```

正文使用当前 `notes.body` 中保存的 Markdown 内容。标题规则：

- 默认把 `notes.title` 写成一级标题。
- 如果正文已经以同名一级标题开头，可以避免重复标题。
- 文件名默认使用笔记标题。
- 文件名需要过滤 Windows 非法字符：`<>:"/\|?*`
- 标题为空时使用 `Untitled-{note_id}.md`
- 重名时使用 `标题-{note_id短码}.md`

## 数据模型

新增 `sync_targets` 表，用于保存同步目标配置：

```sql
CREATE TABLE IF NOT EXISTS sync_targets (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  vault_path TEXT NOT NULL,
  base_folder TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  auto_sync INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

第一版只支持 `type = 'obsidian'`。

新增 `note_sync_state` 表，用于保存笔记和外部文件的映射关系：

```sql
CREATE TABLE IF NOT EXISTS note_sync_state (
  note_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  external_path TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  last_synced_at INTEGER,
  status TEXT NOT NULL,
  error_message TEXT,
  PRIMARY KEY (note_id, target_id),
  FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
  FOREIGN KEY (target_id) REFERENCES sync_targets(id) ON DELETE CASCADE
);
```

`content_hash` 用导出的 Markdown 内容计算，判断本地笔记是否已经同步。

## 后端接口

新增同步接口组：

```http
GET    /api/sync/targets
POST   /api/sync/targets
PATCH  /api/sync/targets/:id

POST   /api/sync/obsidian/test
POST   /api/sync/obsidian/notes/:id
POST   /api/sync/obsidian/folders/:folder_id
POST   /api/sync/obsidian/all

GET    /api/notes/:id/sync-state
```

接口行为：

- `POST /api/sync/targets` 保存或创建 Obsidian 配置。
- `POST /api/sync/obsidian/test` 验证路径可用性。
- `POST /api/sync/obsidian/notes/:id` 导出单篇笔记。
- `POST /api/sync/obsidian/folders/:folder_id` 导出指定文件夹笔记。
- `POST /api/sync/obsidian/all` 导出全部笔记。
- `GET /api/notes/:id/sync-state` 返回当前笔记同步状态。

批量同步接口返回汇总：

```json
{
  "synced": 12,
  "failed": 1,
  "items": [
    {
      "note_id": "n01",
      "status": "synced",
      "external_path": "D:\\Obsidian\\MyVault\\FlowSpace Notes\\产品规划.md"
    }
  ]
}
```

## 后端模块边界

新增模块建议：

- `internal/model/sync.go`
- `internal/repository/sync.go`
- `internal/service/obsidian_sync.go`
- `internal/handler/sync.go`

职责划分：

- handler 只做参数解析和 HTTP 响应。
- service 负责路径校验、Markdown 渲染、文件写入、hash 计算和同步状态更新。
- repository 负责同步目标和同步状态的读写。

## 路径安全

本功能允许后端写本地文件，必须做路径限制：

- 只允许写入配置的 `vault_path/base_folder` 内。
- 所有文件名都必须经过清洗。
- 拼接路径后使用绝对路径校验，确认最终路径没有逃逸出 vault。
- 不接受前端直接传入任意输出文件路径。
- Windows 路径分隔符统一由 `filepath.Join` 处理。

## 同步规则

第一版采用单向覆盖规则：

- FlowSpace 是主数据源。
- 每次同步都会覆盖对应 Markdown 文件。
- 如果外部文件不存在，则重新创建。
- 如果外部文件被用户修改，第一版不会合并，下一次同步会覆盖。
- 如果 FlowSpace 删除笔记，第一版不主动删除 Obsidian 文件，避免误删用户文件。

后续可以增加“外部修改检测”。第一版只在同步状态里保存 `content_hash`，为后续冲突检测留出基础。

## 前端设计

### 笔记列表页

在工具区增加“同步”按钮，打开同步面板。

面板内容：

- 当前同步目标名称
- Vault 路径
- 同步目录
- 自动同步开关
- 测试路径按钮
- 同步当前文件夹按钮
- 同步全部按钮

### 编辑器页

在右侧 inspector 增加“Obsidian 同步”卡片：

- 当前状态
- 上次同步时间
- 目标文件路径
- “同步当前笔记”按钮
- 如果失败，显示错误原因

自动保存成功后，如果开启自动同步，触发单篇同步并刷新状态。

## 错误处理

需要覆盖的错误：

- 未配置 Obsidian 目标
- Vault 路径不存在
- 无写入权限
- 文件名清洗后为空
- 文件写入失败
- 笔记不存在
- 批量同步部分失败

批量同步应尽量继续执行，不因单篇失败中断整批任务。最终返回成功数、失败数和失败明细。

## 测试计划

后端测试：

- 文件名清洗规则
- Markdown frontmatter 渲染
- 路径逃逸防护
- 单篇同步成功
- 文件写入失败时状态更新为 failed
- 批量同步部分失败时返回汇总

前端验证：

- 未配置时显示设置入口
- 配置保存后可测试路径
- 单篇同步后状态变为已同步
- 同步失败时展示错误
- 移动端同步入口不遮挡现有底部导航

手动验证：

- 指定一个临时目录作为 Vault
- 同步单篇笔记，确认生成 Markdown
- 在 Obsidian 打开临时 Vault，确认文件可见、frontmatter 可识别
- 修改 FlowSpace 笔记后再次同步，确认文件更新

## 分阶段实现

### 第一阶段

- 数据表和 repository
- Obsidian 目标配置接口
- 路径测试接口
- Markdown 渲染和单篇同步

### 第二阶段

- 批量同步接口
- 同步状态查询
- 编辑器同步卡片
- 笔记列表同步面板

### 第三阶段

- 保存后自动同步
- 批量结果 UI
- 同步失败重试

## 后续扩展

后续支持 Notion 时，不复用 Obsidian 文件写入逻辑，只复用同步目标、同步状态和同步任务抽象。Notion 应作为新的 `sync_targets.type = 'notion'` 实现。

后续支持双向同步时，需要增加外部修改时间、外部内容 hash、冲突状态和用户选择策略。第一版的 `content_hash` 和 `external_path` 已经为此预留基础。
