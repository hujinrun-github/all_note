import { useQuery } from '@tanstack/react-query'
import { getTodayOverview } from '../api/today'

export function useTodayOverview() {
  return useQuery({
    queryKey: ['today'],
    queryFn: getTodayOverview,
  })
}
