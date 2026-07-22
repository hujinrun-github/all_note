# 项目、任务与日程统一领域模型实施计划

> **实施约束：** 本计划只定义后续执行步骤，不代表已经授权修改业务代码。正式实施时必须严格按任务顺序执行；每个独立行为都先编写并运行能够因目标能力缺失而失败的测试（RED），再完成最小实现（GREEN），最后仅在测试全绿时重构（REFACTOR）。禁止先写实现再补测试，禁止提交红灯状态。

**目标：** 将 FlowSpace 的 Project、Task、TaskSchedule、TaskScheduleVersion、TaskOccurrence、Calendar projection 与 Learning Roadmap 落成一套可验证、可迁移、可按 workspace 原子切换的领域实现；旧 Web API 在过渡期通过 v2 adapter 兼容，mobile-v1 任务域在 cutover 前关闭，移动端重设计另立方案。

**源设计：** `docs/superpowers/specs/2026-07-21-project-task-schedule-domain-redesign.md`

**总体架构：** Project 表达“为什么做”，Task 表达稳定的“做什么”，不可变 ScheduleVersion 表达“什么时候及是否重复”，Occurrence 表达一次实际执行；Calendar、Today、Task list 都是 Occurrence 的查询投影。所有写入都经过 request-scoped tenant runtime 和 fenced transaction，迁移使用 v2 shadow tables、legacy transactional outbox、workspace domain state、drain fence 与 CAS cutover。

**技术栈：** Go、Gin、`database/sql`、pgx、modernc SQLite、React、TanStack Query、Vitest、Testing Library、Playwright。

## 执行记录（2026-07-22）

- Phase 0–7 的 schema、领域服务、fenced runtime、v2/legacy 路由、迁移恢复、后台生成器和 Web UI 已按 RED → GREEN → REFACTOR 实现。
- Phase 8 已完成真实 HTTP/router + 认证 + 独立已迁移 SQLite tenant 的主路径 smoke、真实 PostgreSQL v2 provider contract、全量 Go/Vitest、vet、lint、build 和 rollout runbook。
- 现有 Playwright 启动器仍是单 legacy 数据库，不能冒充 control/data 分离后的 v2 全栈环境；本轮没有运行真实 workspace canary 或 cutover。
- `FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING` 默认关闭。真实灰度、`v2_first_write_at` 边界和后续 legacy 归档必须按 `docs/task-domain-v2-rollout-runbook.md` 另行授权执行。
- mobile-v2、Roadmap AI/资源迁移/布局优化/Edge 写 API 继续 fail-closed，属于独立后续设计，不计入本轮已实现范围。

---

## 1. TDD 执行规则

### 1.1 每个行为的 Red → Green → Refactor

1. **RED：** 只添加一个聚焦测试或测试表中的一个新 case。
2. **验证 RED：** 运行最窄命令，确认失败原因是目标行为尚未实现；编译错误、测试夹具错误、数据库不可达不算有效 RED。
3. **GREEN：** 只实现使当前测试通过的最小生产行为，不顺带完成后续任务。
4. **验证 GREEN：** 先重跑聚焦测试，再运行该任务列出的 package 或 contract suite。
5. **REFACTOR：** 只在绿色状态下提取类型、整理命名和删除重复；随后重跑同一组测试。
6. **提交：** 每次提交只包含一个可说明的绿色行为。不得提交 RED 状态，不得把 schema、领域状态机、API 和 UI 混成一个不可回退的大提交。

如果新增测试第一次执行就是绿色，必须检查它是否只验证了旧行为；应增强断言或调整 fixture，直到它能可靠证明目标能力当前缺失。

### 1.2 测试层级

| 层级 | 验证内容 | 隔离要求 |
| --- | --- | --- |
| 纯领域单元测试 | 状态机、不变量、规则规范化、DST、预期 occurrence key | 无数据库、无网络、fake clock |
| 应用服务测试 | 聚合命令、revision、事务边界、错误映射 | fake repository/fenced writer |
| Repository contract | PostgreSQL/SQLite 的 schema、FK、CHECK、revision、查询语义一致 | 两个 provider 运行同一套测试 |
| 迁移集成测试 | snapshot、outbox、tombstone、drain、reconcile、cutover | 每测试独立 tenant database/schema |
| API 测试 | v2 endpoint、旧 Web adapter、409/400/426 | `httptest` + 隔离 store |
| 前端组件测试 | 页面投影、交互、状态、冲突提示 | mock API，不访问真实后端 |
| E2E | 创建、执行、日历拖动、迁移后兼容 | Playwright 隔离 workspace |
| 故障注入 | backfill/replay/drain/cutover 边界的崩溃恢复 | 每个持久状态边界至少一次 |

### 1.3 阶段门禁

- 当前用户运行时存储计划中的 request-scoped tenant resolver、类型化 repository 和 `BeginFencedWrite(workspaceID, epoch)` 是本计划的前置条件。它们未完成时可以实现纯领域包和 shadow schema，但不得接入生产写路径或执行 cutover。
- 新领域代码不得持有全局 `storage.Store`、底层 `*sql.DB` 或普通 `Store.Transact`；只读 repository 来自当前 runtime，写 repository 只存在于 `TenantWriteTx`。
- PostgreSQL 与 SQLite 必须运行同一组 repository contract；provider-specific trigger 只允许实现差异，不允许产生领域语义差异。
- mobile-v1 contract 和页面不在本计划中修改。v2 cutover 前必须关闭受影响的 mobile-v1 mutation、snapshot、changes、watch，并使旧 cursor/session scope 失效。
- v2 首次业务写入后不承诺数据层回拨。应用回滚必须继续读写 v2 schema。
- 每个 Phase 的 checkpoint 全绿后才进入下一 Phase。任何跨阶段缺陷先补回失败测试，不通过临时双写或异步修复绕过。

### 1.4 数据安全

- PostgreSQL 测试只读取专用测试环境变量，并拒绝数据库名不含 `test` 的连接；不得使用开发或生产库。
- SQLite 测试只使用 `t.TempDir()`。
- 迁移 fixture 必须由测试创建，不复制真实用户任务正文。
- 所有测试时间由 fake clock 或显式时区提供，不读取服务器本地时区。
- 当前工作区存在其他功能变更时，实施提交只暂存本任务文件，禁止覆盖或回滚不属于本计划的改动。

---

## 2. 目标文件地图

以下路径是目标结构。执行时若仓库结构已经变化，先修订本节，再开始对应任务。

### 2.1 领域与应用层

| 目标路径 | 职责 |
| --- | --- |
| `backend/internal/taskdomain/types.go` | Project、Task、Schedule、Occurrence、ExecutionLog 领域类型 |
| `backend/internal/taskdomain/errors.go` | 稳定领域错误码 |
| `backend/internal/taskdomain/schedule.go` | recurrence/timing 规范化、时区和 DST 解析 |
| `backend/internal/taskdomain/task_state.go` | Task lifecycle 状态机 |
| `backend/internal/taskdomain/occurrence_state.go` | Occurrence execution 状态机 |
| `backend/internal/taskdomain/aggregate.go` | 跨 Task/Schedule/Occurrence 的原子不变量 |
| `backend/internal/taskdomain/generator.go` | 预期 key 计算与滚动物化窗口 |
| `backend/internal/taskdomain/dependency.go` | 依赖合法性与环检测 |
| `backend/internal/taskdomain/repository.go` | 类型化只读/写入 repository contracts |
| `backend/internal/taskdomain/service.go` | 创建、状态命令、改期和 schedule 版本命令 |
| `backend/internal/taskdomain/calendar.go` | CalendarEntry/Today/Task list 查询模型 |

### 2.2 Provider、迁移与兼容

| 目标路径 | 职责 |
| --- | --- |
| `backend/db/migrations/tenant/postgres/0002_task_domain_v2.sql` | PostgreSQL shadow tables、约束、索引、trigger |
| `backend/db/migrations/tenant/sqlite/0002_task_domain_v2.sql` | SQLite 等价 schema、约束和 trigger |
| `backend/internal/storage/contracttest/task_domain_v2_contract_tests.go` | 两个 provider 共用 repository contract |
| `backend/internal/storage/postgres/task_domain_v2.go` | PostgreSQL v2 repository |
| `backend/internal/storage/sqlite/task_domain_v2.go` | SQLite v2 repository |
| `backend/internal/taskmigration/` | preflight、snapshot、outbox replay、reconcile、drain、cutover、recovery |
| `backend/internal/legacytaskadapter/` | 旧 `/api/tasks`、`/api/events` 到 v2 的转换 |
| `backend/internal/handler/task_domain_v2.go` | v2 Project/Task/Occurrence/Calendar handlers |
| `backend/internal/router/router.go` | v2 route 与旧 Web adapter 注册 |

### 2.3 前端

| 目标路径 | 职责 |
| --- | --- |
| `frontend/src/api/taskDomain.ts` | Project、Task、Occurrence、Calendar v2 client |
| `frontend/src/hooks/useTaskDomain.ts` | query/mutation、revision conflict、cache invalidation |
| `frontend/src/routes/Projects.tsx` | 项目列表与组合筛选 |
| `frontend/src/routes/ProjectDetail.tsx` | 项目任务、日历、笔记与 Roadmap 汇总 |
| `frontend/src/routes/Tasks.tsx` | occurrence 驱动的任务页面 |
| `frontend/src/routes/Dashboard.tsx` | 今日投影与默认不选中的逾期 Tab |
| `frontend/src/routes/Calendar.tsx` | CalendarEntry 投影与 occurrence 改期 |
| `frontend/src/components/tasks/` | 创建、详情、状态、schedule 与 revision 冲突组件 |
| `frontend/tests/e2e/task-domain-v2.spec.ts` | 新模型主路径 E2E |

---

## 3. 执行总览

```text
Phase 0  基线、契约冻结与架构守卫
  ↓
Phase 1  纯领域类型、状态机与时间规则
  ↓
Phase 2  PostgreSQL/SQLite shadow schema 与 repository contract
  ↓
Phase 3  聚合命令、revision 与 v2 API
  ↓
Phase 4  重复生成器、ScheduleVersion 与自然完成
  ↓
Phase 5  Calendar/Today 查询模型与旧 Web adapter
  ↓
Phase 6  Legacy outbox、backfill、drain、reconcile 与 CAS cutover
  ↓
Phase 7  Web UI 切换
  ↓
Phase 8  故障注入、灰度、归档与发布验收
```

---

## Phase 0：基线、契约冻结与架构守卫

### Task 0：记录可重复基线和前置条件

**文件：** 只更新测试/实施记录，不修改生产行为。

- [ ] **RED/基线：** 运行后端和前端全量测试，记录现有失败、skip 和耗时最长用例。
- [ ] 检查当前 tenant runtime 是否已经做到只读 repository request-scoped、写 repository 仅从 `BeginFencedWrite` 暴露。
- [ ] 检查生产 handler、service、worker 是否仍持有完整 `storage.Store`；若仍存在，将本计划标为“仅可执行 Phase 1–2，不允许接入/cutover”。
- [ ] 记录 mobile-v1 读取 `tasks/events/task_occurrences` 的 snapshot/watch/changes 路径，形成关闭清单。

```powershell
Set-Location backend
go test ./... -count=1
go vet ./...

Set-Location ../frontend
npm test -- --run
npm run lint
npm run build
```

**Checkpoint：** 基线结果可重复；前置条件和 mobile-v1 关闭面有书面清单。

### Task 1：先冻结领域错误和 Web API contract

**测试优先文件：**

- `backend/internal/taskdomain/errors_test.go`
- `backend/internal/handler/task_domain_v2_test.go`
- `frontend/src/api/taskDomain.test.ts`

**TDD 步骤：**

1. **RED：** 为 `revision_conflict`、`invalid_transition`、`nonexistent_local_time`、`ambiguous_local_time`、`legacy_task_domain_fenced`、`mobile_task_domain_upgrade_required` 写错误映射测试。
2. **RED：** 写请求/响应 contract 测试，固定 `expected_revision`、聚合命令返回的新 revisions 和 CalendarEntry 字段。
3. **GREEN：** 只定义 DTO、错误常量和映射，不接 repository。
4. **REFACTOR：** 将旧 Web DTO 与 v2 DTO 分开，禁止共享含糊的 `note_id` 或 status 字段。

```powershell
Set-Location backend
go test ./internal/taskdomain ./internal/handler -run 'Contract|ErrorMapping' -count=1
```

### Task 2：建立禁止绕过 fenced write 的架构测试

**测试优先文件：** `backend/internal/taskdomain/architecture_test.go`

1. **RED：** 扫描新领域 handler/service/worker，禁止依赖 `storage.Store`、`*sql.DB`、legacy Task/Event repository 或 `Store.Transact`。
2. **RED：** 验证 v2 写 repository 只能由 `TenantWriteTx` 取得，runtime 只暴露读接口。
3. **GREEN：** 建立最小接口骨架和依赖方向；此任务不实现业务逻辑。
4. 将架构测试加入 required backend suite。

**Checkpoint 0：** contract 与依赖边界已被测试锁定；尚未改变线上路由和旧表。

---

## Phase 1：纯领域类型、状态机与时间规则

### Task 3：Project 与系统项目不变量

**测试优先文件：** `backend/internal/taskdomain/project_test.go`

按一个 case 一个 RED 循环实现：

- `kind=standard/learning` 与 `horizon=short/long` 可正交组合；
- 每个 workspace 需要 inbox 和 personal，但两个 workspace 可复用相同 ID；
- system project 不可删除、不可更改 `system_role`；
- 有非终态 occurrence 的项目不能直接完成；
- learning 项目最多一个 current Roadmap。

**GREEN 文件：** `types.go`、`project.go`。只实现纯函数和领域错误，不访问数据库。

### Task 4：Task lifecycle 状态机

**测试优先文件：** `backend/internal/taskdomain/task_state_test.go`

1. **RED：** table-driven 测试覆盖设计中的每一条合法边。
2. **RED：** 覆盖 draft 直接 completed、active 直接 archived、cancelled occurrence 直接 reopen 等非法路径。
3. **RED：** PATCH 普通属性不得改变 lifecycle 的领域命令测试。
4. **GREEN：** 实现 `Publish/Pause/Resume/Cancel/Restore/Archive` 纯状态转换。
5. **REFACTOR：** 对外只返回稳定错误码，不泄漏内部状态枚举实现。

### Task 5：Occurrence execution 状态机与日志事实

**测试优先文件：**

- `backend/internal/taskdomain/occurrence_state_test.go`
- `backend/internal/taskdomain/execution_log_test.go`

逐项 RED：

- open → active/done/skipped/cancelled；
- active → blocked/done/cancelled；blocked → active/cancelled；
- 终态显式 reopen；
- blocked 必须同时包含非空 reason 和 next action；
- done 与 `completed_at` 双向一致；
- 每次状态变化产生不可变 ExecutionLog after-image；
- 单次 occurrence 拒绝 skipped。

### Task 6：Schedule 规则规范化与时间语义

**测试优先文件：**

- `backend/internal/taskdomain/schedule_test.go`
- `backend/internal/taskdomain/timezone_test.go`

逐项 RED：

- none/daily/weekly/monthly 的合法字段组合；
- interval 为正数、weekdays/month_days 非空去重且范围正确；
- 未知 JSON 字段、`custom`、`skip_holidays` 被拒绝；
- unscheduled/date/time_block 字段组合；
- IANA timezone 校验；
- DST 不存在时间返回 `nonexistent_local_time`；
- DST 重复时间返回 offset 候选而非静默选择；
- 全天 `[start, exclusive_end)` 与 time-block UTC 瞬间转换。

**GREEN：** 使用标准时区数据库实现纯解析器，所有测试使用固定 clock/timezone。

### Task 7：Task 聚合原子规则

**测试优先文件：** `backend/internal/taskdomain/aggregate_test.go`

1. **RED：** 单次完成同时修改 occurrence 与 Task；reopen 同时恢复两者。
2. **RED：** cancel Task 停止生成并取消全部非终态实例，保留终态历史。
3. **RED：** pause 不取消已生成实例；“暂停并取消未来实例”是不同命令。
4. **RED：** cancelled Task 下 occurrence 不能独立 reopen。
5. **RED：** 每个命令同时检查 Task 与目标 Occurrence revision，并分别递增。
6. **GREEN：** 实现内存聚合命令，尚不写数据库。

### Task 8：Dependency 与 Roadmap 同项目规则

**测试优先文件：** `backend/internal/taskdomain/dependency_test.go`

- 拒绝 self edge、重复边、跨 workspace；
- `finish_to_start` 拒绝任一端为重复 Task；
- 拒绝有向环；
- Task 关联的 RoadmapNode 必须与 Task 属于同一 learning Project；
- related/suggested_order 不参与阻断判断。

**Checkpoint 1：** 所有核心不变量在无数据库条件下完成；状态机测试即领域规范的可执行版本。

---

## Phase 2：Shadow schema 与 Repository contract

### Task 9：用失败 migration contract 驱动 v2 schema

**测试优先文件：** `backend/internal/storage/contracttest/task_domain_v2_schema_contract_tests.go`

按以下顺序一次只增加一组 RED：

1. shadow tables 和 `workspace_task_domain_state` 存在；
2. 所有 tenant 身份使用 `(workspace_id, id)` 复合 PK/FK；
3. Project system role partial unique/SQLite 等价 trigger；
4. lifecycle/execution/generation 状态 CHECK；
5. done ↔ completed_at、blocked metadata、时间字段组合；
6. ScheduleVersion 有效区间不重叠且恰好一个开放版本；
7. current revision deferred FK；
8. ExecutionLog UPDATE/DELETE 被数据库拒绝；
9. RoadmapNode 同项目复合 FK。

Shadow schema 同时包含 v2 LearningRoadmap、RoadmapNode 和 RoadmapEdge；不能让新 Task 的复合外键依赖 fresh tenant baseline 中不存在的 legacy `roadmap_nodes`。adopt 旧库时再从 legacy Roadmap 表回填这些 v2 表。

还要分别覆盖两类安装路径：

- **fresh tenant：** `0001 + 0002` 后直接以 v2 初始化，不要求先创建完整 legacy event/recurrence/roadmap schema；
- **adopted legacy tenant：** preflight 先确认真实存在的 project/task/rule/occurrence/event/roadmap 源表及版本，再把 `model_version` 初始化为 legacy 并安装相应 outbox trigger。

不得为了让 `0002` 通过而在 fresh tenant 中伪造一套不会被业务使用的 legacy 表。

**GREEN 文件：** 两个 provider 的 `0002_task_domain_v2.sql`。每增加一组 SQL 立即同时运行 PostgreSQL 和 SQLite contract。

```powershell
Set-Location backend
go test ./internal/storage/sqlite -run TaskDomainV2Schema -count=1
$env:FLOWSPACE_REQUIRE_POSTGRES_TESTS='true'
go test -p 1 ./internal/storage/postgres -run TaskDomainV2Schema -count=1
```

### Task 10：Project repository 与系统项目 provision

**测试优先文件：** `task_domain_v2_contract_tests.go`

- 两个 workspace 都可拥有 `personal/system-inbox` 固定 ID；
- 同一 workspace system role 唯一；
- Project CRUD 使用 expected revision；
- revision 冲突只有一个更新成功；
- 系统项目删除和 role 修改被 repository/DDL 双重拒绝；
- workspace provision 幂等创建 inbox/personal。

**GREEN：** 先 SQLite，随后 PostgreSQL；两个实现都通过共享 contract 后才重构 SQL helper。

### Task 11：Task + Schedule + 首批 Occurrence 原子创建

**RED contract：**

- 缺 project 拒绝创建；
- 单次任务同事务创建 Task、Schedule、ScheduleVersion、唯一 `once` occurrence；
- unscheduled 单次 occurrence 日期和时间为空；
- 重复任务创建时首批 key 无重复；
- 任一步失败时四类记录全部回滚；
- Schedule header、Task、Occurrence revision 独立；
- `generated_schedule_revision` 不随 occurrence 普通更新变化。

**GREEN：** 在类型化 `TenantWriteTx` 中暴露最小 `TaskDomainWriter`，禁止把完整 Store 传入 callback。

### Task 12：ScheduleVersion、ExecutionLog 与状态更新 repository

**RED contract：**

- “本次及以后”关闭旧 effective range、插入新版本、更新 current pointer，历史引用保留；
- 两个开放版本、空开放版本、区间重叠在提交时失败；
- blocked 同事务更新 current snapshot 并插入 ExecutionLog；
- ExecutionLog 不可更新和删除；
- stale Task/Schedule/Occurrence revision 返回统一 conflict。

### Task 13：查询 contract 与必要索引

**RED contract：**

- occurrence 按 today/upcoming/overdue/unscheduled/completed 查询；
- Calendar range 同时返回 date 和 time_block，不返回 unscheduled；
- project、status、recurring 条件可组合；
- 时间范围边界使用 `[from,to)`；
- 查询绝不泄漏其他 workspace；
- explain/基准测试确认关键条件使用设计中的复合索引。

**Checkpoint 2：** 两个 provider 的 schema 与 repository 语义一致，未接生产路由，legacy 仍为唯一写源。

---

## Phase 3：聚合服务、revision 与 v2 API

### Task 14：Task 创建与显式 lifecycle commands

**测试优先文件：** `backend/internal/taskdomain/service_test.go`

1. **RED：** CreateTask 在一次 fenced transaction 中持久化完整聚合。
2. **RED：** publish/pause/resume/cancel/restore/archive 调用纯领域状态机并写审计。
3. **RED：** cancel 的批量 occurrence 副作用与 Task 状态原子提交。
4. **RED：** stale runtime epoch、Task revision、Schedule revision 各自返回可区分错误。
5. **GREEN：** 实现最小 service；不允许 handler 自己拼 repository 调用。

### Task 15：Occurrence commands

**RED：** 为 start/block/unblock/complete/skip/cancel/reopen 逐命令验证：

- 合法转换、非法转换；
- expected Task + Occurrence revisions；
- 单次 Task 聚合副作用；
- 重复实例只改变本次；
- ExecutionLog 与 current blocked metadata 同事务；
- 失败不产生半更新或日志孤儿。

### Task 16：改期和 ScheduleVersion commands

**RED：**

- “仅本次”只更新 occurrence 并标记 `manually_overridden`；
- 已完成实例必须先 reopen；
- “本次及以后”创建新 ScheduleVersion；
- 已开始、终态或人工 override 的历史/未来实例不被重写；
- DST 歧义错误通过 API 保留候选 offset；
- 修改 schedule 与并发 generator 只有一个 revision 胜出。

### Task 17：v2 handlers 与路由

**测试优先文件：**

- `backend/internal/handler/task_domain_v2_test.go`
- `backend/internal/router/task_domain_v2_routes_test.go`

先写失败 API 测试，再逐组开放：

1. Projects CRUD/archive/complete；
2. Tasks CRUD 与 publish/pause/resume/cancel/restore/archive；
3. Occurrence 查询与状态命令；
4. Calendar entries；
5. PATCH lifecycle/schedule/execution 字段返回 400；
6. revision 冲突返回 409；
7. tenant runtime fenced/epoch 错误映射为 409/503，并且绝不静默回退 legacy。

**Checkpoint 3：** v2 API 在隔离测试路由可用；生产 model_version 仍为 legacy。

---

## Phase 4：重复生成器与自然完成

### Task 18：确定性的 occurrence key 计算器

**测试优先文件：** `backend/internal/taskdomain/generator_test.go`

使用 table-driven RED 覆盖：

- daily interval；
- weekly 多 weekday；
- monthly 1..31 与不存在日期的明确策略；
- starts_on/ends_on/effective range 边界；
- timezone 与 DST；
- 相同输入产生相同有序 key；
- ScheduleVersion 切分前后不重复、不漏 key。

**GREEN：** 计算器是纯函数，不读数据库，不用当前系统时间。

### Task 19：滚动窗口 generator 与幂等 claim

**测试优先文件：**

- `backend/internal/taskdomain/generator_service_test.go`
- provider contract 中的 generation cases

逐项 RED：

- 创建时至少覆盖 today..today+90d；
- 重跑不产生重复 occurrence；
- workspace 分批 claim，一个 workspace 失败不阻塞其他 workspace；
- worker 每次 claim 后重新取得 runtime snapshot 与当前 epoch；
- 新 endpoint/epoch 下继续写，`created_epoch` 只审计；
- 插入全部预期 key 后才能推进 watermark；
- 中途失败设置 retry_pending/failed，不提前推进水位。

### Task 20：重复 Task 自然完成判定

**RED：** ends_on 已过但以下任一条件成立时 Task 仍为 active：

- watermark 未覆盖 ends_on；
- 预期 key 有缺失；
- generation_status 非 idle；
- 有 retry_pending/failed job；
- 任一 occurrence 非终态。

然后写唯一 GREEN case：所有条件满足时在 fenced transaction 中完成 Task。再增加 reopen 历史实例使 Task 恢复 active、再次终态后重新完成的测试。

**Checkpoint 4：** 重复规则、生成水位和自然完成具有可执行证明，不依赖后台“最终修复”。

---

## Phase 5：查询投影与旧 Web adapter

### Task 21：CalendarEntry、Today 与 Task list 读模型

**测试优先文件：**

- `backend/internal/taskdomain/calendar_test.go`
- `backend/internal/taskdomain/query_service_test.go`

**RED：**

- time_block 进入时间网格；date 进入全天区；unscheduled 不进入日历；
- planned time 与 due 可以同时存在；
- overdue 只包含 due 已过且未终态实例；
- Today 默认不混入 overdue Tab；
- 列表显示 open/active/blocked/done 等所有执行状态；
- CalendarEntry 包含 project/task/occurrence/revision 和完整 calendar metadata。

### Task 22：旧 Event adapter 无损往返

**测试优先文件：** `backend/internal/legacytaskadapter/events_test.go`

逐项 RED：

- GET 同时投影 date 与 time_block；
- POST 创建单次 Task 聚合，不写 legacy events；
- PATCH 只更新 occurrence，缺失扩展字段时保留旧值；
- DELETE 原子取消单次 Task/Occurrence，保留 ID map；
- location/kind/notes/note_id round-trip；
- 全天和多日 `[start,end)` round-trip；
- 使用 ScheduleVersion 固化 timezone，不读取当前用户时区。

### Task 23：旧 Task adapter 与唯一写源守卫

**RED：**

- legacy model_version 时旧接口仍走旧 repository；
- v2 model_version 时旧接口只调用 v2 service；
- 同一请求绝不写两套表；
- model version/revision/epoch 在每次写请求中持久校验；
- 进程内缓存过期时不能路由到错误写源。

**Checkpoint 5：** 新旧 Web API 均可由 v2 唯一写源提供，全天 Event 不丢失；尚未切换任何真实 workspace。

---

## Phase 6：Legacy 数据迁移与原子 cutover

### Task 24：Domain state、logical version 与全源表 outbox trigger

**测试优先文件：** `backend/internal/taskmigration/outbox_contract_test.go`

PostgreSQL/SQLite 同组 RED 覆盖所有源实体：

- project/task/rule/occurrence/event 的 insert/update/delete；
- cascade delete 的子实体 tombstone；
- insert/update 保存 after-image；delete 保存 before-image；
- 同一实体 logical_version 单调递增；
- outbox 与源 DML 同事务提交/回滚；
- outbox 不写 v2 表；
- `accept_legacy_writes=false` 时旧 DML 被数据库 trigger 拒绝。

**GREEN：** 只增加 domain state、version ledger、legacy outbox 和 trigger，不做 backfill。

Trigger 安装必须由 adopt manifest 中的源 schema inventory 驱动：legacy 表不存在的 fresh tenant 不执行对应 trigger DDL，并直接使用 v2；legacy tenant 缺少声明为必需的源表时 preflight 失败，不能静默少迁某类实体。

### Task 25：Preflight 与确定性映射

**测试优先文件：** `backend/internal/taskmigration/preflight_test.go`

**RED：** fixture 覆盖：

- 多个 personal 项目；
- 已占用“收件箱”名称；
- 孤立任务；
- priority 越界；
- planned_date 与 due 同时存在；
- Roadmap 关联；
- 全天、多日和无时区 Event；
- 不合法当地日界。

验证 personal/inbox 冲突规则、migration timezone 来源优先级、阻断错误和审计原因完全确定。

### Task 26：一致性 snapshot backfill

**测试优先文件：** `backend/internal/taskmigration/backfill_test.go`

逐项 RED：

- PostgreSQL REPEATABLE READ/SQLite 一致性读记录 snapshot sequence；
- project/task/rule/occurrence/event 全部进入 v2；
- ID map 唯一且可重跑；
- Event 原子创建 Task/Schedule/Occurrence；
- 完成、阻塞、跳过、note、calendar metadata、全天时间不丢失；
- 单 workspace 失败回滚该 workspace，不污染其他 workspace。

### Task 27：Outbox replay 与 tombstone 防复活

**测试优先文件：** `backend/internal/taskmigration/replay_test.go`

**RED：**

- 按 sequence 回放并持久推进 watermark；
- 较旧 logical_version 不覆盖较新 v2 投影；
- 删除后源行不存在仍可仅靠 tombstone 删除投影；
- snapshot 延迟 upsert 不得复活已删除行；
- 中断后从 watermark 恢复，结果幂等；
- 依赖实体按确定顺序投影/删除。

### Task 28：Drain、双向差集 reconcile 与旧实例栅栏

**测试优先文件：**

- `backend/internal/taskmigration/drain_test.go`
- `backend/internal/taskmigration/reconcile_test.go`

逐项 RED：

- drain 提升 write_epoch、关闭 legacy writes 并等待已进入事务排空；
- 不认识 epoch 的旧服务写入也被数据库 trigger 拒绝；
- source-v2 差集补齐缺行；
- v2-source 差集删除无来源 mapped 行；
- generated system projects 不被反向删除；
- 行数、关键 checksum、FK、状态不变量全部通过才进入 ready；
- 老 writer protocol heartbeat 未退出时拒绝 cutover。

### Task 29：CAS cutover、mobile-v1 关闭与回退边界

**测试优先文件：**

- `backend/internal/taskmigration/cutover_test.go`
- `backend/internal/mobilecontract/task_domain_shutdown_test.go`

**RED：**

- mobile task/event mutation、snapshot、changes、watch 未关闭时拒绝 cutover；
- 关闭后旧接口返回 426，旧 cursor/token 失效；
- model_version + revision + migration_id CAS 只有一个 coordinator 成功；
- cutover 前所有 sequence 已回放；
- 首次 v2 写原子设置 `v2_first_write_at`；
- 首次写之前允许维护事务切回 legacy；首次写之后拒绝数据层回拨；
- 应用回滚版本必须声明支持 v2 schema。

### Task 30：迁移恢复器与可观测性

**RED/故障注入：** 在 after snapshot、mid replay、after drain、before CAS、after CAS/before response 各注入一次崩溃，重启后验证：

- 状态机从持久 state 恢复；
- 不重复生成或复活数据；
- model_version 不出现半切换；
- 失败保留 last_error、migration_id 和可操作的恢复动作；
- 指标包含 lag、failure、conflict、adapter request、generation lag。

**Checkpoint 6：** 固定迁移 fixture 在 PostgreSQL/SQLite 全部通过；只有此时才允许对隔离 workspace 做真实 cutover 演练。

---

## Phase 7：Web UI 切换

### Task 31：前端 API、query key 与 revision conflict

**测试优先文件：**

- `frontend/src/api/taskDomain.test.ts`
- `frontend/src/hooks/useTaskDomain.test.tsx`

**RED：**

- 所有 mutation 发送 expected revision；
- 409 保留本地编辑并提示刷新/比较，不静默覆盖；
- mutation 成功只失效相关 project/task/occurrence/calendar query；
- DTO 明确区分 Task note 与 Occurrence note；
- lifecycle 只能通过 command API 修改。

### Task 32：项目页面与创建任务

**测试优先文件：** `frontend/src/routes/Projects.test.tsx`、`ProjectDetail.test.tsx`

逐项 RED：

- kind + horizon 可组合筛选；
- 系统 inbox/personal 有明确标识且无删除入口；
- 创建 Task 必须选择项目，快速捕获明确显示进入 inbox；
- learning Project 才展示 Roadmap；
- 项目完成遇到非终态实例时要求取消或迁移选择。

### Task 33：Tasks 与 Today 改为 occurrence 投影

**测试优先文件：**

- `frontend/src/routes/Tasks.test.tsx`
- `frontend/src/routes/Dashboard.test.tsx`

**RED：**

- 今天/接下来/逾期/无日期/重复/已完成来自 occurrence query；
- 已逾期 Tab 默认不选中；
- open/active/blocked/done 四个主要状态在列表中可辨认；
- 完成重复实例后下一实例仍未完成；
- blocked 展示原因和下一步；
- Task 详情与 occurrence 详情不混淆 definition 状态和本次状态。

### Task 34：Calendar 与 Schedule 编辑

**测试优先文件：** `frontend/src/routes/Calendar.test.tsx`、`frontend/src/components/tasks/ScheduleEditor.test.tsx`

**RED：**

- date 进入全天区，time_block 进入网格，unscheduled 不显示；
- 拖动单次实例只更新 occurrence；
- 拖动重复实例默认“仅本次”；
- “本次及以后”展示明确确认并创建 schedule revision；
- 已完成实例拖动入口禁用并提示先 reopen；
- DST 歧义要求用户选择 offset；
- 全天多日 exclusive end 正确显示。

### Task 35：Roadmap 只表达结构和聚合进度

**测试优先文件：** `frontend/tests/e2e/tasks-roadmap.spec.ts` 及对应组件测试。

- Node 无任务完成复选框；
- Node 可创建多个同项目 Task；
- 跨项目 Task 关联失败并显示稳定错误；
- Node 进度由关联 occurrence 汇总；
- 删除节点前要求解绑或迁移 Task。

**Checkpoint 7：** Web 页面全部读取 v2 DTO；旧 Web adapter 仍通过测试，可供短期兼容但 UI 不依赖 legacy 字段。

---

## Phase 8：E2E、灰度与旧表归档

### Task 36：主路径 E2E

**测试优先文件：** `frontend/tests/e2e/task-domain-v2.spec.ts`

至少覆盖：

1. 创建 standard-short、standard-long、learning-short 项目；
2. 快速捕获进入 inbox；
3. 创建无日期、日期、time-block、daily/weekly/monthly Task；
4. start → block → unblock → complete；
5. 完成和 reopen 单次 Task 的聚合状态；
6. 完成重复实例不完成定义；
7. 日历仅本次拖动和本次及以后；
8. 两个会话并发编辑返回 revision conflict；
9. Roadmap 同项目关联和聚合进度；
10. workspace 隔离。

```powershell
Set-Location frontend
npm run test:e2e -- task-domain-v2.spec.ts
```

### Task 37：迁移演练与灰度门禁

- [ ] 在合成大数据 fixture 上记录 snapshot/replay/drain/cutover 时长和锁等待。
- [ ] 先迁移内部隔离 workspace，再迁移无 mobile-v1 活跃 session 的 canary workspace。
- [ ] 每批迁移前检查 writer protocol heartbeat、outbox lag、generation lag、失败任务和 adapter 比例。
- [ ] 每批迁移后比较 source/v2 双向差集、用户可见计数和关键时间字段。
- [ ] 保留停止批次和向前修复 runbook；`v2_first_write_at` 非空后不执行 legacy 数据回拨。

### Task 38：稳定期与 legacy 归档

**RED 运维测试：**

- legacy 表只读后旧 writer 必须明确失败；
- v2 Web API、旧 Web adapter 和后台生成器不再查询 legacy 表；
- mobile-v1 task domain 仍返回 426；
- 删除 legacy 表的 migration 不与 cutover 同一发布。

稳定至少一个发布周期后，只读归档 legacy 表。物理删除、表重命名和 mobile-v2 均建立独立设计与实施计划。

---

## 4. 每阶段统一验证命令

### 4.1 后端聚焦与全量

```powershell
Set-Location backend

# 先运行当前任务最窄测试
go test ./internal/taskdomain -count=1

# SQLite contract
go test ./internal/storage/sqlite -run TaskDomainV2 -count=1

# PostgreSQL contract（仅测试库）
$env:FLOWSPACE_REQUIRE_POSTGRES_TESTS='true'
go test -p 1 ./internal/storage/postgres -run TaskDomainV2 -count=1

# 阶段 checkpoint
go test ./internal/... -count=1
go test -race ./internal/taskdomain ./internal/taskmigration -count=1
go vet ./...
```

### 4.2 前端

```powershell
Set-Location frontend
npm test -- --run
npm run lint
npm run build
```

### 4.3 文档和 diff

```powershell
git diff --check
git status --short
```

不得使用“全量测试通过”替代有效 RED 证明。每个 PR/提交说明必须记录：新增的失败测试、失败原因、最小实现、重构内容和实际执行命令。

---

## 5. Definition of Done

只有同时满足以下条件，统一领域模型才算完成：

- [ ] Project、Task、Schedule、ScheduleVersion、Occurrence、ExecutionLog 和 Dependency 不变量均有纯领域测试与数据库 contract 双重保护。
- [ ] PostgreSQL 与 SQLite 对合法和非法输入表现一致。
- [ ] 所有 v2 写入都经过 request-scoped runtime 与 fenced transaction，没有全局 Store 旁路。
- [ ] Task/Occurrence 状态命令原子执行，revision 冲突稳定返回 409。
- [ ] 重复生成器只有在预期 key 完整后推进 watermark，不会过早完成 Task。
- [ ] Calendar、Today、Tasks、Project 和 Roadmap 页面均使用 Occurrence 查询模型。
- [ ] 旧 Event API 的 date/time-block、全天多日和扩展字段无损往返，且不双写。
- [ ] project/task/rule/occurrence/event 的全部 DML 都有 logical version outbox 和 delete tombstone。
- [ ] drain 能拦截旧二进制写入；最终 reconcile 包含双向差集。
- [ ] mobile-v1 任务域读写在 cutover 时关闭并返回 426，旧 cursor/session scope 失效。
- [ ] v2 首次写入后只允许继续使用 v2 schema 的应用回滚。
- [ ] 后端、前端、provider contract、race、迁移、故障注入和 E2E 门禁全部绿色。
- [ ] legacy 表只在稳定期后只读归档，物理删除另开变更。

最终用户只需要理解一条链路：**项目 → 任务 → 安排 → 执行实例**；日历和今日只是执行实例的不同视图。
