import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import * as api from '../api/roadmapV2'

const keys = {
  roadmap: (projectID: string) =>
    ['task-domain', 'roadmap-v2', projectID] as const,
}
export function useRoadmapV2(projectID: string) {
  return useQuery({
    queryKey: keys.roadmap(projectID),
    queryFn: () => api.getRoadmapV2(projectID),
    enabled: projectID !== '',
  })
}
export function useCreateRoadmapMutation(projectID: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { title: string; description?: string }) =>
      api.createRoadmapV2(projectID, input),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: keys.roadmap(projectID) }),
  })
}
export function useCreateRoadmapNodeMutation(projectID: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({
      roadmapID,
      input,
    }: {
      roadmapID: string
      input: api.RoadmapNodeInput
    }) => api.createRoadmapNode(roadmapID, input),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: keys.roadmap(projectID) }),
  })
}
export function useUpdateRoadmapNodeMutation(projectID: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({
      roadmapID,
      nodeID,
      input,
    }: {
      roadmapID: string
      nodeID: string
      input: api.RoadmapNodeInput & { expected_revision: number }
    }) => api.updateRoadmapNode(roadmapID, nodeID, input),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: keys.roadmap(projectID) }),
  })
}
export function useDeleteRoadmapNodeMutation(projectID: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({
      roadmapID,
      nodeID,
      expectedRevision,
    }: {
      roadmapID: string
      nodeID: string
      expectedRevision: number
    }) => api.deleteRoadmapNode(roadmapID, nodeID, expectedRevision),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: keys.roadmap(projectID) }),
  })
}
