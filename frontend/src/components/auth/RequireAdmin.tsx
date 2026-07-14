import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { getCurrentUser } from '../../api/auth'

export function RequireAdmin({ children }: { children?: ReactNode }) {
  const location = useLocation()
  const currentUser = useQuery({
    queryKey: ['auth', 'me'],
    queryFn: getCurrentUser,
    retry: false,
    staleTime: 5 * 60_000,
  })

  if (currentUser.isLoading) {
    return <p className="route-guard-status">正在确认账号权限...</p>
  }

  if (currentUser.isError || currentUser.data?.user.role !== 'admin') {
    return <Navigate to="/" replace state={{ from: location.pathname }} />
  }

  return children ?? <Outlet />
}
