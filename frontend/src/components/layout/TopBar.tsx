import { type FormEvent, useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useLocation, useNavigate } from 'react-router-dom'
import { getCurrentUser, logout } from '../../api/auth'
import { useUIStore } from '../../stores/ui'

const pageMeta: Record<string, { title: string; subtitle: string }> = {
  '/settings': {
    title: '用户设置',
    subtitle: '管理个人资料，以及当前工作空间使用的存储和 AI 服务',
  },
  '/': {
    title: '今日',
    subtitle: `${formatToday()} · 把任务、日程和笔记收束到一个可执行视图`,
  },
  '/notes': {
    title: '笔记库',
    subtitle: '按项目、标签和最近更新整理想法，让知识可以继续被任务调用',
  },
  '/tasks': {
    title: '任务工作台',
    subtitle: '按项目、状态和节奏推进每天要完成的事情',
  },
  '/projects': {
    title: '项目中心',
    subtitle: '按目标、类型和周期组织任务定义与每次实际执行',
  },
  '/calendar': {
    title: '日历',
    subtitle: '查看月份安排，连接任务、提醒和关联笔记',
  },
  '/inbox': {
    title: '未整理捕获',
    subtitle: '把临时想法转为笔记、任务或日程，先捕获，再整理成具体对象',
  },
  '/search': {
    title: '全局搜索',
    subtitle: '跨笔记、任务、日程和项目找回上下文',
  },
  '/summary': {
    title: '每日总结',
    subtitle: '回顾完成事项、未完成事项和知识产出',
  },
  '/admin/users': {
    title: '账号管理',
    subtitle: '管理登录账号、角色权限和临时密码',
  },
  '/change-password': { title: '修改密码', subtitle: '更新当前账号的登录凭据' },
}

function formatToday() {
  const date = new Date()
  const dateText = new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  }).format(date)
  const weekday = new Intl.DateTimeFormat('zh-CN', { weekday: 'long' }).format(
    date
  )
  return `${dateText}，${weekday}`
}

function getAccountInitial(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return '用'
  const ascii = trimmed.match(/[A-Za-z0-9]/)?.[0]
  return (ascii ?? Array.from(trimmed)[0]).toUpperCase()
}

export function TopBar() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [searchValue, setSearchValue] = useState('')
  const [accountMenuOpen, setAccountMenuOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)
  const [logoutError, setLogoutError] = useState('')
  const accountMenuRef = useRef<HTMLDivElement>(null)
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)

  const meta = pathname.startsWith('/editor/')
    ? { title: '编辑器', subtitle: '专注写作，并把内容连接到任务和日程' }
    : pathname.startsWith('/projects/')
      ? { title: '项目详情', subtitle: '在一个目标下查看任务定义与执行实例' }
      : (pageMeta[pathname] ?? pageMeta['/'])
  const { data: currentUser } = useQuery({
    queryKey: ['auth', 'me'],
    queryFn: getCurrentUser,
    retry: false,
    staleTime: 5 * 60_000,
  })
  const accountName =
    currentUser?.user.display_name?.trim() ||
    currentUser?.user.email ||
    '当前账号'
  const accountEmail = currentUser?.user.email
  const accountInitial = getAccountInitial(accountName)

  function openSearch() {
    const keyword = searchValue.trim()
    navigate(keyword ? `/search?q=${encodeURIComponent(keyword)}` : '/search')
  }

  function handleSearchSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    openSearch()
  }

  async function handleLogout() {
    if (loggingOut) return
    setLoggingOut(true)
    setLogoutError('')

    try {
      await logout()
      queryClient.clear()
      setAccountMenuOpen(false)
      navigate('/login', { replace: true })
    } catch {
      setLogoutError('退出失败，请稍后重试')
      setLoggingOut(false)
    }
  }

  useEffect(() => {
    if (!accountMenuOpen) return

    function handlePointerDown(event: PointerEvent) {
      if (accountMenuRef.current?.contains(event.target as Node)) return
      setAccountMenuOpen(false)
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        setAccountMenuOpen(false)
      }
    }

    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [accountMenuOpen])

  return (
    <div className="workspace-topbar">
      <div className="min-w-0">
        <h1 className="page-title">{meta.title}</h1>
        <p className="workspace-subtitle">{meta.subtitle}</p>
      </div>
      <div className="workspace-actions">
        <form
          className="workspace-search-hint"
          role="search"
          onSubmit={handleSearchSubmit}
        >
          <SearchIcon />
          <input
            value={searchValue}
            onChange={(event) => setSearchValue(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                openSearch()
              }
            }}
            placeholder="搜索笔记、任务、日程..."
            aria-label="搜索笔记、任务、日程"
          />
          <kbd>⌘K</kbd>
        </form>
        <button onClick={() => setCaptureOpen(true)} className="primary-action">
          <PlusIcon />
          快速捕获
        </button>
        <div className="account-menu-wrap" ref={accountMenuRef}>
          <button
            className="avatar-button"
            type="button"
            aria-label="打开账号菜单"
            aria-haspopup="menu"
            aria-expanded={accountMenuOpen}
            aria-controls="account-menu"
            onClick={() => {
              setLogoutError('')
              setAccountMenuOpen((open) => !open)
            }}
          >
            {currentUser?.avatar_url ? (
              <img src={currentUser.avatar_url} alt="" />
            ) : (
              accountInitial
            )}
          </button>
          {accountMenuOpen && (
            <div
              className="account-menu"
              id="account-menu"
              role="menu"
              aria-label="账号菜单"
            >
              <div className="account-menu-header">
                <span>当前账号</span>
                <strong>{accountName}</strong>
                {accountEmail && accountEmail !== accountName && (
                  <em>{accountEmail}</em>
                )}
                {currentUser?.must_change_password && (
                  <small>需要修改密码</small>
                )}
              </div>
              <button
                className="account-menu-item"
                type="button"
                role="menuitem"
                onClick={() => {
                  setAccountMenuOpen(false)
                  navigate('/settings')
                }}
              >
                用户设置
              </button>
              <button
                className="account-menu-item is-danger"
                type="button"
                role="menuitem"
                onClick={handleLogout}
                disabled={loggingOut}
              >
                {loggingOut ? '退出中...' : '退出登录'}
              </button>
              {logoutError && (
                <p className="account-menu-error" role="alert">
                  {logoutError}
                </p>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function SearchIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="11" cy="11" r="7" />
      <path d="m16.5 16.5 4 4" />
    </svg>
  )
}

function PlusIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}
