import { type FormEvent, useState } from 'react'

import {
  type ProjectV2,
  TaskDomainRevisionConflictError,
} from '../api/taskDomain'
import { useCreateTaskMutation, useProjects } from '../hooks/useTaskDomain'
import { useUIStore } from '../stores/ui'

export function QuickCaptureV2() {
  const setCaptureOpen = useUIStore((state) => state.setCaptureOpen)
  const projectsQuery = useProjects()
  const createTask = useCreateTaskMutation()
  const [title, setTitle] = useState('明天推进最重要的一步')
  const [selectedProjectID, setSelectedProjectID] = useState('')
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
    setError('')
    try {
      await createTask.mutateAsync({
        project_id: selectedProject.id,
        title: title.trim(),
        priority: 0,
        schedule: {
          recurrence_type: 'none',
          timing_type: 'unscheduled',
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
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
            <p>先捕获，再从项目中心安排日期或重复规则。</p>
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
