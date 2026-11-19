import {
  ArrowRight,
  CalendarDays,
  CheckSquare,
  ChevronRight,
  CircleAlert,
  FileText,
  Layers3,
  NotebookPen,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import type {
  ProjectWithSummary,
  WorkspaceNotesSection,
} from '../../api/workspace'
import { Alert, AlertTitle } from '../ui/alert'
import { Button } from '../ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../ui/card'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '../ui/empty'
import { PlanningLoading } from '../planning/PlanningWorkspace'

const shortcuts = [
  {
    to: '/tasks',
    title: '任务工作台',
    description: '梳理待办，聚焦当下',
    icon: CheckSquare,
  },
  {
    to: '/projects',
    title: '项目中心',
    description: '管理项目，推进目标',
    icon: Layers3,
  },
  {
    to: '/notes',
    title: '笔记库',
    description: '沉淀想法，连接知识',
    icon: NotebookPen,
  },
  {
    to: '/calendar',
    title: '日历安排',
    description: '查看日程，安排时间',
    icon: CalendarDays,
  },
] as const

export function WorkspaceShortcuts() {
  return (
    <nav className="workspace-shortcuts" aria-label="工作台快捷入口">
      <strong className="workspace-shortcuts-title">从这里开始</strong>
      <div className="workspace-shortcuts-links">
        {shortcuts.map(({ to, title, description, icon: Icon }) => (
          <Link to={to} key={to} aria-label={title}>
            <Icon aria-hidden="true" />
            <span>
              <strong>
                {title}
                <ArrowRight aria-hidden="true" />
              </strong>
              <small>{description}</small>
            </span>
          </Link>
        ))}
      </div>
    </nav>
  )
}

export function WorkspaceProjects({
  projects,
  loading,
  error,
}: {
  projects: ProjectWithSummary[]
  loading: boolean
  error: boolean
}) {
  return (
    <Card
      className="workspace-overview-panel"
      role="region"
      aria-label="我的项目"
    >
      <CardHeader className="workspace-panel-header">
        <CardTitle>
          <h2>
            <Layers3 aria-hidden="true" />
            我的项目
          </h2>
        </CardTitle>
        <Button asChild variant="ghost" size="sm">
          <Link to="/projects" aria-label="查看全部项目">
            查看全部
            <ArrowRight data-icon="inline-end" aria-hidden="true" />
          </Link>
        </Button>
      </CardHeader>
      <CardContent className="workspace-panel-content">
        {error ? (
          <Alert variant="destructive">
            <CircleAlert aria-hidden="true" />
            <AlertTitle>项目暂时不可用，请稍后重试。</AlertTitle>
          </Alert>
        ) : loading ? (
          <PlanningLoading label="正在加载项目" />
        ) : projects.length ? (
          <ul className="workspace-project-list">
            {projects.map((project) => {
              const summary = project.summary
              const percent = summary.completion_percent
              return (
                <li key={project.id}>
                  <Link
                    className="workspace-project-link"
                    to={`/projects/${encodeURIComponent(project.id)}`}
                  >
                    <span
                      className="workspace-project-icon"
                      data-kind={project.kind}
                    >
                      <Layers3 aria-hidden="true" />
                    </span>
                    <span className="workspace-project-copy">
                      <strong>{project.name}</strong>
                      <small>
                        {summary.total_actionable_task_count
                          ? `${summary.completed_task_count} / ${summary.total_actionable_task_count} 个任务已完成`
                          : '暂无可计入进度的任务'}
                      </small>
                    </span>
                    <span className="workspace-project-progress">
                      {percent !== null ? (
                        <progress
                          aria-label={`${project.name}完成进度`}
                          max={100}
                          value={percent}
                        />
                      ) : null}
                      <small>
                        {percent !== null
                          ? `${percent}%`
                          : summary.has_open_ended_recurrence
                            ? '持续进行'
                            : summary.total_actionable_task_count === 0
                              ? '未开始'
                              : '—'}
                      </small>
                    </span>
                    <ChevronRight aria-hidden="true" />
                  </Link>
                </li>
              )
            })}
          </ul>
        ) : (
          <Empty className="workspace-overview-empty">
            <EmptyHeader>
              <EmptyTitle>让目标有个归属</EmptyTitle>
              <EmptyDescription>用项目组织工作、学习与生活。</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button asChild variant="outline" size="sm">
                <Link to="/projects">创建一个项目</Link>
              </Button>
            </EmptyContent>
          </Empty>
        )}
      </CardContent>
    </Card>
  )
}

export function RecentNotes({
  section,
  loading,
  error,
}: {
  section?: WorkspaceNotesSection
  loading: boolean
  error: boolean
}) {
  const notes = section?.items ?? []

  return (
    <Card
      className="workspace-overview-panel"
      role="region"
      aria-label="最近笔记"
    >
      <CardHeader className="workspace-panel-header">
        <CardTitle>
          <h2>
            <NotebookPen aria-hidden="true" />
            最近笔记
          </h2>
        </CardTitle>
        <Button asChild variant="ghost" size="sm">
          <Link to="/notes" aria-label="查看全部笔记">
            查看全部
            <ArrowRight data-icon="inline-end" aria-hidden="true" />
          </Link>
        </Button>
      </CardHeader>
      <CardContent className="workspace-panel-content">
        {error || section?.state === 'unavailable' ? (
          <Alert variant="destructive">
            <CircleAlert aria-hidden="true" />
            <AlertTitle>最近笔记暂时不可用，请稍后重试。</AlertTitle>
          </Alert>
        ) : loading ? (
          <PlanningLoading label="正在加载最近笔记" />
        ) : notes.length ? (
          <ul className="workspace-note-list">
            {notes.map((note) => (
              <li key={note.id}>
                <Link
                  to={`/editor/${encodeURIComponent(note.id)}`}
                  aria-label={`打开笔记${note.title || '未命名笔记'}`}
                >
                  <FileText aria-hidden="true" />
                  <span>
                    <strong>{note.title || '未命名笔记'}</strong>
                    <small>
                      <time
                        dateTime={new Date(
                          note.updated_at * 1000
                        ).toISOString()}
                      >
                        {formatNoteDate(note.updated_at)}
                      </time>
                      · 个人笔记
                    </small>
                  </span>
                  <ChevronRight aria-hidden="true" />
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <Empty className="workspace-overview-empty">
            <EmptyHeader>
              <EmptyTitle>还没有笔记</EmptyTitle>
              <EmptyDescription>留住灵感，让思考继续生长。</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button asChild variant="outline" size="sm">
                <Link to="/notes">创建第一篇笔记</Link>
              </Button>
            </EmptyContent>
          </Empty>
        )}
      </CardContent>
    </Card>
  )
}

function formatNoteDate(timestamp: number) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(timestamp * 1000))
}
