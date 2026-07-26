import { CalendarDays, CalendarPlus, Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'

import type { OccurrenceV2, TaskV2 } from '../api/taskDomain'
import {
  OccurrenceInspector,
  OccurrenceRow,
} from '../components/taskDomain/TaskDomainWorkspace'
import { ScheduleCreateDialog } from '../components/taskDomain/ScheduleCreateDialog'
import {
  useBlockOccurrenceMutation,
  useCompleteOccurrenceMutation,
  useOccurrences,
  useProjects,
  useReopenOccurrenceMutation,
  useStartOccurrenceMutation,
  useTaskDefinitions,
  useUnblockOccurrenceMutation,
} from '../hooks/useTaskDomain'
import { useUIStore } from '../stores/ui'

type TodayTab = 'today' | 'overdue' | 'done'

const todayTabs: Array<{ id: TodayTab; label: string }> = [
  { id: 'today', label: '今天' },
  { id: 'overdue', label: '已逾期' },
  { id: 'done', label: '已完成' },
]

export default function DashboardV2() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [scheduleCreateOpen, setScheduleCreateOpen] = useState(false)
  const navigate = useNavigate()
  const setCaptureOpen = useUIStore((state) => state.setCaptureOpen)
  const requestedTab = searchParams.get('view')
  const activeTab: TodayTab =
    requestedTab === 'overdue' || requestedTab === 'done'
      ? requestedTab
      : 'today'
  const selectedOccurrenceID = searchParams.get('occurrence_id') ?? ''

  const todayQuery = useOccurrences({ scope: 'today' })
  const overdueQuery = useOccurrences({ scope: 'overdue' })
  const completedQuery = useOccurrences({ scope: 'completed' })
  const definitionsQuery = useTaskDefinitions()
  const projectsQuery = useProjects()
  const completeOccurrence = useCompleteOccurrenceMutation()
  const startOccurrence = useStartOccurrenceMutation()
  const blockOccurrence = useBlockOccurrenceMutation()
  const unblockOccurrence = useUnblockOccurrenceMutation()
  const reopenOccurrence = useReopenOccurrenceMutation()

  const definitions = useMemo(
    () =>
      new Map(
        (definitionsQuery.data ?? []).map((definition) => [
          definition.id,
          definition,
        ])
      ),
    [definitionsQuery.data]
  )
  const projects = useMemo(
    () =>
      new Map(
        (projectsQuery.data ?? []).map((project) => [project.id, project])
      ),
    [projectsQuery.data]
  )
  const queries = {
    today: todayQuery,
    overdue: overdueQuery,
    done: completedQuery,
  }
  const activeQuery = queries[activeTab]
  const activeOccurrences = activeQuery.data ?? []
  const timedOccurrences = activeOccurrences.filter(
    (occurrence) => occurrence.planned_start_at
  )
  const flexibleOccurrences = activeOccurrences.filter(
    (occurrence) => !occurrence.planned_start_at
  )
  const selectedOccurrence = activeOccurrences.find(
    (occurrence) => occurrence.id === selectedOccurrenceID
  )
  const selectedTask = selectedOccurrence
    ? definitions.get(selectedOccurrence.task_id)
    : undefined
  const selectedProject = selectedTask
    ? projects.get(selectedTask.project_id)
    : undefined
  const busy =
    completeOccurrence.isPending ||
    startOccurrence.isPending ||
    blockOccurrence.isPending ||
    unblockOccurrence.isPending ||
    reopenOccurrence.isPending

  function updateSearchParams(
    update: Record<string, string | null>,
    replace = false
  ) {
    const next = new URLSearchParams(searchParams)
    Object.entries(update).forEach(([key, value]) => {
      if (value === null || value === '') next.delete(key)
      else next.set(key, value)
    })
    setSearchParams(next, { replace })
  }

  function expectedRevisions(task: TaskV2, occurrence: OccurrenceV2) {
    return {
      expected_task_revision: occurrence.task_revision ?? task.revision,
      expected_schedule_revision:
        occurrence.schedule_revision ?? task.schedule_revision,
      expected_occurrence_revisions: {
        [occurrence.id]: occurrence.revision,
      },
    }
  }

  function commandVariables(task: TaskV2, occurrence: OccurrenceV2) {
    return {
      projectID: occurrence.project_id ?? task.project_id,
      taskID: task.id,
      occurrenceID: occurrence.id,
      expectedRevisions: expectedRevisions(task, occurrence),
    }
  }

  function renderOccurrence(occurrence: OccurrenceV2) {
    const task = definitions.get(occurrence.task_id)
    const project = task ? projects.get(task.project_id) : undefined
    return (
      <OccurrenceRow
        key={occurrence.id}
        occurrence={occurrence}
        task={task}
        project={project}
        selected={occurrence.id === selectedOccurrenceID}
        onSelect={() =>
          updateSearchParams({ occurrence_id: occurrence.id, task_id: null })
        }
        onComplete={
          task
            ? () =>
                void completeOccurrence.mutateAsync(
                  commandVariables(task, occurrence)
                )
            : undefined
        }
      />
    )
  }

  return (
    <section className="td-page td-today-page" aria-labelledby="today-heading">
      <header className="td-page-header">
        <div>
          <div className="td-title-line">
            <h1 id="today-heading">今天</h1>
            <time>{formatToday()}</time>
          </div>
          <p>先完成今天，逾期事项不会抢占你的注意力。</p>
        </div>
        <div className="td-page-actions">
          <button
            type="button"
            className="td-secondary-action"
            onClick={() => setScheduleCreateOpen(true)}
          >
            <CalendarPlus aria-hidden="true" />
            新增日程
          </button>
          <button
            type="button"
            className="td-primary-action"
            onClick={() => setCaptureOpen(true)}
          >
            <Plus aria-hidden="true" />
            添加任务
          </button>
        </div>
      </header>

      <div className="td-tabs" role="tablist" aria-label="今日任务筛选">
        {todayTabs.map((tab) => {
          const count = queries[tab.id].data?.length ?? 0
          return (
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === tab.id}
              className={activeTab === tab.id ? 'is-active' : ''}
              key={tab.id}
              onClick={() =>
                updateSearchParams({
                  view: tab.id === 'today' ? null : tab.id,
                  occurrence_id: null,
                  task_id: null,
                })
              }
            >
              {tab.label} <span>{count}</span>
            </button>
          )
        })}
      </div>

      <div
        className={`td-workspace ${selectedOccurrence ? 'has-inspector' : ''}`}
      >
        <div className="td-list-canvas">
          {activeQuery.isLoading || definitionsQuery.isLoading ? (
            <div className="td-loading-list" aria-label="正在加载今日执行">
              <span />
              <span />
              <span />
            </div>
          ) : null}
          {activeQuery.isError ? (
            <div className="td-inline-error" role="alert">
              今日执行暂时不可用，请刷新后重试。
            </div>
          ) : null}

          {activeTab === 'today' && timedOccurrences.length > 0 ? (
            <>
              <SectionHeading
                title="固定时间"
                count={`${timedOccurrences.length} 个安排`}
              />
              <div
                className="td-occurrence-list is-timeline"
                role="list"
                aria-label="固定时间任务"
              >
                {timedOccurrences.map(renderOccurrence)}
              </div>
            </>
          ) : null}

          <SectionHeading
            title={
              activeTab === 'today'
                ? '今天要做'
                : activeTab === 'overdue'
                  ? '已逾期'
                  : '今天已完成'
            }
            count={`${flexibleOccurrences.length} 个行动`}
          />
          <div
            className="td-occurrence-list"
            role="list"
            aria-label="今日执行实例"
          >
            {flexibleOccurrences.map(renderOccurrence)}
          </div>

          {activeOccurrences.length === 0 && !activeQuery.isLoading ? (
            <div className="td-empty-state">
              <CalendarDays aria-hidden="true" />
              <strong>这里还没有执行实例</strong>
              <span>
                {activeTab === 'today'
                  ? '添加一个今天要完成的任务。'
                  : '切换视图或调整任务安排。'}
              </span>
            </div>
          ) : null}
        </div>

        {selectedOccurrence ? (
          <OccurrenceInspector
            key={selectedOccurrence.id}
            occurrence={selectedOccurrence}
            task={selectedTask}
            project={selectedProject}
            busy={busy}
            onClose={() =>
              updateSearchParams({ occurrence_id: null, task_id: null }, true)
            }
            onStart={
              selectedTask
                ? () =>
                    startOccurrence.mutateAsync(
                      commandVariables(selectedTask, selectedOccurrence)
                    )
                : undefined
            }
            onComplete={
              selectedTask
                ? () =>
                    completeOccurrence.mutateAsync(
                      commandVariables(selectedTask, selectedOccurrence)
                    )
                : undefined
            }
            onReopen={
              selectedTask
                ? () =>
                    reopenOccurrence.mutateAsync(
                      commandVariables(selectedTask, selectedOccurrence)
                    )
                : undefined
            }
            onBlock={
              selectedTask
                ? (reason, nextAction) =>
                    blockOccurrence.mutateAsync({
                      ...commandVariables(selectedTask, selectedOccurrence),
                      blockedReason: reason,
                      nextAction,
                    })
                : undefined
            }
            onUnblock={
              selectedTask
                ? () =>
                    unblockOccurrence.mutateAsync(
                      commandVariables(selectedTask, selectedOccurrence)
                    )
                : undefined
            }
            onViewTask={
              selectedTask
                ? () =>
                    navigate(
                      `/tasks?task_id=${encodeURIComponent(selectedTask.id)}`
                    )
                : undefined
            }
          />
        ) : null}
      </div>

      {scheduleCreateOpen ? (
        <ScheduleCreateDialog
          projects={projectsQuery.data ?? []}
          onClose={() => setScheduleCreateOpen(false)}
        />
      ) : null}
    </section>
  )
}

function SectionHeading({ title, count }: { title: string; count: string }) {
  return (
    <header className="td-section-heading">
      <strong>{title}</strong>
      <span>{count}</span>
    </header>
  )
}

function formatToday() {
  const date = new Date()
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'long',
  }).format(date)
}
