# iOS / Apple Watch 原生接口

这些接口由 `all_note` 后端直接提供，不需要单独部署移动端服务。iPhone 可以继续使用登录接口返回的 `fs_session` Cookie；Apple Watch 使用配对时签发的受限 Bearer Token。

## 服务配置

音频文件保存在 MinIO，数据库只保存笔记、上传状态和对象键。服务端通过环境变量读取凭据，不把访问密钥写入代码或数据库。

```powershell
$env:FLOWSPACE_MINIO_ENDPOINT = "http://minio-host:9000"
$env:FLOWSPACE_MINIO_ACCESS_KEY = "<access-key>"
$env:FLOWSPACE_MINIO_SECRET_KEY = "<secret-key>"
$env:FLOWSPACE_MINIO_BUCKET = "flowspace"

# 可选，默认 50 MiB
$env:FLOWSPACE_VOICE_MAX_BYTES = "52428800"
```

如果没有设置 MinIO 变量，现有网页接口仍可启动和使用；音频上传与读取会返回 `503 VOICE_STORAGE_UNAVAILABLE`。

转写服务采用常见的 multipart HTTP 协议：请求字段为 `file`、可选的 `model` 和 `language`，成功响应为 `{"text":"..."}`。

```powershell
$env:FLOWSPACE_TRANSCRIPTION_URL = "https://speech-service.example.com/v1/audio/transcriptions"
$env:FLOWSPACE_TRANSCRIPTION_API_KEY = "<api-key>"
$env:FLOWSPACE_TRANSCRIPTION_MODEL = "<model-name>"
$env:FLOWSPACE_TRANSCRIPTION_TIMEOUT_SECONDS = "120"
```

不配置转写服务时，其余语音笔记功能可正常使用，触发转写会返回 `503 TRANSCRIPTION_UNAVAILABLE`。

## Watch 配对

以下两个接口只接受正常用户会话 Cookie，不接受 Watch Token：

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/devices/watch/authorize` | 签发 Watch Token；明文 Token 只在本次响应返回 |
| `POST` | `/api/devices/watch/revoke` | 撤销指定 Watch，撤销后立即失效 |

授权请求：

```json
{
  "name": "My Apple Watch",
  "expires_in_days": 90
}
```

`expires_in_days` 可选，默认 90 天，范围为 1–365 天。Watch 随后使用：

```http
Authorization: Bearer <watch-token>
```

Watch Token 只在下列受限路由生效，不可访问 `/api/notes`、管理接口或同步配置：

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/watch/snapshot` | 今日任务、逾期任务、事件、近期笔记和近期语音笔记 |
| `PATCH` | `/api/watch/tasks/{id}` | 使用现有任务更新请求完成或修改任务 |
| 多种 | `/api/voice-notes...` | 创建、上传、读取和转写语音笔记 |

## 语音笔记

### 1. 创建元数据

```http
POST /api/voice-notes
Content-Type: application/json
```

```json
{
  "client_id": "0d8e1549-9e85-4724-9627-508a70332012",
  "title": "散步时的想法",
  "duration_ms": 18300,
  "recorded_at": 1783987200,
  "language": "zh"
}
```

`client_id` 必须是客户端生成的 UUID，也是弱网重试幂等键。首次创建返回 `201`；重复请求返回同一条记录和 `200`，不会生成重复普通笔记。

### 2. 上传音频

```http
PUT /api/voice-notes/{client_id}/audio
Content-Type: audio/mp4
X-Audio-SHA256: <64 位十六进制摘要，可选但推荐>

<原始音频字节>
```

支持 `audio/mp4`、M4A、AAC、MP3 和 WAV。相同摘要可安全重试；同一 `client_id` 上传不同内容会返回 `409 AUDIO_CONFLICT`，避免覆盖原录音。

### 3. 状态、读取与转写

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/voice-notes/{client_id}/status` | 查询上传和转写状态 |
| `GET` | `/api/voice-notes/{client_id}/audio` | 以原 Content-Type 流式读取音频 |
| `POST` | `/api/voice-notes/{client_id}/transcription` | 调用转写服务，并把结果写回普通笔记正文 |

转写请求体可省略；如需覆盖创建时的语言：

```json
{
  "language": "zh"
}
```

上传状态为 `pending`、`uploading`、`uploaded` 或 `failed`；转写状态为 `not_started`、`processing`、`completed` 或 `failed`。

## 数据迁移

- PostgreSQL 启动时自动执行 `0009_native_voice_watch.sql`。
- SQLite 启动时自动创建相同约束和索引。
- 语音元数据按工作区隔离；删除普通笔记时，对应语音元数据会级联删除。
