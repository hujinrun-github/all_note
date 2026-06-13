import { expect, test } from '@playwright/test'

type NotesResponse = {
  data: {
    notes: Array<{ id: string; title: string }>
  }
}

test.beforeEach(async ({ request }) => {
  const res = await request.get('/api/notes', {
    params: { sort: 'recent', page: '1', page_size: '100' },
  })
  expect(res.ok()).toBeTruthy()
  const body = (await res.json()) as NotesResponse
  for (const note of body.data.notes.filter((item) => item.title === 'Mock Notion Note')) {
    const deleteRes = await request.delete(`/api/notes/${encodeURIComponent(note.id)}`)
    expect(deleteRes.ok()).toBeTruthy()
  }
})

test('configures notion target, imports mock note, and shows synced notion card', async ({ page }) => {
  await page.goto('/notes')

  await page.getByRole('button', { name: '同步' }).click()
  await page.getByRole('tab', { name: 'Notion' }).click()
  await page.getByLabel('Data Source ID').fill('mock-data-source')
  await page.getByRole('button', { name: 'Save Notion settings' }).click()
  await expect(page.getByText('Notion settings saved')).toBeVisible()

  await page.getByRole('button', { name: 'Test Notion connection' }).click()
  await expect(page.getByText('Notion connection available')).toBeVisible()

  await page.getByRole('button', { name: 'Run Notion bidirectional sync' }).click()
  await expect(page.getByText('Imported 1')).toBeVisible()
  await page.getByRole('button', { name: 'Close sync settings panel' }).click()

  await expect(page.getByText('Mock Notion Note')).toBeVisible()
  await page.getByText('Mock Notion Note').click()

  const notionCard = page.locator('.sync-card').filter({ hasText: 'Notion' })
  await expect(notionCard).toBeVisible()
  await expect(notionCard.getByRole('link', { name: 'Open Notion page' })).toHaveAttribute(
    'href',
    'https://www.notion.so/mock-page-1',
  )
  await expect(notionCard.getByText('Synced')).toBeVisible()
})
