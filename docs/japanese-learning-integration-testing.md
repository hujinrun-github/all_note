# 日语学习联动测试

本页说明两个仓库联合验证的方法。测试使用临时 HTTP server、独立 PostgreSQL 测试库及临时 SQLite 文件，不启动或修改日常使用的 FlowSpace/Japanese 服务，也不访问生产数据。

## 准备独立测试数据库

配置两个不同的 PostgreSQL 测试数据库。日语数据库名必须以 `_test` 结尾或包含 `_test_`，并且不同于 `DATABASE_URL` 的应用库；每个测试通过 `internal/testdb` 创建、清理独立 schema，不清空 `public`。

例如，本次本机验证创建了全新的 `flowspace_integration_test` 与 `japanese_flowspace_test`。本机已运行的 PostgreSQL 位于 `127.0.0.1:5432`，使用当前本机角色。此配置仅用于隔离测试，不替代 AGENTS.md 中的标准 v2 应用启动配置。

下列环境变量请填写自己的测试连接；有密码时通过未提交的本地配置或安全环境变量提供。

```sh
export FLOWSPACE_TEST_DATABASE_URL='postgres://LOCAL_ROLE@127.0.0.1:5432/flowspace_integration_test?sslmode=disable'
export JAPANESE_TEST_DATABASE_URL='postgres://LOCAL_ROLE@127.0.0.1:5432/japanese_flowspace_test?sslmode=disable'
export FLOWSPACE_JAPANESE_REPO='/absolute/path/to/japanese-learning-app'
```

数据库必须事先存在。日语项目也可通过其现有 `make test-db` 创建指定测试库，务必在 make 命令行显式覆盖 `DATABASE_URL`、`DATABASE_URL_TEST`，以免 `.env.local` 中的远端配置覆盖测试环境。

## 跨仓库真实 HTTP 测试

在 `all_note/backend/` 运行：

```sh
TZ=UTC FLOWSPACE_REQUIRE_POSTGRES_TESTS=true \
go test ./internal/japaneseintegration \
  -run '^TestJapaneseCrossProjectHTTP$' -count=1 -v
```

该测试会调用相邻仓库中的 `go test ./internal/module/flowspace -run '^TestFlowSpaceCrossProjectSource$'`。来源测试数据库由父测试显式传入，不使用相邻仓库的默认应用数据库。

完整验证链路为：

1. 创建隔离的 FlowSpace control/tenant 数据和当前日期的 v2 重复任务，分别验证 SQLite、PostgreSQL 两种接收端。
2. 日语项目使用真实 PostgreSQL 迁移和隔离 schema，创建测试学习计划，通过带 JWT 的真实 HTTP 接口进行配对。
3. 来源 outbox worker 用签名 HTTP 传递计划目录，FlowSpace 实际 handler 接收后，通过实际管理接口关联来源任务与目标任务。
4. 来源通过实际学习计划 HTTP 接口完成当天任务，重复请求不会增加第二条完成事件。
5. FlowSpace 持久化第一条事件后，测试刻意丢弃 HTTP 应答，模拟接收成功而发送端无法确认的情况。
6. 重建来源 worker，通过原有持久化队列重试。同一业务 event ID 保持不变，HTTP nonce 更新，接收端只产生一次完成效果。
7. 验证 FlowSpace 当天执行实例为 `done`、次日仍为 `open`，只有一条 inbox 与 execution log，mobile-v2 change batch 已更新；来源最终通过签名查询读到 `applied` 回执。

只在没有设置 `FLOWSPACE_JAPANESE_REPO` 时跳过此可选联合测试。一旦明确启用，缺少日语测试库、仓库路径无效、PostgreSQL 无法连接或任何断言失败都会使测试失败；不会静默跳过已要求的数据库验证。

本测试覆盖真实传输和状态提交，不调用外部 ASR、TTS、AI 或 MinIO。听力完整训练自动打卡及各类撤销、乱序、权限、完成条件冲突由两边各自的后端测试覆盖。

## 各仓库完整回归

日语项目根目录：

```sh
make test \
  DATABASE_URL='postgres://LOCAL_ROLE@127.0.0.1:5432/japanese_application_unused?sslmode=disable' \
  DATABASE_URL_TEST="$JAPANESE_TEST_DATABASE_URL"
```

`DATABASE_URL` 在此仅用于校验应用库与测试库不同，测试不会连接该示例应用库。日语网页另在 `front/react/` 运行 `npm test` 和 `npm run build`。

FlowSpace 在 `backend/` 运行：

```sh
TZ=UTC FLOWSPACE_REQUIRE_POSTGRES_TESTS=true \
go test -p 1 ./cmd/... ./internal/... -count=1
```

后端测试进程使用 UTC，避免既有数据库时间断言受运行机器本地时区影响；业务学习日期仍由计划中的 IANA 时区解释，不因此改为 UTC 学习日。

FlowSpace 前端任意改动后，仍须在 `frontend/` 执行：

```sh
npm run test:regression
```

该命令包括 lint、全部 Vitest、生产构建和桌面 Chrome、Android Chrome、iPhone WebKit 的 UI 测试。UI suite 使用 v2 API fixtures，不等同于上述跨仓库真实 HTTP 验证。

本次日语设置页也通过现有开发服务上的 Playwright fixture 验证了过期配对码、连接、待送达/已送达/已处理区别、重试和断开连接，覆盖桌面、Android 和 iPhone。该检查未使用真实账号凭据。
