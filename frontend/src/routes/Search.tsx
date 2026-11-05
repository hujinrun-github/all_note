import {
  ArrowRight,
  CalendarDays,
  CircleAlert,
  FileText,
  Folder,
  ListTodo,
  Search as SearchIcon,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import type { SearchResult } from '../api/search'
import {
  PlanningHeading,
  PlanningLoading,
  PlanningTabs,
} from '../components/planning/PlanningWorkspace'
import { Alert, AlertTitle } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '../components/ui/empty'
import { Input } from '../components/ui/input'
import { useSearch } from '../hooks/useSearch'
import { useProjects, useTaskDomainCapabilities } from '../hooks/useTaskDomain'

type SearchType = 'all' | 'note' | 'task' | 'event' | 'project'

const recentSearchesKey = 'flowspace.recent-searches'
const typeTabs: { value: SearchType; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'note', label: '笔记' },
  { value: 'task', label: '任务' },
  { value: 'event', label: '日程' },
  { value: 'project', label: '项目' },
]
const typeLabels: Record<string, string> = {
  note: '笔记',
  task: '任务',
  event: '日程',
  project: '项目',
}
const typeIcons = {
  note: FileText,
  task: ListTodo,
  event: CalendarDays,
  project: Folder,
}
const recommendations = [
  {
    icon: ListTodo,
    title: '待处理任务',
    hint: '聚焦需要推进的事项',
    query: '待处理',
  },
  {
    icon: CalendarDays,
    title: '本周日程',
    hint: '查看最近的时间安排',
    query: '本周',
  },
  {
    icon: FileText,
    title: '最近更新笔记',
    hint: '继续整理近期内容',
    query: '最近更新',
  },
  {
    icon: Folder,
    title: '学习项目',
    hint: '找回学习任务与资料',
    query: '学习',
  },
]

export default function Search() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const query = params.get('q') || ''
  const [input, setInput] = useState(query)
  const [activeType, setActiveType] = useState<SearchType>('all')
  const [recentSearches, setRecentSearches] =
    useState<string[]>(readRecentSearches)
  const searchQuery = useSearch(query)
  const capabilities = useTaskDomainCapabilities()
  const projectSearchEnabled = Boolean(
    query.trim() &&
    capabilities.data?.available &&
    capabilities.data.model_version === 'v2'
  )
  const projectsQuery = useProjects({}, { enabled: projectSearchEnabled })

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
      const next = [
        keyword,
        ...current.filter((item) => item !== keyword),
      ].slice(0, 6)
      writeRecentSearches(next)
      return next
    })
  }, [query, searchQuery.isFetching])

  const allResults = useMemo(() => {
    const items = searchQuery.data?.items ?? []
    if (!projectSearchEnabled) return items
    const keyword = query.trim().toLocaleLowerCase()
    const indexedProjectIDs = new Set(
      items.filter(({ type }) => type === 'project').map(({ id }) => id)
    )
    const matchingProjects: SearchResult[] = (projectsQuery.data ?? [])
      .filter(
        ({ id, name }) =>
          !indexedProjectIDs.has(id) &&
          name.toLocaleLowerCase().includes(keyword)
      )
      .map(({ id, name }) => ({
        type: 'project',
        id,
        title: name,
        highlight: '',
        updated_at: 0,
      }))
    return [...items, ...matchingProjects]
  }, [projectSearchEnabled, projectsQuery.data, query, searchQuery.data?.items])
  const visibleResults = useMemo(
    () =>
      activeType === 'all'
        ? allResults
        : allResults.filter((item) => item.type === activeType),
    [activeType, allResults]
  )
  const counts = useMemo(() => {
    return allResults.reduce<Record<string, number>>((result, item) => {
      result[item.type] = (result[item.type] ?? 0) + 1
      return result
    }, {})
  }, [allResults])
  const isSearching =
    searchQuery.isFetching || (projectSearchEnabled && projectsQuery.isFetching)
  const searchFailed =
    searchQuery.isError || (projectSearchEnabled && projectsQuery.isError)

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
    else if (item.type === 'project')
      navigate(`/projects/${encodeURIComponent(item.id)}`)
    else navigate('/tasks')
  }

  return (
    <section
      className="planning-page"
      aria-labelledby="workspace-search-heading"
    >
      <PlanningHeading
        id="workspace-search-heading"
        title="全局搜索"
        description="按关键词查找笔记、任务、日程和项目。"
      />
      <div className="grid min-w-0 items-start gap-4 min-[1060px]:grid-cols-[minmax(0,1fr)_minmax(250px,0.42fr)]">
        <Card className="min-w-0 gap-0 py-0 shadow-none">
          <form
            role="search"
            className="flex min-w-0 items-center gap-2 p-4 sm:gap-3"
            onSubmit={(event) => {
              event.preventDefault()
              applySearch(input)
            }}
          >
            <div className="relative min-w-0 flex-1">
              <SearchIcon
                aria-hidden="true"
                className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
              />
              <Input
                type="search"
                autoFocus
                value={input}
                onChange={(event) => setInput(event.target.value)}
                placeholder="搜索笔记、任务、日程、项目..."
                aria-label="全局搜索"
                className="h-11 pl-9"
              />
            </div>
            <Button type="submit" className="min-h-11">
              搜索
            </Button>
          </form>

          <PlanningTabs
            label="结果类型"
            value={activeType}
            tabs={typeTabs.map(({ value: id, label }) => ({
              id,
              label,
              accessibleLabel: label,
              count: query && id !== 'all' ? (counts[id] ?? 0) : undefined,
            }))}
            onChange={setActiveType}
          >
            <section
              className="min-w-0 p-4"
              aria-live="polite"
              aria-busy={isSearching}
            >
              <div className="flex items-start justify-between gap-2 py-1">
                <h2 className="min-w-0 text-base font-semibold break-words">
                  {query ? `“${query}”的搜索结果` : '搜索结果'}
                </h2>
                {query && !isSearching && !searchFailed ? (
                  <Badge variant="secondary" className="shrink-0">
                    {visibleResults.length} 项
                  </Badge>
                ) : null}
              </div>
              {isSearching ? <PlanningLoading label="正在搜索" /> : null}
              {searchFailed ? (
                <Alert variant="destructive" className="mt-3">
                  <CircleAlert aria-hidden="true" />
                  <AlertTitle>搜索暂时不可用，请稍后重试。</AlertTitle>
                </Alert>
              ) : null}
              {!query ||
              (!isSearching && !searchFailed && !visibleResults.length) ? (
                <Empty className="planning-empty">
                  <EmptyHeader>
                    <EmptyMedia variant="icon">
                      <SearchIcon aria-hidden="true" />
                    </EmptyMedia>
                    <EmptyTitle>
                      {query ? '没有匹配内容' : '从一个关键词开始'}
                    </EmptyTitle>
                    <EmptyDescription>
                      {query
                        ? '换一个关键词，或切换到“全部”查看其他类型。'
                        : '可以查找笔记正文、任务标题、日程内容和项目名称。'}
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              ) : null}
              {query && visibleResults.length > 0 ? (
                <ul className="mt-3 divide-y divide-border">
                  {visibleResults.map((item) => {
                    const Icon =
                      typeIcons[item.type as keyof typeof typeIcons] ??
                      SearchIcon
                    return (
                      <li key={`${item.type}-${item.id}`}>
                        <Button
                          type="button"
                          variant="ghost"
                          className="h-auto min-h-14 w-full justify-start gap-3 py-2 text-left whitespace-normal"
                          aria-label={`打开${typeLabels[item.type] || ''}${item.title}`}
                          onClick={() => openResult(item)}
                        >
                          <span className="grid size-8 shrink-0 place-items-center rounded-md bg-accent text-primary">
                            <Icon aria-hidden="true" />
                          </span>
                          <span className="grid min-w-0 flex-1 gap-1">
                            <strong className="break-words font-semibold">
                              <HighlightedText
                                text={item.title}
                                query={query}
                              />
                            </strong>
                            <span className="text-xs font-normal text-muted-foreground">
                              {typeLabels[item.type] || item.type}
                              {item.done !== undefined &&
                                ` · ${item.done ? '已完成' : '未完成'}`}
                              {item.updated_at > 0 &&
                                ` · ${formatDate(item.updated_at)}`}
                            </span>
                          </span>
                          <ArrowRight
                            className="text-muted-foreground"
                            aria-hidden="true"
                          />
                        </Button>
                      </li>
                    )
                  })}
                </ul>
              ) : null}
            </section>
          </PlanningTabs>
        </Card>

        <aside className="grid min-w-0 gap-4" aria-label="搜索快捷入口">
          <Card className="gap-0 py-0 shadow-none">
            <CardHeader className="px-4 py-4">
              <CardTitle>
                <h2>推荐搜索</h2>
              </CardTitle>
            </CardHeader>
            <CardContent className="grid gap-1 px-3 pb-3">
              {recommendations.map(({ title, hint, icon: Icon, query }) => (
                <Button
                  key={title}
                  type="button"
                  variant="ghost"
                  className="h-auto min-h-12 justify-start py-2 text-left whitespace-normal"
                  onClick={() => applySearch(query)}
                >
                  <span className="grid size-8 shrink-0 place-items-center rounded-md bg-accent text-primary">
                    <Icon aria-hidden="true" />
                  </span>
                  <span className="grid min-w-0 gap-0.5">
                    <strong className="font-medium">{title}</strong>
                    <span className="text-xs font-normal text-muted-foreground">
                      {hint}
                    </span>
                  </span>
                </Button>
              ))}
            </CardContent>
          </Card>
          <Card className="gap-0 py-0 shadow-none">
            <CardHeader className="flex items-center justify-between gap-2 px-4 py-3">
              <CardTitle>
                <h2>最近搜索</h2>
              </CardTitle>
              {recentSearches.length ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={clearHistory}
                >
                  清除历史
                </Button>
              ) : null}
            </CardHeader>
            <CardContent className="flex flex-wrap gap-2 px-4 pb-4">
              {recentSearches.length ? (
                recentSearches.map((item) => (
                  <Button
                    key={item}
                    type="button"
                    variant="outline"
                    size="sm"
                    className="max-w-full min-w-0"
                    aria-label={item}
                    onClick={() => applySearch(item)}
                  >
                    <span className="truncate">{item}</span>
                  </Button>
                ))
              ) : (
                <p className="text-sm text-muted-foreground">
                  搜索记录会保存在当前浏览器。
                </p>
              )}
            </CardContent>
          </Card>
        </aside>
      </div>
    </section>
  )
}

function HighlightedText({ text, query }: { text: string; query: string }) {
  const keyword = query.trim()
  const index = keyword
    ? text.toLocaleLowerCase().indexOf(keyword.toLocaleLowerCase())
    : -1
  if (index < 0) return text
  return (
    <>
      {text.slice(0, index)}
      <mark className="rounded-sm bg-accent text-accent-foreground">
        {text.slice(index, index + keyword.length)}
      </mark>
      {text.slice(index + keyword.length)}
    </>
  )
}

function readRecentSearches() {
  try {
    const stored = JSON.parse(
      window.localStorage.getItem(recentSearchesKey) || '[]'
    )
    return Array.isArray(stored)
      ? stored
          .filter((item): item is string => typeof item === 'string')
          .slice(0, 6)
      : []
  } catch {
    return []
  }
}

function writeRecentSearches(items: string[]) {
  window.localStorage.setItem(recentSearchesKey, JSON.stringify(items))
}

function formatDate(timestamp: number) {
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  }).format(timestamp * 1000)
}
