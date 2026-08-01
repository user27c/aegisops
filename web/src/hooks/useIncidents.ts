import { useQuery } from '@tanstack/react-query'
import { listIncidents } from '../api/incidents'

/** Incident 列表查询。非终态 Incident 由轮询逻辑（M1）控制刷新。 */
export function useIncidents(filters: { namespace?: string; phase?: string; severity?: string } = {}) {
  return useQuery({
    queryKey: ['incidents', filters],
    queryFn: () => listIncidents(filters),
  })
}
