# Roadmap 节点子图、任务思维导图与文章绑定实施计划

> **实施约束：** 本计划定义后续编码顺序，不代表已经开始修改业务代码。每一个可观察行为都必须先出现能够因能力缺失而失败的测试（RED），再做最小实现（GREEN），最后只在绿色状态下重构（REFACTOR）。禁止先写生产代码再补测试，禁止提交红灯状态。

**目标：** 在现有 task-domain v2、Roadmap v2 和 workspace runtime 上增加 RoadmapNode 任务思维导图；思维导图中的每个可见节点引用真实 Task；文章精确绑定 Task；旧 RoadmapNode 资源进入可审计的待分配区。

**源设计：** `docs/superpowers/specs/2026-07-23-roadmap-node-mindmap-task-articles-design.md`

**领域语言：** `CONTEXT.md`

**当前状态：** 设计已确认；实现尚未开始。现有 tenant migration 已到 `0005`，本能力从 `0006` 开始，不改写历史 migration。

**技术栈：** Go、Gin、`database/sql`、PostgreSQL、modernc SQLite、React、TanStack Query、Vitest、Testing Library、Playwright。

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
| `backend/internal/taskapp/mindmap_facade.go` | runtime 解析、ID、command id、DTO 编排 |
| `backend/internal/taskapp/mindmap_facade_test.go` | epoch、幂等和错误传播测试 |

现有 `TaskService.CreateTask` 不能从 MindMapService 内部调用，因为它会自行开启 fenced transaction。实现应复用 `task_factory.go` 的纯构造能力，并通过同一个 `MindMapCommandTx` 同时取得 `TaskDomainWriter` 与 `MindMapWriter`。

### 3.2 Schema 与 provider

| 路径 | 计划职责 |
| --- | --- |
| `backend/db/migrations/tenant/postgres/0006_roadmap_task_mindmaps.sql` | PostgreSQL 表、复合外键、CHECK、partial unique index |
| `backend/db/migrations/tenant/sqlite/0006_roadmap_task_mindmaps.sql` | SQLite 等价约束和索引 |
| `backend/internal/storage/contracttest/roadmap_mindmap_contract_tests.go` | 两个 provider 共用契约 |
| `backend/internal/storage/postgres/roadmap_mindmap.go` | PostgreSQL reader/writer |
| `backend/internal/storage/sqlite/roadmap_mindmap.go` | SQLite reader/writer |
| `backend/internal/storage/postgres/task_domain_runtime.go` | 将 MindMap tx capability 接入现有 fenced tx |
| `backend/internal/storage/sqlite/task_domain_runtime.go` | SQLite 同等接入 |

### 3.3 HTTP 与路由

| 路径 | 计划职责 |
| --- | --- |
| `backend/internal/handler/roadmap_mindmap_v2.go` | MindMap、placement、article、settings DTO |
| `backend/internal/handler/roadmap_mindmap_v2_test.go` | handler contract |
| `backend/internal/handler/task_domain_v2.go` | 扩展应用接口组合，不放业务规则 |
| `backend/internal/router/task_domain_v2_routes.go` | model-aware v2-only 路由 |
| `backend/internal/router/task_domain_v2_routes_test.go` | legacy/v2 fail-closed 路由测试 |

### 3.4 Web

| 路径 | 计划职责 |
| --- | --- |
| `frontend/src/api/roadmapMindMap.ts` | API 类型和 client |
| `frontend/src/api/roadmapMindMap.test.ts` | payload、错误和 revision contract |
| `frontend/src/hooks/useRoadmapMindMap.ts` | query/mutation/cache invalidation |
| `frontend/src/hooks/useRoadmapMindMap.test.tsx` | optimistic state 与冲突处理 |
| `frontend/src/routes/RoadmapMindMapRoute.tsx` | capability gate 与 lazy route |
| `frontend/src/routes/RoadmapMindMap.tsx` | 页面状态与 inspector 协调 |
| `frontend/src/routes/RoadmapMindMap.test.tsx` | 页面交互测试 |
| `frontend/src/components/roadmapMindMap/` | canvas、task node、toolbar、article panel、pending inbox |
| `frontend/src/styles/roadmap-mind-map.css` | 独立响应式样式 |
| `frontend/src/router.tsx` | `/projects/:projectID/roadmap/nodes/:roadmapNodeID/mind-map` |
| `frontend/src/routes/RoadmapV2.tsx` | 外层节点主体导航，菜单操作隔离 |
| `frontend/tests/e2e/roadmap-mind-map.spec.ts` | 浏览器主路径 |

### 3.5 Legacy adopt 与存储迁移

| 路径 | 计划职责 |
| --- | --- |
| `backend/internal/taskmigration/roadmap_resource_adopt.go` | legacy resource → ArticleResource + UnassignedArticle |
| `backend/internal/taskmigration/roadmap_resource_adopt_test.go` | 幂等 adopt、重复 URL、断点恢复 |
| `backend/internal/taskmigration/roadmap_freeze_manifest.go` | 将 `roadmap_resources` 纳入旧 writer 冻结边界 |
| `backend/internal/taskmigration/reconcile.go` | 新实体反向差集与 tombstone 验证 |
| `backend/internal/storage/tenant_snapshot.go` | 新表 snapshot manifest/摘要 |
| `backend/internal/storage/sqlite/tenant_snapshot.go` | SQLite 一致性快照覆盖 |
| 对应 PostgreSQL snapshot 实现 | 相同表集、行数和摘要语义 |

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
go test ./internal/taskdomain ./internal/taskapp ./internal/handler ./internal/router -count=1

Set-Location ../frontend
npm test -- --run
npm run build
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

GREEN 只增加 DTO、错误常量和 fake application，不注册生产路由。

### Task 0.3：禁止事务绕过

**测试优先文件：** `backend/internal/taskdomain/architecture_test.go`

RED：

- MindMap handler/service 不得依赖 `storage.Store`、`*sql.DB` 或 legacy repository；
- request runtime 只暴露 reader；
- writer 只在 fenced callback 内可见；
- 创建根/子 Task 过程中只能调用一次 `BeginFencedMindMapWrite`；
- 禁止从 callback 内调用会另开事务的 `TaskService.CreateTask`。

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

共享 contract 必须先在两个 provider 都失败，然后才增加 `0006`：

- 五张业务表与待分配收件箱存在；
- workspace/project/RoadmapNode/Task 复合外键不可绕过；
- partial unique index 保证至多一个根；
- TaskResourceSettings 一 Task 一行；
- normalized URL 在 workspace 内唯一；
- 同文章可关联多个 Task，同 Task 不可重复关联；
- pending/assigned 状态与 assigned_task_id CHECK；
- X/Y 成对 NULL；
- 枚举与长度 CHECK 一致。

表集：

1. `domain_roadmap_mindmaps_v2`
2. `domain_task_mindmap_nodes_v2`
3. `domain_article_resources_v2`
4. `domain_task_article_links_v2`
5. `domain_task_resource_settings_v2`
6. `domain_unassigned_roadmap_articles_v2`

### Task 2.2：PostgreSQL migration GREEN

只添加 `0006_roadmap_task_mindmaps.sql`，使 PostgreSQL schema cases 通过。不得修改 `0001`–`0005`。

特别验证：

- partial unique root index；
- composite FK 列顺序与被引用 unique key一致；
- 所有 revision `>= 1`；
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
- transaction callback 返回后 writer 失效。

每个 provider 必须运行相同 suite：

```powershell
Set-Location backend
go test ./internal/storage/sqlite -run RoadmapMindMap -count=1
$env:FLOWSPACE_REQUIRE_POSTGRES_TESTS='true'
go test -p 1 ./internal/storage/postgres -run RoadmapMindMap -count=1
```

**Checkpoint 2：** PostgreSQL/SQLite contract 全绿；普通 resolver 打开连接不运行 `0006`。

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
6. 相同 command/idempotency key 返回同一结果；
7. stale runtime epoch 不重试到 legacy。

GREEN 复用纯 `BuildTaskAggregateSnapshot`，通过一个 `MindMapCommandTx` 写 Task 和 MindMap。

### Task 3.2：创建子 Task 与放置已有 Task

RED：

- parent 必须在同一图；
- 已有 Task 必须属于同 project/RoadmapNode；
- 节点/深度上限；
- 创建 Task 成功但 placement 失败时全部回滚；
- 已有 Task placement 不修改 Task 内容；
- 并发创建导致 revision stale 时只有一个成功。

### Task 3.3：移动、布局与移除

按行为拆分：

- 单 placement PATCH 使用 placement revision；
- 批量布局 PUT 使用 mind-map revision；
- 移动分支进行环和深度复检；
- 第一版只移除叶子；
- 移除清空 Task.roadmap_node_id，但保留 Task、Occurrence、ExecutionLog 和文章关联；
- 归档 Task 只改变显示，不删除 placement。

### Task 3.4：手动文章与设置

RED：

- 只允许给当前图内 Task 添加；
- 同 URL 复用 ArticleResource；
- 同 idempotency key 不重复 link；
- 删除 link 不影响其他 Task；
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

### Task 3.6：Handler 与 model-aware 路由

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

全部注册为 v2-only。legacy workspace 必须 fail-closed，不转发到旧 Roadmap resource handler。

**Checkpoint 3：**

```powershell
Set-Location backend
go test ./internal/taskdomain ./internal/taskapp ./internal/handler ./internal/router -run 'MindMap|Article|Roadmap' -count=1
```

---

## Phase 4：Web 思维导图

### Task 4.1：API client 与 hooks

先写 `roadmapMindMap.test.ts` 和 hook 测试：

- 空图、完整图和待分配区解析；
- 各 revision 进入正确 payload；
- 409 不进行盲目 optimistic overwrite；
- 成功 mutation 只失效当前 RoadmapNode、相关 Task 和外层 Roadmap progress；
- 取消请求不会显示过期文章搜索结果。

### Task 4.2：外层 Roadmap 导航

**RED：** `RoadmapV2.test.tsx`

- 点击节点主体进入 mind-map route；
- 点击菜单、加号、拖动画布不导航；
- 返回时恢复 zoom、scroll 和 focused node；
- 深链接刷新仍能通过 projectID + roadmapNodeID 加载。

### Task 4.3：空图与根 Task

组件测试覆盖：

- 空图显示“创建根任务”和“选择已有任务”；
- 首个节点固定 center；
- 加载/空/错误/无权限状态可区分；
- 创建失败不留下本地幽灵节点；
- 成功后选中根节点并打开 inspector。

### Task 4.4：树布局与交互

- 使用树/思维导图布局，不复用外层流程图的有向流程视觉；
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
npm test -- --run roadmapMindMap RoadmapMindMap RoadmapV2
npm run lint
npm run build
```

---

## Phase 5：Legacy adopt 与存储迁移

### Task 5.1：legacy resource 冻结

先扩展 manifest 测试，再把 `roadmap_resources` 的 INSERT/UPDATE/DELETE 纳入 migration active 时的数据库写栅栏。必须在 cutover 前排空不认识新实体的旧实例。

### Task 5.2：资源 adopt

**RED：** `roadmap_resource_adopt_test.go`

- 每个 legacy resource 规范化为 ArticleResource；
- 同 URL 只创建一个资源；
- 每条旧资源创建一个 pending UnassignedArticle；
- 重复 adopt 幂等；
- 中途失败可从 ledger/version 恢复；
- 删除使用 tombstone，不通过回读已删除源行；
- 不创建 TaskArticleLink。

### Task 5.3：已有 Task 待整理

已有 `Task.roadmap_node_id` 但没有 placement 时由查询返回 `unplaced_tasks`。不自动猜 root/parent；用户 placement 后才离开待整理区。

### Task 5.4：endpoint snapshot/outbox/reconcile

将六张新表加入：

- 一致性 snapshot 表清单；
- 迁移复制顺序；
- outbox after-image/tombstone；
- replay 去重；
- 反向差集清理；
- 最终 row count + checksum + FK audit。

依赖顺序：

```text
RoadmapNode / Task
  → RoadmapMindMap
  → TaskPlacement
  → ArticleResource
  → TaskArticleLink / TaskResourceSettings / UnassignedArticle
```

### Task 5.5：故障恢复

每个持久边界至少一个故障注入：

- snapshot 后崩溃；
- article upsert 后、link 前回滚；
- outbox claim 后崩溃；
- replay 完成但 watermark 未提交；
- cutover CAS 失败；
- 反向迁移 job。

**Checkpoint 5：** legacy 数据可完整保留、明确分配、重复恢复；endpoint 切换不漏新实体。

---

## Phase 6：E2E、性能与发布

### Task 6.1：SQLite v2 E2E

独立 workspace 执行：

```text
登录
→ 创建 learning Project 与 RoadmapNode
→ 点击外层节点进入空图
→ 创建根 Task
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
- 迁移 checksum。

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
npm test -- --run
npm run lint
npm run build
npx playwright test tests/e2e/roadmap-mind-map.spec.ts
```

灰度顺序：

1. 先部署只读 schema/provider；
2. 开启内部 workspace 的空图 GET；
3. 开启根/子 Task 写入；
4. 开启文章与待分配迁移；
5. 验证 endpoint migration；
6. 扩大 workspace 范围。

首次新实体写入后不承诺回拨到不认识这些表的应用版本。应用回滚版本必须继续读写新 schema 或保持对应功能 fail-closed。

---

## 5. 完成定义

只有同时满足以下条件才算完成：

- 设计中的全部领域不变量都有至少一个先失败后通过的测试；
- PostgreSQL 与 SQLite 共用 contract 全绿；
- 根/子 Task 创建经故障注入证明无半成品；
- 文章只绑定明确 Task，legacy 文章无静默归属；
- 搜索 prompt 修改能在 outbound request 中被观测；
- 外层流程图与内层思维导图语义、路由和视觉交互分离；
- 现有 Today、Calendar、Task detail 和 Roadmap progress 继续以 Task/Occurrence 为事实源；
- endpoint snapshot/outbox/reconcile 覆盖新实体及 tombstone；
- mobile-v2 contract 未被破坏；
- 全量后端、前端、lint、build 与目标 E2E 全绿；
- 发布与回滚边界记录到 runbook。

在 Phase 0 contract 和架构守卫通过之前，不新增 `0006` migration；在 Phase 3 原子性测试通过之前，不注册生产写路由；在 Phase 5 迁移验证通过之前，不对已有 workspace 开启文章 adopt。
