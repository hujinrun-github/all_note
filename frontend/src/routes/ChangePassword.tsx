import { type FormEvent, useState } from 'react'
import { Link } from 'react-router-dom'
import { APIError } from '../api/client'
import { changePassword } from '../api/auth'

export default function ChangePassword() {
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    setMessage('')

    if (newPassword !== confirmPassword) {
      setError('两次输入的新密码不一致')
      return
    }
    if (!isStrongEnough(newPassword)) {
      setError('新密码至少 8 位，并且需要同时包含字母和数字')
      return
    }

    setSubmitting(true)
    try {
      await changePassword({
        current_password: currentPassword,
        new_password: newPassword,
      })
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setMessage('密码已更新，当前会话会保留，其他会话已失效。')
    } catch (caught) {
      setError(errorMessage(caught, '修改密码失败'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="password-workspace">
      <section className="surface-panel password-card">
        <div className="panel-heading">
          <div>
            <span>账号安全</span>
            <h2>修改登录密码</h2>
          </div>
          <Link className="secondary-action" to="/">
            返回工作台
          </Link>
        </div>

        <form className="account-form" onSubmit={handleSubmit}>
          <label className="account-field" htmlFor="current-password">
            <span>当前密码</span>
            <input
              id="current-password"
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
              required
            />
          </label>

          <label className="account-field" htmlFor="new-password">
            <span>新密码</span>
            <input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
              required
            />
          </label>

          <label className="account-field" htmlFor="confirm-password">
            <span>确认新密码</span>
            <input
              id="confirm-password"
              type="password"
              autoComplete="new-password"
              value={confirmPassword}
              onChange={(event) => setConfirmPassword(event.target.value)}
              required
            />
          </label>

          <p className="password-policy">密码策略：8-72 个字符，至少包含一个字母和一个数字。</p>

          {error && (
            <p className="account-message is-error" role="alert">
              {error}
            </p>
          )}
          {message && (
            <p className="account-message is-success" role="status">
              {message}
            </p>
          )}

          <button className="primary-action" type="submit" disabled={submitting}>
            {submitting ? '保存中...' : '更新密码'}
          </button>
        </form>
      </section>
    </div>
  )
}

function isStrongEnough(password: string) {
  return password.length >= 8 && /[A-Za-z]/.test(password) && /\d/.test(password)
}

function errorMessage(error: unknown, fallback: string) {
  if (error instanceof APIError) return error.message
  return fallback
}
