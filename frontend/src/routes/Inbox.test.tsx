import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Inbox from './Inbox'
import { listTaskProjects } from '../api/tasks'
import {
  useBatchInbox,
  useConvertInboxItem,
  useDeleteInboxItem,
  useInboxList,
} from '../hooks/useInbox'

vi.mock('../api/tasks', () => ({
  listTaskProjects: vi.fn(),
}))

vi.mock('../hooks/useInbox', () => ({
  useInboxList: vi.fn(),
  useConvertInboxItem: vi.fn(),
  useDeleteInboxItem: vi.fn(),
  useBatchInbox: vi.fn(),
}))

const convertItemMock = vi.fn()
const refetchMock = vi.fn()

function renderInbox() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <Inbox />
    </QueryClientProvider>
  )
}

describe('Inbox task organizer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    convertItemMock.mockResolvedValue({})
    refetchMock.mockResolvedValue({})
    vi.mocked(listTaskProjects).mockResolvedValue([
      {
        id: 'personal',
        name: 'Personal',
        type: 'personal',
        description: '',
        created_at: 1,
        updated_at: 1,
      },
      {
        id: 'learning-1',
        name: '学习计划',
        type: 'learning',
        description: '',
        created_at: 2,
        updated_at: 2,
      },
    ])
    vi.mocked(useInboxList).mockReturnValue({
      data: {
        items: [
          {
            id: 'capture-1',
            kind: 'event',
            title: '明天晚上复习 N2',
            body: '原始备注',
            source: 'quick-capture',
            archived: 0,
            created_at: 1_788_683_400,
            updated_at: 1_788_683_400,
          },
        ],
        pagination: { page: 1, page_size: 100, total: 1, total_pages: 1 },
      },
      isLoading: false,
      error: null,
      refetch: refetchMock,
    } as unknown as ReturnType<typeof useInboxList>)
    vi.mocked(useConvertInboxItem).mockReturnValue({
      mutateAsync: convertItemMock,
      isPending: false,
    } as unknown as ReturnType<typeof useConvertInboxItem>)
    vi.mocked(useDeleteInboxItem).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof useDeleteInboxItem>)
    vi.mocked(useBatchInbox).mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof useBatchInbox>)
  })

  it('selects a real task project and submits the edited organizer fields', async () => {
    const user = userEvent.setup()
    renderInbox()

    const projectSelect = await screen.findByLabelText('项目')
    expect(
      await screen.findByRole('option', { name: 'Personal · 个人' })
    ).toBeVisible()
    expect(
      await screen.findByRole('option', { name: '学习计划 · 学习项目' })
    ).toBeVisible()

    await user.selectOptions(projectSelect, 'learning-1')
    await user.clear(screen.getByLabelText('标题'))
    await user.type(screen.getByLabelText('标题'), '复习 N2 语法')
    await user.clear(screen.getByLabelText('备注'))
    await user.type(screen.getByLabelText('备注'), '完成第三章练习')
    await user.selectOptions(screen.getByLabelText('优先级'), '2')
    await user.click(screen.getByRole('button', { name: '确认整理' }))

    expect(convertItemMock).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 'capture-1',
        kind: 'task',
        title: '复习 N2 语法',
        content: '完成第三章练习',
        project_id: 'learning-1',
        priority: 2,
      })
    )
  })
})
