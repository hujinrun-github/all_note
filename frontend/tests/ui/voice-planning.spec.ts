import { expect, test, type Page } from '@playwright/test'
import type { PlanDraft, PlanJob } from '../../src/api/planning'

const project = {
  id: 'personal',
  name: '个人事务',
  kind: 'standard',
  horizon: 'short',
  status: 'active',
  revision: 1,
}
const inbox = { ...project, id: 'inbox', name: '收件箱', system_role: 'inbox' }
const meta = {
  workspace_id: 'workspace-planning-ui',
  runtime_epoch: 1,
  task_model_version: 2,
  date: '2026-11-19',
  timezone: 'Asia/Shanghai',
  generated_at: '2026-11-19T02:00:00Z',
  read_consistency: 'planning_snapshot',
}
const facets = {
  today: 0,
  week: 0,
  month: 0,
  overdue: 0,
  unscheduled: 0,
  inbox: 0,
  completed_today: 0,
  completed_week: 0,
  completed_total: 0,
  draft: 0,
  recurring: 0,
}
const summary = {
  completed_task_count: 0,
  total_actionable_task_count: 0,
  open_occurrence_count: 0,
  blocked_occurrence_count: 0,
  has_open_ended_recurrence: false,
  next_occurrence: null,
  completion_percent: null,
}
function draft(): PlanDraft {
  return {
    id: 'draft-ui',
    revision: 1,
    status: 'input',
    input_text: '',
    transcript: '',
    timezone: 'Asia/Shanghai',
    reference_time: '2026-11-19T02:00:00Z',
    project_id: '',
    title: '',
    items: [],
    clarifications: [],
    assumptions: [],
    audio_available: false,
    created_at: '2026-11-19T02:00:00Z',
    updated_at: '2026-11-19T02:00:00Z',
  }
}
const proposedItems: PlanDraft['items'] = [
  {
    id: 'item-1',
    selected: true,
    title: '复习日语单词',
    description: '每天复习半小时',
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
  },
  {
    id: 'item-2',
    selected: true,
    title: '日语听力练习',
    description: '周三额外练习',
    priority: 0,
    schedule: {
      recurrence_type: 'none',
      timing_type: 'time_block',
      timezone: 'Asia/Shanghai',
      starts_on: '2026-11-25',
      local_start_time: '20:30',
      duration_minutes: 30,
    },
  },
]
async function fixture(page: Page, restore = false) {
  let current = draft()
  if (restore)
    current = {
      ...current,
      status: 'ready',
      project_id: 'personal',
      title: '尚未提交的学习计划',
      input_text: '下周学习日语',
      items: structuredClone(proposedItems),
    }
  let job: PlanJob | undefined
  let commitCount = 0
  let audioType = ''
  let commitBody: Record<string, unknown> = {}
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      const path = url.pathname
      const data = (value: unknown) => route.fulfill({ json: { data: value } })
      if (path === '/api/auth/me')
        return data({
          user: {
            id: 'u-plan',
            email: 'planning@example.test',
            display_name: '计划回归用户',
            role: 'user',
            status: 'active',
          },
          workspace: { id: meta.workspace_id, name: '测试工作区' },
          must_change_password: false,
        })
      if (path === '/api/task-domain/capabilities')
        return route.fulfill({ json: { model_version: 'v2', available: true } })
      if (path === '/api/planning/capabilities')
        return data({
          available: true,
          generation_available: true,
          transcription_available: true,
          max_audio_bytes: 20971520,
          max_duration_seconds: 180,
        })
      if (path === '/api/plan-drafts' && request.method() === 'GET')
        return data({
          drafts: restore && current.status !== 'committed' ? [current] : [],
        })
      if (path === '/api/plan-drafts' && request.method() === 'POST') {
        const body = request.postDataJSON()
        current = {
          ...current,
          input_text: body.input_text,
          project_id: body.project_id || '',
          timezone: body.timezone,
        }
        return data({ draft: current })
      }
      if (path === '/api/plan-drafts/draft-ui/audio') {
        if (request.method() === 'PUT') {
          audioType = request.headers()['content-type']
          current = {
            ...current,
            revision: current.revision + 1,
            audio_available: true,
          }
          return data({ draft: current })
        }
        return route.fulfill({
          contentType: 'audio/mp4',
          body: Buffer.from('audio-fixture'),
        })
      }
      if (/\/draft-ui\/(generate|transcribe)$/.test(path)) {
        const stage = path.endsWith('generate') ? 'generate' : 'transcribe'
        job = {
          id: `job-${stage}`,
          draft_id: current.id,
          stage,
          state: 'queued',
          retryable: false,
        }
        current = {
          ...current,
          revision: current.revision + 1,
          latest_job: job,
        }
        return data({ draft: current, job })
      }
      if (path.startsWith('/api/plan-jobs/')) {
        job = { ...job!, state: 'completed' }
        current = {
          ...current,
          revision: current.revision + 1,
          latest_job: job,
          ...(job.stage === 'transcribe'
            ? {
                input_text: '下周复习日语单词和听力',
                transcript: '下周复习日语单词和听力',
              }
            : {
                title: '下周日语计划',
                status: 'ready',
                items: structuredClone(proposedItems),
              }),
        }
        return data({ job })
      }
      if (path === '/api/plan-drafts/draft-ui/validate')
        return data({
          draft_revision: current.revision,
          issues: [],
          conflicts: ['2026-11-23 20:00 与已有安排重叠'],
        })
      if (path === '/api/plan-drafts/draft-ui/commit') {
        commitCount++
        commitBody = request.postDataJSON()
        current = { ...current, status: 'committed' }
        return data({
          receipt: {
            draft_id: current.id,
            project_id: 'personal',
            task_ids: current.items
              .filter((item) => item.selected)
              .map((_, index) => `task-${index + 1}`),
            occurrence_ids: ['occurrence-1'],
          },
        })
      }
      if (path === '/api/plan-drafts/draft-ui') {
        if (request.method() === 'PATCH')
          current = {
            ...current,
            ...request.postDataJSON(),
            revision: current.revision + 1,
          }
        return data({ draft: current })
      }
      if (path === '/api/workspace/overview')
        return data({
          ...meta,
          facets,
          today: [],
          projects: [{ ...project, summary }],
          notes: {
            state: 'available',
            authority: 'platform_compatibility',
            as_of: meta.generated_at,
            items: [],
          },
          partial: false,
        })
      if (path === '/api/projects/personal/overview')
        return data({ ...meta, project: { ...project, summary } })
      if (path === '/api/projects')
        return data({
          ...meta,
          projects:
            url.searchParams.get('include') === 'summary'
              ? [{ ...project, summary }]
              : [inbox, project],
        })
      if (path === '/api/projects/personal') return data({ project })
      if (path === '/api/tasks') return data({ tasks: [] })
      if (path === '/api/task-workbench/facets')
        return data({ ...meta, facets })
      if (path === '/api/task-occurrences') return data({ occurrences: [] })
      if (path === '/api/calendar/entries') return data({ entries: [] })
      if (path.startsWith('/api/tasks/task-'))
        return data({
          task: {
            id: 'task-1',
            project_id: 'personal',
            title: '复习 N2 单词',
            priority: 0,
            sort_order: 0,
            lifecycle_status: 'active',
            revision: 1,
            schedule_revision: 1,
          },
        })
      if (path === '/api/notes') return data({ notes: [] })
      return data({})
    }
  )
  return {
    commits: () => commitCount,
    audioType: () => audioType,
    commitBody: () => commitBody,
    errors,
  }
}

test('text plan preview, edits, conflict acknowledgement and one commit work responsively', async ({
  page,
}, testInfo) => {
  const api = await fixture(page)
  await page.goto('/')
  await page.getByRole('button', { name: '语音计划', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '语音与文字生成计划' })
  await expect(dialog).toBeVisible()
  await dialog
    .getByLabel('计划描述', { exact: true })
    .fill('安排下周日语学习，周一到周五八点复习单词半小时，周三练听力。')
  await dialog
    .getByRole('button', { name: '生成计划草稿', exact: true })
    .click()
  await expect(dialog.getByLabel('任务 1 标题', { exact: true })).toHaveValue(
    '复习日语单词'
  )
  expect(api.commits()).toBe(0)
  await dialog.getByLabel('任务 1 标题', { exact: true }).fill('复习 N2 单词')
  await dialog
    .getByRole('checkbox', { name: '选择任务 2', exact: true })
    .uncheck()
  await dialog.getByRole('button', { name: '检查安排', exact: true }).click()
  await expect(
    dialog.getByText('2026-11-23 20:00 与已有安排重叠')
  ).toBeVisible()
  const commit = dialog.getByRole('button', {
    name: '确认创建 1 个任务',
    exact: true,
  })
  await expect(commit).toBeDisabled()
  await dialog.getByRole('checkbox', { name: '我已核对，保留重叠安排' }).check()
  await expect(commit).toBeEnabled()
  const bounds = await dialog.boundingBox()
  expect(bounds!.width).toBeLessThanOrEqual(page.viewportSize()!.width)
  expect(
    await dialog.evaluate(
      (element) => element.scrollWidth <= element.clientWidth + 1
    )
  ).toBe(true)
  // Optional screenshot destination is supplied by the local QA run, never hardcoded.
  if (process.env.PLAN_QA_SCREENSHOTS && testInfo.project.name !== 'android') {
    await dialog.evaluate((element) => element.scrollTo(0, 0))
    await page.screenshot({
      path: `${process.env.PLAN_QA_SCREENSHOTS}-${testInfo.project.name}.png`,
    })
  }
  await commit.click()
  await expect(dialog.getByText('计划已创建', { exact: true })).toBeVisible()
  expect(api.commits()).toBe(1)
  expect(api.commitBody()).toMatchObject({
    publish: true,
    allow_conflicts: true,
  })
  await dialog.getByRole('link', { name: '查看本次创建' }).click()
  await expect(page).toHaveURL(
    /\/projects\/personal\?section=tasks&task_id=task-1$/
  )
  expect(api.errors).toEqual([])
})

test('project entry carries context and restores a saved plan before explicit new-project commit', async ({
  page,
}) => {
  const api = await fixture(page, true)
  await page.goto('/projects/personal')
  await page.getByRole('button', { name: '生成计划', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '语音与文字生成计划' })
  await expect(dialog.getByLabel('计划归属项目')).toHaveValue('personal')
  await dialog.getByRole('button', { name: '尚未提交的学习计划' }).click()
  await expect(dialog.getByLabel('任务 1 标题', { exact: true })).toHaveValue(
    '复习日语单词'
  )
  await dialog.getByLabel('计划归属项目').selectOption('__new__')
  await dialog.getByLabel('新项目名称').fill('新学习项目')
  await dialog.getByLabel('新项目类型').selectOption('learning')
  await dialog.getByLabel('计划创建方式').selectOption('draft')
  await dialog.getByRole('button', { name: '检查安排' }).click()
  await dialog.getByRole('checkbox', { name: '我已核对，保留重叠安排' }).check()
  await dialog.getByRole('button', { name: '保存为 2 个任务草稿' }).click()
  await expect(dialog.getByText('计划已创建', { exact: true })).toBeVisible()
  expect(api.commitBody()).toMatchObject({
    publish: false,
    new_project: { name: '新学习项目', kind: 'learning', horizon: 'short' },
  })
  expect(api.errors).toEqual([])
})

test('microphone denial retains the text plan path', async ({ page }) => {
  await fixture(page)
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'mediaDevices', {
      configurable: true,
      value: {
        getUserMedia: () =>
          Promise.reject(new DOMException('denied', 'NotAllowedError')),
      },
    })
    Object.defineProperty(window, 'MediaRecorder', {
      configurable: true,
      value: class {
        static isTypeSupported() {
          return true
        }
      },
    })
  })
  await page.goto('/')
  await page.getByRole('button', { name: '语音计划', exact: true }).click()
  await page.getByRole('button', { name: '开始录音', exact: true }).click()
  await expect(
    page.getByText('麦克风权限未获允许。你仍可输入文字生成计划。')
  ).toBeVisible()
  await page.getByLabel('计划描述', { exact: true }).fill('文字也可以生成安排')
  await expect(page.getByRole('button', { name: '生成计划草稿' })).toBeEnabled()
})

test('recorded audio keeps its browser MIME, transcribes, and releases microphone tracks', async ({
  page,
}, testInfo) => {
  const api = await fixture(page)
  const mime =
    testInfo.project.name === 'iphone' ? 'audio/mp4' : 'audio/webm;codecs=opus'
  await page.addInitScript((type) => {
    Object.defineProperty(navigator, 'mediaDevices', {
      configurable: true,
      value: {
        getUserMedia: async () => ({
          getTracks: () => [
            {
              stop() {
                document.documentElement.dataset.micStopped = 'true'
              },
            },
          ],
        }),
      },
    })
    class Recorder {
      static isTypeSupported(candidate: string) {
        return candidate === type
      }
      state = 'inactive'
      mimeType = type
      ondataavailable: ((event: { data: Blob }) => void) | null = null
      onstop: (() => void) | null = null
      start() {
        this.state = 'recording'
      }
      stop() {
        this.state = 'inactive'
        queueMicrotask(() => {
          this.ondataavailable?.({
            data: new Blob(['synthetic fixture audio'], { type }),
          })
          this.onstop?.()
        })
      }
    }
    Object.defineProperty(window, 'MediaRecorder', {
      configurable: true,
      value: Recorder,
    })
  }, mime)
  await page.goto('/')
  await page.getByRole('button', { name: '语音计划', exact: true }).click()
  await page.getByRole('button', { name: '开始录音', exact: true }).click()
  await page.getByRole('button', { name: '结束录音', exact: true }).click()
  await page.getByRole('button', { name: '上传并转写', exact: true }).click()
  await expect(page.getByLabel('计划描述', { exact: true })).toHaveValue(
    '下周复习日语单词和听力'
  )
  expect(api.audioType()).toBe(mime)
  await expect(page.locator('html')).toHaveAttribute('data-mic-stopped', 'true')
  expect(api.commits()).toBe(0)
  expect(api.errors).toEqual([])
})
