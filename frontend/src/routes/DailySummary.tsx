import {
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  Clock3,
  FileText,
  ListTodo,
} from 'lucide-react'
import {
  PlanningHeading,
  PlanningLoading,
} from '../components/planning/PlanningWorkspace'
import { Alert, AlertTitle } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '../components/ui/empty'
import { useMemo } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import type { TaskData } from '../components/ui/TaskRow'
import { useSummary } from '../hooks/useSummary'
import { useTodayOverview } from '../hooks/useTodayOverview'

function toDateInput(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function getMonday(date: Date = new Date()) {
  const day = date.getDay()
  const diff = date.getDate() - day + (day === 0 ? -6 : 1)
  return toDateInput(new Date(date.getFullYear(), date.getMonth(), diff))
}

function todayDateInputValue() {
  return toDateInput(new Date())
}

function getMonthStart() {
  const date = new Date()
  return toDateInput(new Date(date.getFullYear(), date.getMonth(), 1))
}

export default function DailySummary() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const from = searchParams.get('from') || getMonday()
  const to = searchParams.get('to') || todayDateInputValue()
  const page = Number.parseInt(searchParams.get('page') || '1', 10)
  const summaryQuery = useSummary(from, to, page)
  const todayQuery = useTodayOverview()

  const activePreset = useMemo(() => {
    if (from === getMonday() && to === todayDateInputValue()) return 'week'
    if (from === getMonthStart() && to === todayDateInputValue()) return 'month'
    return null
  }, [from, to])

  function setRange(newFrom: string, newTo: string) {
    setSearchParams({ from: newFrom, to: newTo, page: '1' })
  }

  const summary = summaryQuery.data?.summary
  const overview = todayQuery.data
  const completedCount = summaryQuery.data?.pagination.total ?? 0
  const todayTasks = (overview?.todayTasks ?? []).filter(
    (task) => !isTaskDone(task)
  )
  const overdueTasks = (overview?.overdueTasks ?? []).filter(
    (task) => !isTaskDone(task)
  )
  const recentNotes = overview?.recentNotes ?? []
  const denominator = completedCount + todayTasks.length + overdueTasks.length
  const completionRate =
    denominator > 0 ? Math.round((completedCount / denominator) * 100) : 0

  return (
    <section className="planning-page" aria-labelledby="summary-heading">
      <PlanningHeading
        id="summary-heading"
        title="每日总结"
        description="回顾任务完成情况，查看待处理事项和最近更新的笔记。"
      />
      <Card className="mb-4 gap-3 px-4 py-3 shadow-none sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <span className="text-sm text-muted-foreground">复盘周期</span>
          <strong className="text-sm font-semibold tabular-nums">
            {formatRange(from, to)}
          </strong>
        </div>
        <div className="workspace-choice" role="group" aria-label="复盘周期">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            aria-pressed={activePreset === 'week'}
            onClick={() => setRange(getMonday(), todayDateInputValue())}
          >
            本周
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            aria-pressed={activePreset === 'month'}
            onClick={() => setRange(getMonthStart(), todayDateInputValue())}
          >
            本月
          </Button>
        </div>
      </Card>

      {summaryQuery.isLoading || todayQuery.isLoading ? (
        <PlanningLoading label="正在加载每日总结" />
      ) : summaryQuery.error || todayQuery.error ? (
        <Alert variant="destructive">
          <CircleAlert aria-hidden="true" />
          <AlertTitle>每日总结暂时不可用，请稍后重试。</AlertTitle>
        </Alert>
      ) : (
        <>
          <section
            className="mb-4 grid grid-cols-2 gap-3 xl:grid-cols-4"
            aria-label="复盘概览"
          >
            <SummaryMetric
              icon={CheckCircle2}
              label="周期完成"
              value={completedCount}
              hint={`${summary?.active_days ?? 0} 个活跃日`}
            />
            <SummaryMetric
              icon={ListTodo}
              label="今日待办"
              value={todayTasks.length}
              hint="等待推进"
            />
            <SummaryMetric
              icon={Clock3}
              label="逾期任务"
              value={overdueTasks.length}
              hint="需要重新安排"
            />
            <SummaryMetric
              icon={FileText}
              label="最近笔记"
              value={recentNotes.length}
              hint="可继续沉淀"
            />
          </section>

          <div className="grid min-w-0 items-start gap-4 min-[1060px]:grid-cols-[minmax(0,1fr)_minmax(252px,0.48fr)]">
            <Card className="min-w-0 gap-3 py-4 shadow-none">
              <CardHeader className="flex items-center justify-between gap-2 px-4">
                <CardTitle>
                  <h2>任务回顾</h2>
                </CardTitle>
                <Badge variant="secondary">{completionRate}% 综合完成率</Badge>
              </CardHeader>
              <CardContent className="px-4">
                <div
                  className="h-2 overflow-hidden rounded-full bg-muted"
                  role="progressbar"
                  aria-label="综合完成率"
                  aria-valuenow={completionRate}
                >
                  <div
                    className="h-full rounded-full bg-primary"
                    style={{ width: `${completionRate}%` }}
                  />
                </div>
                {!summary?.groups?.length ? (
                  <Empty className="min-h-48">
                    <EmptyHeader>
                      <EmptyMedia variant="icon">
                        <CheckCircle2 aria-hidden="true" />
                      </EmptyMedia>
                      <EmptyTitle>这个周期还没有完成记录</EmptyTitle>
                      <EmptyDescription>
                        完成任务后会按日期汇总到这里。
                      </EmptyDescription>
                    </EmptyHeader>
                  </Empty>
                ) : (
                  <div className="mt-4 grid gap-3">
                    {summary.groups.map((group) => (
                      <section key={group.date}>
                        <header className="flex items-center justify-between rounded-md bg-muted px-3 py-2 text-sm">
                          <time dateTime={group.date} className="font-medium">
                            {formatDay(group.date)}
                          </time>
                          <span className="text-muted-foreground">
                            {group.count} 项
                          </span>
                        </header>
                        {group.tasks.map((task) => (
                          <SummaryRow
                            key={task.id}
                            title={task.title}
                            detail={task.project?.name || '未归属项目'}
                            onOpen={() => navigate('/tasks')}
                          />
                        ))}
                      </section>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>

            <aside className="grid min-w-0 gap-4" aria-label="复盘快捷入口">
              <Card className="gap-3 py-4 shadow-none">
                <CardHeader className="flex items-center justify-between gap-2 px-4">
                  <CardTitle>
                    <h2>需要关注</h2>
                  </CardTitle>
                  <Badge variant="secondary">
                    {overdueTasks.length + todayTasks.length}
                  </Badge>
                </CardHeader>
                <CardContent className="grid gap-1 px-4">
                  {!overdueTasks.length && !todayTasks.length ? (
                    <p className="py-2 text-sm text-muted-foreground">
                      今天没有未完成事项。
                    </p>
                  ) : null}
                  {overdueTasks.slice(0, 3).map((task) => (
                    <FocusTask
                      key={task.id}
                      task={task}
                      overdue
                      onOpen={() => navigate('/tasks')}
                    />
                  ))}
                  {todayTasks
                    .slice(0, Math.max(0, 4 - overdueTasks.length))
                    .map((task) => (
                      <FocusTask
                        key={task.id}
                        task={task}
                        onOpen={() => navigate('/tasks')}
                      />
                    ))}
                  <Button
                    type="button"
                    variant="outline"
                    className="mt-2 w-full"
                    onClick={() => navigate('/tasks')}
                  >
                    打开任务工作台
                  </Button>
                </CardContent>
              </Card>
              <Card className="gap-3 py-4 shadow-none">
                <CardHeader className="flex items-center justify-between gap-2 px-4">
                  <CardTitle>
                    <h2>最近笔记</h2>
                  </CardTitle>
                  <Badge variant="secondary">{recentNotes.length}</Badge>
                </CardHeader>
                <CardContent className="grid gap-1 px-4">
                  {recentNotes.length ? (
                    recentNotes
                      .slice(0, 3)
                      .map((note) => (
                        <SummaryRow
                          key={note.id}
                          title={note.title || '未命名笔记'}
                          detail={formatTimestamp(note.updated_at)}
                          onOpen={() =>
                            navigate(`/editor/${encodeURIComponent(note.id)}`)
                          }
                        />
                      ))
                  ) : (
                    <p className="py-2 text-sm text-muted-foreground">
                      还没有最近更新的笔记。
                    </p>
                  )}
                  <Button
                    type="button"
                    variant="outline"
                    className="mt-2 w-full"
                    onClick={() => navigate('/notes')}
                  >
                    查看笔记库
                  </Button>
                </CardContent>
              </Card>
            </aside>
          </div>
        </>
      )}
    </section>
  )
}

function SummaryMetric({
  icon: Icon,
  label,
  value,
  hint,
}: {
  icon: typeof CheckCircle2
  label: string
  value: number
  hint: string
}) {
  return (
    <Card className="min-w-0 gap-2 px-3 py-3 shadow-none sm:px-4">
      <CardHeader className="flex flex-wrap items-center gap-2 p-0">
        <span className="grid size-8 shrink-0 place-items-center rounded-md bg-accent text-primary">
          <Icon className="size-4" aria-hidden="true" />
        </span>
        <CardTitle className="text-sm">{label}</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-1 p-0">
        <strong className="text-2xl font-semibold tabular-nums">{value}</strong>
        <p className="text-xs text-muted-foreground">{hint}</p>
      </CardContent>
    </Card>
  )
}

function SummaryRow({
  title,
  detail,
  onOpen,
}: {
  title: string
  detail: string
  onOpen: () => void
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      className="h-auto min-h-12 w-full justify-between gap-2 py-2 text-left whitespace-normal"
      onClick={onOpen}
    >
      <span className="grid min-w-0 gap-1">
        <strong className="break-words font-medium">{title}</strong>
        <small className="text-xs font-normal text-muted-foreground">
          {detail}
        </small>
      </span>
      <ArrowRight className="text-muted-foreground" aria-hidden="true" />
    </Button>
  )
}

function FocusTask({
  task,
  overdue = false,
  onOpen,
}: {
  task: TaskData
  overdue?: boolean
  onOpen: () => void
}) {
  return (
    <SummaryRow
      title={task.title}
      detail={`${overdue ? '已逾期' : '今日待办'}${task.project ? ` · ${task.project}` : ''}`}
      onOpen={onOpen}
    />
  )
}

function isTaskDone(task: TaskData) {
  return task.done === 1 || task.occurrence_status === 'done'
}

function formatRange(from: string, to: string) {
  return `${from.replaceAll('-', '.')} - ${to.replaceAll('-', '.')}`
}

function formatDay(value: string) {
  const date = new Date(`${value}T00:00:00`)
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  }).format(date)
}

function formatTimestamp(timestamp: number) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(timestamp * 1000)
}
