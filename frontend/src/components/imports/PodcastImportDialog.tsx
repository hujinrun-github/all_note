import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Check,
  FileText,
  Headphones,
  Link2,
  LoaderCircle,
  Podcast,
  Sparkles,
  X,
} from 'lucide-react'
import { APIError } from '../../api/client'
import { getRuntimeSettings } from '../../api/settings'
import {
  useCreateContentImport,
  useResolveContentImport,
} from '../../hooks/useContentImports'
import '../../styles/content-imports.css'

interface Props {
  open: boolean
  projects: ReadonlyArray<{ id: string; name: string }>
  projectsLoading?: boolean
  projectsError?: boolean
  selectedProjectID?: string
  onClose: () => void
  onCreated: () => void
}

export function PodcastImportDialog({
  open,
  projects,
  projectsLoading = false,
  projectsError = false,
  selectedProjectID = '',
  onClose,
  onCreated,
}: Props) {
  const [sourceURL, setSourceURL] = useState('')
  const [summarizeWithAI, setSummarizeWithAI] = useState(false)
  const [includeTranscript, setIncludeTranscript] = useState(false)
  const [projectID, setProjectID] = useState(selectedProjectID)
  const [tags, setTags] = useState('播客')
  const resolveImport = useResolveContentImport()
  const createImport = useCreateContentImport()
  const runtimeSettings = useQuery({
    queryKey: ['settings', 'runtime'],
    queryFn: getRuntimeSettings,
    enabled: open,
    retry: false,
    staleTime: 30_000,
  })
  const chatBinding = runtimeSettings.data?.bindings.find(
    (binding) => binding.kind === 'llm_chat'
  )
  const textAIAvailable = Boolean(
    chatBinding &&
      chatBinding.mode !== 'disabled' &&
      chatBinding.provider !== 'unavailable'
  )

  useEffect(() => {
    if (!open) return
    setProjectID(selectedProjectID)
  }, [open, selectedProjectID])

  useEffect(() => {
    if (!open || !runtimeSettings.isFetched) return
    setSummarizeWithAI(textAIAvailable)
  }, [open, runtimeSettings.isFetched, textAIAvailable])

  if (!open) return null

  const episode = resolveImport.data
  const busy = resolveImport.isPending || createImport.isPending
  const error = resolveImport.error ?? createImport.error

  function handleURLChange(value: string) {
    setSourceURL(value)
    resolveImport.reset()
    createImport.reset()
  }

  async function handlePrimaryAction() {
    if (!episode) {
      await resolveImport.mutateAsync(sourceURL.trim())
      return
    }
    await createImport.mutateAsync({
      source_url: sourceURL.trim(),
      summarize_with_ai: summarizeWithAI,
      include_transcript: summarizeWithAI ? includeTranscript : true,
      language: 'auto',
      project_ids: projectID ? [projectID] : [],
      tags: tags
        .split(/[，,]/)
        .map((tag) => tag.trim())
        .filter(Boolean),
    })
    onCreated()
    onClose()
  }

  return (
    <div className="content-import-backdrop" onMouseDown={onClose}>
      <section
        className="content-import-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="content-import-title"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="content-import-dialog-header">
          <span className="content-import-dialog-icon">
            <Podcast />
          </span>
          <div>
            <span>CONTENT TO NOTE</span>
            <h2 id="content-import-title">导入播客</h2>
            <p>先生成可靠的逐字稿，再按需用 AI 整理成笔记。</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭导入弹窗">
            <X />
          </button>
        </header>

        <div className="content-import-dialog-body">
          <label className="content-import-url-field">
            <span>单集链接</span>
            <div className={error ? 'has-error' : ''}>
              <Link2 />
              <input
                autoFocus
                value={sourceURL}
                onChange={(event) => handleURLChange(event.target.value)}
                placeholder="粘贴小宇宙或 Apple Podcasts 单集链接"
              />
              {episode ? <Check className="is-valid" /> : null}
            </div>
          </label>

          {error ? (
            <p className="content-import-error" role="alert">
              {error instanceof APIError
                ? error.message
                : '链接解析失败，请稍后重试。'}
            </p>
          ) : null}

          {episode ? (
            <>
              <article className="content-import-episode-card">
                {episode.cover_url ? (
                  <img src={episode.cover_url} alt="" />
                ) : (
                  <span>
                    <Headphones />
                  </span>
                )}
                <div>
                  <em>
                    {episode.source_type === 'apple'
                      ? 'Apple Podcasts'
                      : '小宇宙'}
                  </em>
                  <strong>{episode.title || '已识别的播客单集'}</strong>
                  <p>
                    {episode.podcast_title || '播客节目'}
                    {episode.duration_seconds
                      ? ` · ${formatDuration(episode.duration_seconds)}`
                      : ''}
                  </p>
                </div>
              </article>

              {!episode.has_public_transcript ? (
                <div className="content-import-capability-note">
                  <Headphones />
                  <p>
                    <strong>未发现发布者公开逐字稿</strong>
                    <span>
                      将安全下载公开音频并在后台分片转写；AI
                      整理只会在逐字稿完成后运行。
                    </span>
                  </p>
                </div>
              ) : null}

              <section
                className="content-import-pipeline"
                aria-label="处理步骤"
              >
                <div>
                  <span>1</span>
                  <FileText />
                  <p>
                    <strong>生成逐字稿</strong>
                    <small>必选 · 优先使用发布者公开文字稿</small>
                  </p>
                  <em>必需</em>
                </div>
                <i />
                <div
                  className={`${summarizeWithAI ? 'is-enabled' : ''}${textAIAvailable ? '' : ' is-unavailable'}`}
                >
                  <span>2</span>
                  <Sparkles />
                  <p>
                    <strong>AI 整理</strong>
                    <small>
                      {runtimeSettings.isPending
                        ? '正在检查文本 AI 配置'
                        : textAIAvailable
                          ? '摘要、章节、核心观点与行动项'
                          : '当前工作区未配置文本 AI'}
                    </small>
                  </p>
                  <button
                    type="button"
                    role="switch"
                    aria-label="AI 整理"
                    aria-checked={summarizeWithAI}
                    disabled={runtimeSettings.isPending || !textAIAvailable}
                    onClick={() => setSummarizeWithAI((value) => !value)}
                  >
                    <b />
                  </button>
                </div>
              </section>

              {runtimeSettings.isFetched && !textAIAvailable ? (
                <div
                  className="content-import-capability-note is-ai-unavailable"
                  role="status"
                >
                  <Sparkles />
                  <p>
                    <strong>AI 整理暂不可用</strong>
                    <span>
                      当前工作区没有可用的文本 AI。请先在“设置 → AI
                      服务”中完成配置；本次仍可只生成完整逐字稿。
                    </span>
                  </p>
                </div>
              ) : null}

              <div className="content-import-options">
                {summarizeWithAI ? (
                  <label className="content-import-checkbox">
                    <input
                      type="checkbox"
                      checked={includeTranscript}
                      onChange={(event) =>
                        setIncludeTranscript(event.target.checked)
                      }
                    />
                    <span>
                      <Check />
                    </span>
                    在结构化笔记末尾附上完整逐字稿
                  </label>
                ) : (
                  <p className="content-import-mode-note">
                    <FileText />
                    关闭 AI 整理后，笔记正文将直接保存完整逐字稿，不调用文本
                    AI。
                  </p>
                )}
                <div className="content-import-form-row">
                  <label>
                    <span>关联项目</span>
                    <select
                      aria-label="关联项目"
                      value={projectID}
                      disabled={projectsLoading}
                      onChange={(event) => setProjectID(event.target.value)}
                    >
                      <option value="">
                        {projectsLoading ? '项目加载中…' : '未归属'}
                      </option>
                      {projects.map((project) => (
                        <option key={project.id} value={project.id}>
                          {project.name}
                        </option>
                      ))}
                    </select>
                    {projectsError ? (
                      <small role="alert">
                        项目列表加载失败，仍可保存为未归属笔记。
                      </small>
                    ) : null}
                  </label>
                  <label>
                    <span>标签</span>
                    <input
                      value={tags}
                      onChange={(event) => setTags(event.target.value)}
                      placeholder="播客, 学习"
                    />
                  </label>
                </div>
              </div>
            </>
          ) : (
            <div className="content-import-empty-preview">
              <Headphones />
              <strong>支持公开单集链接</strong>
              <p>解析只读取公开元数据，不会在这一步调用转写或文本 AI。</p>
            </div>
          )}
        </div>

        <footer className="content-import-dialog-footer">
          <p>
            {episode && summarizeWithAI ? (
              <>
                <Sparkles />
                逐字稿完成后才会调用文本 AI
              </>
            ) : (
              <>
                <FileText />
                解析与转录是两个独立步骤
              </>
            )}
          </p>
          <div>
            <button
              type="button"
              className="secondary-action"
              onClick={onClose}
            >
              取消
            </button>
            <button
              type="button"
              className="primary-action"
              disabled={busy || !sourceURL.trim()}
              onClick={() => void handlePrimaryAction()}
            >
              {busy ? (
                <LoaderCircle className="is-spinning" />
              ) : episode && summarizeWithAI ? (
                <Sparkles />
              ) : episode ? (
                <Headphones />
              ) : (
                <Link2 />
              )}
              {resolveImport.isPending
                ? '解析中'
                : createImport.isPending
                  ? '提交中'
                  : !episode
                    ? '解析链接'
                    : summarizeWithAI
                      ? '开始转写并整理'
                      : '开始转写'}
            </button>
          </div>
        </footer>
      </section>
    </div>
  )
}

function formatDuration(seconds: number) {
  const minutes = Math.round(seconds / 60)
  return minutes >= 60
    ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分`
    : `${minutes} 分钟`
}
