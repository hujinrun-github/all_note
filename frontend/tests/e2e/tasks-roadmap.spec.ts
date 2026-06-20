import { expect, test } from '@playwright/test'

function todayInputValue() {
  const date = new Date()
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

test('creates and completes a weekly task with project and date', async ({ page }, testInfo) => {
  const title = `本周任务 ${testInfo.project.name} ${Date.now()}`

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

test('deletes a regular project from the project list', async ({ page }, testInfo) => {
  const projectName = `可删除项目 ${testInfo.project.name} ${Date.now()}`

  await page.goto('/tasks')
  await page.getByLabel('项目名称').fill(projectName)
  await page.getByLabel('项目类型').selectOption('regular')
  await page.getByRole('button', { name: '新增项目' }).click()

  await expect(page.getByRole('button', { name: `${projectName} 任务项目` })).toBeVisible()
  await page.getByRole('button', { name: `删除项目 ${projectName}` }).click()
  await page.getByRole('button', { name: `确认删除 ${projectName}` }).click()
  await expect(page.getByRole('button', { name: `${projectName} 任务项目` })).toHaveCount(0)
})

test('generates a learning roadmap, attaches resources, and creates a weekly task from a node', async ({ page }, testInfo) => {
  const suffix = `${testInfo.project.name} ${Date.now()}`
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
  const nodeDialog = page.getByTestId('roadmap-node-dialog')
  await expect(nodeDialog).toBeVisible()
  await nodeDialog.getByRole('button', { name: '关闭节点详情' }).click()
  await expect(nodeDialog).toBeHidden()
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

test('opens roadmap node dialog with editing, status, linked tasks, and progress', async ({ page }, testInfo) => {
  const suffix = `${testInfo.project.name} ${Date.now()}`
  const projectName = `节点编辑学习 ${suffix}`
  const editedTitle = `已编辑节点 ${suffix}`

  await page.goto('/tasks')
  await page.getByLabel('项目名称').fill(projectName)
  await page.getByLabel('项目类型').selectOption('learning')
  await page.getByRole('button', { name: '新增项目' }).click()

  await page.getByRole('tab', { name: '学习 Roadmap' }).click()
  await page.getByLabel('学习项目').selectOption({ label: projectName })
  await page.getByRole('button', { name: '生成 Roadmap' }).click()

  const firstNode = page.getByTestId('roadmap-node').filter({ hasText: '项目目标与环境' })
  await expect(firstNode).toBeVisible()
  await firstNode.click()

  const dialog = page.getByTestId('roadmap-node-dialog')
  await expect(dialog).toBeVisible()
  await dialog.getByLabel('节点标题').fill(editedTitle)
  await dialog.getByLabel('节点说明').fill('用可运行的项目验证学习节点编辑能力')
  await dialog.getByLabel('学习状态').selectOption('done')
  await dialog.getByRole('button', { name: '保存节点' }).click()

  await expect(dialog.getByLabel('节点标题')).toHaveValue(editedTitle)
  await expect(dialog.getByLabel('学习状态')).toHaveValue('done')

  await dialog.getByRole('button', { name: '加入本周' }).click()
  await expect(dialog.getByTestId('roadmap-node-progress')).toContainText('0 / 1')
  await expect(dialog.getByTestId('roadmap-linked-task-list')).toContainText(editedTitle)

  await dialog.getByRole('button', { name: `完成 ${editedTitle}` }).click()
  await expect(dialog.getByTestId('roadmap-node-progress')).toContainText('1 / 1')
})

test('optimizes roadmap layout from the canvas toolbar', async ({ page }, testInfo) => {
  const projectName = `布局优化 Roadmap ${testInfo.project.name} ${Date.now()}`

  await page.goto('/tasks')
  await page.getByLabel('项目名称').fill(projectName)
  await page.getByLabel('项目类型').selectOption('learning')
  await page.getByRole('button', { name: '新增项目' }).click()

  await page.getByRole('tab', { name: '学习 Roadmap' }).click()
  await page.getByLabel('学习项目').selectOption({ label: projectName })
  await page.getByRole('button', { name: '生成 Roadmap' }).click()

  await expect(page.getByTestId('roadmap-canvas')).toBeVisible()
  await expect(page.getByRole('button', { name: '自动优化布局' })).toBeVisible()
  await page.getByRole('button', { name: '自动优化布局' }).click()
  await expect(page.getByRole('button', { name: '自动优化布局' })).toBeEnabled()
  await expect(page.getByTestId('roadmap-node').first()).toBeVisible()
})

test('adds and deletes roadmap nodes from the canvas', async ({ page }, testInfo) => {
  const suffix = `${testInfo.project.name} ${Date.now()}`
  const projectName = `画布节点编辑 ${suffix}`
  const nodeTitle = `手动新增节点 ${suffix}`

  await page.goto('/tasks')
  await page.getByLabel('项目名称').fill(projectName)
  await page.getByLabel('项目类型').selectOption('learning')
  await page.getByRole('button', { name: '新增项目' }).click()

  await page.getByRole('tab', { name: '学习 Roadmap' }).click()
  await page.getByLabel('学习项目').selectOption({ label: projectName })
  await page.getByRole('button', { name: '生成 Roadmap' }).click()

  await expect(page.getByTestId('roadmap-canvas')).toBeVisible()
  await expect(page.getByRole('button', { name: '新增节点' })).toHaveCount(0)
  const sourceNode = page.getByTestId('roadmap-node').first()
  await sourceNode.getByRole('button', { name: '添加后续节点' }).click()
  const createDialog = page.getByRole('dialog', { name: '新增 Roadmap 节点' })
  await expect(createDialog).toBeVisible()
  await expect(createDialog.getByText('接在')).toBeVisible()
  await createDialog.getByLabel('节点标题').fill(nodeTitle)
  await createDialog.getByRole('button', { name: '创建节点' }).click()

  const createdNode = page.getByTestId('roadmap-node').filter({ hasText: nodeTitle })
  await expect(createdNode).toBeVisible()
  await createdNode.click()

  const nodeDialog = page.getByTestId('roadmap-node-dialog')
  await expect(nodeDialog).toBeVisible()
  await nodeDialog.getByRole('button', { name: '删除节点' }).click()
  await nodeDialog.getByRole('button', { name: '确认删除节点' }).click()
  await expect(page.getByTestId('roadmap-node-dialog')).toBeHidden()
  await expect(createdNode).toHaveCount(0)
})

test('shows roadmap nodes on mobile without a blank screen', async ({ page }, testInfo) => {
  const projectName = `移动 Roadmap ${testInfo.project.name} ${Date.now()}`

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
