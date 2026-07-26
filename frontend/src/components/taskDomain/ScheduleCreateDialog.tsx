import { CalendarClock, X } from 'lucide-react'
import { type FormEvent, useState } from 'react'

import type { ProjectV2 } from '../../api/taskDomain'
import { useCreateTaskMutation } from '../../hooks/useTaskDomain'

export function ScheduleCreateDialog({
  projects,
  onClose,
}: {
  projects: ProjectV2[]
  onClose: () => void
}) {
  const initialTime = buildInitialTimeRange()
  const createTask = useCreateTaskMutation()
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [date, setDate] = useState(todayInputValue)
  const [startTime, setStartTime] = useState(initialTime.start)
  const [endTime, setEndTime] = useState(initialTime.end)
  const [priority, setPriority] = useState(0)
  const [error, setError] = useState('')

  const availableProjects = projects.filter(
    (project) => project.status !== 'completed' && project.status !== 'archived'
  )
  const personalProject = availableProjects.find(
    (project) => project.system_role === 'personal'
  )

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const durationMinutes = timeRangeMinutes(startTime, endTime)
    if (
      title.trim() === '' ||
      !personalProject ||
      date === '' ||
      durationMinutes <= 0
    ) {
      setError('请填写标题，并确保结束时间晚于开始时间。')
      return
    }

    setError('')
    try {
      await createTask.mutateAsync({
        project_id: personalProject.id,
        title: title.trim(),
        description: description.trim() || undefined,
        priority,
        schedule: {
          recurrence_type: 'none',
          timing_type: 'time_block',
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
          starts_on: date,
          local_start_time: startTime,
          duration_minutes: durationMinutes,
        },
      })
      onClose()
    } catch {
      setError('日程创建失败，请稍后重试。')
    }
  }

  return (
    <div className="schedule-create-overlay" onClick={onClose}>
      <form
        className="schedule-create-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="schedule-create-title"
        onSubmit={handleSubmit}
        onClick={(event) => event.stopPropagation()}
      >
        <header className="schedule-create-heading">
          <div className="schedule-create-heading-icon">
            <CalendarClock aria-hidden="true" />
          </div>
          <div>
            <h2 id="schedule-create-title">新增日程</h2>
            <p>创建一个有明确开始和结束时间的任务安排。</p>
          </div>
          <button type="button" aria-label="关闭新增日程" onClick={onClose}>
            <X aria-hidden="true" />
          </button>
        </header>

        <div className="schedule-create-body">
          <label className="schedule-create-title-field">
            <span>日程标题</span>
            <input
              aria-label="日程标题"
              value={title}
              autoFocus
              placeholder="例如：项目方案评审"
              onChange={(event) => setTitle(event.target.value)}
            />
          </label>

          <div className="schedule-create-destination">
            <span>归属项目</span>
            <strong>{personalProject?.name ?? 'Personal 项目不可用'}</strong>
            <small>今日新增日程会自动归入系统 Personal 项目。</small>
          </div>

          <div className="schedule-create-date-row">
            <label>
              <span>日期</span>
              <input
                aria-label="日程日期"
                type="date"
                value={date}
                onChange={(event) => setDate(event.target.value)}
              />
            </label>
            <label>
              <span>优先级</span>
              <select
                aria-label="日程优先级"
                value={priority}
                onChange={(event) => setPriority(Number(event.target.value))}
              >
                <option value={0}>普通</option>
                <option value={1}>中</option>
                <option value={2}>高</option>
                <option value={3}>紧急</option>
              </select>
            </label>
          </div>

          <div className="schedule-create-time-row">
            <label>
              <span>开始时间</span>
              <input
                aria-label="开始时间"
                type="time"
                value={startTime}
                onChange={(event) => {
                  const nextStart = event.target.value
                  setStartTime(nextStart)
                  if (timeRangeMinutes(nextStart, endTime) <= 0) {
                    setEndTime(addMinutes(nextStart, 60))
                  }
                }}
              />
            </label>
            <span aria-hidden="true">至</span>
            <label>
              <span>结束时间</span>
              <input
                aria-label="结束时间"
                type="time"
                value={endTime}
                onChange={(event) => setEndTime(event.target.value)}
              />
            </label>
          </div>

          <label>
            <span>说明（可选）</span>
            <textarea
              aria-label="日程说明"
              value={description}
              rows={3}
              placeholder="补充地点、准备事项或验收结果"
              onChange={(event) => setDescription(event.target.value)}
            />
          </label>

          {error !== '' ? (
            <div className="schedule-create-error" role="alert">
              {error}
            </div>
          ) : null}
        </div>

        <footer className="schedule-create-actions">
          <button type="button" className="secondary-action" onClick={onClose}>
            取消
          </button>
          <button
            type="submit"
            className="primary-action"
            disabled={
              createTask.isPending || title.trim() === '' || !personalProject
            }
          >
            {createTask.isPending ? '正在创建…' : '创建日程'}
          </button>
        </footer>
      </form>
    </div>
  )
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
  return hours * 60 + minutes
}

function addMinutes(value: string, minutes: number) {
  const total = timeToMinutes(value) + minutes
  const hours = Math.floor(total / 60) % 24
  const remainder = total % 60
  return `${String(hours).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
}
