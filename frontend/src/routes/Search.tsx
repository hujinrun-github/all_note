import { useState, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useSearch } from '../hooks/useSearch'
import type { SearchResult } from '../api/search'

const typeLabels: Record<string, string> = { note: '笔记', task: '任务', event: '日程' }

export default function Search() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') || ''
  const [input, setInput] = useState(q)

  useEffect(() => {
    const timer = setTimeout(() => {
      if (input.trim()) {
        setParams({ q: input.trim() })
      } else {
        setParams({})
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [input, setParams])

  const { data, isLoading } = useSearch(q)

  const grouped: Record<string, SearchResult[]> = {}
  data?.items?.forEach((item) => {
    if (!grouped[item.type]) grouped[item.type] = []
    grouped[item.type].push(item)
  })

  return (
    <div className="grid gap-5 max-w-[720px]">
      <input
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder="搜索笔记、任务、日程..."
        className="w-full border border-fs-border rounded-md px-4 py-3 text-[15px] outline-none focus:border-fs-accent transition-colors font-sans"
        autoFocus
      />

      {isLoading && (
        <div className="grid gap-2">{Array.from({ length: 5 }).map((_, i) => <div key={i} className="h-12 bg-fs-hover rounded-md animate-pulse" />)}</div>
      )}

      {!q.trim() && (
        <p className="text-fs-text-muted text-sm text-center py-8">输入关键词开始搜索</p>
      )}

      {data && q.trim() && data.items.length === 0 && (
        <p className="text-fs-text-muted text-sm text-center py-8">未找到"{q}"相关结果</p>
      )}

      {data && q.trim() && Object.entries(grouped).map(([type, items]) => (
        <div key={type}>
          <div className="text-fs-text-muted text-[11px] font-semibold uppercase tracking-wider mb-2">
            {typeLabels[type] || type}
          </div>
          <div className="grid gap-1">
            {items.map((item) => (
              <div key={item.id} className="px-3 py-2.5 rounded-md hover:bg-fs-hover cursor-pointer transition-colors">
                <strong className="text-sm font-medium" dangerouslySetInnerHTML={{ __html: item.highlight || item.title }} />
                <div className="text-fs-text-muted text-xs mt-1 flex gap-2">
                  <span>{typeLabels[item.type] || item.type}</span>
                  {item.folder_id && <span>· {item.folder_id.replace('__', '')}</span>}
                  {item.done !== undefined && <span>· {item.done ? '已完成' : '未完成'}</span>}
                  <span>· {new Date(item.updated_at * 1000).toLocaleDateString('zh-CN')}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}
