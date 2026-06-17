import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { SyncSettingsPanel } from './SyncSettingsPanel'

vi.mock('./ObsidianSyncPanel', () => ({
  ObsidianSyncPanel: () => <div>Obsidian 面板</div>,
}))

vi.mock('./NotionSyncPanel', () => ({
  NotionSyncPanel: () => <div>Notion 面板</div>,
}))

describe('SyncSettingsPanel', () => {
  it('uses Chinese labels for the sync settings modal', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()

    render(<SyncSettingsPanel onClose={onClose} />)

    expect(screen.getByText('同步')).toBeVisible()
    expect(screen.getByRole('heading', { name: '同步设置' })).toBeVisible()
    expect(screen.getByRole('tablist', { name: '同步目标' })).toBeVisible()
    expect(screen.getByRole('tabpanel', { name: 'Obsidian 同步设置' })).toBeVisible()

    await user.click(screen.getByRole('tab', { name: 'Notion' }))
    expect(screen.getByRole('tabpanel', { name: 'Notion 同步设置' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: '关闭同步设置面板' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
