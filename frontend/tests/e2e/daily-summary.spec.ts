import { test, expect } from '@playwright/test'

test.describe('Daily Summary page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/summary')
    await page.waitForLoadState('networkidle')
  })

  test('loads with default week range', async ({ page }) => {
    await expect(page.locator('.segmented-tabs button.is-active')).toHaveText('本周')
    await expect(page.locator('.summary-date-inputs input').first()).toHaveValue(/.+/) // date populated
  })

  test('switches preset to month and updates data', async ({ page }) => {
    await page.click('button:has-text("本月")')
    await expect(page.locator('.segmented-tabs button.is-active')).toHaveText('本月')
    await page.waitForResponse(r => r.url().includes('/api/summary') && r.status() === 200)
  })

  test('shows stats cards', async ({ page }) => {
    await expect(page.locator('.metric-tile').first()).toBeVisible()
  })

  test('expands task to show linked notes', async ({ page }) => {
    const firstTask = page.locator('.summary-task-card summary').first()
    if (await firstTask.isVisible()) {
      await firstTask.click()
      await expect(page.locator('.summary-task-detail').first()).toBeVisible()
    }
  })

  test('navigates to editor from note link', async ({ page }) => {
    const noteLink = page.locator('.summary-task-detail a').first()
    if (await noteLink.isVisible()) {
      await noteLink.click()
      await expect(page).toHaveURL(/\/editor\//)
    }
  })

  test('shows empty state for no completed tasks', async ({ page }) => {
    // Navigate to a far future range
    await page.locator('.summary-date-inputs input').first().fill('2099-01-01')
    await page.locator('.summary-date-inputs input').last().fill('2099-01-07')
    await page.waitForResponse(r => r.url().includes('/api/summary'))
    await expect(page.locator('.empty-copy')).toBeVisible()
  })

  test('pagination works', async ({ page }) => {
    const nextBtn = page.locator('button:has-text("下一页")')
    if (await nextBtn.isVisible() && await nextBtn.isEnabled()) {
      await nextBtn.click()
      const url = new URL(page.url())
      expect(url.searchParams.get('page')).toBe('2')
    }
  })

  test('refresh preserves URL state', async ({ page }) => {
    await page.locator('.summary-date-inputs input').first().fill('2026-06-01')
    await page.locator('.summary-date-inputs input').last().fill('2026-06-10')
    await page.waitForResponse(r => r.url().includes('/api/summary'))
    const beforeUrl = page.url()
    await page.reload()
    expect(page.url()).toBe(beforeUrl)
  })

  test('can navigate from sidebar', async ({ page }) => {
    await page.click('.sidebar-link:has-text("每日总结")')
    await expect(page.locator('.page-title')).toHaveText('每日总结')
  })
})
