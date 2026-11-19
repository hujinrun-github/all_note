import { useCallback, useEffect, useRef, useState } from 'react'

export const VOICE_MAX_BYTES = 20 * 1024 * 1024
export const VOICE_MAX_SECONDS = 180
const recordingTypes = [
  'audio/webm;codecs=opus',
  'audio/mp4',
  'audio/webm',
  'audio/ogg;codecs=opus',
]

export function useVoiceRecorder(
  maxSeconds = VOICE_MAX_SECONDS,
  maxBytes = VOICE_MAX_BYTES
) {
  const [status, setStatus] = useState<
    'idle' | 'requesting' | 'recording' | 'stopping' | 'ready' | 'error'
  >('idle')
  const [blob, setBlob] = useState<Blob | null>(null)
  const [seconds, setSeconds] = useState(0)
  const [error, setError] = useState('')
  const resources = useRef<{
    recorder?: MediaRecorder
    stream?: MediaStream
    timer?: ReturnType<typeof setInterval>
  }>({})
  const generation = useRef(0)

  const release = useCallback(() => {
    clearInterval(resources.current.timer)
    resources.current.stream?.getTracks().forEach((track) => track.stop())
    resources.current = {}
  }, [])

  const cancel = useCallback(() => {
    generation.current++
    const recorder = resources.current.recorder
    if (recorder) {
      recorder.ondataavailable = null
      recorder.onstop = null
      recorder.onerror = null
      if (recorder.state !== 'inactive') recorder.stop()
    }
    release()
    setBlob(null)
    setSeconds(0)
    setError('')
    setStatus('idle')
  }, [release])

  const stop = useCallback(() => {
    const recorder = resources.current.recorder
    if (recorder?.state === 'recording') {
      setStatus('stopping')
      clearInterval(resources.current.timer)
      recorder.stop()
      resources.current.stream?.getTracks().forEach((track) => track.stop())
    }
  }, [])

  const start = useCallback(async () => {
    cancel()
    if (
      !navigator.mediaDevices?.getUserMedia ||
      typeof MediaRecorder === 'undefined'
    ) {
      setError(
        '当前浏览器无法录音，请使用文字输入，或通过 HTTPS 打开支持录音的浏览器。'
      )
      setStatus('error')
      return
    }
    const current = generation.current
    setStatus('requesting')
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      if (current !== generation.current) {
        stream.getTracks().forEach((track) => track.stop())
        return
      }
      resources.current.stream = stream
      const mimeType = recordingTypes.find((type) =>
        MediaRecorder.isTypeSupported(type)
      )
      const recorder = new MediaRecorder(
        stream,
        mimeType ? { mimeType } : undefined
      )
      resources.current.recorder = recorder
      const chunks: Blob[] = []
      let size = 0
      let oversized = false
      recorder.ondataavailable = (event) => {
        if (current !== generation.current || event.data.size === 0) return
        size += event.data.size
        if (size > maxBytes) {
          oversized = true
          chunks.length = 0
          stop()
        } else if (!oversized) chunks.push(event.data)
      }
      recorder.onstop = () => {
        if (current !== generation.current) return
        release()
        if (oversized || size === 0) {
          setError(
            oversized
              ? '录音超过大小限制，请缩短后重新录制。'
              : '录音为空，请检查麦克风后重试，或使用文字输入。'
          )
          setStatus('error')
          return
        }
        setBlob(
          new Blob(chunks, {
            type:
              chunks[0]?.type ||
              recorder.mimeType ||
              'application/octet-stream',
          })
        )
        setStatus('ready')
      }
      recorder.onerror = () => {
        if (current !== generation.current) return
        generation.current++
        release()
        setError('录音设备出现异常，请重新录音或使用文字输入。')
        setStatus('error')
      }
      recorder.start(1000)
      const started = Date.now()
      setStatus('recording')
      resources.current.timer = setInterval(() => {
        const elapsed = Math.floor((Date.now() - started) / 1000)
        setSeconds(Math.min(elapsed, maxSeconds))
        if (elapsed >= maxSeconds) stop()
      }, 250)
    } catch (caught) {
      if (current !== generation.current) return
      release()
      setError(
        caught instanceof DOMException && caught.name === 'NotAllowedError'
          ? '麦克风权限未获允许。你仍可输入文字生成计划。'
          : '无法访问麦克风，请检查设备或使用文字输入。'
      )
      setStatus('error')
    }
  }, [cancel, maxBytes, maxSeconds, release, stop])

  useEffect(
    () => () => {
      generation.current++
      const recorder = resources.current.recorder
      if (recorder) {
        recorder.onstop = null
        recorder.ondataavailable = null
        recorder.onerror = null
        if (recorder.state !== 'inactive') recorder.stop()
      }
      release()
    },
    [release]
  )

  return { status, blob, seconds, error, start, stop, cancel }
}
