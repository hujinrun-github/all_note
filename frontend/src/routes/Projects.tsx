import { type FormEvent, useState } from 'react'
import {
  ArrowUpRight,
  CalendarRange,
  ChevronDown,
  FolderKanban,
  GraduationCap,
  Inbox,
  Play,
  Plus,
  RotateCcw,
  Trash2,
  UserRound,
  X,
  type LucideIcon,
} from 'lucide-react'
import { Link } from 'react-router-dom'

import {
  type ProjectHorizon,
  type ProjectKind,
  type ProjectV2,
  TaskDomainRevisionConflictError,
} from '../api/taskDomain'
import {
  useActivateProjectMutation,
  useCreateProjectMutation,
  useDeleteProjectMutation,
  useProjects,
} from '../hooks/useTaskDomain'

const kindLabels: Record<ProjectKind, string> = {
  standard: '标准项目',
  learning: '学习项目',
}

const horizonLabels: Record<ProjectHorizon, string> = {
  short: '短期',
  long: '长期',
}

const statusLabels: Record<string, string> = {
  planning: '规划中',
  active: '进行中',
  paused: '已暂停',
  completed: '已完成',
  archived: '已归档',
}

function iconForProject(project: ProjectV2): LucideIcon {
  if (project.system_role === 'inbox') return Inbox
  if (project.system_role === 'personal') return UserRound
  return project.kind === 'learning' ? GraduationCap : FolderKanban
}

function systemRoleLabel(project: ProjectV2) {
  if (project.system_role === 'inbox') return '系统收件箱'
  if (project.system_role === 'personal') return '系统个人项目'
  return '自定义项目'
}

export default function Projects() {
  const [kind, setKind] = useState<ProjectKind | ''>('')
  const [horizon, setHorizon] = useState<ProjectHorizon | ''>('')
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [newKind, setNewKind] = useState<ProjectKind>('standard')
  const [newHorizon, setNewHorizon] = useState<ProjectHorizon>('short')
  const [error, setError] = useState('')
  const projectsQuery = useProjects({
    kind: kind || undefined,
    horizon: horizon || undefined,
  })
  const createProject = useCreateProjectMutation()
  const activateProject = useActivateProjectMutation()
  const deleteProject = useDeleteProjectMutation()
  const projects = projectsQuery.data ?? []
  const hasFilters = kind !== '' || horizon !== ''

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (name.trim() === '') return
    setError('')
    try {
      await createProject.mutateAsync({
        name: name.trim(),
        kind: newKind,
        horizon: newHorizon,
        status: 'planning',
      })
      setName('')
      setCreating(false)
    } catch (caught) {
      setError(
        caught instanceof TaskDomainRevisionConflictError
          ? '项目已在其他窗口更新。你的输入已保留，请刷新后比较。'
          : '项目创建失败，请稍后重试。'
      )
    }
  }

  function clearFilters() {
    setKind('')
    setHorizon('')
  }

  async function handleActivate(project: ProjectV2) {
    setError('')
    try {
      await activateProject.mutateAsync({
        projectID: project.id,
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

  return (
    <section
      className="domain-page projects-page"
      aria-labelledby="projects-heading"
    >
      <header className="projects-page-heading">
        <div>
          <h2 id="projects-heading">项目</h2>
          <p>项目承载目标，任务和每次执行都从这里获得清晰归属。</p>
        </div>
        <button
          type="button"
          className="projects-new-button"
          aria-expanded={creating}
          aria-controls="project-create-panel"
          onClick={() => setCreating(true)}
        >
          <Plus size={16} strokeWidth={1.8} aria-hidden="true" />
          新建项目
        </button>
      </header>

      <section className="projects-commandbar" aria-label="项目筛选">
        <div className="projects-count">
          <span aria-hidden="true">
            <FolderKanban size={17} strokeWidth={1.7} />
          </span>
          <div>
            <strong>{projects.length} 个项目</strong>
            <small>{hasFilters ? '符合当前筛选' : '全部可用项目'}</small>
          </div>
        </div>

        <div className="projects-filters">
          <label className="projects-filter">
            <span>项目类型</span>
            <span className="projects-select">
              <select
                aria-label="项目类型"
                value={kind}
                onChange={(event) =>
                  setKind(event.target.value as ProjectKind | '')
                }
              >
                <option value="">全部类型</option>
                <option value="standard">标准项目</option>
                <option value="learning">学习项目</option>
              </select>
              <ChevronDown size={13} aria-hidden="true" />
            </span>
          </label>
          <label className="projects-filter">
            <span>项目周期</span>
            <span className="projects-select">
              <select
                aria-label="项目周期"
                value={horizon}
                onChange={(event) =>
                  setHorizon(event.target.value as ProjectHorizon | '')
                }
              >
                <option value="">全部周期</option>
                <option value="short">短期</option>
                <option value="long">长期</option>
              </select>
              <ChevronDown size={13} aria-hidden="true" />
            </span>
          </label>
          {hasFilters ? (
            <button
              type="button"
              className="projects-reset-button"
              onClick={clearFilters}
            >
              <RotateCcw size={13} aria-hidden="true" />
              重置
            </button>
          ) : null}
        </div>
      </section>

      {creating ? (
        <form
          id="project-create-panel"
          className="projects-create-panel"
          onSubmit={handleCreate}
        >
          <header>
            <div>
              <h3>新建项目</h3>
              <p>先定义项目名称、类型和计划周期。</p>
            </div>
            <button
              type="button"
              className="projects-icon-button"
              aria-label="关闭新建项目"
              onClick={() => setCreating(false)}
            >
              <X size={16} aria-hidden="true" />
            </button>
          </header>
          <div className="projects-create-fields">
            <label className="projects-create-name">
              <span>项目名称</span>
              <input
                aria-label="项目名称"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="例如：年度写作计划"
                autoFocus
              />
            </label>
            <label>
              <span>类型</span>
              <span className="projects-select">
                <select
                  aria-label="新项目类型"
                  value={newKind}
                  onChange={(event) =>
                    setNewKind(event.target.value as ProjectKind)
                  }
                >
                  <option value="standard">标准项目</option>
                  <option value="learning">学习项目</option>
                </select>
                <ChevronDown size={13} aria-hidden="true" />
              </span>
            </label>
            <label>
              <span>周期</span>
              <span className="projects-select">
                <select
                  aria-label="新项目周期"
                  value={newHorizon}
                  onChange={(event) =>
                    setNewHorizon(event.target.value as ProjectHorizon)
                  }
                >
                  <option value="short">短期</option>
                  <option value="long">长期</option>
                </select>
                <ChevronDown size={13} aria-hidden="true" />
              </span>
            </label>
            <div className="projects-create-actions">
              <button type="button" onClick={() => setCreating(false)}>
                取消
              </button>
              <button
                type="submit"
                className="projects-submit-button"
                disabled={name.trim() === '' || createProject.isPending}
              >
                创建项目
              </button>
            </div>
          </div>
        </form>
      ) : null}

      {error !== '' ? (
        <div className="domain-alert" role="alert">
          {error}
        </div>
      ) : null}

      <section
        className="projects-collection"
        aria-labelledby="project-list-title"
      >
        <header className="projects-collection-heading">
          <h3 id="project-list-title">项目列表</h3>
          <span>{projects.length} 项</span>
        </header>

        <div className="projects-list-head" aria-hidden="true">
          <span>项目</span>
          <span>类型</span>
          <span>周期</span>
          <span>状态</span>
          <span>操作</span>
        </div>

        {projectsQuery.isLoading ? (
          <p className="projects-state" role="status">
            正在加载项目…
          </p>
        ) : projectsQuery.isError ? (
          <p className="projects-state is-error">项目暂时不可用。</p>
        ) : projects.length === 0 ? (
          <div className="projects-empty">
            <FolderKanban size={24} strokeWidth={1.4} aria-hidden="true" />
            <strong>没有符合条件的项目</strong>
            <span>调整筛选条件，或者建立一个新项目。</span>
            {hasFilters ? (
              <button type="button" onClick={clearFilters}>
                清除筛选
              </button>
            ) : null}
          </div>
        ) : (
          <div className="projects-list">
            {projects.map((project) => {
              const ProjectIcon = iconForProject(project)
              const projectPath = `/projects/${encodeURIComponent(project.id)}`
              return (
                <article
                  className={`projects-row is-${project.kind}`}
                  key={project.id}
                >
                  <Link className="projects-row-main" to={projectPath}>
                    <span className="projects-row-icon" aria-hidden="true">
                      <ProjectIcon size={18} strokeWidth={1.6} />
                    </span>
                    <span className="projects-row-copy">
                      <strong>{project.name}</strong>
                      <small>{systemRoleLabel(project)}</small>
                    </span>
                  </Link>

                  <div className="projects-row-cell" data-label="类型">
                    <ProjectIcon
                      size={13}
                      strokeWidth={1.7}
                      aria-hidden="true"
                    />
                    <span>{kindLabels[project.kind]}</span>
                  </div>
                  <div className="projects-row-cell" data-label="周期">
                    <CalendarRange
                      size={13}
                      strokeWidth={1.7}
                      aria-hidden="true"
                    />
                    <span>{horizonLabels[project.horizon]}</span>
                  </div>
                  <div
                    className={`projects-row-status is-${project.status}`}
                    data-label="状态"
                  >
                    <i aria-hidden="true" />
                    <span>
                      {statusLabels[project.status] ?? project.status}
                    </span>
                    {project.status === 'planning' && !project.system_role ? (
                      <button
                        type="button"
                        className="projects-status-action"
                        aria-label={`开始${project.name}`}
                        disabled={activateProject.isPending}
                        onClick={() => void handleActivate(project)}
                      >
                        <Play
                          size={12}
                          fill="currentColor"
                          aria-hidden="true"
                        />
                        开始
                      </button>
                    ) : null}
                  </div>
                  <div className="projects-row-actions">
                    <Link
                      className="projects-open-button"
                      to={projectPath}
                      aria-label={`打开${project.name}`}
                      title="打开项目"
                    >
                      <ArrowUpRight size={15} aria-hidden="true" />
                    </Link>
                    {!project.system_role ? (
                      <button
                        type="button"
                        className="projects-delete-button"
                        aria-label={`删除${project.name}`}
                        title="删除项目"
                        disabled={deleteProject.isPending}
                        onClick={() =>
                          void deleteProject.mutateAsync({
                            projectID: project.id,
                            expectedRevision: {
                              expected_project_revision: project.revision,
                            },
                          })
                        }
                      >
                        <Trash2 size={14} aria-hidden="true" />
                      </button>
                    ) : null}
                  </div>
                </article>
              )
            })}
          </div>
        )}
      </section>
    </section>
  )
}
