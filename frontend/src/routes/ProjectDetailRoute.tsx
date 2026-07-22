import { TaskDomainGate } from '../components/taskDomain/TaskDomainGate'
import ProjectDetail from './ProjectDetail'

export default function ProjectDetailRoute() {
  return (
    <TaskDomainGate
      legacy={
        <div className="domain-unavailable">
          <strong>当前工作空间仍使用旧任务模型</strong>
          <p>项目详情将在任务域迁移完成后启用。</p>
        </div>
      }
      v2={<ProjectDetail />}
    />
  )
}
