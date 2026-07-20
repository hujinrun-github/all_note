import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  deleteUserAvatar,
  getRuntimeSettings,
  getUserProfile,
  saveServiceProfile,
  testServiceProfile,
  updateUserProfile,
  uploadUserAvatar,
} from '../api/settings'
import Settings from './Settings'

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
    expect(screen.getByRole('textbox', { name: /Schema/ })).toHaveValue('public')
    await user.click(screen.getByRole('button', { name: '对象存储' }))
    await user.click(screen.getByRole('button', { name: '添加自定义配置' }))
    expect(screen.getByRole('textbox', { name: /Bucket 名称/ })).toHaveValue('flowspace')
    expect(screen.getByRole('textbox', { name: 'Access Key' })).toBeVisible()
    expect(screen.getByLabelText('Secret Key')).toHaveAttribute('type', 'password')
    expect(screen.queryByText('凭据')).not.toBeInTheDocument()
  })

  it('offers direct SenseVoice and FunASR transcription providers', async () => {
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole('heading', { name: '个人资料' })
    await user.click(screen.getByRole('button', { name: 'AI 服务' }))
    await user.click(screen.getByLabelText('语音转写模式：平台默认'))

    const provider = screen.getByRole('combobox', { name: '语音服务类型' })
    expect(provider).toHaveValue('sensevoice')
    expect(screen.getByPlaceholderText('例如：iic/SenseVoiceSmall')).toBeVisible()

    await user.selectOptions(provider, 'funasr')
    expect(screen.getByPlaceholderText('例如：paraformer-zh')).toBeVisible()
    expect(screen.getByText(/multipart/)).toBeVisible()
  })
})
