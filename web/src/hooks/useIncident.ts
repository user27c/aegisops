import { useQuery } from '@tanstack/react-query'
import type { AIOpsIncident } from '../api/types'
import { apiFetch } from '../api/client'
import { isTerminal } from './useIncidents'

/** 单事故详情查询。非终态每 5 秒轮询。 */
export function useIncident(namespace: string, name: string) {
  return useQuery({
    queryKey: ['incident', namespace, name],
    queryFn: () => apiFetch<AIOpsIncident>(`/incidents/${namespace}/${name}`),
    enabled: Boolean(namespace && name),
    refetchInterval: (query) => {
      const data = query.state.data as AIOpsIncident | undefined
      if (!data) return 5000
      return isTerminal(data.status.phase) ? false : 5000
    },
  })
}
