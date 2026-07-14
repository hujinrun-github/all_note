import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  getCalendarProjectSources,
  saveCalendarProjectSources,
} from '../api/calendar'

export const calendarProjectSourcesQueryKey = ['calendar-project-sources'] as const

export function useCalendarProjectSources() {
  return useQuery({
    queryKey: calendarProjectSourcesQueryKey,
    queryFn: getCalendarProjectSources,
  })
}

export function useSaveCalendarProjectSources() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: saveCalendarProjectSources,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: calendarProjectSourcesQueryKey })
      qc.invalidateQueries({ queryKey: ['events'] })
    },
  })
}
