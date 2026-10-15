import {
  CalendarDays,
  CheckCheck,
  House,
  Menu,
  NotebookPen,
  Settings,
  X,
} from 'lucide-react'
import { useState } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { useMediaQuery } from '../../hooks/useMediaQuery'
import { Sidebar } from './Sidebar'
import { Modal } from '../ui/Modal'

const tabs = [
  { to: '/', label: '今日', icon: House },
  { to: '/tasks', label: '任务', icon: CheckCheck },
  { to: '/calendar', label: '日历', icon: CalendarDays },
  { to: '/notes', label: '笔记', icon: NotebookPen },
]

export function MobileNavigation() {
  const [openAt, setOpenAt] = useState<string | null>(null)
  const location = useLocation()
  const mobile = useMediaQuery('(max-width: 820px)')
  const close = () => setOpenAt(null)
  if (!mobile) return null
  const open = openAt === location.key
  const otherPage = !tabs.some(({ to }) =>
    to === '/' ? location.pathname === '/' : location.pathname.startsWith(to)
  )

  return (
    <>
      <nav className="mobile-navigation" aria-label="手机主导航">
        {tabs.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            onClick={close}
            className={({ isActive }) => (isActive ? 'is-active' : '')}
          >
            <Icon aria-hidden="true" />
            <span>{label}</span>
          </NavLink>
        ))}
        <button
          type="button"
          className={open || otherPage ? 'is-active' : ''}
          aria-label="更多导航"
          aria-expanded={open}
          onClick={() => setOpenAt(location.key)}
        >
          <Menu aria-hidden="true" />
          <span>更多</span>
        </button>
      </nav>
      {open && (
        <Modal className="mobile-menu-dialog" label="全部功能" onClose={close}>
          <header className="mobile-menu-heading">
            <strong>全部功能</strong>
            <button type="button" aria-label="关闭导航" onClick={close}>
              <X aria-hidden="true" />
            </button>
          </header>
          <Sidebar onNavigate={close} />
          <Link className="mobile-settings-link" to="/settings" onClick={close}>
            <Settings aria-hidden="true" />
            用户设置
          </Link>
        </Modal>
      )}
    </>
  )
}
