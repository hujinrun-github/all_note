# FlowSpace 服务端口与存储隔离

日常开发、调试和 Codex 修改验证必须使用测试服务。正式服务只用于真实数据验证、演示或发布前检查。

| 环境 | 前端入口 | 后端 API | 后端环境 | SQLite 文件 | 启动命令 |
| --- | --- | --- | --- | --- | --- |
| 测试 | `http://127.0.0.1:15199` | `http://127.0.0.1:18080/api` | `FLOWSPACE_ENV=test` | `backend/flowspace.test.db` | `make dev` 或 `make dev-test` |
| 正式 | `http://127.0.0.1:5199` | `http://127.0.0.1:8080/api` | `FLOWSPACE_ENV=prod` | `backend/flowspace.db` | `make dev-prod` |

## Tailscale 外网入口

| 环境 | 外网域名 | 本地公开前端 | 后端 API | SQLite 文件 | 启动/映射命令 |
| --- | --- | --- | --- | --- | --- |
| 测试 | `https://tylerhu-1.king-shiner.ts.net/all-note-test/` | `http://127.0.0.1:15198/all-note-test/` | `http://127.0.0.1:18080/api` | `backend/flowspace.test.db` | `make start-test-tailscale-frontend` + `make serve-test-tailscale` |
| 正式 | `https://tylerhu-1.king-shiner.ts.net/all-note/` | `http://127.0.0.1:5198/all-note/` | `http://127.0.0.1:8080/api` | `backend/flowspace.db` | `make start-tailscale-frontend` + `make serve-tailscale` |

Windows 下可以用 `.\.tailscale\start-flowspace-public.ps1` 同时启动正式和测试两个公开前端，并写入两个 Tailscale Funnel path。

## 使用规则

- 默认开发入口是测试服务：`make dev` 等同于 `make dev-test`。
- 默认停止入口也是测试服务：`make kill` 等同于 `make kill-test`。
- 只在明确需要正式数据时使用 `make dev-prod`、`make kill-prod`、`make start-prod-backend` 或 `make start-prod-frontend`。
- 前端本地开发默认代理测试后端：`frontend/.env` 中的 `VITE_BACKEND_PORT=18080`。
- 新环境可从 `frontend/.env.example` 复制本地 env，默认代理测试后端。
- 正式构建代理正式后端：`frontend/.env.production` 中的 `VITE_BACKEND_PORT=8080`。
- 后端手动启动时，`FLOWSPACE_ENV=test` 默认监听 `18080` 并使用 `flowspace.test.db`；`FLOWSPACE_ENV=prod` 默认监听 `8080` 并使用 `flowspace.db`。
- `PORT` 和 `FLOWSPACE_DB_PATH` 是显式覆盖项，只在临时排查或 CI 场景使用，使用后要确认没有指向正式库。

## 单独启动

```powershell
# 测试后端：18080 + flowspace.test.db
$env:FLOWSPACE_ENV = "test"; go run ./cmd/server

# 正式后端：8080 + flowspace.db
$env:FLOWSPACE_ENV = "prod"; go run ./cmd/server
```

```powershell
# 测试前端：15199，代理 18080
cd frontend
$env:VITE_BACKEND_PORT = "18080"
npm run dev -- --host 127.0.0.1 --port 15199

# 正式前端：5199，代理 8080
cd frontend
$env:VITE_BACKEND_PORT = "8080"
npm run dev -- --host 127.0.0.1 --port 5199
```

手动启动前端时要注意当前 shell 或 env 文件里的 `VITE_BACKEND_PORT`。日常开发应保持 `18080`。
