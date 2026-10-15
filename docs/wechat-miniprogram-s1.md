# S1 交付：工程骨架 + 协议地基

> 状态：**已通过验证** —— 19 个 golden 向量测试 + 5 个示例测试全绿，TS 实现与 Go 服务端字节级一致
> 前置决策：原生小程序 + TS ｜ 内存副本 + KV 水位 ｜ 完整复用 mobile-v2

---

## 1. 修改清单

### 1.1 新增（小程序工程，目录 `miniprogram/`）

| 文件 | 职责 |
|---|---|
| `package.json` | 工程声明；`npm test` 跑 vitest，`npm run typecheck` 跑 tsc |
| `tsconfig.json` | 小程序 TS 配置（strict、ESNext module、`wechat-miniprogram` 类型） |
| `project.config.json` | 微信开发者工具配置（appid 先用 `touristappid`） |
| `app.json` / `app.ts` / `app.wxss` | 入口；`app.json` 目前只注册 `pages/bind/bind` |
| `sitemap.json` | 索引配置 |
| `pages/bind/bind.{ts,json,wxml,wxss}` | 占位页，S5 实现绑定流程 |

### 1.2 新增（协议地基 `miniprogram/lib/`）

| 文件 | 职责 | 对应 Go 源码 |
|---|---|---|
| `sha256.ts` | 纯 TS SHA-256 + UTF-8 编码；**小程序无 Web Crypto 亦无 Node crypto**，必须自带 | `digest.go:59-66` |
| `jcs.ts` | RFC 8785 规范化：`lessUTF16` / `lessCodePoint` / 数字格式化 / 字符串转义 | `jcs.go` 全文 |
| `digest.ts` | `requestDigest` / `pageChecksum` / `manifestChecksum` + 三个语义集合排序 | `digest.go` 全文 |
| `uuid.ts` | 小写 UUID v4（服务端 `validUUID` 校验格式）+ 格式校验 | `envelope.go` validUUID |
| `time.ts` | `instant` / `date` / `time` 三种线格式；固定时区偏移表 | OpenAPI 时间格式 |
| `bignum.ts` | 十进制字符串比较（revision 等永不转 number） | `envelope.go:265-268` |
| `contract.ts` | 冻结常量：`contract_sha256`、epoch、scope 名、1 MiB 命令上限 + 契约自检 | `version.go`、`mobile_v2.go:15` |

### 1.3 新增（测试）

| 文件 | 职责 |
|---|---|
| `miniprogram/test/vectors.test.ts` | 消费共享 golden 向量（14 条）+ SHA-256 已知值 + 排序规则判别 |
| `miniprogram/test/examples.test.ts` | 可运行调用示例，输出即文档（见 §3） |
| `miniprogram/test/tsconfig.json` | 测试专用 TS 配置（含 node 类型） |

### 1.4 新增（共享契约资产，Go 侧）

| 文件 | 职责 |
|---|---|
| `backend/testdata/mobile-v2/miniprogram-vectors.json` | **14 条 golden 向量，Go 与小程序共用同一份文件** |
| `backend/internal/mobilev2contract/miniprogram_vectors_test.go` | Go 侧校验这些向量；另有一条测试强制"判别向量"必须存在 |

**后端业务代码零改动。** mobile-v2 协议、路由、存储均未触碰。

---

## 2. 为什么向量要双边共用

`miniprogram/test/vectors.test.ts` 与 `backend/internal/mobilev2contract/miniprogram_vectors_test.go` 读的是**同一个 JSON 文件**。任何一侧实现漂移都会立刻测试失败，不存在"只改一边悄悄对齐"的可能。

向量覆盖：命令信封 digest（6 条，含 entities / identity_mappings / field_paths / schedule_version 四种排序语义）、page checksum、manifest checksum、JCS 规范化（数字阈值、binary64 边界、转义、两套排序判别）。

---

## 3. 调用示例（真实输出，由 `npm test` 打印）

### 3.1 构造命令信封并计算 `request_digest`

```ts
import { requestDigest } from './lib/digest'

const envelope = {
  command_id: 'e5e5e5e5-e5e5-4e5e-8e5e-e5e5e5e5e5e5',
  origin_device_client_id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
  workspace_id: 'workspace-1',
  command_type: 'note.update',
  target: { entity_id: 'n-1', client_id: null },
  created_runtime_epoch: '8',
  expected: { entity_revision: { source: 'exact', value: '3' } },
  depends_on_command_id: null,
  supersedes_command_id: null,
  payload: {
    title: '晨间笔记',
    content: '今天要完成 #任务 与 😀 复盘',
    revision: '9007199254740993',
    flag: true, count: 42, ratio: 0.5,
  },
}

requestDigest(envelope)
// => sha256:a79b0b3d773a23eb8a3390828b8c1c245e079f65b26ecb3a10c356562621e0e9
//    与 Go 向量一致：true
```

注意 `requestDigest` 会**深拷贝**入参，删掉 `request_digest` 与 `forwarded_by_device_client_id` 后再规范化，不会污染原对象。

### 3.2 生成 command id

```ts
import { uuidV4, isUUID } from './lib/uuid'

uuidV4()                    // => 小写 v4，如 3f2a...-...-4...-8...
uuidV4(() => 0.5)           // => 80808080-8080-4080-8080-808080808080（测试可注入随机源）
isUUID('...')               // => true；大写形式返回 false（服务端只接受小写）
```

### 3.3 时间格式化

```ts
import { formatInstant, formatLocalDate, formatLocalTime, zoneOffsetMinutes } from './lib/time'

const at = Date.UTC(2026, 9, 12, 1, 34, 56, 789)
const offset = zoneOffsetMinutes('Asia/Shanghai')   // => 480

formatInstant(at)           // => 2026-10-12T01:34:56.789Z   （UTC，恒 3 位毫秒）
formatLocalDate(at, offset) // => 2026-10-12
formatLocalTime(at, offset) // => 09:34:56
```

### 3.4 大 revision 比较（不能转 number）

```ts
import { compareDecimalString, atLeast } from './lib/bignum'

const revision = '9007199254740993'
Number(revision)                                  // => 9007199254740992  ← 精度已丢
compareDecimalString(revision, '9007199254740992') // => 1   （字符串比较正确）
atLeast(revision, '9007199254740993')              // => true
```

### 3.5 契约自检（S3 同步入口会用）

```ts
import { CONTRACT_SHA256, ALL_SCOPES, SLIDING_SCOPES, assertContract } from './lib/contract'

CONTRACT_SHA256  // => c602dd87b040c706edb08da6aa8a8d99281e5ab5fae95b780b231774e595474c
ALL_SCOPES       // => ["iphone-task-core","iphone-content","iphone-occurrence-window"]
SLIDING_SCOPES   // => ["iphone-occurrence-window"]  ← 只有它要传 projection_time_zone

assertContract(serverSHA256)   // 不匹配抛 ContractMismatchError
```

服务端默认不强制 `minimum_client_build`（`service.go:60-62` 留 1），所以客户端必须自己比对 `contract_sha256`，这是唯一能拦住协议漂移的手段。

---

## 4. 验证方式

```bash
cd miniprogram
npm test          # 24 tests：19 向量 + 5 示例
npm run typecheck # tsc --noEmit，strict 模式

cd ../backend
go test ./internal/mobilev2contract/ -run MiniProgramVectors   # Go 侧校验同一份向量
```

当前结果：**小程序 24 passed ／ Go ok**。

---

## 5. 本阶段修正的两处认知偏差

1. **语义集合排序不是 UTF-16**。原以为全部按 RFC 8785 的 UTF-16 排序，实际 `entities` / `identity_mappings` / `field_paths` 用的是 `lessCodePoint`（码点），只有对象键用 `lessUTF16`。二者在 BMP 内一致，仅 surrogate pair 处分歧 —— 已用 U+E000 vs U+1F600 这对判别字符固化成向量。
2. **U+E000 不会被转义**。它是非控制字符（`>= 0x20`），JCS 原样输出，不是 `\ue000`。第一版测试断言就是栽在这。

---

## 6. 下一步（S2：后端鉴权）

按设计文档 §3 落地，不依赖 S1 已完成的部分之外的任何东西：

1. `mp_sessions` / `mp_bind_codes` 两表 + Postgres `0020_*.sql` + SQLite `ensureSQLiteMiniProgramSchema`
2. `storage/native.go` 的 `NativeStore` 加两个 repository；sqlite / postgres 各实现一份
3. `middleware/auth.go:44` 后插入 `restoreMiniProgramSession`；令牌加 `mp_` 前缀，`auth.go:145` 后让 watch 分支跳过
4. `/api/mp/*` 路由 + `config/auth.go` 新增 `WeChatMiniProgramConfig`
5. 绑定码落 DB（内存 store 多实例会失效）

需要你提供：小程序 AppID 与后端 HTTPS 备案域名（联调硬前置）。
