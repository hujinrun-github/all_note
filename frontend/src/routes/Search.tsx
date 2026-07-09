import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useSearch } from '../hooks/useSearch'
import type { SearchResult } from '../api/search'

const typeLabels: Record<string, string> = { note: '笔记', task: '任务', event: '日程', project: '项目' }

const recommendations = [
  { icon: '!', title: '逾期任务', hint: '你有 1 个逾期任务', q: 'kapathy' },
  { icon: '◇', title: '本周日程', hint: '本周暂无日程', q: '本周日程' },
  { icon: '▣', title: '最近更新笔记', hint: '最近更新 1 篇', q: '第一篇笔记' },
  { icon: 'N', title: '日语N2相关内容', hint: '查找学习任务', q: 'N2 语法' },
]

export default function Search() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') || ''
  const [input, setInput] = useState(q)

  useEffect(() => {
    setInput(q)
  }, [q])

  useEffect(() => {
    if (input.trim() === q) return
    const timer = window.setTimeout(() => {
      if (input.trim()) {
        setParams({ q: input.trim() })
      } else {
        setParams({})
      }
    }, 300)
    return () => window.clearTimeout(timer)
  }, [input, q, setParams])

  const { data, isLoading, isFetching } = useSearch(q)
  const grouped: Record<string, SearchResult[]> = {}
  data?.items?.forEach((item) => {
    grouped[item.type] = [...(grouped[item.type] ?? []), item]
  })

  const totalResults = data?.items?.length ?? 0
  const isSearching = isLoading || isFetching
  const hasResults = Boolean(data && q.trim() && totalResults > 0)

  return (
    <div className="search-canvas">
      <section className="surface-panel search-command-panel">
        <div className="search-command">
          <input
            value={input}
            onChange={(event) => setInput(event.target.value)}
            placeholder="搜索笔记、任务、日程、项目..."
            autoFocus
          />
          <kbd>⌘K</kbd>
          {isSearching && <div className="search-spinner" />}
        </div>

        <div className="segmented-tabs search-tabs">
          <button className="is-active">全部</button>
          <button>笔记</button>
          <button>任务</button>
          <button>日程</button>
          <button>项目</button>
        </div>

        <section className="search-section">
          <h2>推荐搜索</h2>
          <div className="recommend-grid">
            {recommendations.map((item) => (
              <button key={item.title} type="button" onClick={() => setInput(item.q)}>
                <span>{item.icon}</span>
                <strong>{item.title}</strong>
                <em>{item.hint}</em>
              </button>
            ))}
          </div>
        </section>

        <section className="search-section">
          <div className="section-line-heading">
            <h2>最近搜索</h2>
            <button type="button">清除历史</button>
          </div>
          <article className="recent-search-card">
            <strong>kapathy</strong>
            <p>任务 <em>1 个结果</em></p>
          </article>
          <article className="recent-search-card">
            <strong>N2 语法</strong>
            <p>项目 日语N2考级</p>
          </article>
        </section>

        <section className="search-section search-results-preview">
          <h2>搜索结果预览</h2>
          {!q.trim() && <p className="empty-copy">输入关键词开始搜索，可以搜索笔记正文、任务标题和日程地点。</p>}
          {q.trim() && totalResults === 0 && !isSearching && <p className="empty-copy">未找到「{q}」相关结果。</p>}
          {hasResults &&
            Object.entries(grouped).map(([type, items]) => (
              <div key={type} className="result-group">
                {items.map((item) => (
                  <article key={item.id} className="search-result-row">
                    <span>{type === 'task' ? '✓' : type === 'event' ? '◇' : '▣'}</span>
                    <div>
                      <strong dangerouslySetInnerHTML={{ __html: item.highlight || item.title }} />
                      <p>
                        {typeLabels[item.type] || item.type}
                        {item.folder_id && item.folder_id !== '__uncategorized' && ` · ${item.folder_id.replace('__', '')}`}
                        {item.done !== undefined && ` · ${item.done ? '已完成' : '未完成'}`}
                        {` · ${new Date(item.updated_at * 1000).toLocaleDateString('zh-CN')}`}
                      </p>
                    </div>
                  </article>
                ))}
              </div>
            ))}
        </section>
      </section>
    </div>
  )
}
