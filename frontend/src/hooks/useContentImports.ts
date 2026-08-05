import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import * as contentImports from '../api/contentImports'

export function useContentImports(enabled = true) {
  return useQuery({
    queryKey: ['content-imports'],
    queryFn: () => contentImports.listContentImports(),
    enabled,
    refetchInterval: (query) => {
      const imports = query.state.data
      return imports?.some((item) => item.status === 'active') ? 2000 : false
    },
    refetchIntervalInBackground: false,
  })
}

export function useResolveContentImport() {
  return useMutation({ mutationFn: contentImports.resolveContentImport })
}

export function useCreateContentImport() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: contentImports.createContentImport,
    onSuccess: (item) => {
      queryClient.setQueryData<contentImports.ContentImport[]>(
        ['content-imports'],
        (current = []) => [
          item,
          ...current.filter((entry) => entry.id !== item.id),
        ]
      )
    },
  })
}

export function useCancelContentImport() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: contentImports.cancelContentImport,
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['content-imports'] }),
  })
}

export function useRetryContentImport() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: contentImports.retryContentImport,
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['content-imports'] }),
  })
}

export function useDeleteContentImport() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: contentImports.deleteContentImport,
    onSuccess: (_, deletedID) => {
      queryClient.setQueryData<contentImports.ContentImport[]>(
        ['content-imports'],
        (current = []) => current.filter((item) => item.id !== deletedID)
      )
    },
  })
}

export function useNoteContentImport(noteID?: string) {
  return useQuery({
    queryKey: ['content-imports', 'note', noteID],
    queryFn: () => contentImports.getContentImportForNote(noteID!),
    enabled: Boolean(noteID),
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
}

export function useContentImportTranscript(importID?: string, enabled = false) {
  return useQuery({
    queryKey: ['content-imports', importID, 'transcript'],
    queryFn: () => contentImports.getContentImportTranscript(importID!),
    enabled: Boolean(importID) && enabled,
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  })
}
