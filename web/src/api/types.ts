/** 与 Incident API DTO 同步的 TypeScript 类型。M1 阶段补齐字段。 */

export type IncidentPhase =
  | "Detected"
  | "CollectingEvidence"
  | "Diagnosing"
  | "PolicyChecking"
  | "AwaitingApproval"
  | "Executing"
  | "Verifying"
  | "RollingBack"
  | "Resolved"
  | "RolledBack"
  | "Escalated";

export interface TargetReference {
  apiVersion: string;
  kind: string;
  namespace: string;
  name: string;
  uid?: string;
}

export interface TimelineEntry {
  time: string;
  type: string;
  reason?: string;
  message?: string;
  actor?: string;
  sequence?: number;
  eventHash?: string;
}

export interface TimelineResponse {
  items: TimelineEntry[];
  detailsUnavailable?: boolean;
  source?: "audit" | "cr";
}

export interface EvidenceItemDetail {
  id: string;
  kind: string;
  source?: string;
  timestamp?: string;
  summary?: string;
}

export interface EvidenceDetail {
  id?: string;
  hash?: string;
  schemaVersion?: string;
  windowStart?: string;
  windowEnd?: string;
  partial?: boolean;
  missingSources?: string[];
  redactions?: Array<Record<string, string>>;
  items?: EvidenceItemDetail[];
}

export interface EvidenceResponse extends EvidenceDetail {
  detailsUnavailable?: boolean;
}

export interface AIOpsIncident {
  metadata: {
    name: string;
    namespace: string;
    uid?: string;
    creationTimestamp?: string;
  };
  spec: {
    fingerprint: string;
    cluster: string;
    alertName: string;
    severity: string;
    sourceStatus: string;
    targetRef: TargetReference;
    startedAt: string;
    lastReceivedAt?: string;
    resolvedAt?: string;
    commonLabels?: Record<string, string>;
  };
  status: {
    phase: IncidentPhase;
    observedGeneration?: number;
    evidence?: { id: string; hash: string; counts?: Record<string, number> };
    diagnosis?: {
      category?: string;
      rootCause?: string;
      confidence?: number;
      evidenceIDs?: string[];
      runbookRefs?: string[];
    };
    proposal?: {
      revision: number;
      action: string;
      parameters?: Record<string, unknown>;
      risk?: string;
      planDigest?: string;
    };
    policyDecision?: { decision: string; reasonCodes?: string[] };
    approval?: { decision?: string; actor?: string };
    execution?: {
      reference?: {
        executionID?: string;
        operationID?: string;
        snapshotID?: string;
        startedAt?: string;
      };
      targetLock?: {
        leaseName?: string;
        holderIdentity?: string;
        acquiredAt?: string;
        renewTime?: string;
      };
      attempts?: number;
    };
    timeline?: TimelineEntry[];
  };
}

export interface IncidentPage {
  items: AIOpsIncident[];
  continueToken?: string;
  total?: number;
}
