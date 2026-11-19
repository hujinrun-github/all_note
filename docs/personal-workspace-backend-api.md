# 个人工作台后台接口：第一批

本批提供统一的已认证工作区上下文，以及首页聚合、任务计数和项目摘要查询。现有任务写入、内容权威数据源和 mobile-v2 同步协议保持原有语义。

## 数据来源与迁移边界

| 入口或区域 | 认证来源 | 当前业务权威源 | 本批处理 |
| --- | --- | --- | --- |
| Web 任务、项目与日历 | 控制库会话、工作区成员资格 | 工作区绑定的 v2 租户数据源 | 共用持久运行时校验及 epoch 约束 |
| 首页计划指标、执行、项目摘要 | 同上 | 同一工作区 v2 租户只读快照 | SQL 聚合，有限列表，明确读取边界 |
| Web 笔记与首页最近笔记 | 同上 | 现有平台内容 Store | 明确标记 `platform_compatibility`，本批不重定向 |
| mobile-v2 内容与同步 | 控制库会话或已有受限原生身份 | 工作区租户运行时 | 保留现有 snapshot/change/receipt 原子性和协议 |
| 旧内容同步、导入及作业 | 既有业务范围 | 现有显式或兼容 Store | 后续按模块完成回填与对账后切换，不能因重构解析器提前改库 |

上述矩阵描述源码调用边界，不是对生产绑定与数据内容的远程检查结果。请求中的 `workspace_id` 不能选择工作区；新查询接口拒绝这个参数。实际范围只来自验证后的身份与一致的工作区 scope。

运行时解析失败、租户 epoch 与控制状态不一致、模型不可用时返回明确错误，不能回落 legacy 或默认数据库。新建临时测试租户可以按既有迁移直接 provision v2；已 cutover 的工作区不重新运行 cutover。

## 公共查询约定

- 所有新查询要求正常登录、已完成密码更新，且工作区可用。
- `date` 为真实日历日期 `YYYY-MM-DD`，`timezone` 为有效 IANA 时区，例如 `Asia/Shanghai` 或 `UTC`；`Local` 不接受。
- 当天、周一开始的本周和自然月均按指定本地日期确定，精确时间区间采用 `[from,to)`。全天日期与精确时间不混为一种时间值。
- 响应沿用 `{ "data": ... }` 包装。新区域失败可明确标记不可用，不能用空列表或 0 隐藏失败。
- 未支持的参数、同名参数重复提交、显式空 limit、无效日期/时区或超出范围的 limit 均返回 400。调用方不能通过附加查询参数选择其他工作区。
- `runtime_epoch` 是本次使用的工作区运行时版本，不代替实体的乐观 revision。
- `generated_at` 记录响应生成时间。`read_consistency=planning_snapshot` 只描述计划区域的共同 SQL 快照；兼容笔记区域有独立 `as_of`。

错误状态：

| HTTP 状态 | 错误码 | 含义 |
| --- | --- | --- |
| 401 | `UNAUTHENTICATED` / `unauthorized` | 既有登录中间件拒绝未登录请求，或应用层发现身份、工作区 scope 缺失或不匹配 |
| 403 | 既有认证错误码 | 账号被禁用或尚需更新密码，沿用现有认证规则 |
| 400 | `invalid_request` | 查询参数、日期、时区或 limit 无效 |
| 404 | `project_not_found` | 项目不存在或不在已认证工作区 |
| 503 | `workspace_unavailable` | v2 运行时、权威计划数据源或 epoch 不可用 |

路由始终注册在正常认证边界后。未配置 v2 运行时时，新查询在登录后返回 503；未登录请求仍为 401。

## 首页聚合

```http
GET /api/workspace/overview?date=2026-11-19&timezone=Asia%2FShanghai
```

可选参数：

| 参数 | 默认值 | 允许范围 |
| --- | --- | --- |
| `today_limit` | 8 | 1–50 |
| `project_limit` | 3 | 1–20 |
| `notes_limit` | 3 | 1–10 |

`data` 包含：

| 字段 | 含义 |
| --- | --- |
| `workspace_id`、`task_model_version`、`runtime_epoch` | 已认证工作区、任务模型 2、解析得到的 epoch |
| `date`、`timezone`、`generated_at`、`read_consistency` | 查询窗口与读取边界 |
| `facets` | 轻量计数，见下表 |
| `today` | 当天未终结执行的有限列表，现有 occurrence 字段并附 `project_name` |
| `projects` | 有限项目列表，现有 project 字段并附 `summary` |
| `notes` | 独立可用性、来源和有限最近笔记摘要 |
| `partial` | 是否存在明确的非核心区域失败 |

`today` 与 `projects` 数量受 limit 约束，`facets` 仍统计完整匹配范围。客户端不能用有限列表长度代替计数。

笔记区域：

```json
{
  "state": "available",
  "authority": "platform_compatibility",
  "as_of": "2026-11-19T04:00:00Z",
  "items": [{"id": "note-id", "title": "笔记标题", "updated_at": 1795060800}]
}
```

笔记源故障时，核心计划区域仍可返回 200，`partial=true`，`notes.state=unavailable`、`notes.items=null`，并提供稳定错误码。真正没有笔记时则是 `state=available`、`items=[]`、`partial=false`。

## 任务视图计数

```http
GET /api/task-workbench/facets?date=2026-11-19&timezone=Asia%2FShanghai
GET /api/task-workbench/facets?date=2026-11-19&timezone=Asia%2FShanghai&project_id=project-id
```

可按已认证工作区内的项目筛选。不存在或不属于当前工作区的项目返回 404。

| 计数 | 口径 |
| --- | --- |
| `today` | 与本地当天重叠的未终结执行 |
| `week`、`month` | 本周、自然月内开始的未终结执行 |
| `overdue` | 指定本地当天开始前已截止且尚未终结的执行 |
| `unscheduled` | 没有计划日期和开始时间的未终结执行 |
| `inbox` | 系统收件箱项目中的未终结执行 |
| `completed_today`、`completed_week` | 按实际完成时间处于当天或本周区间的 done 执行 |
| `completed_total` | 历史 done 执行总数 |
| `draft` | 草稿任务定义数，单独计数 |
| `recurring` | 未终结的重复任务执行实例数，计数单位仍为执行实例 |

未终结执行指 `open/active/blocked`，不是把任务定义状态与执行状态混合成一个状态。行动计数只包括 `active/paused` 任务及 `planning/active/paused` 项目下的这类实例；草稿、完成、取消和归档任务不作为开放行动。完成计数读取历史 done 事实，保留已完成或已归档项目中的执行记录。这是新增聚合接口的明确口径，现有 occurrence 列表契约保持原样。

### Web 任务工作台口径

任务工作台的标签数量全部读取 facets，并与内容请求使用同一个本地日期、IANA 时区和可选 `project_id`。默认“本周”保留旧 `view=upcoming` 深链接兼容，日期为周一零点至下周一零点；新增“本月”采用自然月。今天使用当天重叠区间；逾期以本地当天零点为界；已完成采用全部历史 done 事实，不再隐含最近 30 天窗口。重复与无日期计数仍按执行实例，草稿单独按任务定义计数。

标签反映所选项目的完整视图数量；优先级、执行状态和日期范围只细化当前内容，结果区明确显示“当前显示 N 项”。“未来 7 天”是当前视图与从本地今天到第七天结束区间的交集，不会改变本周标签含义。日期比较使用指定时区的日历边界与瞬时重叠，不从 UTC 时间字符串截取日期。

页面只请求当前视图的内容列表，草稿仅在草稿视图请求；任务元数据按当前内容关联 ID 读取，详情深链接按 ID 读取，不下载完整任务定义列表凑统计。facets 失败时显示计数错误并保留未知数量，不以内容条数或 0 替代；内容仍可独立加载和操作。

## 项目摘要与概览

```http
GET /api/projects?include=summary&date=2026-11-19&timezone=Asia%2FShanghai
GET /api/projects?include=summary&kind=standard&status=active&date=2026-11-19&timezone=Asia%2FShanghai
GET /api/projects/project-id/overview?date=2026-11-19&timezone=Asia%2FShanghai
```

`include=summary` 继续支持原项目类型、周期和状态筛选。省略 `include` 的现有 `/api/projects` 请求保留原响应和参数语义，不强制要求新日期参数或增加摘要。

项目概览的 `data` 包含工作区、epoch、日期、时区、生成时间和附带 `summary` 的 `project`。摘要字段：

| 字段 | 含义 |
| --- | --- |
| `completed_task_count` | 已完成任务定义数 |
| `total_actionable_task_count` | 可计入任务完成比例的定义总数，不含 draft/cancelled/archived |
| `open_occurrence_count` | 该项目尚未终结的执行数量 |
| `blocked_occurrence_count` | 其中受阻的执行数量 |
| `has_open_ended_recurrence` | 是否存在没有结束日期的当前重复计划 |
| `next_occurrence` | 下一项可执行安排的 occurrence 摘要；没有则为 null |
| `completion_percent` | 分母明确时的完成百分比；无限期重复或分母未建立时为 null |

任务完成数量和执行数量为两个维度。完成每日任务的一次执行不能把整个重复任务记作已完成，也不能把无限期重复计划显示为确定的整体完成百分比。

`next_occurrence` 以请求日期的本地零点为参考，排除未安排项及在该时刻已结束的安排。按计划日期排序，同日全天安排先于固定时间，固定时间按开始时刻排序，最后以稳定 ID 消除并列；不会因服务器当前时刻不同而改变同一日期查询的参考边界。今日行动列表独立采用固定时间优先，再按优先级和用户顺序展示日期任务。

## 验证与兼容

第一批 HTTP 测试使用临时 SQLite 控制/租户数据，覆盖独立认证与业务来源、同 ID 的跨工作区隔离、有限列表与完整计数、项目摘要一致性、笔记兼容来源、局部失败、日期与 limit 校验、失效 epoch，并通过实际 v2 完成命令验证聚合结果随规范数据变化。PostgreSQL 与 SQLite 查询语义由相同存储契约约束，实际执行结果以本批验证记录为准。

现有 mobile-v2 协议字段矩阵、收据、快照与变更流不因新增 Web 查询而改变。现有 Web `/projects` 基本响应也单独验证兼容。第一批不切换内容存储、不创建实际工作区或重跑 cutover。

2026-11-19 本地验证记录：

- `make format-check lint test-openapi` 通过，显式检查 mobile-v1 与 mobile-v2 契约。
- `make test-unit` 通过，覆盖全部后端包及 SQLite 契约。
- 在独立、临时 PostgreSQL 17 数据库上，以 CI 对齐的 `TZ=UTC` 环境执行 `make test-contract-postgres` 通过，包含 PostgreSQL 存储与迁移测试。
- 两种数据库共用七个工作台契约场景，覆盖同 ID 隔离、统计、有限列表、状态与项目筛选、下一次执行、DST、微秒边界及失效 epoch；相关存储、上下文和 HTTP 流程的 race 测试通过。
- 服务端构建与 `git diff --check` 通过。

前端接入已完成：

- 首页使用 `/api/workspace/overview` 的完整计数、有限今日预览、项目摘要和最近笔记；切换其他标签时只加载当前范围的执行列表。选择任务时才按 ID 加载任务定义，预览外的执行深层链接也按 ID 读取。
- 项目列表使用 `/api/projects?include=summary`，项目详情使用 `/api/projects/:id/overview`。完成比例和待执行数量采用服务端摘要；内容列表仅服务于任务、日程展示或项目批量操作，不用于重新计算摘要。
- 任务工作台采用 `/api/task-workbench/facets` 的视图完整计数；默认本周、自然月、今日、逾期和历史完成的日期口径按上文统一，内容按当前视图加载。
- 侧栏收件箱角标使用 `/api/task-workbench/facets`，不再为了角标读取项目目录和收件箱执行列表。相关任务、执行和项目命令统一使工作区查询缓存失效。
- 笔记兼容来源不可用时，仅最近笔记区域显示错误；整个聚合查询失败时显示明确错误，不以零计数或空列表替代。`completion_percent=null` 保留未知或持续推进的含义，不显示虚构的 0%。

2026-11-19 前端完整质量门禁 `npm run test:regression` 通过：ESLint、56 个文件中的 376 个 Vitest 测试、TypeScript 与生产构建通过；桌面 Chrome、Android Chrome、iPhone WebKit 的 Playwright UI 共 49 项通过，8 项设备不适用用例按配置跳过。没有部署。当前标准本机 v2 数据库和运行密钥文件缺失，因此后端验证使用真实 Go HTTP 调用链及独立临时控制/租户数据库；前端浏览器验证使用隔离 v2 HTTP 夹具，不将界面演示数据当作真实运行环境验收。


任务工作台批次验证补充：2026-11-19 在当前共享目录运行 `npm run test:regression` 通过，覆盖 lint、58 个文件中的 413 个 Vitest 用例、TypeScript 和生产构建，以及三端 UI 的 64 项通过 / 8 项设备限定跳过。共用日期工具与服务端窗口分别覆盖跨午夜、自然周月、夏令时跳时和重复午夜；日期相关 `make format-check lint test-unit` 通过。浏览器仍使用隔离 v2 HTTP 夹具，本批未部署。
