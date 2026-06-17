import type { TaskProject } from '../api/tasks'

export const taskProjectTypeLabels: Record<TaskProject['type'], string> = {
  personal: '个人',
  regular: '任务项目',
  learning: '学习项目',
}

export function formatTaskProjectOption(project: TaskProject) {
  return `${project.name} · ${taskProjectTypeLabels[project.type]}`
}
