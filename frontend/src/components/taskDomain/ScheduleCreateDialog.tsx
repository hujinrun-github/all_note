import { CalendarClock, X } from 'lucide-react'
import { type FormEvent, useState } from 'react'

import type { ProjectV2 } from '../../api/taskDomain'
import { useCreateTaskMutation } from '../../hooks/useTaskDomain'
import {
  addMinutes,
  buildTaskScheduleInput,
  createTaskScheduleDraft,
  type TaskScheduleDraft,
  TaskRecurrenceField,
  taskScheduleValidationError,
  timeRangeMinutes,
} from './TaskScheduleFields'

export function ScheduleCreateDialog({
  projects,
  onClose,
  initialSchedule,
  timezone,
}: {
  projects: ProjectV2[]
  onClose: () => void
  initialSchedule?: Partial<TaskScheduleDraft>
  timezone?: string
}) {
  const createTask = useCreateTaskMutation()
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [schedule, setSchedule] = useState(() => ({
    ...createTaskScheduleDraft('time_block'),
    ...initialSchedule,
  }))
  const [priority, setPriority] = useState(0)
  const [selectedProjectID, setSelectedProjectID] = useState('')
  const [error, setError] = useState('')

  const availableProjects = projects.filter(
    (project) => project.status !== 'completed' && project.status !== 'archived'
  )
  const personalProject = availableProjects.find(
    (project) => project.system_role === 'personal'
  )
  const selectedProject =
    availableProjects.find((project) => project.id === selectedProjectID) ??
    personalProject ??
    availableProjects[0]

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const scheduleError = taskScheduleValidationError(schedule)
    if (title.trim() === '' || !selectedProject || scheduleError !== '') {
      setError(scheduleError || '请填写日程标题。')
      return
    }

    setError('')
    try {
      await createTask.mutateAsync({
        project_id: selectedProject.id,
        title: title.trim(),
        description: description.trim() || undefined,
        priority,
        schedule: buildTaskScheduleInput(schedule, timezone),
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
            <select
              aria-label="日程归属项目"
              value={selectedProject?.id ?? ''}
              disabled={availableProjects.length === 0}
              onChange={(event) => setSelectedProjectID(event.target.value)}
            >
              {availableProjects.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name}
                </option>
              ))}
            </select>
            <small>
              默认优先选择系统 Personal 项目；也可以归入其他可用项目。
            </small>
          </div>

          <div className="schedule-create-date-row">
            <label>
              <span>日期</span>
              <input
                aria-label="日程日期"
                type="date"
                value={schedule.startsOn}
                onChange={(event) =>
                  setSchedule((current) => ({
                    ...current,
                    startsOn: event.target.value,
                  }))
                }
              />
            </label>
            <TaskRecurrenceField
              value={schedule}
              onChange={setSchedule}
              labelPrefix="日程"
            />
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
                value={schedule.startTime}
                onChange={(event) => {
                  const nextStart = event.target.value
                  setSchedule((current) => ({
                    ...current,
                    startTime: nextStart,
                    endTime:
                      timeRangeMinutes(nextStart, current.endTime) > 0
                        ? current.endTime
                        : addMinutes(nextStart, 60),
                  }))
                }}
              />
            </label>
            <span aria-hidden="true">至</span>
            <label>
              <span>结束时间</span>
              <input
                aria-label="结束时间"
                type="time"
                value={schedule.endTime}
                onChange={(event) =>
                  setSchedule((current) => ({
                    ...current,
                    endTime: event.target.value,
                  }))
                }
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
              createTask.isPending || title.trim() === '' || !selectedProject
            }
          >
            {createTask.isPending ? '正在创建…' : '创建日程'}
          </button>
        </footer>
      </form>
    </div>
  )
}
