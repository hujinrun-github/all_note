import { TaskDomainGate } from '../components/taskDomain/TaskDomainGate'
import Projects from './Projects'

export default function ProjectsRoute() {
  return (
    <TaskDomainGate
      legacy={<V2Required />}
      v2={<Projects />}
    />
  )
}

function V2Required() {
  return (
    <div className="domain-unavailable">
      <strong>当前工作空间仍使用旧任务模型</strong>
      <p>完成任务域迁移后，新的项目中心会在这里启用。</p>
    </div>
  )
}
