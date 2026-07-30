import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as notesApi from '../api/notes'

export function useNotesList(params: {
  folder_id?: string
  sort?: string
  page?: number
}) {
  return useQuery({
    queryKey: ['notes', params],
    queryFn: () => notesApi.getNotes(params),
  })
}

export function useNote(id: string) {
  return useQuery({
    queryKey: ['notes', id],
    queryFn: () => notesApi.getNote(id),
    enabled: !!id,
  })
}

export function useCreateNote() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: notesApi.createNote,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notes'] }),
  })
}

export function useUpdateNote() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: string
      title?: string
      body?: string
      folder_id?: string
      tags?: string
      project_ids?: string[]
    }) => notesApi.updateNote(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notes'] }),
  })
}

export function useDeleteNote() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: notesApi.deleteNote,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notes'] }),
  })
}

export function useNoteAttachments(noteID?: string) {
  return useQuery({
    queryKey: ['notes', noteID, 'attachments'],
    queryFn: () => notesApi.getNoteAttachments(noteID!),
    enabled: Boolean(noteID),
  })
}

export function useUploadNoteAttachment(noteID: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (file: File) => notesApi.uploadNoteAttachment(noteID, file),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ['notes', noteID, 'attachments'] }),
  })
}

export function useDeleteNoteAttachment(noteID: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (attachmentID: string) =>
      notesApi.deleteNoteAttachment(noteID, attachmentID),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ['notes', noteID, 'attachments'] }),
  })
}
