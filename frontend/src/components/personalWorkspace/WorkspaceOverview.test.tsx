import { render, screen, within } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import type {
  ProjectWithSummary,
  WorkspaceNotePreview,
  WorkspaceNotesSection,
} from '../../api/workspace'
import {
  RecentNotes,
  WorkspaceProjects,
  WorkspaceShortcuts,
} from './WorkspaceOverview'

const project: ProjectWithSummary = {
  id: 'personal',
  name: '个人事务',
  kind: 'standard',
  horizon: 'long',
  status: 'active',
  revision: 1,
  summary: {
    completed_task_count: 2,
    total_actionable_task_count: 8,
    open_occurrence_count: 9,
    blocked_occurrence_count: 1,
    has_open_ended_recurrence: false,
    next_occurrence: null,
    completion_percent: 25,
  },
}
const note: WorkspaceNotePreview = {
  id: 'note/1',
  title: '项目反馈与下一步',
  updated_at: 1790906400,
}
const notesSection: WorkspaceNotesSection = {
  state: 'available',
  authority: 'platform_compatibility',
  as_of: '2026-11-19T04:00:00Z',
  items: [note],
}

function renderOverview(element: ReactElement) {
  return render(<MemoryRouter>{element}</MemoryRouter>)
}

describe('personal workspace overview', () => {
  it('links each workspace shortcut to its existing destination', () => {
    renderOverview(<WorkspaceShortcuts />)
    const shortcuts = screen.getByRole('navigation', {
      name: '工作台快捷入口',
    })

    for (const [name, href] of [
      ['任务工作台', '/tasks'],
      ['项目中心', '/projects'],
      ['笔记库', '/notes'],
      ['日历安排', '/calendar'],
    ]) {
      expect(within(shortcuts).getByRole('link', { name })).toHaveAttribute(
        'href',
        href
      )
    }
  })

  it('opens a recent note from the aggregate with its encoded identifier', () => {
    renderOverview(
      <RecentNotes section={notesSection} loading={false} error={false} />
    )

    const recent = screen.getByRole('region', { name: '最近笔记' })
    expect(
      within(recent).getByRole('link', { name: `打开笔记${note.title}` })
    ).toHaveAttribute('href', '/editor/note%2F1')
    expect(within(recent).getByText(note.title)).toBeVisible()
    expect(screen.getByRole('link', { name: '查看全部笔记' })).toHaveAttribute(
      'href',
      '/notes'
    )
  })

  it('shows loading without inventing an empty notes result', () => {
    renderOverview(<RecentNotes loading error={false} />)

    expect(
      screen.getByRole('status', { name: '正在加载最近笔记' })
    ).toBeVisible()
    expect(screen.queryByText('还没有笔记')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: `打开笔记${note.title}` })
    ).not.toBeInTheDocument()
  })

  it('distinguishes an unavailable notes section from a successful empty list', () => {
    const unavailable: WorkspaceNotesSection = {
      ...notesSection,
      state: 'unavailable',
      items: null,
      error: { code: 'notes_unavailable', message: 'source unavailable' },
    }
    const { unmount } = renderOverview(
      <RecentNotes section={unavailable} loading={false} error={false} />
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      '最近笔记暂时不可用，请稍后重试。'
    )
    expect(screen.queryByText('还没有笔记')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: '创建第一篇笔记' })
    ).not.toBeInTheDocument()
    unmount()

    renderOverview(
      <RecentNotes
        section={{ ...notesSection, items: [] }}
        loading={false}
        error={false}
      />
    )
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByText('还没有笔记')).toBeVisible()
    expect(
      screen.getByRole('link', { name: '创建第一篇笔记' })
    ).toHaveAttribute('href', '/notes')
  })

  it('shows aggregate failures without displaying a successful notes result', () => {
    renderOverview(<RecentNotes section={notesSection} loading={false} error />)

    expect(screen.getByRole('alert')).toHaveTextContent(
      '最近笔记暂时不可用，请稍后重试。'
    )
    expect(screen.queryByText(note.title)).not.toBeInTheDocument()
    expect(screen.queryByText('还没有笔记')).not.toBeInTheDocument()
  })

  it('renders project progress from the server summary without receiving tasks', () => {
    renderOverview(
      <WorkspaceProjects projects={[project]} loading={false} error={false} />
    )

    const progress = screen.getByRole('progressbar', {
      name: '个人事务完成进度',
    })
    expect(progress).toHaveAttribute('max', '100')
    expect(progress).toHaveAttribute('value', '25')
    expect(screen.getByText('2 / 8 个任务已完成')).toBeVisible()
    expect(screen.getByText('25%')).toBeVisible()
    expect(screen.getByRole('link', { name: /个人事务/ })).toHaveAttribute(
      'href',
      '/projects/personal'
    )
    expect(screen.getByRole('link', { name: '查看全部项目' })).toHaveAttribute(
      'href',
      '/projects'
    )
  })

  it('does not manufacture progress for an indefinite recurring project', () => {
    renderOverview(
      <WorkspaceProjects
        projects={[
          {
            ...project,
            summary: {
              ...project.summary,
              completion_percent: null,
              has_open_ended_recurrence: true,
            },
          },
        ]}
        loading={false}
        error={false}
      />
    )

    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
    expect(screen.getByText('持续进行')).toBeVisible()
    expect(screen.queryByText(/\d+%/)).not.toBeInTheDocument()
    expect(screen.getByText('2 / 8 个任务已完成')).toBeVisible()
  })

  it('does not turn an undefined progress denominator into zero percent', () => {
    renderOverview(
      <WorkspaceProjects
        projects={[
          {
            ...project,
            summary: {
              ...project.summary,
              completed_task_count: 0,
              total_actionable_task_count: 0,
              completion_percent: null,
            },
          },
        ]}
        loading={false}
        error={false}
      />
    )

    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
    expect(screen.queryByText('0%')).not.toBeInTheDocument()
    expect(screen.getByText('未开始')).toBeVisible()
    expect(screen.getByText('暂无可计入进度的任务')).toBeVisible()
    expect(screen.queryByText('还没有任务')).not.toBeInTheDocument()
  })

  it('distinguishes unavailable projects from an empty project list', () => {
    const error = renderOverview(
      <WorkspaceProjects projects={[]} loading={false} error />
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      '项目暂时不可用，请稍后重试。'
    )
    expect(screen.queryByText('让目标有个归属')).not.toBeInTheDocument()
    error.unmount()

    renderOverview(
      <WorkspaceProjects projects={[]} loading={false} error={false} />
    )
    expect(screen.getByText('让目标有个归属')).toBeVisible()
    expect(screen.getByRole('link', { name: '创建一个项目' })).toHaveAttribute(
      'href',
      '/projects'
    )
  })
})
