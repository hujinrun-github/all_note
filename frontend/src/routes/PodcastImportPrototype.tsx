import { useEffect, useMemo, useState } from 'react'
import {
  Bell,
  BookOpen,
  CalendarDays,
  Check,
  CheckCircle2,
  CheckSquare,
  ChevronDown,
  ChevronRight,
  Clock,
  FileText,
  Folder,
  Hash,
  Headphones,
  Inbox,
  Link2,
  LoaderCircle,
  MoreHorizontal,
  Notebook,
  PanelRightClose,
  Plus,
  Podcast,
  Search,
  Settings,
  Share2,
  SlidersHorizontal,
  Sparkles,
  Star,
  X,
} from 'lucide-react'
import '../styles/podcast-import-prototype.css'

type ImportStatus = 'processing' | 'done'

type DemoNote = {
  id: string
  title: string
  preview: string
  tag: string
  updatedAt: string
}

const DEFAULT_URL = 'https://www.xiaoyuzhoufm.com/episode/65f2c8a7…'

const DEMO_PROJECTS = [
  { id: 'ai-learning', name: 'AI 学习' },
  { id: 'product-research', name: '产品研究' },
  { id: 'knowledge-system', name: '知识系统' },
] as const

const BASE_NOTES: DemoNote[] = [
  {
    id: 'product-manager',
    title: 'AI 产品经理的下一站',
    preview: 'AI 时代，产品经理的核心能力正在从定义需求转向定义问题。',
    tag: '播客',
    updatedAt: '今天 10:32',
  },
  {
    id: 'knowledge-system',
    title: '如何建立长期知识系统',
    preview: '输入、处理、输出与迭代，构成一套能够复利的知识系统。',
    tag: '方法论',
    updatedAt: '昨天 21:15',
  },
  {
    id: 'prd',
    title: '产品需求文档模板（PRD）',
    preview: '一份可复用的 PRD 模板，覆盖背景、目标、范围与验收标准。',
    tag: '产品',
    updatedAt: '昨天 16:45',
  },
  {
    id: 'okr',
    title: 'OKR 与项目计划的关系',
    preview: 'OKR 提供方向，项目计划负责落地，两者需要在节奏上对齐。',
    tag: '管理',
    updatedAt: '5 月 18 日',
  },
  {
    id: 'interview',
    title: '用户访谈记录：SaaS 团队协作',
    preview: '整理了 8 位用户的访谈重点，关注权限、通知与协作体验。',
    tag: '研究',
    updatedAt: '5 月 17 日',
  },
]

const GENERATED_NOTE: DemoNote = {
  id: 'generated-ai-pm',
  title: 'AI 产品经理的下一站｜播客笔记',
  preview: '从需求执行者到问题定义者：AI 正在重塑产品经理的工作边界。',
  tag: '播客',
  updatedAt: '刚刚',
}

const GENERATED_TRANSCRIPT: DemoNote = {
  id: 'generated-transcript',
  title: 'AI 产品经理的下一站｜完整逐字稿',
  preview: '00:00 主持人：今天我们来聊一聊，AI 会怎样改变产品经理的工作。',
  tag: '逐字稿',
  updatedAt: '刚刚',
}

export default function PodcastImportPrototype() {
  const [modalOpen, setModalOpen] = useState(true)
  const [drawerOpen, setDrawerOpen] = useState(true)
  const [url, setUrl] = useState(DEFAULT_URL)
  const [parsed, setParsed] = useState(true)
  const [summarizeWithAI, setSummarizeWithAI] = useState(true)
  const [attachTranscript, setAttachTranscript] = useState(false)
  const [keepAudio, setKeepAudio] = useState(false)
  const [projectID, setProjectID] = useState('ai-learning')
  const [task, setTask] = useState<{
    status: ImportStatus
    progress: number
    summarize: boolean
  }>({ status: 'processing', progress: 46, summarize: true })
  const [selectedNoteID, setSelectedNoteID] = useState('product-manager')

  useEffect(() => {
    if (task.status !== 'processing') return

    const timer = window.setInterval(() => {
      setTask((current) => {
        const nextProgress = Math.min(current.progress + 7, 100)
        return {
          progress: nextProgress,
          status: nextProgress === 100 ? 'done' : 'processing',
          summarize: current.summarize,
        }
      })
    }, 900)

    return () => window.clearInterval(timer)
  }, [task.status])

  const notes = useMemo(() => {
    if (task.status !== 'done') return BASE_NOTES
    return [
      task.summarize ? GENERATED_NOTE : GENERATED_TRANSCRIPT,
      ...BASE_NOTES,
    ]
  }, [task.status, task.summarize])
  const selectedNote =
    notes.find((note) => note.id === selectedNoteID) ?? notes[0]

  function handleUrlChange(nextUrl: string) {
    setUrl(nextUrl)
    setParsed(false)
  }

  function handlePrimaryAction() {
    if (!parsed) {
      setParsed(true)
      return
    }

    setTask({
      status: 'processing',
      progress: 8,
      summarize: summarizeWithAI,
    })
    setModalOpen(false)
    setDrawerOpen(true)
  }

  function openGeneratedNote() {
    setSelectedNoteID(
      task.summarize ? GENERATED_NOTE.id : GENERATED_TRANSCRIPT.id
    )
    setDrawerOpen(false)
  }

  return (
    <div className="podcast-demo-shell">
      <DemoSidebar />

      <main className="podcast-demo-main">
        <header className="podcast-demo-topbar">
          <div className="podcast-demo-breadcrumb">
            <BookOpen aria-hidden="true" />
            <span>知识库</span>
            <ChevronRight aria-hidden="true" />
            <strong>笔记</strong>
          </div>

          <div className="podcast-demo-topbar-actions">
            <label className="podcast-demo-global-search">
              <Search aria-hidden="true" />
              <span className="sr-only">搜索笔记</span>
              <input placeholder="搜索笔记、标签或内容" />
              <kbd>⌘ K</kbd>
            </label>
            <button
              type="button"
              className="podcast-demo-icon-button"
              aria-label="通知"
            >
              <Bell aria-hidden="true" />
              <i />
            </button>
            <button type="button" className="podcast-demo-profile">
              <span>林</span>
              林川
              <ChevronDown aria-hidden="true" />
            </button>
          </div>
        </header>

        <section className="podcast-demo-page-heading">
          <div>
            <span>KNOWLEDGE LIBRARY</span>
            <h1>笔记</h1>
          </div>
          <div className="podcast-demo-page-actions">
            <button
              type="button"
              className="podcast-demo-button podcast-demo-button-secondary"
              onClick={() => setModalOpen(true)}
            >
              <Podcast aria-hidden="true" />
              导入播客
            </button>
            <button
              type="button"
              className="podcast-demo-button podcast-demo-button-primary"
            >
              <Plus aria-hidden="true" />
              新建笔记
            </button>
          </div>
        </section>

        <section className="podcast-demo-workspace" aria-label="笔记工作区">
          <DemoFilters />

          <section className="podcast-demo-note-list" aria-label="笔记列表">
            <div className="podcast-demo-list-heading">
              <div>
                <span>当前视图</span>
                <strong>全部笔记</strong>
              </div>
              <button type="button">
                最新编辑
                <ChevronDown aria-hidden="true" />
              </button>
            </div>
            <label className="podcast-demo-note-search">
              <Search aria-hidden="true" />
              <span className="sr-only">在笔记中搜索</span>
              <input placeholder="在笔记中搜索" />
            </label>
            <div className="podcast-demo-note-rows">
              {notes.map((note) => (
                <button
                  type="button"
                  key={note.id}
                  className={note.id === selectedNote.id ? 'is-selected' : ''}
                  aria-pressed={note.id === selectedNote.id}
                  onClick={() => setSelectedNoteID(note.id)}
                >
                  <span className="podcast-demo-note-row-heading">
                    <strong>{note.title}</strong>
                    <time>{note.updatedAt}</time>
                  </span>
                  <span className="podcast-demo-note-preview">
                    {note.preview}
                  </span>
                  <span className="podcast-demo-note-meta">
                    <em>{note.tag}</em>
                    <span>AI 学习</span>
                    {note.id === 'generated-ai-pm' ? <i>新生成</i> : null}
                  </span>
                </button>
              ))}
            </div>
          </section>

          <DemoNoteDetail note={selectedNote} />
        </section>
      </main>

      {drawerOpen ? (
        <aside className="podcast-demo-task-drawer" aria-label="导入任务">
          <div className="podcast-demo-drawer-heading">
            <div>
              <span>后台处理</span>
              <h2>导入任务</h2>
            </div>
            <button
              type="button"
              onClick={() => setDrawerOpen(false)}
              aria-label="关闭导入任务"
            >
              <PanelRightClose aria-hidden="true" />
            </button>
          </div>

          <div
            className="podcast-demo-import-task is-current"
            aria-live="polite"
          >
            <div className="podcast-demo-task-artwork">
              <Headphones aria-hidden="true" />
            </div>
            <div>
              <strong>AI 产品经理的下一站</strong>
              {task.status === 'processing' ? (
                <>
                  <span>
                    {task.summarize ? '正在整理笔记' : '正在生成逐字稿'} ·{' '}
                    {task.progress}%
                  </span>
                  <div className="podcast-demo-progress" aria-hidden="true">
                    <i style={{ width: `${task.progress}%` }} />
                  </div>
                  <small>
                    {getProgressCopy(task.progress, task.summarize)}
                  </small>
                </>
              ) : (
                <>
                  <span className="is-success">
                    {task.summarize ? '笔记已生成' : '逐字稿已生成'}
                  </span>
                  <button type="button" onClick={openGeneratedNote}>
                    {task.summarize ? '打开笔记' : '打开逐字稿'}
                    <ChevronRight aria-hidden="true" />
                  </button>
                </>
              )}
            </div>
            {task.status === 'processing' ? (
              <LoaderCircle className="is-spinning" aria-hidden="true" />
            ) : (
              <CheckCircle2 className="is-complete" aria-hidden="true" />
            )}
          </div>

          <div className="podcast-demo-import-task">
            <div className="podcast-demo-task-artwork is-purple">
              <Podcast aria-hidden="true" />
            </div>
            <div>
              <strong>如何建立长期知识系统</strong>
              <span className="is-success">笔记已生成</span>
            </div>
            <CheckCircle2 className="is-complete" aria-hidden="true" />
          </div>

          <button type="button" className="podcast-demo-all-tasks">
            查看全部任务
            <ChevronRight aria-hidden="true" />
          </button>
        </aside>
      ) : (
        <button
          type="button"
          className="podcast-demo-drawer-trigger"
          onClick={() => setDrawerOpen(true)}
        >
          <Podcast aria-hidden="true" />
          导入任务
          {task.status === 'processing' ? (
            <span>{task.progress}%</span>
          ) : (
            <Check aria-hidden="true" />
          )}
        </button>
      )}

      {modalOpen ? (
        <div className="podcast-demo-overlay" role="presentation">
          <section
            className="podcast-demo-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="podcast-import-title"
          >
            <div className="podcast-demo-modal-heading">
              <div>
                <span className="podcast-demo-modal-icon">
                  <Podcast aria-hidden="true" />
                </span>
                <div>
                  <h2 id="podcast-import-title">导入播客</h2>
                  <p>先生成逐字稿，再按需使用 AI 整理成笔记。</p>
                </div>
              </div>
              <button
                type="button"
                onClick={() => setModalOpen(false)}
                aria-label="关闭"
              >
                <X aria-hidden="true" />
              </button>
            </div>

            <div className="podcast-demo-modal-body">
              <div className="podcast-demo-field">
                <label htmlFor="podcast-url">单集链接</label>
                <div className="podcast-demo-url-input">
                  <Link2 aria-hidden="true" />
                  <input
                    id="podcast-url"
                    value={url}
                    onChange={(event) => handleUrlChange(event.target.value)}
                    placeholder="粘贴小宇宙或 Apple Podcasts 单集链接"
                  />
                  {url ? (
                    <button
                      type="button"
                      onClick={() => handleUrlChange('')}
                      aria-label="清空链接"
                    >
                      <X aria-hidden="true" />
                    </button>
                  ) : null}
                </div>
                <small>支持小宇宙、Apple Podcasts 与公开 RSS 单集链接</small>
              </div>

              {parsed ? (
                <EpisodePreview onReset={() => setParsed(false)} />
              ) : null}

              {parsed ? (
                <section
                  className="podcast-demo-processing-options"
                  aria-labelledby="podcast-processing-title"
                >
                  <div className="podcast-demo-processing-heading">
                    <div>
                      <h3 id="podcast-processing-title">处理方式</h3>
                      <p>转录和 AI 总结是两个独立步骤</p>
                    </div>
                    <span>预计 6–10 分钟</span>
                  </div>

                  <div className="podcast-demo-processing-step is-required">
                    <div className="podcast-demo-step-marker">
                      <span>1</span>
                      <i />
                    </div>
                    <div className="podcast-demo-step-content">
                      <div className="podcast-demo-step-title">
                        <span className="podcast-demo-step-icon">
                          <FileText aria-hidden="true" />
                        </span>
                        <div>
                          <strong>生成逐字稿</strong>
                          <small>语音转录</small>
                        </div>
                        <em>必需</em>
                      </div>
                      <p>优先使用节目已有逐字稿；没有时使用语音识别。</p>
                      {summarizeWithAI ? (
                        <label className="podcast-demo-inline-check">
                          <input
                            type="checkbox"
                            checked={attachTranscript}
                            onChange={(event) =>
                              setAttachTranscript(event.target.checked)
                            }
                          />
                          <span>
                            <Check aria-hidden="true" />
                          </span>
                          在笔记末尾附上完整逐字稿
                        </label>
                      ) : (
                        <div className="podcast-demo-transcript-note">
                          <CheckCircle2 aria-hidden="true" />
                          最终产物将保留说话人与时间戳
                        </div>
                      )}
                    </div>
                  </div>

                  <div
                    className={`podcast-demo-processing-step${
                      summarizeWithAI ? ' is-enabled' : ' is-disabled'
                    }`}
                  >
                    <div className="podcast-demo-step-marker">
                      <span>2</span>
                    </div>
                    <div className="podcast-demo-step-content">
                      <div className="podcast-demo-step-title">
                        <span className="podcast-demo-step-icon is-ai">
                          <Sparkles aria-hidden="true" />
                        </span>
                        <div>
                          <strong>AI 整理</strong>
                          <small>可选步骤</small>
                        </div>
                        <button
                          type="button"
                          role="switch"
                          aria-label="AI 整理"
                          aria-checked={summarizeWithAI}
                          className="podcast-demo-ai-switch"
                          onClick={() =>
                            setSummarizeWithAI((enabled) => !enabled)
                          }
                        >
                          <span />
                        </button>
                      </div>
                      <p>
                        {summarizeWithAI
                          ? '从逐字稿中提炼摘要、章节、关键观点与行动项。'
                          : '已关闭。只生成逐字稿，不调用 AI 总结模型。'}
                      </p>
                    </div>
                  </div>

                  <div
                    className="podcast-demo-output-summary"
                    aria-live="polite"
                  >
                    <span>{summarizeWithAI ? <Sparkles /> : <FileText />}</span>
                    <div>
                      <small>最终生成</small>
                      <strong>
                        {summarizeWithAI
                          ? '结构化播客笔记'
                          : '带时间戳的完整逐字稿'}
                      </strong>
                      <div>
                        {summarizeWithAI ? (
                          <>
                            <em>内容摘要</em>
                            <em>章节</em>
                            <em>关键观点</em>
                            <em>行动项</em>
                            {attachTranscript ? <em>完整逐字稿</em> : null}
                          </>
                        ) : (
                          <>
                            <em>说话人</em>
                            <em>时间戳</em>
                          </>
                        )}
                      </div>
                    </div>
                    <p className={summarizeWithAI ? 'uses-ai' : 'no-ai'}>
                      {summarizeWithAI ? '会调用 AI 总结' : '不调用 AI 总结'}
                    </p>
                  </div>
                </section>
              ) : (
                <div className="podcast-demo-link-empty">
                  <Podcast aria-hidden="true" />
                  <strong>先解析链接</strong>
                  <span>我们会读取节目名称、封面、时长与公开音频地址。</span>
                </div>
              )}

              {parsed ? (
                <>
                  <div className="podcast-demo-destination-grid">
                    <label>
                      <span>保存到</span>
                      <button type="button">
                        未归属笔记 <ChevronDown aria-hidden="true" />
                      </button>
                    </label>
                    <label>
                      <span>关联项目</span>
                      <select
                        aria-label="关联项目"
                        value={projectID}
                        onChange={(event) => setProjectID(event.target.value)}
                      >
                        <option value="">未归属</option>
                        {DEMO_PROJECTS.map((project) => (
                          <option key={project.id} value={project.id}>
                            {project.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  </div>
                  <div className="podcast-demo-tag-field">
                    <span>标签</span>
                    <div>
                      <em>
                        播客 <X aria-hidden="true" />
                      </em>
                      <em>
                        AI <X aria-hidden="true" />
                      </em>
                      <small>输入标签，按回车添加</small>
                    </div>
                  </div>
                  <div className="podcast-demo-checks">
                    <label>
                      <input type="checkbox" defaultChecked />
                      <span>
                        <Check aria-hidden="true" />
                      </span>
                      自动识别语言
                    </label>
                    <label>
                      <input
                        type="checkbox"
                        checked={keepAudio}
                        onChange={(event) => setKeepAudio(event.target.checked)}
                      />
                      <span>
                        <Check aria-hidden="true" />
                      </span>
                      保留原始音频
                    </label>
                  </div>
                </>
              ) : null}
            </div>

            <footer className="podcast-demo-modal-footer">
              <p>
                <CheckCircle2 aria-hidden="true" />
                任务会在后台继续，不影响使用其他功能
              </p>
              <div>
                <button
                  type="button"
                  className="podcast-demo-button podcast-demo-button-quiet"
                  onClick={() => setModalOpen(false)}
                >
                  取消
                </button>
                <button
                  type="button"
                  className="podcast-demo-button podcast-demo-button-primary"
                  onClick={handlePrimaryAction}
                  disabled={!url.trim()}
                >
                  {parsed ? (
                    summarizeWithAI ? (
                      <Sparkles aria-hidden="true" />
                    ) : (
                      <FileText aria-hidden="true" />
                    )
                  ) : (
                    <Link2 aria-hidden="true" />
                  )}
                  {parsed
                    ? summarizeWithAI
                      ? '开始生成笔记'
                      : '开始转写'
                    : '解析链接'}
                </button>
              </div>
            </footer>
          </section>
        </div>
      ) : null}
    </div>
  )
}

function DemoSidebar() {
  const nav = [
    { label: '收件箱', icon: Inbox },
    { label: '笔记', icon: Notebook, active: true },
    { label: '任务', icon: CheckSquare },
    { label: '日程', icon: CalendarDays },
    { label: '项目', icon: Folder },
  ]

  return (
    <aside className="podcast-demo-sidebar">
      <div className="podcast-demo-brand">
        <span>F</span>
        <strong>FlowSpace</strong>
      </div>
      <nav aria-label="原型主导航">
        {nav.map(({ label, icon: Icon, active }) => (
          <button
            type="button"
            key={label}
            className={active ? 'is-active' : ''}
          >
            <Icon aria-hidden="true" />
            {label}
          </button>
        ))}
        <i />
        <button type="button">
          <Settings aria-hidden="true" />
          设置
        </button>
      </nav>
      <div className="podcast-demo-storage">
        <span>个人空间</span>
        <i>
          <b />
        </i>
        <small>28.6 GB / 100 GB</small>
      </div>
    </aside>
  )
}

function DemoFilters() {
  const filters = [
    { label: '全部笔记', count: 286, icon: Notebook, active: true },
    { label: '最近编辑', count: 20, icon: Clock },
    { label: '我的收藏', count: 35, icon: Star },
  ]
  const tags = [
    ['AI', 48],
    ['产品', 42],
    ['播客', 31],
    ['读书', 28],
    ['方法论', 24],
  ]

  return (
    <aside className="podcast-demo-filters">
      <div className="podcast-demo-filter-title">
        <strong>筛选</strong>
        <SlidersHorizontal aria-hidden="true" />
      </div>
      <div className="podcast-demo-filter-group">
        {filters.map(({ label, count, icon: Icon, active }) => (
          <button
            type="button"
            key={label}
            className={active ? 'is-active' : ''}
          >
            <span>
              <Icon aria-hidden="true" />
              {label}
            </span>
            <em>{count}</em>
          </button>
        ))}
      </div>
      <div className="podcast-demo-filter-group">
        <h2>
          标签 <Plus aria-hidden="true" />
        </h2>
        {tags.map(([tag, count]) => (
          <button type="button" key={tag}>
            <span>
              <Hash aria-hidden="true" />
              {tag}
            </span>
            <em>{count}</em>
          </button>
        ))}
      </div>
    </aside>
  )
}

function DemoNoteDetail({ note }: { note: DemoNote }) {
  return (
    <article className="podcast-demo-detail">
      <header>
        <div>
          <span>笔记预览</span>
          <h2>{note.title}</h2>
          <p>今天 10:32 · 约 6 分钟阅读</p>
        </div>
        <div>
          <button type="button" aria-label="收藏">
            <Star aria-hidden="true" />
          </button>
          <button type="button" aria-label="分享">
            <Share2 aria-hidden="true" />
          </button>
          <button type="button" aria-label="更多">
            <MoreHorizontal aria-hidden="true" />
          </button>
        </div>
      </header>
      {note.id === 'generated-ai-pm' ? (
        <div className="podcast-demo-generated-body">
          <span className="podcast-demo-source-line">
            <Podcast aria-hidden="true" />
            来自小宇宙 · 58 分钟
          </span>
          <h3>内容摘要</h3>
          <p>
            AI
            正在把产品经理从大量执行工作中释放出来。真正重要的能力，变成判断什么问题值得解决，以及如何连接用户、技术与商业。
          </p>
          <h3>关键观点</h3>
          <ul>
            <li>从“定义需求”转向“探索问题”。</li>
            <li>AI 扩大可能性边界，判断力决定方向。</li>
            <li>产品经理需要成为跨角色的连接器。</li>
          </ul>
        </div>
      ) : note.id === 'generated-transcript' ? (
        <div className="podcast-demo-generated-body">
          <span className="podcast-demo-source-line">
            <FileText aria-hidden="true" />
            完整逐字稿 · 58 分钟
          </span>
          <h3>00:00 · 开场</h3>
          <p>
            <strong>主持人：</strong>今天我们来聊一聊，AI
            会怎样改变产品经理的工作。
          </p>
          <h3>01:26 · 产品经理的变化</h3>
          <p>
            <strong>嘉宾：</strong>
            我觉得最明显的变化，是执行工作的比例会大幅下降。
          </p>
        </div>
      ) : (
        <div>
          <p>{note.preview}</p>
          <h3>核心观点</h3>
          <ul>
            <li>真正的价值来自发现值得解决的问题。</li>
            <li>工具会变化，理解用户的能力不会过时。</li>
            <li>好的知识需要进入行动与复盘。</li>
          </ul>
        </div>
      )}
      <footer>
        <span>
          <FileText aria-hidden="true" />
          {note.tag}
        </span>
        <button type="button">编辑笔记</button>
      </footer>
    </article>
  )
}

function EpisodePreview({ onReset }: { onReset: () => void }) {
  return (
    <article className="podcast-demo-episode-preview">
      <div className="podcast-demo-episode-artwork">
        <span />
        <Headphones aria-hidden="true" />
        <small>小宇宙</small>
      </div>
      <div>
        <span>已识别 · 小宇宙</span>
        <strong>AI 产品经理的下一站</strong>
        <small>42章经 · 58 分钟 · 中文</small>
      </div>
      <button type="button" onClick={onReset}>
        重新解析
      </button>
    </article>
  )
}

function getProgressCopy(progress: number, summarize: boolean) {
  if (progress < 30) return '正在获取节目音频'
  if (progress < 65) return '正在识别说话内容'
  if (!summarize && progress < 88) return '正在整理说话人与时间戳'
  if (!summarize) return '正在保存完整逐字稿'
  if (progress < 88) return '正在提炼章节与观点'
  return '正在保存到笔记库'
}
