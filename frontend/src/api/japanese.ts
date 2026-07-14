import { api } from './client'

export interface FuriganaSegment {
  text: string
  reading?: string
}

export interface FuriganaResponse {
  segments: FuriganaSegment[]
  source: 'ai' | 'local'
}

export async function annotateJapanese(
  text: string
): Promise<FuriganaResponse> {
  const response = await api.post<FuriganaResponse>('/api/japanese/furigana', {
    text,
  })
  return response.data
}
