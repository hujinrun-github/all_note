import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useSummary } from '../hooks/useSummary'

function getMonday(d: Date = new Date()): string {
  const day = d.getDay()
  const diff = d.getDate() - day + (day === 0 ? -6 : 1)
  const monday = new Date(d.getFullYear(), d.getMonth(), diff)
  return `${monday.getFullYear()}-${String(monday.getMonth() + 1).padStart(2, '0')}-${String(monday.getDate()).padStart(2, '0')}`
}

function todayDateInputValue(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function getMonthStart(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`
}

export default function DailySummary() {
  const [searchParams, setSearchParams] = useSearchParams()
  const from = searchParams.get('from') || getMonday()
  const to = searchParams.get('to') || todayDateInputValue()
  const page = parseInt(searchParams.get('page') || '1', 10)
  const { data, isLoading, error } = useSummary(from, to, page)

  const activePreset = useMemo(() => {
    if (from === getMonday() && to === todayDateInputValue()) return 'week'
    if (from === getMonthStart() && to === todayDateInputValue()) return 'month'
    return null
  }, [from, to])

  function setRange(newFrom: string, newTo: string) {
    setSearchParams({ from: newFrom, to: newTo, page: '1' })
  }

  if (isLoading) {
    return (
      <div className="summary-page">
        <section className="metric-strip">
          {Array.from({ length: 4 }).map((_, index) => (
            <div key={index} className="metric-tile animate-pulse" />
          ))}
        </section>
      </div>
    )
  }

  if (error) {
    return (
      <div className="empty-state">
        <strong>加载失败</strong>
        <p>每日总结暂时不可用，请稍后重试。</p>
      </div>
    )
  }

  const completedCount = data?.pagination.total ?? 0
  const firstCompleted = data?.summary.groups?.[0]?.tasks?.[0]

  return (
    <div className="summary-page">
      <div className="page-local-actions">
        <div className="segmented-tabs">
          <button className={activePreset === 'week' ? 'is-active' : ''} onClick={() => setRange(getMonday(), todayDateInputValue())}>
            本周
          </button>
          <button className={activePreset === 'month' ? 'is-active' : ''} onClick={() => setRange(getMonthStart(), todayDateInputValue())}>
            本月
          </button>
          <button type="button">{to.replaceAll('-', '/')}</button>
        </div>
      </div>

      <section className="metric-strip">
        <Metric label="任务完成" value={String(completedCount)} hint="较昨日 --" />
        <Metric label="新增任务" value="1" hint="较昨日 +1" />
        <Metric label="逾期任务" value="1" hint="需要处理" tone="danger" />
        <Metric label="笔记产出" value="1" hint="较昨日 +1" tone="success" />
      </section>

      <section className="summary-board">
        <article className="surface-panel summary-completed-panel">
          <h2>今日完成</h2>
          {firstCompleted ? (
            <div className="summary-completed-task">
              <span>✓</span>
              <strong>{firstCompleted.title}</strong>
            </div>
          ) : (
            <div className="summary-empty-illustration">
              <i />
              <p>暂无完成的任务</p>
            </div>
          )}
          <div className="summary-progress">
            <strong>完成率</strong>
            <i><span style={{ width: completedCount > 0 ? '100%' : '0%' }} /></i>
            <em>{completedCount} / {Math.max(completedCount, 1)} 项</em>
          </div>
        </article>

        <article className="surface-panel summary-undone-panel">
          <div className="panel-heading is-compact">
            <div>
              <h2>未完成事项</h2>
            </div>
            <button className="soft-danger-badge" type="button">建议处理</button>
          </div>
          <div className="summary-task-alert">
            <label>
              <input type="checkbox" readOnly />
              <strong>尝试 kapathy 的知识库方案</strong>
            </label>
            <div>
              <span className="task-project-tag">逾期 4 天</span>
              <span className="task-project-tag">Personal</span>
            </div>
            <footer>
              <button>延期到明天</button>
              <button>重新设定日期</button>
              <button>归档</button>
            </footer>
          </div>
          <div className="summary-advice-box">
            <h3>复盘建议</h3>
            <p>这个任务已经逾期，建议先把它拆成一个可执行的小步骤：今天只完成资料整理或方案大纲。</p>
          </div>
        </article>

        <aside className="surface-panel summary-knowledge-panel">
          <h2>今日笔记</h2>
          <article className="linked-note-card">
            <strong>第一篇笔记</strong>
            <span>128 字 · Personal</span>
          </article>
          <h3>知识产出</h3>
          <div className="knowledge-meter">
            <span />
          </div>
          <p>最近更新 1 篇</p>
          <button className="wide-secondary-action" type="button">查看全部笔记 →</button>
        </aside>
      </section>
    </div>
  )
}

function Metric({ label, value, hint, tone }: { label: string; value: string; hint: string; tone?: 'danger' | 'success' }) {
  return (
    <div className="metric-tile">
      <span>{label}</span>
      <strong className={tone === 'danger' ? 'is-danger' : tone === 'success' ? 'is-success' : ''}>{value}</strong>
      <p>{hint}</p>
    </div>
  )
}
