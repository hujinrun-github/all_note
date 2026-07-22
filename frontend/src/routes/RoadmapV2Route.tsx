import { TaskDomainGate } from '../components/taskDomain/TaskDomainGate'
import RoadmapV2 from './RoadmapV2'
export default function RoadmapV2Route() {
  return (
    <TaskDomainGate
      legacy={
        <div className="domain-unavailable">
          <strong>当前工作空间尚未启用新版学习路线</strong>
        </div>
      }
      v2={<RoadmapV2 />}
    />
  )
}
