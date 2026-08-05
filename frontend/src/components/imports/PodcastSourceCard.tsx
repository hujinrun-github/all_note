import { useEffect, useState } from 'react'
import { ExternalLink, FileText, Headphones, LoaderCircle, X } from 'lucide-react'
import {
  useContentImportTranscript,
  useNoteContentImport,
} from '../../hooks/useContentImports'
import '../../styles/content-imports.css'

interface Props {
  noteID: string
}

export function PodcastSourceCard({ noteID }: Props) {
  const sourceQuery = useNoteContentImport(noteID)
  const [transcriptOpen, setTranscriptOpen] = useState(false)
  const source = sourceQuery.data
  const transcriptQuery = useContentImportTranscript(
    source?.id,
    transcriptOpen
  )

  useEffect(() => {
    if (!transcriptOpen) return
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === 'Escape') setTranscriptOpen(false)
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [transcriptOpen])

  if (!source) return null

  return (
    <>
      <aside className="podcast-source-card" aria-label="播客来源">
        <span className="podcast-source-card-icon"><Headphones /></span>
        <div className="podcast-source-card-copy">
          <small>PODCAST SOURCE · {source.source_type === 'apple' ? 'APPLE PODCASTS' : '小宇宙'}</small>
          <strong>{source.podcast_title || '播客节目'} · {source.title || '播客单集'}</strong>
          <p>{source.duration_seconds ? formatDuration(source.duration_seconds) : '时长未提供'} · {source.summarize_with_ai ? 'AI 整理笔记' : '完整逐字稿'}</p>
        </div>
        <div className="podcast-source-card-actions">
          <a href={source.canonical_url || source.source_url} target="_blank" rel="noreferrer">
            打开原始链接 <ExternalLink />
          </a>
          <button type="button" onClick={() => setTranscriptOpen(true)}>
            查看逐字稿 <FileText />
          </button>
        </div>
      </aside>

      {transcriptOpen ? (
        <div className="podcast-transcript-backdrop" role="presentation" onMouseDown={(event) => {
          if (event.target === event.currentTarget) setTranscriptOpen(false)
        }}>
          <section className="podcast-transcript-dialog" role="dialog" aria-modal="true" aria-labelledby="podcast-transcript-title">
            <header>
              <div>
                <small>TRANSCRIPT</small>
                <h2 id="podcast-transcript-title">{source.title || '播客逐字稿'}</h2>
                <p>{source.podcast_title || '播客节目'}</p>
              </div>
              <button type="button" aria-label="关闭逐字稿" onClick={() => setTranscriptOpen(false)}><X /></button>
            </header>
            <div className="podcast-transcript-content">
              {transcriptQuery.isLoading ? <p className="podcast-transcript-state"><LoaderCircle className="is-spinning" />正在读取逐字稿</p> : null}
              {transcriptQuery.isError ? <p className="podcast-transcript-state is-error">逐字稿暂时无法读取，请稍后重试。</p> : null}
              {transcriptQuery.data ? <pre>{transcriptQuery.data}</pre> : null}
            </div>
            <footer>
              <span>逐字稿是只读来源材料，不会覆盖当前笔记正文。</span>
              <button type="button" onClick={() => setTranscriptOpen(false)}>完成</button>
            </footer>
          </section>
        </div>
      ) : null}
    </>
  )
}

function formatDuration(seconds: number) {
  const minutes = Math.max(1, Math.round(seconds / 60))
  return minutes >= 60 ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分钟` : `${minutes} 分钟`
}
