import type { OccurrenceV2 } from '../api/taskDomain'

export interface OccurrenceCollectionData {
  taskID: string
  occurrences: OccurrenceV2[]
}

// Group only the caller's visible results. Map insertion order preserves the
// first visible position of each task and the server order of its executions.
export function collectOccurrencesByTask(
  occurrences: OccurrenceV2[],
  groupByTask = true
): OccurrenceCollectionData[] {
  if (!groupByTask)
    return occurrences.map((occurrence) => ({
      taskID: occurrence.task_id,
      occurrences: [occurrence],
    }))
  const collections = new Map<string, OccurrenceCollectionData>()
  for (const occurrence of occurrences) {
    const collection = collections.get(occurrence.task_id)
    if (collection) collection.occurrences.push(occurrence)
    else
      collections.set(occurrence.task_id, {
        taskID: occurrence.task_id,
        occurrences: [occurrence],
      })
  }
  return Array.from(collections.values())
}

export function collectionStatusSummary(occurrences: OccurrenceV2[]) {
  const counts = new Map<OccurrenceV2['execution_status'], number>()
  for (const occurrence of occurrences)
    counts.set(
      occurrence.execution_status,
      (counts.get(occurrence.execution_status) ?? 0) + 1
    )
  const labels: Array<[OccurrenceV2['execution_status'], string]> = [
    ['open', '待执行'],
    ['active', '进行中'],
    ['blocked', '阻塞'],
    ['done', '已完成'],
    ['skipped', '已跳过'],
    ['cancelled', '已取消'],
  ]
  return labels
    .flatMap(([status, label]) => {
      const count = counts.get(status) ?? 0
      return count > 0 ? [`${count} ${label}`] : []
    })
    .join(' · ')
}

export function collectionScheduleRange(occurrences: OccurrenceV2[]) {
  const dates = occurrences
    .flatMap((occurrence) => {
      const date = occurrenceDate(occurrence)
      return date ? [date] : []
    })
    .sort()
  if (!dates.length) return '未安排'
  const first = dates[0]
  const last = dates.at(-1)!
  const includeYear = first.slice(0, 4) !== last.slice(0, 4)
  const range =
    first === last
      ? dateLabel(first, includeYear)
      : `${dateLabel(first, includeYear)} – ${dateLabel(last, includeYear)}`
  const unscheduledCount = occurrences.length - dates.length
  return unscheduledCount ? `${range} · ${unscheduledCount} 次未安排` : range
}

function occurrenceDate(occurrence: OccurrenceV2) {
  if (occurrence.planned_date) return occurrence.planned_date
  if (!occurrence.planned_start_at) return undefined
  const instant = new Date(occurrence.planned_start_at)
  if (Number.isNaN(instant.getTime())) return undefined
  const parts = new Intl.DateTimeFormat('en-US', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    timeZone: occurrence.timezone,
  }).formatToParts(instant)
  const part = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((value) => value.type === type)?.value
  return `${part('year')}-${part('month')}-${part('day')}`
}

function dateLabel(value: string, includeYear: boolean) {
  const [year, month, day] = value.split('-').map(Number)
  return `${includeYear ? `${year}年` : ''}${month}月${day}日`
}
