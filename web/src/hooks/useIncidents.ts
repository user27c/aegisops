import { useQuery } from '@tanstack/react-query'
import type { IncidentPage } from '../api/types'
import { listIncidents } from '../api/incidents'

/** Incident 列表查询。非终态 Incident 每 5 秒刷新，页面隐藏时暂停。 */
export function useIncidents(filters: { namespace?: string; phase?: string; severity?: string } = {}) {
  return useQuery({
    queryKey: ['incidents', filters],
    queryFn: () => listIncidents(filters),
    refetchInterval: (query) => {
      // 有非终态数据时 5 秒轮询。
      const data = query.state.data as IncidentPage | undefined
      if (!data) return 5000
      return data.items.some((i) => !isTerminal(i.status.phase)) ? 5000 : false
    },
  })
}

/** 判断阶段是否终态。 */
export function isTerminal(phase: string | undefined): boolean {
  return phase === 'Resolved' || phase === 'RolledBack' || phase === 'Escalated'
}
