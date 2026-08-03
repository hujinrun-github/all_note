import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  deleteUserAvatar,
  getRuntimeSettings,
  getUserProfile,
  saveServiceProfile,
  setServiceBinding,
  testServiceProfile,
  updateUserProfile,
  uploadUserAvatar,
  verifyServiceProfile,
} from '../api/settings'
import Settings from './Settings'
import { resetOwnPassword } from '../api/auth'

vi.mock('../api/settings', () => ({
  getUserProfile: vi.fn(),
  updateUserProfile: vi.fn(),
  uploadUserAvatar: vi.fn(),
  deleteUserAvatar: vi.fn(),
  getRuntimeSettings: vi.fn(),
  testServiceProfile: vi.fn(),
  saveServiceProfile: vi.fn(),
  verifyServiceProfile: vi.fn(),
  setServiceBinding: vi.fn(),
  startCodexSubscription: vi.fn(),
  pollCodexSubscription: vi.fn(),
}))

vi.mock('../api/auth', () => ({
  resetOwnPassword: vi.fn(),
}))

function renderSettings() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <Settings />
    </QueryClientProvider>
  )
}

describe('Settings', () => {
  beforeEach(() => {
    vi.mocked(getUserProfile).mockResolvedValue({
      user_id: 'u1',
      email: 'user@example.com',
      display_name: '旧名称',
      locale: 'zh-CN',
      time_zone: 'Asia/Shanghai',
      updated_at: 1,
    })
    vi.mocked(updateUserProfile).mockResolvedValue({
      user_id: 'u1',
      email: 'user@example.com',
      display_name: '新名称',
      locale: 'ja-JP',
      time_zone: 'Asia/Tokyo',
      updated_at: 2,
    })
    vi.mocked(uploadUserAvatar).mockResolvedValue({
      avatar_url: '/api/settings/profile/avatar?v=2',
      sha256: 'abc',
      width: 32,
      height: 32,
    })
    vi.mocked(deleteUserAvatar).mockResolvedValue(undefined)
    vi.mocked(getRuntimeSettings).mockResolvedValue({
      workspace_id: 'w1',
      mode: 'active',
      epoch: 1,
      binding_revision: 1,
      bindings: [
        {
          kind: 'data_store',
          mode: 'default',
          has_credentials: true,
          revision: 1,
        },
        {
          kind: 'object_s3',
          mode: 'default',
          has_credentials: true,
          revision: 1,
        },
        {
          kind: 'llm_chat',
          mode: 'default',
          has_credentials: true,
          revision: 1,
        },
        {
          kind: 'llm_transcription',
          mode: 'default',
          has_credentials: true,
          revision: 1,
        },
      ],
    })
    vi.mocked(testServiceProfile).mockResolvedValue({
      ok: true,
      code: 'OK',
      message: '连接测试通过',
    })
    vi.mocked(saveServiceProfile).mockResolvedValue({
      id: 'v1',
      family_id: 'f1',
      kind: 'object_s3',
      version: 1,
      state: 'draft',
      has_credentials: true,
    })
    vi.mocked(verifyServiceProfile).mockResolvedValue({
      endpoint_id: 'custom-v1',
      profile_version_id: 'v1',
      kind: 'object_s3',
    })
    vi.mocked(setServiceBinding).mockResolvedValue({
      kind: 'object_s3',
      mode: 'custom',
      endpoint_id: 'custom-v1',
      provider: 'minio',
      profile_version_id: 'v1',
      has_credentials: true,
      revision: 2,
    })
    vi.mocked(resetOwnPassword).mockReset()
    vi.mocked(resetOwnPassword).mockResolvedValue(undefined)
  })

  it('resets the signed-in user password without asking for the old password', async () => {
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole('heading', { name: '个人资料' })
    await user.click(screen.getByRole('button', { name: '账号安全' }))

    expect(screen.getByRole('heading', { name: '重置登录密码' })).toBeVisible()
    expect(screen.queryByLabelText('当前密码')).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('新密码'), 'resetPass123')
    await user.type(screen.getByLabelText('确认新密码'), 'resetPass123')
    await user.click(screen.getByRole('button', { name: '重置密码' }))

    await waitFor(() =>
      expect(resetOwnPassword).toHaveBeenCalledWith('resetPass123')
    )
    expect(await screen.findByText(/其他设备需要重新登录/)).toBeVisible()
  })

  it('validates matching reset passwords before calling the API', async () => {
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole('heading', { name: '个人资料' })
    await user.click(screen.getByRole('button', { name: '账号安全' }))
    await user.type(screen.getByLabelText('新密码'), 'resetPass123')
    await user.type(screen.getByLabelText('确认新密码'), 'different123')
    await user.click(screen.getByRole('button', { name: '重置密码' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '两次输入的新密码不一致'
    )
    expect(resetOwnPassword).not.toHaveBeenCalled()
  })

  it('loads and saves the profile without a request waterfall', async () => {
    const user = userEvent.setup()
    renderSettings()
    const name = await screen.findByRole('textbox', { name: '显示名称' })
    expect(name).toHaveValue('旧名称')
    await user.clear(name)
    await user.type(name, '新名称')
    await user.selectOptions(
      screen.getByRole('combobox', { name: '界面语言' }),
      'ja-JP'
    )
    const timeZone = screen.getByRole('textbox', { name: '时区' })
    await user.clear(timeZone)
    await user.type(timeZone, 'Asia/Tokyo')
    await user.click(screen.getByRole('button', { name: '保存资料' }))
    await waitFor(() =>
      expect(updateUserProfile).toHaveBeenCalledWith(
        { display_name: '新名称', locale: 'ja-JP', time_zone: 'Asia/Tokyo' },
        expect.anything()
      )
    )
    expect(await screen.findByText('个人资料已保存')).toBeVisible()
  })

  it('shows default service states when the user has not selected custom services', async () => {
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole('heading', { name: '个人资料' })
    const databaseTab = screen.getByRole('button', { name: '数据库' })
    await user.click(databaseTab)
    expect(databaseTab).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('heading', { name: '数据库存储' })).toBeVisible()
    expect(screen.getByText('使用平台默认配置')).toBeVisible()
    const aiTab = screen.getByRole('button', { name: 'AI 服务' })
    await user.click(aiTab)
    expect(aiTab).toHaveAttribute('aria-current', 'page')
    expect(databaseTab).not.toHaveAttribute('aria-current')
    expect(screen.getByLabelText('文本服务模式：平台默认')).toBeVisible()
    expect(screen.getByLabelText('语音转写模式：平台默认')).toBeVisible()
  })

  it('offers a database schema and an object bucket for custom storage', async () => {
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole('heading', { name: '个人资料' })
    await user.click(screen.getByRole('button', { name: '数据库' }))
    await user.click(screen.getByRole('button', { name: '添加自定义配置' }))
    expect(screen.getByRole('textbox', { name: /Schema/ })).toHaveValue(
      'public'
    )
    await user.click(screen.getByRole('button', { name: '对象存储' }))
    await user.click(screen.getByRole('button', { name: '添加自定义配置' }))
    expect(screen.getByRole('textbox', { name: /Bucket 名称/ })).toHaveValue(
      'flowspace'
    )
    expect(screen.getByRole('textbox', { name: 'Access Key' })).toBeVisible()
    expect(screen.getByLabelText('Secret Key')).toHaveAttribute(
      'type',
      'password'
    )
    expect(screen.queryByText('凭据')).not.toBeInTheDocument()
  })

  it('verifies and binds a custom object store when saving', async () => {
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole('heading', { name: '个人资料' })
    await user.click(screen.getByRole('button', { name: '对象存储' }))
    await user.click(screen.getByRole('button', { name: '添加自定义配置' }))
    await user.type(
      screen.getByRole('textbox', { name: '配置名称' }),
      '私有 MinIO'
    )
    await user.type(
      screen.getByRole('textbox', { name: '服务地址' }),
      'https://objects.example.com'
    )
    await user.type(
      screen.getByRole('textbox', { name: 'Access Key' }),
      'access'
    )
    await user.type(screen.getByLabelText('Secret Key'), 'secret')
    await user.click(screen.getByRole('button', { name: '保存并启用' }))
    await waitFor(() =>
      expect(verifyServiceProfile).toHaveBeenCalledWith({
        kind: 'object_s3',
        versionId: 'v1',
      })
    )
    expect(setServiceBinding).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'object_s3',
        mode: 'custom',
        endpoint_id: 'custom-v1',
        expected_revision: 1,
        expected_runtime_revision: 1,
      })
    )
  })

  it('offers direct SenseVoice and FunASR transcription providers', async () => {
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole('heading', { name: '个人资料' })
    await user.click(screen.getByRole('button', { name: 'AI 服务' }))
    await user.click(screen.getByLabelText('语音转写模式：平台默认'))

    const provider = screen.getByRole('combobox', { name: '语音服务类型' })
    expect(provider).toHaveValue('sensevoice')
    expect(
      screen.getByPlaceholderText('例如：iic/SenseVoiceSmall')
    ).toBeVisible()

    await user.selectOptions(provider, 'funasr')
    expect(screen.getByPlaceholderText('例如：paraformer-zh')).toBeVisible()
    expect(screen.getByText(/multipart/)).toBeVisible()
  })

  it('configures faster-whisper through Wyoming TCP without an API key', async () => {
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole('heading', { name: '个人资料' })
    await user.click(screen.getByRole('button', { name: 'AI 服务' }))
    await user.click(screen.getByLabelText('语音转写模式：平台默认'))

    const provider = screen.getByRole('combobox', { name: '语音服务类型' })
    await user.selectOptions(provider, 'wyoming')

    expect(screen.getByLabelText('模型名称（可选）')).toHaveValue('auto')
    expect(screen.queryByLabelText('API Key')).not.toBeInTheDocument()
    expect(screen.getByText(/不是 HTTP 或网页服务/)).toBeVisible()

    await user.type(
      screen.getByRole('textbox', { name: '配置名称' }),
      '局域网 Faster Whisper'
    )
    await user.type(
      screen.getByRole('textbox', { name: 'Wyoming TCP 地址' }),
      '192.168.1.13:20300'
    )
    await user.click(screen.getByRole('button', { name: '测试连接' }))

    await waitFor(() =>
      expect(testServiceProfile).toHaveBeenCalledWith(
        {
          kind: 'llm_transcription',
          provider: 'wyoming',
          config: { endpoint: '192.168.1.13:20300', model: 'auto' },
          secret: '',
        },
        expect.anything()
      )
    )
  })
})
