import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import {
  useCompleteOccurrenceMutation,
  useOccurrences,
  useTaskDefinitions,
} from '../hooks/useTaskDomain'

type TodayTab = 'today' | 'overdue' | 'done'

export default function DashboardV2() {
  const [activeTab, setActiveTab] = useState<TodayTab>('today')
  const todayQuery = useOccurrences({ scope: 'today' })
  const overdueQuery = useOccurrences({ scope: 'overdue' })
  const completedQuery = useOccurrences({ scope: 'completed' })
  const definitionsQuery = useTaskDefinitions()
  const completeOccurrence = useCompleteOccurrenceMutation()
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
  const queries = {
    today: todayQuery,
    overdue: overdueQuery,
    done: completedQuery,
  }
  const activeQuery = queries[activeTab]

  return (
    <div className="dashboard-v2">
      <section className="metric-strip">
        <Metric label="已逾期" value={overdueQuery.data?.length ?? 0} />
        <Metric label="今天" value={todayQuery.data?.length ?? 0} />
        <Metric
          label="进行中"
          value={(todayQuery.data ?? []).filter((item) => item.execution_status === 'active').length}
        />
        <Metric label="已完成" value={completedQuery.data?.length ?? 0} />
      </section>

      <section className="surface-panel dashboard-v2-flow">
        <header>
          <div>
            <span className="domain-eyebrow">TODAY</span>
            <h2>今天的执行流</h2>
            <p>今天和逾期保持分开，先推进你主动选择的工作。</p>
          </div>
          <Link to="/tasks">打开任务工作台</Link>
        </header>
        <div className="task-v2-tabs" role="tablist" aria-label="今日任务筛选">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'today'}
            className={activeTab === 'today' ? 'is-active' : ''}
            onClick={() => setActiveTab('today')}
          >
            今天 {todayQuery.data?.length ?? 0}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'overdue'}
            className={activeTab === 'overdue' ? 'is-active' : ''}
            onClick={() => setActiveTab('overdue')}
          >
            已逾期 {overdueQuery.data?.length ?? 0}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === 'done'}
            className={activeTab === 'done' ? 'is-active' : ''}
            onClick={() => setActiveTab('done')}
          >
            已完成 {completedQuery.data?.length ?? 0}
          </button>
        </div>

        {activeQuery.isLoading || definitionsQuery.isLoading ? (
          <p className="domain-empty">正在加载今天的执行实例…</p>
        ) : null}
        {activeQuery.isError ? <p className="domain-empty">今日执行流暂时不可用。</p> : null}
        <div className="dashboard-v2-list" role="list" aria-label="今日执行实例">
          {(activeQuery.data ?? []).map((occurrence) => {
            const definition = definitions.get(occurrence.task_id)
            const title = occurrence.title ?? definition?.title ?? '未命名任务'
            return (
              <article role="listitem" className="dashboard-v2-row" key={occurrence.id}>
                <button
                  type="button"
                  aria-label={`完成${title}`}
                  disabled={!definition || occurrence.execution_status === 'done'}
                  onClick={() => {
                    if (!definition) return
                    void completeOccurrence.mutateAsync({
                      projectID: occurrence.project_id ?? definition.project_id,
                      taskID: definition.id,
                      occurrenceID: occurrence.id,
                      expectedRevisions: {
                        expected_task_revision:
                          occurrence.task_revision ?? definition.revision,
                        expected_schedule_revision:
                          occurrence.schedule_revision ?? definition.schedule_revision,
                        expected_occurrence_revisions: {
                          [occurrence.id]: occurrence.revision,
                        },
                      },
                    })
                  }}
                >
                  {occurrence.execution_status === 'done' ? '✓' : ''}
                </button>
                <div>
                  <strong>{title}</strong>
                  <span>执行状态：{executionLabel(occurrence.execution_status)}</span>
                </div>
              </article>
            )
          })}
        </div>
        {(activeQuery.data?.length ?? 0) === 0 && !activeQuery.isLoading ? (
          <p className="domain-empty">这个视图里暂时没有执行实例。</p>
        ) : null}
      </section>
    </div>
  )
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <article className="metric-tile">
      <span>{label}</span>
      <strong>{value}</strong>
      <p>执行实例</p>
    </article>
  )
}

function executionLabel(status: string) {
  return (
    {
      open: '未开始',
      active: '进行中',
      blocked: '阻塞',
      done: '已完成',
      skipped: '已跳过',
      cancelled: '已取消',
    }[status] ?? status
  )
}
