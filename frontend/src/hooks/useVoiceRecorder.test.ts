import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useVoiceRecorder } from './useVoiceRecorder'

const recorders: RecorderMock[] = []
let chunk = new Blob(['audio'], { type: 'audio/webm;codecs=opus' })
class RecorderMock {
  static isTypeSupported(type: string) {
    return type.startsWith('audio/webm')
  }
  state: RecordingState = 'inactive'
  mimeType = 'audio/webm;codecs=opus'
  ondataavailable: ((event: BlobEvent) => void) | null = null
  onstop: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor() {
    recorders.push(this)
  }
  start() {
    this.state = 'recording'
  }
  stop() {
    this.state = 'inactive'
    this.ondataavailable?.({ data: chunk } as BlobEvent)
    this.onstop?.()
  }
}
const stopTrack = vi.fn()
const stream = {
  getTracks: () => [{ stop: stopTrack }],
} as unknown as MediaStream
const getUserMedia = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  chunk = new Blob(['audio'], { type: 'audio/webm;codecs=opus' })
  getUserMedia.mockResolvedValue(stream)
  vi.stubGlobal('MediaRecorder', RecorderMock)
  Object.defineProperty(navigator, 'mediaDevices', {
    configurable: true,
    value: { getUserMedia },
  })
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('useVoiceRecorder', () => {
  it('keeps the actual audio MIME and releases tracks after stopping', async () => {
    const { result } = renderHook(() => useVoiceRecorder())
    await act(() => result.current.start())
    expect(result.current.status).toBe('recording')
    act(() => result.current.stop())
    expect(result.current.blob?.type).toBe('audio/webm;codecs=opus')
    expect(result.current.blob?.size).toBe(5)
    expect(stopTrack).toHaveBeenCalled()
  })
  it('keeps text as an available fallback when permission is denied', async () => {
    getUserMedia.mockRejectedValue(
      new DOMException('denied', 'NotAllowedError')
    )
    const { result } = renderHook(() => useVoiceRecorder())
    await act(() => result.current.start())
    expect(result.current.error).toContain('文字')
    expect(result.current.status).toBe('error')
  })
  it('releases a late permission result after cancellation', async () => {
    let resolve!: (value: MediaStream) => void
    getUserMedia.mockReturnValue(
      new Promise<MediaStream>((done) => {
        resolve = done
      })
    )
    const { result } = renderHook(() => useVoiceRecorder())
    let pending!: Promise<void>
    act(() => {
      pending = result.current.start()
    })
    act(() => result.current.cancel())
    await act(async () => {
      resolve(stream)
      await pending
    })
    expect(stopTrack).toHaveBeenCalled()
    expect(result.current.status).toBe('idle')
  })
  it('rejects empty and oversized audio instead of uploading it', async () => {
    const { result } = renderHook(() => useVoiceRecorder(180, 4))
    await act(() => result.current.start())
    act(() => result.current.stop())
    expect(result.current.blob).toBeNull()
    expect(result.current.error).toContain('大小')
    chunk = new Blob([])
    await act(() => result.current.start())
    act(() => result.current.stop())
    expect(result.current.error).toContain('为空')
  })
  it('stops at the configured duration and releases tracks on unmount', async () => {
    vi.useFakeTimers()
    const { result, unmount } = renderHook(() => useVoiceRecorder(2))
    await act(() => result.current.start())
    act(() => vi.advanceTimersByTime(2000))
    expect(result.current.status).toBe('ready')
    expect(recorders.at(-1)!.state).toBe('inactive')
    expect(result.current.seconds).toBe(2)
    await act(() => result.current.start())
    unmount()
    expect(stopTrack).toHaveBeenCalled()
  })
  it('preserves microphone errors and does not offer empty audio', async () => {
    const { result } = renderHook(() => useVoiceRecorder())
    await act(() => result.current.start())
    act(() => recorders.at(-1)!.onerror?.())
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.blob).toBeNull()
    expect(stopTrack).toHaveBeenCalled()
  })
})
