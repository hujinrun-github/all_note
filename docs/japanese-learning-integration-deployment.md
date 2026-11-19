# 联动发布流水线要求

FlowSpace 继续使用现有 `.github/workflows/deploy.yml`，通过 `Quality gates` 后才执行生产 SSH 部署。前端回归 job 失败时，deploy 不会启动。

部署把触发事件的 `github.sha` 作为 `DEPLOY_SHA` 传到服务器，验证它属于目标分支历史后 checkout 这个完整提交；不会把测试后才推送的其他提交作为本次发布内容。应核对部署日志中的完整 SHA、服务健康状态以及线上项目列表到详情及刷新路径。

当前生产发布要求 `FLOWSPACE_ENABLE_MOBILE_SYNC_V2=true` 和 `FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=true`。未配置时按 true 启用；显式 false 或其他值会在写入环境配置和重启服务前拒绝部署。现有工作区的 v2 cutover 状态保持，不能重跑 cutover 或恢复 legacy writes。

Compose 的 `control-migrate → tenant-adopt（按配置可跳过）→ tenant-migrate → backend` 顺序负责新增 control `0006_japanese_integration.sql` 与默认 tenant `0017_japanese_integration.sql`。如有独立的自定义 tenant endpoint，也必须在接收新流量前完成该 endpoint 的0017迁移。

联动签名密钥继续使用现有 credential keyring 加密；该 keyring 保存在发布之外，不因 checkout 或构建镜像而替换。日语端的持久私有配置及校验规则见相邻项目 `docs/binary-deployment.md`。

质量门禁的 PostgreSQL integration job 额外执行 `go test ./internal/japaneseintegration -count=1`，确保新联动包的真实 PostgreSQL 场景不因 unit/race job 没有数据库而跳过。跨仓库真实 HTTP 测试和线上配对验收见 [联动测试说明](japanese-learning-integration-testing.md)。
