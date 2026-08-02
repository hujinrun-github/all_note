import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import NotFound from './NotFound'

describe('NotFound', () => {
  it('shows the unknown path and offers safe destinations', () => {
    render(
      <MemoryRouter initialEntries={['/css']}>
        <NotFound />
      </MemoryRouter>
    )

    expect(
      screen.getByRole('heading', { name: '这条路径暂时没有内容' })
    ).toBeInTheDocument()
    expect(screen.getByText('/css')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回工作台' })).toHaveAttribute(
      'href',
      '/'
    )
    expect(screen.getByRole('link', { name: '前往登录' })).toHaveAttribute(
      'href',
      '/login'
    )
  })
})
