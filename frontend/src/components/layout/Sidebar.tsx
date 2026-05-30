import { NavLink } from 'react-router-dom'

const navItems = [
  { to: '/', label: '今日', icon: CalendarIcon },
  { to: '/tasks', label: '任务', icon: CheckIcon },
  { to: '/notes', label: '笔记', icon: FileIcon },
  { to: '/calendar', label: '日历', icon: CalendarDaysIcon },
  { to: '/inbox', label: '收件箱', icon: InboxIcon },
  { to: '/search', label: '搜索', icon: SearchIcon },
]

export function Sidebar() {
  return (
    <aside className="h-screen border-r border-fs-border bg-fs-surface px-3.5 py-[18px] flex flex-col gap-[22px] max-[760px]:hidden">
      <div className="flex gap-2.5 items-center px-1 pb-2">
        <div className="w-8 h-8 rounded-lg bg-fs-accent grid place-items-center text-white font-bold text-sm">F</div>
        <div>
          <strong className="block text-sm leading-tight">FlowSpace</strong>
          <span className="text-fs-text-muted text-xs">轻量效率中枢</span>
        </div>
      </div>

      <nav className="grid gap-1.5">
        {navItems.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              `flex items-center justify-between min-h-[34px] px-2.5 rounded-md text-[13px] font-medium transition-colors ${
                isActive ? 'bg-fs-hover text-fs-accent font-semibold' : 'text-fs-text hover:bg-fs-hover'
              }`
            }
          >
            <span className="inline-flex items-center gap-2 min-w-0">
              <Icon />
              {label}
            </span>
          </NavLink>
        ))}
      </nav>

      <div className="mt-auto grid gap-1.5">
        <div className="text-fs-text-muted text-[11px] font-semibold uppercase tracking-wider px-1">FlowSpace v0.1</div>
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
