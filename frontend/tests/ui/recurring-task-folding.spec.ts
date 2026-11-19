import { expect, test, type Page } from '@playwright/test'
import type { OccurrenceV2, ProjectV2, TaskV2 } from '../../src/api/taskDomain'

const recurringTitle = '学习日语听力'
const projects: ProjectV2[] = [
  {
    id: 'learning',
    name: '日语N2考试',
    kind: 'learning',
    horizon: 'long',
    status: 'active',
    revision: 1,
  },
  {
    id: 'personal',
    name: '个人事务',
    kind: 'standard',
    horizon: 'short',
    status: 'active',
    revision: 1,
  },
  {
    id: 'inbox',
    name: '收件箱',
    kind: 'standard',
    horizon: 'short',
    status: 'active',
    system_role: 'inbox',
    revision: 1,
  },
]

async function fixture(page: Page) {
  await page.clock.setFixedTime(new Date('2026-11-19T04:00:00Z'))
  const tasks: TaskV2[] = [
    {
      id: 'listening',
      project_id: 'learning',
      title: recurringTitle,
      priority: 2,
      sort_order: 0,
      lifecycle_status: 'active',
      revision: 1,
      schedule_revision: 1,
    },
    {
      id: 'same-name',
      project_id: 'personal',
      title: recurringTitle,
      priority: 1,
      sort_order: 1,
      lifecycle_status: 'active',
      revision: 1,
      schedule_revision: 1,
    },
    {
      id: 'once',
      project_id: 'personal',
      title: '准备评审材料',
      priority: 0,
      sort_order: 2,
      lifecycle_status: 'active',
      revision: 1,
      schedule_revision: 1,
    },
  ]
  const occurrences: OccurrenceV2[] = [
    ['listening', 'listen-3', '2026-11-20'],
    ['same-name', 'other-1', '2026-11-17'],
    ['listening', 'listen-1', '2026-11-16'],
    ['once', 'once-1', '2026-11-20'],
    ['listening', 'listen-2', '2026-11-18'],
    ['same-name', 'other-2', '2026-11-19'],
  ].map(([taskID, id, date]) => {
    const task = tasks.find((item) => item.id === taskID)!
    return {
      id,
      task_id: taskID,
      project_id: task.project_id,
      title: task.title,
      occurrence_key: taskID === 'once' ? 'once' : date,
      execution_status: 'open',
      revision: 1,
      generated_schedule_revision: 1,
      task_revision: 1,
      schedule_revision: 1,
      planned_date: date,
      timing_type: 'date',
      timezone: 'Asia/Shanghai',
      recurrence_type: taskID === 'once' ? 'none' : 'daily',
      recurring: taskID !== 'once',
    }
  })
  const completions: Array<{ id: string; body: unknown }> = []
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('console', (message) => {
    // WebKit reports the existing Chromium-only viewport hint as a console
    // error. Keep all application errors, excluding only this known hint.
    if (
      message.type() === 'error' &&
      message.text() !==
        'Viewport argument key "interactive-widget" not recognized and ignored.'
    )
      errors.push(message.text())
  })
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      const path = url.pathname
      const data = (value: unknown) => route.fulfill({ json: { data: value } })
      if (path === '/api/task-domain/capabilities')
        return route.fulfill({ json: { model_version: 'v2', available: true } })
      if (path === '/api/auth/me')
        return data({
          user: {
            id: 'recurring-user',
            email: 'recurring@example.test',
            display_name: '任务回归用户',
            role: 'user',
            status: 'active',
          },
          workspace: { id: 'recurring-workspace', name: '任务测试工作区' },
          must_change_password: false,
        })
      if (path === '/api/projects') return data({ projects })
      if (path === '/api/task-workbench/facets') {
        const projectID = url.searchParams.get('project_id')
        const visible = occurrences.filter(
          (item) => !projectID || item.project_id === projectID
        )
        const open = visible.filter((item) => item.execution_status !== 'done')
        return data({
          workspace_id: 'recurring-workspace',
          task_model_version: 2,
          runtime_epoch: 1,
          date: '2026-11-19',
          timezone: 'Asia/Shanghai',
          generated_at: '2026-11-19T04:00:00Z',
          read_consistency: 'planning_snapshot',
          facets: {
            today: 1,
            week: open.length,
            month: open.length,
            overdue: 0,
            unscheduled: 0,
            inbox: 0,
            completed_today: visible.length - open.length,
            completed_week: visible.length - open.length,
            completed_total: visible.length - open.length,
            draft: 0,
            recurring: open.filter((item) => item.recurring).length,
          },
        })
      }
      if (path === '/api/tasks')
        return data({
          tasks:
            url.searchParams.get('lifecycle_status') === 'draft' ? [] : tasks,
        })
      if (path.startsWith('/api/tasks/'))
        return data({
          task: tasks.find((item) => item.id === path.split('/')[3]),
        })
      const completed = path.match(
        /^\/api\/task-occurrences\/([^/]+)\/complete$/
      )
      if (completed) {
        const item = occurrences.find((item) => item.id === completed[1])!
        completions.push({ id: item.id, body: request.postDataJSON() })
        item.execution_status = 'done'
        item.revision++
        const task = tasks.find((task) => task.id === item.task_id)!
        task.revision++
        occurrences
          .filter((other) => other.task_id === task.id)
          .forEach((other) => {
            other.task_revision = task.revision
          })
        return data({
          task_revision: task.revision,
          schedule_revision: 1,
          occurrence_revision: item.revision,
          occurrence_revisions: { [item.id]: item.revision },
        })
      }
      if (path === '/api/task-occurrences') {
        const taskID = url.searchParams.get('task_id')
        const projectID = url.searchParams.get('project_id')
        return data({
          occurrences: occurrences.filter((item) =>
            taskID
              ? item.task_id === taskID
              : (!projectID || item.project_id === projectID) &&
                (url.searchParams.get('execution_status') === 'done'
                  ? item.execution_status === 'done'
                  : item.execution_status !== 'done')
          ),
        })
      }
      if (path.startsWith('/api/task-occurrences/'))
        return data({
          occurrence: occurrences.find(
            (item) => item.id === path.split('/')[3]
          ),
        })
      return data({})
    }
  )
  return { completions, errors }
}

test('recurring tasks start folded, preserve distinct tasks, and expose individual completion', async ({
  page,
  isMobile,
}, info) => {
  const api = await fixture(page)
  await page.goto('/tasks?view=upcoming')
  await expect(
    page.getByRole('heading', { name: '任务', exact: true })
  ).toBeVisible()
  await expect(
    page.getByRole('tab', { name: '本周 6', exact: true })
  ).toBeVisible()
  const expand = page.getByRole('button', {
    name: `展开${recurringTitle}，3 次执行`,
    exact: true,
  })
  await expect(expand).toHaveAttribute('aria-expanded', 'false')
  await expect(
    page.getByRole('button', {
      name: `展开${recurringTitle}，2 次执行`,
      exact: true,
    })
  ).toBeVisible()
  await expect(
    page.getByRole('status').filter({ hasText: '当前显示 3 个任务 · 6 次执行' })
  ).toBeVisible()
  await expect(
    page.getByRole('button', {
      name: `完成${recurringTitle} · 11月16日`,
      exact: true,
    })
  ).toHaveCount(0)
  await expect(
    page.getByRole('button', { name: '完成准备评审材料', exact: true })
  ).toBeVisible()
  await expect(expand).toContainText('11月16日')
  await expect(expand).toContainText('11月20日')
  await expect(expand).toContainText('日语N2考试')
  expect(
    await page
      .locator('html')
      .evaluate((element) => element.scrollWidth <= window.innerWidth + 1)
  ).toBe(true)
  if (process.env.RECURRING_QA_SCREENSHOTS && info.project.name !== 'android') {
    await page.screenshot({
      path: `${process.env.RECURRING_QA_SCREENSHOTS}-${info.project.name}.png`,
    })
  }
  await expand.focus()
  await page.keyboard.press('Enter')
  const collapse = page.getByRole('button', {
    name: `收起${recurringTitle}，3 次执行`,
    exact: true,
  })
  await expect(collapse).toHaveAttribute('aria-expanded', 'true')
  // The existing mobile row opens its actions through the detail sheet.
  if (!isMobile)
    await expect(
      page.getByRole('button', {
        name: `改期${recurringTitle} · 11月16日`,
        exact: true,
      })
    ).toBeVisible()
  await collapse.focus()
  await page.keyboard.press('Space')
  await expect(expand).toHaveAttribute('aria-expanded', 'false')
  await expand.click()
  await page
    .getByRole('button', {
      name: `完成${recurringTitle} · 11月16日`,
      exact: true,
    })
    .click()
  await expect.poll(() => api.completions.length).toBe(1)
  expect(api.completions[0]).toMatchObject({
    id: 'listen-1',
    body: { expected_occurrence_revisions: { 'listen-1': 1 } },
  })
  await expect(
    page.getByRole('tab', { name: '本周 5', exact: true })
  ).toBeVisible()
  await expect(
    page.getByRole('button', {
      name: `收起${recurringTitle}，2 次执行`,
      exact: true,
    })
  ).toBeVisible()
  await expect(
    page.getByRole('status').filter({ hasText: '当前显示 3 个任务 · 5 次执行' })
  ).toBeVisible()
  expect(api.errors).toEqual([])
})

test('deep links reveal the selected occurrence and explicit collapse closes it', async ({
  page,
  isMobile,
}) => {
  const api = await fixture(page)
  await page.goto('/tasks?view=upcoming&occurrence_id=listen-3')
  // Mobile details use a native modal; inspect the auto-expanded list beneath
  // it without treating the inert background as an accessible click target.
  const collapse = page.locator('.td-occurrence-collection-toggle').filter({
    hasText: '日语N2考试',
  })
  await expect(collapse).toHaveAttribute('aria-expanded', 'true')
  await expect(
    page.locator('.td-occurrence-row[aria-current="true"]')
  ).toContainText(`${recurringTitle} · 11月20日`)
  await page.reload()
  await expect(collapse).toHaveAttribute('aria-expanded', 'true')
  if (isMobile) await page.keyboard.press('Escape')
  else await collapse.click()
  await expect(page).not.toHaveURL(/occurrence_id=/)
  await expect(
    page.getByRole('button', {
      name: `展开${recurringTitle}，3 次执行`,
      exact: true,
    })
  ).toHaveAttribute('aria-expanded', 'false')
  expect(api.errors).toEqual([])
})

test('filtering happens before grouping and does not change facet meanings', async ({
  page,
}) => {
  const api = await fixture(page)
  await page.goto('/tasks?view=upcoming')
  await page
    .getByRole('combobox', { name: '按项目筛选', exact: true })
    .selectOption('learning')
  await expect(page).toHaveURL(/project=learning/)
  await expect(
    page.getByRole('tab', { name: '本周 3', exact: true })
  ).toBeVisible()
  await expect(
    page.getByRole('status').filter({ hasText: '当前显示 1 个任务 · 3 次执行' })
  ).toBeVisible()
  await expect(
    page.getByRole('button', {
      name: `展开${recurringTitle}，2 次执行`,
      exact: true,
    })
  ).toHaveCount(0)
  await page
    .getByRole('combobox', { name: '按日期范围筛选', exact: true })
    .selectOption('next-7-days')
  await expect(page).toHaveURL(/date=next-7-days/)
  // Only the November 20 execution matches this refinement, so it stays flat.
  await expect(
    page.getByRole('button', {
      name: `完成${recurringTitle} · 11月20日`,
      exact: true,
    })
  ).toBeVisible()
  await expect(
    page.getByRole('button', { name: new RegExp(`展开${recurringTitle}`) })
  ).toHaveCount(0)
  await expect(
    page.getByRole('tab', { name: '本周 3', exact: true })
  ).toBeVisible()
  expect(api.errors).toEqual([])
})
