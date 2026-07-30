# Roadmap 节点子图、任务思维导图与文章绑定实施计划（迁移到当前代码基线）

> **实施约束：** 本计划定义后续编码顺序，不代表已经开始修改业务代码。每一个可观察行为都必须先出现能够因能力缺失而失败的测试（RED），再做最小实现（GREEN），最后只在绿色状态下重构（REFACTOR）。禁止先写生产代码再补测试，禁止提交红灯状态。

**目标：** 在现有 task-domain v2、Roadmap v2 和 workspace runtime 上增加 RoadmapNode 任务思维导图；思维导图中的每个可见节点引用真实 Task；文章精确绑定 Task；旧 RoadmapNode 资源进入可审计的待分配区。

**源设计：** `docs/superpowers/specs/2026-07-23-roadmap-node-mindmap-task-articles-design.md`

**领域语言：** `CONTEXT.md`

**当前状态：** 设计已确认；业务实现尚未开始。本计划已在 2026-07-30 按当前仓库重新基线。PostgreSQL/SQLite tenant migration 已到 `0009_manual_occurrence_timing.sql`，本能力从 `0010_roadmap_task_mindmaps.sql` 开始，不改写 `0001`–`0009`。

**技术栈：** Go、Gin、`database/sql`、PostgreSQL、modernc SQLite、React、TanStack Query、Vitest、Testing Library、Playwright。

---

## 0. 当前代码基线与迁移结论

以下事实是本版计划的前置条件；执行中若再次变化，先更新本节和目标文件地图：

1. tenant migration `0006`–`0009` 已分别被 mobile-v2 sync、mobile-v2 content、Task `attachment_links`、手动 Occurrence 排期占用；新表只能从 `0010` 开始。
2. `taskruntime.ExpectedTenantSchemaVersion` 当前仍指向 `0007_mobile_v2_content_domain.sql`。`0010` 必须先迁移到所有目标 tenant endpoint，再在启用生产路由的版本中把 schema gate 提升到 `0010_roadmap_task_mindmaps.sql`；不能先提升 gate 再补数据库。
3. 当前普通 v2 写入统一经过 `storage.TenantFencedWriter.BeginFencedWrite` 和 `storage.TenantWriteTx`。Mind Map 不新建第二套数据库事务；`BeginFencedMindMapWrite` 只能是这一现有 fence 的窄适配层。
4. 当前 `taskapp.RuntimeSnapshot` 已分别携带 `Roadmaps`、`RoadmapReader` 和通用 `Reader`。Mind Map 按相同模式增加 `MindMaps` 与 `MindMapReader`，不把完整 `storage.Store` 或 `*sql.DB` 暴露给 Facade/Handler。
5. 当前 `taskruntime.NewTransactionApplication` 会在 mobile-v2 已开启的 fenced transaction 上组装应用服务。新增 Mind Map transaction capability 必须复用传入的 `MobileV2TenantWriteTx`，禁止在 mobile 命令事务内嵌套开启新事务。
6. `domain_tasks_v2.attachment_links` 是 Task 的通用附件链接 JSON，不是文章资源表。它继续由现有 Task PATCH 管理，且当前不在 mobile-v2 wire payload 中；ArticleResource/TaskArticleLink 不复用、不回填也不覆盖该字段。
7. 当前 Roadmap Web 是纵向 timeline，不是可拖拽外层画布。Roadmap 节点主体和“添加任务”入口要迁移为进入子图；现有已经直接创建到 `roadmap_node_id`、但尚无 placement 的 Task 进入 `unplaced_tasks`，不能自动猜根或父节点。
8. 当前 legacy task-domain 迁移已经有 `taskmigration` 的 Roadmap snapshot/freeze/replay/reconcile 骨架；`roadmap_freeze_manifest.go` 目前只覆盖 roadmap、node、edge。资源 adopt 必须接入这条既有流水线，并额外覆盖已经完成 v2 cutover 的 workspace。
9. 当前 endpoint transfer 的 `tenantmigration.BaselineLogicalTables()` 和 SQLite `ExportTenantSnapshot` 只覆盖五张早期表，连现有 `domain_*_v2` 依赖闭包都尚未纳入。不能只复制六张 `0010` 表；必须先补齐当前 v2 Task/Roadmap 事实表，再用 capability-aware manifest 追加 Mind Map 表，同时不能要求 legacy/未迁移 workspace 存在这些表。
10. mobile-v2 第一版不增加 Mind Map/Article wire entity；但通过 Mind Map 创建真实 Task 时，必须继续走被 `TrackTaskDomainWriter` 包装的 `TaskDomainWriter`，确保现有 task-core change batch 不丢失。

---

## 1. 不可变实施决策

1. RoadmapNode 仍是宏观学习结构，不变成 Task。
2. Task Mind Map 的根节点和所有子节点都是真实 Task。
3. TaskPlacement 只保存拆解关系和布局，不复制 Task 标题、状态或排期。
4. Decomposition Relation 不创建或修改 TaskDependency。
5. 一个 Task 第一版最多出现在一个 RoadmapNode 的思维导图中。
6. ArticleResource 按 workspace + normalized URL 复用；TaskArticleLink 才表示归属。
7. legacy RoadmapNode 文章先进入 UnassignedArticle，只有用户明确选择 Task 后才建立 TaskArticleLink。
8. 创建 Task 聚合与 TaskPlacement 必须处于同一个 fenced transaction；不允许嵌套调用会另开事务的 TaskService。
9. 文章搜索是只读外部调用，不在数据库事务中执行；保存搜索结果是另一个使用当前 runtime epoch 的命令。
10. mobile-v2 本计划只保持 API 不受破坏，不增加移动端思维导图编辑能力。
11. 现有 `attachment_links` 保持通用附件语义；文章归属只能由 TaskArticleLink 表达。
12. Mind Map 写命令必须复用现有 `TenantFencedWriter`、事务关闭保护和 mobile-v2 task change tracking，不引入 raw SQL 旁路。

---

## 2. TDD 工作规则

### 2.1 单行为循环

每个任务严格重复以下循环：

1. **RED：** 只添加一个聚焦 case。
2. **验证 RED：** 运行最窄测试，确认失败来自目标行为尚不存在；编译错误、fixture 错误或数据库不可达不算有效 RED。
3. **GREEN：** 只写使该 case 通过的最小生产代码。
4. **验证 GREEN：** 重跑聚焦测试，再跑所在 package 或共享 contract。
5. **REFACTOR：** 只整理命名、提取纯函数或去重；重跑相同测试。
6. **提交：** 一个提交只包含一个可说明的绿色行为。

若新增测试第一次就是绿色，必须增强断言或修正 fixture，直到它能证明新能力此前确实缺失。

### 2.2 测试层级

| 层级 | 负责验证 | 不允许 |
| --- | --- | --- |
| 纯领域测试 | 根、父子、环、深度、节点上限、URL 规范化、settings 规范化 | 数据库、网络、真实时钟 |
| 应用服务测试 | 原子命令、runtime epoch、revision、幂等键、错误映射 | `storage.Store`、第二次事务 |
| Provider contract | PostgreSQL/SQLite 约束与语义一致 | provider 私有业务规则 |
| 迁移集成测试 | legacy adopt、tombstone、snapshot、replay、reconcile | 真实用户数据 |
| Handler 测试 | 认证、严格 DTO、HTTP 状态、稳定错误码 | 直接构造数据库连接 |
| 前端组件测试 | 路由、选择态、任务/文章隔离、冲突恢复 | 真实后端 |
| E2E | 从 Roadmap 到 Task 执行和进度回投 | 共享 workspace fixture |

### 2.3 数据安全

- SQLite 测试只使用 `t.TempDir()`。
- PostgreSQL contract 只读取专用测试环境变量，并拒绝数据库名不含 `test` 的连接。
- 本计划不得连接 AGENTS.md 中记录的开发 PostgreSQL 或 MinIO 做自动测试。
- 所有时间使用 fake clock 或显式 `Asia/Shanghai`/`UTC`，不依赖主机时区。
- 当前工作区存在其他未提交文件时，只暂存本计划目标文件，不回滚或覆盖其他改动。

---

## 3. 目标文件地图

执行时若仓库结构已变化，先修订本节再编码。

### 3.1 领域与应用

| 路径 | 计划职责 |
| --- | --- |
| `backend/internal/taskdomain/mindmap.go` | MindMap、TaskPlacement、树不变量与领域错误 |
| `backend/internal/taskdomain/mindmap_test.go` | 纯领域树结构 table-driven 测试 |
| `backend/internal/taskdomain/article_resource.go` | URL 与 source settings 规范化 |
| `backend/internal/taskdomain/article_resource_test.go` | URL、sources、长度和复用规则 |
| `backend/internal/taskdomain/mindmap_service.go` | 原子创建根/子 Task、放置、移动、布局、移除 |
| `backend/internal/taskdomain/mindmap_service_test.go` | fenced transaction 与 revision 测试 |
| `backend/internal/taskdomain/mindmap_repository.go` | request-scoped reader 和 transaction-bound writer contracts |
| `backend/internal/taskdomain/roadmap_service.go` | 扩展现有 node delete/replace guard，保护待分配文章 |
| `backend/internal/taskdomain/roadmap_service_test.go` | unassigned article/mind-map data 删除保护 |
| `backend/internal/taskapp/mindmap_facade.go` | runtime 解析、ID、command id、DTO 编排 |
| `backend/internal/taskapp/mindmap_facade_test.go` | epoch、幂等和错误传播测试 |
| `backend/internal/service/task_article_search.go` | 新 Task 文章搜索 adapter；不依赖 legacy Roadmap repository |
| `backend/internal/service/task_article_search_test.go` | provider 请求、凭据清理、超时和取消 |
| `backend/internal/taskapp/facade.go` | 在现有 `RuntimeSnapshot` 增加 `MindMaps`/`MindMapReader`，不复制 resolver |
| `backend/internal/taskruntime/factory.go` | 从现有 tenant reader/writer 组装 Mind Map service |
| `backend/internal/taskruntime/transaction_runtime.go` | 在已经 fenced 的 mobile-v2 transaction 上复用 Mind Map capability |
| `backend/internal/taskruntime/runtime_test.go` | schema gate、runtime 完整性和 stale epoch 测试 |

现有 `TaskService.CreateTask` 不能从 MindMapService 内部调用，因为它会再次进入 fenced write。实现应复用 `task_factory.go` 的 `TaskFactory.Build`/`BuildTaskAggregateSnapshot`，并通过同一个 `MindMapCommandTx` 同时取得被 mobile change tracker 包装的 `TaskDomainWriter` 与 `MindMapWriter`。

### 3.2 Schema 与 provider

| 路径 | 计划职责 |
| --- | --- |
| `backend/db/migrations/tenant/postgres/0010_roadmap_task_mindmaps.sql` | PostgreSQL 表、复合外键、CHECK、partial unique index |
| `backend/db/migrations/tenant/sqlite/0010_roadmap_task_mindmaps.sql` | SQLite 等价约束和索引 |
| `backend/internal/storage/contracttest/roadmap_mindmap_contract_tests.go` | 两个 provider 共用契约 |
| `backend/internal/storage/postgres/roadmap_mindmap.go` | PostgreSQL reader/writer |
| `backend/internal/storage/sqlite/roadmap_mindmap.go` | SQLite reader/writer |
| `backend/internal/storage/tenant.go` | 在现有 `TenantWriteTx` 暴露窄 `MindMapWriter` capability |
| `backend/internal/storage/postgres/tenant_writer.go` | 通过现有 PostgreSQL fence/tx 暴露 Mind Map reader/writer |
| `backend/internal/storage/sqlite/tenant_writer.go` | 通过现有 `BEGIN IMMEDIATE` fence/tx 暴露同等 capability |
| `backend/internal/storage/postgres/task_domain_runtime.go` | 让当前 request-scoped reader 实现 `MindMapReader` |
| `backend/internal/storage/sqlite/task_domain_runtime.go` | SQLite 同等接入 |
| `backend/internal/storage/mobile_v2_task_tracking.go` | 验证 Mind Map 创建 Task 仍产生现有 mobile task change |

### 3.3 HTTP 与路由

| 路径 | 计划职责 |
| --- | --- |
| `backend/internal/handler/roadmap_mindmap_v2.go` | MindMap、placement、article、settings DTO |
| `backend/internal/handler/roadmap_mindmap_v2_test.go` | handler contract |
| `backend/internal/handler/task_domain_v2.go` | 扩展现有应用接口组合并在 isolated v2 router 注册 Handler |
| `backend/internal/handler/task_domain_contract.go` | 复用统一严格 JSON、409/422 与稳定错误 envelope |
| `backend/internal/config/native.go` | 默认关闭的 `FLOWSPACE_ENABLE_ROADMAP_MIND_MAP` |
| `backend/cmd/server/main.go` | 将 release flag 接入 router Config |
| `backend/internal/router/task_domain_v2_routes.go` | model-aware v2-only 路由 |
| `backend/internal/router/task_domain_v2_routes_test.go` | release flag、legacy/v2 fail-closed 与 capability 测试 |
| `docs/runtime-settings-deployment.md` | 新 release flag 与 schema-first 配置 |
| `docs/task-domain-v2-rollout-runbook.md` | `0010` 迁移、启用、观测和回滚步骤 |

### 3.4 Web

| 路径 | 计划职责 |
| --- | --- |
| `frontend/src/api/roadmapMindMap.ts` | API 类型和 client |
| `frontend/src/api/roadmapMindMap.test.ts` | payload、错误和 revision contract |
| `frontend/src/hooks/useRoadmapMindMap.ts` | query/mutation/cache invalidation |
| `frontend/src/hooks/useRoadmapMindMap.test.tsx` | optimistic state 与冲突处理 |
| `frontend/src/api/taskDomain.ts` | 扩展现有 capabilities `features.roadmap_mind_map` 类型 |
| `frontend/src/api/taskDomain.test.ts` | capability 向后兼容与 feature gate |
| `frontend/src/routes/RoadmapMindMapRoute.tsx` | capability gate 与 lazy route |
| `frontend/src/routes/RoadmapMindMap.tsx` | 页面状态与 inspector 协调 |
| `frontend/src/routes/RoadmapMindMap.test.tsx` | 页面交互测试 |
| `frontend/src/components/roadmapMindMap/` | canvas、task node、toolbar、article panel、pending inbox |
| `frontend/src/styles/task-domain-v2.css` | 按当前样式组织追加 `.roadmap-mind-map-*` 响应式样式 |
| `frontend/src/router.tsx` | `/projects/:projectID/roadmap/nodes/:roadmapNodeID/mind-map` |
| `frontend/src/routes/RoadmapV2.tsx` | 将现有 timeline 节点和“添加任务”入口迁移为子图导航 |
| `frontend/src/routes/RoadmapV2.test.tsx` | 保留进度/重生成保护并补导航隔离 |
| `frontend/tests/e2e/roadmap-mind-map.spec.ts` | 浏览器主路径 |

### 3.5 Legacy adopt 与存储迁移

| 路径 | 计划职责 |
| --- | --- |
| `backend/internal/taskmigration/roadmap_resource_adopt.go` | legacy resource → ArticleResource + UnassignedArticle |
| `backend/internal/taskmigration/roadmap_resource_adopt_test.go` | 幂等 adopt、重复 URL、断点恢复 |
| `backend/internal/taskmigration/roadmap_freeze_manifest.go` | 将 `roadmap_resources` 纳入旧 writer 冻结边界 |
| `backend/internal/taskmigration/{mapper,legacy_snapshot_loader,coordinator}.go` | 增加 resource identity/snapshot 并接入既有 cutover state machine |
| `backend/internal/taskmigration/postgres_outbox_sql.go` | PostgreSQL freeze installer/final observer 覆盖 resource |
| `backend/internal/taskmigration/sqlite_outbox_sql.go` | SQLite 同等 freeze 覆盖 |
| `backend/internal/taskmigration/{reconcile,reconcile_store}.go` | legacy adopt 映射、新实体反向差集与 tombstone/FK 验证 |
| `backend/cmd/flowspace-admin/` | 为已完成 v2 cutover 的 workspace 提供可恢复 adopt 命令 |
| `backend/internal/tenantmigration/manifest.go` | capability-aware endpoint transfer 表集和依赖顺序 |
| `backend/internal/tenantmigration/{export,import,sql_adapter}.go` | 按 manifest 复制、namespace 和校验新实体 |
| `backend/internal/storage/sqlite/tenant_snapshot.go` | 保持旧 snapshot API 与 capability-aware manifest 一致 |
| 对应 PostgreSQL snapshot/endpoint transfer 实现 | 相同表集、行数和摘要语义 |

---

## 4. 阶段与依赖

```text
Phase 0  契约冻结与架构守卫
  ↓
Phase 1  纯领域树、URL 与资源设置
  ↓
Phase 2  PostgreSQL/SQLite schema 与 provider contract
  ↓
Phase 3  原子命令、应用 facade 与 HTTP API
  ↓
Phase 4  Web 路由、思维导图与任务文章交互
  ↓
Phase 5  legacy adopt、endpoint migration 与 reconcile
  ↓
Phase 6  E2E、故障注入、性能与灰度
```

Phase 1 完成后，Phase 2 的 PostgreSQL/SQLite provider 可分开实现，但必须共同通过同一 contract。Phase 3 API 稳定后，Web 和 legacy adopt 可并行；它们在 Phase 6 E2E 前汇合。

---

## Phase 0：契约冻结与架构守卫

### Task 0.1：记录基线

只运行现有测试并记录既有失败，不改生产代码：

```powershell
Set-Location backend
go test ./internal/taskdomain ./internal/taskapp ./internal/taskruntime ./internal/handler ./internal/router -count=1

Set-Location ../frontend
pnpm test
pnpm lint
pnpm build
```

**Checkpoint：** 基线可重复；新测试不能把既有失败伪装成 RED。

### Task 0.2：冻结 API 和错误码

**测试优先文件：**

- `backend/internal/handler/roadmap_mindmap_v2_test.go`
- `frontend/src/api/roadmapMindMap.test.ts`

按设计先写失败 contract：

- 空子图 GET 返回 `200` 与 `mind_map: null`；
- 未认证 `401`；
- legacy workspace 调用新 API 返回稳定 fail-closed 错误；
- 严格拒绝未知 JSON 字段；
- 固定 `mind_map_revision`、`placement_revision`、`settings_revision`；
- 固定 409/422 错误码和 `retryable`。

GREEN 只增加 DTO、领域错误和 `MapTaskDomainError` 映射、fake application，不在 `task_domain_v2_routes.go` 注册生产 model-aware 路由。

### Task 0.3：禁止事务绕过

**测试优先文件：** `backend/internal/taskdomain/architecture_test.go`

RED：

- MindMap handler/service 不得依赖 `storage.Store`、`*sql.DB` 或 legacy repository；
- request runtime 只暴露 reader；
- writer 只在 fenced callback 内可见；
- 创建根/子 Task 过程中只能调用一次 `BeginFencedMindMapWrite`；
- `BeginFencedMindMapWrite` 必须委托现有 `TenantFencedWriter.BeginFencedWrite`；
- `taskruntime.NewTransactionApplication` 只能复用已有 `MobileV2TenantWriteTx`；
- 创建 Task 必须从同一 tx 取得带 `TrackTaskDomainWriter` 的 writer；
- 禁止从 callback 内调用会再次进入 fence 的 `TaskService.CreateTask`。

GREEN 只建立接口方向，不实现行为。

**Checkpoint 0：** contract 与依赖方向锁定，数据库仍无新表。

---

## Phase 1：纯领域模型

### Task 1.1：MindMap 与 TaskPlacement 值对象

**RED：** `mindmap_test.go`

逐 case 实现：

- 空图可以创建一个 center 根 placement；
- 第二个根拒绝；
- 非根必须是 left/right；
- manual X/Y 必须同时为空或同时存在；
- task 不能成为自己的父节点；
- 同一 Task 不能重复放置；
- 跨 workspace/project/RoadmapNode 拒绝。

**GREEN：** `mindmap.go` 中只放类型、纯校验函数和错误。

### Task 1.2：树结构、环、深度与节点上限

按一个 case 一个 RED：

- 直接环、两节点环和深层间接环；
- 深度 6 允许、深度 7 拒绝；
- 第 200 个节点允许、第 201 个拒绝；
- 移动整个分支后重新计算最大深度；
- 父子变化不产生 TaskDependency，也不修改 Task lifecycle。

REFACTOR 后树校验只接收不可变 placement snapshot，不访问 repository。

### Task 1.3：文章 URL 规范化

**RED：** `article_resource_test.go`

- scheme/host 小写；
- 删除 fragment 与默认端口；
- 拒绝非 http(s)、无 host、用户名/密码；
- 保留有业务意义 query；
- 同一规范化 URL 得到相同 identity key；
- 不发起 DNS 或 HTTP 请求。

### Task 1.4：搜索设置规范化

RED：

- source id 去重并稳定排序；
- 未知 source 拒绝；
- custom prompt 最大 4000；
- 空 prompt 表示使用系统任务上下文；
- settings revision 独立于 Task 和 MindMap revision。

**Checkpoint 1：**

```powershell
Set-Location backend
go test ./internal/taskdomain -run 'MindMap|Placement|Article|ResourceSettings' -count=1
```

---

## Phase 2：Schema 与 provider contract

### Task 2.1：先写失败的 schema contract

**测试优先文件：** `roadmap_mindmap_contract_tests.go`

共享 contract 必须先在两个 provider 都失败，然后才增加 `0010`：

- 六张业务表（含待分配收件箱）存在；
- workspace/project/RoadmapNode/Task 复合外键不可绕过；
- partial unique index 保证至多一个根；
- TaskResourceSettings 一 Task 一行；
- normalized URL 在 workspace 内唯一；
- 同文章可关联多个 Task，同 Task 不可重复关联；
- `attachment_links` 的增删不创建 ArticleResource，文章 link 的增删也不改写 `attachment_links`；
- pending/assigned 状态与 assigned_task_id CHECK；
- X/Y 成对 NULL；
- RoadmapNode 删除不能级联丢失 Mind Map 或 UnassignedArticle；
- 枚举与长度 CHECK 一致。

表集：

1. `domain_roadmap_mindmaps_v2`
2. `domain_task_mindmap_nodes_v2`
3. `domain_article_resources_v2`
4. `domain_task_article_links_v2`
5. `domain_task_resource_settings_v2`
6. `domain_unassigned_roadmap_articles_v2`

### Task 2.2：PostgreSQL migration GREEN

只添加 `0010_roadmap_task_mindmaps.sql`，使 PostgreSQL schema cases 通过。不得修改 `0001`–`0009`。

特别验证：

- partial unique root index；
- composite FK 列顺序与被引用 unique key一致；
- 所有 revision `>= 1`；
- RoadmapNode FK 对齐现有 `domain_roadmap_nodes_v2(workspace_id, project_id, roadmap_id, id)` unique key，ArticleResource FK 对齐 `(workspace_id, id)`；
- 当前 `domain_tasks_v2` 没有 `(workspace_id, project_id, roadmap_node_id, id)` unique key；`0010` 必须先增加跨 PostgreSQL/SQLite 等价的 unique constraint/index，placement/settings/link 再用它直接约束 Task 所属 RoadmapNode；
- migration 幂等写入 schema capability `roadmap_task_mindmap_v1=true`，供 endpoint transfer 与 API feature 检查；
- migration 重跑由 `tenant_schema_migrations` 幂等管理，不在普通 `OpenTenant` 自动 DDL。

### Task 2.3：SQLite migration GREEN

实现等价 schema；不能用应用层检查替代 SQLite 可表达的 FK/CHECK/partial index。开启并验证 `PRAGMA foreign_keys=ON`。

### Task 2.4：reader/writer provider

按 contract case 逐步实现：

- 查询空图、完整图、unplaced tasks、unassigned articles；
- root/child 原子创建回滚；
- layout batch CAS；
- placement CAS；
- article upsert + link；
- settings CAS；
- pending article assignment CAS；
- transaction callback 返回后 writer 失效；
- `TenantWriteTx.TaskDomainWriter()` 仍经过现有 mobile-v2 tracker；`MindMapWriter()` 不返回 raw runner。

每个 provider 必须运行相同 suite：

```powershell
Set-Location backend
go test ./internal/storage/sqlite -run RoadmapMindMap -count=1
$env:FLOWSPACE_REQUIRE_POSTGRES_TESTS='true'
go test -p 1 ./internal/storage/postgres -run RoadmapMindMap -count=1
```

### Task 2.5：runtime 组装与 schema gate

**RED：**

- `taskruntime.Factory` 对 v2 runtime 要求 reader 实现 `MindMapReader`、writer 实现 `MindMapCommandFencer`；
- `RuntimeSnapshot` 同时提供 `MindMaps` 和 `MindMapReader`，但通用 `Reader` 仍不暴露 writer/Store；
- `NewTransactionApplication` 在已有 mobile-v2 tx 上组装相同 capability，调用时不增加第二次 `BeginFencedWrite`；
- expected schema 为 `0010` 时，只有 `0009` 的 endpoint 必须 fail closed；
- 已应用 `0010` 的 SQLite/PostgreSQL endpoint 可以正常解析 runtime；
- stale runtime epoch 返回现有 `tenant_epoch_mismatch`，不降级到 legacy。

**GREEN：**

1. 先发布并执行 `migrate-tenant` 应用 `0010`；
2. 验证所有目标 endpoint 的 `tenant_schema_migrations` 含 `0010_roadmap_task_mindmaps.sql` 和正确 checksum；
3. 再把 `taskruntime.ExpectedTenantSchemaVersion` 从当前 `0007_mobile_v2_content_domain.sql` 提升到 `0010_roadmap_task_mindmaps.sql`；
4. 在 `factory.go`、`transaction_runtime.go` 组装 service/reader；不得让 `OpenTenant` 自动跑 DDL。

**Checkpoint 2：** PostgreSQL/SQLite contract 全绿；schema-first 部署验证完成；普通 resolver 打开连接不运行 `0010`，但在生产路由启用版本中拒绝缺少 `0010` 的 endpoint。

---

## Phase 3：原子命令、Facade 与 HTTP

### Task 3.1：创建根 Task

**RED：** `mindmap_service_test.go`

验证同一个 fenced transaction 内：

1. 校验 learning Project 和 RoadmapNode；
2. 构造 Task、ScheduleVersion 和初始 Occurrence；
3. 创建 RoadmapMindMap；
4. 创建 root TaskPlacement；
5. 任一步失败全部回滚；
6. Facade 生成的 command id/actor/time 正确进入审计结果；
7. Task 写入通过现有 tracked `TaskDomainWriter`，mobile task-core change 不丢失；
8. stale runtime epoch 不重试到 legacy。

GREEN 复用纯 `TaskFactory.Build`，由 Facade 生成 Task/command ID，通过一个 `MindMapCommandTx` 写 Task 和 MindMap。`MindMapCommandTx` 是现有 `TenantWriteTx` 的窄领域视图，不得调用 `TaskService.CreateTask`。

### Task 3.2：创建子 Task 与放置已有 Task

RED：

- parent 必须在同一图；
- 已有 Task 必须属于同 project/RoadmapNode；
- 空图选择已有 Task 时，在同一 fenced tx 创建 RoadmapMindMap 和 center root placement；
- 已有 root 后再次省略 parent/请求 center 必须拒绝；
- 节点/深度上限；
- 创建 Task 成功但 placement 失败时全部回滚；
- 已有 Task placement 不修改 Task 内容；
- 已有 `attachment_links` 原样保留；
- 并发创建导致 revision stale 时只有一个成功。

### Task 3.3：移动、布局与移除

按行为拆分：

- 单 placement PATCH 使用 placement revision；
- 批量布局 PUT 使用 mind-map revision；
- 移动分支进行环和深度复检；
- 第一版只移除叶子；
- 移除在同一 tx 内 CAS 更新 Task 并清空 `roadmap_node_id`，保留 Task、Schedule、Occurrence、ExecutionLog、`attachment_links` 和文章关联；
- 清空 `roadmap_node_id` 必须提升 Task revision，并通过 tracked writer 发布现有 mobile task-core change；
- 归档 Task 只改变显示，不删除 placement。

### Task 3.4：手动文章与设置

RED：

- 只允许给当前图内 Task 添加；
- 同 URL 复用 ArticleResource；
- 同 idempotency key 不重复 link；
- 删除 link 不影响其他 Task；
- 添加/删除文章不读写 `domain_tasks_v2.attachment_links`；
- Task PATCH 修改 `attachment_links` 不创建、删除或重排 TaskArticleLink；
- settings 使用独立 CAS；
- unassigned article 分配时同事务创建 link 并更新状态；
- 两个并发分配只有一个成功。

### Task 3.5：文章搜索

拆成两个阶段：

1. 在当前 runtime 读取 Task、祖先路径和 settings，形成不可变 SearchSnapshot；
2. 事务外调用搜索服务，只返回候选，不自动持久化。

测试必须证明：

- 自定义 prompt 实际进入请求；
- 兄弟 Task 的 prompt/articles 不泄漏；
- 搜索失败不返回固定模板；
- profile/endpoint 使用任务创建时的资源快照；
- 保存候选时重新解析当前 runtime epoch；
- API Key、URL 凭据不进日志。

当前 `service.SearchRoadmapNodeResources` 接收完整 `storage.Store`，并写入 legacy `roadmap_resources`，不能从新 Handler/Facade 调用。可在保持 legacy 测试绿色的前提下提取其纯 query/ranking/provider 逻辑，但新入口必须是注入式 `TaskArticleSearchService`：输入不可变 SearchSnapshot，输出候选，不接触 tenant repository、不自动保存。

### Task 3.6：RoadmapNode 删除与重生成保护

当前 `RoadmapService.DeleteNode`/`ReplaceNodes` 只通过 `CountRoadmapNodeTasks` 保护执行数据。`0010` 上线后补 RED：

- 有 Mind Map Task 时继续返回现有 `roadmap_node_has_tasks`；
- 没有 Task、但有 pending/assigned/dismissed UnassignedArticle 时，删除节点和重新生成 Roadmap 都返回稳定 `409 roadmap_node_has_mind_map_data`；
- provider FK 使用 RESTRICT/NO ACTION 作为最后防线，不能 cascade 删除 placement、link 或待分配记录；
- 保护检查和 delete/replace 必须在现有 `BeginFencedRoadmapWrite` 内完成；
- 冲突由 `MapTaskDomainError` 映射，不能把 PostgreSQL/SQLite FK 文本暴露为 500；
- 明确 dismiss 不等于授权删除历史 adopt 审计；若需要清理，另建显式 purge 命令。

GREEN 扩展现有 `RoadmapCommandTx` 的窄计数/存在性 capability，不让 `RoadmapService` 依赖 Mind Map repository 实现或 raw SQL。

### Task 3.7：Handler 与 model-aware 路由

路由：

```text
GET    /api/roadmap-nodes/:roadmapNodeID/mind-map
POST   /api/roadmap-nodes/:roadmapNodeID/mind-map/tasks
POST   /api/roadmap-nodes/:roadmapNodeID/mind-map/placements
PATCH  /api/roadmap-nodes/:roadmapNodeID/mind-map/tasks/:taskID/placement
PUT    /api/roadmap-nodes/:roadmapNodeID/mind-map/layout
POST   /api/tasks/:taskID/articles/search
POST   /api/tasks/:taskID/articles
DELETE /api/tasks/:taskID/articles/:articleID
PATCH  /api/tasks/:taskID/article-search-settings
POST   /api/roadmap-nodes/:roadmapNodeID/unassigned-articles/:articleID/assign
POST   /api/roadmap-nodes/:roadmapNodeID/unassigned-articles/:articleID/dismiss
```

实现顺序：

1. 增加默认关闭的 `FLOWSPACE_ENABLE_ROADMAP_MIND_MAP`，接入 native config、router Config 和配置测试；
2. 扩展 `/api/task-domain/capabilities`，在不泄露 endpoint/schema 的前提下返回 `features.roadmap_mind_map`；
3. 在 `roadmap_mindmap_v2.go` 使用现有 `DecodeTaskDomainRequest`/`writeTaskDomainError`；
4. 在 `TaskDomainV2Application` 增加 Facade 方法；
5. 在不破坏现有 `RegisterTaskDomainV2RoutesWithAI` 调用方的前提下，用窄 services/options 组合注入 `TaskArticleSearchService`；不得把 `storage.Store` 加回 `newTaskDomainV2Application`；
6. 在 isolated v2 router 注册实际 handler；
7. 最后仅在 release flag 开启时，于 `registerModelAwareTaskDomainRoutes` 注册相同路径为 `v2Only`。

全部注册为 v2-only。legacy workspace 必须 fail-closed，不转发到旧 Roadmap resource handler。文章搜索继续使用 workspace AI/settings resolver，但不得让 Handler 接触 endpoint 凭据或 tenant DB。

**Checkpoint 3：**

```powershell
Set-Location backend
go test ./internal/taskdomain ./internal/taskapp ./internal/taskruntime ./internal/handler ./internal/router -run 'MindMap|Article|Roadmap' -count=1
```

---

## Phase 4：Web 思维导图

### Task 4.1：API client 与 hooks

先写 `roadmapMindMap.test.ts` 和 hook 测试：

- 空图、完整图和待分配区解析；
- 各 revision 进入正确 payload；
- 409 不进行盲目 optimistic overwrite；
- query key 延续现有 `['task-domain', ...]` 前缀，Mind Map key 至少包含 `projectID` 与 `roadmapNodeID`；
- 成功 mutation 只失效当前 RoadmapNode、相关 Task、`roadmap-v2` project key 和 task list/detail key；
- 取消请求不会显示过期文章搜索结果。

### Task 4.2：外层 Roadmap 导航

**RED：** `RoadmapV2.test.tsx`

- 在当前 timeline 中点击节点标题/主体进入 mind-map route；
- 现有“编辑”“删除”“重新生成”操作不触发导航；
- 当前“添加任务”按钮不再直接调用 `useCreateTaskMutation` 留下无 placement Task，而是进入 mind-map 并打开创建/整理入口；
- 既有直接关联到 `roadmap_node_id` 的 Task 在子图中显示为 `unplaced_tasks`；
- 从子图返回时通过 route state/hash 恢复 focused timeline node 和浏览位置；不再测试当前页面不存在的 zoom/drag 状态；
- 深链接刷新仍能通过 projectID + roadmapNodeID 加载。

GREEN 复用现有 `TaskDomainGate`、`useTaskDomainCapabilities` 和 lazy route 结构；同一 capability 响应还必须确认 `features.roadmap_mind_map=true`，不要增加第二套 capability 请求。

### Task 4.3：空图与根 Task

组件测试覆盖：

- 空图显示“创建根任务”和“选择已有任务”；
- 首个节点固定 center；
- 加载/空/错误/无权限状态可区分；
- 创建失败不留下本地幽灵节点；
- 成功后选中根节点并打开 inspector。

### Task 4.4：树布局与交互

- 使用树/思维导图布局，与当前外层 Roadmap timeline 保持清晰视觉层级；
- 自动布局只读取 Decomposition Relation；
- 拖动只更新坐标；
- 换父节点才改变拆解关系；
- 折叠只改变 placement；
- 最大 200 节点时保持可操作，必要时虚拟化 inspector 列表；
- 键盘可选中节点，焦点样式清晰。

### Task 4.5：Task inspector 与文章

RED：

- 选择 Task 只显示该 Task 的文章和 settings；
- 同文章绑定两个 Task 时两边独立可见；
- 兄弟节点不串数据；
- 搜索 prompt 修改后下一次请求 payload 改变；
- 待分配文章必须明确选择 Task；
- “全部关联到根任务”需要二次确认；
- 409 显示“数据已更新，请刷新”，不覆盖服务器值；
- 归档 Task 视觉弱化但仍可打开历史文章。

**Checkpoint 4：**

```powershell
Set-Location frontend
pnpm exec vitest run roadmapMindMap RoadmapMindMap RoadmapV2
pnpm lint
pnpm build
```

---

## Phase 5：Legacy adopt 与存储迁移

### Task 5.1：legacy resource 冻结

当前 `LegacyRoadmapFreezeTriggerManifest` 只列出 `learning_roadmaps`、`roadmap_nodes`、`roadmap_edges`。先扩展 manifest/installer/final observer 测试，再把 `roadmap_resources` 的 INSERT/UPDATE/DELETE 纳入 migration active 时的数据库写栅栏。

必须同步修改 PostgreSQL/SQLite installer 中现有硬编码 Roadmap 表集，证明：

- `LegacyEntityRoadmapResource` identity 和 legacy snapshot loader 能稳定读取 `roadmap_resources.id`、所属 node、URL 与元数据；
- migration idle/failed 时 logical-version ledger 行为不变；
- migration active/fenced 时三种 resource 写入都被数据库拒绝；
- node 级 cascade delete 不能绕过 resource tombstone/freeze 审计；
- cutover 前已经排空不认识 `0010` 新实体的旧实例。

### Task 5.2：资源 adopt

**RED：** `roadmap_resource_adopt_test.go`

- 每个 legacy resource 规范化为 ArticleResource；
- 同 URL 只创建一个资源；
- 每条旧资源创建一个 pending UnassignedArticle；
- 重复 adopt 幂等；
- 中途失败可从 ledger/version 恢复；
- 删除使用 tombstone，不通过回读已删除源行；
- 不创建 TaskArticleLink。

同一 adopt service 提供两个入口：

1. **尚在 legacy → v2 cutover 的 workspace：** 接入现有 `taskmigration` snapshot/backfill/reconcile 流水线，在 resource freeze 生效后生成 pending UnassignedArticle。
2. **已经完成 v2 cutover 的 workspace：** 增加显式 `flowspace-admin` adopt 命令；用现有 `TenantMigrationFencer` fence workspace、分页执行、持久化 checkpoint，完成后同步 control/tenant epoch 再恢复 active。

两个入口共享 normalized URL、identity、ledger 和故障恢复逻辑，重复运行结果一致。不得假设重新执行完整 task-domain cutover。

### Task 5.3：已有 Task 待整理

当前 `RoadmapV2` 已经可以直接创建带 `roadmap_node_id` 的 Task。所有已有 `Task.roadmap_node_id` 但没有 placement 的记录由 Mind Map 查询返回 `unplaced_tasks`：

- 不自动猜 root/parent；
- 不改 Task title/schedule/attachment links；
- 用户 placement 后才离开待整理区；
- 第一个被用户明确放置的 Task 可以成为 root，后续 Task 必须选择 parent/branch；
- 从 timeline 进入子图时不得再次创建同标题的替代 Task。

### Task 5.4：endpoint snapshot/import/verify

当前 endpoint transfer 使用 `tenantmigration.Export`/`Import`/`Verify` 的 fenced snapshot，而不是增量 outbox replay。不要为本能力再造第二套 endpoint outbox。改为先让表集 capability-aware：

- 表集按物理 schema version/capability 选择，不按当前 `model_version` 省略数据；旧 schema 继续使用现有 baseline，已具备 v2 schema 的 legacy/backfilling/cutover workspace 也必须复制 shadow、ledger 和恢复状态；
- 具备 v2 schema 时先加入当前依赖闭包：`workspace_task_domain_state`、`domain_projects_v2`、`domain_learning_roadmaps_v2`、`domain_roadmap_nodes_v2`、`domain_roadmap_edges_v2`、`domain_tasks_v2`、`domain_task_dependencies_v2`、`domain_task_schedules_v2`、`domain_task_schedule_versions_v2`、`domain_task_occurrences_v2`、`domain_task_execution_logs_v2`；
- v2 Task manifest 必须包含 `0008` 的 `attachment_links` canonical JSON，Occurrence manifest 必须包含 `0009` 的实际 timing/manual override 字段，不能按旧 `0002` 列集截断；
- 同一 RED 审计 `0005`–`0007` mobile ledger/sync/content 表是否属于 endpoint continuity，不能默认为已覆盖；不属于本次复制的 ephemeral 表必须有可重建证明；
- schema 含 `0010` 且 `roadmap_task_mindmap_v1` capability 开启时，再在 v2 事实表之后加入六张新表；
- `Export` 读取 schema/capabilities 后再选择表集，不再无条件循环 `BaselineLogicalTables()`；
- `Import` 按依赖顺序写入，并正确 namespace 所有主键和复合外键；
- `Verify` 检查 row count、primary key hash、critical hash、max revision 与 FK audit；
- SQLite `ExportTenantSnapshot` 与权威 manifest 保持一致或委托同一表集选择器；
- PostgreSQL/SQLite 对相同 logical rows 产生相同摘要。

依赖顺序：

```text
workspace_task_domain_state
  → Project
  → Roadmap → RoadmapNode / RoadmapEdge
  → Task → Dependency / Schedule → ScheduleVersion → Occurrence → ExecutionLog
  → RoadmapMindMap
  → TaskPlacement
  → ArticleResource
  → TaskArticleLink / TaskResourceSettings / UnassignedArticle
```

### Task 5.5：故障恢复

每个持久边界至少一个故障注入：

- resource freeze 安装一半时回滚；
- snapshot/adopt 分页后崩溃；
- article upsert 后、link 前回滚；
- adopt 完成但 checkpoint/watermark 未提交；
- endpoint import 部分表后回滚；
- cutover CAS 失败；
- stable-v2 adopt 完成但 control epoch 尚未同步；
- 反向迁移 job。

**Checkpoint 5：** legacy 与已 cutover workspace 的旧资源都能完整保留、明确分配、重复恢复；capability-aware endpoint transfer 不漏新实体，也不要求未迁移 workspace 存在 `0010` 表。

---

## Phase 6：E2E、性能与发布

### Task 6.1：SQLite v2 E2E

独立 workspace 执行：

```text
登录
→ 创建 learning Project 与 RoadmapNode
→ 通过现有通用 Task API 创建一个带 roadmap_node_id、但没有 placement 的 Task
→ 点击外层节点进入空图
→ 验证既有 Task 出现在 unplaced_tasks，并明确放置为根或另建根 Task
→ 创建两个子 Task
→ 修改其中一个子 Task 的文章搜索提示词
→ 验证请求与结果发生变化
→ 给两个 Task 绑定同一 URL
→ 完成一个 Occurrence
→ 返回外层 Roadmap 验证进度
→ 再进入子图验证结构、状态、文章和布局
```

### Task 6.2：PostgreSQL contract E2E

在专用测试库复跑核心路径，额外验证：

- 复合外键；
- 并发 mind-map revision；
- 并发 unassigned assignment；
- 相同 URL upsert；
- 迁移 checksum；
- 只有 `0009` 时 runtime schema gate fail closed，应用 `0010` 后恢复；
- Mind Map 创建 Task 会产生现有 mobile task-core change，纯 placement/article mutation 不改变 mobile-v2 OpenAPI。

### Task 6.3：性能门禁

固定 200 节点、每节点 10 篇文章的 fixture：

- GET mind-map 不出现 N+1；
- provider 查询数量有上限；
- 前端首次可交互时间和布局耗时记录基线；
- 批量布局为单命令，不逐节点发 200 个请求；
- 搜索候选分页/限制，禁止无限结果进入页面。

### Task 6.4：全量回归与灰度

```powershell
Set-Location backend
go test ./... -count=1
go vet ./...

Set-Location ../frontend
pnpm test
pnpm lint
pnpm build
pnpm exec playwright test tests/e2e/roadmap-mind-map.spec.ts
```

灰度顺序：

1. 发布包含 `0010` migration、但保持 `ExpectedTenantSchemaVersion="0007_mobile_v2_content_domain.sql"` 且 `FLOWSPACE_ENABLE_ROADMAP_MIND_MAP=false` 的 schema-first 版本；
2. 对目标 tenant endpoint 执行 `migrate-tenant`，核对 `0010` checksum、六张表和 capability；
3. 验证 endpoint transfer 后，再发布把 `ExpectedTenantSchemaVersion` 提升到 `"0010_roadmap_task_mindmaps.sql"` 的应用版本；
4. 在内部环境开启 release flag，先只授权测试人员验证空图 GET 和已有 Task 整理，再逐步执行写路径；
5. 开启根/子 Task 写入，验证 task-core mobile change 和 Roadmap progress；
6. 开启文章写入及 stable-v2/legacy adopt；
7. 观察 409、fence、adopt checkpoint、外部搜索错误率后扩大环境/endpoint 范围。

首次新实体写入后不承诺回拨到不认识这些表的应用版本。应用回滚版本必须继续读写新 schema 或保持对应功能 fail-closed。

---

## 5. 完成定义

只有同时满足以下条件才算完成：

- 设计中的全部领域不变量都有至少一个先失败后通过的测试；
- PostgreSQL 与 SQLite 共用 contract 全绿；
- 根/子 Task 创建经故障注入证明无半成品；
- 文章只绑定明确 Task，legacy 文章无静默归属；
- 通用 `attachment_links` 与 TaskArticleLink 双向隔离；
- 搜索 prompt 修改能在 outbound request 中被观测；
- 外层 Roadmap timeline 与内层思维导图语义、路由和视觉交互分离；
- 现有 Today、Calendar、Task detail 和 Roadmap progress 继续以 Task/Occurrence 为事实源；
- `taskmigration` resource freeze/adopt/reconcile 覆盖 legacy 资源及 tombstone；
- capability-aware endpoint snapshot/import/verify 覆盖当前 v2 依赖闭包、`0008`/`0009` 字段和六张新表；
- 生产 schema gate 已提升到 `0010`，release flag 和 capability 返回均可 fail closed；
- mobile-v2 contract 未被破坏；
- 全量后端、前端、lint、build 与目标 E2E 全绿；
- 发布与回滚边界记录到 runbook。

在 Phase 0 contract 和架构守卫通过之前，不新增 `0010` migration；在 schema-first 部署和 Phase 3 原子性测试通过之前，不提升 `ExpectedTenantSchemaVersion`、不打开 release flag；在 Phase 5 迁移验证通过之前，不对已有 workspace 开启文章 adopt。
