import { expect, test, type Page } from '@playwright/test'
import type { JapaneseIntegrationStatus } from '../../src/api/japaneseIntegration'

async function fixture(page: Page) {
  const errors: string[] = []
  const saved: unknown[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  const state: JapaneseIntegrationStatus = {
    connections: [
      {
        connection_id: 'c1',
        workspace_id: 'w1',
        actor_id: 'u1',
        source_instance_id: 'japanese-test',
        source_user_id: '1',
        source_user_name: '日语测试用户',
        key_id: 'v1',
        state: 'active',
        created_at: '',
      },
    ],
    catalogs: [
      {
        connection_id: 'c1',
        catalog: {
          schema_version: 1,
          source_instance_id: 'japanese-test',
          source_user_id: '1',
          catalog_revision: 1,
          plan: {
            plan_id: 'p1',
            revision: 1,
            title: 'N2 学习',
            timezone: 'Asia/Shanghai',
            start_date: '2026-11-19',
            end_date: '2027-06-30',
            weekdays: [1, 2, 3, 4, 5],
            time_slots: [
              {
                id: 'evening',
                title: '傍晚',
                time: '18:40',
                duration_minutes: 20,
                tasks: [
                  {
                    id: 'listening',
                    title: '听力训练',
                    path: '/listening',
                    minutes: 20,
                  },
                ],
              },
            ],
          },
        },
      },
    ],
    targets: [
      {
        task_id: 't1',
        title: '晚间听力练习',
        timezone: 'Asia/Shanghai',
        recurrence_type: 'weekly',
      },
    ],
    bindings: [],
    events: [],
  }
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    async (route) => {
      const request = route.request()
      const path = new URL(request.url()).pathname
      const data = (value: unknown) => route.fulfill({ json: { data: value } })
      if (path === '/api/auth/me')
        return data({
          user: {
            id: 'u1',
            email: 'integration@example.test',
            display_name: '联动回归用户',
            role: 'admin',
            status: 'active',
          },
          workspace: { id: 'w1', name: '联动测试工作区' },
          must_change_password: false,
        })
      if (path === '/api/task-domain/capabilities')
        return route.fulfill({ json: { model_version: 'v2', available: true } })
      if (path === '/api/settings/profile')
        return data({
          user_id: 'u1',
          display_name: '联动回归用户',
          locale: 'zh-CN',
          time_zone: 'Asia/Shanghai',
          updated_at: 1,
        })
      if (path === '/api/settings/runtime')
        return data({
          workspace_id: 'w1',
          mode: 'active',
          epoch: 1,
          binding_revision: 1,
          bindings: [],
        })
      if (path === '/api/task-workbench/facets')
        return data({ facets: { inbox: 0 } })
      if (path === '/api/integrations/japanese') return data(state)
      if (path === '/api/integrations/japanese/pairing-codes')
        return data({
          pairing_code: 'one-time-pairing-code',
          expires_at: '2026-11-19T14:00:00Z',
        })
      if (path === '/api/integrations/japanese/bindings') {
        const body = request.postDataJSON()
        saved.push(body)
        const binding = {
          ...body,
          id: 'b1',
          target_title: '晚间听力练习',
          binding_version: 1,
          effective_from: '2026-11-19',
          schedule_revision: 1,
          source_fingerprint: 'fp',
          enabled: body.enabled ?? true,
        }
        state.bindings = [binding]
        return data(binding)
      }
      if (path === '/api/integrations/japanese/connections/c1') {
        state.connections[0].state = 'revoked'
        return route.fulfill({ status: 204 })
      }
      if (path.endsWith('/retry')) {
        state.events[0].status = 'received'
        return data({ queued: true })
      }
      return data({})
    }
  )
  return { state, saved, errors }
}

test('pairs, binds, reads completion receipts and pauses safely', async ({
  page,
}, testInfo) => {
  const { state, saved, errors } = await fixture(page)
  await page.goto('/settings')
  await expect(page).toHaveTitle(/FlowSpace/i)
  await page.getByRole('button', { name: '日语联动', exact: true }).click()
  await expect(
    page.getByRole('heading', { name: '日语学习联动' })
  ).toBeVisible()
  await page.getByRole('button', { name: '生成配对码' }).click()
  await expect(page.getByText('one-time-pairing-code')).toBeVisible()
  await page
    .getByLabel('日语学习任务')
    .selectOption(JSON.stringify(['c1', 'p1', 'evening', 'listening']))
  await page.getByLabel('FlowSpace 重复任务').selectOption('t1')
  await expect(page.getByText(/只完成相同学习日期的执行实例/)).toBeVisible()
  await page.getByRole('button', { name: '保存关联' }).click()
  await expect(page.getByText(/关联已保存/)).toBeVisible()
  expect(saved[0]).toEqual({
    connection_id: 'c1',
    plan_id: 'p1',
    slot_id: 'evening',
    task_id: 'listening',
    target_task_id: 't1',
  })
  state.events = [
    {
      event_id: 'evt1',
      connection_id: 'c1',
      status: 'received',
      study_date: '2026-11-19',
      source_task_id: 'listening',
      target_task_id: 't1',
      completed: true,
      source_occurred_at: '2026-11-19T10:40:00Z',
    },
  ]
  await page.getByRole('button', { name: '刷新状态' }).click()
  await expect(page.getByText('已送达，等待处理')).toBeVisible()
  state.events[0].status = 'applied'
  await page.getByRole('button', { name: '刷新状态' }).click()
  await expect(page.getByText('已同步完成', { exact: true })).toBeVisible()
  await page.screenshot({
    path: `/tmp/flowspace-japanese-integration-${testInfo.project.name}.png`,
    fullPage: true,
  })
  await page.getByRole('button', { name: '暂停关联' }).click()
  await expect(page.getByText(/关联已暂停/)).toBeVisible()
  await page.getByRole('button', { name: '停用连接' }).click()
  await expect(page.getByText(/连接已停用/)).toBeVisible()
  await expect(page.getByRole('button', { name: '停用连接' })).toHaveCount(0)
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth
    )
  ).toBe(true)
  expect(errors).toEqual([])
})

test('deep-link opens integration and request failures stay visible', async ({
  page,
}) => {
  await fixture(page)
  await page.goto('/settings?integration_task=t1#japanese')
  await expect(
    page.getByRole('heading', { name: '日语学习联动' })
  ).toBeVisible()
  await expect(page.getByLabel('FlowSpace 重复任务')).toHaveValue('t1')
  await page.route('**/api/integrations/japanese/pairing-codes', (route) =>
    route.fulfill({
      status: 403,
      json: { error: { code: 'FORBIDDEN', message: '需要工作区管理员权限。' } },
    })
  )
  await page.getByRole('button', { name: '生成配对码' }).click()
  await expect(page.getByRole('alert')).toHaveText('需要工作区管理员权限。')
  await expect(page.getByText('one-time-pairing-code')).toHaveCount(0)
})
