import { api, APIError } from './client'

export type RoadmapNodeType = 'stage' | 'topic' | 'milestone'
export interface RoadmapNodeProgress {
  tasks: number
  total: number
  open: number
  active: number
  blocked: number
  done: number
  skipped: number
  cancelled: number
}
export interface RoadmapNodeV2 {
  id: string
  project_id: string
  roadmap_id: string
  parent_id?: string
  title: string
  description: string
  node_type: RoadmapNodeType
  position: number
  revision: number
  progress: RoadmapNodeProgress
}
export interface RoadmapEdgeV2 {
  id: string
  from_node_id: string
  to_node_id: string
  edge_type: 'prerequisite' | 'related' | 'suggested_order'
  revision: number
}
export interface RoadmapV2 {
  id: string
  project_id: string
  title: string
  description: string
  status: 'draft' | 'active' | 'completed' | 'failed' | 'archived'
  revision: number
  nodes: RoadmapNodeV2[]
  edges: RoadmapEdgeV2[]
}
export interface RoadmapNodeInput {
  parent_id?: string
  title: string
  description?: string
  node_type: RoadmapNodeType
  position?: number
}

export async function getRoadmapV2(projectID: string) {
  try {
    return (
      await api.get<{ roadmap: RoadmapV2 }>(
        `/api/projects/${encodeURIComponent(projectID)}/roadmap`
      )
    ).data.roadmap
  } catch (error) {
    if (error instanceof APIError && error.status === 404) return null
    throw error
  }
}
export async function createRoadmapV2(
  projectID: string,
  input: { title: string; description?: string }
) {
  return (
    await api.post<{ roadmap: RoadmapV2 }>(
      `/api/projects/${encodeURIComponent(projectID)}/roadmap`,
      input
    )
  ).data.roadmap
}
export async function createRoadmapNode(
  roadmapID: string,
  input: RoadmapNodeInput
) {
  return (
    await api.post<{ node: RoadmapNodeV2 }>(
      `/api/roadmaps/${encodeURIComponent(roadmapID)}/nodes`,
      input
    )
  ).data.node
}
export async function updateRoadmapNode(
  roadmapID: string,
  nodeID: string,
  input: RoadmapNodeInput & { expected_revision: number }
) {
  return (
    await api.patch<{ node: RoadmapNodeV2 }>(
      `/api/roadmaps/${encodeURIComponent(roadmapID)}/nodes/${encodeURIComponent(nodeID)}`,
      input
    )
  ).data.node
}
export function deleteRoadmapNode(
  roadmapID: string,
  nodeID: string,
  expectedRevision: number
) {
  return api.del(
    `/api/roadmaps/${encodeURIComponent(roadmapID)}/nodes/${encodeURIComponent(nodeID)}?expected_revision=${expectedRevision}`
  )
}
