import { expect, test } from '@playwright/test'

function todayInputValue() {
  const date = new Date()
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

test('creates and completes a weekly task with project and date', async ({ page }) => {
  const title = `本周任务 ${Date.now()}`

  await page.goto('/tasks')
  await page.getByRole('tab', { name: '本周' }).click()
  await page.getByLabel('任务内容').fill(title)
  await page.getByLabel('任务日期').fill(todayInputValue())
  await page.getByLabel('任务项目').selectOption({ label: '个人' })
  await page.getByRole('button', { name: '添加任务' }).click()

  await expect(page.getByText(title)).toBeVisible()
  await page.getByRole('button', { name: `完成 ${title}` }).click()
  await expect(page.getByRole('button', { name: `重新打开 ${title}` })).toBeVisible()
})

test('generates a learning roadmap, attaches resources, and creates a weekly task from a node', async ({ page }) => {
  const suffix = Date.now()
  const projectName = `Playwright 学习 ${suffix}`
  const manualTitle = `手动文章 ${suffix}`

  await page.goto('/tasks')
  await page.getByLabel('项目名称').fill(projectName)
  await page.getByLabel('项目类型').selectOption('learning')
  await page.getByRole('button', { name: '新增项目' }).click()

  await page.getByRole('tab', { name: '学习 Roadmap' }).click()
  await page.getByLabel('学习项目').selectOption({ label: projectName })
  await page.getByRole('button', { name: '生成 Roadmap' }).click()

  await expect(page.getByTestId('roadmap-canvas')).toBeVisible()
  const firstNode = page.getByTestId('roadmap-node').filter({ hasText: '项目目标与环境' })
  await expect(firstNode).toBeVisible()

  await firstNode.click()
  await expect(page.getByRole('heading', { name: '项目目标与环境' })).toBeVisible()

  const sourceSettings = page.getByRole('group', { name: '搜索源' })
  await expect(sourceSettings.getByLabel('Medium')).toBeChecked()
  await sourceSettings.getByLabel('Reddit').uncheck()
  await sourceSettings.getByLabel('Reddit').check()

  await page.getByRole('button', { name: '搜索文章' }).click()
  const resourceDialog = page.getByRole('dialog', { name: '选择文章' })
  await expect(resourceDialog).toBeVisible()
  await expect(resourceDialog.getByRole('checkbox')).toHaveCount(10)
  await page.getByRole('button', { name: '添加选中文章' }).click()
  await expect(page.getByRole('dialog', { name: '选择文章' })).toBeHidden()
  await expect(page.getByRole('link', { name: /项目目标与环境 官方文档/ }).first()).toBeVisible()

  await page.getByLabel('文章标题').fill(manualTitle)
  await page.getByLabel('文章链接').fill('https://example.com/manual-roadmap')
  await page.getByRole('button', { name: '添加链接' }).click()
  await expect(page.getByText(manualTitle)).toBeVisible()

  await page.getByRole('button', { name: '加入本周' }).click()
  await page.getByRole('tab', { name: '本周' }).click()
  await expect(page.getByRole('button', { name: /完成 项目目标与环境/ }).first()).toBeVisible()
})

test('shows roadmap nodes on mobile without a blank screen', async ({ page }) => {
  const projectName = `移动 Roadmap ${Date.now()}`

  await page.goto('/tasks')
  await page.getByLabel('项目名称').fill(projectName)
  await page.getByLabel('项目类型').selectOption('learning')
  await page.getByRole('button', { name: '新增项目' }).click()

  await page.getByRole('tab', { name: '学习 Roadmap' }).click()
  await page.getByLabel('学习项目').selectOption({ label: projectName })
  await page.getByRole('button', { name: '生成 Roadmap' }).click()

  await expect(page.getByTestId('roadmap-canvas')).toBeVisible()
  await expect(page.getByTestId('roadmap-node').first()).toBeVisible()
})
