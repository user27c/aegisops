import type { IncidentPage } from '../api/types'
import { apiFetch } from '../api/client'

/** 分页查询 Incident 列表。 */
export async function listIncidents(params: {
  namespace?: string
  phase?: string
  severity?: string
  continueToken?: string
} = {}): Promise<IncidentPage> {
  const query = new URLSearchParams()
  if (params.namespace) query.set('namespace', params.namespace)
  if (params.phase) query.set('phase', params.phase)
  if (params.severity) query.set('severity', params.severity)
  if (params.continueToken) query.set('continue', params.continueToken)
  const qs = query.toString()
  return apiFetch<IncidentPage>(`/incidents${qs ? `?${qs}` : ''}`)
}
