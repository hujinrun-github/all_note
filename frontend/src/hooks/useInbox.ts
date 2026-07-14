import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as inboxApi from '../api/inbox'
import type { ConvertInboxInput } from '../api/inbox'

export function useInboxList(params: {
  kind?: string
  page?: number
  page_size?: number
}) {
  return useQuery({
    queryKey: ['inbox', params],
    queryFn: () => inboxApi.getInbox(params),
  })
}

export function useCreateInboxItem() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: inboxApi.createInboxItem,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['inbox'] }),
  })
}

export function useConvertInboxItem() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & ConvertInboxInput) =>
      inboxApi.convertInboxItem(id, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['inbox'] })
      qc.invalidateQueries({ queryKey: ['notes'] })
      qc.invalidateQueries({ queryKey: ['tasks'] })
      qc.invalidateQueries({ queryKey: ['events'] })
    },
  })
}

export function useDeleteInboxItem() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: inboxApi.deleteInboxItem,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['inbox'] }),
  })
}

export function useBatchInbox() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      ids,
      action,
    }: {
      ids: string[]
      action: 'archive' | 'delete'
    }) => inboxApi.batchInbox(ids, action),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['inbox'] }),
  })
}
