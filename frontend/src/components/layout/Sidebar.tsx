import { NavLink } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { useInboxList } from '../../hooks/useInbox'

const navItems = [
  { to: '/', label: '今日', icon: CalendarIcon },
  { to: '/tasks', label: '任务', icon: CheckIcon },
  { to: '/notes', label: '笔记', icon: FileIcon },
  { to: '/calendar', label: '日历', icon: CalendarDaysIcon },
  { to: '/inbox', label: '收件箱', icon: InboxIcon },
  { to: '/search', label: '搜索', icon: SearchIcon },
  { to: '/summary', label: '每日总结', icon: SummaryIcon },
]

export function Sidebar() {
  const queryClient = useQueryClient()
  const inboxQ = useInboxList({ page_size: 1 })
  const inboxCount = inboxQ.data?.pagination.total ?? 0

  return (
    <aside className="workspace-sidebar">
      <div className="sidebar-brand">
        <div className="sidebar-logo">F</div>
        <div>
          <strong>FlowSpace</strong>
          <span>轻量效率中枢</span>
        </div>
      </div>

      <nav className="sidebar-nav">
        {navItems.map(({ to, label, icon: Icon }) => {
          const showInboxBadge = to === '/inbox' && inboxCount > 0

          return (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            onClick={() => {
              void queryClient.invalidateQueries()
            }}
            className={({ isActive }) =>
              `sidebar-link ${isActive ? 'is-active' : ''}`
            }
          >
            <Icon />
            <span>{label}</span>
            {showInboxBadge && (
              <span className="sidebar-badge" aria-label={`${inboxCount} 条未整理`}>
                {inboxCount > 99 ? '99+' : inboxCount}
              </span>
            )}
          </NavLink>
          )
        })}
      </nav>

      <div className="sidebar-status">
        <span>本地优先</span>
        <strong>FlowSpace v0.1</strong>
      </div>
    </aside>
  )
}

function CalendarIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="18" rx="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
}
function CheckIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><polyline points="9 11 12 14 22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>
}
function FileIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z"/><polyline points="14 2 14 8 20 8"/></svg>
}
function CalendarDaysIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="18" rx="2"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/><line x1="8" y1="14" x2="8" y2="14.01"/><line x1="12" y1="14" x2="12" y2="14.01"/><line x1="16" y1="14" x2="16" y2="14.01"/></svg>
}
function InboxIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><polyline points="22 12 16 12 14 15 10 15 8 12 2 12"/><path d="M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/></svg>
}
function SearchIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
}
function SummaryIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 5h12M3 9h12M3 13h8" />
      <circle cx="14" cy="13" r="2" />
      <path d="M15.5 14.5L17 16" />
    </svg>
  )
}
