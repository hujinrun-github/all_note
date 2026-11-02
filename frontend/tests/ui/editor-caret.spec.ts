import { expect, test, type Page } from '@playwright/test'

const quote = [
  '如果要了解 harness，我们首先要从 AI 和大模型说起。',
  '模型通过大量数据训练，学习根据上下文生成下一个词。'.repeat(8),
  '提示词、示例和上下文共同帮助模型完成任务。'.repeat(8),
  '上下文工程让模型能够使用企业内部提供的信息。'.repeat(8),
]

async function clickFirstCharacter(page: Page, paragraphIndex: number) {
  const paragraph = page.locator('.tiptap.ProseMirror blockquote p').nth(paragraphIndex)
  await paragraph.scrollIntoViewIfNeeded()
  const point = await paragraph.evaluate((element) => {
    const range = document.createRange()
    range.setStart(element.firstChild!, 0)
    range.setEnd(element.firstChild!, 1)
    const rect = range.getBoundingClientRect()
    return { x: rect.left + 1, y: rect.top + rect.height / 2 }
  })
  await page.mouse.click(point.x, point.y)
  const caret = await paragraph.evaluate(() => {
    const selection = window.getSelection()!
    const rect = selection.getRangeAt(0).getBoundingClientRect()
    return {
      top: rect.top,
      bottom: rect.bottom,
      text: selection.anchorNode?.parentElement?.closest('p')?.textContent,
    }
  })
  expect(caret.text).toBe(await paragraph.textContent())
  expect(caret.top).toBeLessThanOrEqual(point.y)
  expect(caret.bottom).toBeGreaterThanOrEqual(point.y)
}

test.beforeEach(async ({ page }) => {
  let note = {
    id: 'caret-note',
    title: '引用块光标回归',
    body: quote.map((text) => `> ${text}`).join('\n>\n'),
    tags: '[]',
    projects: [],
    created_at: 1790906400,
    updated_at: 1790906400,
  }
  await page.route((url) => url.pathname.startsWith('/api/'), async (route) => {
    const path = new URL(route.request().url()).pathname
    const data = (value: unknown) => route.fulfill({ json: { data: value } })
    if (path === '/api/auth/me')
      return data({
        user: {
          id: 'caret-user',
          display_name: '测试用户',
          email: 'caret@example.test',
          role: 'user',
          status: 'active',
        },
        workspace: { id: 'caret-workspace', name: '测试工作区' },
        must_change_password: false,
      })
    if (path === '/api/task-domain/capabilities')
      return route.fulfill({ json: { model_version: 'v2', available: true } })
    if (path === '/api/notes/caret-note') {
      if (route.request().method() === 'PATCH')
        note = { ...note, ...route.request().postDataJSON() }
      return data({ note })
    }
    if (path === '/api/projects') return data({ projects: [] })
    if (path === '/api/tasks') return data({ tasks: [] })
    if (path === '/api/sync/targets') return data({ targets: [] })
    if (path.endsWith('/sync-binding')) return data({ binding: null })
    if (path.endsWith('/attachments')) return data({ attachments: [] })
    if (path.endsWith('/content-import')) return data({ import: null })
    return data({})
  })
})

test('clicking quote text keeps the caret at the clicked line after scrolling and fullscreen', async ({
  page,
}) => {
  await page.goto('/editor/caret-note')
  const editor = page.locator('.tiptap.ProseMirror')
  await expect(editor).toHaveCSS('white-space', 'pre-wrap')
  await expect(editor.locator('blockquote p')).toHaveCount(quote.length)
  for (const fullscreen of [false, true]) {
    if (fullscreen)
      await page.getByRole('button', { name: '进入全屏写作' }).click()
    await clickFirstCharacter(page, quote.length - 1)
  }
})

test('long quote keeps click and insertion positions aligned after autosave', async ({ page }, testInfo) => {
  test.skip(
    testInfo.project.name !== 'desktop',
    'This regression covers desktop Chrome mouse coordinates after long text input.'
  )
  test.setTimeout(60_000)
  await page.goto('/editor/caret-note')
  const editor = page.locator('.tiptap.ProseMirror')
  await expect(editor.locator('blockquote p')).toHaveCount(quote.length)
  await clickFirstCharacter(page, quote.length - 1)
  for (let index = 0; index < 35; index += 1) {
    await page.keyboard.insertText(`长文第${index + 1}段。${'通过鼠标点击正文，检查中文长段落的光标位置和输入位置是否一致。'.repeat(22)}`)
    await page.keyboard.press('Enter')
  }
  await page.waitForResponse((response) =>
    response.url().endsWith('/api/notes/caret-note') && response.request().method() === 'PATCH'
  )
  await expect(editor).toContainText('长文第35段')
  for (const index of [0, 18, 34]) {
    await clickFirstCharacter(page, index)
    const paragraph = editor.locator('blockquote p').nth(index)
    await page.keyboard.insertText('定位标记')
    await expect(paragraph).toContainText('定位标记')
    await page.screenshot({ path: testInfo.outputPath(`caret-${index}.png`), caret: 'initial' })
  }
})
