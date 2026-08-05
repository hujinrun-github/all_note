import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import PodcastImportPrototype from './PodcastImportPrototype'

describe('Podcast import prototype', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('re-parses a changed link and starts a background task', async () => {
    const user = userEvent.setup()
    render(<PodcastImportPrototype />)

    const dialog = screen.getByRole('dialog', { name: '导入播客' })
    expect(dialog).toBeVisible()
    expect(within(dialog).getByText('AI 产品经理的下一站')).toBeVisible()

    const urlInput = screen.getByLabelText('单集链接')
    await user.clear(urlInput)
    await user.type(
      urlInput,
      'https://podcasts.apple.com/cn/podcast/example/id123?i=456'
    )

    expect(screen.getByRole('button', { name: /解析链接/ })).toBeVisible()
    await user.click(screen.getByRole('button', { name: /解析链接/ }))
    expect(screen.getByRole('button', { name: /开始生成笔记/ })).toBeVisible()

    expect(screen.getByText('会调用 AI 总结')).toBeVisible()
    await user.click(screen.getByRole('checkbox', { name: /附上完整逐字稿/ }))
    expect(screen.getByText('完整逐字稿', { selector: 'em' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: /开始生成笔记/ }))

    expect(
      screen.queryByRole('dialog', { name: '导入播客' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('complementary', { name: '导入任务' })
    ).toHaveTextContent('8%')
  })

  it('can skip AI summarization and clearly switches to transcript-only output', async () => {
    const user = userEvent.setup()
    render(<PodcastImportPrototype />)

    const aiSwitch = screen.getByRole('switch', { name: 'AI 整理' })
    expect(aiSwitch).toHaveAttribute('aria-checked', 'true')

    await user.click(aiSwitch)

    expect(aiSwitch).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByText('不调用 AI 总结')).toBeVisible()
    expect(screen.getByText('带时间戳的完整逐字稿')).toBeVisible()
    expect(screen.getByRole('button', { name: /开始转写/ })).toBeVisible()
  })

  it('allows choosing the destination project', async () => {
    const user = userEvent.setup()
    render(<PodcastImportPrototype />)

    const projectSelect = screen.getByRole('combobox', {
      name: '关联项目',
    })
    expect(projectSelect).toHaveValue('ai-learning')

    await user.selectOptions(projectSelect, 'product-research')

    expect(projectSelect).toHaveValue('product-research')
    expect(
      screen.getByRole('option', { name: '产品研究', selected: true })
    ).toBeVisible()
  })

  it('finishes processing and opens the generated note', async () => {
    vi.useFakeTimers()
    render(<PodcastImportPrototype />)

    await act(async () => {
      vi.advanceTimersByTime(8_000)
    })

    expect(screen.getByRole('button', { name: /打开笔记/ })).toBeVisible()

    await act(async () => {
      screen.getByRole('button', { name: /打开笔记/ }).click()
    })

    expect(
      screen.getByRole('heading', { name: 'AI 产品经理的下一站｜播客笔记' })
    ).toBeVisible()
    expect(
      screen.queryByRole('complementary', { name: '导入任务' })
    ).not.toBeInTheDocument()
  })
})
