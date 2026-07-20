# 用户运行时存储、AI 设置与图片上传实施计划

> **实施约束：** 本计划必须按任务顺序执行。每项生产代码变更都遵循严格 TDD：先写并运行一个因目标行为缺失而失败的测试（RED），再写最小实现（GREEN），最后只在绿色状态下重构。禁止先写实现后补测试。

**目标：** 将 FlowSpace 从部署级单一 PostgreSQL/SQLite、MinIO 和 AI 客户端，演进为按 workspace 解析的用户运行时；提供用户设置页、用户级头像和笔记图片上传；数据库/表不存在时通过显式维护流程自动创建；用户未选择时继续使用不可变的平台默认 endpoint。

**源设计：** `docs/superpowers/specs/2026-07-19-user-runtime-storage-ai-settings-design.md`

**总体架构：** 控制面保存认证、profile/version、endpoint、binding、runtime state、迁移任务、媒体目录和审计；tenant 数据面只保存 workspace 业务数据、anchor、tenant migration history、媒体实时引用/outbox 和 worker job。请求先从控制面取得持久 runtime snapshot，再解析只读 tenant store；所有写 repository 只能由 fenced `TenantWriteTx` 暴露。数据库切换使用持久 transition state machine，同 namespace 只 rebind，不复制。

**技术栈：** Go 1.26、Gin、`database/sql`、pgx stdlib、modernc SQLite、MinIO Go SDK、React 19、React Router、TanStack Query、Tiptap 3、Vitest、Playwright。

---

## 1. 实施规则

### 1.1 严格 TDD 循环

每个任务中每个独立行为都执行以下循环：

1. **RED**：只添加一个聚焦的失败测试或一个最小测试表格中的新 case。
2. **验证 RED**：运行最窄测试命令，确认失败原因是目标行为尚未实现，而不是编译错误、夹具错误、网络不可达或断言写错。
3. **GREEN**：只写让当前失败测试通过的最小生产实现，不顺手实现后续任务。
4. **验证 GREEN**：先重跑聚焦测试，再运行任务列出的 package/contract suite。
5. **REFACTOR**：仅在绿色状态下整理命名、提取 helper、删除重复；随后重跑相同测试。
6. **COMMIT**：只提交通过测试的单一工作单元。不要提交 RED 状态，不创建空提交。

若新测试第一次运行就是绿色，说明它没有证明新行为缺失：必须增强断言或调整夹具，使其针对目标缺口可靠失败后才能实现。

### 1.2 测试层级

| 层级 | 目的 | 要求 |
| --- | --- | --- |
| 单元测试 | 配置、状态机、加密、策略、解析和错误映射 | 不访问真实网络；使用 fake clock、fake dialer、fake store |
| Provider contract | PostgreSQL/SQLite 读写、fence、baseline、outbox 语义一致 | 同一组 contract 同时运行两个 provider |
| 控制面集成 | 复合 FK、CHECK、partial unique index、CAS、keyring | PostgreSQL 每测试独立 schema/database，禁止复用生产库 |
| 多实例集成 | drain、旧 epoch、通知丢失、连接池重建 | 两个 resolver/coordinator 进程对象，共享同一测试控制库/tenant 库 |
| 对象存储集成 | MinIO put/get/delete、历史 endpoint、幂等上传 | 使用专用测试 bucket/prefix；测试结束只清理自身前缀 |
| 前端组件 | 设置表单、菜单、状态、编辑器上传 | Vitest + Testing Library；网络使用 mock server/fetch |
| 端到端 | 用户设置、迁移、上传和降级体验 | Playwright；只运行在已初始化的隔离测试环境 |
| 故障注入 | transition/projector 跨库崩溃恢复 | 每个持久状态边界注入一次崩溃并重新启动恢复器 |

### 1.3 环境与数据安全

- 所有 PostgreSQL 集成测试只读取 `FLOWSPACE_TEST_CONTROL_DATABASE_URL`、`FLOWSPACE_TEST_TENANT_DATABASE_URL`；禁止在测试代码或本文档硬编码账号密码。
- 测试启动时必须拒绝数据库名不含 `test` 的 URL。数据库创建测试使用专用测试角色和临时 database 名。
- MinIO 集成测试只读取 `FLOWSPACE_TEST_MINIO_*`，对象 key 必须带测试运行 UUID 前缀。
- SQLite 测试只使用 `t.TempDir()` 下的文件，不读取开发/生产 SQLite 文件。
- 凭据测试只使用随机测试 keyring；日志断言必须检查 secret、DSN、Authorization 不出现。
- 测试不依赖 `192.168.1.20` 旧 PostgreSQL 地址；本地真实连接配置由环境提供。
- 任何 integration test 因环境变量缺失而 skip 时，CI 中必须有一个 required job 提供该环境并执行，不能让关键合同长期只处于 skip。

### 1.4 任务边界

- Phase 0～4 完成前，不开放用户数据库切换入口。
- `OpenControl` / `OpenTenant` 永不执行 DDL；只有 admin command 调用 `Migrate*` / `Adopt*`。
- handler、service、普通 worker 不得持有完整 `storage.Store`、底层 `*sql.DB` 或 maintenance handle。
- 数据库/object runtime 不允许静默回退。仅 AI 的显式 local/template fallback 可按 binding 设置执行。
- 所有 schema 行为先在 migration/contract test 中 RED，再新增 SQL 文件。
- 计划中的路径是目标路径；如执行时发现项目结构已变化，先更新本计划的 File Map 和测试命令，再继续实现。

---

## 2. 目标文件地图

### 2.1 后端核心

| 路径 | 职责 |
| --- | --- |
| `backend/internal/config/runtime_storage.go` | control/platform-data/keyring 配置与启动校验 |
| `backend/internal/storage/control.go` | `ControlStore` 和控制面 repository contracts |
| `backend/internal/storage/tenant.go` | `TenantReadStore`、`TenantFencedWriter`、`TenantWriteTx`、maintenance contracts |
| `backend/internal/storage/provider_roles.go` | `OpenControl/MigrateControl/OpenTenant/MigrateTenant/AdoptExistingTenant` |
| `backend/internal/storage/contracttest/tenant_read_contract_tests.go` | tenant 只读合同 |
| `backend/internal/storage/contracttest/tenant_fence_contract_tests.go` | PostgreSQL/SQLite fenced write 合同 |
| `backend/internal/storage/contracttest/tenant_baseline_contract_tests.go` | baseline/anchor/installation/outbox 合同 |
| `backend/internal/controlplane/` | profile、endpoint、binding、runtime state、transition、media repository |
| `backend/internal/tenantruntime/` | runtime snapshot、resolver、连接池/client cache、invalidate |
| `backend/internal/tenantmigration/` | namespace preflight、export/import、校验、transition coordinator/recovery |
| `backend/internal/credentials/` | AES-GCM keyring、AAD、rewrap |
| `backend/internal/outboundpolicy/` | DNS/IP/redirect/代理策略与受控 dialer |
| `backend/internal/ai/` | chat/transcription runtime client factories 与 capability probe |
| `backend/internal/media/` | 上传状态机、投影器、delete barrier、finalizer、GC |
| `backend/internal/handler/settings.go` | 设置/profile/runtime/transition API |
| `backend/internal/handler/media.go` | 图片/头像 API |
| `backend/internal/middleware/tenant_runtime.go` | request-scoped runtime 解析 |
| `backend/cmd/flowspace-admin/main.go` | control/tenant migrate、adopt、system profile reconcile、rewrap/rekey |

### 2.2 Migration 目录

| 路径 | 内容 |
| --- | --- |
| `backend/db/migrations/control/postgres/` | 新装控制面 baseline 与后续控制面 migration |
| `backend/db/migrations/control/sqlite/` | 单实例兼容模式的 SQLite 控制面 baseline |
| `backend/db/migrations/tenant/postgres/` | PostgreSQL tenant baseline 与版本化升级 |
| `backend/db/migrations/tenant/sqlite/` | SQLite tenant baseline 与版本化升级 |
| `backend/db/adopt/control/postgres/` | 旧混合 PostgreSQL control adopt manifests |
| `backend/db/adopt/control/sqlite/` | 旧混合 SQLite control adopt manifests |
| `backend/db/adopt/tenant/postgres/` | 旧平台 PostgreSQL tenant adopt manifests |
| `backend/db/adopt/tenant/sqlite/` | 旧 SQLite tenant adopt manifests |

现有 `backend/db/migrations/postgres/` 保留为 legacy history，完成 adopt 前不得移动或改 checksum；新 runtime 不再直接执行该目录。

### 2.3 前端

| 路径 | 职责 |
| --- | --- |
| `frontend/src/api/settings.ts` | 设置/profile/runtime/transition API client |
| `frontend/src/api/media.ts` | 图片、头像、删除状态 API client |
| `frontend/src/hooks/useSettings.ts` | query/mutation、revision/If-Match、迁移轮询 |
| `frontend/src/routes/Settings.tsx` | 设置页 shell 与分区导航 |
| `frontend/src/components/settings/` | 资料、数据库、对象、AI、安全卡片 |
| `frontend/src/extensions/ImageUpload.ts` | Tiptap image node/upload plugin |
| `frontend/src/components/editor/ImageUploadPlaceholder.tsx` | 上传进度、重试、失败状态 |
| `frontend/tests/e2e/runtime-settings.spec.ts` | 设置与 runtime E2E |
| `frontend/tests/e2e/note-images.spec.ts` | 选择/拖拽/粘贴/删除图片 E2E |

---

## 3. 执行顺序与总检查点

```text
Phase 0 基线
  -> Phase 1 配置与 provider 角色分离
  -> Phase 2 control/tenant baseline + adopt
  -> Phase 3 类型化读写边界 + fenced transaction
  -> Phase 4 runtime resolver + 全路径去 global store
  -> Phase 5 profile/keyring/default endpoint + AI
  -> Phase 6 设置页 + 用户级头像
  -> Phase 7 对象存储 + 图片媒体状态机
  -> Phase 8 数据迁移/rebind/rollback
  -> Phase 9 灰度、故障注入与 E2E
```

每个 Phase 的 checkpoint 必须全部绿色才能进入下一 Phase。若某个任务暴露了新的跨 provider 行为，先扩展共享 contract test，再实现 provider-specific SQL。

---

## Phase 0：建立可重复基线

### Task 0：创建实施分支并记录基线

**文件：** 无生产文件变更。

- [ ] **Step 1：创建分支**

```powershell
git switch -c codex/user-runtime-storage-settings
```

- [ ] **Step 2：确认当前工作树**

```powershell
git status --short
```

预期：只存在已知的设计/计划文档变更。发现其他变更时先确认归属，不覆盖用户工作。

- [ ] **Step 3：运行后端基线**

```powershell
Set-Location backend
go test ./... -count=1
```

- [ ] **Step 4：运行前端基线**

```powershell
Set-Location frontend
npm test
npm run build
```

- [ ] **Step 5：记录慢测/skip**

在 PR 描述记录失败、skip、耗时最长 package；不得把已有失败误归因于本功能。

**Checkpoint 0：** 后端、前端基线可重复；所有后续任务使用相同命令和隔离环境。

---

## Phase 1：控制面配置与 provider 生命周期分离

### Task 1：拆分 control 与 platform data 配置

**文件：**

- Create: `backend/internal/config/runtime_storage.go`
- Test: `backend/internal/config/runtime_storage_test.go`
- Modify/retire: `backend/internal/config/storage.go` and `backend/internal/storage/config.go`
- Modify tests: `backend/internal/config/storage_test.go` and `backend/internal/storage/config_test.go`
- Modify: `backend/cmd/server/main.go`
- Modify: 启动脚本/部署环境示例

- [ ] **RED 1：配置作用域测试**

先写表驱动测试覆盖：

- `FLOWSPACE_CONTROL_DATABASE_URL` 只进入 `ControlConfig`。
- `FLOWSPACE_PLATFORM_DATA_DATABASE_*` 只进入 platform candidate config。
- 两个 URL 相同合法但对象独立。
- 只设置旧 `FLOWSPACE_DATABASE_URL` 时，普通 server startup 返回弃用/未迁移错误；只有 upgrade command 可读取。
- 多实例 + SQLite control/data 被拒绝。
- control driver 与 URL/path 不匹配被拒绝。

验证 RED：

```powershell
Set-Location backend
go test ./internal/config -run "TestLoadRuntimeStorageConfig|TestValidateRuntimeStorageConfig" -count=1 -v
```

预期：因新 config 类型/校验不存在而失败。

- [ ] **GREEN 1：最小配置实现**

只实现配置解析、结构化错误和安全摘要；不要在本任务创建 profile 或打开数据库。日志摘要不得包含密码/query secret。

- [ ] **REFACTOR / VERIFY 1**

```powershell
go test ./internal/config -count=1
go test ./cmd/server ./internal/config -count=1
```

### Task 2：把 Open 与 Migrate 从接口和行为上分开

**文件：**

- Create: `backend/internal/storage/provider_roles.go`
- Test: `backend/internal/storage/provider_roles_test.go`
- Modify: `backend/internal/storage/postgres/provider.go`
- Modify: `backend/internal/storage/sqlite/provider.go`
- Modify: `backend/internal/storage/postgres/migrations.go`
- Create: `backend/cmd/flowspace-admin/main.go`

- [ ] **RED 2A：Open 不执行 DDL**

为 PostgreSQL 和 SQLite 建立空 schema/file，调用 `OpenControl` / `OpenTenant`，断言：

- 不创建 migration 表；
- 返回 `CONTROL_SCHEMA_NOT_READY` / `TENANT_SCHEMA_NOT_READY`；
- 已存在但 checksum 错误时 fail closed；
- 不调用 legacy `RunPostgresMigrationsContext` / `initializeLegacySchema`。

```powershell
go test ./internal/storage/postgres ./internal/storage/sqlite -run "TestOpen.*DoesNotMigrate|TestOpen.*SchemaNotReady" -count=1 -v
```

- [ ] **GREEN 2A：角色化 provider API**

增加 `ControlProvider`、`TenantProvider`、`TenantMaintenanceProvider`；`Open*` 只 validate/ping/schema inspect，`Migrate*` 才加载目录和执行 DDL。保留旧 `Provider.Open` 仅作为未接入 server 的临时 compatibility adapter，并用弃用测试保证 Phase 4 删除。

- [ ] **RED 2B：admin command 路由测试**

以 fake providers 测试 `migrate-control`、`migrate-tenant`、`adopt-tenant` 只调用对应 maintenance 方法，普通 `serve` 不调用任何 migrate/adopt。

- [ ] **GREEN 2B：最小 admin command**

先只实现命令解析和依赖调用；各 migration 的真实 SQL 在 Phase 2 实现。

- [ ] **VERIFY 2**

```powershell
go test ./internal/storage/... ./cmd/flowspace-admin -run "TestOpen|TestAdminCommand" -count=1
```

**Checkpoint 1：** 配置语义彻底分离；server open 路径在空/落后 schema 上只报错，不产生任何 DDL。

---

## Phase 2：控制面与 tenant baseline/adopt

### Task 3：建立 PostgreSQL/SQLite control baseline

**文件：**

- Create: `backend/db/migrations/control/postgres/0001_control_baseline.sql`
- Create: `backend/internal/storage/postgres/control_migrations.go`
- Test: `backend/internal/storage/postgres/control_migrations_test.go`
- Create: `backend/db/migrations/control/sqlite/0001_control_baseline.sql`
- Create: `backend/internal/storage/sqlite/control_migrations.go`
- Test: `backend/internal/storage/sqlite/control_migrations_test.go`
- Create/Modify: `backend/internal/storage/control.go`
- Create: `backend/internal/storage/postgres/control_store.go`

- [ ] **RED 3A：control baseline schema contract**

先对 PostgreSQL/SQLite 写同一份 schema contract，要求 fresh control database 的核心 baseline 只包含：auth/workspace/session、user profile/avatar、profile family/version、workspace endpoint/binding、AI feature、runtime state、transition job、audit；不包含 notes/tasks/events 等 tenant 表。后续媒体表通过各自 control migration runner 的同一逻辑版本加入。

- [ ] **RED 3B：安全约束测试**

对两个 provider 直接 SQL 尝试并断言数据库拒绝：跨 workspace endpoint/binding、错误 kind、非法 AI mode、同 workspace 两个 active transition、无效 operation kind/id 组合。媒体约束在 Phase 7 加表时扩展同一 contract。

- [ ] **GREEN 3：最小 control baseline 和 store**

两个 provider 都实现版本化 `control_schema_migrations(version, checksum, applied_at)`、固定 checksum、事务内逐版本迁移。先实现 schema 和最小 repository opening，不实现业务 API；SQLite control 只允许单实例模式。

- [ ] **VERIFY 3**

```powershell
go test ./internal/storage/postgres ./internal/storage/sqlite -run "TestControlBaseline|TestControlConstraints|TestControlMigrationChecksum" -count=1
```

### Task 4：建立 PostgreSQL tenant baseline 与 installation identity

**文件：**

- Create: `backend/db/migrations/tenant/postgres/0001_tenant_baseline.sql`
- Create: `backend/internal/storage/postgres/tenant_migrations.go`
- Test: `backend/internal/storage/postgres/tenant_migrations_test.go`
- Create: `backend/internal/storage/contracttest/tenant_baseline_contract_tests.go`

- [ ] **RED 4A：tenant baseline 内容测试**

断言 fresh tenant schema：

- 有且仅有一条 `tenant_installations(singleton_key=1)`，重复 migrate 不改变 installation id。
- 有 `tenant_schema_migrations`、`tenant_workspaces`、所有当前业务表和 job 表；媒体 guard/ref/outbox/head 在 Phase 7 以新的 tenant migration 加入。
- 不包含 users/sessions/service profiles/audit。
- 所有业务表都带 `workspace_id` 并引用本地 anchor；实体关联使用复合 FK。
- baseline 不执行 `CREATE EXTENSION pg_trgm`。

- [ ] **RED 4B：capability 与 checksum**

分别在已安装/未安装 `pg_trgm` 的测试 schema 验证 capability；已记录版本 checksum 被修改时 `OpenTenant` 拒绝可写。

- [ ] **GREEN 4：最小 tenant migration runner**

实现 advisory lock、固定 checksum、optional trigram branch、installation id 初始化；业务 SQL 必须从现有 mixed migrations重新整理，不直接复制跨 control FK。

- [ ] **VERIFY 4**

```powershell
go test ./internal/storage/postgres ./internal/storage/contracttest -run "TestPostgresTenantBaseline|TestTenantBaselineContract" -count=1
```

### Task 5：建立 SQLite tenant baseline、adopt 和一致性快照

**文件：**

- Create: `backend/db/migrations/tenant/sqlite/0001_tenant_baseline.sql`
- Create: `backend/internal/storage/sqlite/tenant_migrations.go`
- Create: `backend/internal/storage/sqlite/tenant_adopt.go`
- Create: `backend/internal/storage/sqlite/tenant_snapshot.go`
- Test: `backend/internal/storage/sqlite/tenant_migrations_test.go`
- Test: `backend/internal/storage/sqlite/tenant_adopt_test.go`
- Test: `backend/internal/storage/sqlite/tenant_snapshot_test.go`

- [ ] **RED 5A：SQLite baseline contract**

复用 Task 4 contract，另外断言 `PRAGMA foreign_keys=ON`、WAL capability 和 provider-neutral schema manifest；Phase 7 再用共享 contract 增加 `tenant_media_*`。

- [ ] **RED 5B：旧库 adopt**

从版本化 legacy fixture 创建 SQLite 文件，测试：

- adopt 前创建可恢复备份；
- 在 `BEGIN IMMEDIATE` 内创建 installation/anchor/migration history；
- 需要复合约束的表通过 rebuild 完成；
- `PRAGMA foreign_key_check` 非空时回滚；
- 任一步故障注入后原文件/备份可恢复，runtime binding 不变；
- 重复 adopt 幂等，manifest 不同 fail closed。

- [ ] **RED 5C：一致性导出**

fence 后开启读事务，模拟另一个 goroutine 尝试写入并断言无法成功；导出 manifest 不包含 rowid/provider-specific 派生列。

- [ ] **GREEN 5：最小 SQLite baseline/adopt/snapshot**

实现 temp backup、事务 rebuild、foreign key check 和 provider-neutral exporter；不要在 `OpenTenant` 中调用。

- [ ] **VERIFY 5**

```powershell
go test ./internal/storage/sqlite ./internal/storage/contracttest -run "TestSQLiteTenant|TestTenantBaselineContract" -count=1
```

### Task 6：旧混合平台库的 control/tenant adopt

**文件：**

- Create: `backend/db/adopt/control/postgres/legacy_manifest.json`
- Create: `backend/db/adopt/control/sqlite/legacy_manifest.json`
- Create: `backend/db/adopt/tenant/postgres/legacy_manifest.json`
- Create: `backend/db/adopt/tenant/sqlite/legacy_manifest.json`
- Create: `backend/internal/storage/adopt/manifest.go`
- Test: `backend/internal/storage/adopt/manifest_test.go`
- Modify: provider-specific adopt helpers

- [ ] **RED 6：真实 legacy fixture adopt**

从现有 migrations 构建 fixture，不手写“理想旧库”。分别验证 PostgreSQL mixed DB 和 SQLite mixed file：

- control/tenant history 分别建立；
- 旧 DDL 不重跑；
- 所有现有 workspace 获得 tenant anchor；
- 业务数据行数/hash 不变；
- manifest 列/约束/checksum 少一项即拒绝；
- adopt 可重复；
- control 与 tenant repository opening 逻辑分离，即使物理上同库。

- [ ] **GREEN 6：版本化 adopt manifest**

实现只识别明确支持的 legacy versions 的 adopt；未知版本不猜测。admin command 输出 dry-run 和结构差异，但不输出 secret/DSN。

- [ ] **VERIFY 6**

```powershell
go test ./internal/storage/adopt ./internal/storage/postgres ./internal/storage/sqlite -run "Test.*Adopt" -count=1
```

**Checkpoint 2：** fresh control、fresh tenant、PostgreSQL/SQLite legacy adopt 全部可重复；普通 Open 始终不执行 DDL。

---

## Phase 3：类型化读写边界与 fenced transaction

### Task 7：拆分 tenant read/write contracts 并建立架构守卫

**文件：**

- Create: `backend/internal/storage/tenant.go`
- Modify: `backend/internal/storage/store.go`
- Create: `backend/internal/architecture/tenant_write_boundary_test.go`
- Create: `backend/internal/storage/contracttest/tenant_read_contract_tests.go`
- Modify: PostgreSQL/SQLite repository adapters

- [ ] **RED 7A：编译期接口合同**

添加 compile assertions/fakes，要求：

- `TenantReadStore` 只有只读 repositories；不含 `Transact`、Create/Update/Delete/Claim/Complete。
- `TenantWriteTx` 才暴露 write repositories。
- `TenantWriteTx` 不能从 provider/open/runtime 直接取得，只能由 `TenantFencedWriter.BeginFencedWrite` 返回。
- Auth/Profile/Binding/Media control repositories 不出现在 tenant interfaces。

- [ ] **RED 7B：Go AST 架构测试**

使用 `go/parser` 扫描非测试代码，拒绝：

- `internal/handler`、`internal/service`、普通 worker 字段/参数类型为完整 `storage.Store`；
- 上述包调用 `.Transact` 或导入 provider-specific package；
- request/worker 代码持有 `*sql.DB` / `*sql.Tx`；
- 新增 `repository.SetStore/CurrentStore/WithScopedStore` 使用。

初次运行应因现有 router/handler/worker 注入完整 store 而 RED。此任务只建立目标接口和允许带明确到期 Task 编号的临时 allowlist；不得用广泛目录豁免让测试虚假绿色。

```powershell
Set-Location backend
go test ./internal/architecture ./internal/storage/... -run "TestTenantWriteBoundary|TestTenantReadContract" -count=1 -v
```

- [ ] **GREEN 7：最小接口和 provider adapter**

拆分每个领域的 Read/Write repository。旧完整 `Store` 临时留在 compatibility package，不再新增消费者。每次移动一个方法都先增加共享 contract case。

- [ ] **VERIFY 7**

```powershell
go test ./internal/storage/... ./internal/architecture -count=1
```

### Task 8：PostgreSQL `BeginFencedWrite`

**文件：**

- Create: `backend/internal/storage/postgres/tenant_writer.go`
- Test: `backend/internal/storage/postgres/tenant_writer_test.go`
- Create: `backend/internal/storage/contracttest/tenant_fence_contract_tests.go`

- [ ] **RED 8A：fence 基本合同**

测试 active + matching epoch 可写；fenced/retired/missing anchor、epoch mismatch 均在领域写之前失败；callback/commit 失败回滚。

- [ ] **RED 8B：跨连接排空**

连接 A 开始 fenced write 并持有 anchor 共享锁；连接 B 尝试 migration fence 必须等待；A commit 后 B 才能把 anchor 改为 fenced。B 完成后旧 epoch 的连接 C 必须失败，即使它持有旧 resolver/cache。

- [ ] **RED 8C：事务对象生命周期**

commit/rollback 后 repository 调用失败；重复 commit、跨 goroutine 使用和 nil callback 有确定错误，不发生 panic/隐式新事务。

- [ ] **GREEN 8：最小 PostgreSQL writer**

使用同一 SQL transaction `SELECT ... FOR SHARE` anchor、校验 epoch/mode、返回绑定该 `*sql.Tx` 的 write repositories。migration fence 使用 `FOR UPDATE`。不要在 commit 后回到 pool 执行领域 SQL。

- [ ] **VERIFY 8**

```powershell
go test ./internal/storage/postgres ./internal/storage/contracttest -run "TestPostgresFenced|TestTenantFenceContract" -count=1
```

### Task 9：SQLite `BeginFencedWrite` 与单实例限制

**文件：**

- Create: `backend/internal/storage/sqlite/tenant_writer.go`
- Test: `backend/internal/storage/sqlite/tenant_writer_test.go`
- Modify: runtime config validation

- [ ] **RED 9A：SQLite fence contract**

复用 Task 8 合同；额外测试 `BEGIN IMMEDIATE`、单连接、进程内 drain，以及取消 context 时事务释放。

- [ ] **RED 9B：部署模式限制**

多实例声明 + SQLite control 或 active data endpoint 必须在启动/激活前被拒绝，不能仅记录 warning。

- [ ] **GREEN 9：最小 SQLite writer**

用进程级 workspace gate + `BEGIN IMMEDIATE` 实现与 PostgreSQL 相同接口。gate 只承担单实例 drain，不宣称分布式正确性。

- [ ] **VERIFY 9**

```powershell
go test ./internal/storage/sqlite ./internal/storage/contracttest ./internal/config -run "TestSQLiteFenced|TestTenantFenceContract|Test.*MultiInstance" -count=1
```

### Task 10：控制面 runtime state CAS 与持久 operation fence

**文件：**

- Create: `backend/internal/controlplane/runtime_state.go`
- Test: `backend/internal/controlplane/runtime_state_test.go`
- Create: `backend/internal/controlplane/postgres/runtime_state.go`
- Test: `backend/internal/controlplane/postgres/runtime_state_integration_test.go`

- [ ] **RED 10A：状态转换表**

表驱动测试所有合法/非法转换：active→draining→migrating→activating→active、pre-activation recover、blocked recover；验证 epoch 只在 operation 开始时单调增加、active 清空 operation id/kind。

- [ ] **RED 10B：CAS 与并发**

两个 coordinator 读取同一 revision，只有一个能开始 operation；错误 source binding revision、epoch、operation id、mode 都返回 conflict。partial unique index 同时阻止 migration/rebind。

- [ ] **GREEN 10：最小 repository/state machine**

状态转换逻辑保持纯函数，数据库 repository 只执行带前置条件的 UPDATE/CAS；错误映射区分 conflict、blocked、not found。

- [ ] **VERIFY 10**

```powershell
go test ./internal/controlplane/... -run "TestRuntimeState|TestRuntimeCAS" -count=1
```

**Checkpoint 3：** 两个 provider 通过同一 fence contract；旧 epoch 无法在任何 PostgreSQL 实例越过 anchor；生产写接口在类型层只能来自 `TenantWriteTx`。

---

## Phase 4：Tenant runtime resolver 与全路径依赖注入

### Task 11：持久 snapshot 与 runtime resolver/cache

**文件：**

- Create: `backend/internal/tenantruntime/snapshot.go`
- Create: `backend/internal/tenantruntime/resolver.go`
- Create: `backend/internal/tenantruntime/cache.go`
- Test: `backend/internal/tenantruntime/resolver_test.go`
- Test: `backend/internal/tenantruntime/cache_test.go`

- [ ] **RED 11A：每请求持久版本校验**

fake control repository 记录调用次数；连续两个请求即使 cache hit 也各读取一次 `mode/epoch/binding_revision`。模拟通知丢失后 revision 改变，第二次请求必须解析新 runtime。

- [ ] **RED 11B：cache key 与生命周期**

验证 key 含 workspace/revision/epoch/endpoint version；singleflight 防并发重复 open；invalidate 只影响性能；in-flight runtime 不被提前 close；idle LRU 最终关闭；secret 不进入 key/log。

- [ ] **RED 11C：fail closed**

control 不可达、mode 不允许、endpoint/profile retired/unreadable、schema not ready 时返回稳定错误，不回退平台默认。

- [ ] **GREEN 11：最小 resolver**

resolver 先接 fake provider/client factory；真实 PostgreSQL/MinIO/AI factory 在后续任务接入。读 runtime 暴露 `TenantReadStore`、fenced writer、endpoint snapshots，不暴露 maintenance handle。

- [ ] **VERIFY 11**

```powershell
go test ./internal/tenantruntime -count=1
```

### Task 12：认证固定走 control，业务请求接入 runtime middleware

**文件：**

- Create: `backend/internal/middleware/tenant_runtime.go`
- Test: `backend/internal/middleware/tenant_runtime_test.go`
- Modify: `backend/internal/middleware/auth.go`
- Modify: `backend/internal/router/router.go`
- Test: router auth/runtime tests

- [ ] **RED 12A：认证不依赖 tenant**

让 tenant resolver 永远失败，login/logout/me/settings 仍只用 control store；业务 API 返回 data unavailable。数据库异常时设置页仍可访问。

- [ ] **RED 12B：workspace 来源**

请求 body/header/query 伪造 workspace/profile/epoch 不改变 session workspace runtime；缺 session scope 时不解析 tenant。

- [ ] **RED 12C：runtime mode HTTP 语义**

draining/migrating 写请求返回稳定的 409/423 error code 与 retry hint；读是否允许严格遵循 state table；blocked 不泄露 endpoint secret。

- [ ] **GREEN 12：中间件与 router 分组**

将 auth/settings routes 与 tenant business routes 分组。runtime context 只保存当前 session workspace 的 snapshot/runtime。

- [ ] **VERIFY 12**

```powershell
go test ./internal/middleware ./internal/router ./internal/handler -run "Test.*Runtime|Test.*TenantUnavailable|Test.*SettingsAvailable" -count=1
```

### Task 13：按领域移除完整 Store/global repository

此任务拆为 3 个独立提交；每组都执行一次完整 RED→GREEN，不允许一次大爆炸式改完。

#### Task 13A：notes/tasks/events/inbox/roadmaps/calendar

**文件：** 对应 `backend/internal/handler/*.go`、`backend/internal/service/*.go`、provider repositories 与 tests。

- [ ] 先把 handler/service fake 改为只提供 `TenantReadStore` / `TenantFencedWriter`，测试所有 mutation 都调用 `BeginFencedWrite` 且使用 middleware snapshot epoch。
- [ ] 执行最小签名/调用链改造；一个领域完成后运行该领域 service tests + 双 provider contracts。

```powershell
go test ./internal/handler ./internal/service ./internal/storage/... -run "Test.*(Note|Task|Event|Inbox|Roadmap|Calendar)" -count=1
```

#### Task 13B：sync、mobile、native/watch/voice

- [ ] RED 覆盖同步批量写、mobile mutation、watch task update、voice upload metadata 全部在一个 fenced tx 内。
- [ ] 移除 service allowlist 中的 global repository；native object lookup 暂用 endpoint snapshot，不使用启动单例。

```powershell
go test ./internal/service ./internal/handler ./internal/mobilesync ./internal/storage/... -run "Test.*(Sync|Mobile|Watch|Voice)" -count=1
```

#### Task 13C：删除 compatibility Store/facade

- [ ] 先收紧 AST 测试为零 allowlist，并写测试确认 server/router/worker 不再调用 `repository.SetStore`。
- [ ] 删除生产 `repository.CurrentStore/SetStore/WithScopedStore`、旧 `Provider.Open -> Store` compatibility path 和普通 tenant `Store.Transact`。

```powershell
go test ./internal/architecture ./internal/repository ./internal/router ./cmd/server -count=1
go test ./... -count=1
```

### Task 14：后台任务资源快照与执行 epoch

**文件：**

- Modify: transcription/mobile/sync/cleanup job models and tenant migrations
- Create: `backend/internal/tenantruntime/worker_coordinator.go`
- Test: `backend/internal/tenantruntime/worker_coordinator_test.go`
- Modify: `backend/internal/transcriptionjob/worker.go`
- Modify: other worker packages

- [ ] **RED 14A：资源快照不可变**

创建 job 后切换 AI/object binding，重试仍使用创建时 endpoint/profile/model snapshot。

- [ ] **RED 14B：执行 epoch 动态读取**

`created_epoch` 只用于审计；claim 写入当前 `execution_epoch`。heartbeat/complete/fail 重新读取 snapshot 并通过 fenced writer；旧 lease 在 migration 后 completion 失败。

- [ ] **RED 14C：跨迁移任务恢复**

复制 queued/processing jobs 后，资源 snapshot/created_epoch 保留，lease token/execution_epoch 清空；目标重新 claim，源旧 worker 不能提交。

- [ ] **GREEN 14：coordinator 与 worker adapter**

先改 transcription worker，再逐个复用到 mobile publisher、voice cleanup、sync worker。外部 AI/MinIO 调用发生在 lease 期间，所有状态写入仍是独立 fenced tx。

- [ ] **VERIFY 14**

```powershell
go test ./internal/transcriptionjob ./internal/voiceaudiocleanup ./internal/mobilesyncpublisher ./internal/tenantruntime -count=1
```

**Checkpoint 4：** server/router/service/worker 不再依赖 global/full tenant Store；每请求校验持久 snapshot；所有任务写入使用执行时 epoch。

---

## Phase 5：凭据、profile、默认 endpoint 与 AI runtime

### Task 15：AES-GCM keyring 与滚动轮换

**文件：**

- Create: `backend/internal/credentials/keyring.go`
- Create: `backend/internal/credentials/cipher.go`
- Create: `backend/internal/credentials/rewrap.go`
- Test: corresponding `_test.go`
- Modify: runtime config

- [ ] **RED 15A：加解密/AAD**

round-trip；随机 nonce；篡改 ciphertext/AAD/key id 失败；不同 workspace/family/version/kind 不能互换密文；错误/日志不含 plaintext。

- [ ] **RED 15B：keyring 配置**

active key 缺失、重复 id、错误 key size 启动失败；读取按行 `encryption_key_id` 精确选 key，不盲试其他 key。

- [ ] **RED 15C：滚动轮换**

覆盖 old-only、old+new active-old、active-new、新写、CAS rewrap、仍有 old 引用阻止删除、缺 key 标记 unreadable。并发 profile 更新不能被 rewrap 覆盖。

- [ ] **GREEN 15：最小 keyring/cipher/rewrap**

key material 只从挂载文件/secret provider 读取；数据库只存 key id/nonce/ciphertext。rewrap 支持 dry-run、batch 和 resume cursor。

- [ ] **VERIFY 15**

```powershell
go test ./internal/credentials ./internal/config -count=1
```

### Task 16：profile family/version、endpoint、binding repositories

**文件：**

- Create: `backend/internal/controlplane/profiles.go`
- Create: `backend/internal/controlplane/bindings.go`
- Create: `backend/internal/controlplane/postgres/profiles.go`
- Create: `backend/internal/controlplane/postgres/bindings.go`
- Test: unit + PostgreSQL integration tests

- [ ] **RED 16A：不可变 version**

verified version 不能原地编辑/硬删除；edit 创建 family 下一 version；`preserve` 解密旧 secret 后按新 AAD/nonce 重加密；retired 禁止新 endpoint/job，但历史解析可读。

- [ ] **RED 16B：复合安全边界**

数据库直接拒绝跨 workspace profile/endpoint、错误 kind/source、非法 mode/profile id 组合。transcription `reuse_chat` 不保存跨 kind profile FK。

- [ ] **RED 16C：binding revision**

更新某 kind 必须 CAS row revision，并在同一 control tx 增加全局 runtime binding revision；两个 tab 只有一个成功。

- [ ] **GREEN 16：最小 repositories**

所有 secret 操作调用 credentials package；GET DTO 只返回 `secret_configured`/安全摘要。

- [ ] **VERIFY 16**

```powershell
go test ./internal/controlplane/... -run "TestProfile|TestEndpoint|TestBinding" -count=1
```

### Task 17：system profile bootstrap 与具体默认 endpoint

**文件：**

- Create: `backend/internal/controlplane/system_profiles.go`
- Test: `backend/internal/controlplane/system_profiles_test.go`
- Modify: `backend/cmd/flowspace-admin/main.go`

- [ ] **RED 17A：首次导入**

从 platform data/MinIO/AI 配置生成不可变 system versions；每个 workspace 原子创建四个 system endpoints、四行 default bindings、AI feature defaults、runtime active/epoch=1。

- [ ] **RED 17B：环境变化只创建 candidate**

修改 platform data URL 后仍在原 control store 创建新 candidate；已有 binding、对象、job 不变化。相同指纹重复 reconcile 幂等。

- [ ] **RED 17C：未配置能力**

可选 AI 未配置时创建明确 unavailable system version；不以 NULL/运行时 env fallback 表示。

- [ ] **GREEN 17：bootstrap/reconcile command**

普通 server startup 不自动 promote candidate。首次 legacy upgrade 允许 control/data URL 相同，但分别记录角色。

- [ ] **VERIFY 17**

```powershell
go test ./internal/controlplane ./cmd/flowspace-admin -run "TestSystemProfile|TestBootstrapWorkspaceDefaults" -count=1
```

### Task 18：受控 outbound dialer 与 endpoint probe

**文件：**

- Create: `backend/internal/outboundpolicy/policy.go`
- Create: `backend/internal/outboundpolicy/dialer.go`
- Create: `backend/internal/outboundpolicy/http.go`
- Test: corresponding `_test.go`
- Modify: PostgreSQL/MinIO/AI client factories

- [ ] **RED 18A：IP/域名策略**

拒绝 loopback/link-local/multicast/metadata/multi-host DSN；RFC1918 只有 allowlisted CIDR 可用；IPv4/IPv6 都覆盖。

- [ ] **RED 18B：每次物理连接重新校验**

fake DNS 第一次返回允许地址、第二次返回禁止地址；PostgreSQL pool 第二次 physical dial 必须拒绝，证明没有 check-then-driver-resolve TOCTOU。

- [ ] **RED 18C：HTTP 安全**

环境代理无效；每跳 redirect 重验；跨 origin 不传 Authorization；TLS `ServerName` 使用原 hostname；响应大小/超时/并发限制生效。

- [ ] **GREEN 18：统一 dialer/transport**

PostgreSQL、MinIO、AI 和 profile test 全部复用，禁止 provider 自建默认 `http.Client`/dial path。

- [ ] **VERIFY 18**

```powershell
go test ./internal/outboundpolicy ./internal/objectstore ./internal/ai ./internal/storage/postgres -run "Test.*(Dial|DNS|Redirect|Proxy|EndpointProbe)" -count=1
```

### Task 19：AI binding resolver 与功能开关

**文件：**

- Create: `backend/internal/ai/chat.go`
- Create: `backend/internal/ai/factory.go`
- Create: `backend/internal/ai/capabilities.go`
- Test: corresponding tests
- Modify: roadmap、furigana、transcription services

- [ ] **RED 19A：mode 解析**

覆盖 chat `default/custom/disabled`、transcription `default/custom/reuse_chat/disabled`；reuse_chat 仅在 chat active 且 endpoint 声明 transcription capability 时成功。

- [ ] **RED 19B：feature fallback**

roadmap template、furigana local fallback 只在显式 feature setting 允许时发生；结果标明来源。数据库/object failure 永不进入这些 fallback。

- [ ] **RED 19C：错误和脱敏**

网络、401、404 model、429、协议错误映射成稳定 code；API key/provider body 不进入响应、日志和审计。

- [ ] **GREEN 19：最小 client/factory 与 service 注入**

删除 service 中 `os.Getenv("AI_...")`；同步调用使用 runtime client，异步调用使用 job resource snapshot。

- [ ] **VERIFY 19**

```powershell
go test ./internal/ai ./internal/service ./internal/transcriptionjob -run "Test.*(AI|Roadmap|Furigana|Transcription)" -count=1
```

**Checkpoint 5：** profile/version/keyring/default endpoint/AI mode 全部由控制面明确建模；环境变量不参与请求时解析。

---

## Phase 6：设置 API、设置页与用户级头像

### Task 20：用户资料与头像控制面 API

**文件：**

- Create: `backend/internal/controlplane/user_profile.go`
- Create: `backend/internal/controlplane/postgres/user_profile.go`
- Create: `backend/internal/handler/profile.go`
- Test: repository/handler tests
- Modify: auth current-user DTO

- [ ] **RED 20A：用户级作用域**

同一用户属于两个 workspace 时，头像在两个 workspace/session 下均可读取；另一个用户不可读写。删除头像恢复 GitHub avatar/首字母 fallback。

- [ ] **RED 20B：格式与限制**

只接受 magic bytes 验证后的 JPEG/PNG/WebP，最大 2 MiB；拒绝 GIF/SVG、伪 MIME、像素炸弹；保存原字节和 SHA-256，不转码。

- [ ] **RED 20C：tenant 故障无关**

tenant resolver 不可用时 profile/头像 GET/PATCH/POST/DELETE 仍通过 control store 工作。

- [ ] **GREEN 20：最小 profile/avatar API**

头像存 `user_avatar_blobs`，响应使用鉴权应用 URL；不要创建 workspace media asset 或依赖 MinIO。

- [ ] **VERIFY 20**

```powershell
go test ./internal/controlplane/... ./internal/handler ./internal/router -run "Test.*(UserProfile|Avatar)" -count=1
```

### Task 21：设置/profile/runtime/transition API

**文件：**

- Create: `backend/internal/handler/settings.go`
- Test: `backend/internal/handler/settings_test.go`
- Modify: `backend/internal/router/router.go`
- Create: request/response models

- [ ] **RED 21A：权限和 DTO 脱敏**

owner 可修改；member 只看到“由空间管理员管理”和健康摘要；平台管理员默认看不到 secret。响应中不出现 ciphertext、DSN password、access key secret、API key。

- [ ] **RED 21B：revision 与显式 mode**

AI/object update 缺 `If-Match`/revision 返回 428；过期 revision 返回 409；非法 mode/source/id 由 handler 校验和数据库约束双重拒绝。数据库 binding 不能由普通 runtime PUT 修改。

- [ ] **RED 21C：profile 测试/保存分离**

draft 可测试但不能绑定；test 区分 network/TLS/auth/permission/capability；verified 保存创建 immutable version；secret action 必填。

- [ ] **GREEN 21：最小 settings handlers**

按设计暴露 `/api/settings`、profiles、runtime、storage transition routes；transition endpoint 在 Phase 8 前可返回 feature-not-enabled，但 contract/权限先固定。

- [ ] **VERIFY 21**

```powershell
go test ./internal/handler ./internal/router -run "TestSettings|TestProfileAPI|TestRuntimeAPI" -count=1
```

### Task 22：前端设置入口与资料页

**文件：**

- Create: `frontend/src/api/settings.ts`
- Create: `frontend/src/api/media.ts`
- Create: `frontend/src/hooks/useSettings.ts`
- Create: `frontend/src/routes/Settings.tsx`
- Create: `frontend/src/components/settings/ProfileSettings.tsx`
- Modify: `frontend/src/components/layout/TopBar.tsx`
- Modify: `frontend/src/components/layout/TopBar.test.tsx`
- Modify: `frontend/src/router.tsx` / `router.test.tsx`

- [ ] **RED 22A：头像菜单导航**

Testing Library 测试右上角菜单显示“用户设置”，点击进入 `/settings`；键盘 Enter/Space、Escape、点击外部和 focus return 正常；头像优先显示上传 URL。

- [ ] **RED 22B：资料表单**

加载、保存、头像预览/上传/删除、格式/大小错误、tenant unavailable 时仍可使用。邮箱只读。

- [ ] **GREEN 22：最小 settings shell/profile**

先只实现个人资料 tab 和占位但不可误操作的其他 tab。路由 lazy load，现有 account/admin 流不回归。

- [ ] **VERIFY 22**

```powershell
Set-Location frontend
npm test -- --run src/components/layout/TopBar.test.tsx src/routes/Settings.test.tsx src/router.test.tsx
npm run build
```

### Task 23：数据库、对象与 AI 设置卡片

**文件：**

- Create: `frontend/src/components/settings/DatabaseSettings.tsx`
- Create: `frontend/src/components/settings/ObjectStorageSettings.tsx`
- Create: `frontend/src/components/settings/AISettings.tsx`
- Test: adjacent component tests

- [ ] **RED 23A：默认状态**

无用户选择时显示“平台默认”且不要求填表；四类能力独立，修改一项不改变其他项。

- [ ] **RED 23B：draft/test/save**

测试连接前不能启用；secret 已保存只显示掩码；`preserve/replace/clear` 意图明确；并发 revision 冲突提示刷新而不覆盖。

- [ ] **RED 23C：迁移 UX**

数据库卡没有“直接保存切换”；只允许“测试→初始化→迁移并启用”。迁移中显示只读/进度/可恢复状态。对象切换明确提示仅新对象写新位置。

- [ ] **RED 23D：AI mode/feature**

disabled/reuse_chat 字段联动正确；roadmap/furigana fallback 单独保存；provider error 不把 secret 放入页面。

- [ ] **GREEN 23：最小设置卡片**

TanStack Query key 含 workspace/revision；mutation 成功后精确 invalidate。Phase 8 前数据库迁移按钮受后端 capability flag 控制。

- [ ] **VERIFY 23**

```powershell
npm test -- --run src/components/settings
npm run build
```

**Checkpoint 6：** 设置页从头像可达；tenant 故障不影响设置/头像；默认、custom、disabled/reuse_chat 状态在 UI/API/数据库约束间一致。

---

## Phase 7：对象 runtime、笔记图片与媒体生命周期

### Task 24：对象 endpoint resolver 与历史对象路由

**文件：**

- Create: `backend/internal/objectstore/factory.go`
- Create: `backend/internal/objectstore/resolver.go`
- Test: corresponding tests
- Modify: voice note/object job schema/repositories

- [ ] **RED 24A：对象记住具体 endpoint**

创建对象后切换 binding/修改 platform env，旧对象仍通过记录的 system/custom endpoint version 读取；新对象只进入当前显式 binding。

- [ ] **RED 24B：无静默回退**

原 endpoint 认证失败/retired 可历史读取但不可新写；不可达时返回 object unavailable，不查询当前默认 bucket。

- [ ] **RED 24C：cache 与脱敏**

client cache 按 endpoint version/revision；secret 不进 cache key/metric；profile version 退休时在途读完成后释放。

- [ ] **RED 24D：bucket 权限与创建策略**

probe 执行临时 put/get/head/delete 闭环；bucket 不存在时默认返回明确错误，不用用户凭据自动创建。只有部署管理员显式启用的私有部署开关才允许创建，workspace 请求不能覆盖该开关。

- [ ] **GREEN 24：最小 factory/resolver**

把 voice upload/read/cleanup 从启动单例改为对象 endpoint snapshot 解析；沿用受控 HTTP transport。

- [ ] **VERIFY 24**

```powershell
Set-Location backend
go test ./internal/objectstore ./internal/handler ./internal/voiceaudiocleanup -run "Test.*ObjectEndpoint|Test.*VoiceObject" -count=1
```

### Task 25：媒体控制面和 tenant 实时引用 schema

**文件：**

- Create: control migration for `media_assets`、receipts、links、heads、watermarks
- Create: tenant PostgreSQL/SQLite migration for guards、refs、outbox、heads
- Create: `backend/internal/media/repository.go`
- Create: provider implementations
- Test: control + shared tenant contracts

- [ ] **RED 25A：复合归属约束**

数据库拒绝跨 workspace asset/endpoint/link/ref；note ref 只能引用已注册 guard。media asset kind 只允许 `note_image`。

- [ ] **RED 25B：sequence/事务合同**

note create/update/delete 在同一 fenced tx 更新 note revision、tenant refs、分配无 gap workspace sequence、写完整 reference event。事务失败三者都回滚。

- [ ] **RED 25C：note 物理删除**

删除 note 后 outbox 仍存在可发布，tenant refs 被清理；证明 outbox 不错误外键绑定已删除 note。

- [ ] **GREEN 25：最小 schema/repositories**

PostgreSQL/SQLite 使用各自 JSON 表达但输出同一 domain event。所有 sequence 分配和 ref 更新只由 `TenantWriteTx` 暴露。

- [ ] **VERIFY 25**

```powershell
go test ./internal/media ./internal/storage/postgres ./internal/storage/sqlite ./internal/storage/contracttest -run "TestMedia|TestTenantMedia" -count=1
```

### Task 26：幂等图片上传状态机

**文件：**

- Create: `backend/internal/media/upload.go`
- Test: `backend/internal/media/upload_test.go`
- Create: `backend/internal/handler/media.go`
- Test: `backend/internal/handler/media_test.go`
- Modify: router

- [ ] **RED 26A：`Idempotency-Key` 协议**

同 workspace/user/key+hash 重试复用固定 asset id；ready 返回同 response；pending 继续原状态机；同 key 不同 hash 返回 409；不同 key 相同 bytes 可产生不同逻辑 asset。

- [ ] **RED 26B：跨资源崩溃恢复**

在 receipt reservation、object put、tenant guard commit、control ready 各边界注入崩溃；重试/reconciler 不重复对象、不返回未注册 asset、不删除已有 guard 的对象。

- [ ] **RED 26C：格式/字节语义**

JPEG/PNG/WebP/GIF 依据 magic bytes；动画 GIF 原样保存；SHA-256 对实际存储字节；key 扩展名为 `jpg/png/webp/gif`；拒绝 SVG、伪 MIME、>10 MiB、>20 MP。

- [ ] **RED 26D：鉴权读取**

同 workspace session 可读；其他 workspace/user 返回 404/403 约定；MinIO credentials/presigned URL 不下发；Range/ETag/conditional request 正确。

- [ ] **GREEN 26：最小 upload/read API**

实现 reserve→put→fenced guard→ready 状态机和同步重试；只在 ready 返回稳定 `/api/assets/:id/content` URL。

- [ ] **VERIFY 26**

```powershell
go test ./internal/media ./internal/handler ./internal/objectstore -run "TestUpload|TestAssetContent|TestImageFormat" -count=1
```

### Task 27：单调 projector、delete barrier 与 GC

**文件：**

- Create: `backend/internal/media/projector.go`
- Create: `backend/internal/media/delete.go`
- Create: `backend/internal/media/finalizer.go`
- Create: `backend/internal/media/gc.go`
- Test: adjacent tests

- [ ] **RED 27A：乱序/重放**

同 note revision 5、3、5 replay、6 乱序投递：links 只按 note revision 单调前进，workspace event watermark 按 sequence 连续前进；control commit 后 tenant publish 前崩溃只导致幂等重放。

- [ ] **RED 27B：未投影引用与 DELETE 竞争**

note 已提交、outbox 未发布时 DELETE 查询 tenant refs 返回 409；删除事务与新 note ref 并发时 guard row lock 只允许一个合法结果。

- [ ] **RED 27C：delete barrier finalization**

无引用 DELETE 返回 202/幂等状态；watermark < barrier、outbox 未发布、grace 未过、tenant 不可达、任一 link/ref 存在都不得删对象。全部满足才标记 deleted。

- [ ] **RED 27D：晚到异常引用**

模拟 legacy 旁路在 barrier 后产生引用，projector 取消 control delete request、停止 finalizer、告警，repairer fenced re-activate guard。

- [ ] **RED 27E：orphan GC**

只清理超时 pending/uploading/orphaned 且无 guard 的对象；绝不以“control 当前无 link”删除正常 asset。

- [ ] **GREEN 27：最小 workers**

projector 按 workspace 串行/可分片；所有 delete 检查不确定时 fail closed。metrics 不使用 workspace/email/host 高基数标签。

- [ ] **VERIFY 27**

```powershell
go test ./internal/media -run "TestProjector|TestDeleteBarrier|TestMediaGC" -count=1
```

### Task 28：Tiptap 图片上传体验

**文件：**

- Create: `frontend/src/extensions/ImageUpload.ts`
- Create: `frontend/src/extensions/ImageUpload.test.ts`
- Create: `frontend/src/components/editor/ImageUploadPlaceholder.tsx`
- Modify: `frontend/src/routes/Editor.tsx`
- Modify/Test: `frontend/src/routes/Editor.test.tsx`

- [ ] **RED 28A：插入方式**

选择、拖拽、粘贴都调用上传 API，生成唯一 Idempotency-Key，上传中插入当前位置 placeholder，成功替换为带 `assetId/src/alt/title` 的 image node。

- [ ] **RED 28B：失败/重试/序列化**

失败保留可重试 placeholder；同一次重试复用 key；移除不污染正文；Markdown round-trip 为稳定应用 URL，不写 blob/presigned URL。

- [ ] **RED 28C：自动保存引用**

图片 node 增删触发正常 note revision save；上传未 ready 前不保存虚假 asset ref；迁移只读时禁用上传并显示原因。

- [ ] **GREEN 28：最小 extension/UI**

保持现有 Ruby/Markdown extensions 兼容；上传进度状态不写入最终 Markdown。

- [ ] **VERIFY 28**

```powershell
Set-Location frontend
npm test -- --run src/extensions/ImageUpload.test.ts src/routes/Editor.test.tsx
npm run build
```

### Task 28A：历史对象迁移与 endpoint 退役（可延后到运维增强批次）

**文件：**

- Create: `backend/internal/media/object_migration.go`
- Test: `backend/internal/media/object_migration_test.go`
- Extend: control transition/job schema and settings API

- [ ] **RED 28A-1：逐对象幂等复制**

job 固化 source/target endpoint versions；copy 后以 size/hash/head 校验，再用 CAS 更新对象目录的具体 endpoint id。崩溃重试不产生重复逻辑对象。

- [ ] **RED 28A-2：并发与失败安全**

迁移期间新上传进入当前 binding；失败对象继续引用旧 endpoint；目标不可达不修改目录；源删除只在对象目录/voice/job 无引用且保留期结束后发生。

- [ ] **RED 28A-3：退役规则**

承载历史对象的 endpoint/profile 只能 retired、不能硬删除；retired 允许历史读取但禁止新上传/job。

- [ ] **GREEN 28A：最小对象迁移 worker**

使用 checkpoint cursor 和 per-object 状态；不使用双写。设置页展示复制/校验/失败数量，源清理由独立显式动作执行。

- [ ] **VERIFY 28A**

```powershell
Set-Location backend
go test ./internal/media ./internal/objectstore -run "TestObjectMigration|TestEndpointRetirement" -count=1
```

**Checkpoint 7：** 两个 workspace 可写不同 MinIO；历史对象仍通过原 endpoint 读取；上传、引用投影、删除 barrier 和 GC 在崩溃/乱序下不丢正在使用的图片。

---

## Phase 8：数据库自动初始化、迁移、rebind 与 rollback

### Task 29：数据库 endpoint preflight、自动建库/建表和 namespace identity

**文件：**

- Create: `backend/internal/tenantmigration/preflight.go`
- Create: `backend/internal/tenantmigration/postgres_create.go`
- Create: `backend/internal/tenantmigration/namespace.go`
- Test: corresponding unit/integration tests

- [ ] **RED 29A：结构化连接与安全标识符**

拒绝 multi-host/危险 query/非法 database/schema identifier；identifier 始终 quote，不拼接原始 SQL；错误/审计不回显密码。

- [ ] **RED 29B：database 不存在**

目标返回 SQLSTATE `3D000` 时才连接显式 maintenance DB；有 CREATEDB 权限则创建并重连；并发 duplicate database 幂等继续；无权限返回可操作错误；绝不猜测其他 database。

- [ ] **RED 29C：表/schema 不存在**

只有显式 initialize 调用 `MigrateTenant` 创建 schema、installation、tables/indexes/history；普通 test/open 不执行 DDL。缺 DDL 权限阻止 verified/transition。

- [ ] **RED 29D：namespace identity**

不同 endpoint id、DNS alias、credential 连接同 installation+schema 判同 namespace；不同 schema 判不同；克隆相同 installation id 产生 collision 并要求 admin rekey，不自动迁移。

- [ ] **GREEN 29：最小 preflight/initializer**

将 namespace snapshot 固化进 transition job；profile 只有完整 probe/initialize 成功才 verified。

- [ ] **VERIFY 29**

```powershell
go test ./internal/tenantmigration -run "TestPreflight|TestCreateDatabase|TestNamespace" -count=1
```

### Task 30：provider-neutral export/import 与校验

**文件：**

- Create: `backend/internal/tenantmigration/manifest.go`
- Create: `backend/internal/tenantmigration/export.go`
- Create: `backend/internal/tenantmigration/import.go`
- Create: `backend/internal/tenantmigration/verify.go`
- Create: provider adapters
- Test: fixture/integration tests

- [ ] **RED 30A：PostgreSQL→PostgreSQL**

fixture 覆盖 folders/notes/tasks/events/inbox/roadmaps/sync/mobile/watch/voice/transcription/jobs/media refs/outbox/tombstones；保留 ID/time/revision/state，搜索派生列目标重建。

- [ ] **RED 30B：SQLite→PostgreSQL**

同一 fixture 产生相同逻辑 manifest/hash；不复制 SQLite rowid/provider-specific FTS；导出发生在成功 fence 后的一致性 snapshot。

- [ ] **RED 30C：事务与残留策略**

中途失败整个目标 workspace import 回滚。目标 active/unknown 拒绝；retired 默认拒绝；显式 `replace_retired` 在验证未被 active binding 使用后事务清空+导入；同 migration fenced 目标允许 resume。

- [ ] **RED 30D：校验失败**

行数、主键集合、关键 hash、最大 revision、schema capability 任一不符不能 activate；错误定位到逻辑表但不输出用户正文/secret。

- [ ] **GREEN 30：最小 transfer pipeline**

以 logical table manifest 驱动，不为每种 source/target 复制一套 coordinator。第一版不实现 merge/staging。

- [ ] **VERIFY 30**

```powershell
go test ./internal/tenantmigration -run "TestExport|TestImport|TestVerify|TestReplaceRetired" -count=1
```

### Task 31：transition coordinator、恢复器和双实例 fence

**文件：**

- Create: `backend/internal/tenantmigration/coordinator.go`
- Create: `backend/internal/tenantmigration/recovery.go`
- Create: `backend/internal/tenantmigration/faults.go`（仅测试注入接口）
- Test: coordinator/recovery/multi-instance integration tests

- [ ] **RED 31A：完整 migration happy path**

preflight→draining(epoch+1)→source fence→snapshot→target fenced import→verify→control activating CAS→target active→source retired→control active。每一步断言持久状态/anchor。

- [ ] **RED 31B：每个状态边界崩溃**

在上述每次 control/tenant commit 后崩溃并新建 coordinator；恢复器只能推进合法终态，任何时刻最多一个 active anchor。进入 activating 后不得回头激活源。

- [ ] **RED 31C：双实例/旧缓存**

实例 A 有旧 epoch write，drain 等待；实例 B 丢失通知并保留旧 pool，fence 后 write/complete job 均失败；下一请求读取新 persistent revision。

- [ ] **RED 31D：CAS 冲突/取消**

source binding/runtime revision 变化时不覆盖；pre-activation cancel/recover 以新 epoch 恢复源；activation 后 cancel 被拒绝。

- [ ] **RED 31E：namespace snapshot 防漂移**

preflight 后目标被 restore/rekey、schema identity 改变或 DNS 指到另一 installation 时，copy/activation 重新读取的 identity 与 job snapshot 不符，任务进入 blocked 且不激活。

- [ ] **GREEN 31：最小 coordinator/recovery**

所有 state transition 调 Task 10 repository；通知只用于加速 invalidate。进度可恢复，不把大快照放控制面。

- [ ] **VERIFY 31**

```powershell
go test ./internal/tenantmigration -run "TestMigrationStateMachine|TestMigrationRecovery|TestTwoInstanceFence" -count=1
```

### Task 32：同 namespace rebind 与反向 rollback job

**文件：**

- Extend: transition coordinator/state tests
- Modify: settings handlers/API models
- Test: handler + integration tests

- [ ] **RED 32A：rebind**

same namespace 的 migration 创建被拒绝；rebind 走 pending→preflight→draining→activating→completed，不进入 copy/verify，不初始化/删除数据；排空旧 pool、提升 epoch、CAS binding、同一 anchor 恢复 active。

- [ ] **RED 32B：migration/rebind 互斥**

同 workspace 一个 active transition 后，第二个任意 kind 由 partial unique index 直接拒绝。

- [ ] **RED 32C：rollback 语义**

对 completed migration 调 rollback 创建新的 reverse migration，`caused_by_migration_id` 指向原 completed migration；原状态不变。新库已有写入也通过反向 snapshot 搬回，不直接切 endpoint。

- [ ] **GREEN 32：最小 rebind/reverse job API**

API 返回新 job id；UI 复用进度组件但明确“反向迁移”。不提供丢弃新写入捷径。

- [ ] **VERIFY 32**

```powershell
go test ./internal/tenantmigration ./internal/handler -run "TestRebind|TestRollbackCreatesReverseMigration|TestTransitionMutualExclusion" -count=1
```

### Task 33：启用数据库迁移 UI

**文件：**

- Modify: `frontend/src/components/settings/DatabaseSettings.tsx`
- Modify: `frontend/src/hooks/useSettings.ts`
- Test: component tests

- [ ] **RED 33：完整用户流**

测试 database missing 自动初始化进度、权限不足提示、same namespace 引导 rebind、target retired 决策确认、迁移只读、失败恢复、completed 后新 binding、rollback 返回新 job。

- [ ] **GREEN 33：打开 capability flag**

只在后端 transition capability ready 且 schema version 满足时显示启用操作；轮询退避且页面恢复后可继续。

- [ ] **VERIFY 33**

```powershell
Set-Location frontend
npm test -- --run src/components/settings/DatabaseSettings.test.tsx
npm run build
```

**Checkpoint 8：** PostgreSQL→PostgreSQL、SQLite→PostgreSQL、custom→system 反向迁移通过；same namespace 只 rebind；故障恢复不会双活或丢 committed 数据。

---

## Phase 9：灰度、可观测性与端到端验收

### Task 34：安全审计、指标与运行状态

**文件：**

- Modify/create metrics、audit、status handlers
- Test: redaction/metric cardinality/status tests

- [ ] **RED 34A：审计脱敏**

profile test/save/retire、binding、transition、upload/delete、rewrap 均有审计；metadata 不含 password/key/ciphertext/完整 DSN/provider body。

- [ ] **RED 34B：指标基数**

指标只按 provider kind/state/error class；拒绝把 workspace/user/email/host/database/object key 作为 label。

- [ ] **RED 34C：健康语义**

`/api/health` 只检测 control；`/api/settings/runtime/status` 返回各 endpoint 最近 probe；单 workspace circuit breaker 不阻塞其他 workspace。

- [ ] **RED 34D：资源配额与隔离**

验证每 profile pool 上限、全局连接预算、LRU 回收和 probe 并发限额；workspace A 的断路/超时不占满预算导致 workspace B 不可用。

- [ ] **GREEN 34：最小 audit/metrics/status**

统一 redaction helper；所有新错误先映射安全 code 再日志化。

- [ ] **VERIFY 34**

```powershell
go test ./internal/handler ./internal/controlplane ./internal/tenantruntime ./internal/media ./internal/tenantmigration -run "Test.*(Audit|Redact|Metrics|RuntimeStatus)" -count=1
```

### Task 35：升级、灰度与回退演练

**文件：**

- Update: `README.md`、部署文档、运维 runbook
- Add: migration/recovery runbook
- Test: admin command smoke scripts/tests

- [ ] **Step 1：fresh install 演练**

显式 `migrate-control`、`migrate-tenant`、system profile bootstrap 后启动 server；确认 server 自身不跑 DDL。

- [ ] **Step 2：legacy PostgreSQL/SQLite upgrade 演练**

备份→control adopt→tenant adopt→system endpoint backfill→启动；不搬用户业务数据，所有旧对象回填具体 endpoint。

- [ ] **Step 3：keyring rolling deploy 演练**

old+new active-old→active-new→rewrap→确认零 old refs→移除 old。任一步失败可继续读已有密文。

- [ ] **Step 4：灰度开关**

依次开放：AI custom→设置/头像→object custom/image→database migration。每阶段可关闭“新建/切换”但不破坏已有 endpoint 解析。

- [ ] **Step 5：恢复演练**

control backup restore、tenant endpoint unavailable、migration blocked、media projector backlog、missing key id 都有明确 runbook；不得用隐式默认回退“修复”。

### Task 36：全量自动化与 E2E

- [ ] **后端全量**

```powershell
Set-Location backend
go test ./... -count=1
go test -race ./internal/tenantruntime/... ./internal/tenantmigration/... ./internal/media/... -count=1
```

- [ ] **前端全量**

```powershell
Set-Location frontend
npm test
npm run lint
npm run build
```

- [ ] **Playwright 场景**

```powershell
npm run test:e2e -- tests/e2e/runtime-settings.spec.ts tests/e2e/note-images.spec.ts
```

必须覆盖：

1. 新用户不设置任何配置即可使用 concrete system defaults。
2. 用户 A/B 使用不同 PostgreSQL/MinIO/AI 且不串数据/图片/配置。
3. tenant DB 不可用仍能登录、进入设置、更新凭据。
4. 上传选择/拖拽/粘贴、失败重试、刷新后图片仍可读。
5. note 已保存但 media outbox 未投影时删除图片不会误删。
6. 两实例 migration fence、通知丢失、旧 epoch write 拒绝。
7. same namespace endpoint 使用 rebind；rollback 创建反向 job。
8. platform 环境配置变化只创建 candidate，已有 binding/对象不漂移。

- [ ] **故障注入 required suite**

在 CI 单独 job 运行 transition/projector crash matrix。所有状态边界必须至少被杀进程/重建 coordinator 一次，不能仅 mock 返回 error。

**Checkpoint 9：** 全量、race、integration、fault-injection、frontend build、E2E 全绿；运维有可执行 upgrade/recovery/key rotation 文档。

---

## 4. 设计决策覆盖矩阵

| 设计决策/风险 | 实施任务 | 必须看到的 RED 证据 |
| --- | --- | --- |
| control URL 与 platform data 配置分离 | 1、17、35 | platform URL 变化不能改变 control connection |
| Open 不自动 DDL | 2～6 | 空 schema 调 Open 后仍无 migration 表 |
| PostgreSQL/SQLite tenant baseline/adopt | 4～6 | legacy manifest/checksum/foreign key 失败时回滚 |
| 类型层禁止绕过 fence | 7、13 | AST/compile contract 对现有完整 Store 注入失败 |
| 跨实例 epoch fence | 8～10、31 | 旧连接/通知丢失后写提交失败 |
| 每请求持久 runtime snapshot | 11、12 | cache hit 仍读取 persistent revision |
| 异步资源 snapshot 与执行 epoch 分离 | 14、31 | migration 后旧 lease completion 失败、新 claim 成功 |
| profile kind/workspace 复合约束 | 3、16 | 直接 SQL 跨 workspace/kind 插入被数据库拒绝 |
| AI default/custom/disabled/reuse_chat | 16、19、23 | 每个合法/非法 mode 组合有测试 |
| concrete system defaults、无 NULL 漂移 | 17、36 | env 改变只创建 candidate，旧 binding/object 不变 |
| keyring 滚动轮换 | 15、35 | 仍有 old key 引用时移除失败 |
| SSRF/DNS rebinding/redirect | 18 | 第二次 physical dial 命中禁止 IP 并失败 |
| 用户级头像 | 20、22 | 同用户跨 workspace 可读、其他用户不可读 |
| 对象记录原 endpoint | 24、28A | binding 切换后旧对象仍从旧 endpoint 读取 |
| 上传 Idempotency-Key | 26 | same key/same hash 同 asset；same key/different hash 409 |
| 原格式与 SHA-256 语义 | 26、28 | 动画 GIF 不转 `.webp`、存储字节 hash 一致 |
| 媒体乱序投影 | 25、27 | note revision/event replay 不回退 links/watermark |
| 未发布 outbox 与图片删除竞争 | 25～27 | control links 为空但 tenant refs 存在时 DELETE 409 |
| delete barrier/watermark | 27 | watermark 未追平或 tenant 不可达时绝不删除 |
| 自动创建 database/schema/table | 29 | 仅 3D000 进入 maintenance DB；普通 Open 无 DDL |
| installation/namespace 同库识别 | 29、32 | alias/不同凭据同库拒绝 copy、改走 rebind |
| 目标 retired 残留策略 | 30 | 默认拒绝；显式 replace_retired 事务覆盖 |
| transition crash recovery | 31 | 每个持久 commit 后崩溃都不双活 |
| rollback 是新反向 job | 32 | 原 completed 不变且新 job 有 causal link |

---

## 5. 合并与提交策略

- 每个 Task 至少一个独立 commit；Task 13A/B/C、Task 31 的故障边界可以再拆小。
- commit 前必须附上该任务 RED 和 GREEN 的测试命令/结果摘要；不要只写“tests pass”。
- 每个 Phase 建议独立 PR 或至少独立可回滚 merge checkpoint。Phase 0～4 是基础设施 PR，不包含用户可见切换入口。
- 数据库 migration 一旦进入共享分支，不修改已发布文件；修正使用下一版本 migration。只有尚未发布的同一 PR 内可在测试证明下重写。
- feature flag 只控制新建/切换入口，不能关闭已有 endpoint 的解析和历史对象读取。
- 不在一个 commit 同时做 schema、后端 API 和完整前端页面；先 schema contract，再 repository/service，再 API，再 UI。

推荐 PR 切片：

1. PR-A：Tasks 1～6，provider lifecycle 与 baseline/adopt。
2. PR-B：Tasks 7～14，fence/runtime/global store removal。
3. PR-C：Tasks 15～19，keyring/profile/default/AI。
4. PR-D：Tasks 20～23，设置与头像。
5. PR-E：Tasks 24～28，媒体与编辑器；Task 28A 可独立延后。
6. PR-F：Tasks 29～33，数据库 transition。
7. PR-G：Tasks 34～36，观测、runbook、E2E 与灰度。

---

## 6. Definition of Done

只有同时满足以下条件，整体实施才算完成：

- [ ] 设计文档中的 control/data 分离、default 行为、profile/version、AI mode、媒体和迁移状态模型都有数据库约束与测试，而不只存在 handler 校验。
- [ ] `Provider.Open`、普通请求、resolver 和 worker 均不会执行 DDL。
- [ ] 生产代码中不存在 global active tenant store，也不存在 handler/service/worker 可取得的普通 tenant `Transact`。
- [ ] PostgreSQL/SQLite 通过共享 read/fence/baseline/media contracts。
- [ ] 两实例测试证明旧 epoch/旧 cache 无法在 fence 后写入。
- [ ] migration/rebind/projector/upload 在每个跨库持久边界崩溃后可幂等恢复。
- [ ] 数据库不存在时可按权限自动创建；表/schema 不存在时由显式 migration runner 自动创建。
- [ ] 用户不进行选择时始终解析到 provisioning 时的 concrete system endpoint，不读取漂移的 env/NULL default。
- [ ] tenant/MinIO/AI 故障不影响登录和设置修复入口；数据库/object 不静默回退。
- [ ] 头像是用户级；笔记图片是 workspace 级；跨用户/workspace 访问测试全部通过。
- [ ] 上传幂等、原格式、delete barrier、watermark 和 orphan GC 测试通过。
- [ ] keyring 轮换演练通过，secret 不出现在 API、日志、审计、metric、trace。
- [ ] 后端全量/race/integration/fault-injection、前端 unit/build/E2E 均绿色。
- [ ] fresh install、legacy PostgreSQL upgrade、legacy SQLite upgrade、回退/恢复 runbook 已实际演练。

---

## 7. 实施时禁止的捷径

- 不允许为赶进度保留 `repository.SetStore` 并在 resolver 前后切换全局 store。
- 不允许把完整 `storage.Store` 包装成另一个名字注入 handler/worker。
- 不允许把 fence 仅实现为 control state 检查；tenant anchor transaction 必须承担最终写拒绝。
- 不允许依赖 cache invalidation/通知保证正确性。
- 不允许在用户点击普通“保存”时切换数据库 binding。
- 不允许 migration 只比较 endpoint id/host，不读取 installation/schema identity。
- 不允许对象或数据库失败后静默使用平台默认。
- 不允许 DELETE 只查询 control `note_media_links` 后立即删对象。
- 不允许用 checksum 代替 Idempotency-Key 的请求语义。
- 不允许统一把 JPEG/PNG/GIF 命名或转码为 `.webp` 而不记录转换语义。
- 不允许在集成测试中连接或清理非 test 数据库/bucket。

本计划的基本节奏是：**一个可观察行为，一个可靠 RED，一个最小 GREEN，一个局部重构**。任何不能用这一循环安全落地的“大任务”，在实现前必须继续拆分。
