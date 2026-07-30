import { api } from './client'

export interface FuriganaSegment {
  text: string
  reading?: string
}

export interface FuriganaResponse {
  segments: FuriganaSegment[]
  source: 'ai' | 'local'
}

export interface AnnotateJapaneseOptions {
  signal?: AbortSignal
  mode?: 'local'
}

export async function annotateJapanese(
  text: string,
  options: AnnotateJapaneseOptions = {}
): Promise<FuriganaResponse> {
  const body = options.mode ? { text, mode: options.mode } : { text }
  const response = options.signal
    ? await api.post<FuriganaResponse>('/api/japanese/furigana', body, {
        signal: options.signal,
      })
    : await api.post<FuriganaResponse>('/api/japanese/furigana', body)
  return response.data
}
