import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as eventsApi from '../api/events'
import type { Event } from '../api/events'

export function useEventsList(params: { month?: string; page?: number }) {
  return useQuery({
    queryKey: ['events', params],
    queryFn: () => eventsApi.getEvents(params),
  })
}

export function useCreateEvent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: eventsApi.createEvent,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['events'] }),
  })
}

export function useUpdateEvent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & Partial<Event>) => eventsApi.updateEvent(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['events'] }),
  })
}

export function useDeleteEvent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: eventsApi.deleteEvent,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['events'] }),
  })
}
