import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { QuickCapture } from './QuickCapture'
import * as eventsApi from '../api/events'
import * as inboxApi from '../api/inbox'
import * as notesApi from '../api/notes'
import * as tasksApi from '../api/tasks'

vi.mock('../api/events')
vi.mock('../api/inbox')
vi.mock('../api/notes')
vi.mock('../api/tasks')
vi.mock('../hooks/useTaskDomain', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../hooks/useTaskDomain')>()),
  useTaskDomainCapabilities: () => ({
    data: { model_version: 'legacy', available: true },
    isLoading: false,
    isError: false,
  }),
}))

const projects: tasksApi.TaskProject[] = [
  {
    id: 'personal',
    name: '个人',
    type: 'personal',
    description: '',
    created_at: 1,
    updated_at: 1,
  },
  {
    id: 'learning-1',
    name: '学习写小说',
    type: 'learning',
    description: '',
    created_at: 1,
    updated_at: 1,
  },
]

function renderQuickCapture() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <QuickCapture />
    </QueryClientProvider>,
  )
}

function tomorrowDateValue() {
  const tomorrow = new Date()
  tomorrow.setDate(tomorrow.getDate() + 1)
  const year = tomorrow.getFullYear()
  const month = String(tomorrow.getMonth() + 1).padStart(2, '0')
  const day = String(tomorrow.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

describe('QuickCapture', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(tasksApi.listTaskProjects).mockResolvedValue(projects)
    vi.mocked(eventsApi.createEvent).mockResolvedValue({
      id: 'event-1',
      title: '明天晚上8点复习N2语法',
      start_time: 1,
      end_time: 2,
      kind: 'work',
      created_at: 1,
      updated_at: 1,
    })
    vi.mocked(tasksApi.createTask).mockResolvedValue({
      id: 'task-1',
      title: '明天晚上8点复习N2语法',
      content: '',
      project_id: 'personal',
      priority: 0,
      done: 0,
      status: 'open',
      horizon: 'week',
      scope: 'daily',
      sort_order: 0,
      created_at: 1,
      updated_at: 1,
    })
    vi.mocked(notesApi.createNote).mockResolvedValue({
      id: 'note-1',
      title: '明天晚上8点复习N2语法',
      body: '',
      folder_id: '__uncategorized',
      tags: '[]',
      projects: [],
      created_at: 1,
      updated_at: 1,
    })
    vi.mocked(inboxApi.createInboxItem).mockResolvedValue({
      id: 'inbox-1',
      kind: 'event',
      title: '明天晚上8点复习N2语法',
      source: 'manual',
      archived: 0,
      created_at: 1,
      updated_at: 1,
    })
  })

  it('lets users edit recognized time and project instead of showing fake readonly data', async () => {
    renderQuickCapture()

    const selectedDate = tomorrowDateValue()

    const projectSelect = await screen.findByLabelText('捕获项目')
    const dateInput = screen.getByLabelText('捕获日期')
    const timeInput = screen.getByLabelText('捕获时间')
    await screen.findByRole('option', { name: '个人 · 个人' })
    expect(dateInput).not.toHaveAttribute('readonly')
    expect(timeInput).not.toHaveAttribute('readonly')
    expect(projectSelect).toHaveDisplayValue('个人 · 个人')

    fireEvent.change(dateInput, { target: { value: selectedDate } })
    fireEvent.change(timeInput, { target: { value: '15:30' } })
    await userEvent.selectOptions(projectSelect, 'learning-1')

    expect(dateInput).toHaveValue(selectedDate)
    expect(timeInput).toHaveValue('15:30')
    expect(projectSelect).toHaveValue('learning-1')
    expect(screen.getByText(/直接创建会写入日历/)).toBeVisible()
  })

  it('directly creates an event with the selected time and project-derived calendar kind', async () => {
    renderQuickCapture()
    const user = userEvent.setup()
    const selectedDate = tomorrowDateValue()
    const projectSelect = await screen.findByLabelText('捕获项目')
    const dateInput = screen.getByLabelText('捕获日期')
    const timeInput = screen.getByLabelText('捕获时间')

    fireEvent.change(dateInput, { target: { value: selectedDate } })
    fireEvent.change(timeInput, { target: { value: '15:30' } })
    await user.selectOptions(projectSelect, 'learning-1')
    await user.click(screen.getByRole('button', { name: '直接创建' }))

    const startTime = Math.floor(new Date(`${selectedDate}T15:30`).getTime() / 1000)
    await waitFor(() => expect(eventsApi.createEvent).toHaveBeenCalled())
    expect(vi.mocked(eventsApi.createEvent).mock.calls[0]?.[0]).toEqual({
      title: '明天晚上8点复习N2语法',
      start_time: startTime,
      end_time: startTime + 60 * 60,
      location: '学习写小说 · 学习项目',
      kind: 'work',
      project_id: 'learning-1',
    })
  })
})
