import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as eventsApi from '../api/events'
import type { Event } from '../api/events'

export function eventsListQueryOptions(params: {
  month?: string
  page?: number
}) {
  return {
    queryKey: ['events', params] as const,
    queryFn: () => eventsApi.getEvents(params),
    staleTime: 5 * 60_000,
  }
}

export function useEventsList(params: { month?: string; page?: number }) {
  return useQuery({
    ...eventsListQueryOptions(params),
    placeholderData: (previousData) => previousData,
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
    mutationFn: ({ id, ...body }: { id: string } & Partial<Event>) =>
      eventsApi.updateEvent(id, body),
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
