# FlowSpace 微信小程序设计方案

> 基线：mobile-v2 同步协议（契约指纹 `c602dd87b040c706edb08da6aa8a8d99281e5ab5fae95b780b231774e595474c`，来源 `backend/api/mobile-v2.sha256`）
> 技术选型（已确认）：原生小程序 + TypeScript ｜ 微信 code2session + 绑定码鉴权 ｜ 完整复用 mobile-v2 同步协议
> 状态：**待评审**，通过后冻结基线再进入实现

---

## 0. 结论摘要

| 维度 | 结论 |
|---|---|
| 后端 | 新增「小程序会话凭证」体系：DB 表 + 认证中间件分支 + `/api/mp/*` 路由 + 微信 code2session 客户端。**不改动任何 mobile-v2 协议代码** |
| 小程序端 | 四层：`mp-api`（传输）/ `sync`（快照+增量）/ `commands`（信封+JCS digest+幂等队列）/ `ui`（4 个 Tab） |
| 数据归属 | **服务端是唯一权威数据源**。小程序只持客户端投影副本，不落地为数据源 |
| 最大风险 | **客户端 JCS（RFC 8785）+ SHA-256 摘要**：小程序无 Web Crypto，需纯 TS 实现，任何一个字节差异即 422 |
| 次大风险 | **小程序生命周期**：切后台 5 秒挂起、随时被销毁，长同步可能被中断 |
| 工作量 | 后端约 1 个模块；小程序端约 15-20 个文件，其中 JCS/digest 与同步引擎是重头 |

---

## 1. 背景与范围

### 1.1 现状盘点

| 端 | 形态 | 数据通道 | 鉴权 |
|---|---|---|---|
| Web | React SPA（`frontend/`） | REST `/api/*` | `fs_session` Cookie |
| macOS | SwiftUI（`macos/`） | 同上 | 同上 |
| iPhone | **同一套 React 前端的响应式布局** | mobile-v2 `/api/mobile/v2/*` | Cookie（Simulator 走前端代理） |
| Apple Watch | 原生 | 同上 | `Authorization: Bearer <watch token>` |
| **微信小程序（新增）** | 原生 TS | mobile-v2 | **新增小程序 Bearer** |

现有移动端 4 个主 Tab（`frontend/src/components/layout/MobileNavigation.tsx:16-21`）：今日 / 任务 / 日历 / 笔记。

### 1.2 首期目标（V1）

1. 微信一键登录 + Web 端绑定码完成账号绑定
2. 完整 mobile-v2 离线同步：3 个 scope 全量快照 + 增量拉取 + 命令回写
3. 4 个 Tab 对齐 iPhone 端：今日、任务、日历、笔记
4. 核心写操作：笔记增删改、收件箱、任务创建/更新、occurrence 完成/跳过

### 1.3 非目标（V1 不做）

- 语音笔记录制与上传（`PUT /api/mobile/v2/voice-notes/:clientID/audio`）
- 转录任务（`transcription.*`）
- 学习路线图编辑（只读投影已含 `learning_roadmap` 等实体，但不做写命令）
- 项目生命周期命令（`project.activate/pause/complete/archive/restore`）
- 日程重排（`schedule.reschedule-this-and-following`、`occurrence.reschedule-only-this`）

### 1.4 为什么客户端必须持有投影副本（不是落盘，是内存态）

**服务端是唯一权威数据源**，所有数据都在后端数据库中。但 mobile-v2 是「离线优先复制协议」，写命令强制要求客户端提供它认为的当前 revision：

```go
// backend/internal/mobilev2command/envelope.go:27-31
type EnvelopeExpectedRevision struct {
    Source              string  `json:"source"`               // "exact" | "from_dependency_receipt"
    Value               *string `json:"value"`                // uint64 字符串，envelope.go:265-268 校验 ≥1
    DependencyCommandID *string `json:"dependency_command_id"`
}
```

- `source:"exact"` 要求客户端给出实体的当前 revision，服务端据此做乐观并发控制，不匹配则回冲突
- **mobile-v2 没有任何「查询某实体当前 revision」的在线接口**，只有 snapshot（分页全量）/ changes（增量）/ commands（写）。因此 revision 只能来自客户端自己持有的投影
- 绕法 `source:"from_dependency_receipt"`（`envelope.go:269-272`）把 revision 挂在前一条命令的回执上，能覆盖「离线新建」，但覆盖不了「打开小程序改一条已有笔记」

**结论**：客户端必须持有投影，但**不必须落盘**。据此采用 §4.2 的内存副本策略。

---

## 2. 总体架构

```
┌─────────────── 微信小程序（miniprogram/） ───────────────┐
│  ui/ 今日 · 任务 · 日历 · 笔记                            │
│    ↓ 读本地、写命令                                       │
│  store/ 本地实体库（文件系统分片 + 索引）                  │
│    ↑                                                      │
│  sync/ 快照分页 → 增量 changes → upsert                   │
│  commands/ 信封构造 → JCS digest → 幂等队列 → receipt      │
│  auth/ wx.login → code2session → 绑定码 → 令牌           │
└──────────────────────┬───────────────────────────────────┘
                       │ HTTPS（需备案域名，见 §6.5）
┌──────────────────────┴─── Go 后端（backend/） ───────────┐
│  /api/mp/*        登录 · 绑定码 · 绑定管理      【新增】    │
│  middleware/auth  新增小程序 Bearer 分支        【新增】    │
│  /api/mobile/v2/* capabilities/snapshot/changes/  【零改动】│
│                   commands/receipt                        │
└──────────────────────────────────────────────────────────┘
```

---

## 3. 后端改造设计

### 3.1 凭证模型（照抄 Apple Watch 范式）

现有 Watch 令牌是最近模板：`service.AuthorizeWatchDevice`（`internal/service/native.go:48-115`）→ `GenerateSessionToken()`（`internal/auth/session.go:18-24`，32 字节随机 + base64url）→ DB 只存 `HashSessionToken`（`session.go:26-36`，HMAC-SHA256(secret, token)）。

新增两张表，结构对齐 `watch_devices`（`db/migrations/postgres/0009_native_voice_watch.sql:1-24`）：

**`mp_sessions`（长期令牌）**

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 会话 ID |
| `workspace_id` / `user_id` | TEXT NOT NULL | FK → `workspace_members` |
| `openid` | TEXT NOT NULL | 微信 openid（同一 openid 可多 workspace） |
| `unionid` | TEXT NULL | 预留 |
| `token_hash` | TEXT NOT NULL UNIQUE | HMAC-SHA256 |
| `status` | TEXT CHECK(active/revoked) | |
| `expires_at` / `last_seen_at` / `created_at` / `updated_at` / `revoked_at` | BIGINT | |
| `client_build` | INTEGER NOT NULL DEFAULT 1 | 供日后强制升级用 |

**`mp_bind_codes`（一次性绑定码）**

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` / `workspace_id` / `user_id` | TEXT NOT NULL | Web 端已登录会话写入 |
| `code_hash` | TEXT NOT NULL UNIQUE | 6 位大写字母数字，用户手动输入 |
| `status` | TEXT CHECK(pending/consumed/expired/revoked) | |
| `expires_at` / `consumed_at` / `created_at` | BIGINT | TTL 10 分钟 |

> **为什么不用内存 store**：仓库已有 `MemoryNativeOAuthExchangeStore`（`internal/auth/native_oauth_exchange.go:29-99`）是一次性码的现成范式，但它是**进程内内存**，重启即失效、多实例不可用。绑定码关联「Web 用户 → openid」是业务状态，落库更稳。

### 3.2 认证中间件改动

插入点：`internal/middleware/auth.go:44` 之后（`RequiredSessionOrWatch` 内 watch 分支之后、401 之前）。

```go
if m.restoreMiniProgramSession(c) {   // 新增
    c.Next()
    return
}
```

**令牌识别（关键决策）**：`restoreWatchDevice`（`auth.go:143-147`）无条件把任何 `Bearer` 当 watch token 试。为避免每次小程序请求多查一次 `watch_devices` 表，**小程序令牌加固定前缀 `mp_`**，并在 `auth.go:145` 之后加一行提前返回：

```go
if strings.HasPrefix(parts[1], "mp_") { return false }
```

identity 注入照抄 `auth.go:167-176`：`SessionID` 留空、`DeviceID` 填 session ID（与 `mobile_v2.go:17-18` 的既有约定一致）。已核实 `mobilev2service` 只消费 `WorkspaceID` 与 `UserID`，`SessionID`/`DeviceID` 未被读取，留空安全。

### 3.3 路由

| 插入位置 | 分组 | 端点 | 语义 |
|---|---|---|---|
| `router.go:75`（authRoutes 之后、protected 之前） | **无鉴权** `api.Group("/mp")` | `POST /api/mp/login` | code → openid；返回 `bound` 状态或 `need_bind` |
| 同上 | 同上 | `POST /api/mp/bind-code/exchange` | 一次性码 + login ticket → 长期令牌 |
| `router.go:120`（watch 设备端点旁） | `protected`（Cookie 必需） | `POST /api/mp/bind-codes` | Web 端生成 6 位绑定码 |
| 同上 | `protected` | `GET /api/mp/bindings`、`POST /api/mp/bindings/:id/revoke` | 绑定管理 |

`/api/mobile/v2/*` **路由零改动** —— 它挂在 `nativeProtected`（`router.go:110`），中间件加分支即可触达。

### 3.4 配置

新增 `WeChatMiniProgramConfig` 到 `internal/config/auth.go`（`GitHubOAuthConfig` 之后，`auth.go:51` 附近），挂到 `AuthConfig`（`auth.go:69`）。

```
AUTH_WECHAT_MINIAPP_ENABLED / _APPID / _SECRET / _BIND_CODE_TTL / _SESSION_TTL_DAYS
```

code2session 客户端做成**接口**注入（仿 `MobileV2Service`，`mobile_v2.go:55-61`），便于测试替身。

### 3.5 存储与 migration

| 项 | 位置 |
|---|---|
| Postgres DDL | 新建 `db/migrations/postgres/0020_miniprogram_auth.sql` |
| SQLite DDL | 新建 `internal/storage/sqlite/miniprogram_migrations.go`，并在 `provider.go:161` 后注册 `ensureSQLiteMiniProgramSchema` |
| 接口 | `internal/storage/native.go:17-20` 的 `NativeStore` 加 `MiniProgramSessions()` / `MiniProgramBindCodes()`，同步更新 `NativeStoreFrom` 的 nil 校验（`:24`） |
| 实现 | 新建 `sqlite/miniprogram.go` / `postgres/miniprogram.go`（分别照抄 `sqlite/native.go:16-81`、`postgres/native.go:16-81`） |
| 装配 | `sqlite/provider.go:381-383,458-460`；`postgres/provider.go:343-345,420-422` |
| 模型 | 新建 `internal/model/miniprogram.go` |

### 3.6 后端测试

风格：标准库 `testing` + 表驱动，**无 testify**（`go.mod` 未引入）。需补：

1. `middleware/auth_test.go` 追加小程序 Bearer 用例（参照 `:67-103`）
2. `service/miniprogram_test.go`（参照 `service/native_test.go:246-269`）
3. `router/miniprogram_routes_test.go`（参照 `mobile_v2_routes_test.go:17-34` 的开关双断言）
4. `config/auth_test.go` 追加 `t.Setenv` 用例
5. 双方言契约测试 `storage/contracttest/miniprogram_contract_tests.go`

---

## 4. 小程序端设计

### 4.1 目录

```
miniprogram/
  app.ts · app.json · app.wxss · project.config.json · tsconfig.json
  pages/
    today/       今日：occurrence 时间轴 + 快捷完成
    tasks/       任务：按项目/状态分组 + 创建
    calendar/    日历：occurrence 月视图（滑动窗口渲染）
    notes/       笔记：列表 + 编辑
    bind/        绑定引导：展示输入码 / 一键登录
  lib/
    http.ts           wx.request 封装（Bearer、错误码归一化、重试）
    uuid.ts           v4 小写 UUID
    jcs.ts            RFC 8785 规范化
    sha256.ts         纯 TS SHA-256（hex 输出）
    digest.ts         request_digest 计算
    time.ts           2006-01-02T15:04:05.000Z / YYYY-MM-DD / HH:MM:SS
    bignum.ts         大整数字符串运算（revision 比较）
  sync/
    client.ts         capabilities / snapshot / changes
    engine.ts         同步编排、错误恢复、进度上报
    store.ts          内存实体投影（Map<entityType, Map<id, Entity>>）
    watermark.ts      同步水位 KV 持久化（令牌/cursor/scope_generation/epoch）
  commands/
    envelope.ts       信封构造 + expected 矩阵
    queue.ts          串行队列、幂等、退避、receipt 兜底
    types.ts          37 种命令的 payload 类型（TS 类型约束防漏字段）
  auth/
    login.ts          wx.login + code2session + 令牌持久化
```

### 4.2 存储策略：内存副本 + KV 水位

**决策（已确认）**：实体投影只放内存，KV 只存同步水位。不做文件系统持久化。

理由：小程序切后台 5 秒即挂起、随时可能被系统销毁，「跨会话离线」在这个形态下本来就靠不住，为它付文件分片那套复杂度（分片滚动 / 容量回收 / 索引一致性 / 文件被回收兜底）不划算。

| 层 | 载体 | 内容 | 量级 |
|---|---|---|---|
| 实体投影 | **进程内存** `Map<entityType, Map<id, Entity>>` | task / occurrence / note / inbox / project 等 | 随数据量，退出即丢 |
| 同步水位 | `wx.setStorageSync` | 令牌、`scope→cursor`、`scope_generation`、`runtime_epoch`、`projection_time_zone` | 几 KB |

**冷启动路径**：

```
读水位
 ├─ 有 cursor 且 scope_generation 匹配 → changes 增量拉取（通常秒级）
 ├─ 无 cursor / generation 不匹配 / 410 → 全量 snapshot
 └─ 全量期间内存构建投影，完成后写水位
```

**代价**：小程序被销毁后重开需重新拉一次（增量或全量）。UI 需给出"正在同步"态，且首屏允许先渲染上一次的水位外数据（无则空态）。

> 若日后要真正的离线，只需把 `store.ts` 换成文件存储实现，上层 `engine.ts` 接口不变 —— 此处预留了升级路径。

### 4.3 同步引擎

```
start():
  caps = GET /capabilities
  校验 contract_sha256 == 基线常量，否则提示升级
  保存 runtime_epoch
  for scope in [iphone-task-core, iphone-content, iphone-occurrence-window]:
     本地 scope_generation 与 caps 不一致 → 清库重来
     fullSnapshot(scope)

fullSnapshot(scope):
  循环 GET /snapshot?scope&page_token（滑动 scope 带 projection_time_zone）
  upsert entities（按 entity_type+entity_id，deleted_at 非空即删除）
  保存 snapshot_cursor

pullChanges(scope):
  GET /changes?scope&cursor&[projection_time_zone]
  has_more → 循环
  upsert + 保存 next_cursor
```

**错误恢复映射**（照 `mobilev2service/service.go` 实测行为）：

| HTTP / code | 动作 |
|---|---|
| 410 `resync_required` | 清该 scope 本地数据 + cursor，重跑 fullSnapshot |
| 409 `projection_refresh_required` | 仅滑动 scope：丢弃窗口游标，重跑该 scope 快照 |
| 422 `projection_refresh_required` | 补 `projection_time_zone` |
| 503 | 指数退避重试 |
| 401 | 清令牌，回登录页 |

滑动窗口注意：`scope_generation` 含**当地日期**（`service.go:356-372`，如 `2026-10-12@Asia/Shanghai:runtime:1`），跨日或跨时区必须重新快照；`iphone-occurrence-window` 窗口是 **-30 天 ~ +91 天**。

### 4.4 命令引擎

信封 11 个必填字段（`mobilev2command/envelope.go:117-142`），其中：

- `command_id` / `origin_device_client_id`：**小写 UUID**
- `created_runtime_epoch`：字符串大整数，须等于 capabilities 的 `runtime_epoch`
- `expected`：键集合必须与命令类型**精确匹配**（`engine.go:291-316`，以实现为准，**不要照 OpenAPI 的 `x-command-revision-matrix`，它与实现不一致**）

| 命令 | 必填 expected 键 |
|---|---|
| `note.create` / `inbox.create` / `voice.create` / `project.create` | 无 |
| `note.*`其余 / `inbox.*`其余 | `entity` |
| `task.create` | `project` |
| `task.update` | `schedule`, `task` |
| `task.publish/pause/resume/cancel/restore/archive` | `occurrences`, `schedule`, `task` |
| `occurrence.*` | `occurrence`, `schedule`, `task` |

`request_digest` = `"sha256:" + hex(SHA256(JCS(信封 - request_digest - forwarded_by_device_client_id)))`，且 `entities`/`identity_mappings`/`field_paths` 三个数组需语义排序（`mobilev2contract/digest.go:13-26,68-93`）。

**提交流程**：

```
POST /commands
 ├─ 200 → 写回 identity_mappings（client_id→entity_id）、affected_revisions
 ├─ 409 receipt_history_ambiguous → 退避重试（幂等安全）
 ├─ 409 stale_runtime_epoch → 重拉 capabilities，用新 epoch 重建命令
 ├─ 413 / 422 → 不重试，标记失败（属客户端 bug）
 └─ 网络超时 → GET /commands/{originDeviceClientID}/{commandID}/receipt
                 ├─ 200 → 已提交
                 └─ 404 → 可安全用同一 command_id 重发
```

- 幂等键 = `(workspace_id, origin_device_client_id, command_id)`；**换 digest 必须换 command_id**，否则 422（`sql_ledger.go:146-149`）
- 冲突（revision 不匹配）→ receipt `status=conflict`，HTTP 仍是 200；mobile-v2 **无服务端冲突解决**，客户端需自行提示用户或丢弃重发
- `wx.request` 并发上限 10，命令队列严格串行

### 4.5 UI 对齐

| Tab | 数据源 | 说明 |
|---|---|---|
| 今日 | `iphone-occurrence-window` 中 `planned_date == 今天` 或 `execution_status ∈ (open,active)` 且无计划时间 | 滑动窗口渲染，跨日触发重快照 |
| 任务 | `iphone-task-core` 的 `project` + `task` | 按 `lifecycle_status` 分组 |
| 日历 | `iphone-occurrence-window` | 月视图，窗口 ±30/91 天 |
| 笔记 | `iphone-content` 的 `note` | **协议内无 folder 实体**，仅 `note.folder_id` 字符串，需自行聚合 |

---

## 5. 关键难点与对策

### 5.1 JCS + SHA-256（最高风险）

小程序无 `crypto.subtle`（非 WebView 环境）。需纯 TS 实现：

- **数字序列化**必须对齐 Go（`mobilev2contract/jcs.go:182-209`）：`0`→`"0"`；`1e-6 ≤ |x| < 1e21` 用最短定点表示；否则科学计数法（`1e+30`、`1e-7`）。JS `Number.prototype.toString` 语义基本等价，但**必须逐 case 对齐**
- **对象键排序**按 **UTF-16 码元**（`jcs.go:211-224` lessUTF16）
- **但语义集合排序按 Unicode 码点**（`digest.go:144-157` lessCodePoint）—— `entities` / `identity_mappings` / `field_paths` 三个数组用的是 `lessCodePoint`，**不是** `lessUTF16`。这是服务端对 RFC 8785 的偏离，两套规则共存
  - 判别对：U+E000（单个 UTF-16 单元）与 U+1F600（代理对 D83D DE00）。UTF-16 序下 emoji 在前，码点序下 U+E000 在前。已在共享向量中固化两种 case（`jcs-object-key-utf16-order` 与 `request-digest-entities-codepoint-order`）
- **字符串转义**按 JCS：非 ASCII **原样输出不转 `\u`**（Go `jcs.go:154-180`），这条与直觉相反；仅 `< 0x20` 转小写 `\u00xx`
- SHA-256 纯 TS 实现（`lib/sha256.ts`），小程序无 Web Crypto 亦无 Node crypto

**对策**：JCS + SHA-256 各配一套**向量测试**（golden vector），与 Go 侧 `mobilev2contract/jcs_test.go`、`digest.go` 的真实输出比对。这是唯一能证明对齐的手段，列为 P0。

### 5.2 大整数

所有 revision / sequence / epoch / duration_ms / audio_size 都是**十进制字符串**（服务端 `decoder.UseNumber()` + `BigIntString`）。JS `number` 会丢精度 → 一律用 `string` 承载，比较时实现 `cmpBigIntString`。

### 5.3 严格字段校验

服务端 `DisallowUnknownFields`（`types.go:406`、`envelope.go:296`）：**多余字段直接 422**。对策是所有 payload 用 TS 类型 + 构造时显式挑字段，禁止直接 `JSON.stringify(整个对象)`。

### 5.4 时区

滑动 scope 每次请求都要带同一个 IANA `projection_time_zone`。小程序无 IANA 时区库 → 内置常用中国时区（`Asia/Shanghai`，偏移固定 +08:00，无夏令时）即可覆盖 V1。

### 5.5 同步中断与恢复

小程序切后台 5 秒挂起、随时被销毁，长同步会被打断。因为采用内存副本（§4.2），恢复策略比落盘方案更关键：

1. **水位写入时机**：每拉完**一页**就 `wx.setStorageSync` 写一次 cursor，不要等整个 scope 拉完。中途被杀，下次从该页之后的增量续拉，不重头开始
2. **幂等保证**：`upsert` 按 `entity_type + entity_id`，重复拉取同一页无副作用
3. **首屏策略**：内存空时先展示"正在同步"，同步完再渲染；不做假数据骨架
4. **前台恢复**：`App.onShow` 检测 `runtime_epoch` 是否变化，变了就触发一次增量
5. **跨日**：`scope_generation` 含当地日期，跨日必须重跑滑动 scope 快照 —— `onShow` 时比对日期

### 5.6 部署约束

微信要求 `request` 合法域名：**HTTPS + 已备案域名**，且开发环境需在小程序后台配置。本地 `127.0.0.1` 仅在不校验合法域名的开发者工具/真机调试模式下可用。这是联调前必须解决的前置条件。

---

## 6. 方案优缺点与改进点

### 6.1 优点

1. **后端协议零改动** —— mobile-v2 已冻结且有 SHA-256 契约守卫（`mobilev2contract/openapi_test.go:17-34`），小程序只是新客户端，不引入协议分叉
2. **与 iPhone/Watch 行为一致** —— 同一份快照/命令语义，跨端数据天然一致
3. **会话内离线可用** —— 一次同步后，在网络断开期间仍可浏览与写命令（命令入队，恢复后按幂等键提交），符合小程序"即用即走"场景（快速记一笔、勾一个任务）
4. 令牌复用成熟的 Watch 范式，hash 存储 + 可吊销 + `last_seen_at` 审计
5. 内存副本策略把最复杂的一块（文件分片/容量/索引一致性）从 V1 中移除，且 `store.ts` 接口不变，日后可无痛换成文件存储

### 6.2 缺点

1. **客户端实现成本高**：JCS/SHA-256/大整数/命令信封/幂等队列全部要手写，比"调 REST"重一个数量级
2. **小程序生命周期限制**：切后台 5 秒挂起，长同步可能被中断；被销毁后重开需重新拉一次（增量或全量），首屏有"正在同步"态
3. **无跨会话离线**：内存副本策略下退出即丢数据，弱网/断网场景只有会话内可用
4. **部署门槛**：HTTPS 备案域名是硬约束

### 6.3 建议的改进点

1. **先做 digest 对齐验证再写业务**：用一个最小脚本把 Go 侧真实信封喂给 TS 实现比对，通过再往下走，避免后期返工
2. **V1 命令集克制**：只做 note/inbox/task/occurrence 共 12 条命令（见 §1.2），其余留接口不实现
3. **契约常量做编译期校验**：`contract_sha256` 与 capabilities 返回比对，不等则阻断同步并提示升级（服务端不强制 `minimum_client_build`，客户端必须自检 —— `service.go:60-62` 默认 1，且 `cmd/server/main.go` 未覆盖）
4. **绑定码用 DB 不用内存**：多实例部署下内存方案会失效
5. 命令队列失败项保留在内存队列并暴露"重试/丢弃"，不要让失败命令静默丢失
6. **水位按页写**：不要等整个 scope 拉完才写 cursor，否则被系统杀掉要重头拉（见 §5.5）
7. **滑动 scope 单独存水位**：`iphone-occurrence-window` 的 generation 含日期，与两个 stable scope 分开管理，避免跨日误判

---

## 7. 实施计划

| 阶段 | 内容 | 产出 |
|---|---|---|
| **S1 地基** | 小程序工程骨架 + TS 配置 + `lib/`（uuid/jcs/sha256/digest/time/bignum）+ **digest golden 向量测试** | 可通过 Go 侧比对的 digest 实现 |
| **S2 后端鉴权** | 两张表 + migration + 存储实现 + 中间件分支 + `/api/mp/*` + 配置 + 测试 | `POST /api/mp/login` 可拿到令牌 |
| **S3 同步** | `auth/login.ts`、`sync/client.ts`、`sync/store.ts`（内存投影）、`sync/watermark.ts`、`sync/engine.ts` | 登录后可拉全量快照构建内存投影，重开走增量 |
| **S4 命令** | `commands/envelope.ts`、`queue.ts` + receipt 兜底 | 能成功创建/更新一条笔记 |
| **S5 UI** | 4 个 Tab + 绑定页 | 可用版本 |
| **S6 加固** | 中断续传、跨日重快照、冲突提示、错误上报 | 可交付 |

每阶段结束出一个 checkpoint，你确认"通过"后再进下一阶段。

**已确认决策**：原生小程序 + TS ｜ code2session + 绑定码鉴权 ｜ 完整复用 mobile-v2 ｜ 内存副本 + KV 水位。

---

## 8. 待确认项

| # | 事项 | 影响 |
|---|---|---|
| 1 | 小程序 AppID（正式/测试号）与后端 HTTPS 备案域名 | 联调前置条件 |
| 2 | 新表放主库还是租户库 | 建议跟随 `watch_devices` 放主库 |
| 3 | 小程序令牌是否等价于 watch 令牌（可访问全部 `nativeProtected` 路由） | 当前设计：等价（改 `RequiredSessionOrWatch`） |
| 4 | 绑定码形态：6 位码手动输入，还是 Web 端生成二维码让小程序扫 | 影响是否需要额外"待绑定"状态字段；二维码体验更好但多一套轮询 |
| 5 | V1 命令集是否够用 | 见 §1.2 / §1.3 |
| 6 | 小程序项目目录名：`miniprogram/`（仓库根）是否可接受（仓库已有空的 `all_note_mobile/`） | 目录归属 |
