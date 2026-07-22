import type { ReactNode } from 'react'

import { useTaskDomainCapabilities } from '../../hooks/useTaskDomain'

export function TaskDomainGate({
  legacy,
  v2,
}: {
  legacy: ReactNode
  v2: ReactNode
}) {
  const capability = useTaskDomainCapabilities()

  if (capability.isLoading) {
    return <p className="domain-empty">正在确认当前工作空间的任务模型…</p>
  }
  if (capability.isError || !capability.data?.available) {
    return (
      <div className="domain-unavailable" role="alert">
        <strong>任务服务暂时不可用</strong>
        <p>无法确认当前工作空间的数据模型，请稍后重试。</p>
      </div>
    )
  }
  return capability.data.model_version === 'v2' ? v2 : legacy
}
