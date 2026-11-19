import { NavLink, useLocation } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getCurrentUser } from '../../api/auth'
import { createWorkspaceQueryParams } from '../../api/workspace'
import { useTaskWorkbenchFacets } from '../../hooks/useWorkspace'
import { useTaskDomainCapabilities } from '../../hooks/useTaskDomain'
import {
  CalendarDays,
  CheckSquare2,
  House,
  Inbox,
  Layers3,
  NotebookPen,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  Settings2,
  ShieldCheck,
  Sparkles,
  SquareChartGantt,
} from 'lucide-react'

const navGroups = [
  {
    title: '工作空间',
    items: [
      { to: '/', label: '工作台', icon: House },
      { to: '/tasks', label: '任务', icon: CheckSquare2 },
      { to: '/projects', label: '项目', icon: Layers3, v2Only: true },
      { to: '/calendar', label: '日历', icon: CalendarDays },
      { to: '/inbox', label: '收件箱', icon: Inbox },
    ],
  },
  {
    title: '知识与回顾',
    items: [
      { to: '/notes', label: '笔记', icon: NotebookPen },
      { to: '/search', label: '搜索', icon: Search },
      { to: '/summary', label: '每日总结', icon: SquareChartGantt },
    ],
  },
  {
    title: '系统',
    items: [{ to: '/admin/users', label: '账号管理', icon: ShieldCheck }],
  },
]

type SidebarProps = {
  collapsed?: boolean
  onToggleCollapsed?: () => void
  onNavigate?: () => void
}

export function Sidebar({
  collapsed = false,
  onToggleCollapsed,
  onNavigate,
}: SidebarProps) {
  const queryClient = useQueryClient()
  const location = useLocation()
  const toggleLabel = collapsed ? '展开侧边栏' : '收起侧边栏'
  const currentUser = useQuery({
    queryKey: ['auth', 'me'],
    queryFn: getCurrentUser,
    retry: false,
    staleTime: 5 * 60_000,
  })
  const taskDomainCapability = useTaskDomainCapabilities()
  const inboxQuery = useTaskWorkbenchFacets(createWorkspaceQueryParams(), {
    enabled:
      taskDomainCapability.data?.model_version === 'v2' &&
      taskDomainCapability.data.available,
  })
  const inboxCount = inboxQuery.isError
    ? 0
    : (inboxQuery.data?.facets.inbox ?? 0)
  const visibleGroups = navGroups.filter(
    (group) => group.title !== '系统' || currentUser.data?.user.role === 'admin'
  )

  return (
    <aside className={`workspace-sidebar ${collapsed ? 'is-collapsed' : ''}`}>
      <div className="sidebar-brand">
        <div className="sidebar-brand-main">
          <div className="sidebar-logo" aria-hidden="true">
            <Sparkles />
          </div>
          <div className="sidebar-brand-copy">
            <strong>FlowSpace</strong>
            <span>个人工作台</span>
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
          {collapsed ? (
            <PanelLeftOpen aria-hidden="true" />
          ) : (
            <PanelLeftClose aria-hidden="true" />
          )}
        </button>
      </div>

      <nav className="sidebar-nav" aria-label="主导航">
        {visibleGroups.map((group) => (
          <div className="sidebar-group" key={group.title}>
            <span className="sidebar-group-title">{group.title}</span>
            {group.items
              .filter(
                (item) =>
                  !('v2Only' in item) ||
                  taskDomainCapability.data?.model_version === 'v2'
              )
              .map(({ to, label, icon: Icon }) => {
                const showInboxBadge = to === '/inbox' && inboxCount > 0

                return (
                  <NavLink
                    key={to}
                    to={to}
                    end={to === '/'}
                    onClick={() => {
                      if (location.pathname === to)
                        void queryClient.invalidateQueries()
                      onNavigate?.()
                    }}
                    className={({ isActive }) =>
                      `sidebar-link ${isActive ? 'is-active' : ''}`
                    }
                    aria-label={label}
                    title={collapsed ? label : undefined}
                  >
                    <Icon aria-hidden="true" />
                    <span className="sidebar-link-label">{label}</span>
                    {showInboxBadge && (
                      <span
                        className="sidebar-badge"
                        aria-label={`${inboxCount} 条未整理`}
                      >
                        {inboxCount > 99 ? '99+' : inboxCount}
                      </span>
                    )}
                  </NavLink>
                )
              })}
          </div>
        ))}
      </nav>

      <NavLink
        to="/settings"
        className="sidebar-account"
        aria-label="用户设置"
        onClick={onNavigate}
      >
        <span className="sidebar-account-avatar" aria-hidden="true">
          {(currentUser.data?.user.display_name || 'F')
            .slice(0, 1)
            .toUpperCase()}
        </span>
        <span className="sidebar-account-copy">
          <strong>
            {currentUser.data?.user.display_name || '我的工作空间'}
          </strong>
          <small>个人工作空间</small>
        </span>
        <Settings2 aria-hidden="true" />
      </NavLink>
    </aside>
  )
}
