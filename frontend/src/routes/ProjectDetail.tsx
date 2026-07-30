import {
  Archive,
  ArrowRight,
  CalendarDays,
  FileText,
  History,
  Map as MapIcon,
  MoreHorizontal,
  Pencil,
  Play,
  Plus,
  Trash2,
  WandSparkles,
} from 'lucide-react'
import { useQueries } from '@tanstack/react-query'
import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'

import { getNote } from '../api/notes'
import type { RoadmapV2 } from '../api/roadmapV2'
import type { OccurrenceV2, TaskV2 } from '../api/taskDomain'
import { TaskDomainRevisionConflictError } from '../api/taskDomain'
import {
  ExecutionStatusLabel,
  TaskDefinitionInspector,
  TaskLifecycleStatusLabel,
  formatOccurrenceSchedule,
} from '../components/taskDomain/TaskDomainWorkspace'
import { useGenerateRoadmapMutation, useRoadmapV2 } from '../hooks/useRoadmapV2'
import {
  useActivateProjectMutation,
  useArchiveProjectMutation,
  useArchiveTaskMutation,
  useCancelTaskMutation,
  useCompleteProjectMutation,
  useCreateTaskMutation,
  useDeleteProjectMutation,
  useOccurrences,
  usePauseTaskMutation,
  useProject,
  useProjects,
  usePublishTaskMutation,
  useRestoreTaskMutation,
  useResumeTaskMutation,
  useTaskDefinitions,
  useUpdateProjectMutation,
  useUpdateTaskDefinitionMutation,
} from '../hooks/useTaskDomain'

const terminalStatuses = new Set(['done', 'skipped', 'cancelled'])

type ProjectSection =
  | 'overview'
  | 'tasks'
  | 'schedule'
  | 'notes'
  | 'roadmap'
  | 'history'

const projectSections: Array<{ id: ProjectSection; label: string }> = [
  { id: 'overview', label: '概览' },
  { id: 'tasks', label: '任务' },
  { id: 'schedule', label: '日程' },
  { id: 'notes', label: '笔记' },
  { id: 'roadmap', label: '学习路线' },
  { id: 'history', label: '历史' },
]

export default function ProjectDetail() {
  const { projectID = '' } = useParams()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const requestedSection = searchParams.get('section') as ProjectSection | null
  const selectedTaskID = searchParams.get('task_id') ?? ''

  const projectQuery = useProject(projectID)
  const learningProject = projectQuery.data?.kind === 'learning'
  const roadmapQuery = useRoadmapV2(projectID, learningProject)
  const generateRoadmap = useGenerateRoadmapMutation(projectID)
  const projectsQuery = useProjects()
  const tasksQuery = useTaskDefinitions({ project_id: projectID })
  const occurrencesQuery = useOccurrences({ project_id: projectID })
  const createTask = useCreateTaskMutation()
  const activateProject = useActivateProjectMutation()
  const completeProject = useCompleteProjectMutation()
  const updateProject = useUpdateProjectMutation()
  const archiveProject = useArchiveProjectMutation()
  const deleteProject = useDeleteProjectMutation()
  const cancelTask = useCancelTaskMutation()
  const updateTask = useUpdateTaskDefinitionMutation()
  const publishTask = usePublishTaskMutation()
  const pauseTask = usePauseTaskMutation()
  const resumeTask = useResumeTaskMutation()
  const restoreTask = useRestoreTaskMutation()
  const archiveTask = useArchiveTaskMutation()

  const [title, setTitle] = useState('')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const [completionDecisionOpen, setCompletionDecisionOpen] = useState(false)
  const [completionDecision, setCompletionDecision] = useState<
    'choose' | 'move'
  >('choose')
  const [targetProjectID, setTargetProjectID] = useState('')
  const [roadmapDialogOpen, setRoadmapDialogOpen] = useState(false)
  const [roadmapPrompt, setRoadmapPrompt] = useState('')
  const [roadmapError, setRoadmapError] = useState('')
  const [projectActionsOpen, setProjectActionsOpen] = useState(false)
  const [projectActionDialog, setProjectActionDialog] = useState<
    'edit' | 'archive' | 'delete' | null
  >(null)
  const [projectName, setProjectName] = useState('')
  const [projectHorizon, setProjectHorizon] = useState<'short' | 'long'>(
    'short'
  )
  const projectActionsRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!projectActionsOpen) return
    function closeActions(event: PointerEvent) {
      if (
        projectActionsRef.current &&
        !projectActionsRef.current.contains(event.target as Node)
      ) {
        setProjectActionsOpen(false)
      }
    }
    function closeActionsOnEscape(event: KeyboardEvent) {
      if (event.key === 'Escape') setProjectActionsOpen(false)
    }
    window.addEventListener('pointerdown', closeActions)
    window.addEventListener('keydown', closeActionsOnEscape)
    return () => {
      window.removeEventListener('pointerdown', closeActions)
      window.removeEventListener('keydown', closeActionsOnEscape)
    }
  }, [projectActionsOpen])

  const visibleSections = useMemo(
    () =>
      projectSections.filter(
        (section) => section.id !== 'roadmap' || learningProject
      ),
    [learningProject]
  )
  const activeSection = visibleSections.some(
    (section) => section.id === requestedSection
  )
    ? requestedSection!
    : 'overview'

  const tasksByID = useMemo(
    () => new Map((tasksQuery.data ?? []).map((task) => [task.id, task])),
    [tasksQuery.data]
  )
  const linkedNoteIDs = useMemo(
    () => [
      ...new Set(
        (tasksQuery.data ?? [])
          .map((task) => task.task_note_id)
          .filter((noteID): noteID is string => Boolean(noteID))
      ),
    ],
    [tasksQuery.data]
  )
  const linkedNoteQueries = useQueries({
    queries: linkedNoteIDs.map((noteID) => ({
      queryKey: ['notes', noteID],
      queryFn: () => getNote(noteID),
    })),
  })
  const occurrencesByTask = useMemo(() => {
    const byTask = new Map<string, OccurrenceV2[]>()
    for (const occurrence of occurrencesQuery.data ?? []) {
      const current = byTask.get(occurrence.task_id)
      if (current) current.push(occurrence)
      else byTask.set(occurrence.task_id, [occurrence])
    }
    return byTask
  }, [occurrencesQuery.data])
  const openOccurrences = (occurrencesQuery.data ?? []).filter(
    (occurrence) => !terminalStatuses.has(occurrence.execution_status)
  )
  const selectedTask = tasksByID.get(selectedTaskID)
  const selectedTaskOccurrences = selectedTask
    ? (occurrencesByTask.get(selectedTask.id) ?? [])
    : []
  const actionableTasks = (tasksQuery.data ?? []).filter(
    (task) =>
      task.lifecycle_status !== 'cancelled' &&
      task.lifecycle_status !== 'archived'
  )
  const completedTasks = actionableTasks.filter(
    (task) => task.lifecycle_status === 'completed'
  )
  const progress = actionableTasks.length
    ? Math.round((completedTasks.length / actionableTasks.length) * 100)
    : 0
  const commandBusy = [
    updateTask,
    publishTask,
    pauseTask,
    resumeTask,
    cancelTask,
    restoreTask,
    archiveTask,
  ].some((mutation) => mutation.isPending)

  function updateSearchParams(update: Record<string, string | null>) {
    const next = new URLSearchParams(searchParams)
    Object.entries(update).forEach(([key, value]) => {
      if (!value) next.delete(key)
      else next.set(key, value)
    })
    setSearchParams(next)
  }

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
      setCreating(false)
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '项目或任务已更新。你的标题已保留，请刷新后比较。'
          : '任务创建失败，请稍后重试。'
      )
    }
  }

  async function handleGenerateRoadmap(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setRoadmapError('')
    try {
      await generateRoadmap.mutateAsync({
        prompt: roadmapPrompt.trim(),
      })
      setRoadmapDialogOpen(false)
      setRoadmapPrompt('')
      void navigate(`/projects/${encodeURIComponent(projectID)}/roadmap`)
    } catch (caught) {
      setRoadmapError(
        caught instanceof Error
          ? caught.message
          : '学习路线生成失败，请稍后重试。'
      )
    }
  }

  function openProjectAction(action: 'edit' | 'archive' | 'delete') {
    const project = projectQuery.data
    if (!project) return
    setProjectActionsOpen(false)
    setProjectActionDialog(action)
    setError('')
    if (action === 'edit') {
      setProjectName(project.name)
      setProjectHorizon(project.horizon)
    }
  }

  async function handleUpdateProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const project = projectQuery.data
    if (!project || projectName.trim() === '') return
    setError('')
    try {
      await updateProject.mutateAsync({
        projectID,
        input: {
          name: projectName.trim(),
          horizon: projectHorizon,
          expected_project_revision: project.revision,
        },
      })
      setProjectActionDialog(null)
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '项目已在其他窗口更新，请刷新后重新编辑。'
          : '项目更新失败，请稍后重试。'
      )
    }
  }

  async function confirmProjectAction() {
    const project = projectQuery.data
    if (
      !project ||
      (projectActionDialog !== 'archive' && projectActionDialog !== 'delete')
    )
      return
    setError('')
    try {
      const variables = {
        projectID,
        expectedRevision: {
          expected_project_revision: project.revision,
        },
      }
      if (projectActionDialog === 'archive') {
        await archiveProject.mutateAsync(variables)
      } else {
        await deleteProject.mutateAsync(variables)
      }
      setProjectActionDialog(null)
      void navigate('/projects')
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '项目已在其他窗口更新，请刷新后再执行。'
          : projectActionDialog === 'archive'
            ? '项目归档失败，请稍后重试。'
            : '项目删除失败，请稍后重试。'
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

  async function startProject() {
    const project = projectQuery.data
    if (!project) return
    setError('')
    try {
      await activateProject.mutateAsync({
        projectID,
        expectedRevision: {
          expected_project_revision: project.revision,
        },
      })
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '项目已在其他窗口更新，请刷新后再开始项目。'
          : '开始项目失败，请稍后重试。'
      )
    }
  }

  function taskRevisions(taskID: string) {
    const task = tasksByID.get(taskID)
    if (!task) return null
    return {
      expected_task_revision: task.revision,
      expected_schedule_revision: task.schedule_revision,
      expected_occurrence_revisions: Object.fromEntries(
        (occurrencesByTask.get(taskID) ?? []).map((occurrence) => [
          occurrence.id,
          occurrence.revision,
        ])
      ),
    }
  }

  function taskCommandVariables(task: TaskV2) {
    return {
      projectID,
      taskID: task.id,
      expectedRevisions: taskRevisions(task.id)!,
    }
  }

  async function handleTaskCommand(command: () => Promise<unknown>) {
    setError('')
    try {
      await command()
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '任务已在其他窗口更新，请刷新后重试。'
          : '任务操作失败，请稍后重试。'
      )
    }
  }

  async function completeAfterCancelling() {
    const project = projectQuery.data
    if (!project) return
    setError('')
    try {
      const taskIDs = new Set(
        openOccurrences.map((occurrence) => occurrence.task_id)
      )
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
    const project = projectQuery.data
    if (!project || targetProjectID === '') return
    setError('')
    try {
      const taskIDs = new Set(
        openOccurrences.map((occurrence) => occurrence.task_id)
      )
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

  if (projectQuery.isLoading)
    return <div className="td-route-loading">正在加载项目…</div>
  if (projectQuery.isError || !projectQuery.data)
    return <div className="td-route-error">项目暂时不可用，请刷新后重试。</div>

  const project = projectQuery.data

  return (
    <section
      className="td-page td-project-page td-project-detail-v3"
      aria-labelledby="project-detail-heading"
    >
      <header className="td-page-header">
        <div className="td-project-identity">
          <Link className="td-back-link" to="/projects">
            ← 返回项目
          </Link>
          <div className="td-title-line">
            <h1 id="project-detail-heading">{project.name}</h1>
            <span>
              {project.kind === 'learning' ? '学习项目' : '标准项目'} ·{' '}
              {project.horizon === 'short' ? '短期' : '长期'}
            </span>
          </div>
          <p>在一个目标下查看任务定义、每次执行和近期日程。</p>
          <div
            className="td-project-progress"
            aria-label={`项目进度 ${completedTasks.length}/${actionableTasks.length}`}
          >
            <span>
              <i style={{ width: `${progress}%` }} />
            </span>
            <small>
              已完成 {completedTasks.length} / {actionableTasks.length}
            </small>
          </div>
        </div>
        <div className="td-page-actions">
          {project.status === 'planning' ? (
            <button
              type="button"
              className="td-secondary-action td-start-project-action"
              disabled={activateProject.isPending}
              onClick={() => void startProject()}
            >
              <Play fill="currentColor" aria-hidden="true" />
              {activateProject.isPending ? '正在开始…' : '开始项目'}
            </button>
          ) : null}
          {project.kind === 'learning' ? (
            roadmapQuery.data ? (
              <Link
                className="td-roadmap-action"
                to={`/projects/${encodeURIComponent(project.id)}/roadmap`}
              >
                <MapIcon aria-hidden="true" />
                打开学习 Roadmap
              </Link>
            ) : (
              <button
                type="button"
                className="td-roadmap-action"
                disabled={roadmapQuery.isLoading}
                onClick={() => setRoadmapDialogOpen(true)}
              >
                <WandSparkles aria-hidden="true" />
                生成学习 Roadmap
              </button>
            )
          ) : null}
          {project.status === 'active' || project.status === 'paused' ? (
            <button
              type="button"
              className="td-secondary-action"
              onClick={requestCompletion}
            >
              完成项目
            </button>
          ) : null}
          <div className="td-project-actions" ref={projectActionsRef}>
            <button
              type="button"
              className="td-secondary-action is-icon"
              aria-label="项目操作"
              aria-haspopup="menu"
              aria-expanded={projectActionsOpen}
              aria-controls="project-actions-menu"
              onClick={() => setProjectActionsOpen((current) => !current)}
            >
              <MoreHorizontal aria-hidden="true" />
            </button>
            {projectActionsOpen ? (
              <div
                className="td-project-actions-menu"
                id="project-actions-menu"
                role="menu"
                aria-label="项目操作菜单"
              >
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => openProjectAction('edit')}
                >
                  <Pencil aria-hidden="true" />
                  <span>
                    <strong>编辑项目信息</strong>
                    <small>修改名称和项目周期</small>
                  </span>
                </button>
                {!project.system_role ? (
                  <>
                    <button
                      type="button"
                      role="menuitem"
                      onClick={() => openProjectAction('archive')}
                    >
                      <Archive aria-hidden="true" />
                      <span>
                        <strong>归档项目</strong>
                        <small>保留任务和执行历史</small>
                      </span>
                    </button>
                    <button
                      type="button"
                      role="menuitem"
                      className="is-danger"
                      onClick={() => openProjectAction('delete')}
                    >
                      <Trash2 aria-hidden="true" />
                      <span>
                        <strong>删除项目</strong>
                        <small>仅适用于不再需要的项目</small>
                      </span>
                    </button>
                  </>
                ) : null}
              </div>
            ) : null}
          </div>
          <button
            type="button"
            className="td-primary-action"
            aria-label="打开添加任务"
            onClick={() => setCreating((current) => !current)}
          >
            <Plus aria-hidden="true" />
            添加任务
          </button>
        </div>
      </header>

      {creating ? (
        <form className="td-inline-create" onSubmit={handleCreate}>
          <label>
            <span>新任务</span>
            <input
              aria-label="任务标题"
              value={title}
              placeholder="写下一个明确的行动"
              onChange={(event) => setTitle(event.target.value)}
              autoFocus
            />
          </label>
          <button
            type="button"
            onClick={() => {
              setCreating(false)
              setTitle('')
            }}
          >
            取消
          </button>
          <button
            type="submit"
            className="is-primary"
            disabled={title.trim() === '' || createTask.isPending}
          >
            添加任务
          </button>
        </form>
      ) : null}
      {error ? (
        <div className="td-inline-error" role="alert">
          {error}
        </div>
      ) : null}

      <div className="td-tabs" role="tablist" aria-label="项目详情分区">
        {visibleSections.map((section) => (
          <button
            type="button"
            role="tab"
            aria-selected={activeSection === section.id}
            className={activeSection === section.id ? 'is-active' : ''}
            key={section.id}
            onClick={() =>
              updateSearchParams({
                section: section.id === 'overview' ? null : section.id,
                task_id: null,
              })
            }
          >
            {section.label}{' '}
            {section.id === 'tasks' ? (
              <span>{tasksByID.size}</span>
            ) : section.id === 'schedule' ? (
              <span>{openOccurrences.length}</span>
            ) : null}
          </button>
        ))}
      </div>

      <div className={`td-workspace ${selectedTask ? 'has-inspector' : ''}`}>
        <div className="td-project-content">
          {activeSection === 'overview' || activeSection === 'tasks' ? (
            <div className="td-project-overview">
              <div className="td-project-main-column">
                {activeSection === 'overview' && project.kind === 'learning' ? (
                  <ProjectRoadmapOverview
                    projectID={project.id}
                    roadmap={roadmapQuery.data}
                    loading={roadmapQuery.isLoading}
                    onGenerate={() => setRoadmapDialogOpen(true)}
                  />
                ) : null}
                <div
                  className="td-project-task-list"
                  role="list"
                  aria-label="项目任务"
                >
                  <header className="td-section-heading">
                    <strong>
                      {activeSection === 'overview' ? '正在推进' : '任务定义'}
                    </strong>
                    <span>{tasksByID.size} 个任务定义</span>
                  </header>
                  {(tasksQuery.data ?? []).map((task) => {
                    const occurrences = occurrencesByTask.get(task.id) ?? []
                    return (
                      <article
                        className={`td-project-task ${
                          selectedTaskID === task.id ? 'is-selected' : ''
                        }`}
                        role="listitem"
                        tabIndex={0}
                        key={task.id}
                        onClick={() => updateSearchParams({ task_id: task.id })}
                        onKeyDown={(event) => {
                          if (event.key === 'Enter') {
                            updateSearchParams({ task_id: task.id })
                          }
                        }}
                      >
                        <div className="td-project-task-title">
                          <h3>{task.title}</h3>
                          <TaskLifecycleStatusLabel
                            status={task.lifecycle_status}
                          />
                        </div>
                        <div className="td-project-task-meta">
                          <span>
                            定义：{taskLifecycleText(task.lifecycle_status)}
                          </span>
                          <span>{priorityText(task.priority)}</span>
                          <span>{occurrences.length} 个实例</span>
                        </div>
                        <div className="td-project-occurrences">
                          {occurrences.slice(0, 3).map((occurrence) => (
                            <span key={occurrence.id}>
                              <time>
                                {formatOccurrenceSchedule(occurrence)}
                              </time>
                              <span className="td-visually-hidden">执行：</span>
                              <ExecutionStatusLabel
                                status={occurrence.execution_status}
                              />
                            </span>
                          ))}
                        </div>
                      </article>
                    )
                  })}
                  {tasksByID.size === 0 ? (
                    <div className="td-empty-state td-project-empty-state">
                      <Plus aria-hidden="true" />
                      <strong>项目还没有任务</strong>
                      <span>先添加一个清晰、可以执行的行动。</span>
                      <button type="button" onClick={() => setCreating(true)}>
                        添加第一个任务
                        <ArrowRight aria-hidden="true" />
                      </button>
                    </div>
                  ) : null}
                </div>
              </div>
              {activeSection === 'overview' ? (
                <ProjectAgenda occurrences={occurrencesQuery.data ?? []} />
              ) : null}
            </div>
          ) : activeSection === 'schedule' ? (
            <ProjectSchedule occurrences={occurrencesQuery.data ?? []} />
          ) : activeSection === 'roadmap' ? (
            <ProjectRoadmapOverview
              projectID={project.id}
              roadmap={roadmapQuery.data}
              loading={roadmapQuery.isLoading}
              expanded
              onGenerate={() => setRoadmapDialogOpen(true)}
            />
          ) : activeSection === 'notes' ? (
            <div className="td-project-notes">
              <header>
                <div>
                  <FileText aria-hidden="true" />
                  <div>
                    <strong>项目笔记</strong>
                    <span>来自当前项目任务的关联笔记</span>
                  </div>
                </div>
                <em>{linkedNoteIDs.length}</em>
              </header>
              {linkedNoteQueries.some((query) => query.isLoading) ? (
                <div className="td-project-notes-state">正在加载关联笔记…</div>
              ) : null}
              {linkedNoteIDs.length === 0 ? (
                <div className="td-project-notes-state">
                  <FileText aria-hidden="true" />
                  <strong>还没有关联笔记</strong>
                  <span>在笔记编辑器中选择本项目及其任务即可建立关联。</span>
                </div>
              ) : null}
              <div className="td-project-note-list">
                {linkedNoteQueries.map((query, index) =>
                  query.data ? (
                    <Link
                      key={query.data.id}
                      to={`/editor/${encodeURIComponent(query.data.id)}`}
                    >
                      <FileText aria-hidden="true" />
                      <span>
                        <strong>{query.data.title || '未命名笔记'}</strong>
                        <small>
                          {new Date(
                            query.data.updated_at * 1000
                          ).toLocaleDateString('zh-CN')}
                        </small>
                      </span>
                      <ArrowRight aria-hidden="true" />
                    </Link>
                  ) : linkedNoteQueries[index]?.isError ? (
                    <div
                      className="td-project-note-error"
                      key={linkedNoteIDs[index]}
                    >
                      一条关联笔记暂时无法加载
                    </div>
                  ) : null
                )}
              </div>
            </div>
          ) : (
            <ProjectHistory occurrences={occurrencesQuery.data ?? []} />
          )}
        </div>

        {selectedTask ? (
          <TaskDefinitionInspector
            task={selectedTask}
            project={project}
            occurrences={selectedTaskOccurrences}
            busy={commandBusy}
            onClose={() => updateSearchParams({ task_id: null })}
            onUpdate={(input) =>
              updateTask.mutateAsync({
                projectID,
                taskID: selectedTask.id,
                input: {
                  ...input,
                  expected_task_revision: selectedTask.revision,
                  expected_schedule_revision: selectedTask.schedule_revision,
                },
              })
            }
            onPublish={() =>
              handleTaskCommand(() =>
                publishTask.mutateAsync(taskCommandVariables(selectedTask))
              )
            }
            onPause={() =>
              handleTaskCommand(() =>
                pauseTask.mutateAsync(taskCommandVariables(selectedTask))
              )
            }
            onResume={() =>
              handleTaskCommand(() =>
                resumeTask.mutateAsync(taskCommandVariables(selectedTask))
              )
            }
            onCancel={() =>
              handleTaskCommand(() =>
                cancelTask.mutateAsync(taskCommandVariables(selectedTask))
              )
            }
            onRestore={() =>
              handleTaskCommand(() =>
                restoreTask.mutateAsync(taskCommandVariables(selectedTask))
              )
            }
            onArchive={() =>
              handleTaskCommand(() =>
                archiveTask.mutateAsync(taskCommandVariables(selectedTask))
              )
            }
          />
        ) : null}
      </div>

      {projectActionDialog === 'edit' ? (
        <div
          className="td-dialog-backdrop"
          role="dialog"
          aria-modal="true"
          aria-label="编辑项目信息"
        >
          <form
            className="td-decision-dialog td-project-edit-dialog"
            onSubmit={handleUpdateProject}
          >
            <header>
              <div>
                <span>PROJECT SETTINGS</span>
                <h2>编辑项目信息</h2>
              </div>
            </header>
            <div className="td-project-edit-fields">
              <label>
                <span>项目名称</span>
                <input
                  aria-label="项目名称"
                  value={projectName}
                  maxLength={120}
                  onChange={(event) => setProjectName(event.target.value)}
                  autoFocus
                />
              </label>
              <label>
                <span>项目周期</span>
                <select
                  aria-label="项目周期"
                  value={projectHorizon}
                  onChange={(event) =>
                    setProjectHorizon(event.target.value as 'short' | 'long')
                  }
                >
                  <option value="short">短期</option>
                  <option value="long">长期</option>
                </select>
              </label>
            </div>
            <div className="td-form-actions">
              <button
                type="button"
                onClick={() => setProjectActionDialog(null)}
              >
                取消
              </button>
              <button
                type="submit"
                className="is-primary"
                disabled={projectName.trim() === '' || updateProject.isPending}
              >
                {updateProject.isPending ? '正在保存…' : '保存修改'}
              </button>
            </div>
          </form>
        </div>
      ) : null}

      {projectActionDialog === 'archive' || projectActionDialog === 'delete' ? (
        <div
          className="td-dialog-backdrop"
          role="dialog"
          aria-modal="true"
          aria-label={
            projectActionDialog === 'archive' ? '归档项目' : '删除项目'
          }
        >
          <div className="td-decision-dialog">
            <header>
              <div>
                <span>
                  {projectActionDialog === 'archive'
                    ? 'ARCHIVE PROJECT'
                    : 'DELETE PROJECT'}
                </span>
                <h2>
                  {projectActionDialog === 'archive'
                    ? `归档“${project.name}”`
                    : `删除“${project.name}”`}
                </h2>
              </div>
            </header>
            <p>
              {projectActionDialog === 'archive'
                ? '项目会从活跃列表中移出，但任务、日程和执行历史都会保留。'
                : '项目及其关联数据将被永久删除，此操作无法撤销。'}
            </p>
            <div className="td-form-actions">
              <button
                type="button"
                onClick={() => setProjectActionDialog(null)}
              >
                取消
              </button>
              <button
                type="button"
                className={
                  projectActionDialog === 'delete' ? 'is-danger' : 'is-primary'
                }
                disabled={archiveProject.isPending || deleteProject.isPending}
                onClick={() => void confirmProjectAction()}
              >
                {projectActionDialog === 'archive'
                  ? archiveProject.isPending
                    ? '正在归档…'
                    : '确认归档'
                  : deleteProject.isPending
                    ? '正在删除…'
                    : '永久删除'}
              </button>
            </div>
          </div>
        </div>
      ) : null}

      {roadmapDialogOpen ? (
        <div
          className="td-dialog-backdrop"
          role="dialog"
          aria-modal="true"
          aria-label="生成学习 Roadmap"
        >
          <form
            className="td-decision-dialog td-roadmap-generation-dialog"
            onSubmit={handleGenerateRoadmap}
          >
            <header>
              <div>
                <span>学习路线生成</span>
                <h2>为“{project.name}”生成完整 Roadmap</h2>
              </div>
            </header>
            <p>
              系统会从目标诊断、基础知识到独立实践生成一条连续路径。你也可以补充希望重点覆盖的方向。
            </p>
            <label>
              <span>补充生成要求（可选）</span>
              <textarea
                aria-label="补充生成要求"
                value={roadmapPrompt}
                rows={5}
                maxLength={4000}
                placeholder="例如：更重视口语实战，每个阶段都要有可验证的产出。"
                onChange={(event) => setRoadmapPrompt(event.target.value)}
                autoFocus
              />
              <small>{roadmapPrompt.length} / 4000</small>
            </label>
            {roadmapError ? (
              <div className="td-inline-error" role="alert">
                {roadmapError}
              </div>
            ) : null}
            <div className="td-form-actions">
              <button
                type="button"
                onClick={() => {
                  setRoadmapDialogOpen(false)
                  setRoadmapError('')
                }}
              >
                取消
              </button>
              <button
                type="submit"
                className="is-primary"
                disabled={generateRoadmap.isPending}
              >
                <WandSparkles aria-hidden="true" />
                {generateRoadmap.isPending
                  ? '正在生成完整路径…'
                  : '生成学习 Roadmap'}
              </button>
            </div>
          </form>
        </div>
      ) : null}

      {completionDecisionOpen ? (
        <div
          className="td-dialog-backdrop"
          role="dialog"
          aria-modal="true"
          aria-label="处理未完成执行实例"
        >
          <div className="td-decision-dialog">
            <header>
              <div>
                <span>完成项目前</span>
                <h2>还有 {openOccurrences.length} 个未完成执行实例</h2>
              </div>
            </header>
            <p>请明确取消这些实例，或把对应任务迁移到其他项目。</p>
            {completionDecision === 'choose' ? (
              <div className="td-form-actions">
                <button
                  type="button"
                  onClick={() => setCompletionDecision('move')}
                >
                  迁移到其他项目
                </button>
                <button
                  type="button"
                  className="is-primary"
                  disabled={cancelTask.isPending || completeProject.isPending}
                  onClick={() => void completeAfterCancelling()}
                >
                  取消未完成实例并完成
                </button>
              </div>
            ) : (
              <div className="td-project-move">
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
                <div className="td-form-actions">
                  <button
                    type="button"
                    onClick={() => setCompletionDecision('choose')}
                  >
                    返回
                  </button>
                  <button
                    type="button"
                    className="is-primary"
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
              className="td-dialog-dismiss"
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

function ProjectRoadmapOverview({
  projectID,
  roadmap,
  loading,
  expanded = false,
  onGenerate,
}: {
  projectID: string
  roadmap: RoadmapV2 | null | undefined
  loading: boolean
  expanded?: boolean
  onGenerate: () => void
}) {
  const executionTotal =
    roadmap?.nodes.reduce((sum, node) => sum + node.progress.total, 0) ?? 0
  const executionDone =
    roadmap?.nodes.reduce((sum, node) => sum + node.progress.done, 0) ?? 0

  if (loading) {
    return (
      <section
        className={`td-roadmap-overview ${expanded ? 'is-expanded' : ''}`}
        aria-label="学习路线"
      >
        <div className="td-roadmap-overview-icon" aria-hidden="true">
          <MapIcon />
        </div>
        <div>
          <strong>正在读取学习路线…</strong>
          <p>稍候即可继续规划。</p>
        </div>
      </section>
    )
  }

  if (!roadmap) {
    return (
      <section
        className={`td-roadmap-overview is-empty ${
          expanded ? 'is-expanded' : ''
        }`}
        aria-labelledby="roadmap-overview-heading"
      >
        <div className="td-roadmap-overview-icon" aria-hidden="true">
          <WandSparkles />
        </div>
        <div className="td-roadmap-overview-copy">
          <span>学习路径</span>
          <h2 id="roadmap-overview-heading">从目标生成一条可执行的学习路线</h2>
          <p>生成阶段、主题和验收节点，再把具体任务放到每个节点中持续推进。</p>
          <div className="td-roadmap-overview-actions">
            <button type="button" onClick={onGenerate}>
              <WandSparkles aria-hidden="true" />
              生成学习 Roadmap
            </button>
            <Link to={`/projects/${encodeURIComponent(projectID)}/roadmap`}>
              也可以先创建空白路线
              <ArrowRight aria-hidden="true" />
            </Link>
          </div>
        </div>
      </section>
    )
  }

  return (
    <section
      className={`td-roadmap-overview ${expanded ? 'is-expanded' : ''}`}
      aria-labelledby="roadmap-overview-heading"
    >
      <div className="td-roadmap-overview-icon" aria-hidden="true">
        <MapIcon />
      </div>
      <div className="td-roadmap-overview-copy">
        <span>学习路径</span>
        <h2 id="roadmap-overview-heading">{roadmap.title}</h2>
        {roadmap.description ? <p>{roadmap.description}</p> : null}
        <div className="td-roadmap-overview-meta">
          <span>{roadmap.nodes.length} 个路线节点</span>
          <span>
            {executionTotal > 0
              ? `已完成 ${executionDone} / ${executionTotal} 次执行`
              : '尚未关联执行任务'}
          </span>
        </div>
      </div>
      <Link
        className="td-roadmap-open-link"
        to={`/projects/${encodeURIComponent(projectID)}/roadmap`}
      >
        打开学习 Roadmap
        <ArrowRight aria-hidden="true" />
      </Link>
    </section>
  )
}

function ProjectAgenda({ occurrences }: { occurrences: OccurrenceV2[] }) {
  const upcoming = occurrences
    .filter((occurrence) => !terminalStatuses.has(occurrence.execution_status))
    .slice(0, 5)
  return (
    <aside className="td-project-agenda">
      <header className="td-section-heading">
        <strong>近期日程</strong>
        <span>未来安排</span>
      </header>
      {upcoming.map((occurrence) => (
        <div className="td-agenda-row" key={occurrence.id}>
          <CalendarDays aria-hidden="true" />
          <div>
            <strong>{occurrence.title ?? '任务执行'}</strong>
            <span>{formatOccurrenceSchedule(occurrence)}</span>
          </div>
        </div>
      ))}
      {upcoming.length === 0 ? (
        <p className="td-agenda-empty">近期没有安排。</p>
      ) : null}
    </aside>
  )
}

function ProjectSchedule({ occurrences }: { occurrences: OccurrenceV2[] }) {
  return (
    <div className="td-section-surface">
      <header className="td-section-heading">
        <strong>项目日程</strong>
        <span>{occurrences.length} 个执行实例</span>
      </header>
      {occurrences.map((occurrence) => (
        <div className="td-schedule-row" key={occurrence.id}>
          <CalendarDays aria-hidden="true" />
          <strong>{occurrence.title ?? '任务执行'}</strong>
          <time>{formatOccurrenceSchedule(occurrence)}</time>
          <ExecutionStatusLabel status={occurrence.execution_status} />
        </div>
      ))}
    </div>
  )
}

function ProjectHistory({ occurrences }: { occurrences: OccurrenceV2[] }) {
  const history = occurrences.filter((occurrence) =>
    terminalStatuses.has(occurrence.execution_status)
  )
  return (
    <div className="td-section-surface">
      <header className="td-section-heading">
        <strong>完成历史</strong>
        <span>{history.length} 个实例</span>
      </header>
      {history.map((occurrence) => (
        <div className="td-schedule-row" key={occurrence.id}>
          <History aria-hidden="true" />
          <strong>{occurrence.title ?? '任务执行'}</strong>
          <time>{formatOccurrenceSchedule(occurrence)}</time>
          <ExecutionStatusLabel status={occurrence.execution_status} />
        </div>
      ))}
    </div>
  )
}

function taskLifecycleText(status: TaskV2['lifecycle_status']) {
  return {
    draft: '草稿',
    active: '进行中',
    paused: '已暂停',
    completed: '已完成',
    cancelled: '已取消',
    archived: '已归档',
  }[status]
}

function priorityText(priority: number) {
  if (priority >= 3) return '紧急'
  if (priority === 2) return '高优先级'
  if (priority === 1) return '中优先级'
  return '普通'
}
