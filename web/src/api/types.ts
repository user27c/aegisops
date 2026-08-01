/** 与 Incident API DTO 同步的 TypeScript 类型。M1 阶段补齐字段。 */

export type IncidentPhase =
  | 'Detected'
  | 'CollectingEvidence'
  | 'Diagnosing'
  | 'PolicyChecking'
  | 'AwaitingApproval'
  | 'Executing'
  | 'Verifying'
  | 'RollingBack'
  | 'Resolved'
  | 'RolledBack'
  | 'Escalated'

export interface TargetReference {
  apiVersion: string
  kind: string
  namespace: string
  name: string
  uid?: string
}

export interface AIOpsIncident {
  metadata: {
    name: string
    namespace: string
    uid?: string
    creationTimestamp?: string
  }
  spec: {
    fingerprint: string
    cluster: string
    alertName: string
    severity: string
    sourceStatus: string
    targetRef: TargetReference
    startedAt: string
    commonLabels?: Record<string, string>
  }
  status: {
    phase: IncidentPhase
    observedGeneration?: number
    evidence?: { id: string; hash: string; counts?: Record<string, number> }
    diagnosis?: {
      category?: string
      rootCause?: string
      confidence?: number
      evidenceIDs?: string[]
      runbookRefs?: string[]
    }
    proposal?: {
      revision: number
      action: string
      parameters?: Record<string, unknown>
      risk?: string
      planDigest?: string
    }
    policyDecision?: { decision: string; reasonCodes?: string[] }
    approval?: { decision?: string; actor?: string }
    timeline?: Array<{ time: string; type: string; reason?: string; message?: string }>
  }
}

export interface IncidentPage {
  items: AIOpsIncident[]
  continueToken?: string
  total?: number
}
