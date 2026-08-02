import { Link, useLocation } from 'react-router-dom'

export default function NotFound() {
  const location = useLocation()

  return (
    <main className="not-found-page">
      <section className="not-found-card" aria-labelledby="not-found-title">
        <Link className="not-found-brand" to="/" aria-label="返回 FlowSpace 工作台">
          <span aria-hidden="true">F</span>
          <strong>FlowSpace</strong>
        </Link>

        <div className="not-found-code" aria-hidden="true">
          404
        </div>
        <p className="not-found-eyebrow">页面未找到</p>
        <h1 id="not-found-title">这条路径暂时没有内容</h1>
        <p className="not-found-description">
          地址 <code>{location.pathname}</code> 不存在，可能已经移动或输入有误。
        </p>

        <div className="not-found-actions">
          <Link className="not-found-primary" to="/">
            返回工作台
          </Link>
          <Link className="not-found-secondary" to="/login">
            前往登录
          </Link>
        </div>
      </section>
    </main>
  )
}
