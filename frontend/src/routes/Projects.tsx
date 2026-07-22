import { type FormEvent, useState } from 'react'
import { Link } from 'react-router-dom'

import {
  type ProjectHorizon,
  type ProjectKind,
  TaskDomainRevisionConflictError,
} from '../api/taskDomain'
import {
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
  const deleteProject = useDeleteProjectMutation()

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

  return (
    <section className="domain-page" aria-labelledby="projects-heading">
      <header className="domain-page-heading">
        <div>
          <span className="domain-eyebrow">PROJECTS</span>
          <h2 id="projects-heading">项目</h2>
          <p>项目承载目标，任务和每次执行都从这里获得清晰归属。</p>
        </div>
        <button
          type="button"
          className="domain-primary-button"
          onClick={() => setCreating(true)}
        >
          新建项目
        </button>
      </header>

      <div className="domain-toolbar" aria-label="项目筛选">
        <label>
          <span>项目类型</span>
          <select
            aria-label="项目类型"
            value={kind}
            onChange={(event) => setKind(event.target.value as ProjectKind | '')}
          >
            <option value="">全部类型</option>
            <option value="standard">标准项目</option>
            <option value="learning">学习项目</option>
          </select>
        </label>
        <label>
          <span>项目周期</span>
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
        </label>
      </div>

      {creating ? (
        <form className="domain-create-panel" onSubmit={handleCreate}>
          <label>
            <span>项目名称</span>
            <input
              aria-label="项目名称"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
            />
          </label>
          <label>
            <span>类型</span>
            <select
              aria-label="新项目类型"
              value={newKind}
              onChange={(event) => setNewKind(event.target.value as ProjectKind)}
            >
              <option value="standard">标准项目</option>
              <option value="learning">学习项目</option>
            </select>
          </label>
          <label>
            <span>周期</span>
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
          </label>
          <div className="domain-form-actions">
            <button type="button" onClick={() => setCreating(false)}>
              取消
            </button>
            <button
              type="submit"
              className="domain-primary-button"
              disabled={name.trim() === '' || createProject.isPending}
            >
              创建项目
            </button>
          </div>
        </form>
      ) : null}

      {error !== '' ? <div className="domain-alert">{error}</div> : null}
      {projectsQuery.isLoading ? <p className="domain-empty">正在加载项目…</p> : null}
      {projectsQuery.isError ? <p className="domain-empty">项目暂时不可用。</p> : null}

      <div className="project-v2-grid">
        {(projectsQuery.data ?? []).map((project) => {
          const systemLabel =
            project.system_role === 'inbox'
              ? '系统收件箱'
              : project.system_role === 'personal'
                ? '系统个人项目'
                : ''
          return (
            <article className="project-v2-card" key={project.id}>
              <div className="project-v2-card-topline">
                <span>{kindLabels[project.kind]}</span>
                <span>{horizonLabels[project.horizon]}</span>
              </div>
              <Link to={`/projects/${encodeURIComponent(project.id)}`}>
                <h3>{project.name}</h3>
              </Link>
              <div className="project-v2-meta">
                <span className={`domain-status domain-status-${project.status}`}>
                  {projectStatusLabel(project.status)}
                </span>
                {systemLabel !== '' ? (
                  <span className="domain-system-badge">{systemLabel}</span>
                ) : null}
              </div>
              {!project.system_role ? (
                <button
                  type="button"
                  className="domain-text-button domain-danger-button"
                  aria-label={`删除${project.name}`}
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
                  删除
                </button>
              ) : null}
            </article>
          )
        })}
      </div>
    </section>
  )
}

function projectStatusLabel(status: string) {
  return (
    {
      planning: '规划中',
      active: '进行中',
      paused: '已暂停',
      completed: '已完成',
      archived: '已归档',
    }[status] ?? status
  )
}
