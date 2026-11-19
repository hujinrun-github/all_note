import { ChevronDown, Layers3 } from 'lucide-react'
import type { ReactNode } from 'react'
import type { OccurrenceV2, TaskV2 } from '../../api/taskDomain'
import {
  collectionScheduleRange,
  collectionStatusSummary,
} from '../../utils/occurrenceCollections'
import { cn } from '../../lib/utils'

export function OccurrenceCollection({
  task,
  projectName,
  occurrences,
  expanded,
  onToggle,
  children,
}: {
  task?: TaskV2
  projectName?: string
  occurrences: OccurrenceV2[]
  expanded: boolean
  onToggle: () => void
  children: ReactNode
}) {
  const title = task?.title ?? occurrences[0]?.title ?? '未命名任务'
  const collectionID = `occurrence-collection-${occurrences[0]?.id}`
  return (
    <section
      className={cn('td-occurrence-collection', expanded && 'is-expanded')}
      role="listitem"
    >
      <button
        type="button"
        className="td-occurrence-collection-toggle"
        aria-expanded={expanded}
        aria-controls={collectionID}
        aria-label={`${expanded ? '收起' : '展开'}${title}，${occurrences.length} 次执行`}
        onClick={onToggle}
      >
        <span className="td-occurrence-collection-icon" aria-hidden="true">
          <Layers3 />
        </span>
        <span className="td-occurrence-collection-copy">
          <strong title={title}>{title}</strong>
          <span>
            {projectName ?? '未命名项目'} ·{' '}
            {collectionStatusSummary(occurrences)}
          </span>
        </span>
        <span className="td-occurrence-collection-range">
          {collectionScheduleRange(occurrences)}
        </span>
        <span className="td-occurrence-collection-count">
          {occurrences.length} 次执行
        </span>
        <ChevronDown
          className="td-occurrence-collection-chevron"
          aria-hidden="true"
        />
      </button>
      {expanded ? (
        <div
          id={collectionID}
          className="td-occurrence-collection-items"
          role="list"
          aria-label={`${title}执行实例`}
        >
          {children}
        </div>
      ) : null}
    </section>
  )
}
