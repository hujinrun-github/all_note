export const DEFAULT_TASK_PROJECT = '个人'
export const DEFAULT_TASK_PROJECTS = [DEFAULT_TASK_PROJECT, '工作', '技术']

const TASK_PROJECTS_STORAGE_KEY = 'flowspace.taskProjects.v1'

export function todayDateInputValue() {
  return dateToInputValue(new Date())
}

export function dateToInputValue(date: Date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

export function dateInputToUnix(value: string) {
  const [year, month, day] = value.split('-').map(Number)
  if (!year || !month || !day) return undefined
  const date = new Date(year, month - 1, day, 0, 0, 0, 0)
  return Math.floor(date.getTime() / 1000)
}

export function normalizeTaskProject(value: unknown) {
  if (typeof value === 'string') return value.trim()
  if (value && typeof value === 'object' && 'name' in value) {
    const name = (value as { name?: unknown }).name
    return typeof name === 'string' ? name.trim() : ''
  }
  return ''
}

export function readStoredTaskProjects() {
  if (typeof window === 'undefined') return []
  try {
    const raw = window.localStorage.getItem(TASK_PROJECTS_STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

export function saveStoredTaskProjects(projects: string[]) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(TASK_PROJECTS_STORAGE_KEY, JSON.stringify(mergeTaskProjects(projects)))
  } catch {
    // Storage can be unavailable in restricted browser contexts; the in-memory state still works.
  }
}

export function mergeTaskProjects(...groups: Array<Array<unknown> | undefined>) {
  const seen = new Set<string>()
  const projects: string[] = []

  for (const project of DEFAULT_TASK_PROJECTS) {
    appendProject(project)
  }
  for (const group of groups) {
    for (const project of group ?? []) {
      appendProject(project)
    }
  }

  return projects

  function appendProject(value: unknown) {
    const project = normalizeTaskProject(value)
    if (!project || seen.has(project)) return
    seen.add(project)
    projects.push(project)
  }
}
