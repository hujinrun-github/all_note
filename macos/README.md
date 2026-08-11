# FlowSpace for macOS

原生 SwiftUI 客户端，最低系统版本为 macOS 26。客户端直接连接现有 FlowSpace Web 服务，并且只允许进入已启用 task-domain v2 的工作空间。

## 当前实现

- 服务地址、健康检查、Cookie/邮箱密码登录、GitHub 原生 OAuth、强制改密和 v2 capability gate；GitHub 回调使用 `ASWebAuthenticationSession` 与短时一次性 PKCE 兑换，不把会话令牌放入回调 URL；
- 原生三栏工作区、今日/本周/本月/逾期/已完成范围；
- 同一任务的多次执行按 task definition 折叠为集合；
- 任务、未整理、项目列表与执行实例检查器；
- 任务工作台覆盖收件箱、今天、接下来、逾期、无日期、重复、已完成和草稿八个固定视图，并支持项目、优先级、执行状态、日期范围组合筛选、键盘选择、Space 完成及任务定义生命周期命令；
- revision/CAS 完成、重新打开、开始、跳过和取消命令；任务定义支持与 Web 一致的附件链接和完成门槛，必选项未完成时客户端会阻止完成操作；
- 周/月/年日历、时区切换、日历事件，以及单击时间槽打开可编辑默认时间的创建表单；
- 可调整日期、时间、时长和重复规则的任务创建；
- `⌘⇧N` 快速捕获窗口和九类设置入口；快速捕获支持项目、优先级、日期/时间块、重复规则，以及保存并继续或保存并关闭。
- 三栏笔记库、项目/标签过滤、创建删除、独立编辑窗口和 5 秒自动保存；正文使用本地打包的 Tiptap 富文本编辑器，通过 note ID 与 editor generation 校验的窄桥接同步 Markdown；
- `⌘K` 全局搜索、结果分类和最近搜索；
- 本周/本月总结、完成记录、待关注事项和最近笔记；
- 项目概览、任务、日程、笔记、学习路线和历史六个分区；
- 学习 Roadmap 阶段轨道、空白创建、AI 生成、节点管理，以及按节点打开的独立脑图窗口；
- 脑图节点关联任务的创建与进度回看；
- 笔记附件的上传、列表和删除；语音笔记附件支持转写状态、失败重试、覆盖确认，并把服务端识别结果同步写回正文；
- 对编辑器选中的日文调用服务端注音，并以 Web 端兼容的青空文库语法写回 Markdown。
- Notes 与 File 菜单发起播客导入，独立 Import Activity 窗口展示后台阶段、进度、错误、重试、取消、删除、原始单集和逐字稿；导入生成的笔记会在检查器中显示播客来源卡片。
- 同步设置管理 Notion/Obsidian 目标、连接测试和明确方向的推送/拉取；外部删除候选可逐条恢复外部文稿或确认删除本地笔记；笔记检查器支持绑定、CAS 解除绑定和立即同步。
- 运行时设置支持 PostgreSQL、MinIO/S3、文本 AI、四类转写服务和 Codex 设备授权，保留 profile 测试/验证及 binding CAS。
- 个人资料支持显示名称、语言、IANA 时区和账户级头像；安全设置可重置密码并保留当前会话。
- 管理员可从用户菜单或 Window 菜单打开独立账号管理窗口，进行搜索分页、创建账号、编辑资料/角色、设置临时密码和启停账号；最后一个启用管理员的约束由服务端权威校验。
- 通知设置支持按任务完成、播客导入和同步结果分别开关；权限只在用户明确操作时请求，导入通知可直接打开生成笔记，应用运行时会持续检查导入状态。
- 可在通用设置开启 `MenuBarExtra`；菜单栏显示下一项任务、快速捕获、今日入口、连接/同步状态和退出操作，主应用继续保留正常 Dock 行为。
- 任务、项目和笔记会写入按工作空间隔离的 Core Spotlight 索引；点击系统搜索结果可回到对应笔记窗口、任务执行或项目详情，退出登录与切换服务时会清理旧工作空间索引。
- 基于 Sparkle 2 的安全自动更新；设置页和应用菜单均可手动检查，正式构建强制使用 HTTPS appcast、Ed25519 公钥、Developer ID 签名与 Apple 公证。未注入更新通道的本地开发构建会明确显示为未配置，不会发起后台更新请求。

当前富文本编辑器覆盖 Web 端的 Markdown 往返、标题/列表/引用/代码/分割线、选区气泡菜单、假名标注、当前笔记查找和保存请求。所有完成和通知状态均来自服务端真实事件，不使用本地假数据模拟。

## 构建

需要 Xcode 26.3+、Node.js 22、pnpm 和 [XcodeGen](https://github.com/yonaskolb/XcodeGen)。干净检出后先在 `frontend/` 执行 `pnpm install --frozen-lockfile`：

```sh
make macos-editor
make macos-build
make macos-test
make macos-ui-test
make macos-release
make macos-release-verify
```

也可以打开 `macos/FlowSpaceMac.xcodeproj`。开发服务默认使用 `http://127.0.0.1:4100`，并通过前端代理访问后端 API。

`make macos-release` 会在 `.codex-run/macos-derived-data` 构建未签名的 `arm64 + x86_64` 通用 Release 应用，`make macos-release-verify` 还会校验 bundle ID、版本、两个架构、AppIcon 和内置富文本编辑器。GitHub Actions 会在 macOS 26 / Xcode 26 上执行单元测试、UI 冒烟测试和同样的产物校验。

UI 冒烟测试使用独立的临时偏好设置域，不会读取或清理真实服务地址。首次在本机命令行执行 UI 测试时，macOS 可能要求允许终端或 Xcode 控制其他应用；该权限未授权时测试运行器会停在系统自动化初始化阶段。

## 签名与公证

对外分发前，先把 App Store Connect 公证凭据保存到 Keychain：

```sh
xcrun notarytool store-credentials flowspace-release \
  --key /path/to/AuthKey_KEYID.p8 \
  --key-id KEYID \
  --issuer ISSUER_UUID
```

然后执行：

```sh
FLOWSPACE_DEVELOPER_ID_APPLICATION='Developer ID Application: Example (TEAMID)' \
FLOWSPACE_NOTARY_PROFILE='flowspace-release' \
FLOWSPACE_MARKETING_VERSION='0.1.0' \
FLOWSPACE_BUILD_NUMBER='1' \
FLOWSPACE_UPDATE_FEED_URL='https://updates.example.com/flowspace/appcast.xml' \
FLOWSPACE_UPDATE_PUBLIC_ED_KEY='BASE64_ED25519_PUBLIC_KEY' \
make macos-distribute
```

命令会复制未签名的 Universal 应用，按 Sparkle 要求从内到外签署安装服务、更新助手、框架和主应用，提交 Apple 公证、装订 ticket，再在 `dist/macos/` 输出 ZIP 和 SHA-256 文件。分发预检会拒绝缺少 HTTPS 更新源或 Ed25519 公钥的构建。命令不会修改源构建产物，也不会把证书或凭据写入仓库。

更新源需要用 Sparkle 官方 `generate_appcast` 工具维护并发布到上述 HTTPS 地址；每个版本的 `CFBundleVersion` 必须递增。用于生成 appcast 签名的私钥只能保存在发布环境，应用和仓库中只放公钥。

手动 GitHub 工作流 `macOS notarized release` 使用临时 Keychain，需要配置以下 Actions Secrets：

- `MACOS_DEVELOPER_ID_APPLICATION`
- `MACOS_DEVELOPER_ID_CERTIFICATE_P12`（Base64）
- `MACOS_DEVELOPER_ID_CERTIFICATE_PASSWORD`
- `MACOS_NOTARY_KEY_P8`
- `MACOS_NOTARY_KEY_ID`
- `MACOS_NOTARY_ISSUER_ID`
- `MACOS_UPDATE_FEED_URL`
- `MACOS_UPDATE_PUBLIC_ED_KEY`
