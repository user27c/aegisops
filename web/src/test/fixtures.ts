import { http, HttpResponse } from "msw";
import type { AIOpsIncident, IncidentPage } from "../api/types";

/** 测试 fixtures：样本 Incident。 */
export const incidentOOM: AIOpsIncident = {
  metadata: {
    name: "containeroomkilled-abc123",
    namespace: "fault-lab",
    uid: "11111111-1111-1111-1111-111111111111",
    creationTimestamp: "2026-08-01T10:00:00Z",
  },
  spec: {
    fingerprint:
      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    cluster: "local-k3s",
    alertName: "ContainerOOMKilled",
    severity: "critical",
    sourceStatus: "firing",
    targetRef: {
      apiVersion: "apps/v1",
      kind: "Deployment",
      namespace: "fault-lab",
      name: "checkout-api",
    },
    startedAt: "2026-08-01T10:00:00Z",
  },
  status: {
    phase: "AwaitingApproval",
    diagnosis: {
      category: "OOMKilled",
      rootCause: "内存 limit 低于工作集",
      confidence: 0.91,
      evidenceIDs: ["event-1", "metric-2"],
    },
    proposal: {
      revision: 1,
      action: "PatchResourceLimit",
      risk: "medium",
      planDigest:
        "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    },
  },
};

export const incidentResolved: AIOpsIncident = {
  metadata: {
    name: "containercrashloop-def456",
    namespace: "fault-lab",
    uid: "22222222-2222-2222-2222-222222222222",
  },
  spec: {
    fingerprint:
      "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
    cluster: "local-k3s",
    alertName: "ContainerCrashLooping",
    severity: "warning",
    sourceStatus: "resolved",
    targetRef: {
      apiVersion: "apps/v1",
      kind: "Deployment",
      namespace: "fault-lab",
      name: "checkout-api",
    },
    startedAt: "2026-08-01T09:00:00Z",
  },
  status: { phase: "Resolved" },
};

/** MSW handlers：匹配 /api/v1/*。 */
export const handlers = [
  http.get("/api/v1/incidents", () => {
    const page: IncidentPage = { items: [incidentOOM, incidentResolved] };
    return HttpResponse.json(page);
  }),
  http.get("/api/v1/incidents/fault-lab/containeroomkilled-abc123", () => {
    return HttpResponse.json(incidentOOM);
  }),
  http.get(
    "/api/v1/incidents/fault-lab/containeroomkilled-abc123/timeline",
    () => {
      return HttpResponse.json({
        items: [
          {
            time: "2026-08-01T10:00:00Z",
            type: "PhaseTransition",
            reason: "Detected→CollectingEvidence",
            actor: "aegisops-operator",
            sequence: 1,
            eventHash: "1a2b3c4d5e6f",
          },
        ],
        source: "audit",
      });
    },
  ),
  http.get(
    "/api/v1/incidents/fault-lab/containeroomkilled-abc123/evidence",
    () => {
      return HttpResponse.json({
        id: "evidence-uuid-1",
        hash: "sha256:abcdefabcdefabcdefabcdefabcdef",
        windowStart: "2026-08-01T09:30:00Z",
        windowEnd: "2026-08-01T10:00:00Z",
        partial: false,
        items: [
          {
            id: "event-1",
            kind: "KubernetesEvent",
            source: "k8s",
            timestamp: "2026-08-01T09:55:00Z",
            summary: "ContainerOOMKilled 容器 checkout-api 内存超限",
          },
        ],
      });
    },
  ),
  http.get("/api/v1/incidents/*", () => {
    return HttpResponse.json(
      { code: "NOT_FOUND", message: "事故不存在" },
      { status: 404 },
    );
  }),
];
