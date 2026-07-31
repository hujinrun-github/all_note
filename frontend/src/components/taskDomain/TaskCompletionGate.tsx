import {
  BookOpen,
  Check,
  CircleCheckBig,
  ExternalLink,
  LockKeyhole,
  Pencil,
  Play,
  Plus,
  Trash2,
} from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'

import type {
  ExecutionStatus,
  OccurrenceV2,
  TaskCompletionRequirement,
  TaskCompletionRequirementKind,
  TaskV2,
} from '../../api/taskDomain'
import { useUpdateTaskDefinitionMutation } from '../../hooks/useTaskDomain'

export interface TaskCompletionGateProps {
  task: TaskV2
  occurrence?: OccurrenceV2
  status?: ExecutionStatus
  onComplete?: () => Promise<unknown> | void
  isCompleting?: boolean
  showCompleteAction?: boolean
}

export function taskCompletionProgress(
  task?: Pick<TaskV2, 'completion_requirements'>
) {
  const requirements = task?.completion_requirements ?? []
  const completed = requirements.reduce(
    (count, requirement) => count + Number(requirement.completed),
    0
  )
  return {
    total: requirements.length,
    completed,
    remaining: requirements.length - completed,
  }
}

export function TaskCompletionGate({
  task,
  occurrence,
  status,
  onComplete,
  isCompleting = false,
  showCompleteAction = false,
}: TaskCompletionGateProps) {
  const updateTask = useUpdateTaskDefinitionMutation()
  const headingID = useId()
  const revisionsRef = useRef({
    taskID: task.id,
    taskRevision: task.revision,
    scheduleRevision: task.schedule_revision,
  })
  const [requirements, setRequirements] = useState<TaskCompletionRequirement[]>(
    task.completion_requirements ?? []
  )
  const [editingRequirementID, setEditingRequirementID] = useState<string>()
  const [requirementKind, setRequirementKind] =
    useState<TaskCompletionRequirementKind>('article')
  const [requirementTitle, setRequirementTitle] = useState('')
  const [requirementURL, setRequirementURL] = useState('')
  const [requirementError, setRequirementError] = useState('')

  useEffect(() => {
    setRequirements(task.completion_requirements ?? [])
    setEditingRequirementID(undefined)
    setRequirementError('')
    revisionsRef.current = {
      taskID: task.id,
      taskRevision: task.revision,
      scheduleRevision: task.schedule_revision,
    }
  }, [
    task.completion_requirements,
    task.id,
    task.revision,
    task.schedule_revision,
  ])

  function resetRequirementEditor() {
    setEditingRequirementID(undefined)
    setRequirementKind('article')
    setRequirementTitle('')
    setRequirementURL('')
    setRequirementError('')
  }

  function beginRequirementEditor(requirement?: TaskCompletionRequirement) {
    setEditingRequirementID(requirement?.id ?? '')
    setRequirementKind(requirement?.kind ?? 'article')
    setRequirementTitle(requirement?.title ?? '')
    setRequirementURL(requirement?.url ?? '')
    setRequirementError('')
  }

  async function persistRequirements(next: TaskCompletionRequirement[]) {
    const previous = requirements
    setRequirements(next)
    setRequirementError('')
    try {
      const revisions = revisionsRef.current
      const updatedTask = await updateTask.mutateAsync({
        projectID: task.project_id,
        taskID: task.id,
        input: {
          completion_requirements: next,
          expected_task_revision: revisions.taskRevision,
          expected_schedule_revision: revisions.scheduleRevision,
        },
      })
      if (updatedTask) {
        revisionsRef.current = {
          taskID: updatedTask.id,
          taskRevision: updatedTask.revision,
          scheduleRevision: updatedTask.schedule_revision,
        }
        setRequirements(updatedTask.completion_requirements ?? next)
      }
      return true
    } catch (error) {
      setRequirements(previous)
      setRequirementError(
        error instanceof Error ? error.message : '必选项保存失败，请重试。'
      )
      return false
    }
  }

  async function submitRequirement() {
    const normalizedTitle = requirementTitle.trim()
    if (normalizedTitle === '') {
      setRequirementError('请输入必选项名称。')
      return
    }
    const normalizedURL = requirementURL.trim()
    if (normalizedURL !== '' && !isValidHTTPURL(normalizedURL)) {
      setRequirementError('请输入有效的 http(s) 链接。')
      return
    }
    const nextRequirement: TaskCompletionRequirement = {
      id: editingRequirementID || createRequirementID(),
      kind: requirementKind,
      title: normalizedTitle,
      url: normalizedURL || undefined,
      completed:
        requirements.find(({ id }) => id === editingRequirementID)?.completed ??
        false,
    }
    const next = editingRequirementID
      ? requirements.map((requirement) =>
          requirement.id === editingRequirementID
            ? nextRequirement
            : requirement
        )
      : [...requirements, nextRequirement]
    if (await persistRequirements(next)) resetRequirementEditor()
  }

  async function toggleRequirement(requirementID: string) {
    await persistRequirements(
      requirements.map((requirement) =>
        requirement.id === requirementID
          ? { ...requirement, completed: !requirement.completed }
          : requirement
      )
    )
  }

  async function removeRequirement(requirementID: string) {
    const persisted = await persistRequirements(
      requirements.filter((requirement) => requirement.id !== requirementID)
    )
    if (persisted && editingRequirementID === requirementID) {
      resetRequirementEditor()
    }
  }

  const completedRequirementCount = requirements.reduce(
    (count, requirement) => count + Number(requirement.completed),
    0
  )
  const remainingRequirementCount =
    requirements.length - completedRequirementCount
  const executionStatus = status ?? occurrence?.execution_status

  return (
    <section className="task-completion-gate" aria-labelledby={headingID}>
      <header>
        <div>
          <CircleCheckBig aria-hidden="true" />
          <div>
            <strong id={headingID}>完成门槛</strong>
            <span>全部满足后才能完成任务</span>
          </div>
        </div>
        <b>
          {completedRequirementCount} / {requirements.length}
        </b>
      </header>

      {requirements.length > 0 ? (
        <div
          className="task-requirement-progress"
          aria-label={
            '已完成 ' +
            completedRequirementCount +
            ' 项，共 ' +
            requirements.length +
            ' 项'
          }
        >
          {requirements.map((requirement) => (
            <span
              className={requirement.completed ? 'is-complete' : ''}
              key={requirement.id}
            />
          ))}
        </div>
      ) : null}

      {requirements.length > 0 ? (
        <ul className="task-requirement-list">
          {requirements.map((requirement) => {
            const RequirementIcon =
              requirement.kind === 'article'
                ? BookOpen
                : requirement.kind === 'video'
                  ? Play
                  : Check
            const typeLabel =
              requirement.kind === 'article'
                ? '文章'
                : requirement.kind === 'video'
                  ? '视频'
                  : '检查项'
            return (
              <li
                className={requirement.completed ? 'is-complete' : ''}
                key={requirement.id}
              >
                <button
                  className="task-requirement-check"
                  type="button"
                  aria-label={
                    (requirement.completed ? '取消完成' : '标记完成') +
                    '：' +
                    requirement.title
                  }
                  aria-pressed={requirement.completed}
                  disabled={updateTask.isPending}
                  onClick={() => void toggleRequirement(requirement.id)}
                >
                  {requirement.completed ? <Check aria-hidden="true" /> : null}
                </button>
                <div className="task-requirement-copy">
                  <span>
                    <RequirementIcon aria-hidden="true" />
                    {typeLabel}
                  </span>
                  {requirement.url ? (
                    <a href={requirement.url} target="_blank" rel="noreferrer">
                      {requirement.title}
                      <ExternalLink aria-hidden="true" />
                    </a>
                  ) : (
                    <strong>{requirement.title}</strong>
                  )}
                </div>
                <div className="task-requirement-row-actions">
                  <button
                    type="button"
                    aria-label={'编辑：' + requirement.title}
                    disabled={updateTask.isPending}
                    onClick={() => beginRequirementEditor(requirement)}
                  >
                    <Pencil aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    aria-label={'删除：' + requirement.title}
                    disabled={updateTask.isPending}
                    onClick={() => void removeRequirement(requirement.id)}
                  >
                    <Trash2 aria-hidden="true" />
                  </button>
                </div>
              </li>
            )
          })}
        </ul>
      ) : (
        <div className="task-requirement-empty">
          <CircleCheckBig aria-hidden="true" />
          <p>还没有必选项；添加阅读、视频或验收检查，让完成标准更明确。</p>
        </div>
      )}

      {editingRequirementID !== undefined ? (
        <div className="task-requirement-editor">
          <div>
            <select
              aria-label="必选项类型"
              value={requirementKind}
              onChange={(event) =>
                setRequirementKind(
                  event.target.value as TaskCompletionRequirementKind
                )
              }
            >
              <option value="article">文章</option>
              <option value="video">视频</option>
              <option value="check">检查项</option>
            </select>
            <input
              aria-label="必选项名称"
              autoFocus
              value={requirementTitle}
              placeholder="例如：读完《设计心理学》"
              onChange={(event) => setRequirementTitle(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault()
                  void submitRequirement()
                }
                if (event.key === 'Escape') resetRequirementEditor()
              }}
            />
          </div>
          <input
            aria-label="必选项链接"
            value={requirementURL}
            placeholder="https://…（可选）"
            onChange={(event) => setRequirementURL(event.target.value)}
          />
          <div>
            <button type="button" onClick={resetRequirementEditor}>
              取消
            </button>
            <button
              className="is-primary"
              type="button"
              disabled={updateTask.isPending}
              onClick={() => void submitRequirement()}
            >
              {updateTask.isPending ? '保存中…' : '保存必选项'}
            </button>
          </div>
        </div>
      ) : (
        <button
          className="task-add-requirement"
          type="button"
          disabled={requirements.length >= 20}
          onClick={() => beginRequirementEditor()}
        >
          <Plus aria-hidden="true" />
          {requirements.length >= 20 ? '最多添加 20 项' : '添加必选项'}
        </button>
      )}

      {requirementError ? (
        <p className="task-requirement-error" role="alert">
          {requirementError}
        </p>
      ) : null}

      {showCompleteAction ? (
        <div
          className={
            'task-completion-lock ' +
            (remainingRequirementCount > 0 ? 'is-locked' : '')
          }
        >
          <button
            type="button"
            disabled={
              remainingRequirementCount > 0 ||
              !occurrence ||
              executionStatus === 'done' ||
              !onComplete ||
              isCompleting ||
              updateTask.isPending
            }
            onClick={() => void onComplete?.()}
          >
            {remainingRequirementCount > 0 ? (
              <LockKeyhole aria-hidden="true" />
            ) : (
              <CircleCheckBig aria-hidden="true" />
            )}
            {executionStatus === 'done'
              ? '任务已完成'
              : isCompleting
                ? '正在完成…'
                : remainingRequirementCount > 0
                  ? '还需完成 ' + remainingRequirementCount + ' 项'
                  : '完成任务'}
          </button>
          <span>
            {completionGateHint(requirements.length, remainingRequirementCount)}
          </span>
        </div>
      ) : (
        <p className="task-completion-summary">
          {completionGateHint(requirements.length, remainingRequirementCount)}
        </p>
      )}
    </section>
  )
}

function completionGateHint(total: number, remaining: number) {
  if (total === 0) return '未设置必选项，可直接完成任务。'
  if (remaining > 0) return '所有必选项完成后，才能完成任务。'
  return '完成门槛已满足，可以完成任务。'
}

function isValidHTTPURL(value: string) {
  try {
    const parsed = new URL(value)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

function createRequirementID() {
  return (
    'requirement-' +
    (typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : Date.now() + '-' + Math.random().toString(16).slice(2))
  )
}
