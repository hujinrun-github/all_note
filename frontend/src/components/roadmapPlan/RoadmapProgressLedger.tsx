import { ArrowRight, Flag, GitBranch, ListChecks } from 'lucide-react'
import { Link } from 'react-router-dom'

import type { RoadmapNodeV2 } from '../../api/roadmapV2'

export function RoadmapProgressLedger({
  node,
  projectID,
  progress,
  totalTasks,
  totalOccurrences,
  doneOccurrences,
}: {
  node: RoadmapNodeV2
  projectID: string
  progress: number
  totalTasks: number
  totalOccurrences: number
  doneOccurrences: number
}) {
  return (
    <aside className="plan-progress-ledger" aria-label="路线进度与下一步">
      <section>
        <header>
          <span>总进度</span>
          <strong>{progress}%</strong>
        </header>
        <div className="plan-progress-ring">
          <svg viewBox="0 0 100 100" aria-hidden="true">
            <circle className="is-track" cx="50" cy="50" r="42" pathLength="100" />
            <circle
              className="is-value"
              cx="50"
              cy="50"
              r="42"
              pathLength="100"
              strokeDasharray={`${progress} 100`}
            />
          </svg>
          <span>
            <strong>{doneOccurrences}</strong>
            <small>/ {totalOccurrences} 次执行</small>
          </span>
        </div>
        <dl>
          <div>
            <dt>路线节点</dt>
            <dd>当前第 {node.position + 1} 步</dd>
          </div>
          <div>
            <dt>任务定义</dt>
            <dd>{totalTasks} 项</dd>
          </div>
          <div>
            <dt>待完成执行</dt>
            <dd>{Math.max(0, totalOccurrences - doneOccurrences)} 次</dd>
          </div>
        </dl>
      </section>

      <section className="plan-next-step">
        <span className="plan-ledger-icon" aria-hidden="true">
          <Flag />
        </span>
        <div>
          <span>下一步</span>
          <strong>继续推进「{node.title}」</strong>
          <p>
            {node.progress.blocked > 0
              ? `先处理 ${node.progress.blocked} 个阻塞项，再继续安排新的执行。`
              : node.progress.tasks > 0
                ? `已有 ${node.progress.tasks} 个关联任务，可以在脑图中集中查看。`
                : '先建立一项清晰、可验证的关联任务。'}
          </p>
        </div>
        <Link
          to={`/projects/${encodeURIComponent(projectID)}/roadmap/nodes/${encodeURIComponent(node.id)}/mind-map`}
        >
          <GitBranch aria-hidden="true" />
          进入任务脑图
          <ArrowRight aria-hidden="true" />
        </Link>
      </section>

      <section className="plan-ledger-note">
        <ListChecks aria-hidden="true" />
        <p>
          路线负责学习结构，任务负责实际执行；节点完成度由任务执行记录自动汇总。
        </p>
      </section>
    </aside>
  )
}
