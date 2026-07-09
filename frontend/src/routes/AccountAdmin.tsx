import { type FormEvent, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { APIError } from '../api/client'
import {
  createAdminUser,
  listAdminUsers,
  resetAdminUserPassword,
  setAdminUserStatus,
  updateAdminUser,
  type AccountRole,
  type AccountStatus,
  type AccountUser,
} from '../api/admin'

const pageSize = 20

export default function AccountAdmin() {
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [queryInput, setQueryInput] = useState('')
  const [query, setQuery] = useState('')
  const [form, setForm] = useState({
    email: '',
    display_name: '',
    temporary_password: '',
    role: 'user' as AccountRole,
  })
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')

  const usersQuery = useQuery({
    queryKey: ['admin-users', page, query],
    queryFn: () => listAdminUsers({ page, pageSize, q: query }),
  })

  const createUser = useMutation({
    mutationFn: createAdminUser,
    onSuccess: () => {
      setForm({ email: '', display_name: '', temporary_password: '', role: 'user' })
      setNotice('账号已创建，首次登录时需要修改临时密码。')
      setError('')
      void queryClient.invalidateQueries({ queryKey: ['admin-users'] })
    },
    onError: (caught) => {
      setNotice('')
      setError(errorMessage(caught, '创建账号失败'))
    },
  })

  const updateRole = useMutation({
    mutationFn: ({ userID, role }: { userID: string; role: AccountRole }) => updateAdminUser(userID, { role }),
    onSuccess: () => {
      setNotice('账号角色已更新。')
      setError('')
      void queryClient.invalidateQueries({ queryKey: ['admin-users'] })
    },
    onError: (caught) => {
      setNotice('')
      setError(errorMessage(caught, '更新角色失败'))
    },
  })

  const updateStatus = useMutation({
    mutationFn: ({ userID, status }: { userID: string; status: AccountStatus }) => setAdminUserStatus(userID, status),
    onSuccess: () => {
      setNotice('账号状态已更新。')
      setError('')
      void queryClient.invalidateQueries({ queryKey: ['admin-users'] })
    },
    onError: (caught) => {
      setNotice('')
      setError(errorMessage(caught, '更新状态失败'))
    },
  })

  const resetPassword = useMutation({
    mutationFn: ({ userID, temporaryPassword }: { userID: string; temporaryPassword: string }) =>
      resetAdminUserPassword(userID, temporaryPassword),
    onSuccess: () => {
      setNotice('临时密码已设置，目标用户下次登录后需要修改密码。')
      setError('')
      void queryClient.invalidateQueries({ queryKey: ['admin-users'] })
    },
    onError: (caught) => {
      setNotice('')
      setError(errorMessage(caught, '重置密码失败'))
    },
  })

  function handleSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPage(1)
    setQuery(queryInput.trim())
  }

  function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setNotice('')
    setError('')

    if (!isStrongEnough(form.temporary_password)) {
      setError('临时密码至少 8 位，并且需要同时包含字母和数字')
      return
    }

    createUser.mutate({
      email: form.email.trim(),
      display_name: form.display_name.trim(),
      temporary_password: form.temporary_password,
      role: form.role,
    })
  }

  function handleReset(user: AccountUser) {
    const temporaryPassword = window.prompt(`为 ${user.display_name} 设置临时密码`)
    if (!temporaryPassword) return
    resetPassword.mutate({ userID: user.id, temporaryPassword })
  }

  const users = usersQuery.data?.users ?? []
  const pagination = usersQuery.data?.pagination
  const totalPages = Math.max(1, Math.ceil((pagination?.total ?? users.length) / pageSize))
  const activeCount = users.filter((user) => user.status === 'active').length
  const adminCount = users.filter((user) => user.role === 'admin').length

  return (
    <div className="account-page">
      <div className="page-local-actions">
        <button className="primary-action" type="button">＋ 邀请用户</button>
      </div>

      <section className="account-metrics">
        <Metric label="当前账户" value={String(users.length)} />
        <Metric label="活跃账户" value={String(activeCount)} tone="success" />
        <Metric label="管理员" value={String(adminCount)} />
      </section>

      <div className="account-workspace">
        <section className="surface-panel account-list-panel">
          <div className="panel-heading">
            <div>
              <h2>用户与权限</h2>
            </div>
            <form className="account-search" onSubmit={handleSearch}>
              <input
                type="search"
                value={queryInput}
                onChange={(event) => setQueryInput(event.target.value)}
                placeholder="搜索邮箱或昵称..."
                aria-label="搜索用户"
              />
            </form>
          </div>

          {error && <p className="account-message is-error" role="alert">{error}</p>}
          {notice && <p className="account-message is-success" role="status">{notice}</p>}

          {usersQuery.isLoading ? (
            <p className="empty-copy">账号列表加载中...</p>
          ) : usersQuery.error ? (
            <div className="empty-state">
              <strong>账号列表加载失败</strong>
              <p>{errorMessage(usersQuery.error, '请确认当前账号拥有管理员权限')}</p>
            </div>
          ) : users.length === 0 ? (
            <div className="empty-state">
              <strong>没有匹配的账号</strong>
              <p>换一个搜索词，或在右侧创建新账号。</p>
            </div>
          ) : (
            <div className="account-table">
              <div className="account-row account-row-header">
                <span>用户</span>
                <span>邮箱</span>
                <span>角色</span>
                <span>状态</span>
                <span>操作</span>
              </div>
              {users.map((user) => (
                <div className="account-row" key={user.id}>
                  <div className="account-user-main">
                    <span className="account-avatar">{initials(user)}</span>
                    <div>
                      <strong>{user.display_name}</strong>
                      <span>{user.role === 'admin' ? '管理员账号' : '普通账号'}</span>
                    </div>
                  </div>
                  <span className="account-email">{user.email}</span>
                  <select
                    className="account-inline-select"
                    value={user.role}
                    disabled={updateRole.isPending}
                    onChange={(event) => updateRole.mutate({ userID: user.id, role: event.target.value as AccountRole })}
                    aria-label={`修改 ${user.display_name} 的角色`}
                  >
                    <option value="user">普通用户</option>
                    <option value="admin">管理员</option>
                  </select>
                  <span className={`account-status is-${user.status}`}>
                    {user.status === 'active' ? '启用' : '禁用'}
                    {user.must_change_password && <em>需改密</em>}
                  </span>
                  <div className="row-actions">
                    <button type="button" onClick={() => handleReset(user)} disabled={resetPassword.isPending}>
                      重置
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        updateStatus.mutate({
                          userID: user.id,
                          status: user.status === 'active' ? 'disabled' : 'active',
                        })
                      }
                      disabled={updateStatus.isPending}
                    >
                      {user.status === 'active' ? '禁用' : '启用'}
                    </button>
                  </div>
                </div>
              ))}
              <div className="account-pagination">
                <button className="secondary-action" disabled={page <= 1} onClick={() => setPage((value) => value - 1)}>
                  ‹
                </button>
                <span>{page} / {totalPages}</span>
                <button className="secondary-action" disabled={page >= totalPages} onClick={() => setPage((value) => value + 1)}>
                  ›
                </button>
              </div>
            </div>
          )}
        </section>

        <aside className="surface-panel account-create-panel">
          <div className="panel-heading is-compact">
            <div>
              <h2>邀请一个用户</h2>
              <p>创建后可复制临时密码</p>
            </div>
          </div>
          <form className="account-form" onSubmit={handleCreate}>
            <label className="account-field" htmlFor="new-user-email">
              <span>邮箱</span>
              <input id="new-user-email" type="email" value={form.email} onChange={(event) => setForm((value) => ({ ...value, email: event.target.value }))} required />
            </label>
            <label className="account-field" htmlFor="new-user-name">
              <span>显示名称</span>
              <input id="new-user-name" value={form.display_name} onChange={(event) => setForm((value) => ({ ...value, display_name: event.target.value }))} required />
            </label>
            <label className="account-field" htmlFor="new-user-password">
              <span>临时密码</span>
              <input id="new-user-password" type="password" value={form.temporary_password} onChange={(event) => setForm((value) => ({ ...value, temporary_password: event.target.value }))} required />
            </label>
            <label className="account-field" htmlFor="new-user-role">
              <span>角色</span>
              <select id="new-user-role" value={form.role} onChange={(event) => setForm((value) => ({ ...value, role: event.target.value as AccountRole }))}>
                <option value="user">普通用户</option>
                <option value="admin">管理员</option>
              </select>
            </label>
            <button className="primary-action" type="submit" disabled={createUser.isPending}>
              {createUser.isPending ? '创建中...' : '创建账号'}
            </button>
          </form>
          <p className="account-footnote">普通用户不会看到账号管理入口；危险操作需要二次确认。</p>
        </aside>
      </div>
    </div>
  )
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: 'success' }) {
  return (
    <div className="metric-tile">
      <span>{label}</span>
      <strong className={tone === 'success' ? 'is-success' : ''}>{value}</strong>
    </div>
  )
}

function initials(user: AccountUser) {
  const source = user.display_name.trim() || user.email.trim()
  return source.slice(0, 1).toUpperCase()
}

function isStrongEnough(password: string) {
  return password.length >= 8 && /[A-Za-z]/.test(password) && /\d/.test(password)
}

function errorMessage(error: unknown, fallback: string) {
  if (error instanceof APIError) return error.message
  return fallback
}
