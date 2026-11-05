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
            id: 'web-regression-user',
            display_name: '网页回归用户',
            email: 'web-regression@example.test',
            role: 'user',
            status: 'active',
          },
          workspace: {
            id: 'workspace-web-regression',
            name: '网页回归工作区',
          },
          must_change_password: false,
        })
      if (path === '/api/projects/personal')
        return data({
          project: projects.find((project) => project.id === 'personal'),
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
      if (path === '/api/search')
        return route.fulfill({
          json: {
            data: {
              items: [
                { id: task.id, type: 'task', title, updated_at: 0 },
              ],
            },
            pagination: { page: 1, page_size: 20, total: 1 },
          },
        })
      if (path === '/api/summary')
        return route.fulfill({
          json: {
            data: {
              groups: [
                {
                  date: '2026-10-02',
                  count: 1,
                  tasks: [
                    {
                      id: 'summary-task-1',
                      title: '完成网页回归检查',
                      done: 1,
                      project: { id: 'personal', name: '个人事务' },
                    },
                  ],
                },
              ],
              active_days: 1,
              project_count: 1,
            },
            pagination: { page: 1, page_size: 50, total: 1 },
          },
        })
      if (path === '/api/today')
        return data({
          todayTasks: [],
          overdueTasks: [],
          events: [],
          recentNotes: [],
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

test('desktop sidebar and account controls open the expected destinations', async ({
  page,
  isMobile,
}) => {
  test.skip(isMobile, 'Desktop navigation coverage')
  await page.goto('/')

  const navigation = page.getByRole('navigation', {
    name: '主导航',
    exact: true,
  })
  const destinations = [
    ['今日', '/', '今天'],
    ['任务', '/tasks', '任务'],
    ['项目', '/projects', '项目'],
    ['日历', '/calendar', '日历'],
    ['未整理', '/inbox', '未整理'],
    ['笔记', '/notes', '笔记库'],
    ['搜索', '/search', '全局搜索'],
    ['每日总结', '/summary', '每日总结'],
  ] as const

  for (const [label, path, heading] of destinations) {
    await navigation.getByRole('link', { name: label, exact: true }).click()
    await expect(page).toHaveURL(
      new RegExp(`${path === '/' ? '/$' : `${path}$`}`)
    )
    await expect(
      page.getByRole('heading', { name: heading, exact: true }).first()
    ).toBeVisible()
  }

  await page.getByRole('button', { name: '收起侧边栏' }).click()
  await expect(page.getByRole('button', { name: '展开侧边栏' })).toBeVisible()
  await page.getByRole('button', { name: '展开侧边栏' }).click()

  await page.getByRole('button', { name: '打开账号菜单' }).click()
  const accountMenu = page.getByRole('menu', { name: '账号菜单' })
  await expect(accountMenu.getByText('网页回归用户')).toBeVisible()
  await expect(
    accountMenu.getByText('web-regression@example.test')
  ).toBeVisible()
  await expect(
    accountMenu.getByRole('menuitem', { name: '用户设置' })
  ).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(accountMenu).not.toBeVisible()
})

test('project list opens project details and primary controls show their panels', async ({
  page,
}) => {
  await page.goto('/projects')
  await page.getByRole('link', { name: '打开个人事务' }).click()

  await expect(page).toHaveURL(/\/projects\/personal$/)
  await expect(
    page.getByRole('heading', { name: '个人事务', exact: true })
  ).toBeVisible()
  await expect(page.getByText('Unexpected Application Error!')).toHaveCount(0)
  await page.reload()
  await expect(page.getByRole('heading', { name: '个人事务', exact: true })).toBeVisible()

  await page.getByRole('tab', { name: /任务\s*1/ }).click()
  await expect(page.getByRole('heading', { name: title }).first()).toBeVisible()

  await page.getByRole('button', { name: '打开添加任务' }).click()
  await expect(page.getByRole('textbox', { name: '任务标题' })).toBeFocused()
  await page.getByRole('button', { name: '取消' }).click()
  await expect(page.getByRole('textbox', { name: '任务标题' })).toHaveCount(0)

  await page.getByRole('button', { name: '项目操作' }).click()
  await page.getByRole('menuitem', { name: /编辑项目信息/ }).click()
  const editDialog = page.getByRole('dialog', { name: '编辑项目信息' })
  await expect(
    editDialog.getByRole('textbox', { name: '项目名称' })
  ).toHaveValue('个人事务')
  await editDialog.getByRole('button', { name: '取消' }).click()
  await expect(editDialog).not.toBeVisible()

  await page.getByRole('button', { name: '完成项目' }).click()
  await expect(
    page.getByRole('dialog', { name: '处理未完成执行实例' })
  ).toBeVisible()
})

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
      if (path === '/calendar') {
        await expect(page.getByRole('combobox', { name: '显示时区' })).toBeVisible()
        await expect(page.getByText('正在更新…')).not.toBeVisible()
      }
      await expectNoPageOverflow(page)
      await page.screenshot({
        path: info.outputPath(`desktop-${filename}-${width}.png`),
      })
    }
  }
})

test('search links to the right project and review controls stay usable on every device', async ({ page }, info) => {
  await page.goto('/search')
  const search = page.getByRole('searchbox', { name: '全局搜索' })
  await expect(search).toBeFocused()
  await search.fill('反馈')
  await search.press('Enter')
  await expect(page).toHaveURL(/q=/)
  const tabs = page.getByRole('tablist', { name: '结果类型' })
  await tabs.getByRole('tab', { name: '任务', exact: true }).click()
  await expect(page.getByRole('button', { name: `打开任务${title}` })).toBeVisible()
  await expect(page.getByRole('button', { name: '打开项目个人事务' })).toHaveCount(0)
  await tabs.getByRole('tab', { name: '项目', exact: true }).click()
  await search.fill('个人')
  await search.press('Enter')
  await expect(page.getByRole('button', { name: '打开项目个人事务' })).toBeVisible()
  await expectNoPageOverflow(page)
  await page.screenshot({ path: info.outputPath('workspace-search.png') })
  await page.getByRole('button', { name: '打开项目个人事务' }).click()
  await expect(page).toHaveURL(/\/projects\/personal$/)
  await expect(page.locator('#project-detail-heading')).toHaveText('个人事务')

  await page.goto('/inbox')
  await expect(page.getByRole('heading', { name: '未整理', exact: true })).toBeVisible()
  await page.getByRole('button', { name: /^进行中/ }).click()
  await expect(page.getByRole('button', { name: /^进行中/ })).toHaveAttribute('aria-pressed', 'true')
  await expectNoPageOverflow(page)

  await page.goto('/summary')
  await expect(page.getByRole('heading', { name: '每日总结', exact: true })).toBeVisible()
  const month = page.getByRole('button', { name: '本月', exact: true })
  await month.click()
  await expect(month).toHaveAttribute('aria-pressed', 'true')
  await expect(page).toHaveURL(/from=2026-10-01/)
  await expect(page.getByRole('button', { name: /完成网页回归检查/ })).toBeVisible()
  await expectNoPageOverflow(page)
  await page.getByRole('button', { name: '查看笔记库' }).click()
  await expect(page).toHaveURL(/\/notes$/)
  await expect(page.getByRole('heading', { name: '笔记库' })).toBeVisible()
})

test('planning overview links, keyboard tabs and task filters stay in sync with the URL', async ({
  page,
}, info) => {
  await page.goto('/')
  const weekMetric = page.getByRole('button', { name: /本周安排/ })
  await weekMetric.click()
  await expect(page).toHaveURL(/\?view=week$/)
  await expect(weekMetric).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('tab', { name: /^本周/ })).toHaveAttribute(
    'aria-selected',
    'true'
  )

  await page.getByRole('link', { name: /还没安排的任务/ }).click()
  await expect(page).toHaveURL(/\/tasks\?view=unscheduled$/)
  const unscheduled = page.getByRole('tab', { name: /^无日期/ })
  const recurring = page.getByRole('tab', { name: /^重复/ })
  await expect(unscheduled).toHaveAttribute('aria-selected', 'true')
  await unscheduled.focus()
  await page.keyboard.press('ArrowRight')
  await expect(recurring).toBeFocused()
  await expect(unscheduled).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/tasks\?view=recurring$/)
  await expect(recurring).toHaveAttribute('aria-selected', 'true')
  await page.goBack()
  await expect(unscheduled).toHaveAttribute('aria-selected', 'true')

  const project = page.getByRole('combobox', { name: '按项目筛选' })
  const priority = page.getByRole('combobox', { name: '按优先级筛选' })
  await project.selectOption('learning')
  await priority.selectOption('2')
  await expect(page).toHaveURL(/project=learning&priority=2/)
  await expect(page.getByText('没有符合筛选条件的任务')).toBeVisible()
  await expect(page.getByRole('status').filter({ hasText: '0 个结果' })).toBeVisible()
  await page.reload()
  await expect(project).toHaveValue('learning')
  await expect(priority).toHaveValue('2')

  await page.getByRole('button', { name: '清除筛选' }).click()
  await expect(page).toHaveURL(/\/tasks\?view=unscheduled$/)
  await expect(project).toBeFocused()
  await expect(project).toHaveValue('')
  await expect(priority).toHaveValue('')
  await expect(page.getByRole('listitem').filter({ hasText: title })).toBeVisible()
  await expectNoPageOverflow(page)
  await page.screenshot({ path: info.outputPath('tasks.png') })
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
  await page.keyboard.press('Escape')
  await expect(
    page.getByRole('dialog', { name: `编辑日程：${title}` })
  ).not.toBeVisible()
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
  const actions = await page.getByRole('button', { name: '新建笔记' }).boundingBox()
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
