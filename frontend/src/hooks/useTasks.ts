import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as tasksApi from '../api/tasks'
import type { Task } from '../api/tasks'

export function useTasksList(params: {
  project?: string
  project_id?: string
  status?: string
  scope?: string
  horizon?: string
  planned_date?: string
  page?: number
}) {
  return useQuery({
    queryKey: ['tasks', params],
    queryFn: () => tasksApi.getTasks(params),
  })
}

export function useCreateTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: tasksApi.createTask,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tasks'] })
      qc.invalidateQueries({ queryKey: ['task-projects'] })
      qc.invalidateQueries({ queryKey: ['task-project-names'] })
      qc.invalidateQueries({ queryKey: ['today'] })
    },
  })
}

export function useUpdateTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & Partial<Task>) => tasksApi.updateTask(id, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tasks'] })
      qc.invalidateQueries({ queryKey: ['today'] })
    },
  })
}

export function useDeleteTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: tasksApi.deleteTask,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks'] }),
  })
}
