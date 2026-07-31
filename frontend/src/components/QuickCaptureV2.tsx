import { type FormEvent, useState } from 'react'

import {
  type ProjectV2,
  TaskDomainRevisionConflictError,
  type TimingType,
} from '../api/taskDomain'
import { useCreateTaskMutation, useProjects } from '../hooks/useTaskDomain'
import { useUIStore } from '../stores/ui'

export function QuickCaptureV2() {
  const setCaptureOpen = useUIStore((state) => state.setCaptureOpen)
  const projectsQuery = useProjects()
  const createTask = useCreateTaskMutation()
  const [title, setTitle] = useState('明天推进最重要的一步')
  const [selectedProjectID, setSelectedProjectID] = useState('')
  const [timingType, setTimingType] = useState<TimingType>('unscheduled')
  const [plannedDate, setPlannedDate] = useState(todayInputValue)
  const [timeRange, setTimeRange] = useState(buildInitialTimeRange)
  const [error, setError] = useState('')
  const availableProjects = (projectsQuery.data ?? []).filter(
    (project) => project.status !== 'completed' && project.status !== 'archived'
  )
  const inbox = availableProjects.find(
    (project) => project.system_role === 'inbox'
  )
  const selectedProject =
    availableProjects.find((project) => project.id === selectedProjectID) ??
    inbox ??
    availableProjects[0]

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selectedProject || title.trim() === '') return
    const durationMinutes = timeRangeMinutes(timeRange.start, timeRange.end)
    if (
      timingType !== 'unscheduled' &&
      (plannedDate === '' ||
        (timingType === 'time_block' && durationMinutes <= 0))
    ) {
      setError('请选择日期，并确保结束时间晚于开始时间。')
      return
    }
    setError('')
    try {
      await createTask.mutateAsync({
        project_id: selectedProject.id,
        title: title.trim(),
        priority: 0,
        schedule: {
          recurrence_type: 'none',
          timing_type: timingType,
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
          ...(timingType === 'unscheduled'
            ? {}
            : {
                starts_on: plannedDate,
                ...(timingType === 'time_block'
                  ? {
                      local_start_time: timeRange.start,
                      duration_minutes: durationMinutes,
                    }
                  : {}),
              }),
        },
      })
      setCaptureOpen(false)
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '收件箱已更新，你的输入仍在。请刷新后重试。'
          : '创建失败，请稍后重试。'
      )
    }
  }

  return (
    <div
      className="quick-capture-overlay"
      onClick={() => setCaptureOpen(false)}
    >
      <form
        className="quick-capture-modal quick-capture-v2"
        role="dialog"
        aria-modal="true"
        aria-labelledby="quick-capture-v2-title"
        onSubmit={handleSubmit}
        onClick={(event) => event.stopPropagation()}
      >
        <header className="quick-capture-heading">
          <div>
            <h2 id="quick-capture-v2-title">快速捕获任务</h2>
            <p>快速记录任务，也可以现在就安排日期和具体时间。</p>
          </div>
          <button
            type="button"
            className="quick-capture-close"
            aria-label="关闭快速捕获"
            onClick={() => setCaptureOpen(false)}
          >
            ×
          </button>
        </header>
        <textarea
          aria-label="快速捕获任务标题"
          className="quick-capture-textarea"
          value={title}
          rows={3}
          autoFocus
          onChange={(event) => setTitle(event.target.value)}
        />
        <div className="quick-capture-v2-destination">
          <label>
            <span>归属项目</span>
            <select
              aria-label="归属项目"
              value={selectedProject?.id ?? ''}
              disabled={
                projectsQuery.isLoading || availableProjects.length === 0
              }
              onChange={(event) => setSelectedProjectID(event.target.value)}
            >
              {availableProjects.map((project) => (
                <option key={project.id} value={project.id}>
                  {projectOptionLabel(project)}
                </option>
              ))}
            </select>
          </label>
          <small>
            默认进入系统收件箱；也可以直接归入正在规划或推进的项目。
          </small>
        </div>
        <div className="quick-capture-v2-schedule">
          <label>
            <span>安排方式</span>
            <select
              aria-label="任务安排方式"
              value={timingType}
              onChange={(event) =>
                setTimingType(event.target.value as TimingType)
              }
            >
              <option value="unscheduled">暂不安排</option>
              <option value="date">指定日期（全天）</option>
              <option value="time_block">指定具体时间</option>
            </select>
          </label>
          {timingType !== 'unscheduled' ? (
            <label>
              <span>执行日期</span>
              <input
                aria-label="任务执行日期"
                type="date"
                value={plannedDate}
                onChange={(event) => setPlannedDate(event.target.value)}
              />
            </label>
          ) : null}
          {timingType === 'time_block' ? (
            <div className="quick-capture-v2-time-range">
              <label>
                <span>开始时间</span>
                <input
                  aria-label="任务开始时间"
                  type="time"
                  value={timeRange.start}
                  onChange={(event) => {
                    const start = event.target.value
                    setTimeRange((current) => ({
                      start,
                      end:
                        timeRangeMinutes(start, current.end) > 0
                          ? current.end
                          : addMinutes(start, 60),
                    }))
                  }}
                />
              </label>
              <span aria-hidden="true">至</span>
              <label>
                <span>结束时间</span>
                <input
                  aria-label="任务结束时间"
                  type="time"
                  value={timeRange.end}
                  onChange={(event) =>
                    setTimeRange((current) => ({
                      ...current,
                      end: event.target.value,
                    }))
                  }
                />
              </label>
            </div>
          ) : null}
          <small>
            {timingType === 'unscheduled'
              ? '任务会进入“无日期”，稍后仍可安排。'
              : timingType === 'date'
                ? '任务会作为全天事项显示。'
                : `将创建 ${timeRangeMinutes(timeRange.start, timeRange.end)} 分钟的时间块。`}
          </small>
        </div>
        {projectsQuery.isError ? (
          <div className="quick-capture-error">无法读取系统收件箱。</div>
        ) : null}
        {error !== '' ? (
          <div className="quick-capture-error">{error}</div>
        ) : null}
        <div className="quick-capture-actions">
          <button
            type="button"
            className="secondary-action"
            onClick={() => setCaptureOpen(false)}
          >
            取消
          </button>
          <button
            type="submit"
            className="primary-action"
            disabled={
              !selectedProject || title.trim() === '' || createTask.isPending
            }
          >
            {createTask.isPending ? '正在创建…' : '创建任务'}
          </button>
        </div>
      </form>
    </div>
  )
}

function projectOptionLabel(project: ProjectV2) {
  if (project.system_role === 'inbox') return `${project.name} · 系统收件箱`
  if (project.system_role === 'personal')
    return `${project.name} · 系统个人项目`
  return `${project.name} · ${project.kind === 'learning' ? '学习项目' : '标准项目'}`
}

function todayInputValue() {
  const now = new Date()
  const year = now.getFullYear()
  const month = String(now.getMonth() + 1).padStart(2, '0')
  const day = String(now.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function buildInitialTimeRange() {
  const now = new Date()
  const nextHour = now.getHours() + 1
  const start = `${String(nextHour >= 23 ? 9 : nextHour).padStart(2, '0')}:00`
  return { start, end: addMinutes(start, 60) }
}

function timeRangeMinutes(start: string, end: string) {
  return timeToMinutes(end) - timeToMinutes(start)
}

function timeToMinutes(value: string) {
  const [hours, minutes] = value.split(':').map(Number)
  if (!Number.isFinite(hours) || !Number.isFinite(minutes)) return 0
  return hours * 60 + minutes
}

function addMinutes(value: string, minutes: number) {
  const total = Math.min(timeToMinutes(value) + minutes, 23 * 60 + 59)
  const hours = Math.floor(total / 60)
  const remainingMinutes = total % 60
  return `${String(hours).padStart(2, '0')}:${String(remainingMinutes).padStart(2, '0')}`
}
