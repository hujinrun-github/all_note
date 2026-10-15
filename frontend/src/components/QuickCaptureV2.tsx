import { type FormEvent, useRef, useState } from 'react'
import { Modal } from './ui/Modal'

import {
  type ProjectV2,
  TaskDomainRevisionConflictError,
} from '../api/taskDomain'
import { useCreateTaskMutation, useProjects } from '../hooks/useTaskDomain'
import { useUIStore } from '../stores/ui'
import {
  buildTaskScheduleInput,
  createTaskScheduleDraft,
  TaskScheduleFields,
  taskScheduleValidationError,
} from './taskDomain/TaskScheduleFields'

export function QuickCaptureV2() {
  const setCaptureOpen = useUIStore((state) => state.setCaptureOpen)
  const showNotice = useUIStore((state) => state.showNotice)
  const projectsQuery = useProjects()
  const createTask = useCreateTaskMutation()
  const [title, setTitle] = useState('')
  const submitting = useRef(false)
  const [selectedProjectID, setSelectedProjectID] = useState('')
  const [schedule, setSchedule] = useState(createTaskScheduleDraft)
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
    if (!selectedProject || title.trim() === '' || submitting.current) return
    const scheduleError = taskScheduleValidationError(schedule)
    if (scheduleError !== '') {
      setError(scheduleError)
      return
    }
    setError('')
    submitting.current = true
    try {
      await createTask.mutateAsync({
        project_id: selectedProject.id,
        title: title.trim(),
        priority: 0,
        schedule: buildTaskScheduleInput(schedule),
      })
      showNotice(`任务已添加到「${selectedProject.name}」`)
      setCaptureOpen(false)
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '收件箱已更新，你的输入仍在。请刷新后重试。'
          : '创建失败，请稍后重试。'
      )
    } finally {
      submitting.current = false
    }
  }

  return (
    <Modal
      className="capture-dialog"
      labelledBy="quick-capture-v2-title"
      onClose={() => {
        if (!submitting.current) setCaptureOpen(false)
      }}
    >
      <form
        className="quick-capture-modal quick-capture-v2"
        onSubmit={handleSubmit}
        onKeyDown={(event) => {
          if (
            (event.metaKey || event.ctrlKey) &&
            event.key === 'Enter' &&
            !event.nativeEvent.isComposing
          ) {
            event.preventDefault()
            event.currentTarget.requestSubmit()
          }
        }}
      >
        <header className="quick-capture-heading">
          <div>
            <h2 id="quick-capture-v2-title">快速捕获任务</h2>
            <p>先记下来，再从容安排。默认存入收件箱，也可以选择项目和日期。</p>
          </div>
          <button
            type="button"
            className="quick-capture-close"
            aria-label="关闭快速捕获"
            disabled={createTask.isPending}
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
          placeholder="有什么需要记下来？"
          autoFocus
          data-modal-autofocus
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
        <TaskScheduleFields
          className="quick-capture-v2-schedule"
          value={schedule}
          onChange={setSchedule}
        />
        {projectsQuery.isError ? (
          <div className="quick-capture-error" role="alert">
            无法读取项目，请稍后重新打开。
          </div>
        ) : null}
        {!projectsQuery.isLoading &&
        !projectsQuery.isError &&
        availableProjects.length === 0 ? (
          <div className="quick-capture-error" role="alert">
            暂无可用项目，请先创建项目。
          </div>
        ) : null}
        {error !== '' ? (
          <div className="quick-capture-error" role="alert">
            {error}
          </div>
        ) : null}
        <div className="quick-capture-actions">
          <button
            type="button"
            className="secondary-action"
            disabled={createTask.isPending}
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
    </Modal>
  )
}

function projectOptionLabel(project: ProjectV2) {
  if (project.system_role === 'inbox') return `${project.name} · 系统收件箱`
  if (project.system_role === 'personal')
    return `${project.name} · 系统个人项目`
  return `${project.name} · ${project.kind === 'learning' ? '学习项目' : '标准项目'}`
}
