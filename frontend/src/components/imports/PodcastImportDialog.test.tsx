import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as contentImportsApi from '../../api/contentImports'
import { getRuntimeSettings } from '../../api/settings'
import { PodcastImportDialog } from './PodcastImportDialog'

vi.mock('../../api/contentImports')
vi.mock('../../api/settings')

function renderDialog(
  onCreated = vi.fn(),
  onClose = vi.fn(),
  projects: ReadonlyArray<{ id: string; name: string }> = []
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <PodcastImportDialog
      open
      projects={projects}
      onCreated={onCreated}
      onClose={onClose}
    />,
    {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    }
  )
}

describe('PodcastImportDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getRuntimeSettings).mockResolvedValue({
      workspace_id: 'workspace-1',
      mode: 'active',
      epoch: 1,
      binding_revision: 1,
      bindings: [
        {
          kind: 'llm_chat',
          mode: 'custom',
          provider: 'openai_compatible',
          endpoint_id: 'chat-1',
          has_credentials: true,
          revision: 1,
        },
      ],
    })
    vi.mocked(contentImportsApi.resolveContentImport).mockResolvedValue({
      source_type: 'xiaoyuzhou',
      submitted_url: 'https://www.xiaoyuzhoufm.com/episode/e1',
      canonical_url: 'https://www.xiaoyuzhoufm.com/episode/e1',
      external_id: 'e1',
      title: 'AI 产品经理的下一站',
      podcast_title: '产品沉思录',
      has_public_transcript: false,
    })
    vi.mocked(contentImportsApi.createContentImport).mockResolvedValue({
      id: 'import-1',
      source_url: 'https://www.xiaoyuzhoufm.com/episode/e1',
      status: 'active',
      stage: 'queued',
      progress: 0,
      summarize_with_ai: false,
      include_transcript: true,
      language: 'auto',
      project_ids: [],
      tags: ['播客'],
      revision: 1,
      created_at: 1,
      updated_at: 1,
    })
  })

  it('separates required transcription from optional AI organization', async () => {
    const user = userEvent.setup()
    const onCreated = vi.fn()
    const onClose = vi.fn()
    renderDialog(onCreated, onClose)

    await user.type(
      screen.getByLabelText('单集链接'),
      'https://www.xiaoyuzhoufm.com/episode/e1'
    )
    await user.click(screen.getByRole('button', { name: '解析链接' }))

    expect(await screen.findByText('AI 产品经理的下一站')).toBeVisible()
    expect(screen.getByText('未发现发布者公开逐字稿')).toBeVisible()
    expect(screen.getByText(/安全下载公开音频并在后台分片转写/)).toBeVisible()
    expect(screen.getByText('生成逐字稿')).toBeVisible()
    expect(screen.getByText('必需')).toBeVisible()

    const aiSwitch = screen.getByRole('switch', { name: 'AI 整理' })
    await waitFor(() =>
      expect(aiSwitch).toHaveAttribute('aria-checked', 'true')
    )
    await user.click(aiSwitch)
    expect(aiSwitch).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByText(/不调用文本 AI/)).toBeVisible()

    await user.click(screen.getByRole('button', { name: '开始转写' }))
    await waitFor(() => {
      expect(contentImportsApi.createContentImport).toHaveBeenCalledTimes(1)
      expect(
        vi.mocked(contentImportsApi.createContentImport).mock.calls[0][0]
      ).toEqual(
        expect.objectContaining({
          summarize_with_ai: false,
          include_transcript: true,
        })
      )
    })
    expect(onCreated).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('selects a v2 project and submits it with the import', async () => {
    const user = userEvent.setup()
    renderDialog(vi.fn(), vi.fn(), [
      { id: 'project-ai', name: 'AI 学习' },
      { id: 'project-product', name: '产品研究' },
    ])

    await user.type(
      screen.getByLabelText('单集链接'),
      'https://www.xiaoyuzhoufm.com/episode/e1'
    )
    await user.click(screen.getByRole('button', { name: '解析链接' }))
    await screen.findByText('AI 产品经理的下一站')

    await user.selectOptions(
      screen.getByRole('combobox', { name: '关联项目' }),
      'project-product'
    )
    await user.click(screen.getByRole('button', { name: '开始转写并整理' }))

    await waitFor(() => {
      expect(
        vi.mocked(contentImportsApi.createContentImport).mock.calls[0]?.[0]
      ).toEqual(expect.objectContaining({ project_ids: ['project-product'] }))
    })
  })

  it('submits an edited AI summary prompt', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(
      screen.getByLabelText('单集链接'),
      'https://www.xiaoyuzhoufm.com/episode/e1'
    )
    await user.click(screen.getByRole('button', { name: '解析链接' }))
    const prompt = await screen.findByRole('textbox', { name: 'AI 总结提示词' })
    await user.clear(prompt)
    await user.type(prompt, '重点总结可执行的产品策略，并保留结构化 JSON 字段。')
    await user.click(screen.getByRole('button', { name: '开始转写并整理' }))

    await waitFor(() => {
      expect(
        vi.mocked(contentImportsApi.createContentImport).mock.calls[0]?.[0]
      ).toEqual(
        expect.objectContaining({
          summary_prompt: '重点总结可执行的产品策略，并保留结构化 JSON 字段。',
        })
      )
    })
  })

  it('does not promise AI organization when the workspace text AI is disabled', async () => {
    vi.mocked(getRuntimeSettings).mockResolvedValue({
      workspace_id: 'workspace-1',
      mode: 'active',
      epoch: 1,
      binding_revision: 1,
      bindings: [
        {
          kind: 'llm_chat',
          mode: 'disabled',
          provider: 'unavailable',
          has_credentials: false,
          revision: 1,
        },
      ],
    })
    const user = userEvent.setup()
    renderDialog()

    await user.type(
      screen.getByLabelText('单集链接'),
      'https://www.xiaoyuzhoufm.com/episode/e1'
    )
    await user.click(screen.getByRole('button', { name: '解析链接' }))

    expect(await screen.findByText('AI 整理暂不可用')).toBeVisible()
    const aiSwitch = screen.getByRole('switch', { name: 'AI 整理' })
    expect(aiSwitch).toBeDisabled()
    expect(aiSwitch).toHaveAttribute('aria-checked', 'false')
    await user.click(screen.getByRole('button', { name: '开始转写' }))

    await waitFor(() => {
      expect(
        vi.mocked(contentImportsApi.createContentImport).mock.calls[0]?.[0]
      ).toEqual(
        expect.objectContaining({
          summarize_with_ai: false,
          include_transcript: true,
        })
      )
    })
  })
})
