import { type ChangeEvent, type FormEvent, useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AudioWaveform,
  ChevronRight,
  Database,
  ExternalLink,
  HardDrive,
  MessageCircleMore,
  Sparkles,
  UserRound,
} from 'lucide-react'
import {
  deleteUserAvatar,
  getRuntimeSettings,
  getUserProfile,
  pollCodexSubscription,
  saveServiceProfile,
  setServiceBinding,
  startCodexSubscription,
  testServiceProfile,
  updateUserProfile,
  uploadUserAvatar,
  verifyServiceProfile,
  type ServiceBinding,
  type ServiceKind,
  type UserProfile,
} from '../api/settings'

type SettingsTab = 'profile' | 'database' | 'objects' | 'ai'

const tabs = [
  { id: 'profile', label: '个人资料', icon: UserRound },
  { id: 'database', label: '数据库', icon: Database },
  { id: 'objects', label: '对象存储', icon: HardDrive },
  { id: 'ai', label: 'AI 服务', icon: Sparkles },
]

export default function Settings() {
  const [activeTab, setActiveTab] = useState<SettingsTab>('profile')
  const profile = useQuery({
    queryKey: ['settings', 'profile'],
    queryFn: getUserProfile,
  })
  const runtime = useQuery({
    queryKey: ['settings', 'runtime'],
    queryFn: getRuntimeSettings,
    retry: false,
  })

  return (
    <div className="settings-page">
      <aside className="settings-nav" aria-label="设置分类">
        <span className="settings-nav-label">设置菜单</span>
        {tabs.map((tab) => {
          const Icon = tab.icon
          const selected = activeTab === tab.id
          return (
            <button
              key={tab.id}
              type="button"
              className={selected ? 'is-active' : ''}
              aria-current={selected ? 'page' : undefined}
              onClick={() => setActiveTab(tab.id as SettingsTab)}
            >
              <Icon aria-hidden="true" size={19} strokeWidth={1.8} />
              <span>{tab.label}</span>
              <ChevronRight
                className="settings-nav-chevron"
                aria-hidden="true"
                size={16}
              />
            </button>
          )
        })}
      </aside>
      <section className="settings-content" aria-live="polite">
        {activeTab === 'profile' ? (
          <ProfileSettings
            profile={profile.data}
            loading={profile.isLoading}
            error={profile.isError}
          />
        ) : null}
        {activeTab === 'database' ? (
          <ServiceSettingsCard
            kind="data_store"
            title="数据库存储"
            description="保存笔记、任务和同步数据。切换数据库前需要完成数据迁移。"
            binding={runtime.data?.bindings.find(
              (item) => item.kind === 'data_store'
            )}
            runtimeRevision={runtime.data?.binding_revision ?? 0}
            unavailable={runtime.isError}
          />
        ) : null}
        {activeTab === 'objects' ? (
          <ServiceSettingsCard
            kind="object_s3"
            title="对象存储"
            description="保存笔记图片和语音文件。历史对象始终从原存储位置读取。"
            binding={runtime.data?.bindings.find(
              (item) => item.kind === 'object_s3'
            )}
            runtimeRevision={runtime.data?.binding_revision ?? 0}
            unavailable={runtime.isError}
          />
        ) : null}
        {activeTab === 'ai' ? (
          <AISettings
            runtime={runtime.data?.bindings}
            runtimeRevision={runtime.data?.binding_revision ?? 0}
            unavailable={runtime.isError}
          />
        ) : null}
      </section>
    </div>
  )
}

function ProfileSettings({
  profile,
  loading,
  error,
}: {
  profile?: UserProfile
  loading: boolean
  error: boolean
}) {
  const queryClient = useQueryClient()
  const [displayName, setDisplayName] = useState('')
  const [locale, setLocale] = useState<UserProfile['locale']>('zh-CN')
  const [timeZone, setTimeZone] = useState('Asia/Shanghai')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    if (!profile) return
    setDisplayName(profile.display_name)
    setLocale(profile.locale)
    setTimeZone(profile.time_zone)
  }, [profile?.display_name, profile?.locale, profile?.time_zone])

  const save = useMutation({
    mutationFn: updateUserProfile,
    onSuccess: (updated) => {
      queryClient.setQueryData(['settings', 'profile'], updated)
      void queryClient.invalidateQueries({ queryKey: ['auth', 'me'] })
      setNotice('个人资料已保存')
    },
  })
  const upload = useMutation({
    mutationFn: uploadUserAvatar,
    onSuccess: ({ avatar_url }) => {
      queryClient.setQueryData<UserProfile>(
        ['settings', 'profile'],
        (current) => (current ? { ...current, avatar_url } : current)
      )
      void queryClient.invalidateQueries({ queryKey: ['auth', 'me'] })
      setNotice('头像已更新')
    },
  })
  const remove = useMutation({
    mutationFn: deleteUserAvatar,
    onSuccess: () => {
      queryClient.setQueryData<UserProfile>(
        ['settings', 'profile'],
        (current) => (current ? { ...current, avatar_url: undefined } : current)
      )
      void queryClient.invalidateQueries({ queryKey: ['auth', 'me'] })
      setNotice('头像已移除')
    },
  })

  function submit(event: FormEvent) {
    event.preventDefault()
    setNotice('')
    save.mutate({
      display_name: displayName.trim(),
      locale,
      time_zone: timeZone.trim(),
    })
  }

  function selectAvatar(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    if (file) upload.mutate(file)
    event.target.value = ''
  }

  if (loading) return <div className="settings-state">正在加载个人资料…</div>
  if (error || !profile)
    return (
      <div className="settings-state is-error" role="alert">
        无法加载个人资料，请稍后重试。
      </div>
    )

  const initial = Array.from(
    profile.display_name.trim() || profile.email
  )[0]?.toUpperCase()
  return (
    <form className="settings-card profile-settings-card" onSubmit={submit}>
      <header className="settings-section-header">
        <h2>个人资料</h2>
        <p>头像属于你的用户账户，不依赖当前工作空间的对象存储。</p>
      </header>
      <div className="avatar-settings-row">
        <div className="settings-avatar">
          {profile.avatar_url ? (
            <img src={profile.avatar_url} alt="当前头像" />
          ) : (
            initial
          )}
        </div>
        <div>
          <label className="secondary-button">
            上传图片
            <input
              type="file"
              accept="image/jpeg,image/png,image/webp"
              onChange={selectAvatar}
            />
          </label>
          {profile.avatar_url ? (
            <button
              type="button"
              className="text-button"
              onClick={() => remove.mutate()}
            >
              移除头像
            </button>
          ) : null}
          <small>JPEG、PNG 或 WebP，最大 2 MiB</small>
        </div>
      </div>
      <div className="settings-field-grid">
        <label>
          <span>显示名称</span>
          <input
            value={displayName}
            maxLength={80}
            required
            onChange={(event) => setDisplayName(event.target.value)}
          />
        </label>
        <label>
          <span>登录邮箱</span>
          <input value={profile.email} disabled />
        </label>
        <label>
          <span>界面语言</span>
          <select
            value={locale}
            onChange={(event) =>
              setLocale(event.target.value as UserProfile['locale'])
            }
          >
            <option value="zh-CN">简体中文</option>
            <option value="en-US">English</option>
            <option value="ja-JP">日本語</option>
          </select>
        </label>
        <label>
          <span>时区</span>
          <input
            value={timeZone}
            required
            onChange={(event) => setTimeZone(event.target.value)}
          />
        </label>
      </div>
      {save.error || upload.error || remove.error ? (
        <p className="settings-message is-error" role="alert">
          保存失败，请检查输入后重试。
        </p>
      ) : null}
      {notice ? (
        <p className="settings-message" role="status">
          {notice}
        </p>
      ) : null}
      <footer>
        <button
          className="primary-action"
          type="submit"
          disabled={save.isPending}
        >
          {save.isPending ? '保存中…' : '保存资料'}
        </button>
      </footer>
    </form>
  )
}

function ServiceSettingsCard({
  kind,
  title,
  description,
  binding,
  runtimeRevision,
  unavailable,
}: {
  kind: ServiceKind
  title: string
  description: string
  binding?: ServiceBinding
  runtimeRevision: number
  unavailable: boolean
}) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [namespace, setNamespace] = useState(
    kind === 'data_store' ? 'public' : 'flowspace'
  )
  const [secret, setSecret] = useState('')
  const [accessKey, setAccessKey] = useState('')
  const [objectSecretKey, setObjectSecretKey] = useState('')
  const [message, setMessage] = useState('')
  const provider = kind === 'data_store' ? 'postgres' : 'minio'
  const profileInput = () => ({
    kind,
    provider,
    config:
      kind === 'data_store'
        ? { endpoint: endpoint.trim(), schema: namespace.trim() }
        : { endpoint: endpoint.trim(), bucket: namespace.trim() },
    secret:
      kind === 'object_s3'
        ? JSON.stringify({
            access_key: accessKey.trim(),
            secret_key: objectSecretKey,
          })
        : secret,
  })
  const test = useMutation({
    mutationFn: testServiceProfile,
    onSuccess: (result) => setMessage(result.message || '连接测试通过'),
  })
  const saveAndApply = useMutation({
    mutationFn: async () => {
      const versionId = crypto.randomUUID()
      const saved = await saveServiceProfile({
        ...profileInput(),
        id: versionId,
        family_id: crypto.randomUUID(),
        name: name.trim(),
      })
      const verified = await verifyServiceProfile({ kind, versionId: saved.id })
      if (kind === 'data_store') return { binding: undefined, verified }
      const nextBinding = await setServiceBinding({
        kind,
        mode: 'custom',
        endpoint_id: verified.endpoint_id,
        expected_revision: binding?.revision || 1,
        expected_runtime_revision: runtimeRevision,
      })
      return { binding: nextBinding, verified }
    },
    onSuccess: (result) => {
      setMessage(
        kind === 'data_store'
          ? '配置已验证；数据库切换需要启动数据迁移。'
          : '对象存储已验证并启用。'
      )
      setSecret('')
      setAccessKey('')
      setObjectSecretKey('')
      if (result.binding) {
        setEditing(false)
        void queryClient.invalidateQueries({
          queryKey: ['settings', 'runtime'],
        })
      }
    },
  })
  const currentLabel = unavailable
    ? '控制面暂不可用'
    : binding?.mode === 'custom'
      ? '使用自定义配置'
      : binding?.mode === 'disabled'
        ? '已关闭'
        : '使用平台默认配置'

  return (
    <article className="settings-card service-settings-card">
      <header className="settings-section-header">
        <h2>{title}</h2>
        <p>{description}</p>
      </header>
      <div className={`service-status${editing ? ' is-expanded' : ''}`}>
        <div className="service-status-icon" aria-hidden="true">
          {kind === 'data_store' ? (
            <Database size={22} />
          ) : (
            <HardDrive size={22} />
          )}
        </div>
        <div>
          <strong>{currentLabel}</strong>
          <small>
            {binding?.provider
              ? `${binding.provider} · revision ${binding.revision}`
              : '尚未选择自定义服务时自动使用默认配置。'}
          </small>
        </div>
        <span className="service-mode-badge">
          {binding?.mode === 'custom'
            ? '自定义'
            : binding?.mode === 'disabled'
              ? '关闭'
              : '默认'}
        </span>
      </div>
      {editing ? (
        <div className="service-config-form storage-config-form">
          <label>
            <span>配置名称</span>
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="例如：团队对象存储"
            />
          </label>
          <label>
            <span>服务地址</span>
            <input
              value={endpoint}
              onChange={(event) => setEndpoint(event.target.value)}
              placeholder={
                kind === 'data_store'
                  ? 'postgres://host/database'
                  : 'https://minio.example.com'
              }
            />
          </label>
          {kind === 'object_s3' ? (
            <>
              <label>
                <span>Access Key</span>
                <input
                  value={accessKey}
                  onChange={(event) => setAccessKey(event.target.value)}
                  autoComplete="off"
                  placeholder="MinIO / S3 Access Key"
                />
              </label>
              <label>
                <span>Secret Key</span>
                <input
                  type="password"
                  value={objectSecretKey}
                  onChange={(event) => setObjectSecretKey(event.target.value)}
                  autoComplete="new-password"
                  placeholder="MinIO / S3 Secret Key"
                />
              </label>
            </>
          ) : (
            <label>
              <span>数据库密码</span>
              <input
                type="password"
                value={secret}
                onChange={(event) => setSecret(event.target.value)}
                autoComplete="new-password"
              />
            </label>
          )}
          <label>
            <span>
              {kind === 'data_store' ? 'Schema（表命名空间）' : 'Bucket 名称'}
            </span>
            <input
              value={namespace}
              required
              onChange={(event) => setNamespace(event.target.value)}
              placeholder={kind === 'data_store' ? 'public' : 'flowspace'}
            />
            <small>
              {kind === 'data_store'
                ? '该 Schema 中会自动创建项目需要的多张关联表。'
                : 'Bucket 需预先存在，系统不会使用用户凭据静默创建。'}
            </small>
          </label>
          <div className="service-config-actions">
            <span className="service-config-action-note">
              填写完成后可先测试连接，不会修改当前配置。
            </span>
            <div className="service-config-primary-actions">
              <button
                type="button"
                className="secondary-button"
                disabled={
                  !endpoint ||
                  (kind === 'object_s3' &&
                    (!accessKey.trim() || !objectSecretKey)) ||
                  test.isPending
                }
                onClick={() => test.mutate(profileInput())}
              >
                {test.isPending ? '测试中…' : '测试连接'}
              </button>
              <button
                type="button"
                className="primary-action"
                disabled={
                  !name.trim() ||
                  !endpoint ||
                  !namespace.trim() ||
                  (kind === 'object_s3' &&
                    (!accessKey.trim() || !objectSecretKey)) ||
                  saveAndApply.isPending
                }
                onClick={() => saveAndApply.mutate()}
              >
                {saveAndApply.isPending
                  ? '验证中…'
                  : kind === 'data_store'
                    ? '保存并验证'
                    : '保存并启用'}
              </button>
            </div>
          </div>
          {message ? (
            <p className="settings-message" role="status">
              {message}
            </p>
          ) : null}
          {test.isError || saveAndApply.isError ? (
            <p className="settings-message is-error" role="alert">
              操作失败，原配置未改变。
            </p>
          ) : null}
        </div>
      ) : (
        <button
          type="button"
          className="secondary-button"
          onClick={() => setEditing(true)}
        >
          添加自定义配置
        </button>
      )}
      {kind === 'data_store' ? (
        <p className="settings-hint">
          保存配置不会立即切换数据库；验证后仍需启动数据迁移。
        </p>
      ) : null}
    </article>
  )
}

function AISettings({
  runtime,
  runtimeRevision,
  unavailable,
}: {
  runtime?: ServiceBinding[]
  runtimeRevision: number
  unavailable: boolean
}) {
  const chat = runtime?.find((item) => item.kind === 'llm_chat')
  const transcription = runtime?.find(
    (item) => item.kind === 'llm_transcription'
  )
  return (
    <article className="settings-card service-settings-card ai-settings-card">
      <header className="settings-section-header">
        <h2>AI 服务</h2>
        <p>分别配置文本对话和语音转写，未选择时使用平台默认配置。</p>
      </header>
      <AIServiceRow
        icon={MessageCircleMore}
        title="文本服务"
        description="可以切换为自定义服务或关闭。"
        mode={unavailable ? '暂不可用' : modeLabel(chat)}
        kind="llm_chat"
        binding={chat}
        runtimeRevision={runtimeRevision}
      />
      <AIServiceRow
        icon={AudioWaveform}
        title="语音转写"
        description="可以复用文本服务、单独配置或关闭。"
        mode={unavailable ? '暂不可用' : modeLabel(transcription)}
        kind="llm_transcription"
        binding={transcription}
        runtimeRevision={runtimeRevision}
      />
    </article>
  )
}

function AIServiceRow({
  icon: Icon,
  title,
  description,
  mode,
  kind,
  binding,
  runtimeRevision,
}: {
  icon: typeof MessageCircleMore
  title: string
  description: string
  mode: string
  kind: 'llm_chat' | 'llm_transcription'
  binding?: ServiceBinding
  runtimeRevision: number
}) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [model, setModel] = useState('')
  const [apiKey, setAPIKey] = useState('')
  const [transcriptionProvider, setTranscriptionProvider] = useState<
    'openai_compatible' | 'sensevoice' | 'funasr'
  >('sensevoice')
  const [message, setMessage] = useState('')
  const [codexFlow, setCodexFlow] =
    useState<Awaited<ReturnType<typeof startCodexSubscription>>>()
  const profileInput = () => ({
    kind,
    provider:
      kind === 'llm_transcription'
        ? transcriptionProvider
        : 'openai_compatible',
    config: { endpoint: endpoint.trim(), model: model.trim() },
    secret: apiKey,
  })
  const test = useMutation({
    mutationFn: testServiceProfile,
    onSuccess: (result) => setMessage(result.message || '连接测试通过'),
  })
  const saveAndUse = useMutation({
    mutationFn: async () => {
      const versionId = crypto.randomUUID()
      const saved = await saveServiceProfile({
        ...profileInput(),
        id: versionId,
        family_id: crypto.randomUUID(),
        name: name.trim(),
      })
      const verified = await verifyServiceProfile({ kind, versionId: saved.id })
      return setServiceBinding({
        kind,
        mode: 'custom',
        endpoint_id: verified.endpoint_id,
        expected_revision: binding?.revision || 1,
        expected_runtime_revision: runtimeRevision,
      })
    },
    onSuccess: () => {
      setAPIKey('')
      setEditing(false)
      void queryClient.invalidateQueries({ queryKey: ['settings', 'runtime'] })
    },
  })
  const changeMode = useMutation({
    mutationFn: (nextMode: 'disabled' | 'reuse_chat') =>
      setServiceBinding({
        kind,
        mode: nextMode,
        expected_revision: binding?.revision || 1,
        expected_runtime_revision: runtimeRevision,
      }),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: ['settings', 'runtime'] }),
  })
  const startCodex = useMutation({
    mutationFn: startCodexSubscription,
    onSuccess: (flow) => {
      setCodexFlow(flow)
      setMessage('授权码已生成，请在 OpenAI 页面完成登录。')
    },
  })
  const finishCodex = useMutation({
    mutationFn: () =>
      pollCodexSubscription({
        flowId: codexFlow!.flow_id,
        expectedRevision: binding?.revision || 0,
        expectedRuntimeRevision: runtimeRevision,
      }),
    onSuccess: (result) => {
      if (result.status === 'pending') {
        setMessage('还没有收到授权结果，请完成登录后再试。')
        return
      }
      if (result.status !== 'connected') {
        setMessage('授权已过期，请重新连接。')
        setCodexFlow(undefined)
        return
      }
      setMessage('Codex 订阅已连接并启用。')
      setCodexFlow(undefined)
      setEditing(false)
      void queryClient.invalidateQueries({ queryKey: ['settings', 'runtime'] })
    },
  })
  return (
    <div
      className={`service-status ai-service-row${editing ? ' is-expanded' : ''}`}
    >
      <div className="service-status-icon" aria-hidden="true">
        <Icon size={23} strokeWidth={1.8} />
      </div>
      <div className="ai-service-copy">
        <strong>{title}</strong>
        <small>{description}</small>
      </div>
      <button
        type="button"
        className="ai-mode-control"
        aria-label={`${title}模式：${mode}`}
        onClick={() => setEditing((value) => !value)}
      >
        <span>当前模式</span>
        <strong>{mode}</strong>
        <ChevronRight aria-hidden="true" size={16} />
      </button>
      {editing ? (
        <div className="service-config-form ai-config-form">
          {kind === 'llm_chat' ? (
            <section
              className="codex-connect-panel"
              aria-label="连接 Codex 订阅"
            >
              <div>
                <strong>使用 Codex 订阅</strong>
                <p>通过 OpenAI 设备授权登录，不需要填写 API Key。</p>
              </div>
              {!codexFlow ? (
                <button
                  type="button"
                  className="primary-action"
                  disabled={startCodex.isPending}
                  onClick={() => startCodex.mutate()}
                >
                  {startCodex.isPending ? '正在生成授权码…' : '连接 Codex 订阅'}
                </button>
              ) : (
                <div className="codex-device-flow">
                  <span>授权码</span>
                  <code>{codexFlow.user_code}</code>
                  <a
                    className="secondary-button"
                    href={codexFlow.verification_url}
                    target="_blank"
                    rel="noreferrer"
                  >
                    打开 OpenAI 授权页{' '}
                    <ExternalLink size={15} aria-hidden="true" />
                  </a>
                  <button
                    type="button"
                    className="primary-action"
                    disabled={finishCodex.isPending}
                    onClick={() => finishCodex.mutate()}
                  >
                    {finishCodex.isPending ? '正在确认…' : '我已完成授权'}
                  </button>
                </div>
              )}
            </section>
          ) : null}
          <div className="service-config-divider">
            <span>
              {kind === 'llm_transcription'
                ? '或直接连接语音服务'
                : '或使用 OpenAI 兼容服务'}
            </span>
          </div>
          {kind === 'llm_transcription' ? (
            <label>
              <span>语音服务类型</span>
              <select
                aria-label="语音服务类型"
                value={transcriptionProvider}
                onChange={(event) =>
                  setTranscriptionProvider(
                    event.target.value as typeof transcriptionProvider
                  )
                }
              >
                <option value="sensevoice">SenseVoice</option>
                <option value="funasr">FunASR</option>
                <option value="openai_compatible">OpenAI 兼容转写</option>
              </select>
            </label>
          ) : null}
          <label>
            <span>配置名称</span>
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="例如：我的 AI 服务"
            />
          </label>
          <label>
            <span>API 地址</span>
            <input
              value={endpoint}
              onChange={(event) => setEndpoint(event.target.value)}
              placeholder={
                kind === 'llm_transcription' &&
                transcriptionProvider !== 'openai_compatible'
                  ? '例如：http://speech.example.com/transcribe'
                  : 'https://api.example.com/v1'
              }
            />
          </label>
          <label>
            <span>模型名称</span>
            <input
              value={model}
              onChange={(event) => setModel(event.target.value)}
              placeholder={
                kind === 'llm_transcription'
                  ? transcriptionProvider === 'sensevoice'
                    ? '例如：iic/SenseVoiceSmall'
                    : transcriptionProvider === 'funasr'
                      ? '例如：paraformer-zh'
                      : '例如：whisper-1'
                  : '例如：deepseek-v4-pro'
              }
            />
          </label>
          <label>
            <span>API Key</span>
            <input
              type="password"
              value={apiKey}
              onChange={(event) => setAPIKey(event.target.value)}
              autoComplete="new-password"
            />
          </label>
          {kind === 'llm_transcription' ? (
            <p className="settings-hint">
              SenseVoice/FunASR 将音频以 multipart 的 file
              字段直接发送到该地址；无需鉴权时 API Key 可以留空。
            </p>
          ) : null}
          <div className="service-config-actions">
            <div className="service-config-mode-actions">
              <button
                type="button"
                className="text-button"
                disabled={changeMode.isPending}
                onClick={() => changeMode.mutate('disabled')}
              >
                关闭服务
              </button>
              {kind === 'llm_transcription' ? (
                <button
                  type="button"
                  className="text-button"
                  disabled={changeMode.isPending}
                  onClick={() => changeMode.mutate('reuse_chat')}
                >
                  复用文本服务
                </button>
              ) : null}
            </div>
            <div className="service-config-primary-actions">
              <button
                type="button"
                className="secondary-button"
                disabled={!endpoint.trim() || !model.trim() || test.isPending}
                onClick={() => test.mutate(profileInput())}
              >
                {test.isPending ? '测试中…' : '测试连接'}
              </button>
              <button
                type="button"
                className="primary-action"
                disabled={
                  !name.trim() ||
                  !endpoint.trim() ||
                  !model.trim() ||
                  saveAndUse.isPending
                }
                onClick={() => saveAndUse.mutate()}
              >
                {saveAndUse.isPending ? '验证并启用中…' : '保存并启用'}
              </button>
            </div>
          </div>
          {message ? (
            <p className="settings-message" role="status">
              {message}
            </p>
          ) : null}
          {test.isError ||
          saveAndUse.isError ||
          changeMode.isError ||
          startCodex.isError ||
          finishCodex.isError ? (
            <p className="settings-message is-error" role="alert">
              操作失败，原配置未改变。
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function modeLabel(binding?: ServiceBinding) {
  if (!binding || binding.mode === 'default') return '平台默认'
  if (binding.mode === 'custom') return binding.provider || '自定义'
  if (binding.mode === 'reuse_chat') return '复用文本服务'
  return '已关闭'
}
