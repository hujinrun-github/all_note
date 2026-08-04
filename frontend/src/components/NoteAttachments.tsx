import { useRef, useState, type ChangeEvent } from 'react'
import { APIError, apiResourceURL } from '../api/client'
import type { NoteAttachment } from '../api/notes'
import {
  useDeleteNoteAttachment,
  useNoteAttachments,
  useTranscribeVoiceNote,
  useUploadNoteAttachment,
} from '../hooks/useNotes'

const CLIENT_MAX_ATTACHMENT_BYTES = 200 * 1024 * 1024

export function NoteAttachmentsSection({
  noteID,
  onTranscribed,
}: {
  noteID: string
  onTranscribed?: (body: string) => void
}) {
  const attachmentsQuery = useNoteAttachments(noteID)
  const upload = useUploadNoteAttachment(noteID)
  const remove = useDeleteNoteAttachment(noteID)
  const transcribe = useTranscribeVoiceNote(noteID)
  const inputRef = useRef<HTMLInputElement>(null)
  const [message, setMessage] = useState('')

  async function chooseFile(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setMessage('')
    if (file.size > CLIENT_MAX_ATTACHMENT_BYTES) {
      setMessage('单个附件不能超过 200 MiB。')
      return
    }
    try {
      await upload.mutateAsync(file)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '附件上传失败。')
    }
  }

  async function deleteAttachment(attachment: NoteAttachment) {
    if (!attachment.deletable || remove.isPending) return
    if (!window.confirm(`确定删除附件“${attachment.original_name}”吗？`)) {
      return
    }
    setMessage('')
    try {
      await remove.mutateAsync(attachment.id)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '附件删除失败。')
    }
  }

  async function transcribeAttachment(attachment: NoteAttachment) {
    if (attachment.source !== 'voice_note' || transcribe.isPending) return
    setMessage('')
    try {
      const voiceNote = await transcribe.mutateAsync(attachment.id)
      onTranscribed?.(voiceNote.body)
      setMessage('转写完成，识别结果已写入正文。')
    } catch (error) {
      setMessage(transcriptionErrorMessage(error))
    }
  }

  const attachments = attachmentsQuery.data ?? []
  const loadError = attachmentLoadError(attachmentsQuery.error)

  return (
    <section
      className="note-attachments"
      aria-labelledby="note-attachments-title"
    >
      <header className="note-attachments-heading">
        <div>
          <span>媒体与文件</span>
          <h2 id="note-attachments-title">附件</h2>
        </div>
        <button
          type="button"
          className="note-attachment-upload"
          onClick={() => inputRef.current?.click()}
          disabled={upload.isPending || attachments.length >= 20}
        >
          <AttachmentPlusIcon />
          {upload.isPending ? '上传中…' : '添加附件'}
        </button>
        <input
          ref={inputRef}
          type="file"
          aria-label="选择附件文件"
          className="note-attachment-input"
          accept="audio/*,video/*,image/jpeg,image/png,image/webp,image/gif,application/pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.zip"
          onChange={chooseFile}
        />
      </header>

      {attachmentsQuery.isLoading ? (
        <div className="note-attachments-loading">正在读取附件…</div>
      ) : attachmentsQuery.isError ? (
        <div className="note-attachments-empty is-error">
          <div>
            <strong>{loadError.title}</strong>
            <p>{loadError.description}</p>
          </div>
          <button
            type="button"
            className="note-attachment-retry"
            onClick={() => void attachmentsQuery.refetch()}
            disabled={attachmentsQuery.isFetching}
          >
            {attachmentsQuery.isFetching ? '重试中…' : '重试'}
          </button>
        </div>
      ) : attachments.length === 0 ? (
        <div className="note-attachments-empty">
          <AttachmentIcon />
          <div>
            <strong>还没有附件</strong>
            <p>可以添加录音、视频、图片、PDF 和常用文档。</p>
          </div>
        </div>
      ) : (
        <div className="note-attachment-list">
          {attachments.map((attachment) => (
            <AttachmentCard
              key={`${attachment.source}:${attachment.id}`}
              attachment={attachment}
              deleting={remove.isPending && remove.variables === attachment.id}
              transcribing={
                transcribe.isPending && transcribe.variables === attachment.id
              }
              onDelete={() => void deleteAttachment(attachment)}
              onTranscribe={() => void transcribeAttachment(attachment)}
            />
          ))}
        </div>
      )}

      {message ? (
        <p className="note-attachment-message" role="alert">
          {message}
        </p>
      ) : null}
      <p className="note-attachment-hint">
        每篇笔记最多 20 个附件，单个文件默认不超过 200 MiB。
      </p>
    </section>
  )
}

function attachmentLoadError(error: unknown) {
  if (error instanceof APIError && error.status === 404) {
    return {
      title: '后端尚未加载附件接口',
      description: '请重启后端服务，然后点击重试。',
    }
  }
  if (error instanceof APIError && error.status === 503) {
    return {
      title: '附件服务暂时不可用',
      description: '请检查后端与对象存储配置，然后点击重试。',
    }
  }
  return {
    title: '附件暂时无法读取',
    description: '请稍后重试；如果问题持续，请查看后端日志。',
  }
}

function AttachmentCard({
  attachment,
  deleting,
  transcribing,
  onDelete,
  onTranscribe,
}: {
  attachment: NoteAttachment
  deleting: boolean
  transcribing: boolean
  onDelete: () => void
  onTranscribe: () => void
}) {
  const contentURL = apiResourceURL(attachment.content_url)
  return (
    <article className={`note-attachment-card is-${attachment.kind}`}>
      <div className="note-attachment-preview">
        {attachment.kind === 'audio' ? (
          <audio
            controls
            preload="metadata"
            src={contentURL}
            aria-label={`播放 ${attachment.original_name}`}
          />
        ) : attachment.kind === 'video' ? (
          <video
            controls
            preload="metadata"
            src={contentURL}
            aria-label={`播放 ${attachment.original_name}`}
          />
        ) : attachment.kind === 'image' ? (
          <img loading="lazy" src={contentURL} alt={attachment.original_name} />
        ) : (
          <div className="note-attachment-file-icon">
            <FileIcon />
          </div>
        )}
      </div>
      <div className="note-attachment-meta">
        <div>
          <strong title={attachment.original_name}>
            {attachment.original_name}
          </strong>
          {attachment.source === 'voice_note' ? (
            <span className="note-attachment-source">语音笔记</span>
          ) : null}
        </div>
        <small>
          {attachment.mime_type} · {formatBytes(attachment.size_bytes)}
        </small>
      </div>
      <div className="note-attachment-actions">
        {attachment.source === 'voice_note' ? (
          <button
            type="button"
            className="note-attachment-action is-transcription"
            onClick={onTranscribe}
            disabled={
              transcribing || attachment.transcription_state === 'completed'
            }
          >
            {transcriptionActionLabel(attachment, transcribing)}
          </button>
        ) : null}
        <a
          href={`${contentURL}?download=1`}
          download={attachment.original_name}
          className="note-attachment-action"
        >
          下载
        </a>
        {attachment.deletable ? (
          <button
            type="button"
            className="note-attachment-action is-danger"
            onClick={onDelete}
            disabled={deleting}
          >
            {deleting ? '删除中…' : '删除'}
          </button>
        ) : null}
      </div>
    </article>
  )
}

function transcriptionActionLabel(
  attachment: NoteAttachment,
  transcribing: boolean
) {
  if (transcribing || attachment.transcription_state === 'processing') {
    return '转写中…'
  }
  if (attachment.transcription_state === 'completed') return '已转写'
  if (attachment.transcription_state === 'failed') return '重试转写'
  return '转成文字'
}

function transcriptionErrorMessage(error: unknown) {
  if (error instanceof APIError && error.status === 503) {
    return '语音转写服务暂不可用，请先在设置中检查语音转写配置。'
  }
  if (error instanceof APIError && error.status === 409) {
    return '录音尚未上传完成，请稍后再试。'
  }
  return error instanceof Error ? error.message : '语音转写失败，请稍后重试。'
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

function AttachmentPlusIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 5v14M5 12h14" />
      <path d="M19 8.5V6a2 2 0 0 0-2-2H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-2.5" />
    </svg>
  )
}

function AttachmentIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="m20.5 11.5-8.9 8.9a6 6 0 0 1-8.5-8.5l9.4-9.4a4 4 0 0 1 5.7 5.7l-9.4 9.4a2 2 0 1 1-2.8-2.8l8.7-8.7" />
    </svg>
  )
}

function FileIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M6 3h8l4 4v14H6z" />
      <path d="M14 3v5h5M9 13h6M9 17h4" />
    </svg>
  )
}
