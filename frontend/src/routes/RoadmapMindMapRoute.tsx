import { TaskDomainGate } from '../components/taskDomain/TaskDomainGate'
import RoadmapMindMap from './RoadmapMindMap'

export default function RoadmapMindMapRoute() {
  return (
    <TaskDomainGate
      legacy={
        <div className="domain-unavailable">
          <strong>当前工作空间尚未启用新版学习路线</strong>
        </div>
      }
      v2={<RoadmapMindMap />}
    />
  )
}
