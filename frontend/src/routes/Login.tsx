import { type FormEvent, useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { APIError } from '../api/client'
import { listAuthProviders, login } from '../api/auth'

export default function Login() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [showPassword, setShowPassword] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [providers, setProviders] = useState<string[]>([])
  const oauthError = searchParams.get('oauth_error')
  const githubLoginHref = useMemo(() => {
    return `/api/auth/github/start?next=${encodeURIComponent(safeNext(searchParams.get('next')))}`
  }, [searchParams])

  useEffect(() => {
    let cancelled = false
    listAuthProviders()
      .then((items) => {
        if (!cancelled) setProviders(items)
      })
      .catch(() => {
        if (!cancelled) setProviders([])
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (oauthError) setError(oauthErrorMessage(oauthError))
  }, [oauthError])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')

    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) {
      setError('请输入有效邮箱地址')
      return
    }

    if (!isStrongEnough(password)) {
      setError('密码至少 8 位，并且需要同时包含字母和数字')
      return
    }

    setSubmitting(true)
    try {
      await login({
        email: email.trim(),
        password,
        remember_me: remember,
      })
      navigate(safeNext(searchParams.get('next')), { replace: true })
    } catch (caught) {
      setError(errorMessage(caught, '登录失败，请稍后重试'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-preview" aria-label="FlowSpace 产品预览">
        <div className="auth-brand">
          <div className="auth-logo">F</div>
          <div>
            <strong>FlowSpace</strong>
            <span>轻量效率中枢</span>
          </div>
        </div>

        <div className="auth-copy">
          <h1>把笔记、任务和日程放回同一个工作台</h1>
          <p>从快速捕获开始，把零散想法整理成今天可以推进的任务、笔记和日程。</p>
        </div>

        <div className="auth-product-panel">
          <div className="auth-panel-top">
            <div>
              <span>今日</span>
              <strong>周四 · 6 月 4 日</strong>
            </div>
            <button type="button">快速捕获</button>
          </div>

          <div className="auth-capture">
            <span />
            <p>记录会议结论，明早整理成任务...</p>
          </div>

          <div className="auth-work-grid">
            <div className="auth-work-block auth-work-block-main">
              <div className="auth-block-header">
                <span>今天要完成</span>
                <strong>4</strong>
              </div>
              <PreviewTask done title="整理 FlowSpace 登录页文案" />
              <PreviewTask title="补充项目周报摘要" />
              <PreviewTask title="19:30 复盘产品路线" />
            </div>

            <div className="auth-work-block">
              <div className="auth-block-header">
                <span>最近笔记</span>
                <strong>3</strong>
              </div>
              <PreviewNote title="产品灵感收集" meta="刚刚更新" />
              <PreviewNote title="六月计划" meta="工作 · 12 分钟前" />
            </div>
          </div>
        </div>
      </section>

      <section className="auth-form-wrap" aria-label="登录表单">
        <form className="auth-form" onSubmit={handleSubmit} noValidate>
          <div className="auth-form-heading">
            <span>FlowSpace</span>
            <h2>登录工作台</h2>
            <p>继续整理今天的想法与安排</p>
          </div>

          {providers.includes('github') && (
            <a className="auth-oauth-btn" href={githubLoginHref}>
              <GithubIcon />
              使用 GitHub 登录
            </a>
          )}

          <div className="auth-divider">
            <span>或使用邮箱登录</span>
          </div>

          <label className="auth-field" htmlFor="login-email">
            <span>邮箱</span>
            <input
              id="login-email"
              name="email"
              type="email"
              autoComplete="email"
              placeholder="name@example.com"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
          </label>

          <label className="auth-field" htmlFor="login-password">
            <span>密码</span>
            <div className="auth-password-control">
              <input
                id="login-password"
                name="password"
                type={showPassword ? 'text' : 'password'}
                autoComplete="current-password"
                placeholder="输入密码"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
              <button
                type="button"
                onClick={() => setShowPassword((value) => !value)}
                aria-label={showPassword ? '隐藏密码' : '显示密码'}
              >
                <EyeIcon />
              </button>
            </div>
          </label>

          <div className="auth-form-row">
            <label className="auth-checkbox">
              <input
                type="checkbox"
                checked={remember}
                onChange={(event) => setRemember(event.target.checked)}
              />
              <span>记住我</span>
            </label>
            <button type="button" className="auth-link-btn">忘记密码？</button>
          </div>

          {error && <p className="auth-error" role="alert">{error}</p>}

          <button className="auth-submit" type="submit" disabled={submitting}>
            {submitting ? '登录中...' : '登录'}
          </button>

          <p className="auth-register">
            需要账号？请联系管理员创建账号
          </p>
        </form>
      </section>
    </main>
  )
}

function isStrongEnough(password: string) {
  return password.length >= 8 && /[A-Za-z]/.test(password) && /\d/.test(password)
}

function safeNext(value: string | null) {
  if (!value || !value.startsWith('/') || value.startsWith('//')) return '/'
  return value
}

function errorMessage(error: unknown, fallback: string) {
  if (error instanceof APIError) return error.message
  return fallback
}

function oauthErrorMessage(code: string) {
  const messages: Record<string, string> = {
    github_disabled: 'GitHub 登录暂未启用',
    github_state_invalid: '登录状态已过期，请重新尝试',
    github_exchange_failed: 'GitHub 授权失败，请稍后重试',
    github_profile_failed: '无法读取 GitHub 用户信息',
    github_no_verified_email: 'GitHub 账号没有已验证邮箱',
    github_auto_create_disabled: '当前暂不允许 GitHub 新账号自动注册',
    github_create_user_failed: '创建账号失败，请稍后重试',
  }
  return messages[code] ?? 'GitHub 登录失败，请重新尝试'
}

function PreviewTask({ title, done = false }: { title: string; done?: boolean }) {
  return (
    <div className="auth-preview-row">
      <span className={done ? 'is-done' : ''}>{done && <CheckMiniIcon />}</span>
      <p>{title}</p>
    </div>
  )
}

function PreviewNote({ title, meta }: { title: string; meta: string }) {
  return (
    <div className="auth-note-row">
      <strong>{title}</strong>
      <span>{meta}</span>
    </div>
  )
}

function GithubIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path
        fill="currentColor"
        d="M12 2C6.48 2 2 6.58 2 12.26c0 4.53 2.87 8.37 6.84 9.73.5.09.68-.22.68-.49 0-.24-.01-.88-.01-1.73-2.78.62-3.37-1.37-3.37-1.37-.45-1.19-1.11-1.5-1.11-1.5-.91-.64.07-.63.07-.63 1 .07 1.53 1.06 1.53 1.06.89 1.56 2.34 1.11 2.91.85.09-.66.35-1.11.63-1.37-2.22-.26-4.55-1.14-4.55-5.07 0-1.12.39-2.03 1.03-2.75-.1-.26-.45-1.3.1-2.71 0 0 .84-.28 2.75 1.05A9.3 9.3 0 0 1 12 6.99c.85 0 1.71.12 2.51.34 1.91-1.33 2.75-1.05 2.75-1.05.55 1.41.2 2.45.1 2.71.64.72 1.03 1.63 1.03 2.75 0 3.94-2.34 4.81-4.57 5.06.36.32.68.95.68 1.91 0 1.38-.01 2.49-.01 2.83 0 .27.18.59.69.49A10.11 10.11 0 0 0 22 12.26C22 6.58 17.52 2 12 2Z"
      />
    </svg>
  )
}

function EyeIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M2.5 12s3.2-6 9.5-6 9.5 6 9.5 6-3.2 6-9.5 6-9.5-6-9.5-6Z" />
      <circle cx="12" cy="12" r="3" />
    </svg>
  )
}

function CheckMiniIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="m3.5 8.2 2.8 2.7 6.2-6.2" />
    </svg>
  )
}
