import { type FormEvent, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { TaskDomainRevisionConflictError } from '../api/taskDomain'
import {
  useCompleteProjectMutation,
  useCancelTaskMutation,
  useCreateTaskMutation,
  useOccurrences,
  useProject,
  useProjects,
  useTaskDefinitions,
  useUpdateTaskDefinitionMutation,
} from '../hooks/useTaskDomain'

const terminalStatuses = new Set(['done', 'skipped', 'cancelled'])

export default function ProjectDetail() {
  const { projectID = '' } = useParams()
  const projectQuery = useProject(projectID)
  const projectsQuery = useProjects()
  const tasksQuery = useTaskDefinitions({ project_id: projectID })
  const occurrencesQuery = useOccurrences({ project_id: projectID })
  const createTask = useCreateTaskMutation()
  const completeProject = useCompleteProjectMutation()
  const cancelTask = useCancelTaskMutation()
  const updateTask = useUpdateTaskDefinitionMutation()
  const [title, setTitle] = useState('')
  const [error, setError] = useState('')
  const [completionDecisionOpen, setCompletionDecisionOpen] = useState(false)
  const [completionDecision, setCompletionDecision] = useState<'choose' | 'move'>('choose')
  const [targetProjectID, setTargetProjectID] = useState('')
  const tasksByID = useMemo(
    () => new Map((tasksQuery.data ?? []).map((task) => [task.id, task])),
    [tasksQuery.data]
  )
  const openOccurrences = (occurrencesQuery.data ?? []).filter(
    (occurrence) => !terminalStatuses.has(occurrence.execution_status)
  )

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (title.trim() === '' || projectID === '') return
    setError('')
    try {
      await createTask.mutateAsync({
        project_id: projectID,
        title: title.trim(),
        priority: 0,
        schedule: {
          recurrence_type: 'none',
          timing_type: 'unscheduled',
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        },
      })
      setTitle('')
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '项目或任务已更新。你的标题已保留，请刷新后比较。'
          : '任务创建失败，请稍后重试。'
      )
    }
  }

  function requestCompletion() {
    const project = projectQuery.data
    if (!project) return
    if (openOccurrences.length > 0) {
      setCompletionDecisionOpen(true)
      return
    }
    void completeProject.mutateAsync({
      projectID,
      expectedRevision: { expected_project_revision: project.revision },
    })
  }

  function taskRevisions(taskID: string) {
    const task = tasksByID.get(taskID)
    if (!task) return null
    const occurrenceRevisions = Object.fromEntries(
      (occurrencesQuery.data ?? [])
        .filter((occurrence) => occurrence.task_id === taskID)
        .map((occurrence) => [occurrence.id, occurrence.revision])
    )
    return {
      expected_task_revision: task.revision,
      expected_schedule_revision: task.schedule_revision,
      expected_occurrence_revisions: occurrenceRevisions,
    }
  }

  async function completeAfterCancelling() {
    setError('')
    try {
      const taskIDs = new Set(openOccurrences.map((occurrence) => occurrence.task_id))
      for (const taskID of taskIDs) {
        const expectedRevisions = taskRevisions(taskID)
        if (!expectedRevisions) continue
        await cancelTask.mutateAsync({
          projectID,
          taskID,
          expectedRevisions,
        })
      }
      await completeProject.mutateAsync({
        projectID,
        expectedRevision: { expected_project_revision: project.revision },
      })
      setCompletionDecisionOpen(false)
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '任务或项目已更新，请刷新并重新确认未完成实例。'
          : '取消任务后完成项目失败，请稍后重试。'
      )
    }
  }

  async function completeAfterMoving() {
    if (targetProjectID === '') return
    setError('')
    try {
      const taskIDs = new Set(openOccurrences.map((occurrence) => occurrence.task_id))
      for (const taskID of taskIDs) {
        const task = tasksByID.get(taskID)
        if (!task) continue
        await updateTask.mutateAsync({
          projectID,
          taskID,
          input: {
            project_id: targetProjectID,
            expected_task_revision: task.revision,
            expected_schedule_revision: task.schedule_revision,
          },
        })
      }
      await completeProject.mutateAsync({
        projectID,
        expectedRevision: { expected_project_revision: project.revision },
      })
      setCompletionDecisionOpen(false)
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '任务或项目已更新，你选择的目标项目已保留。'
          : '迁移任务后完成项目失败，请稍后重试。'
      )
    }
  }

  if (projectQuery.isLoading) return <p className="domain-empty">正在加载项目…</p>
  if (projectQuery.isError || !projectQuery.data)
    return <p className="domain-empty">项目暂时不可用。</p>

  const project = projectQuery.data
  return (
    <section className="domain-page" aria-labelledby="project-detail-heading">
      <header className="domain-page-heading">
        <div>
          <Link className="domain-back-link" to="/projects">
            ← 返回项目
          </Link>
          <h2 id="project-detail-heading">{project.name}</h2>
          <p>
            {project.kind === 'learning' ? '学习项目' : '标准项目'} ·{' '}
            {project.horizon === 'short' ? '短期' : '长期'}
          </p>
        </div>
        <div className="domain-heading-actions">
          {project.kind === 'learning' ? (
            <Link
              className="domain-secondary-button"
              to={`/projects/${encodeURIComponent(project.id)}/roadmap`}
            >
              打开学习 Roadmap
            </Link>
          ) : null}
          <button type="button" onClick={requestCompletion}>
            完成项目
          </button>
        </div>
      </header>

      <form className="domain-inline-create" onSubmit={handleCreate}>
        <label>
          <span>新任务</span>
          <input
            aria-label="任务标题"
            value={title}
            placeholder="写下一个明确的行动"
            onChange={(event) => setTitle(event.target.value)}
          />
        </label>
        <button
          type="submit"
          className="domain-primary-button"
          disabled={title.trim() === '' || createTask.isPending}
        >
          添加任务
        </button>
      </form>
      {error !== '' ? <div className="domain-alert">{error}</div> : null}

      <div className="project-detail-task-list" role="list" aria-label="项目任务">
        {(tasksQuery.data ?? []).map((task) => {
          const occurrences = (occurrencesQuery.data ?? []).filter(
            (occurrence) => occurrence.task_id === task.id
          )
          return (
            <article className="project-detail-task" role="listitem" key={task.id}>
              <div>
                <h3>{task.title}</h3>
                <span className="domain-definition-status">
                  定义：{lifecycleLabel(task.lifecycle_status)}
                </span>
              </div>
              <div className="project-detail-occurrences">
                {occurrences.map((occurrence) => (
                  <span
                    className={`domain-execution-status execution-${occurrence.execution_status}`}
                    key={occurrence.id}
                  >
                    执行：{executionLabel(occurrence.execution_status)}
                  </span>
                ))}
              </div>
            </article>
          )
        })}
        {tasksByID.size === 0 ? <p className="domain-empty">暂无任务。</p> : null}
      </div>

      {completionDecisionOpen ? (
        <div
          className="domain-decision-dialog"
          role="dialog"
          aria-modal="true"
          aria-label="处理未完成执行实例"
        >
          <div>
            <h3>还有 {openOccurrences.length} 个未完成执行实例</h3>
            <p>完成项目之前，请明确取消它们，或先迁移到其他项目。</p>
            {completionDecision === 'choose' ? (
              <div className="domain-form-actions">
                <button
                  type="button"
                  onClick={() => setCompletionDecision('move')}
                >
                  迁移到其他项目
                </button>
                <button
                  type="button"
                  className="domain-primary-button"
                  disabled={cancelTask.isPending || completeProject.isPending}
                  onClick={() => void completeAfterCancelling()}
                >
                  取消未完成实例并完成
                </button>
              </div>
            ) : (
              <div className="project-completion-move">
                <label>
                  <span>目标项目</span>
                  <select
                    aria-label="目标项目"
                    value={targetProjectID}
                    onChange={(event) => setTargetProjectID(event.target.value)}
                  >
                    <option value="">请选择</option>
                    {(projectsQuery.data ?? [])
                      .filter(
                        (candidate) =>
                          candidate.id !== projectID &&
                          candidate.status !== 'completed' &&
                          candidate.status !== 'archived'
                      )
                      .map((candidate) => (
                        <option value={candidate.id} key={candidate.id}>
                          {candidate.name}
                        </option>
                      ))}
                  </select>
                </label>
                <div className="domain-form-actions">
                  <button type="button" onClick={() => setCompletionDecision('choose')}>
                    返回
                  </button>
                  <button
                    type="button"
                    className="domain-primary-button"
                    disabled={
                      targetProjectID === '' ||
                      updateTask.isPending ||
                      completeProject.isPending
                    }
                    onClick={() => void completeAfterMoving()}
                  >
                    迁移任务并完成
                  </button>
                </div>
              </div>
            )}
            <button
              type="button"
              className="domain-text-button"
              onClick={() => {
                setCompletionDecisionOpen(false)
                setCompletionDecision('choose')
                setTargetProjectID('')
              }}
            >
              暂不处理
            </button>
          </div>
        </div>
      ) : null}
    </section>
  )
}

function lifecycleLabel(status: string) {
  return (
    {
      draft: '草稿',
      active: '进行中',
      paused: '已暂停',
      completed: '已完成',
      cancelled: '已取消',
      archived: '已归档',
    }[status] ?? status
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
