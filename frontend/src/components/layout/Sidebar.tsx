import { NavLink } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useInboxList } from '../../hooks/useInbox'

const navGroups = [
  {
    title: '工作台',
    items: [
      { to: '/', label: '今日', icon: TodayIcon },
      { to: '/tasks', label: '任务', icon: CheckIcon },
      { to: '/calendar', label: '日历', icon: CalendarIcon },
      { to: '/inbox', label: '收件箱', icon: InboxIcon },
    ],
  },
  {
    title: '知识',
    items: [
      { to: '/notes', label: '笔记', icon: NoteIcon },
      { to: '/search', label: '搜索', icon: SearchIcon },
    ],
  },
  {
    title: '复盘',
    items: [{ to: '/summary', label: '每日总结', icon: SummaryIcon }],
  },
  {
    title: '系统',
    items: [{ to: '/admin/users', label: '账号管理', icon: AdminIcon }],
  },
]

type SidebarProps = {
  collapsed?: boolean
  onToggleCollapsed?: () => void
}

export function Sidebar({ collapsed = false, onToggleCollapsed }: SidebarProps) {
  const queryClient = useQueryClient()
  const inboxQ = useInboxList({ page_size: 1 })
  const inboxCount = inboxQ.data?.pagination.total ?? 0
  const toggleLabel = collapsed ? '展开侧边栏' : '收起侧边栏'

  return (
    <aside className={`workspace-sidebar ${collapsed ? 'is-collapsed' : ''}`}>
      <div className="sidebar-brand">
        <div className="sidebar-brand-main">
          <div className="sidebar-logo">F</div>
          <div className="sidebar-brand-copy">
            <strong>FlowSpace</strong>
            <span>轻量效率中枢</span>
          </div>
        </div>
        <button
          type="button"
          className="sidebar-collapse-button"
          aria-label={toggleLabel}
          aria-expanded={!collapsed}
          title={toggleLabel}
          onClick={onToggleCollapsed}
        >
          <SidebarCollapseIcon collapsed={collapsed} />
        </button>
      </div>

      <nav className="sidebar-nav" aria-label="主导航">
        {navGroups.map((group) => (
          <div className="sidebar-group" key={group.title}>
            <span className="sidebar-group-title">{group.title}</span>
            {group.items.map(({ to, label, icon: Icon }) => {
              const showInboxBadge = to === '/inbox' && inboxCount > 0

              return (
                <NavLink
                  key={to}
                  to={to}
                  end={to === '/'}
                  onClick={() => {
                    void queryClient.invalidateQueries()
                  }}
                  className={({ isActive }) => `sidebar-link ${isActive ? 'is-active' : ''}`}
                  aria-label={label}
                  title={collapsed ? label : undefined}
                >
                  <Icon />
                  <span className="sidebar-link-label">{label}</span>
                  {showInboxBadge && (
                    <span className="sidebar-badge" aria-label={`${inboxCount} 条未整理`}>
                      {inboxCount > 99 ? '99+' : inboxCount}
                    </span>
                  )}
                </NavLink>
              )
            })}
          </div>
        ))}
      </nav>

      <div className="sidebar-status">
        <span>本地优先</span>
        <strong>FlowSpace v0.2</strong>
        <em>快速捕获 · 任务 · 知识</em>
      </div>
    </aside>
  )
}

function SidebarCollapseIcon({ collapsed }: { collapsed: boolean }) {
  return (
    <svg viewBox="0 0 18 18" aria-hidden="true">
      <path d={collapsed ? 'M7 4.5 11.5 9 7 13.5' : 'M11 4.5 6.5 9 11 13.5'} />
      <path d="M4.5 3.8v10.4" />
    </svg>
  )
}

function IconFrame({ children }: { children: ReactNode }) {
  return (
    <svg viewBox="0 0 18 18" aria-hidden="true">
      {children}
    </svg>
  )
}

function TodayIcon() {
  return (
    <IconFrame>
      <rect x="4.5" y="4.5" width="9" height="9" rx="1.2" />
      <path d="M7 2.7v2.2M11 2.7v2.2M6.4 8.1h5.2" />
    </IconFrame>
  )
}

function CheckIcon() {
  return (
    <IconFrame>
      <path d="M4.2 9.2 7.4 12.4 14.2 5.6" />
    </IconFrame>
  )
}

function CalendarIcon() {
  return (
    <IconFrame>
      <path d="M9 3.2 14.8 9 9 14.8 3.2 9 9 3.2Z" />
      <path d="M6.6 9h4.8M9 6.6v4.8" />
    </IconFrame>
  )
}

function InboxIcon() {
  return (
    <IconFrame>
      <path d="M3.4 10.2h3.3l1.1 1.8h2.4l1.1-1.8h3.3" />
      <path d="M4.8 6.2h8.4l1.4 4v3.2c0 .8-.5 1.3-1.3 1.3H4.7c-.8 0-1.3-.5-1.3-1.3v-3.2l1.4-4Z" />
    </IconFrame>
  )
}

function NoteIcon() {
  return (
    <IconFrame>
      <path d="M5 3.8h8v10.4H5z" />
      <path d="M7.2 6.4h3.6M7.2 8.8h3.6M7.2 11.2h2.1" />
    </IconFrame>
  )
}

function SearchIcon() {
  return (
    <IconFrame>
      <circle cx="8" cy="8" r="3.5" />
      <path d="m10.7 10.7 3.5 3.5" />
    </IconFrame>
  )
}

function SummaryIcon() {
  return (
    <IconFrame>
      <path d="M4 5.1h10M4 9h10M4 12.9h6.6" />
      <path d="M12.1 11.6 13.3 13l2-2.6" />
    </IconFrame>
  )
}

function AdminIcon() {
  return (
    <IconFrame>
      <circle cx="9" cy="7" r="3.2" />
      <path d="M4.2 14.2c.8-2 2.3-3.1 4.8-3.1s4 1.1 4.8 3.1" />
      <circle cx="9" cy="9" r="6.7" />
    </IconFrame>
  )
}
