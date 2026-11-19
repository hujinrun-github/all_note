import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import * as planning from '../../api/planning'
import { useUIStore } from '../../stores/ui'
import PlanCapture from './PlanCapture'

vi.mock('../../api/planning')
vi.mock('../../hooks/useTaskDomain', () => ({
  useProjects: () => ({
    data: [
      {
        id: 'p1',
        name: '日语学习',
        status: 'planning',
        kind: 'learning',
        horizon: 'long',
      },
    ],
    isLoading: false,
    isError: false,
  }),
  taskDomainQueryKeys: { all: ['task-domain'] },
}))
const item: planning.PlanItem = {
  id: 'i1',
  selected: true,
  title: '复习单词',
  description: '每天半小时',
  priority: 0,
  schedule: {
    recurrence_type: 'weekly',
    timing_type: 'time_block',
    timezone: 'Asia/Shanghai',
    starts_on: '2026-11-23',
    ends_on: '2026-11-27',
    local_start_time: '20:00',
    duration_minutes: 30,
    rule: { interval: 1, weekdays: [1, 2, 3, 4, 5] },
  },
}
function baseDraft(): planning.PlanDraft {
  return {
    id: 'd1',
    revision: 1,
    status: 'input',
    input_text: '',
    transcript: '',
    timezone: 'Asia/Shanghai',
    reference_time: '2026-11-19T10:00:00+08:00',
    project_id: '',
    title: '',
    items: [],
    clarifications: [],
    assumptions: [],
    audio_available: false,
    created_at: '',
    updated_at: '',
  }
}
let saved: planning.PlanDraft
let queryClient: QueryClient
let jobState: planning.PlanJob['state']
function mount() {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <PlanCapture />
      </QueryClientProvider>
    </MemoryRouter>
  )
}
beforeEach(() => {
  vi.resetAllMocks()
  saved = baseDraft()
  jobState = 'completed'
  useUIStore.setState({
    captureOpen: true,
    captureMode: 'plan',
    captureProjectID: '',
  })
  vi.mocked(planning.newPlanningRequestID).mockImplementation(() =>
    crypto.randomUUID()
  )
  vi.mocked(planning.getPlanningCapabilities).mockResolvedValue({
    available: true,
    generation_available: true,
    transcription_available: true,
    max_audio_bytes: 20971520,
    max_duration_seconds: 180,
  })
  vi.mocked(planning.listPlanDrafts).mockResolvedValue([])
  vi.mocked(planning.createPlanDraft).mockImplementation(async (input) => {
    saved = {
      ...saved,
      input_text: input.input_text,
      project_id: input.project_id || '',
    }
    return structuredClone(saved)
  })
  vi.mocked(planning.updatePlanDraft).mockImplementation(async (_, input) => {
    saved = { ...saved, ...input, revision: saved.revision + 1 }
    return structuredClone(saved)
  })
  vi.mocked(planning.startPlanJob).mockImplementation(async (_, stage) => {
    const job: planning.PlanJob = {
      id: 'j1',
      draft_id: 'd1',
      stage,
      state: 'queued',
      retryable: false,
    }
    saved = { ...saved, latest_job: job, revision: saved.revision + 1 }
    return { draft: structuredClone(saved), job }
  })
  vi.mocked(planning.getPlanJob).mockImplementation(async () => {
    const job: planning.PlanJob = {
      id: 'j1',
      draft_id: 'd1',
      stage: 'generate',
      state: jobState,
      retryable: false,
    }
    if (jobState === 'completed')
      saved = {
        ...saved,
        revision: saved.revision + 1,
        title: '下周学习',
        items: [structuredClone(item)],
        latest_job: job,
        status: 'ready',
      }
    return job
  })
  vi.mocked(planning.getPlanDraft).mockImplementation(async () =>
    structuredClone(saved)
  )
  vi.mocked(planning.validatePlanDraft).mockImplementation(async () => ({
    draft_revision: saved.revision,
    issues: [],
    conflicts: [],
  }))
  vi.mocked(planning.commitPlanDraft).mockResolvedValue({
    draft_id: 'd1',
    project_id: 'p1',
    task_ids: ['t1'],
    occurrence_ids: ['o1'],
  })
})

it('generates an editable proposal and requires validation/conflict acknowledgement before one commit', async () => {
  vi.mocked(planning.validatePlanDraft).mockImplementation(async () => ({
    draft_revision: saved.revision,
    issues: [],
    conflicts: ['周一 20:00 与现有安排重叠'],
  }))
  mount()
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('计划描述'), '下周每晚八点复习单词')
  await user.click(screen.getByRole('button', { name: '生成计划草稿' }))
  expect(await screen.findByLabelText('任务 1 标题')).toHaveValue('复习单词')
  expect(planning.commitPlanDraft).not.toHaveBeenCalled()
  await user.clear(screen.getByLabelText('任务 1 标题'))
  await user.type(screen.getByLabelText('任务 1 标题'), '复习 N2 单词')
  await user.click(screen.getByRole('button', { name: '检查安排' }))
  await screen.findByText('周一 20:00 与现有安排重叠')
  const commit = screen.getByRole('button', { name: '确认创建 1 个任务' })
  expect(commit).toBeDisabled()
  await user.click(
    screen.getByRole('checkbox', { name: '我已核对，保留重叠安排' })
  )
  await user.dblClick(commit)
  expect(await screen.findByText('计划已创建')).toBeVisible()
  expect(planning.commitPlanDraft).toHaveBeenCalledTimes(1)
  expect(planning.commitPlanDraft).toHaveBeenCalledWith(
    'd1',
    expect.objectContaining({ allow_conflicts: true, publish: true })
  )
  expect(screen.getByRole('link', { name: '查看本次创建' })).toHaveAttribute(
    'href',
    '/projects/p1?section=tasks&task_id=t1'
  )
})

it('restores a durable draft with project context and supports explicit new project creation', async () => {
  saved = {
    ...baseDraft(),
    project_id: 'p1',
    title: '继续学习计划',
    items: [structuredClone(item)],
    status: 'ready',
  }
  vi.mocked(planning.listPlanDrafts).mockResolvedValue([saved])
  mount()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: '继续学习计划' }))
  expect(screen.getByLabelText('计划归属项目')).toHaveValue('p1')
  expect(
    screen.getByText('“创建并开始执行”将启用当前规划中的项目。')
  ).toBeVisible()
  await user.selectOptions(screen.getByLabelText('计划归属项目'), '__new__')
  await user.type(screen.getByLabelText('新项目名称'), '十一月学习')
  await user.selectOptions(screen.getByLabelText('新项目类型'), 'learning')
  await user.click(screen.getByRole('button', { name: '检查安排' }))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: '确认创建 1 个任务' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: '确认创建 1 个任务' }))
  await screen.findByText('计划已创建')
  expect(planning.commitPlanDraft).toHaveBeenCalledWith(
    'd1',
    expect.objectContaining({
      new_project: { name: '十一月学习', kind: 'learning', horizon: 'short' },
    })
  )
})

it('preserves user edits when an older generation completes', async () => {
  jobState = 'processing'
  mount()
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('计划描述'), '旧目标')
  await user.click(screen.getByRole('button', { name: '生成计划草稿' }))
  await waitFor(() => expect(planning.getPlanJob).toHaveBeenCalled())
  await user.clear(screen.getByLabelText('计划描述'))
  await user.type(screen.getByLabelText('计划描述'), '更新后的目标')
  jobState = 'completed'
  await act(() =>
    queryClient.refetchQueries({ queryKey: ['planning', 'job', 'j1'] })
  )
  await screen.findByText('处理已结束，你编辑的内容已保留；保存后可重新生成。')
  expect(screen.getByLabelText('计划描述')).toHaveValue('更新后的目标')
  expect(screen.queryByLabelText('任务 1 标题')).not.toBeInTheDocument()
})

it('keeps text input available after microphone permission denial and confirms unsaved close', async () => {
  Object.defineProperty(navigator, 'mediaDevices', {
    configurable: true,
    value: {
      getUserMedia: vi
        .fn()
        .mockRejectedValue(new DOMException('denied', 'NotAllowedError')),
    },
  })
  vi.stubGlobal(
    'MediaRecorder',
    class {
      static isTypeSupported() {
        return true
      }
    }
  )
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
  mount()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: '开始录音' }))
  await screen.findByText('麦克风权限未获允许。你仍可输入文字生成计划。')
  await user.type(screen.getByLabelText('计划描述'), '仍然可以打字')
  expect(screen.getByRole('button', { name: '生成计划草稿' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: '关闭计划面板' }))
  expect(confirm).toHaveBeenCalled()
  expect(useUIStore.getState().captureOpen).toBe(true)
  vi.unstubAllGlobals()
})

it('automatically saves first text input so it can be restored after refresh', async () => {
  mount()
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('计划描述'), '自动保留的文字计划')
  await waitFor(
    () =>
      expect(planning.createPlanDraft).toHaveBeenCalledWith(
        expect.objectContaining({ input_text: '自动保留的文字计划' })
      ),
    { timeout: 2500 }
  )
  await screen.findByText('草稿已保存，可关闭后继续')
})
