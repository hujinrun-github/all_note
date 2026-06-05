import { useState, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useSearch } from '../hooks/useSearch'
import type { SearchResult } from '../api/search'

const typeLabels: Record<string, string> = { note: '笔记', task: '任务', event: '日程' }
const typeIcons: Record<string, string> = { note: '📄', task: '✅', event: '📅' }

export default function Search() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') || ''
  const [input, setInput] = useState(q)

  // Sync input when URL param changes externally (e.g., browser back/forward)
  useEffect(() => {
    setInput(q)
  }, [q])

  // Debounced URL update on input change
  useEffect(() => {
    if (input.trim() === q) return // already in sync, skip
    const timer = setTimeout(() => {
      if (input.trim()) {
        setParams({ q: input.trim() })
      } else {
        setParams({})
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [input])

  const { data, isLoading, isFetching } = useSearch(q)

  const grouped: Record<string, SearchResult[]> = {}
  data?.items?.forEach((item) => {
    if (!grouped[item.type]) grouped[item.type] = []
    grouped[item.type].push(item)
  })

  const totalResults = data?.items?.length ?? 0
  const isTyping = input.trim() && !q.trim() // user has typed but debounce hasn't fired yet
  const isSearching = isLoading || isFetching
  const hasResults = data && q.trim() && totalResults > 0
  const hasNoResults = data && q.trim() && totalResults === 0

  return (
    <div className="search-workspace">
      <aside className="filter-rail">
        <div className="filter-title">搜索范围</div>
        <button className="is-active">全部</button>
        <button>笔记</button>
        <button>任务</button>
        <button>日程</button>
        <div className="rail-summary">
          <span>匹配结果</span>
          <strong>{totalResults}</strong>
          <p>按最近更新时间排序</p>
        </div>
      </aside>

      <section className="surface-panel list-panel">
        <div className="search-command">
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="搜索笔记、任务、日程..."
            autoFocus
          />
          {isSearching && <div className="search-spinner" />}
        </div>

      {/* Debounce pending — user typing, waiting for URL update */}
      {isTyping && (
        <div className="text-center py-12">
          <p className="text-fs-text-muted text-sm animate-pulse">...</p>
        </div>
      )}

      {/* Initial empty state — nothing typed */}
      {!input.trim() && !q.trim() && (
        <div className="empty-state">
          <strong>输入关键词开始搜索</strong>
          <p>可以搜索笔记正文、任务标题和日程地点。</p>
        </div>
      )}

      {/* Loading skeleton */}
      {isSearching && q.trim() && (
        <div className="list-rows">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="h-14 bg-fs-hover rounded-lg animate-pulse" />
          ))}
        </div>
      )}

      {/* No results */}
      {hasNoResults && !isSearching && (
        <div className="empty-state">
          <strong>未找到「{q}」相关结果</strong>
          <p>换一个关键词，或者检查是否已经归档。</p>
        </div>
      )}

      {/* Results */}
      {hasResults && Object.entries(grouped).map(([type, items]) => (
        <div key={type} className="result-group">
          <div className="group-label">
            <span>{typeIcons[type] || '•'}</span>
            <strong>
              {typeLabels[type] || type}
            </strong>
            <em>{items.length}</em>
          </div>
          <div className="list-rows">
            {items.map((item) => (
              <div
                key={item.id}
                className="rich-row"
              >
                <strong className="text-sm font-medium text-fs-text" dangerouslySetInnerHTML={{ __html: item.highlight || item.title }} />
                <div className="text-fs-text-muted text-xs mt-1.5 flex gap-2 items-center">
                  <span>{typeLabels[item.type] || item.type}</span>
                  {item.folder_id && item.folder_id !== '__uncategorized' && <span>· {item.folder_id.replace('__', '')}</span>}
                  {item.done !== undefined && <span>· {item.done ? '已完成' : '未完成'}</span>}
                  <span>· {new Date(item.updated_at * 1000).toLocaleDateString('zh-CN')}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      ))}

      {/* Results summary */}
      {hasResults && (
        <p className="text-fs-text-muted text-xs text-center pt-2">
          共找到 {totalResults} 条结果
        </p>
      )}
      </section>
    </div>
  )
}
