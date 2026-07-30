import {
  CheckCircle2,
  Circle,
  CircleDot,
  Flag,
} from 'lucide-react'

import type { RoadmapNodeV2 } from '../../api/roadmapV2'

export function roadmapNodeProgress(node: RoadmapNodeV2) {
  if (node.progress.total === 0) return 0
  return Math.round((node.progress.done / node.progress.total) * 100)
}

export function RoadmapStageRail({
  nodes,
  selectedNodeID,
  onSelect,
}: {
  nodes: RoadmapNodeV2[]
  selectedNodeID: string
  onSelect: (nodeID: string) => void
}) {
  return (
    <ol className="plan-stage-rail" aria-label="学习路线节点">
      {nodes.map((node, index) => {
        const progress = roadmapNodeProgress(node)
        const isComplete =
          node.progress.total > 0 &&
          node.progress.done === node.progress.total
        const isSelected = node.id === selectedNodeID

        return (
          <li
            className={`plan-stage${isSelected ? ' is-selected' : ''}${
              isComplete ? ' is-complete' : ''
            }`}
            key={node.id}
          >
            <button
              type="button"
              aria-pressed={isSelected}
              onClick={() => onSelect(node.id)}
            >
              <span className="plan-stage-marker" aria-hidden="true">
                {node.node_type === 'milestone' ? (
                  <Flag />
                ) : isComplete ? (
                  <CheckCircle2 />
                ) : isSelected ? (
                  <CircleDot />
                ) : (
                  <Circle />
                )}
                <b>{index + 1}</b>
              </span>
              <strong>{node.title}</strong>
              <small>
                {node.progress.tasks} 个任务
                <i aria-hidden="true">·</i>
                {progress}%
              </small>
              <span className="plan-stage-progress" aria-hidden="true">
                <i style={{ width: `${progress}%` }} />
              </span>
            </button>
          </li>
        )
      })}
    </ol>
  )
}
