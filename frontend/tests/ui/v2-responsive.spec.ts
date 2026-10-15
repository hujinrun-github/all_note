import { expect, test, type Page } from '@playwright/test'

const title = '整理本周产品反馈，确认下一步改进计划'
const projects = [
  {
    id: 'inbox',
    name: '收件箱',
    kind: 'standard',
    horizon: 'short',
    status: 'active',
    system_role: 'inbox',
    revision: 1,
  },
  {
    id: 'personal',
    name: '个人事务',
    kind: 'standard',
    horizon: 'long',
    status: 'active',
    system_role: 'personal',
    revision: 1,
  },
  {
    id: 'learning',
    name: '日语学习',
    kind: 'learning',
    horizon: 'long',
    status: 'active',
    revision: 1,
  },
]
const task = {
  id: 'task-1',
  project_id: 'personal',
  title,
  priority: 1,
  sort_order: 0,
  lifecycle_status: 'active',
  revision: 1,
  schedule_revision: 1,
}
const occurrence = {
  id: 'occurrence-1',
  task_id: task.id,
  project_id: 'personal',
  title,
  occurrence_key: 'once',
  execution_status: 'open',
  revision: 1,
  generated_schedule_revision: 1,
  task_revision: 1,
  schedule_revision: 1,
  planned_date: '2026-10-02',
  timing_type: 'date',
  timezone: 'Asia/Shanghai',
}

async function fixtureAPI(page: Page) {
  await page.clock.setFixedTime(new Date('2026-10-02T10:00:00+08:00'))
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    async (route) => {
      const url = new URL(route.request().url())
      const path = url.pathname
      const data = (value: unknown) => route.fulfill({ json: { data: value } })
      if (path === '/api/task-domain/capabilities')
        return route.fulfill({ json: { model_version: 'v2', available: true } })
      if (path === '/api/auth/me')
        return data({
          user: {
            id: 'user',
            display_name: '小胡',
            email: 'test@example.com',
            role: 'user',
            status: 'active',
          },
          workspace: { id: 'ui-fixture', name: 'UI fixture' },
          must_change_password: false,
        })
      if (path === '/api/projects') return data({ projects })
      if (path === '/api/tasks' && route.request().method() === 'POST')
        return data({ task, occurrences: [occurrence] })
      if (path === '/api/tasks')
        return data({
          tasks:
            url.searchParams.get('lifecycle_status') === 'draft' ? [] : [task],
        })
      if (path === '/api/tasks/task-1') return data({ task })
      if (path === '/api/task-occurrences/occurrence-1')
        return data({ occurrence })
      if (path === '/api/task-occurrences')
        return data({
          occurrences:
            ['overdue', 'completed'].includes(
              url.searchParams.get('scope') ?? ''
            ) || url.searchParams.get('project_id') === 'inbox'
              ? []
              : [occurrence],
        })
      if (path === '/api/calendar/entries')
        return data({
          entries: [
            {
              ...occurrence,
              project_revision: 1,
              task_title: title,
              occurrence_id: occurrence.id,
              occurrence_revision: 1,
              recurring: false,
              all_day_end_date: '2026-10-03',
            },
          ],
        })
      if (path === '/api/notes')
        return route.fulfill({
          json: {
            data: {
              notes: [
                {
                  id: 'note-1',
                  title: '产品反馈记录',
                  body: '整理本周的用户反馈和行动项。',
                  tags: '["工作"]',
                  projects: [],
                  created_at: 1790906400,
                  updated_at: 1790906400,
                },
              ],
            },
            pagination: { page: 1, page_size: 100, total: 1 },
          },
        })
      return route.fulfill({
        status: 404,
        json: { error: { code: 'UNMOCKED', message: path } },
      })
    }
  )
}

async function expectNoPageOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const main = document.querySelector('main')!
    return {
      document: document.documentElement.scrollWidth - innerWidth,
      main: main.scrollWidth - main.clientWidth,
    }
  })
  expect(overflow.document).toBeLessThanOrEqual(1)
  expect(overflow.main).toBeLessThanOrEqual(1)
}

test.beforeEach(async ({ page }) => fixtureAPI(page))

test('desktop pages keep navigation and content usable at laptop and wide widths', async ({
  page,
  isMobile,
}, info) => {
  test.skip(isMobile, 'Desktop layout coverage')
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    for (const [path, heading, filename] of [
      ['/', '今天', 'today'],
      ['/tasks', '任务', 'tasks'],
      ['/notes', '笔记库', 'notes'],
      ['/calendar', '日历', 'calendar'],
    ]) {
      await page.goto(path)
      await expect(
        page.getByRole('heading', { name: heading, exact: true })
      ).toBeVisible()
      if (path === '/') {
        await expect(
          page.getByRole('button', { name: /今日待办\s*1\s*项/ })
        ).toBeVisible()
        await expect(page.getByRole('link', { name: '管理项目' })).toBeVisible()
      }
      if (path === '/notes')
        await expect(
          page.getByRole('heading', { name: '产品反馈记录' })
        ).toBeVisible()
      if (path === '/calendar')
        await expect(page.getByText('正在更新…')).not.toBeVisible()
      await expectNoPageOverflow(page)
      await page.screenshot({
        path: info.outputPath(`desktop-${filename}-${width}.png`),
      })
    }
  }
})

test('responsive navigation, task details, focus return and calendar editing', async ({
  page,
  isMobile,
}, info) => {
  await page.goto('/')
  const row = page.getByRole('listitem').filter({ hasText: title }).first()
  await expect(row).toBeVisible()
  await expectNoPageOverflow(page)
  await page.screenshot({ path: info.outputPath('today.png') })
  await row.click()
  const panel = page.getByRole(isMobile ? 'dialog' : 'complementary', {
    name: `执行详情：${title}`,
  })
  await expect(panel).toBeVisible()
  await page.screenshot({ path: info.outputPath('task-detail.png') })
  if (isMobile) {
    await expect(page.locator('dialog')).toHaveJSProperty('open', true)
    await page.keyboard.press('Escape')
    await expect(panel).not.toBeVisible()
    await expect(row).toBeFocused()
  } else await panel.getByRole('button', { name: '关闭执行详情' }).click()

  if (isMobile) {
    await page.getByRole('button', { name: '更多导航' }).click()
    const menu = page.getByRole('dialog', { name: '全部功能' })
    await expect(menu.getByRole('link', { name: '账号管理' })).toHaveCount(0)
    await page.screenshot({ path: info.outputPath('navigation.png') })
    await menu.getByRole('link', { name: '项目', exact: true }).click()
    await expect(page).toHaveURL(/\/projects$/)
    await expect(menu).not.toBeVisible()
    await expectNoPageOverflow(page)
  }
  await page
    .getByRole('navigation', {
      name: isMobile ? '手机主导航' : '主导航',
      exact: true,
    })
    .getByRole('link', { name: '日历', exact: true })
    .click()
  if (isMobile)
    await expect(
      page.getByRole('button', { name: '清单', exact: true })
    ).toHaveAttribute('aria-pressed', 'true')
  else await page.getByRole('button', { name: '清单', exact: true }).click()
  await expectNoPageOverflow(page)
  await page.screenshot({ path: info.outputPath('calendar.png') })
  await page.getByRole('button', { name: `编辑日程：${title}` }).click()
  await expect(
    page.getByRole('dialog', { name: `编辑日程：${title}` })
  ).toBeVisible()
  await page.getByRole('button', { name: '关闭日程设置' }).click()
})

test('capture starts empty, keeps focus contained, and confirms successful creation', async ({
  page,
  isMobile,
}, info) => {
  await page.goto('/')
  await page.getByRole('button', { name: '快速捕获', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '快速捕获任务', exact: true })
  const input = dialog.getByRole('textbox', { name: '快速捕获任务标题' })
  await expect(input).toHaveValue('')
  await expect(input).toBeFocused()
  await expect(dialog.getByRole('button', { name: '创建任务' })).toBeDisabled()
  await input.fill('准备明天的项目评审')
  await page.screenshot({ path: info.outputPath('capture.png') })
  for (let i = 0; i < 12; i++) await page.keyboard.press('Tab')
  expect(
    await dialog.evaluate((el) => el.contains(document.activeElement))
  ).toBe(true)
  const request = page.waitForRequest(
    (req) => req.url().endsWith('/api/tasks') && req.method() === 'POST'
  )
  await dialog.getByRole('button', { name: '创建任务' }).click()
  expect((await request).postDataJSON()).toMatchObject({
    title: '准备明天的项目评审',
    project_id: 'inbox',
  })
  await expect(dialog).not.toBeVisible()
  await expect(page.getByRole('status')).toHaveText('任务已添加到「收件箱」')
  if (isMobile) {
    await page
      .getByRole('navigation', { name: '手机主导航' })
      .getByRole('link', { name: '笔记' })
      .click()
    await expect(page.getByRole('heading', { name: '笔记库' })).toBeVisible()
    await expectNoPageOverflow(page)
    await page.screenshot({ path: info.outputPath('notes.png') })
  } else {
    await page.keyboard.press('ControlOrMeta+k')
    await expect(
      page.getByRole('textbox', { name: '搜索笔记、任务、日程' })
    ).toBeFocused()
  }
})

test('task failure stays visible inside the detail panel', async ({
  page,
  isMobile,
}) => {
  await page.route('**/api/task-occurrences/occurrence-1/complete', (route) =>
    route.fulfill({
      status: 500,
      json: { error: { code: 'FAILED', message: 'offline' } },
    })
  )
  await page.goto('/')
  await page.getByRole('listitem').filter({ hasText: title }).first().click()
  const panel = page.getByRole(isMobile ? 'dialog' : 'complementary', {
    name: `执行详情：${title}`,
  })
  await panel.getByRole('button', { name: '完成', exact: true }).click()
  await expect(panel.getByRole('alert')).toContainText('任务操作失败')
})

test('small screens keep navigation and long forms reachable', async ({
  page,
}, info) => {
  await page.setViewportSize({ width: 320, height: 568 })
  await page.goto('/')
  await expect(
    page.getByRole('heading', { name: '今天', exact: true })
  ).toBeVisible()
  await expectNoPageOverflow(page)
  await page.getByRole('button', { name: '快速捕获', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '快速捕获任务', exact: true })
  await dialog
    .getByRole('textbox', { name: '快速捕获任务标题' })
    .fill('安排评审')
  await dialog
    .getByRole('combobox', { name: '任务安排方式' })
    .selectOption('time_block')
  await page.setViewportSize({ width: 320, height: 360 })
  expect(
    await dialog.evaluate((el) => el.scrollWidth - el.clientWidth)
  ).toBeLessThanOrEqual(1)
  const submit = dialog.getByRole('button', { name: '创建任务' })
  await submit.scrollIntoViewIfNeeded()
  await expect(submit).toBeInViewport()
  await page.screenshot({ path: info.outputPath('small-form.png') })
  await page.keyboard.press('Escape')
  await page.setViewportSize({ width: 320, height: 568 })
  await page
    .getByRole('navigation', { name: '手机主导航' })
    .getByRole('link', { name: '笔记' })
    .click()
  await expect(page.getByRole('heading', { name: '笔记库' })).toBeVisible()
  await expectNoPageOverflow(page)
  await page.screenshot({ path: info.outputPath('small-notes.png') })
  const topbar = await page.locator('.workspace-topbar').boundingBox()
  const actions = await page.locator('.notes-page-actions').boundingBox()
  expect(actions!.y).toBeGreaterThanOrEqual(topbar!.y + topbar!.height)
  await expect(
    page.getByRole('button', { name: '筛选笔记 全部笔记' })
  ).toHaveAttribute('aria-expanded', 'false')
  await page.locator('.note-library-select').first().click()
  const preview = page.getByRole('dialog', { name: '笔记预览：产品反馈记录' })
  await expect(preview).toBeVisible()
  await preview.getByRole('button', { name: '关闭笔记预览' }).click()
  await expect(preview).not.toBeVisible()
})
