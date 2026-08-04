import type {
  EvidenceResponse,
  IncidentPage,
  TimelineResponse,
} from "../api/types";
import { apiFetch } from "../api/client";

/** 分页查询 Incident 列表。 */
export async function listIncidents(
  params: {
    namespace?: string;
    phase?: string;
    severity?: string;
    continueToken?: string;
  } = {},
): Promise<IncidentPage> {
  const query = new URLSearchParams();
  if (params.namespace) query.set("namespace", params.namespace);
  if (params.phase) query.set("phase", params.phase);
  if (params.severity) query.set("severity", params.severity);
  if (params.continueToken) query.set("continue", params.continueToken);
  const qs = query.toString();
  return apiFetch<IncidentPage>(`/incidents${qs ? `?${qs}` : ""}`);
}

/** 获取 Incident 时间线（审计源优先，降级 CR 时间线）。 */
export async function fetchIncidentTimeline(
  namespace: string,
  name: string,
): Promise<TimelineResponse> {
  return apiFetch<TimelineResponse>(
    `/incidents/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/timeline`,
  );
}

/** 获取 Incident 证据详情（诊断服务不可用时降级返回详情不可用）。 */
export async function fetchIncidentEvidence(
  namespace: string,
  name: string,
): Promise<EvidenceResponse> {
  return apiFetch<EvidenceResponse>(
    `/incidents/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/evidence`,
  );
}
