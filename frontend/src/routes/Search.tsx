import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import type { SearchResult } from '../api/search'
import { useSearch } from '../hooks/useSearch'

type SearchType = 'all' | 'note' | 'task' | 'event' | 'project'

const recentSearchesKey = 'flowspace.recent-searches'
const typeTabs: { value: SearchType; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'note', label: '笔记' },
  { value: 'task', label: '任务' },
  { value: 'event', label: '日程' },
  { value: 'project', label: '项目' },
]
const typeLabels: Record<string, string> = { note: '笔记', task: '任务', event: '日程', project: '项目' }
const typeSymbols: Record<string, string> = { note: 'N', task: 'T', event: 'E', project: 'P' }
const recommendations = [
  { symbol: 'T', title: '待处理任务', hint: '聚焦需要推进的事项', query: '待处理' },
  { symbol: 'E', title: '本周日程', hint: '查看最近的时间安排', query: '本周' },
  { symbol: 'N', title: '最近更新笔记', hint: '继续整理近期内容', query: '最近更新' },
  { symbol: 'P', title: '学习项目', hint: '找回学习任务与资料', query: '学习' },
]

export default function Search() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const query = params.get('q') || ''
  const [input, setInput] = useState(query)
  const [activeType, setActiveType] = useState<SearchType>('all')
  const [recentSearches, setRecentSearches] = useState<string[]>(readRecentSearches)
  const searchQuery = useSearch(query)

  useEffect(() => setInput(query), [query])

  useEffect(() => {
    if (input.trim() === query) return
    const timer = window.setTimeout(() => {
      const nextQuery = input.trim()
      setParams(nextQuery ? { q: nextQuery } : {})
    }, 300)
    return () => window.clearTimeout(timer)
  }, [input, query, setParams])

  useEffect(() => {
    const keyword = query.trim()
    if (!keyword || searchQuery.isFetching) return
    setRecentSearches((current) => {
      const next = [keyword, ...current.filter((item) => item !== keyword)].slice(0, 6)
      writeRecentSearches(next)
      return next
    })
  }, [query, searchQuery.isFetching])

  const allResults = searchQuery.data?.items ?? []
  const visibleResults = useMemo(
    () => activeType === 'all' ? allResults : allResults.filter((item) => item.type === activeType),
    [activeType, allResults],
  )
  const counts = useMemo(() => {
    return allResults.reduce<Record<string, number>>((result, item) => {
      result[item.type] = (result[item.type] ?? 0) + 1
      return result
    }, {})
  }, [allResults])
  const isSearching = searchQuery.isLoading || searchQuery.isFetching

  function applySearch(keyword: string) {
    setInput(keyword)
    setParams(keyword.trim() ? { q: keyword.trim() } : {})
  }

  function clearHistory() {
    setRecentSearches([])
    writeRecentSearches([])
  }

  function openResult(item: SearchResult) {
    if (item.type === 'note') navigate(`/editor/${encodeURIComponent(item.id)}`)
    else if (item.type === 'event') navigate('/calendar')
    else navigate('/tasks')
  }

  return (
    <div className="search-canvas">
      <section className="surface-panel search-command-panel">
        <div className="search-command">
          <span aria-hidden="true">⌕</span>
          <input
            value={input}
            onChange={(event) => setInput(event.target.value)}
            placeholder="搜索笔记、任务、日程、项目..."
            aria-label="全局搜索"
            autoFocus
          />
          <kbd>⌘K</kbd>
          {isSearching && <div className="search-spinner" aria-label="正在搜索" />}
        </div>

        <div className="segmented-tabs search-tabs" role="tablist" aria-label="结果类型">
          {typeTabs.map((tab) => (
            <button
              key={tab.value}
              type="button"
              role="tab"
              aria-label={tab.label}
              aria-selected={activeType === tab.value}
              className={activeType === tab.value ? 'is-active' : ''}
              onClick={() => setActiveType(tab.value)}
            >
              {tab.label}
              {query && tab.value !== 'all' && <span>{counts[tab.value] ?? 0}</span>}
            </button>
          ))}
        </div>

        <div className="search-workspace">
          <aside className="search-discovery">
            <section className="search-section">
              <div className="section-line-heading">
                <div>
                  <span className="section-eyebrow">快捷入口</span>
                  <h2>推荐搜索</h2>
                </div>
              </div>
              <div className="recommend-list">
                {recommendations.map((item) => (
                  <button key={item.title} type="button" onClick={() => applySearch(item.query)}>
                    <span aria-hidden="true">{item.symbol}</span>
                    <strong>{item.title}</strong>
                    <em>{item.hint}</em>
                  </button>
                ))}
              </div>
            </section>

            <section className="search-section recent-searches">
              <div className="section-line-heading">
                <h2>最近搜索</h2>
                {recentSearches.length > 0 && <button type="button" onClick={clearHistory}>清除历史</button>}
              </div>
              {recentSearches.length === 0 ? (
                <p className="search-aside-empty">搜索记录会保存在当前浏览器。</p>
              ) : (
                <div className="recent-search-list">
                  {recentSearches.map((item) => (
                    <button key={item} type="button" aria-label={item} onClick={() => applySearch(item)}>
                      <span aria-hidden="true">↗</span>
                      {item}
                    </button>
                  ))}
                </div>
              )}
            </section>
          </aside>

          <section className="search-results-preview" aria-live="polite">
            <div className="search-results-heading">
              <div>
                <span className="section-eyebrow">工作区内容</span>
                <h2>{query ? `“${query}”的搜索结果` : '搜索结果'}</h2>
              </div>
              {query && <span>{visibleResults.length} 项</span>}
            </div>

            {!query.trim() && (
              <div className="search-empty-state">
                <strong>从一个关键词开始</strong>
                <p>可以查找笔记正文、任务标题、日程内容和项目名称。</p>
              </div>
            )}
            {query.trim() && visibleResults.length === 0 && !isSearching && (
              <div className="search-empty-state">
                <strong>没有匹配内容</strong>
                <p>换一个关键词，或切换到“全部”查看其他类型。</p>
              </div>
            )}
            {visibleResults.length > 0 && (
              <div className="search-result-list">
                {visibleResults.map((item) => (
                  <button key={`${item.type}-${item.id}`} type="button" className="search-result-row" aria-label={`打开${typeLabels[item.type] || ''}${item.title}`} onClick={() => openResult(item)}>
                    <span aria-hidden="true">{typeSymbols[item.type] ?? '·'}</span>
                    <div>
                      <strong><HighlightedText text={item.title} query={query} /></strong>
                      <p>
                        {typeLabels[item.type] || item.type}
                        {item.done !== undefined && ` · ${item.done ? '已完成' : '未完成'}`}
                        {item.updated_at > 0 && ` · ${formatDate(item.updated_at)}`}
                      </p>
                    </div>
                    <i aria-hidden="true">›</i>
                  </button>
                ))}
              </div>
            )}
          </section>
        </div>
      </section>
    </div>
  )
}

function HighlightedText({ text, query }: { text: string; query: string }) {
  const keyword = query.trim()
  const index = keyword ? text.toLocaleLowerCase().indexOf(keyword.toLocaleLowerCase()) : -1
  if (index < 0) return text
  return <>{text.slice(0, index)}<mark>{text.slice(index, index + keyword.length)}</mark>{text.slice(index + keyword.length)}</>
}

function readRecentSearches() {
  try {
    const stored = JSON.parse(window.localStorage.getItem(recentSearchesKey) || '[]')
    return Array.isArray(stored) ? stored.filter((item): item is string => typeof item === 'string').slice(0, 6) : []
  } catch {
    return []
  }
}

function writeRecentSearches(items: string[]) {
  window.localStorage.setItem(recentSearchesKey, JSON.stringify(items))
}

function formatDate(timestamp: number) {
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'short', day: 'numeric' }).format(timestamp * 1000)
}
