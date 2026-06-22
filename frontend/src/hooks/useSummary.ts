import { useQuery } from '@tanstack/react-query'
import { getSummary } from '../api/summary'

export function useSummary(from: string, to: string, page: number) {
  return useQuery({
    queryKey: ['summary', from, to, page],
    queryFn: () => getSummary(from, to, page),
    enabled: !!from && !!to,
  })
}
