import { useOccurrences, useProjects } from './useTaskDomain'

export function useTaskInbox() {
  const projectsQuery = useProjects()
  const inboxProject = (projectsQuery.data ?? []).find(
    (project) => project.system_role === 'inbox'
  )
  const occurrencesQuery = useOccurrences(
    {
      scope: 'all',
      project_id: inboxProject?.id ?? '__missing_system_inbox__',
    },
    { enabled: Boolean(inboxProject) }
  )

  return {
    inboxProject,
    occurrencesQuery,
    projectsQuery,
  }
}
