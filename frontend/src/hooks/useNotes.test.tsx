import { act, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { PropsWithChildren } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as notesAPI from '../api/notes'
import {
  useCreateNote,
  useDeleteNote,
  useTranscribeVoiceNote,
  useUpdateNote,
} from './useNotes'
import { workspaceQueryKeys } from './useWorkspace'

vi.mock('../api/notes', () => ({
  createNote: vi.fn(),
  updateNote: vi.fn(),
  deleteNote: vi.fn(),
  transcribeVoiceNote: vi.fn(),
}))

const note: notesAPI.Note = {
  id: 'note-1',
  title: 'Note',
  body: 'Text',
  folder_id: '',
  tags: '',
  projects: [],
  created_at: 1,
  updated_at: 2,
}
const overviewKey = workspaceQueryKeys.overview({
  date: '2026-11-19',
  timezone: 'Asia/Shanghai',
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(notesAPI.createNote).mockResolvedValue(note)
  vi.mocked(notesAPI.updateNote).mockResolvedValue(note)
  vi.mocked(notesAPI.deleteNote).mockResolvedValue(undefined)
  vi.mocked(notesAPI.transcribeVoiceNote).mockResolvedValue({
    client_id: 'voice-1',
    note_id: note.id,
    body: 'Transcribed text',
    transcription_state: 'completed',
    updated_at: 3,
  })
})

describe('note aggregate invalidation', () => {
  it('expires the recent-note overview after create, update and delete', async () => {
    const { client, wrapper } = createQueryWrapper()
    const { result } = renderHook(
      () => ({
        create: useCreateNote(),
        update: useUpdateNote(),
        remove: useDeleteNote(),
      }),
      { wrapper }
    )

    for (const mutate of [
      () => result.current.create.mutateAsync({ title: 'New note' }),
      () =>
        result.current.update.mutateAsync({
          id: note.id,
          title: 'Renamed note',
        }),
      () => result.current.remove.mutateAsync(note.id),
    ]) {
      client.setQueryData(overviewKey, { notes: [note] })
      client.setQueryData(['notes'], [note])
      await act(async () => {
        await mutate()
      })
      expect(client.getQueryState(overviewKey)?.isInvalidated).toBe(true)
      expect(client.getQueryState(['notes'])?.isInvalidated).toBe(true)
    }
  })

  it('updates the note immediately and expires the overview after transcription', async () => {
    const { client, wrapper } = createQueryWrapper()
    client.setQueryData(overviewKey, { notes: [note] })
    client.setQueryData(['notes', note.id], note)
    const { result } = renderHook(() => useTranscribeVoiceNote(note.id), {
      wrapper,
    })

    await act(async () => {
      await result.current.mutateAsync('voice-1')
    })

    expect(client.getQueryData<notesAPI.Note>(['notes', note.id])?.body).toBe(
      'Transcribed text'
    )
    expect(client.getQueryState(overviewKey)?.isInvalidated).toBe(true)
  })
})

function createQueryWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return {
    client,
    wrapper: ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  }
}
