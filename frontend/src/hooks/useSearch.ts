import { useQuery } from '@tanstack/react-query'
import { search } from '../api/search'

export function useSearch(q: string, page?: number) {
  return useQuery({
    queryKey: ['search', q, page],
    queryFn: () => search(q, page),
    enabled: q.trim().length > 0,
  })
}
