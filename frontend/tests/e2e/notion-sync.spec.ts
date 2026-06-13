import { expect, test } from '@playwright/test'

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
  await expect(page.getByText('Notion bidirectional sync completed')).toBeVisible()
  const syncResult = page.getByLabel('Notion bidirectional sync result')
  await expect(syncResult).toBeVisible()
  const summary = (await syncResult.textContent()) ?? ''
  if (!summary.includes('Imported 1')) {
    expect(summary).toContain('Imported 0')
    await expect(page.getByText('Mock Notion Note').first()).toBeVisible()
  }
  await page.getByRole('button', { name: 'Close sync settings panel' }).click()

  const mockNote = page.getByText('Mock Notion Note').first()
  await expect(mockNote).toBeVisible()
  await mockNote.click()

  const notionCard = page.locator('.sync-card').filter({ hasText: 'Notion' })
  await expect(notionCard).toBeVisible()
  await expect(notionCard.getByRole('link', { name: 'Open Notion page' })).toHaveAttribute(
    'href',
    'https://www.notion.so/mock-page-1',
  )
  await expect(notionCard.getByText('Synced')).toBeVisible()
})
