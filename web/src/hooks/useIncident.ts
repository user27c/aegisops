import { useQuery } from '@tanstack/react-query'
import type { AIOpsIncident } from '../api/types'
import { apiFetch } from '../api/client'

/** 单事故详情查询（M1 接入后可用）。 */
export function useIncident(namespace: string, name: string) {
  return useQuery({
    queryKey: ['incident', namespace, name],
    queryFn: () => apiFetch<AIOpsIncident>(`/incidents/${namespace}/${name}`),
    enabled: Boolean(namespace && name),
  })
}
