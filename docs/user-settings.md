# 用户设置指南

用户登录后，点击右上角头像进入“用户设置”。数据库、对象存储、文本服务和语音转写分别配置；没有选择自定义配置时使用平台默认服务。

## 配置 FunASR / SenseVoice 语音转写

进入“用户设置 → AI 服务”，展开“语音转写”。FlowSpace 支持 OpenAI 兼容转写接口，也可以把音频直接提交给 FunASR 或 SenseVoice HTTP 服务。

### FunASR Server 1.3.6

FunASR Server 1.3.6 提供以下相关接口：

| 接口 | 用途 |
| --- | --- |
| `POST /v1/audio/transcriptions` | OpenAI 兼容的音频转写接口，推荐使用 |
| `POST /asr` | 包含时间戳、热词和说话人识别能力的完整接口 |
| `GET /v1/models` | 查询服务提供的模型 |
| `GET /health` | 查询服务健康状态和已加载模型 |

`POST /v1/audio/transcriptions` 使用 `multipart/form-data`，只有 `file` 必填。还支持以下可选字段：

- `model`：默认 `fun-asr-nano`
- `language`：音频语言，例如 `zh`
- `response_format`：默认 `json`
- `spk`：是否启用说话人识别，默认 `false`

### 推荐配置

假设接口文档位于 `http://127.0.0.1:8000/docs`，推荐使用 OpenAI 兼容模式：

| 设置项 | 填写内容 |
| --- | --- |
| 语音服务类型 | `OpenAI 兼容转写` |
| 配置名称 | `本地 FunASR` |
| API 地址 | `http://127.0.0.1:8000/v1` |
| 模型名称 | `sensevoice` |
| API Key | 无鉴权时留空 |

OpenAI 兼容模式会自动把 `/audio/transcriptions` 添加到 API 地址末尾，最终调用：

```text
POST http://127.0.0.1:8000/v1/audio/transcriptions
```

也可以选择 `FunASR` 类型。该模式不会自动补全路径，因此 API 地址必须填写完整接口：

| 设置项 | 填写内容 |
| --- | --- |
| 语音服务类型 | `FunASR` |
| API 地址 | `http://127.0.0.1:8000/v1/audio/transcriptions` |
| 模型名称 | `sensevoice`、`fun-asr-nano` 或服务实际提供的模型 |
| API Key | 无鉴权时留空 |

优先选择 `/health` 返回的 `models_loaded` 中已经加载的模型。可用模型列表以 `/v1/models` 的实际响应为准。

### 本地地址与内网地址限制

为防止 SSRF，FlowSpace 后端默认拒绝连接：

- `localhost` 和 `127.0.0.0/8`；
- 未经部署管理员允许的 RFC1918 私有网段；
- 链路本地、组播和未指定地址。

因此，把 `127.0.0.1:8000` 填入设置页后，“测试连接”在当前默认安全策略下会被拒绝。这表示 FlowSpace 的出站策略阻止了请求，并不表示 FunASR 配置或服务异常。

生产环境推荐通过一个解析到允许地址的 HTTPS 域名暴露 FunASR。若需要直接访问本机或内网服务，应由部署管理员只允许 FunASR 服务所在的精确 IP/CIDR，例如单个主机的 `/32`，不要开放整个内网网段。

> `127.0.0.1` 始终表示 FlowSpace 后端进程所在的主机。如果 FunASR 运行在另一台机器或另一个容器中，应填写后端能够访问的服务地址，而不是浏览器所在机器的回环地址。

### 排查步骤

1. 打开 FunASR 的 `/health`，确认 `status` 为 `ok`。
2. 打开 `/v1/models`，确认配置的模型存在；优先使用已经加载的模型。
3. 确认 API 地址在 FlowSpace 后端所在机器上可访问。
4. 无鉴权服务不要随意填写 API Key；启用鉴权后再填写服务要求的 Bearer Token。
5. 点击“测试连接”，通过后再点击“保存并启用”。

